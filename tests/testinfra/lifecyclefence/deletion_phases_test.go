// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/controller/tenantdeletion"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
)

// recordingTB captures the errors a check reports, so a test can assert
// that the check fails on a planted defect.
type recordingTB struct {
	testing.TB
	errs []string
}

func (r *recordingTB) Helper()                   {}
func (r *recordingTB) Error(args ...any)         { r.errs = append(r.errs, fmt.Sprint(args...)) }
func (r *recordingTB) Errorf(f string, a ...any) { r.errs = append(r.errs, fmt.Sprintf(f, a...)) }

// startedPair builds an in-memory World with a twin and starts its
// deletion.
func startedPair(t *testing.T) (*World, *World, *Deletion) {
	t.Helper()
	w := NewWorld(t, MemoryStores(), 2)
	tw := w.NewTwin(t, 2)
	d := w.NewDeletion()
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	return w, tw, d
}

// TestDeletionThroughPhase4ErasesTenantAndSparesTwin_spec_12_8 runs a
// tenant deletion to completion over the harness seams and asserts that
// every Phase 3 step and Phase 4 entry ran once, that the tenant holds
// nothing and is no longer registered, and that the twin tenant sharing the
// blob store, the manifests, the lease pool, and the TokenStore is unchanged
// and still registered.
//
// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle), 10.2 (Authentication)
func TestDeletionThroughPhase4ErasesTenantAndSparesTwin_spec_12_8(t *testing.T) {
	ctx := context.Background()
	w, tw, d := startedPair(t)
	if len(w.Leases.Held(w.Tenant, "")) == 0 || w.Tokens.Count(w.Tenant) == 0 || w.Objects.TenantCount(w.Tenant) == 0 {
		t.Fatal("the seed holds no lease, token, or object, so the test would not exercise Phases 3 and 4")
	}
	if p, err := d.ThroughPhase4(ctx, 10); err != nil || p != tenantdeletion.PhaseDestroyKMSKey {
		t.Fatalf("ThroughPhase4 = %s, %v", p, err)
	}
	if err := d.Complete(ctx, 10); err != nil {
		t.Fatal(err)
	}
	for _, s := range append(append([]DeletionStep{StepReturnLeases}, Phase3Steps...), Phase4Entries...) {
		if n := w.DeletionSteps()[s]; n != 1 {
			t.Errorf("step %s ran %d times, want 1", s, n)
		}
	}
	if r, err := w.Residue(ctx); err != nil || !r.Erased() {
		t.Fatalf("residue = %+v (%v), want erased", r, err)
	}
	if r, err := tw.Residue(ctx); err != nil || !r.Registered || r.Sessions == 0 {
		t.Fatalf("twin residue = %+v (%v), want registered and populated", r, err)
	}
	w.Check(t)
}

