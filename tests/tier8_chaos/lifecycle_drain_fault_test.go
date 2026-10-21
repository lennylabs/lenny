// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 fault injection into each step of the §12.8 lifecycle operations
// over the production Postgres stores: the drain's listing and one forced
// termination, the erasure job's Start, each erasure phase, and the
// completion receipt; a cancelled suspend request; a crash between erasure
// phases and a graceful stop of the leader inside one; a crash between a
// derive's copy and its insert; and a replica takeover of the
// tenant-deletion tick. Every case seeds live sessions in the scope. The
// lifecyclefence World asserts, at the moment an erasure job leaves
// initiated or a deletion job leaves Phase 2, that a primary listing of the
// scope returns no non-terminal session, and that a job's restriction stays
// set while the job is in progress; each case also asserts that a resumed
// job re-ran no completed step.
package tier8_chaos_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/controller/tenantdeletion"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/lifecyclefence"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// lifecycleFaultStores starts one Postgres for the test and returns its
// posture.
func lifecycleFaultStores(t *testing.T) *lifecyclefence.Stores {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: schematest.RepoRoot(t) + "/migrations"})
	s, err := lifecyclefence.PostgresStores(pg.Pool)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// startErasure starts alice's erasure and returns the job.
func startErasure(t *testing.T, e *lifecyclefence.Erasure) erasurejob.Job {
	t.Helper()
	job, err := e.Start(context.Background(), "alice")
	if err != nil {
		t.Fatalf("start erasure: %v", err)
	}
	return job
}

// runPass runs one RunPending on a fresh replica's runner over ctx and
// returns the job as persisted after it.
func runPass(ctx context.Context, t *testing.T, e *lifecyclefence.Erasure, jobID string) erasurejob.Job {
	t.Helper()
	_ = e.Runner().RunPending(ctx)
	j, err := e.Jobs.Get(context.Background(), erasurejob.AllTenants, jobID)
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	return j
}

// complete runs the job to completion on fresh replicas.
func complete(t *testing.T, e *lifecyclefence.Erasure, jobID string) erasurejob.Job {
	t.Helper()
	j, err := e.RunToCompletion(context.Background(), jobID, 6)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

// failOnce returns a step fault that fails step s the first time it runs.
func failOnce(s lifecyclefence.Step) func(lifecyclefence.Step) error {
	fired := false
	return func(got lifecyclefence.Step) error {
		if got == s && !fired {
			fired = true
			return lifecyclefence.ErrInjected
		}
		return nil
	}
}

// requireStepsOnce asserts that each step ran exactly once across every
// replica, so a resumed job re-ran no completed step.
func requireStepsOnce(t *testing.T, e *lifecyclefence.Erasure) {
	t.Helper()
	for _, s := range []lifecyclefence.Step{
		lifecyclefence.StepDrain, lifecyclefence.StepHoldCheck, lifecyclefence.StepErase,
		lifecyclefence.StepPseudonymize, lifecyclefence.StepVerify, lifecyclefence.StepReceipt,
	} {
		if n := e.Steps()[s]; n != 1 {
			t.Errorf("step %s ran %d times, want 1: %v", s, n, e.Steps())
		}
	}
}

// erasureFault is an injected failure that fails an erasure job, counted
// under label; the retry restarts the job at initiated and runs it to
// completion.
type erasureFault struct {
	name   string
	inject func(w *lifecyclefence.World, e *lifecyclefence.Erasure)
	label  string
}

func erasureFaultCases() []erasureFault {
	stepFault := func(s lifecyclefence.Step) func(*lifecyclefence.World, *lifecyclefence.Erasure) {
		return func(_ *lifecyclefence.World, e *lifecyclefence.Erasure) { e.SetFault(failOnce(s)) }
	}
	return []erasureFault{
		{"drain listing", func(w *lifecyclefence.World, _ *lifecyclefence.Erasure) { w.Faults.FailDrainListings(1) }, erasurejob.FailurePhaseStoreDelete},
		{"one forced termination", func(w *lifecyclefence.World, _ *lifecyclefence.Erasure) { w.Faults.FailForceTerminates(1) }, erasurejob.FailurePhaseStoreDelete},
		{"initiated hold re-check", stepFault(lifecyclefence.StepHoldCheck), erasurejob.FailurePhaseStoreDelete},
		{"store_deleting eraser", stepFault(lifecyclefence.StepErase), erasurejob.FailurePhaseStoreDelete},
		{"pseudonymizing", stepFault(lifecyclefence.StepPseudonymize), erasurejob.FailurePhasePseudonymization},
		{"verifying", stepFault(lifecyclefence.StepVerify), erasurejob.FailurePhaseVerification},
	}
}

// TestErasureFaultAtEachStep_spec_12_8 injects a store error into each step
// an erasure job runs. The failure fails the job with the restriction still
// set and no eraser run past the failing step; the retry runs it to
// completion, and the job leaves initiated only with the user's scope
// drained.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 12.8 (Processing restriction during erasure (GDPR Article 18)), 12.8 (Erasure job phase field (crash recovery))
// diagnosis: a failure means an erasure step's error lifted the restriction
// before completion, let the job advance past initiated with a live
// session, ran an eraser after a failed drain or hold re-check, or left the
// job unable to complete on retry.
func TestErasureFaultAtEachStep_spec_12_8(t *testing.T) {
	s := lifecycleFaultStores(t)
	for _, tc := range erasureFaultCases() {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			w := lifecyclefence.NewWorld(t, s, 2)
			e := w.NewErasure()
			job := startErasure(t, e)
			tc.inject(w, e)
			j := runPass(ctx, t, e, job.ID)
			if j.Phase != erasurejob.PhaseFailed {
				t.Fatalf("phase after the fault = %s, want failed", j.Phase)
			}
			e.AssertRestricted(ctx, j)
			if got := e.Failures(); len(got) != 1 || got[0] != tc.label {
				t.Errorf("failure labels = %v, want [%s]", got, tc.label)
			}
			if tc.label == erasurejob.FailurePhaseStoreDelete && e.Steps()[lifecyclefence.StepPseudonymize] > 0 {
				t.Errorf("pseudonymize ran after a store_delete failure: %v", e.Steps())
			}
			e.SetFault(nil)
			if got := complete(t, e, job.ID); got.Phase != erasurejob.PhaseCompleted {
				t.Fatalf("phase after retry = %s", got.Phase)
			}
			w.Check(t)
		})
	}
}

