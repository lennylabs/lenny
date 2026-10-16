// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// recordingRevokeHook records each AfterRevokeWrite call.
type recordingRevokeHook struct {
	mu  sync.Mutex
	ids []string
}

func (r *recordingRevokeHook) AfterRevokeWrite(_ context.Context, row sessionstore.Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ids = append(r.ids, row.ID+":"+string(row.State))
}

// failSecondUpdate fails the second Update it serves, so a full_revoke over
// three sessions cancels the first and stops at the second.
type failSecondUpdate struct {
	sessionstore.Store
	mu sync.Mutex
	n  int
}

func (f *failSecondUpdate) Update(ctx context.Context, tenantID, id string, mut func(*sessionstore.Session) error) (sessionstore.Session, error) {
	f.mu.Lock()
	f.n++
	n := f.n
	f.mu.Unlock()
	if n == 2 {
		return sessionstore.Session{}, errors.New("store unavailable")
	}
	return f.Store.Update(ctx, tenantID, id, mut)
}

// TestFullRevokeRunsTheRevokeHookPerCancelledSession_spec_11_4 asserts that
// the full_revoke write calls AfterRevokeWrite exactly once for each session
// it cancelled, right after the write, with the cancelled row, and never for
// a session already terminal; the write records the Terminated fact.
//
// spec: §7.2 (Interactive Session Model), §11.4 (User Invalidation)
func TestFullRevokeRunsTheRevokeHookPerCancelledSession_spec_11_4(t *testing.T) {
	pods := &fakePodTerminator{}
	router, users, sessions, _ := newFullRevokeAdmin(t, pods, nil, nil)
	hook := &recordingRevokeHook{}
	router.WithRevokeHook(hook)
	seedUser(t, users, "acme", "alice@acme.com")
	for _, s := range []sessionstore.Session{
		{ID: "run_1", TenantID: "acme", UserID: "alice@acme.com", State: session.StateRunning},
		{ID: "run_2", TenantID: "acme", UserID: "alice@acme.com", State: session.StateResumePending},
		{ID: "done", TenantID: "acme", UserID: "alice@acme.com", State: session.StateCompleted},
	} {
		seedSession(t, sessions, s)
	}
	rr := invalidateUser(t, router.Handler(), "alice@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateFullRevoke, Reason: "ticket-9"},
		withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("full_revoke: status %d, body %s", rr.Code, rr.Body.String())
	}
	sort.Strings(hook.ids)
	if len(hook.ids) != 2 || hook.ids[0] != "run_1:cancelled" || hook.ids[1] != "run_2:cancelled" {
		t.Errorf("revoke hook calls = %v, want one per cancelled session", hook.ids)
	}
	row, _ := sessions.Get(context.Background(), "acme", "run_1")
	if row.TerminatedAt.IsZero() || row.TerminatedReason != "Cancelled" {
		t.Errorf("row = %v/%q, want the Terminated fact stamped", row.TerminatedAt, row.TerminatedReason)
	}
}

// TestPartialFullRevokeFansOutOnlyTheCancelledSessions_spec_11_4 asserts that
// a full_revoke whose second cancel fails answers 500, has run the revoke
// hook for the sessions already cancelled only, and runs the pod fan-out for
// those IDs only.
//
// spec: §7.3 (Retry and Resume), §11.4 (User Invalidation)
func TestPartialFullRevokeFansOutOnlyTheCancelledSessions_spec_11_4(t *testing.T) {
	pods := &fakePodTerminator{}
	router, users, inner, _ := newFullRevokeAdmin(t, pods, nil, nil)
	failing := &failSecondUpdate{Store: inner}
	router.WithSessions(failing)
	hook := &recordingRevokeHook{}
	router.WithRevokeHook(hook)
	seedUser(t, users, "acme", "alice@acme.com")
	for _, id := range []string{"run_1", "run_2", "run_3"} {
		seedSession(t, inner, sessionstore.Session{ID: id, TenantID: "acme", UserID: "alice@acme.com", State: session.StateRunning})
	}
	rr := invalidateUser(t, router.Handler(), "alice@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateFullRevoke, Reason: "ticket-9"},
		withAdminPrincipal)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("partial full_revoke: status %d, want 500", rr.Code)
	}
	cancelled := 0
	var first string
	for _, id := range []string{"run_1", "run_2", "run_3"} {
		if row, _ := inner.Get(context.Background(), "acme", id); row.State == session.StateCancelled {
			cancelled++
			first = id
		}
	}
	if cancelled != 1 {
		t.Fatalf("cancelled sessions = %d, want 1 before the failing write", cancelled)
	}
	if len(hook.ids) != 1 || hook.ids[0] != first+":cancelled" {
		t.Errorf("revoke hook calls = %v, want only %s", hook.ids, first)
	}
	if pods.calls != 1 || len(pods.gotSessions) != 1 || pods.gotSessions[0] != first {
		t.Errorf("fan-out = %d calls over %v, want one call over [%s]", pods.calls, pods.gotSessions, first)
	}
}
