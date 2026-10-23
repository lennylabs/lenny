// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// orderLog records the full_revoke calls across every fake in the order
// they happen, so a test can assert the order the handler runs its steps in.
type orderLog struct {
	mu     sync.Mutex
	events []string
}

func (o *orderLog) add(e string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, e)
}

func (o *orderLog) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.events...)
}

// index returns the position of the first event equal to e, or -1.
func (o *orderLog) index(e string) int {
	for i, got := range o.snapshot() {
		if got == e {
			return i
		}
	}
	return -1
}

// withPrefix returns the positions of every event that starts with prefix.
func (o *orderLog) withPrefix(prefix string) []int {
	var out []int
	for i, got := range o.snapshot() {
		if strings.HasPrefix(got, prefix) {
			out = append(out, i)
		}
	}
	return out
}

// recordingRevokeHook is the session server's side of full_revoke: it
// records each claim opened and ended, each AfterRevokeWrite, and each
// pre-running reclaim, and it counts the claims still open.
type recordingRevokeHook struct {
	log *orderLog
	mu  sync.Mutex
	// ids records each AfterRevokeWrite as id:state.
	ids []string
	// reclaimed records each ReclaimRevokedPreRunning as id:from.
	reclaimed []string
	open      int
}

func newRecordingRevokeHook(log *orderLog) *recordingRevokeHook {
	if log == nil {
		log = &orderLog{}
	}
	return &recordingRevokeHook{log: log}
}

func (r *recordingRevokeHook) BeginTerminal(id string) func() {
	r.mu.Lock()
	r.open++
	r.mu.Unlock()
	r.log.add("begin:" + id)
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			r.open--
			r.mu.Unlock()
			r.log.add("end:" + id)
		})
	}
}

func (r *recordingRevokeHook) AfterRevokeWrite(_ context.Context, row sessionstore.Session) {
	r.mu.Lock()
	r.ids = append(r.ids, row.ID+":"+string(row.State))
	r.mu.Unlock()
	r.log.add("after:" + row.ID)
}

func (r *recordingRevokeHook) ReclaimRevokedPreRunning(_ context.Context, from session.State, row sessionstore.Session) {
	r.mu.Lock()
	r.reclaimed = append(r.reclaimed, row.ID+":"+string(from))
	r.mu.Unlock()
	r.log.add("reclaim:" + row.ID)
}

func (r *recordingRevokeHook) openClaims() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.open
}

// orderedStore records List and Update calls and injects the failures the
// cancel-write cases need: a row another writer ends just before its cancel
// write, a write that fails without applying, and a write that commits and
// then reports an error.
type orderedStore struct {
	sessionstore.Store
	log *orderLog
	// raceTerminal names a row another writer completes just before its
	// cancel write runs.
	raceTerminal string
	// failID names a row whose cancel write fails; commitThenFail makes
	// that write apply before it reports the error.
	failID         string
	commitThenFail bool
	// rereadFail makes the primary re-read of a failed cancel write fail.
	rereadFail bool
}

// GetPrimary records the primary re-read of a failed cancel write and fails
// it when rereadFail is set.
func (s *orderedStore) GetPrimary(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	s.log.add("reread:" + id)
	if s.rereadFail {
		return sessionstore.Session{}, errors.New("pgtenant: primary unreachable")
	}
	return s.Get(ctx, tenantID, id)
}

func (s *orderedStore) List(ctx context.Context, tenantID string, f sessionstore.ListFilter) ([]sessionstore.Session, error) {
	s.log.add("list")
	return s.Store.List(ctx, tenantID, f)
}

