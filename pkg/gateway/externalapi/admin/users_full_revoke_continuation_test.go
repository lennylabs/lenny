// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
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

// The full_revoke continuation tests drive the production handler with
// stores that behave as the Postgres ones do where the tier-1 memory stores
// do not: a call on an ended context fails, and a row committed just before
// the call is visible only on the primary.

// continuationSessions wraps the memory session store. Every call fails once
// its context has ended, as a pgx call does. Rows named in primaryOnly are
// missing from a listing that does not read the primary. listErr fails every
// listing; failID fails that row's cancel write, after applying it when
// commitThenFail is set; rereadErr fails the primary re-read.
type continuationSessions struct {
	sessionstore.Store
	primaryOnly    map[string]bool
	listErr        error
	failID         string
	commitThenFail bool
	rereadErr      error
	rereads        []string
}

func (s *continuationSessions) List(ctx context.Context, tenantID string, f sessionstore.ListFilter) ([]sessionstore.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.listErr != nil {
		return nil, s.listErr
	}
	rows, err := s.Store.List(ctx, tenantID, f)
	if err != nil || f.Primary {
		return rows, err
	}
	var lagging []sessionstore.Session
	for _, row := range rows {
		if !s.primaryOnly[row.ID] {
			lagging = append(lagging, row)
		}
	}
	return lagging, nil
}

func (s *continuationSessions) Update(ctx context.Context, tenantID, id string, mut func(*sessionstore.Session) error) (sessionstore.Session, error) {
	if err := ctx.Err(); err != nil {
		return sessionstore.Session{}, err
	}
	if id == s.failID {
		if s.commitThenFail {
			if _, err := s.Store.Update(ctx, tenantID, id, mut); err != nil {
				return sessionstore.Session{}, err
			}
		}
		return sessionstore.Session{}, errors.New("pgtenant: commit: connection reset")
	}
	return s.Store.Update(ctx, tenantID, id, mut)
}

func (s *continuationSessions) GetPrimary(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	s.rereads = append(s.rereads, id)
	if err := ctx.Err(); err != nil {
		return sessionstore.Session{}, err
	}
	if s.rereadErr != nil {
		return sessionstore.Session{}, s.rereadErr
	}
	return s.Get(ctx, tenantID, id)
}

// cancellingUsers ends the request context right after the user row's
// update commits, as a client that disconnects at that point does.
type cancellingUsers struct {
	userstore.Store
	cancel context.CancelFunc
}

func (u *cancellingUsers) Update(ctx context.Context, tenantID, subject string, mut func(*userstore.User) error) (userstore.User, error) {
	out, err := u.Store.Update(ctx, tenantID, subject, mut)
	if u.cancel != nil {
		u.cancel()
	}
	return out, err
}

// continuationInteractions fails DismissByUser with err after counting
// dismissed, and fails it on an ended context.
type continuationInteractions struct {
	interactionstore.Store
	dismissed int
	err       error
	calls     int
}

func (i *continuationInteractions) DismissByUser(ctx context.Context, _, _ string) (int, error) {
	i.calls++
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return i.dismissed, i.err
}

// strictTokenRevoker revokes jtis unless its context has ended.
type strictTokenRevoker struct {
	jtis  []string
	calls int
}

func (s *strictTokenRevoker) RevokeUserTokens(ctx context.Context, _, _, _ string, _ time.Time, _ bool) ([]string, error) {
	s.calls++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.jtis, nil
}

// continuationFixture is one full_revoke router over the continuation fakes,
// with alice's sessions seeded.
type continuationFixture struct {
	router       *admin.Router
	sessions     *continuationSessions
	users        *cancellingUsers
	interactions *continuationInteractions
	tokens       *strictTokenRevoker
	playground   *fakePlaygroundRevoker
	leases       *fakeLeaseRevoker
	pods         *fakePodTerminator
	hook         *recordingRevokeHook
	cache        *fakeRevCache
	audit        *recordingAudit
}