// TestDeletionPhaseExitInvariantsCatchPlantedResidue_spec_12_8 plants a
// defect at each phase exit and asserts the harness reports it: a TokenStore
// row restored after the Phase 3 token delete, an object restored after the
// last Phase 4 entry, and an object written into the twin tenant.
//
// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle)
func TestDeletionPhaseExitInvariantsCatchPlantedResidue_spec_12_8(t *testing.T) {
	ctx := context.Background()
	t.Run("token after phase 3", func(t *testing.T) {
		w, _, d := startedPair(t)
		w.Faults.AfterDeletionStep(func(s DeletionStep) {
			if s == StepDeleteTokens {
				w.Tokens.Seed(w.Tenant, "alice", 1)
			}
		})
		if _, err := d.ThroughPhase4(ctx, 10); err != nil {
			t.Fatal(err)
		}
		if got := w.violationsContaining("TokenStore rows"); len(got) != 1 {
			t.Fatalf("violations = %v, want one naming the TokenStore row", got)
		}
	})
	t.Run("object after phase 4", func(t *testing.T) {
		w, _, d := startedPair(t)
		w.Faults.AfterDeletionStep(func(s DeletionStep) {
			if s == Phase4Entries[len(Phase4Entries)-1] {
				putObject(t, w, w.Tenant)
			}
		})
		if _, err := d.ThroughPhase4(ctx, 10); err != nil {
			t.Fatal(err)
		}
		if got := w.violationsContaining("left Phase 4"); len(got) != 1 {
			t.Fatalf("violations = %v, want one naming the residue", got)
		}
		rec := &recordingTB{TB: t}
		w.checkResidue(rec, ctx)
		if len(rec.errs) != 1 || !strings.Contains(rec.errs[0], "after its deletion left Phase 4") {
			t.Fatalf("checkResidue errors = %v, want the residue", rec.errs)
		}
	})
	t.Run("twin crossed", func(t *testing.T) {
		w, tw, d := startedPair(t)
		if _, err := d.ThroughPhase4(ctx, 10); err != nil {
			t.Fatal(err)
		}
		putObject(t, w, tw.Tenant)
		rec := &recordingTB{TB: t}
		w.checkResidue(rec, ctx)
		if len(rec.errs) != 1 || !strings.Contains(rec.errs[0], "crossed tenants") {
			t.Fatalf("checkResidue errors = %v, want the twin change", rec.errs)
		}
	})
}

// putObject writes one object under tenant into the shared blob store.
func putObject(t *testing.T, w *World, tenant string) {
	t.Helper()
	u := blobstore.URI{TenantID: tenant, ObjectType: blobstore.ObjectTypeCheckpoint, SessionID: "planted", PartID: "0", TTL: time.Hour}
	if _, err := w.Objects.Put(u, "text/plain", strings.NewReader("x")); err != nil {
		t.Fatalf("plant object: %v", err)
	}
}

// TestDeletionPhase3And4ErrorsPauseInPhase_spec_12_8 fails each Phase 3
// step that can return an error and each Phase 4 entry once and asserts the
// job pauses in phase with a blocked reason, and that the next pass
// advances it with the phase-exit invariants holding.
//
// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle), 12.8 (Compliance Interfaces: Idempotency and resumption)
func TestDeletionPhase3And4ErrorsPauseInPhase_spec_12_8(t *testing.T) {
	ctx := context.Background()
	steps := append(append([]DeletionStep(nil), Phase3Steps...), Phase4Entries...)
	for _, s := range steps {
		t.Run(string(s), func(t *testing.T) {
			w, _, d := startedPair(t)
			want := tenantdeletion.PhaseRevokeCredentials
			if !isPhase3Step(s) {
				want = tenantdeletion.PhaseDeleteData
			}
			if p, err := d.until(ctx, 10, want); err != nil || p != want {
				t.Fatalf("advance to %s = %s, %v", want, p, err)
			}
			w.Faults.FailDeletionStep(s, 1)
			p, err := d.Tick(ctx)
			if err != nil || p != want {
				t.Fatalf("faulted tick = %s, %v; want paused at %s", p, err, want)
			}
			if j, _ := d.Job(ctx); !strings.Contains(j.BlockedReason, string(s)) {
				t.Fatalf("blocked reason %q does not name %s", j.BlockedReason, s)
			}
			if err := d.Complete(ctx, 10); err != nil {
				t.Fatal(err)
			}
			w.Check(t)
		})
	}
}

// isPhase3Step reports whether s is one of the Phase 3 steps.
func isPhase3Step(s DeletionStep) bool {
	for _, p := range append([]DeletionStep{StepReturnLeases}, Phase3Steps...) {
		if s == p {
			return true
		}
	}
	return false
}