// TestErasureStartAndReceiptFaults_spec_12_8 injects a store error into
// Start, which records no job and sets no restriction, and into the
// completion receipt, which keeps the job in progress with its restriction
// so the next run writes the receipt and completes without re-running an
// eraser.
//
// spec: 12.8 (Processing restriction during erasure (GDPR Article 18)), 12.8 (Erasure job phase field (crash recovery))
// diagnosis: a failure means a failed Start left a job or a restriction
// behind, or a receipt error either completed the job without a receipt or
// made the next run repeat erasers.
func TestErasureStartAndReceiptFaults_spec_12_8(t *testing.T) {
	s := lifecycleFaultStores(t)
	ctx := context.Background()

	t.Run("start", func(t *testing.T) {
		w := lifecyclefence.NewWorld(t, s, 2)
		e := w.NewErasure()
		e.FailStarts(2)
		if _, err := e.Start(ctx, "alice"); !errors.Is(err, lifecyclefence.ErrInjected) {
			t.Fatalf("Start = %v, want the injected error", err)
		}
		u, err := s.Users.Get(ctx, w.Tenant, "alice")
		if err != nil {
			t.Fatal(err)
		}
		if u.ProcessingRestricted {
			t.Fatal("a failed Start set the restriction")
		}
		job := startErasure(t, e)
		complete(t, e, job.ID)
		requireStepsOnce(t, e)
		w.Check(t)
	})

	t.Run("receipt", func(t *testing.T) {
		w := lifecyclefence.NewWorld(t, s, 2)
		e := w.NewErasure()
		job := startErasure(t, e)
		e.SetFault(failOnce(lifecyclefence.StepReceipt))
		j := runPass(ctx, t, e, job.ID)
		if j.Phase.Terminal() || j.ErasedAt.IsZero() {
			t.Fatalf("after a receipt error the job is %s with ErasedAt %v; want in progress with ErasedAt set", j.Phase, j.ErasedAt)
		}
		e.AssertRestricted(ctx, j)
		complete(t, e, job.ID)
		requireStepsOnce(t, e)
		w.Check(t)
	})
}