func newContinuationFixture(t *testing.T, rows ...sessionstore.Session) *continuationFixture {
	t.Helper()
	inner := memstore.New()
	f := &continuationFixture{
		sessions:     &continuationSessions{Store: inner, primaryOnly: map[string]bool{}},
		users:        &cancellingUsers{Store: userstore.NewMemory()},
		interactions: &continuationInteractions{Store: interactionstore.NewMemory()},
		tokens:       &strictTokenRevoker{jtis: []string{"jti-1", "jti-2"}},
		playground:   &fakePlaygroundRevoker{revoked: 1},
		leases:       &fakeLeaseRevoker{},
		pods:         &fakePodTerminator{},
		hook:         newRecordingRevokeHook(nil),
		cache:        &fakeRevCache{},
		audit:        &recordingAudit{},
	}
	f.router = admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: f.audit,
	}).
		WithUsers(f.users).
		WithSessions(f.sessions).
		WithInteractions(f.interactions).
		WithIssuedTokens(&fakeTokenRevoker{}, f.cache).
		WithUserRevocation(f.pods, f.leases, f.tokens).
		WithPlaygroundRevocation(f.playground).
		WithRevokeHook(f.hook)
	seedUser(t, f.users.Store, "acme", "alice@acme.com")
	for _, row := range rows {
		row.TenantID, row.UserID = "acme", "alice@acme.com"
		seedSession(t, inner, row)
	}
	f.leases.revoked = len(rows)
	return f
}

// revoke runs full_revoke for alice on a request whose context the users
// store ends after its update when disconnect is set.
func (f *continuationFixture) revoke(t *testing.T, disconnect bool) (int, map[string]any) {
	t.Helper()
	as := withAdminPrincipal
	if disconnect {
		as = func(req *http.Request) *http.Request {
			ctx, cancel := context.WithCancel(req.Context())
			f.users.cancel = cancel
			return withAdminPrincipal(req.WithContext(ctx))
		}
	}
	rr := invalidateUser(t, f.router.Handler(), "alice@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateFullRevoke, Reason: "ticket-9"}, as)
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rr.Body.String(), err)
	}
	return rr.Code, body
}