// TestDeletionTakeoverRebuildsAtPhase3AndHoldPauses_spec_12_8 stops a
// replica after the Phase 3 commit, takes the tick over on a fresh replica,
// which rebuilds the job at Phase 3 from the deleting row, holds the
// deletion at Phase 3.5 with the tenant unregistered, and then completes it.
//
// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle), 12.8 (Compliance Interfaces: Idempotency and resumption), 10.2 (Authentication)
func TestDeletionTakeoverRebuildsAtPhase3AndHoldPauses_spec_12_8(t *testing.T) {
	ctx := context.Background()
	w, _, d := startedPair(t)
	if _, err := d.until(ctx, 10, tenantdeletion.PhaseRevokeCredentials); err != nil {
		t.Fatal(err)
	}
	crashCtx, crash := context.WithCancel(ctx)
	w.Faults.AfterDeletionStep(func(s DeletionStep) {
		if s == StepCommitDeleting {
			crash()
		}
	})
	if p, _ := d.Tick(crashCtx); p != tenantdeletion.PhaseRevokeCredentials {
		t.Fatalf("crashed tick left %s", p)
	}
	w.Faults.AfterDeletionStep(nil)
	next, err := w.TakeOverDeletion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if j, _ := next.Job(ctx); j.Phase != tenantdeletion.PhaseRevokeCredentials {
		t.Fatalf("takeover rebuilt the job at %s, want Phase 3", j.Phase)
	}
	next.Hold(true)
	if p, err := next.until(ctx, 4, tenantdeletion.PhaseDeleteData); err == nil || p != tenantdeletion.PhaseLegalHoldSegregation {
		t.Fatalf("held deletion = %s, %v; want paused at Phase 3.5", p, err)
	}
	if reg, err := w.registered(ctx); err != nil || reg {
		t.Fatalf("tenant registered = %v (%v) during the Phase 3.5 pause", reg, err)
	}
	next.Hold(false)
	if err := next.Complete(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if tn, err := w.S.Tenants.Get(ctx, w.Tenant); err != nil || tn.State != tenantstore.TenantStateDeleted && tn.DeletedAt.IsZero() {
		t.Fatalf("tenant after completion = %+v (%v), want the tombstone", tn, err)
	}
	w.Check(t)
}

// TestFullRevokeContinuesPastDisconnect_spec_11_4 cancels a full_revoke
// request right after the user row commits and asserts the production
// handler still cancels every session of the user and returns every lease
// they hold, while another user's sessions and leases are untouched.
//
// spec: 11.4 (User Invalidation: Full revoke propagation mechanism)
func TestFullRevokeContinuesPastDisconnect_spec_11_4(t *testing.T) {
	ctx := context.Background()
	w := NewWorld(t, MemoryStores(), 2)
	res := w.FullRevoke(ctx, "carol", true)
	if res.Status != 200 || res.LeasesRevoked != 2 {
		t.Fatalf("full_revoke = %d leases %d: %s", res.Status, res.LeasesRevoked, res.Body)
	}
	if held := w.Leases.Held(w.Tenant, "alice"); len(held) != 2 {
		t.Fatalf("alice holds %d leases after carol's full_revoke, want 2", len(held))
	}
	if live, _ := w.LiveInScope(ctx, "alice"); len(live) != 2 {
		t.Fatalf("alice has %d live sessions after carol's full_revoke, want 2", len(live))
	}
	w.Check(t)
}

// TestFullRevokeCheckFlagsLiveSession_spec_11_4 pins the full_revoke
// postcondition check: a live session of the user that predates the call
// is a violation.
//
// spec: 11.4 (User Invalidation: Full revoke propagation mechanism)
func TestFullRevokeCheckFlagsLiveSession_spec_11_4(t *testing.T) {
	w := NewWorld(t, MemoryStores(), 1)
	w.assertRevoked(context.Background(), "bob", w.next(), 0)
	if got := w.violationsContaining("left session"); len(got) != 1 {
		t.Fatalf("violations = %v, want the live session", got)
	}
	if got := w.violationsContaining("still holds leases"); len(got) != 1 {
		t.Fatalf("violations = %v, want the held lease", got)
	}
}