// TestErasureCrashAndLeaderStopResume_spec_12_8 stops the executing replica
// at each phase boundary (a crash after the phase write) and inside the
// store_deleting phase (a graceful stop of the leader while an eraser
// runs). The job stays in its persisted phase with the restriction set, and
// another replica resumes it to completion with every step run exactly
// once.
//
// spec: 12.8 (Erasure job phase field (crash recovery)), 12.8 (Processing restriction during erasure (GDPR Article 18)), 12.8 (Compliance Interfaces: Lifecycle fence and drain)
// diagnosis: a failure means a stopped executor failed the job, or the
// resuming replica re-ran a completed step (a second drain, eraser,
// pseudonymize, or verify), or lost the restriction across the takeover.
func TestErasureCrashAndLeaderStopResume_spec_12_8(t *testing.T) {
	s := lifecycleFaultStores(t)
	boundaries := []erasurejob.Phase{erasurejob.PhaseStoreDeleting, erasurejob.PhasePseudonymizing, erasurejob.PhaseVerifying}
	for _, at := range boundaries {
		t.Run("crash after entering "+string(at), func(t *testing.T) {
			w := lifecyclefence.NewWorld(t, s, 2)
			e := w.NewErasure()
			job := startErasure(t, e)
			crashCtx, crash := context.WithCancel(context.Background())
			defer crash()
			e.SetAfterAdvance(func(_, to erasurejob.Phase) {
				if to == at {
					crash()
				}
			})
			j := runPass(crashCtx, t, e, job.ID)
			if j.Phase != at {
				t.Fatalf("phase after the crash = %s, want %s", j.Phase, at)
			}
			e.AssertRestricted(context.Background(), j)
			e.SetAfterAdvance(nil)
			complete(t, e, job.ID)
			requireStepsOnce(t, e)
			w.Check(t)
		})
	}
	t.Run("leader stop inside store_deleting", func(t *testing.T) {
		w := lifecyclefence.NewWorld(t, s, 2)
		e := w.NewErasure()
		job := startErasure(t, e)
		stopCtx, stop := context.WithCancel(context.Background())
		defer stop()
		stopped := false
		e.SetFault(func(st lifecyclefence.Step) error {
			if st == lifecyclefence.StepErase && !stopped {
				stopped = true
				stop()
				return context.Canceled
			}
			return nil
		})
		j := runPass(stopCtx, t, e, job.ID)
		if j.Phase != erasurejob.PhaseStoreDeleting {
			t.Fatalf("phase after the leader stopped = %s, want store_deleting", j.Phase)
		}
		e.AssertRestricted(context.Background(), j)
		e.SetFault(nil)
		complete(t, e, job.ID)
		requireStepsOnce(t, e)
		w.Check(t)
	})
}

// TestDeriveCrashBeforeInsertErasedByOwnerErasure_spec_12_8 stops a derive
// after its reservation and snapshot copy, before its insert and its undo,
// as a replica that dies there does. The owner's next erasure reaches the
// reserved ID and deletes the copied objects and the reservation.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 12.8 (Processing restriction during erasure (GDPR Article 18))
// diagnosis: a failure means the erasure's session lister does not return
// reserved session IDs, or DeleteByUser does not delete the user's
// reservations, so a crashed derive leaves the owner's data behind after
// the erasure completes.
func TestDeriveCrashBeforeInsertErasedByOwnerErasure_spec_12_8(t *testing.T) {
	s := lifecycleFaultStores(t)
	w := lifecyclefence.NewWorld(t, s, 2)
	ctx := context.Background()
	o := w.CreateCrashing(ctx, "alice")
	if !o.Crashed || w.Objects.Count(w.Tenant, o.ID) == 0 {
		t.Fatalf("the crashed derive wrote no objects: %+v", o)
	}
	e := w.NewErasure()
	job := startErasure(t, e)
	complete(t, e, job.ID)
	if n := w.Objects.Count(w.Tenant, o.ID); n != 0 {
		t.Errorf("%d copied objects outlived the owner's erasure", n)
	}
	w.Check(t)
}

// TestSuspendDrainFaults_spec_15_1 injects a listing error and a forced
// termination error into the suspend drain, and cancels a suspend request
// while its drain runs. The marker stays committed and audited in every
// case; a failed listing answers 500 and a failed termination answers 200
// naming the session; a repeat suspend drains the rest; and a cancelled
// request still drains the whole tenant.
//
// spec: 15.1 (REST API), 12.8 (Compliance Interfaces: Lifecycle fence and drain)
// diagnosis: a failure means a suspend drain error lifted or never
// committed the marker, went unreported, was not retried by a repeat
// suspend, or that the request's cancellation stopped the drain or the
// audit write.
func TestSuspendDrainFaults_spec_15_1(t *testing.T) {
	s := lifecycleFaultStores(t)
	ctx := context.Background()

	t.Run("listing error then repeat", func(t *testing.T) {
		w := lifecyclefence.NewWorld(t, s, 2)
		w.Faults.FailDrainListings(1)
		if res := w.Suspend(ctx); res.Status != http.StatusInternalServerError {
			t.Fatalf("suspend with a failing listing = %d, want 500: %s", res.Status, res.Body)
		}
		requireSuspended(t, w)
		if res := w.Suspend(ctx); res.Status != http.StatusOK || len(res.FailedSessions) != 0 {
			t.Fatalf("repeat suspend = %d %v: %s", res.Status, res.FailedSessions, res.Body)
		}
		w.Check(t)
	})

	t.Run("one forced termination error then repeat", func(t *testing.T) {
		w := lifecyclefence.NewWorld(t, s, 2)
		w.Faults.FailForceTerminates(1)
		res := w.Suspend(ctx)
		if res.Status != http.StatusOK || len(res.FailedSessions) != 1 {
			t.Fatalf("suspend = %d failed %v, want 200 naming one session: %s", res.Status, res.FailedSessions, res.Body)
		}
		if res := w.Suspend(ctx); res.Status != http.StatusOK || len(res.FailedSessions) != 0 {
			t.Fatalf("repeat suspend = %d %v: %s", res.Status, res.FailedSessions, res.Body)
		}
		w.Check(t)
	})

	t.Run("request cancelled during the drain", func(t *testing.T) {
		w := lifecyclefence.NewWorld(t, s, 2)
		reqCtx, cancel := context.WithCancel(ctx)
		w.Faults.BeforeDrainList(cancel)
		if res := w.Suspend(reqCtx); res.Status != http.StatusOK || len(res.FailedSessions) != 0 {
			t.Fatalf("cancelled suspend = %d %v: %s", res.Status, res.FailedSessions, res.Body)
		}
		requireSuspended(t, w)
		if live, err := w.LiveInScope(ctx, ""); err != nil || len(live) != 0 {
			t.Fatalf("after a cancelled suspend the tenant holds live sessions %v (%v)", live, err)
		}
		w.Check(t)
	})
}