// state returns the stored state of id.
func (f *continuationFixture) state(t *testing.T, id string) session.State {
	t.Helper()
	row, err := f.sessions.Get(context.Background(), "acme", id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return row.State
}

// invalidatedEvent returns the detail of the one admin.user.invalidated
// audit event.
func (f *continuationFixture) invalidatedEvent(t *testing.T) map[string]any {
	t.Helper()
	var found []map[string]any
	for _, ev := range f.audit.snapshot() {
		if ev.Type == "admin.user.invalidated" {
			found = append(found, ev.Detail)
		}
	}
	if len(found) != 1 {
		t.Fatalf("admin.user.invalidated events = %d, want 1", len(found))
	}
	return found[0]
}

// requireTokensRevoked asserts the user's tokens were revoked in the index
// and pushed into the revocation cache, and the playground step ran.
func (f *continuationFixture) requireTokensRevoked(t *testing.T, counts map[string]any) {
	t.Helper()
	if strings.Join(f.cache.revoked, ",") != "jti-1,jti-2" {
		t.Errorf("revocation cache = %v, want [jti-1 jti-2]", f.cache.revoked)
	}
	if counts["tokensRevoked"] != float64(2) {
		t.Errorf("tokensRevoked = %v, want 2", counts["tokensRevoked"])
	}
	if _, ok := counts["tokenRevocationError"]; ok {
		t.Errorf("tokenRevocationError = %v, want none", counts["tokenRevocationError"])
	}
	if f.playground.calls != 1 || counts["playgroundSessionsRevoked"] != float64(1) {
		t.Errorf("playground calls = %d, playgroundSessionsRevoked = %v; want 1 and 1",
			f.playground.calls, counts["playgroundSessionsRevoked"])
	}
}

// TestFullRevokeCancelsEveryNonTerminalStateAndReturnsEveryLease_spec_11_4
// asserts the full_revoke matrix over session states: each non-terminal
// state is cancelled and counted, each terminal state is left as it is, and
// the lease step receives every listed session in either case. The tokens
// are revoked and the audit event carries the same counts as the response.
//
// spec: §11.4 (User Invalidation: Full revoke propagation mechanism)
func TestFullRevokeCancelsEveryNonTerminalStateAndReturnsEveryLease_spec_11_4(t *testing.T) {
	var rows []sessionstore.Session
	var live, all []string
	for _, st := range session.AllStates() {
		id := "s_" + string(st)
		rows = append(rows, sessionstore.Session{ID: id, State: st})
		all = append(all, id)
		if !session.IsTerminal(st) {
			live = append(live, id)
		}
	}
	f := newContinuationFixture(t, rows...)
	code, resp := f.revoke(t, false)
	if code != http.StatusOK {
		t.Fatalf("full_revoke: status %d, body %v", code, resp)
	}
	for _, st := range session.AllStates() {
		want := st
		if !session.IsTerminal(st) {
			want = session.StateCancelled
		}
		if got := f.state(t, "s_"+string(st)); got != want {
			t.Errorf("session in %s ended %s, want %s", st, got, want)
		}
	}
	if got := sortedCopy(f.leases.gotSessions); strings.Join(got, ",") != strings.Join(sortedCopy(all), ",") {
		t.Errorf("lease step sessions = %v, want every listed session %v", got, sortedCopy(all))
	}
	if got := sortedCopy(f.pods.gotSessions); strings.Join(got, ",") != strings.Join(sortedCopy(live), ",") {
		t.Errorf("binding release sessions = %v, want the cancelled set %v", got, sortedCopy(live))
	}
	if resp["sessionsTerminated"] != float64(len(live)) || resp["leasesRevoked"] != float64(len(all)) {
		t.Errorf("response = %v, want sessionsTerminated %d and leasesRevoked %d", resp, len(live), len(all))
	}
	if _, ok := resp["failedSessions"]; ok {
		t.Errorf("failedSessions = %v on a clean revoke, want absent", resp["failedSessions"])
	}
	f.requireTokensRevoked(t, resp)
	ev := f.invalidatedEvent(t)
	if ev["sessionsTerminated"] != len(live) || ev["leasesRevoked"] != len(all) || ev["mode"] != admin.InvalidateFullRevoke {
		t.Errorf("audit detail = %v, want the response's counts", ev)
	}
}

// TestFullRevokeReportsAFailedCancelWriteAndContinues_spec_11_4 asserts the
// per-session cancel error cases: the loop writes every other session, the
// call answers 200 with the failing session in failedSessions in the body
// and the audit detail, and the tokens are still revoked. A write that
// committed and whose primary re-read finds the row cancelled gets one
// AfterRevokeWrite carrying the re-read row, and is still reported as
// failed and not counted; a re-read that fails runs no AfterRevokeWrite.
//
// spec: §11.4 (User Invalidation: Full revoke propagation mechanism)
func TestFullRevokeReportsAFailedCancelWriteAndContinues_spec_11_4(t *testing.T) {
	for _, tc := range []struct {
		name           string
		commitThenFail bool
		rereadErr      error
		wantState      session.State
		wantHook       bool
	}{
		{name: "write_not_applied", wantState: session.StateRunning},
		{name: "committed_reread_finds_cancelled", commitThenFail: true, wantState: session.StateCancelled, wantHook: true},
		{name: "committed_reread_fails", commitThenFail: true, rereadErr: errors.New("primary unreachable"), wantState: session.StateCancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newContinuationFixture(
				t,
				sessionstore.Session{ID: "s_a", State: session.StateRunning},
				sessionstore.Session{ID: "s_b", State: session.StateRunning},
				sessionstore.Session{ID: "s_c", State: session.StateRunning},
			)
			f.sessions.failID, f.sessions.commitThenFail, f.sessions.rereadErr = "s_b", tc.commitThenFail, tc.rereadErr
			code, resp := f.revoke(t, false)
			if code != http.StatusOK {
				t.Fatalf("full_revoke: status %d, want 200; body %v", code, resp)
			}
			for _, id := range []string{"s_a", "s_c"} {
				if st := f.state(t, id); st != session.StateCancelled {
					t.Errorf("%s = %s, want cancelled; the loop stopped at the failed write", id, st)
				}
			}
			if st := f.state(t, "s_b"); st != tc.wantState {
				t.Errorf("s_b = %s, want %s", st, tc.wantState)
			}
			assertFailedSessions(t, resp, "s_b")
			if tc.rereadErr != nil {
				entry := resp["failedSessions"].([]any)[0].(map[string]any)
				if !strings.Contains(entry["error"].(string), "primary unreachable") {
					t.Errorf("failedSessions error = %v, want the re-read failure named", entry["error"])
				}
			}
			if resp["sessionsTerminated"] != float64(2) {
				t.Errorf("sessionsTerminated = %v, want 2", resp["sessionsTerminated"])
			}
			hookIDs := sortedCopy(f.hook.ids)
			want := []string{"s_a:cancelled", "s_c:cancelled"}
			if tc.wantHook {
				want = []string{"s_a:cancelled", "s_b:cancelled", "s_c:cancelled"}
			}
			if strings.Join(hookIDs, ",") != strings.Join(want, ",") {
				t.Errorf("AfterRevokeWrite calls = %v, want %v", hookIDs, want)
			}
			if got := strings.Join(f.sessions.rereads, ","); got != "s_b" {
				t.Errorf("primary re-reads = %q, want only the failed write's row s_b", got)
			}
			if got := sortedCopy(f.pods.gotSessions); strings.Join(got, ",") != "s_a,s_c" {
				t.Errorf("binding release sessions = %v, want [s_a s_c]", got)
			}
			if n := f.hook.openClaims(); n != 0 {
				t.Errorf("%d claims still open", n)
			}
			f.requireTokensRevoked(t, resp)
			ev := f.invalidatedEvent(t)
			failed, _ := ev["failedSessions"].([]admin.FailedRevokeSession)
			if len(failed) != 1 || failed[0].SessionID != "s_b" {
				t.Errorf("audit failedSessions = %v, want [s_b]", ev["failedSessions"])
			}
		})
	}
}

