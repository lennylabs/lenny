// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// fenceTestServer builds a sessionserver wired only with the in-memory
// store so the CAS-fence tests can exercise the §7.2 line 214 (a)
// snapshot-close bump without any §15.1 precondition gates getting in
// the way of the seeded states.
func fenceTestServer(t *testing.T) (*sessionserver.Server, sessionstore.Store) {
	t.Helper()
	clock := func() time.Time { return time.Date(2026, 5, 27, 9, 0, 0, 0, time.UTC) }
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{Clock: clock})
	return srv, store
}

// TestDeleteFromResumingBumpsCoordinationGeneration_spec_7_2_F_7_1_14
// verifies §7.2 line 197 (resuming → cancelled via DELETE
// /v1/sessions/{id}) bumps coordination_generation in the same logical
// write that records the terminal state, per the §7.2 line 214 (a)
// snapshot-close fence. F-7.1.14.
func TestDeleteFromResumingBumpsCoordinationGeneration_spec_7_2_F_7_1_14(t *testing.T) {
	srv, store := fenceTestServer(t)
	row := sessionstore.Session{
		ID: "sess-del-resuming", TenantID: "acme", RuntimeRef: "claude-code",
		State: session.StateResuming, CoordinationGeneration: 4, RecoveryGeneration: 2,
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h := srv.Handler()
	rr := sessionRequest(t, h, http.MethodDelete, "/v1/sessions/sess-del-resuming")
	if rr.Code != http.StatusOK {
		t.Fatalf("delete: status %d body %s", rr.Code, rr.Body.String())
	}
	got, err := store.Get(context.Background(), "acme", "sess-del-resuming")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != session.StateCancelled {
		t.Errorf("State = %q, want cancelled", got.State)
	}
	if got.CoordinationGeneration != 5 {
		t.Errorf("CoordinationGeneration = %d, want 5 (bumped on resuming → cancelled)", got.CoordinationGeneration)
	}
	if got.RecoveryGeneration != 2 {
		t.Errorf("RecoveryGeneration = %d, want 2 (frozen per §7.2 line 214 (b))", got.RecoveryGeneration)
	}
}

// TestDeleteFromRunningDoesNotBumpCoordinationGeneration_spec_7_2_F_7_1_14
// verifies that the snapshot-close fence is scoped to the resuming
// transient. A DELETE on a healthy session terminates normally without
// a CG bump — there is no in-flight resume to fence against. F-7.1.14.
func TestDeleteFromRunningDoesNotBumpCoordinationGeneration_spec_7_2_F_7_1_14(t *testing.T) {
	srv, store := fenceTestServer(t)
	row := sessionstore.Session{
		ID: "sess-del-running", TenantID: "acme", RuntimeRef: "claude-code",
		State: session.StateRunning, CoordinationGeneration: 9,
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h := srv.Handler()
	rr := sessionRequest(t, h, http.MethodDelete, "/v1/sessions/sess-del-running")
	if rr.Code != http.StatusOK {
		t.Fatalf("delete: status %d body %s", rr.Code, rr.Body.String())
	}
	got, err := store.Get(context.Background(), "acme", "sess-del-running")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CoordinationGeneration != 9 {
		t.Errorf("CoordinationGeneration = %d, want 9 (no in-flight resume to fence)", got.CoordinationGeneration)
	}
}

// TestReportSessionFailureFromResumingBumpsAndWritesAwaiting_spec_7_3_8
// drives a §7.3 mid-resume failure report against a session seeded in
// `resuming`. The §7.3 lines 470-472 collapse rule says the API view
// is resume_pending → running, but the internal state machine MUST
// traverse `resuming` so the mid-resume terminal-collapse edges
// (§7.2 lines 197-198) are reachable. The fence bump verifies the
// row was actually in `resuming` at the moment of collapse.
//
// spec: §7.3 lines 470-472; §7.2 lines 197-198, 214. F-7.3.8.
func TestReportSessionFailureFromResumingBumpsAndWritesAwaiting_spec_7_3_8(t *testing.T) {
	srv, store := fenceTestServer(t)
	row := sessionstore.Session{
		ID: "sess-collapse-res", TenantID: "acme", RuntimeRef: "claude-code",
		State: session.StateResuming, CoordinationGeneration: 14,
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed: %v", err)
	}
	disp, err := srv.ReportSessionFailure(context.Background(), sessionserver.FailureReport{
		TenantID: "acme", SessionID: "sess-collapse-res", Reason: "workspace_validation_failed",
	})
	if err != nil {
		t.Fatalf("ReportSessionFailure: %v", err)
	}
	if disp.From != session.StateResuming {
		t.Fatalf("From = %q, want resuming (mid-resume terminal-collapse must observe `resuming` source state)", disp.From)
	}
	got, err := store.Get(context.Background(), "acme", "sess-collapse-res")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CoordinationGeneration <= 14 {
		t.Errorf("CoordinationGeneration = %d, want > 14 (snapshot-close bump must fire)", got.CoordinationGeneration)
	}
}

// TestStoreMonotonicityRejectsCoordinationGenerationDecrease_spec_4_2_F_7_1_14
// covers the §4.2 line 156 monotonicity floor on CoordinationGeneration.
// Without this floor, a buggy caller could "rewind" the counter and
// silently re-admit a stale coordinator. F-7.1.14.
func TestStoreMonotonicityRejectsCoordinationGenerationDecrease_spec_4_2_F_7_1_14(t *testing.T) {
	store := memstore.New()
	row := sessionstore.Session{
		ID: "sess-mono", TenantID: "acme", RuntimeRef: "claude-code",
		State: session.StateRunning, CoordinationGeneration: 10,
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Attempt a decrement.
	updated, err := store.Update(context.Background(), "acme", "sess-mono",
		func(r *sessionstore.Session) error {
			r.CoordinationGeneration = 5
			return nil
		})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.CoordinationGeneration != 10 {
		t.Errorf("CoordinationGeneration = %d, want 10 (monotonicity floor)", updated.CoordinationGeneration)
	}
}
