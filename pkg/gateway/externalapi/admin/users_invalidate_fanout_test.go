// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// spec: §11.4 full_revoke — the pod-RPC Terminate fan-out, the §4.9
// credential-lease revocation, and the §13.3 cached-auth invalidation.

// fakePodTerminator records the §11.4 pod-RPC Terminate fan-out and
// reports a configurable outcome. It satisfies admin.UserPodTerminator.
type fakePodTerminator struct {
	calls       int
	gotTenant   string
	gotUser     string
	gotSessions []string
	result      admin.UserTerminationResult
}

func (f *fakePodTerminator) TerminateUserSessions(_ context.Context, tenantID, userID string, sessionIDs []string) admin.UserTerminationResult {
	f.calls++
	f.gotTenant, f.gotUser = tenantID, userID
	f.gotSessions = append([]string(nil), sessionIDs...)
	return f.result
}

// fakeLeaseRevoker records the §11.4 credential-lease revocation and
// reports a configurable count. It satisfies admin.UserLeaseRevoker.
type fakeLeaseRevoker struct {
	calls       int
	gotSessions []string
	revoked     int
}

func (f *fakeLeaseRevoker) RevokeUserLeases(_, _ string, sessionIDs []string) int {
	f.calls++
	f.gotSessions = append([]string(nil), sessionIDs...)
	return f.revoked
}

// fakeUserTokenRevoker records the §11.4 cached-auth invalidation and
// reports a configurable JTI set or error. It satisfies
// admin.UserTokenRevoker.
type fakeUserTokenRevoker struct {
	calls     int
	gotTenant string
	gotSub    string
	gotReason string
	jtis      []string
	err       error
}

func (f *fakeUserTokenRevoker) RevokeUserTokens(_ context.Context, tenantID, subject, reason string, _ time.Time) ([]string, error) {
	f.calls++
	f.gotTenant, f.gotSub, f.gotReason = tenantID, subject, reason
	return f.jtis, f.err
}

// fakePlaygroundRevoker records the §11.4 → §27.6 playground revocation
// fan-out and reports a configurable count or error. It satisfies
// admin.UserPlaygroundRevoker.
type fakePlaygroundRevoker struct {
	calls     int
	gotTenant string
	gotUser   string
	revoked   int
	err       error
}

func (f *fakePlaygroundRevoker) RevokeSessionsForUser(_ context.Context, tenantID, userID string) (int, error) {
	f.calls++
	f.gotTenant, f.gotUser = tenantID, userID
	return f.revoked, f.err
}

// newFullRevokeAdmin builds an admin router with the §11.4 full_revoke
// fan-out dependencies and a recording revocation cache wired.
func newFullRevokeAdmin(t *testing.T, pods admin.UserPodTerminator, leases admin.UserLeaseRevoker, tokens admin.UserTokenRevoker) (*admin.Router, userstore.Store, sessionstore.Store, *fakeRevCache) {
	t.Helper()
	users := userstore.NewMemory()
	sessions := memstore.New()
	cache := &fakeRevCache{}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).
		WithUsers(users).
		WithSessions(sessions).
		WithInteractions(interactionstore.NewMemory()).
		WithIssuedTokens(&fakeTokenRevoker{}, cache).
		WithUserRevocation(pods, leases, tokens)
	return router, users, sessions, cache
}