// TestFullRevokeSurvivesAClientDisconnect_spec_11_4 asserts that a request
// context cancelled right after the user row commits does not stop the
// effects: every session is cancelled, the leases are returned, the tokens
// are revoked, the elicitations are dismissed, and the audit event is
// written, all through stores that fail a call on an ended context.
//
// spec: §11.4 (User Invalidation: Full revoke propagation mechanism)
func TestFullRevokeSurvivesAClientDisconnect_spec_11_4(t *testing.T) {
	f := newContinuationFixture(
		t,
		sessionstore.Session{ID: "s_a", State: session.StateRunning},
		sessionstore.Session{ID: "s_b", State: session.StateSuspended},
	)
	f.interactions.dismissed = 3
	code, resp := f.revoke(t, true)
	if code != http.StatusOK {
		t.Fatalf("full_revoke: status %d, body %v", code, resp)
	}
	for _, id := range []string{"s_a", "s_b"} {
		if st := f.state(t, id); st != session.StateCancelled {
			t.Errorf("%s = %s after a client disconnect, want cancelled", id, st)
		}
	}
	if resp["sessionsTerminated"] != float64(2) || resp["leasesRevoked"] != float64(2) || resp["elicitationsDismissed"] != float64(3) {
		t.Errorf("response = %v, want 2 sessions, 2 leases, and 3 elicitations", resp)
	}
	if _, ok := resp["failedSessions"]; ok {
		t.Errorf("failedSessions = %v, want absent", resp["failedSessions"])
	}
	f.requireTokensRevoked(t, resp)
	_ = f.invalidatedEvent(t)
}

// TestFullRevokeListsFromThePrimary_spec_11_4 asserts that a session
// committed just before the call, which a lagging read pool does not yet
// return, is listed, its lease returned, and cancelled.
//
// spec: §11.4 (User Invalidation: Full revoke propagation mechanism), §12.3
// (Postgres HA Requirements)
func TestFullRevokeListsFromThePrimary_spec_11_4(t *testing.T) {
	f := newContinuationFixture(t, sessionstore.Session{ID: "s_new", State: session.StateCreated})
	f.sessions.primaryOnly["s_new"] = true
	code, resp := f.revoke(t, false)
	if code != http.StatusOK {
		t.Fatalf("full_revoke: status %d, body %v", code, resp)
	}
	if st := f.state(t, "s_new"); st != session.StateCancelled {
		t.Errorf("s_new = %s, want cancelled; the listing read a lagging pool", st)
	}
	if strings.Join(f.leases.gotSessions, ",") != "s_new" || resp["sessionsTerminated"] != float64(1) {
		t.Errorf("lease step = %v, response = %v; want s_new listed", f.leases.gotSessions, resp)
	}
	if strings.Join(f.hook.reclaimed, ",") != "s_new:created" {
		t.Errorf("reclaimed = %v, want s_new from created", f.hook.reclaimed)
	}
}

