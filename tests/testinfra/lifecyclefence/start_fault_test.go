// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
)

// TestFaultedErasureStartFailsInsideTheStore_spec_12_8 arms the start fault
// on the in-memory posture and asserts the fault fires at the store's
// restriction write, records no job and no restriction, and leaves a later
// Start free to run the job to completion with every step run once.
//
// spec: 12.8 (Processing restriction during erasure (GDPR Article 18))
func TestFaultedErasureStartFailsInsideTheStore_spec_12_8(t *testing.T) {
	ctx := context.Background()
	w := NewWorld(t, MemoryStores(), 0)
	e := w.NewErasure()
	e.FailStarts(2)
	if _, err := e.Start(ctx, "alice"); !errors.Is(err, ErrInjected) {
		t.Fatalf("Start = %v, want the injected error", err)
	}
	RequireStartLeftNothing(ctx, t, e, w.Tenant, "alice", 2)
	job, err := e.Start(ctx, "alice")
	if err != nil {
		t.Fatalf("Start after the fault: %v", err)
	}
	j, err := e.RunToCompletion(ctx, job.ID, 4)
	if err != nil || j.Phase != erasurejob.PhaseCompleted {
		t.Fatalf("RunToCompletion = %s, %v", j.Phase, err)
	}
	for _, s := range []Step{StepDrain, StepHoldCheck, StepPseudonymize, StepVerify, StepReceipt} {
		if got := e.Steps()[s]; got != 1 {
			t.Errorf("step %s ran %d times, want 1", s, got)
		}
	}
	w.Check(t)
}

// TestStartFaultSurfacesAStartThatNeverReachedTheStore_spec_12_8 pins the
// harness's own check: a faulted Start that fails before the store's
// restriction write answers its real error rather than ErrInjected, so a
// fault short-circuited ahead of the store cannot pass for one inside it.
//
// spec: 12.8 (Processing restriction during erasure (GDPR Article 18))
func TestStartFaultSurfacesAStartThatNeverReachedTheStore_spec_12_8(t *testing.T) {
	ctx := context.Background()
	w := NewWorld(t, MemoryStores(), 0)
	e := w.NewErasure()
	e.FailStarts(1)
	_, err := e.Jobs.Start(ctx, erasurejob.Job{ID: "job-1", UserID: "alice", Phase: erasurejob.PhaseInitiated})
	if err == nil || errors.Is(err, ErrInjected) {
		t.Fatalf("Start without a tenant = %v, want the store's own error", err)
	}
	if got := e.FaultedStarts(); len(got) != 1 || got[0] != "job-1" {
		t.Fatalf("faulted Starts = %v, want [job-1]", got)
	}
}