func TestFullRevokeFansOutToPods(t *testing.T) {
	pods := &fakePodTerminator{result: admin.UserTerminationResult{PodsTerminated: 2}}
	router, users, sessions, _ := newFullRevokeAdmin(t, pods, nil, nil)
	seedUser(t, users, "acme", "alice@acme.com")
	seedSession(t, sessions, sessionstore.Session{
		ID: "run_1", TenantID: "acme", UserID: "alice@acme.com", State: session.StateRunning,
	})
	seedSession(t, sessions, sessionstore.Session{
		ID: "run_2", TenantID: "acme", UserID: "alice@acme.com", State: session.StateAwaitingClientAction,
	})

	rr := invalidateUser(t, router.Handler(), "alice@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateFullRevoke, Reason: "ticket-9"},
		withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("full_revoke: status %d, body %s", rr.Code, rr.Body.String())
	}
	if pods.calls != 1 {
		t.Fatalf("pod terminator called %d times, want 1", pods.calls)
	}
	if pods.gotTenant != "acme" || pods.gotUser != "alice@acme.com" {
		t.Errorf("pod terminator got tenant=%q user=%q", pods.gotTenant, pods.gotUser)
	}
	sort.Strings(pods.gotSessions)
	if len(pods.gotSessions) != 2 || pods.gotSessions[0] != "run_1" || pods.gotSessions[1] != "run_2" {
		t.Errorf("pod terminator got sessions %v, want [run_1 run_2]", pods.gotSessions)
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["podsTerminated"] != float64(2) {
		t.Errorf("podsTerminated = %v, want 2", resp["podsTerminated"])
	}
}

func TestFullRevokeRecordsPodTerminationFailures(t *testing.T) {
	// One pod fails to terminate; full_revoke records it and still
	// succeeds — the failure must not abort the rest of the revocation.
	pods := &fakePodTerminator{result: admin.UserTerminationResult{
		PodsTerminated: 1,
		FailedSessions: []string{"run_2"},
	}}
	leases := &fakeLeaseRevoker{revoked: 3}
	router, users, sessions, _ := newFullRevokeAdmin(t, pods, leases, nil)
	seedUser(t, users, "acme", "alice@acme.com")
	seedSession(t, sessions, sessionstore.Session{
		ID: "run_1", TenantID: "acme", UserID: "alice@acme.com", State: session.StateRunning,
	})
	seedSession(t, sessions, sessionstore.Session{
		ID: "run_2", TenantID: "acme", UserID: "alice@acme.com", State: session.StateRunning,
	})

	rr := invalidateUser(t, router.Handler(), "alice@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateFullRevoke}, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("full_revoke with a pod failure: status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["podsTerminated"] != float64(1) {
		t.Errorf("podsTerminated = %v, want 1", resp["podsTerminated"])
	}
	failed, _ := resp["podTerminationFailures"].([]any)
	if len(failed) != 1 || failed[0] != "run_2" {
		t.Errorf("podTerminationFailures = %v, want [run_2]", resp["podTerminationFailures"])
	}
	// A pod failure does not stop lease revocation.
	if leases.calls != 1 {
		t.Errorf("lease revoker called %d times after a pod failure, want 1", leases.calls)
	}
	if resp["leasesRevoked"] != float64(3) {
		t.Errorf("leasesRevoked = %v, want 3", resp["leasesRevoked"])
	}
}

func TestFullRevokeRevokesCredentialLeases(t *testing.T) {
	leases := &fakeLeaseRevoker{revoked: 2}
	router, users, sessions, _ := newFullRevokeAdmin(t, nil, leases, nil)
	seedUser(t, users, "acme", "bob@acme.com")
	seedSession(t, sessions, sessionstore.Session{
		ID: "run_1", TenantID: "acme", UserID: "bob@acme.com", State: session.StateRunning,
	})

	rr := invalidateUser(t, router.Handler(), "bob@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateFullRevoke}, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("full_revoke: status %d, body %s", rr.Code, rr.Body.String())
	}
	if leases.calls != 1 {
		t.Fatalf("lease revoker called %d times, want 1", leases.calls)
	}
	if len(leases.gotSessions) != 1 || leases.gotSessions[0] != "run_1" {
		t.Errorf("lease revoker got sessions %v, want [run_1]", leases.gotSessions)
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["leasesRevoked"] != float64(2) {
		t.Errorf("leasesRevoked = %v, want 2", resp["leasesRevoked"])
	}
}

func TestFullRevokeInvalidatesCachedAuth(t *testing.T) {
	tokens := &fakeUserTokenRevoker{jtis: []string{"jti-a", "jti-b", "jti-c"}}
	router, users, _, cache := newFullRevokeAdmin(t, nil, nil, tokens)
	seedUser(t, users, "acme", "carol@acme.com")

	rr := invalidateUser(t, router.Handler(), "carol@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateFullRevoke, Reason: "compromised"},
		withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("full_revoke: status %d, body %s", rr.Code, rr.Body.String())
	}
	if tokens.calls != 1 {
		t.Fatalf("token revoker called %d times, want 1", tokens.calls)
	}
	if tokens.gotTenant != "acme" || tokens.gotSub != "carol@acme.com" {
		t.Errorf("token revoker got tenant=%q sub=%q", tokens.gotTenant, tokens.gotSub)
	}
	if tokens.gotReason != "compromised" {
		t.Errorf("token revoker got reason=%q, want compromised", tokens.gotReason)
	}
	// Each revoked JTI is pushed into the revocation cache so the user's
	// cached auth is rejected on the next request.
	sort.Strings(cache.revoked)
	want := []string{"jti-a", "jti-b", "jti-c"}
	if len(cache.revoked) != 3 || cache.revoked[0] != want[0] ||
		cache.revoked[1] != want[1] || cache.revoked[2] != want[2] {
		t.Errorf("revocation cache holds %v, want %v", cache.revoked, want)
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["tokensRevoked"] != float64(3) {
		t.Errorf("tokensRevoked = %v, want 3", resp["tokensRevoked"])
	}
}

func TestFullRevokeInvalidatesCachedAuthWithNoActiveSessions(t *testing.T) {
	// A user with valid issued tokens but no running sessions must still
	// have their cached auth invalidated, so an existing token cannot
	// start a new session after the revoke.
	tokens := &fakeUserTokenRevoker{jtis: []string{"jti-x"}}
	router, users, _, cache := newFullRevokeAdmin(t, nil, nil, tokens)
	seedUser(t, users, "acme", "dave@acme.com")

	rr := invalidateUser(t, router.Handler(), "dave@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateFullRevoke}, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("full_revoke: status %d", rr.Code)
	}
	if tokens.calls != 1 {
		t.Errorf("token revoker called %d times for a user with no sessions, want 1", tokens.calls)
	}
	if len(cache.revoked) != 1 || cache.revoked[0] != "jti-x" {
		t.Errorf("revocation cache holds %v, want [jti-x]", cache.revoked)
	}
}

func TestFullRevokeRecordsTokenRevocationError(t *testing.T) {
	// A failure to reach the issued-token index is recorded; the rest of
	// full_revoke still completes and the request still succeeds.
	tokens := &fakeUserTokenRevoker{err: errors.New("issued-token index unreachable")}
	leases := &fakeLeaseRevoker{revoked: 1}
	router, users, sessions, cache := newFullRevokeAdmin(t, nil, leases, tokens)
	seedUser(t, users, "acme", "erin@acme.com")
	seedSession(t, sessions, sessionstore.Session{
		ID: "run_1", TenantID: "acme", UserID: "erin@acme.com", State: session.StateRunning,
	})

	rr := invalidateUser(t, router.Handler(), "erin@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateFullRevoke}, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("full_revoke with a token-index failure: status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["tokenRevocationError"] == nil {
		t.Error("tokenRevocationError not reported on a token-index failure")
	}
	if resp["tokensRevoked"] != float64(0) {
		t.Errorf("tokensRevoked = %v, want 0 on a failure", resp["tokensRevoked"])
	}
	if len(cache.revoked) != 0 {
		t.Errorf("the revocation cache was written despite the token-index failure: %v", cache.revoked)
	}
	// The lease revocation still ran.
	if leases.calls != 1 || resp["leasesRevoked"] != float64(1) {
		t.Errorf("lease revocation did not run after a token-index failure: calls=%d body=%v",
			leases.calls, resp["leasesRevoked"])
	}
	// The user is still tombstoned.
	got, _ := users.Get(context.Background(), "acme", "erin@acme.com")
	if !got.Disabled || got.DeletedAt.IsZero() {
		t.Error("full_revoke must tombstone the user even when a fan-out step fails")
	}
}

func TestSoftDisableSkipsFanOut(t *testing.T) {
	pods := &fakePodTerminator{}
	leases := &fakeLeaseRevoker{}
	tokens := &fakeUserTokenRevoker{jtis: []string{"jti-1"}}
	router, users, sessions, cache := newFullRevokeAdmin(t, pods, leases, tokens)
	seedUser(t, users, "acme", "frank@acme.com")
	seedSession(t, sessions, sessionstore.Session{
		ID: "run_1", TenantID: "acme", UserID: "frank@acme.com", State: session.StateRunning,
	})

	rr := invalidateUser(t, router.Handler(), "frank@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateSoftDisable}, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("soft_disable: status %d", rr.Code)
	}
	if pods.calls != 0 || leases.calls != 0 || tokens.calls != 0 {
		t.Errorf("soft_disable triggered a fan-out: pods=%d leases=%d tokens=%d",
			pods.calls, leases.calls, tokens.calls)
	}
	if len(cache.revoked) != 0 {
		t.Errorf("soft_disable wrote the revocation cache: %v", cache.revoked)
	}
}

func TestHardDisableSkipsFanOut(t *testing.T) {
	pods := &fakePodTerminator{}
	leases := &fakeLeaseRevoker{}
	tokens := &fakeUserTokenRevoker{jtis: []string{"jti-1"}}
	router, users, sessions, cache := newFullRevokeAdmin(t, pods, leases, tokens)
	seedUser(t, users, "acme", "grace@acme.com")
	seedSession(t, sessions, sessionstore.Session{
		ID: "run_1", TenantID: "acme", UserID: "grace@acme.com", State: session.StateRunning,
	})

	rr := invalidateUser(t, router.Handler(), "grace@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateHardDisable}, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("hard_disable: status %d", rr.Code)
	}
	if pods.calls != 0 || leases.calls != 0 || tokens.calls != 0 {
		t.Errorf("hard_disable triggered a fan-out: pods=%d leases=%d tokens=%d",
			pods.calls, leases.calls, tokens.calls)
	}
	if len(cache.revoked) != 0 {
		t.Errorf("hard_disable wrote the revocation cache: %v", cache.revoked)
	}
	// hard_disable still tombstones the user and leaves the session alone.
	got, _ := users.Get(context.Background(), "acme", "grace@acme.com")
	if !got.Disabled || got.DeletedAt.IsZero() {
		t.Error("hard_disable must set disabled and the tombstone")
	}
	s, _ := sessions.Get(context.Background(), "acme", "run_1")
	if s.State != session.StateRunning {
		t.Errorf("hard_disable cancelled a session: state %q, want running", s.State)
	}
}

func TestFullRevokeOnlyFansOutLiveSessions(t *testing.T) {
	// A terminal session's pod is already gone and its leases already
	// released; only the user's non-terminal sessions reach the fan-out.
	pods := &fakePodTerminator{}
	leases := &fakeLeaseRevoker{}
	router, users, sessions, _ := newFullRevokeAdmin(t, pods, leases, nil)
	seedUser(t, users, "acme", "heidi@acme.com")
	seedSession(t, sessions, sessionstore.Session{
		ID: "live", TenantID: "acme", UserID: "heidi@acme.com", State: session.StateRunning,
	})
	seedSession(t, sessions, sessionstore.Session{
		ID: "done", TenantID: "acme", UserID: "heidi@acme.com", State: session.StateCompleted,
	})

	rr := invalidateUser(t, router.Handler(), "heidi@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateFullRevoke}, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("full_revoke: status %d", rr.Code)
	}
	if len(pods.gotSessions) != 1 || pods.gotSessions[0] != "live" {
		t.Errorf("pod terminator got sessions %v, want only [live]", pods.gotSessions)
	}
	if len(leases.gotSessions) != 1 || leases.gotSessions[0] != "live" {
		t.Errorf("lease revoker got sessions %v, want only [live]", leases.gotSessions)
	}
}

func TestFullRevokeWithNoLiveSessionsSkipsPodAndLeaseFanOut(t *testing.T) {
	// With no non-terminal sessions there is nothing to terminate and no
	// leases to revoke; the pod and lease steps are skipped entirely.
	pods := &fakePodTerminator{}
	leases := &fakeLeaseRevoker{}
	router, users, _, _ := newFullRevokeAdmin(t, pods, leases, nil)
	seedUser(t, users, "acme", "ivan@acme.com")

	rr := invalidateUser(t, router.Handler(), "ivan@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateFullRevoke}, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("full_revoke: status %d", rr.Code)
	}
	if pods.calls != 0 {
		t.Errorf("pod terminator called %d times with no live sessions, want 0", pods.calls)
	}
	if leases.calls != 0 {
		t.Errorf("lease revoker called %d times with no live sessions, want 0", leases.calls)
	}
}

func TestFullRevokeWithoutFanOutDepsStillSucceeds(t *testing.T) {
	// A minimal gateway wires none of the §11.4 fan-out dependencies. A
	// full_revoke must still tombstone the user and cancel their
	// sessions through the SessionStore alone.
	users := userstore.NewMemory()
	sessions := memstore.New()
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithUsers(users).WithSessions(sessions)
	seedUser(t, users, "acme", "judy@acme.com")
	seedSession(t, sessions, sessionstore.Session{
		ID: "run_1", TenantID: "acme", UserID: "judy@acme.com", State: session.StateRunning,
	})

	rr := invalidateUser(t, router.Handler(), "judy@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateFullRevoke}, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("full_revoke on a minimal router: status %d, body %s", rr.Code, rr.Body.String())
	}
	got, _ := users.Get(context.Background(), "acme", "judy@acme.com")
	if !got.Disabled || got.DeletedAt.IsZero() {
		t.Error("full_revoke must tombstone the user with no fan-out deps wired")
	}
	s, _ := sessions.Get(context.Background(), "acme", "run_1")
	if s.State != session.StateCancelled {
		t.Errorf("full_revoke must cancel the session: state %q, want cancelled", s.State)
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["sessionsTerminated"] != float64(1) {
		t.Errorf("sessionsTerminated = %v, want 1", resp["sessionsTerminated"])
	}
	if resp["podsTerminated"] != float64(0) || resp["leasesRevoked"] != float64(0) ||
		resp["tokensRevoked"] != float64(0) {
		t.Errorf("a minimal router reported non-zero fan-out counts: %v", resp)
	}
}

// spec: §27.6 line 204 — user.invalidated drives the §27 playground
// revocation primitive for every session the user holds. F-27.6.4,
// F-27.3.2.
func TestFullRevokeRevokesPlaygroundSessions(t *testing.T) {
	pg := &fakePlaygroundRevoker{revoked: 2}
	users := userstore.NewMemory()
	sessions := memstore.New()
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithUsers(users).WithSessions(sessions).WithPlaygroundRevocation(pg)
	seedUser(t, users, "acme", "alice@acme.com")

	rr := invalidateUser(t, router.Handler(), "alice@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateFullRevoke}, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("full_revoke: status %d, body %s", rr.Code, rr.Body.String())
	}
	if pg.calls != 1 || pg.gotTenant != "acme" || pg.gotUser != "alice@acme.com" {
		t.Fatalf("playground revoker calls=%d tenant=%q user=%q, want 1/acme/alice@acme.com",
			pg.calls, pg.gotTenant, pg.gotUser)
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["playgroundSessionsRevoked"] != float64(2) {
		t.Errorf("playgroundSessionsRevoked = %v, want 2", resp["playgroundSessionsRevoked"])
	}
}

func TestFullRevokeRecordsPlaygroundRevokeError(t *testing.T) {
	// A failure to reach the playground store is recorded; the rest of
	// full_revoke still completes and the request still succeeds.
	pg := &fakePlaygroundRevoker{err: errors.New("playground store unreachable")}
	leases := &fakeLeaseRevoker{revoked: 1}
	users := userstore.NewMemory()
	sessions := memstore.New()
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithUsers(users).WithSessions(sessions).
		WithUserRevocation(nil, leases, nil).
		WithPlaygroundRevocation(pg)
	seedUser(t, users, "acme", "bob@acme.com")
	seedSession(t, sessions, sessionstore.Session{
		ID: "run_1", TenantID: "acme", UserID: "bob@acme.com", State: session.StateRunning,
	})

	rr := invalidateUser(t, router.Handler(), "bob@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateFullRevoke}, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("full_revoke with a playground failure: status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["playgroundRevocationError"] == nil {
		t.Error("playgroundRevocationError not reported on a playground-store failure")
	}
	if resp["playgroundSessionsRevoked"] != float64(0) {
		t.Errorf("playgroundSessionsRevoked = %v, want 0 on a failure", resp["playgroundSessionsRevoked"])
	}
	// A playground failure must not abort the rest of full_revoke.
	if leases.calls != 1 || resp["leasesRevoked"] != float64(1) {
		t.Errorf("lease revocation did not run after a playground failure: calls=%d body=%v",
			leases.calls, resp["leasesRevoked"])
	}
	got, _ := users.Get(context.Background(), "acme", "bob@acme.com")
	if !got.Disabled || got.DeletedAt.IsZero() {
		t.Error("full_revoke must tombstone the user even when the playground step fails")
	}
}

func TestSoftDisableSkipsPlaygroundRevocation(t *testing.T) {
	pg := &fakePlaygroundRevoker{revoked: 1}
	users := userstore.NewMemory()
	sessions := memstore.New()
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithUsers(users).WithSessions(sessions).WithPlaygroundRevocation(pg)
	seedUser(t, users, "acme", "carol@acme.com")

	rr := invalidateUser(t, router.Handler(), "carol@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateSoftDisable}, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("soft_disable: status %d", rr.Code)
	}
	if pg.calls != 0 {
		t.Errorf("soft_disable drove the playground revocation %d times, want 0", pg.calls)
	}
}

func TestFullRevokeAuditEventRecordsFanOut(t *testing.T) {
	pods := &fakePodTerminator{result: admin.UserTerminationResult{
		PodsTerminated: 1,
		FailedSessions: []string{"run_2"},
	}}
	leases := &fakeLeaseRevoker{revoked: 4}
	tokens := &fakeUserTokenRevoker{jtis: []string{"jti-1", "jti-2"}}
	users := userstore.NewMemory()
	sessions := memstore.New()
	audit := &recordingAudit{}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).
		WithUsers(users).
		WithSessions(sessions).
		WithIssuedTokens(&fakeTokenRevoker{}, &fakeRevCache{}).
		WithUserRevocation(pods, leases, tokens)
	seedUser(t, users, "acme", "ken@acme.com")
	seedSession(t, sessions, sessionstore.Session{
		ID: "run_1", TenantID: "acme", UserID: "ken@acme.com", State: session.StateRunning,
	})
	seedSession(t, sessions, sessionstore.Session{
		ID: "run_2", TenantID: "acme", UserID: "ken@acme.com", State: session.StateRunning,
	})

	rr := invalidateUser(t, router.Handler(), "ken@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateFullRevoke}, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("full_revoke: status %d", rr.Code)
	}
	snap := audit.snapshot()
	if len(snap) != 1 || snap[0].Type != "admin.user.invalidated" {
		t.Fatalf("audit events: %+v", snap)
	}
	d := snap[0].Detail
	if d["podsTerminated"] != 1 {
		t.Errorf("audit podsTerminated = %v, want 1", d["podsTerminated"])
	}
	if d["leasesRevoked"] != 4 {
		t.Errorf("audit leasesRevoked = %v, want 4", d["leasesRevoked"])
	}
	if d["tokensRevoked"] != 2 {
		t.Errorf("audit tokensRevoked = %v, want 2", d["tokensRevoked"])
	}
	failed, _ := d["podTerminationFailures"].([]string)
	if len(failed) != 1 || failed[0] != "run_2" {
		t.Errorf("audit podTerminationFailures = %v, want [run_2]", d["podTerminationFailures"])
	}
}