// TestFullRevokeReportsAnElicitationDismissalError_spec_11_4 asserts that a
// DismissByUser failure answers 200 with the error text in
// elicitationDismissalError in the body and the audit detail, after every
// other effect ran.
//
// spec: §11.4 (User Invalidation: Full revoke propagation mechanism)
func TestFullRevokeReportsAnElicitationDismissalError_spec_11_4(t *testing.T) {
	f := newContinuationFixture(t, sessionstore.Session{ID: "s_a", State: session.StateRunning})
	f.interactions.dismissed, f.interactions.err = 1, errors.New("interactions: connection refused")
	code, resp := f.revoke(t, false)
	if code != http.StatusOK {
		t.Fatalf("full_revoke: status %d, want 200; body %v", code, resp)
	}
	if resp["elicitationDismissalError"] != "interactions: connection refused" {
		t.Errorf("elicitationDismissalError = %v, want the store error", resp["elicitationDismissalError"])
	}
	if resp["elicitationsDismissed"] != float64(1) || resp["sessionsTerminated"] != float64(1) {
		t.Errorf("response = %v, want 1 elicitation and 1 session", resp)
	}
	if st := f.state(t, "s_a"); st != session.StateCancelled {
		t.Errorf("s_a = %s, want cancelled", st)
	}
	f.requireTokensRevoked(t, resp)
	if ev := f.invalidatedEvent(t); ev["elicitationDismissalError"] != "interactions: connection refused" {
		t.Errorf("audit elicitationDismissalError = %v", ev["elicitationDismissalError"])
	}
}

// TestFullRevokeListingErrorStillRevokesTokensAndAnswers500_spec_11_4
// asserts the session-listing error case: the fan-out runs with no
// sessions, so the tokens and playground sessions are still revoked, the
// elicitations are still dismissed, the audit event carries
// sessionListingError, and the call answers 500 INTERNAL_ERROR with the
// counts in the error details.
//
// spec: §11.4 (User Invalidation: Full revoke propagation mechanism), §25.2
func TestFullRevokeListingErrorStillRevokesTokensAndAnswers500_spec_11_4(t *testing.T) {
	f := newContinuationFixture(t, sessionstore.Session{ID: "s_a", State: session.StateRunning})
	f.sessions.listErr = errors.New("pgtenant: read: connection reset")
	f.interactions.dismissed = 2
	code, body := f.revoke(t, false)
	resp, _ := body["error"].(map[string]any)
	if code != http.StatusInternalServerError || resp["code"] != "INTERNAL_ERROR" {
		t.Fatalf("full_revoke: status %d body %v, want 500 INTERNAL_ERROR", code, body)
	}
	details, _ := resp["details"].(map[string]any)
	if details == nil {
		t.Fatalf("error body %v carries no details", resp)
	}
	for k, want := range map[string]float64{
		"sessionsTerminated": 0, "podsTerminated": 0, "leasesRevoked": 0,
		"elicitationsDismissed": 2,
	} {
		if details[k] != want {
			t.Errorf("details[%s] = %v, want %v", k, details[k], want)
		}
	}
	f.requireTokensRevoked(t, details)
	if f.interactions.calls != 1 {
		t.Errorf("DismissByUser calls = %d, want 1", f.interactions.calls)
	}
	if f.leases.calls != 0 || f.pods.calls != 0 {
		t.Errorf("lease calls %d, pod calls %d; want none with no listed session", f.leases.calls, f.pods.calls)
	}
	if st := f.state(t, "s_a"); st != session.StateRunning {
		t.Errorf("s_a = %s, want running", st)
	}
	ev := f.invalidatedEvent(t)
	if msg, _ := ev["sessionListingError"].(string); !strings.Contains(msg, "connection reset") {
		t.Errorf("audit sessionListingError = %v, want the listing error", ev["sessionListingError"])
	}
	if ev["tokensRevoked"] != 2 {
		t.Errorf("audit tokensRevoked = %v, want 2", ev["tokensRevoked"])
	}
}