func (s *orderedStore) Update(ctx context.Context, tenantID, id string, mut func(*sessionstore.Session) error) (sessionstore.Session, error) {
	s.log.add("update:" + id)
	if id == s.raceTerminal {
		if _, err := s.Store.Update(ctx, tenantID, id, func(row *sessionstore.Session) error {
			row.State = session.StateCompleted
			return nil
		}); err != nil {
			return sessionstore.Session{}, err
		}
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

// orderedLeases records the lease step and reports one lease per session.
type orderedLeases struct {
	log *orderLog
	got []string
}

func (l *orderedLeases) RevokeUserLeases(_, _ string, ids []string) int {
	l.got = append([]string(nil), ids...)
	sort.Strings(l.got)
	l.log.add("leases")
	return len(ids)
}

// orderedPods records the binding-release fan-out.
type orderedPods struct {
	log *orderLog
	got []string
}

func (p *orderedPods) TerminateUserSessions(_ context.Context, _, _ string, ids []string) admin.UserTerminationResult {
	p.got = append([]string(nil), ids...)
	sort.Strings(p.got)
	p.log.add("pods")
	return admin.UserTerminationResult{}
}

// revokeRecorder is one full_revoke router wired with the ordered fakes.
type revokeRecorder struct {
	log    *orderLog
	store  *orderedStore
	hook   *recordingRevokeHook
	leases *orderedLeases
	pods   *orderedPods
	router *admin.Router
}

func newRevokeRecorder(t *testing.T, rows ...sessionstore.Session) *revokeRecorder {
	t.Helper()
	log := &orderLog{}
	rec := &revokeRecorder{
		log:    log,
		hook:   newRecordingRevokeHook(log),
		leases: &orderedLeases{log: log},
		pods:   &orderedPods{log: log},
	}
	router, users, inner, _ := newFullRevokeAdmin(t, rec.pods, rec.leases, nil)
	rec.store = &orderedStore{Store: inner, log: log}
	rec.router = router.WithSessions(rec.store).WithRevokeHook(rec.hook)
	seedUser(t, users, "acme", "alice@acme.com")
	for _, row := range rows {
		row.TenantID, row.UserID = "acme", "alice@acme.com"
		seedSession(t, inner, row)
	}
	return rec
}

func (rec *revokeRecorder) revoke(t *testing.T) (int, map[string]any) {
	t.Helper()
	rr := invalidateUser(t, rec.router.Handler(), "alice@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateFullRevoke, Reason: "ticket-9"},
		withAdminPrincipal)
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	return rr.Code, resp
}

// assertClaimEndsAtOnce asserts that the event right after id's cancel
// write is the end of the claim that write opened.
func (rec *revokeRecorder) assertClaimEndsAtOnce(t *testing.T, id string) {
	t.Helper()
	ev := rec.log.snapshot()
	i := rec.log.index("update:" + id)
	if i < 0 || i+1 >= len(ev) || ev[i+1] != "end:"+id {
		t.Errorf("events = %v; the claim of %s does not end right after its cancel write", ev, id)
	}
}

// TestFullRevokeRunsItsStepsInOrder_spec_11_4 asserts the full_revoke order
// over every pre-revoke state: the listing, then the lease step over every
// listed session, terminal ones included, then per session the claim, the cancel
// write, and AfterRevokeWrite, then the pre-running reclaim of the sessions
// cancelled from created, finalizing, or ready, then the end of every
// claim, then the binding release. A row another writer ends between the
// listing and its cancel write is in the lease step, its claim ends right
// after the superseded write, and it reaches neither the reclaim nor the
// binding release. No claim is still open when the handler returns.
//
// spec: §11.4 (User Invalidation), §10.1.5 (Stale Replica Behavior)
func TestFullRevokeRunsItsStepsInOrder_spec_11_4(t *testing.T) {
	rec := newRevokeRecorder(
		t,
		sessionstore.Session{ID: "s_created", State: session.StateCreated},
		sessionstore.Session{ID: "s_finalizing", State: session.StateFinalizing},
		sessionstore.Session{ID: "s_ready", State: session.StateReady},
		sessionstore.Session{ID: "s_running", State: session.StateRunning},
		sessionstore.Session{ID: "s_suspended", State: session.StateSuspended},
		sessionstore.Session{ID: "s_raced", State: session.StateRunning},
		sessionstore.Session{ID: "s_done", State: session.StateCompleted},
	)
	rec.store.raceTerminal = "s_raced"
	code, resp := rec.revoke(t)
	if code != http.StatusOK {
		t.Fatalf("full_revoke: status %d, body %v", code, resp)
	}
	cancelled := []string{"s_created", "s_finalizing", "s_ready", "s_running", "s_suspended"}
	listed := append(append([]string(nil), cancelled...), "s_raced", "s_done")
	sort.Strings(listed)
	if strings.Join(rec.leases.got, ",") != strings.Join(listed, ",") {
		t.Errorf("lease step sessions = %v, want every listed session %v", rec.leases.got, listed)
	}
	if rec.log.index("update:s_done") >= 0 {
		t.Errorf("events = %v; a terminal session got a cancel write", rec.log.snapshot())
	}
	ev := rec.log.snapshot()
	leases := rec.log.index("leases")
	if rec.log.index("list") != 0 || leases != 1 {
		t.Fatalf("events = %v; want the listing then the lease step first", ev)
	}
	updates := rec.log.withPrefix("update:")
	lastUpdate := updates[len(updates)-1]
	for _, id := range cancelled {
		b, u, a := rec.log.index("begin:"+id), rec.log.index("update:"+id), rec.log.index("after:"+id)
		if leases >= b || b >= u || u >= a {
			t.Errorf("events = %v; %s does not run claim, cancel write, then AfterRevokeWrite after the lease step", ev, id)
		}
	}
	rec.assertClaimEndsAtOnce(t, "s_raced")
	reclaims := rec.log.withPrefix("reclaim:")
	sort.Strings(rec.hook.reclaimed)
	wantReclaimed := []string{"s_created:created", "s_finalizing:finalizing", "s_ready:ready"}
	if strings.Join(rec.hook.reclaimed, ",") != strings.Join(wantReclaimed, ",") {
		t.Errorf("reclaimed = %v, want %v", rec.hook.reclaimed, wantReclaimed)
	}
	pods := rec.log.index("pods")
	for _, i := range reclaims {
		if i < lastUpdate {
			t.Errorf("events = %v; a reclaim precedes a cancel write", ev)
		}
	}
	for _, id := range cancelled {
		end := rec.log.index("end:" + id)
		for _, i := range reclaims {
			if end < i {
				t.Errorf("events = %v; the claim of %s ends before a reclaim", ev, id)
			}
		}
		if end > pods {
			t.Errorf("events = %v; the claim of %s ends after the binding release", ev, id)
		}
	}
	if strings.Join(rec.pods.got, ",") != strings.Join(sortedCopy(cancelled), ",") {
		t.Errorf("binding release sessions = %v, want the cancelled set %v", rec.pods.got, cancelled)
	}
	if n := rec.hook.openClaims(); n != 0 {
		t.Errorf("%d claims still open after the handler returned", n)
	}
	if resp["leasesRevoked"] != float64(len(listed)) || resp["sessionsTerminated"] != float64(len(cancelled)) {
		t.Errorf("response = %v, want leasesRevoked %d and sessionsTerminated %d", resp, len(listed), len(cancelled))
	}
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
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
	hook := newRecordingRevokeHook(nil)
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
	if len(hook.reclaimed) != 0 {
		t.Errorf("reclaimed = %v, want none for sessions that had started", hook.reclaimed)
	}
	row, _ := sessions.Get(context.Background(), "acme", "run_1")
	if row.TerminatedAt.IsZero() || row.TerminatedReason != "Cancelled" {
		t.Errorf("row = %v/%q, want the Terminated fact stamped", row.TerminatedAt, row.TerminatedReason)
	}
}

// TestPartialFullRevokeContinuesPastAFailedCancelWrite_spec_11_4 asserts the
// cancel-store-error case. Three ready sessions are listed and the second
// cancel write fails. The lease step receives all three, the loop continues
// to the third write, the handler answers 200 with the failing session in
// failedSessions, and only the first and third sessions reach the
// pre-running reclaim, the binding release, and sessionsTerminated. A write
// that reports an error is re-read from the primary while its claim is still
// open, and the claim ends right after. When the write committed and the
// re-read finds the row cancelled, AfterRevokeWrite runs once on the re-read
// row; when the write did not apply, or the re-read fails, it does not run.
// No claim is still open when the handler returns.
//
// spec: §7.3 (Retry and Resume), §11.4 (User Invalidation: Full revoke
// propagation mechanism)
func TestPartialFullRevokeContinuesPastAFailedCancelWrite_spec_11_4(t *testing.T) {
	for _, tc := range []struct {
		name           string
		commitThenFail bool
		rereadFail     bool
		wantFailed     session.State
		wantHookOnFail bool
	}{
		{name: "write_not_applied", wantFailed: session.StateReady},
		{name: "write_committed_then_error", commitThenFail: true, wantFailed: session.StateCancelled, wantHookOnFail: true},
		{name: "write_committed_then_error_reread_fails", commitThenFail: true, rereadFail: true, wantFailed: session.StateCancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := newRevokeRecorder(
				t,
				sessionstore.Session{ID: "run_1", State: session.StateReady},
				sessionstore.Session{ID: "run_2", State: session.StateReady},
				sessionstore.Session{ID: "run_3", State: session.StateReady},
			)
			// The listing order decides which write is second, so the
			// failing row is chosen from the order the writes run in.
			rows, _ := rec.store.Store.List(context.Background(), "acme", sessionstore.ListFilter{UserID: "alice@acme.com"})
			first, failing, third := rows[0].ID, rows[1].ID, rows[2].ID
			rec.store.failID, rec.store.commitThenFail, rec.store.rereadFail = failing, tc.commitThenFail, tc.rereadFail
			code, resp := rec.revoke(t)
			if code != http.StatusOK {
				t.Fatalf("partial full_revoke: status %d, want 200; body %v", code, resp)
			}
			if strings.Join(rec.leases.got, ",") != "run_1,run_2,run_3" {
				t.Errorf("lease step sessions = %v, want all three listed sessions", rec.leases.got)
			}
			done := sortedCopy([]string{first, third})
			sort.Strings(rec.hook.reclaimed)
			if strings.Join(rec.hook.reclaimed, ",") != done[0]+":ready,"+done[1]+":ready" {
				t.Errorf("reclaimed = %v, want only %v", rec.hook.reclaimed, done)
			}
			if strings.Join(rec.pods.got, ",") != strings.Join(done, ",") {
				t.Errorf("binding release sessions = %v, want only %v", rec.pods.got, done)
			}
			wantHook := []string{first + ":cancelled", third + ":cancelled"}
			if tc.wantHookOnFail {
				wantHook = append(wantHook, failing+":cancelled")
			}
			if got := sortedCopy(rec.hook.ids); strings.Join(got, ",") != strings.Join(sortedCopy(wantHook), ",") {
				t.Errorf("revoke hook calls = %v, want %v", got, sortedCopy(wantHook))
			}
			assertFailedWriteReread(t, rec, failing, tc.wantHookOnFail)
			if rec.log.index("update:"+third) < 0 {
				t.Errorf("events = %v; the write after the failing one did not run", rec.log.snapshot())
			}
			if n := rec.hook.openClaims(); n != 0 {
				t.Errorf("%d claims still open after the handler returned", n)
			}
			assertFailedSessions(t, resp, failing)
			if resp["sessionsTerminated"] != float64(2) {
				t.Errorf("sessionsTerminated = %v, want 2", resp["sessionsTerminated"])
			}
			row, _ := rec.store.Get(context.Background(), "acme", failing)
			if row.State != tc.wantFailed {
				t.Errorf("failing row state = %q, want %q", row.State, tc.wantFailed)
			}
		})
	}
}

// assertFailedWriteReread asserts the events after a failed cancel write of
// id: the primary re-read, then AfterRevokeWrite when wantHook is set, then
// the end of the claim the write opened.
func assertFailedWriteReread(t *testing.T, rec *revokeRecorder, id string, wantHook bool) {
	t.Helper()
	want := []string{"update:" + id, "reread:" + id}
	if wantHook {
		want = append(want, "after:"+id)
	}
	want = append(want, "end:"+id)
	ev := rec.log.snapshot()
	i := rec.log.index("update:" + id)
	if i < 0 || i+len(want) > len(ev) || strings.Join(ev[i:i+len(want)], ",") != strings.Join(want, ",") {
		t.Errorf("events = %v; want %v in sequence after the failed write", ev, want)
	}
}

// assertFailedSessions asserts that the response's failedSessions names
// exactly id, with a non-empty error.
func assertFailedSessions(t *testing.T, resp map[string]any, id string) {
	t.Helper()
	failed, _ := resp["failedSessions"].([]any)
	if len(failed) != 1 {
		t.Fatalf("failedSessions = %v, want one entry for %s", resp["failedSessions"], id)
	}
	entry, _ := failed[0].(map[string]any)
	if entry["sessionId"] != id || entry["error"] == "" || entry["error"] == nil {
		t.Errorf("failedSessions[0] = %v, want sessionId %s with the error text", entry, id)
	}
}
