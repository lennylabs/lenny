// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
)

// breachableWorld builds an in-memory World whose creation fence admits
// every create while the returned flag is set, standing in for a fence that
// lets inserts through after a marker commits.
func breachableWorld(t *testing.T, liveSeeds int) (*World, *atomic.Bool) {
	t.Helper()
	breach := &atomic.Bool{}
	stores := memoryStores(func(read sessionstore.MarkerReader) sessionstore.MarkerReader {
		return func(ctx context.Context, tenantID, userID string) (sessionstore.CreationMarkers, error) {
			if breach.Load() {
				return sessionstore.CreationMarkers{TenantFound: true}, nil
			}
			return read(ctx, tenantID, userID)
		}
	})
	return NewWorld(t, stores, liveSeeds), breach
}

// breachBeforeVerifyingListing arms fn to run before the drain's second
// primary listing, which is its verifying listing, through arm.
func breachBeforeVerifyingListing(arm func(func()), fn func()) {
	arm(func() { arm(fn) })
}

// createThroughBreach creates a session of owner with the fence breached
// and records a test failure unless the breached fence admitted it.
func createThroughBreach(t *testing.T, w *World, breach *atomic.Bool, owner string) {
	t.Helper()
	breach.Store(true)
	defer breach.Store(false)
	if o := w.Create(context.Background(), PathCreate, owner); o.Err != nil {
		t.Errorf("breached fence refused the create: %v", o.Err)
	}
}

// violationsContaining returns the recorded violations that contain sub.
func (w *World) violationsContaining(sub string) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for _, v := range w.violations {
		if strings.Contains(v, sub) {
			out = append(out, v)
		}
	}
	return out
}

// TestUnfaultedSuspendFlagsCreateAdmittedAfterMarker_spec_12_8 admits a
// create of the tenant after its suspension marker commits, between the
// drain's listings, and asserts the unfaulted suspend records the 200 that
// reports it in failedSessions as a violation.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 15.1 (REST API)
func TestUnfaultedSuspendFlagsCreateAdmittedAfterMarker_spec_12_8(t *testing.T) {
	w, breach := breachableWorld(t, 1)
	breachBeforeVerifyingListing(w.Faults.BeforeTenantDrainList, func() { createThroughBreach(t, w, breach, "bob") })
	res := w.SuspendUnfaulted(context.Background())
	if res.Status != 200 || len(res.FailedSessions) != 1 {
		t.Fatalf("suspend = %d %v, want 200 reporting the breached session", res.Status, res.FailedSessions)
	}
	if got := w.violationsContaining("unfaulted suspend reported failedSessions"); len(got) != 1 {
		t.Fatalf("violations = %v, want one naming the breached session", got)
	}
}

// TestUnfaultedSuspendRecordsNoViolationForAFenceThatHolds_spec_12_8 is the
// control: with the fence intact the same suspend records nothing.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 15.1 (REST API)
func TestUnfaultedSuspendRecordsNoViolationForAFenceThatHolds_spec_12_8(t *testing.T) {
	w, _ := breachableWorld(t, 1)
	if res := w.SuspendUnfaulted(context.Background()); res.Status != 200 || len(res.FailedSessions) != 0 {
		t.Fatalf("suspend = %d %v, want 200 with no failed session", res.Status, res.FailedSessions)
	}
	w.Check(t)
}

// TestUnfaultedErasureFlagsCreateAdmittedAfterRestriction_spec_12_8 admits
// a create of alice after her restriction commits, between the erasure
// drain's listings, and asserts the unfaulted run records the failed job as
// a violation and does not restart it, so the breached session survives as
// the witness.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 12.8 (Processing restriction during erasure (GDPR Article 18))
func TestUnfaultedErasureFlagsCreateAdmittedAfterRestriction_spec_12_8(t *testing.T) {
	ctx := context.Background()
	w, breach := breachableWorld(t, 1)
	e := w.NewErasure()
	job, err := e.Start(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	breachBeforeVerifyingListing(w.Faults.BeforeDrainList, func() { createThroughBreach(t, w, breach, "alice") })
	j, err := e.RunUnfaulted(ctx, job.ID, 4)
	if err == nil || j.Phase != erasurejob.PhaseFailed {
		t.Fatalf("RunUnfaulted = %s, %v; want the job left failed", j.Phase, err)
	}
	if got := w.violationsContaining("unfaulted erasure job"); len(got) == 0 {
		t.Fatal("the unfaulted run recorded no violation for the failed job")
	}
	if live, err := w.LiveInScope(ctx, "alice"); err != nil || len(live) != 1 {
		t.Fatalf("live sessions of alice = %v, %v; want the breached session kept as the witness", live, err)
	}
}

// TestUnfaultedErasureRecordsNoViolationForAFenceThatHolds_spec_12_8 is the
// control: with the fence intact the unfaulted run completes the job and
// records nothing.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 12.8 (Processing restriction during erasure (GDPR Article 18))
func TestUnfaultedErasureRecordsNoViolationForAFenceThatHolds_spec_12_8(t *testing.T) {
	ctx := context.Background()
	w, _ := breachableWorld(t, 1)
	e := w.NewErasure()
	job, err := e.Start(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if j, err := e.RunUnfaulted(ctx, job.ID, 4); err != nil || j.Phase != erasurejob.PhaseCompleted {
		t.Fatalf("RunUnfaulted = %s, %v", j.Phase, err)
	}
	w.Check(t)
}