// requireSuspended asserts the tenant's suspension marker is committed.
func requireSuspended(t *testing.T, w *lifecyclefence.World) {
	t.Helper()
	tn, err := w.S.Tenants.Get(context.Background(), w.Tenant)
	if err != nil {
		t.Fatal(err)
	}
	if !tn.Suspended {
		t.Fatal("the suspension marker is not set")
	}
}

// TestDeletionPhase2FaultsAndTakeover_spec_12_8 injects a listing error and
// a forced termination error into the Phase 2 drain, which keeps the job at
// Phase 2 until a later tick drains the tenant, and hands the tick to
// another replica whose lease the first lost mid-drain: the new replica's
// fresh job store re-runs Phase 1 and drains the tenant before it leaves
// Phase 2.
//
// spec: 12.8 (Tenant deletion lifecycle), 12.8 (Compliance Interfaces: Lifecycle fence and drain)
// diagnosis: a failure means a Phase 2 drain error failed the job or let it
// advance with live sessions, or a replica takeover advanced the deletion
// past Phase 2 without draining the tenant.
func TestDeletionPhase2FaultsAndTakeover_spec_12_8(t *testing.T) {
	s := lifecycleFaultStores(t)
	ctx := context.Background()
	faults := []struct {
		name   string
		inject func(w *lifecyclefence.World)
	}{
		{"listing error", func(w *lifecyclefence.World) { w.Faults.FailDrainListings(1) }},
		{"one forced termination error", func(w *lifecyclefence.World) { w.Faults.FailForceTerminates(1) }},
	}
	for _, f := range faults {
		t.Run(f.name, func(t *testing.T) {
			w := lifecyclefence.NewWorld(t, s, 2)
			d := w.NewDeletion()
			if err := d.Start(ctx); err != nil {
				t.Fatal(err)
			}
			if p, err := d.Tick(ctx); err != nil || p != tenantdeletion.PhaseTerminateSessions {
				t.Fatalf("Phase 1 tick = %s, %v", p, err)
			}
			f.inject(w)
			if p, err := d.Tick(ctx); err != nil || p != tenantdeletion.PhaseTerminateSessions {
				t.Fatalf("the faulted Phase 2 tick left phase %s (%v), want terminate_sessions", p, err)
			}
			if p, err := d.ThroughPhase2(ctx, 3); err != nil {
				t.Fatalf("Phase 2 retry: %s, %v", p, err)
			}
			w.Check(t)
		})
	}

	t.Run("replica takeover mid-drain", func(t *testing.T) {
		w := lifecyclefence.NewWorld(t, s, 2)
		first := w.NewDeletion()
		if err := first.Start(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := first.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		leaseCtx, loseLease := context.WithCancel(ctx)
		w.Faults.BeforeDrainList(loseLease)
		if p, _ := first.Tick(leaseCtx); p != tenantdeletion.PhaseTerminateSessions {
			t.Fatalf("the replica that lost its lease left phase %s, want terminate_sessions", p)
		}
		second := w.NewDeletion()
		if err := second.Start(ctx); err != nil {
			t.Fatal(err)
		}
		if p, err := second.ThroughPhase2(ctx, 4); err != nil {
			t.Fatalf("takeover replica: %s, %v", p, err)
		}
		w.Check(t)
	})
}
