// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync/atomic"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
)

// slotHolderFixture is a funnel fixture whose executor releases slots
// through a real PodExecutor and binder over a miniredis slot set, with a
// live sibling on pod-a.
type slotHolderFixture struct {
	*funnelFixture
	mr      *miniredis.Miniredis
	counter *slotcounter.Counter
	failUpd *failingUpdateStore
}

// failingUpdateStore fails every Update while fail is set.
type failingUpdateStore struct {
	sessionstore.Store
	fail atomic.Bool
}

func (s *failingUpdateStore) Update(ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error) (sessionstore.Session, error) {
	if s.fail.Load() {
		return sessionstore.Session{}, errors.New("postgres unavailable")
	}
	return s.Store.Update(ctx, tenantID, id, mutate)
}

const perSlotScrub = "best-effort-per-slot"

func newSlotHolderFixture(t *testing.T) *slotHolderFixture {
	t.Helper()
	f := newFunnelFixture(t, funnelOpts{})
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	counter := slotcounter.New(rc, slotcounter.WithSlotSource(f.store), slotcounter.WithFallbackSource(f.store))
	f.srv.podBinder.SlotCounter = counter
	f.srv.executor = executor.NewPodExecutor(f.registry, f.srv.podBinder)
	fu := &failingUpdateStore{Store: f.srv.store}
	f.srv.store = fu
	h := &slotHolderFixture{funnelFixture: f, mr: mr, counter: counter, failUpd: fu}
	h.seedSlotHolder(t, "sib", 0)
	return h
}

// seedSlotHolder seeds a running per-slot row on pod-a, binds it to a slot
// on pod-a, and reserves its member.
func (h *slotHolderFixture) seedSlotHolder(t *testing.T, id string, retries int64) sessionstore.Session {
	t.Helper()
	h.seed(t, id, session.StateRunning, retries, true)
	row, err := h.store.Update(context.Background(), "acme", id, func(r *sessionstore.Session) error {
		r.PodAssignment = "pod-a"
		r.ScrubPolicy = perSlotScrub
		return nil
	})
	if err != nil {
		t.Fatalf("name pod-a on %s: %v", id, err)
	}
	if _, _, err := h.counter.Reserve(context.Background(), "pod-a", id, 4); err != nil {
		t.Fatalf("reserve %s: %v", id, err)
	}
	return row
}

func (h *slotHolderFixture) members(t *testing.T) string {
	t.Helper()
	m, err := h.mr.Members("lenny:pod:{pod-a}:active_slots")
	if err != nil && h.mr.Exists("lenny:pod:{pod-a}:active_slots") {
		t.Fatalf("members: %v", err)
	}
	sort.Strings(m)
	return fmt.Sprint(m)
}

func (h *slotHolderFixture) podOf(t *testing.T, id string) string {
	t.Helper()
	row, err := h.store.Get(context.Background(), "acme", id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return row.PodAssignment
}

// rehydrateWithNewSession deletes pod-a's rehydrated flag, as the recovery
// rebuild does, and reserves a new session, so the rehydration seeds the set
// from the rows.
func (h *slotHolderFixture) rehydrateWithNewSession(t *testing.T) {
	t.Helper()
	h.mr.Del("lenny:pod:{pod-a}:rehydrated")
	if _, _, err := h.counter.Reserve(context.Background(), "pod-a", "new", 4); err != nil {
		t.Fatalf("reserve new: %v", err)
	}
}

// TestFunnelRecoveryEdgesKeepSlotHolderPredicate_spec_5_2 pins the
// slot-holder predicate on the failure funnel's recovery edges: each edge
// leaves the stored row naming no pod and removes the session's member, so a
// rehydration after the recovery rebuild deletes the flag does not add the
// released session again, and the §12.4 fallback gate counts the sibling
// and the new session's reservation record but not the released session.
//
// spec: §5.2 (Post-recovery rehydration atomicity), §7.3 (Resume flow after
// pod failure), §12.4 (Redis failure behavior)
func TestFunnelRecoveryEdgesKeepSlotHolderPredicate_spec_5_2(t *testing.T) {
	for _, tc := range []struct {
		name    string
		retries int64
		want    session.State
	}{
		{"resume_pending edge", 0, session.StateResumePending},
		{"awaiting_client_action edge", 2, session.StateAwaitingClientAction},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newSlotHolderFixture(t)
			snap := h.seedSlotHolder(t, "sess-s", tc.retries)
			disp, err := h.report(context.Background(), snap, "runtime_crash")
			if err != nil || disp.To != tc.want {
				t.Fatalf("report = (%+v, %v), want %s", disp, err, tc.want)
			}
			if pod := h.podOf(t, "sess-s"); pod != "" {
				t.Fatalf("pod_assignment = %q, want it cleared by the edge write", pod)
			}
			if got := h.members(t); got != "[sib]" {
				t.Fatalf("members after the funnel release = %s, want [sib]", got)
			}
			h.rehydrateWithNewSession(t)
			if got := h.members(t); got != "[new sib]" {
				t.Fatalf("members after rehydration = %s, want [new sib]", got)
			}
			// The gate counts the sibling and new's reservation record
			// (F-12.4.38), plus the probe: the released session's record
			// went with its release.
			count, admitted, err := h.store.ReserveSlotUnderLock(context.Background(), "pod-a", "probe", 4)
			if err != nil || !admitted || count != 3 {
				t.Fatalf("ReserveSlotUnderLock = (%d, %v, %v), want (3, true, nil): the sibling and new only", count, admitted, err)
			}
		})
	}
}

// TestPersistPodAssignmentSkipsRecoveryRow_spec_5_2 pins that a binding
// publish whose persistPodAssignment runs after the funnel's edge write does
// not name the pod again.
//
// spec: §5.2 (Post-recovery rehydration atomicity), §4.2 (Pod-to-session binding)
func TestPersistPodAssignmentSkipsRecoveryRow_spec_5_2(t *testing.T) {
	h := newSlotHolderFixture(t)
	snap := h.seedSlotHolder(t, "sess-p", 0)
	if _, err := h.report(context.Background(), snap, "runtime_crash"); err != nil {
		t.Fatalf("report: %v", err)
	}
	h.srv.persistPodAssignment(context.Background(), "acme", "sess-p", "pod-a")
	if pod := h.podOf(t, "sess-p"); pod != "" {
		t.Fatalf("pod_assignment = %q, want it left empty on a recovery row", pod)
	}
	// A non-recovery row still gets the pod.
	h.srv.persistPodAssignment(context.Background(), "acme", "sib", "pod-b")
	if pod := h.podOf(t, "sib"); pod != "pod-b" {
		t.Fatalf("pod_assignment of a running row = %q, want pod-b", pod)
	}
}

// moveToAwaitingWhileBound moves id to awaiting_client_action without
// releasing its binding or clearing its pod, as MarkBudgetUnrecoverable does
// to a tree root.
func (h *slotHolderFixture) moveToAwaitingWhileBound(t *testing.T, id string) sessionstore.Session {
	t.Helper()
	row, err := h.store.Update(context.Background(), "acme", id, func(r *sessionstore.Session) error {
		r.State = session.StateAwaitingClientAction
		return nil
	})
	if err != nil {
		t.Fatalf("move %s to awaiting_client_action: %v", id, err)
	}
	return row
}

// TestReleaseEarlierBindingClearsRecoveryPod_spec_5_2 pins the guarded clear
// before the earlier binding's release: a root MarkBudgetUnrecoverable moved
// to awaiting_client_action while bound names no pod after
// releaseEarlierBinding and before any claim, its member is removed, and the
// next rehydration does not add it again. A store error on the clear returns
// before any claim with the binding kept.
//
// spec: §5.2 (Post-recovery rehydration atomicity), §7.3 (Resume flow after
// pod failure)
func TestReleaseEarlierBindingClearsRecoveryPod_spec_5_2(t *testing.T) {
	t.Run("clear then release", func(t *testing.T) {
		h := newSlotHolderFixture(t)
		h.seedSlotHolder(t, "root", 0)
		row := h.moveToAwaitingWhileBound(t, "root")
		if err := h.srv.releaseEarlierBinding(context.Background(), row); err != nil {
			t.Fatalf("releaseEarlierBinding: %v", err)
		}
		if pod := h.podOf(t, "root"); pod != "" {
			t.Fatalf("pod_assignment = %q, want it cleared before the release", pod)
		}
		if _, ok := h.registry.Get("root"); ok {
			t.Fatal("the earlier binding is still registered")
		}
		if got := h.members(t); got != "[sib]" {
			t.Fatalf("members = %s, want [sib]", got)
		}
		h.rehydrateWithNewSession(t)
		if got := h.members(t); got != "[new sib]" {
			t.Fatalf("members after rehydration = %s, want [new sib]", got)
		}
	})
	t.Run("store error keeps the binding", func(t *testing.T) {
		h := newSlotHolderFixture(t)
		h.seedSlotHolder(t, "root", 0)
		row := h.moveToAwaitingWhileBound(t, "root")
		h.failUpd.fail.Store(true)
		err := h.srv.releaseEarlierBinding(context.Background(), row)
		h.failUpd.fail.Store(false)
		if err == nil {
			t.Fatal("releaseEarlierBinding = nil, want the clear's store error")
		}
		if _, ok := h.registry.Get("root"); !ok {
			t.Fatal("a failed clear released the binding")
		}
		if pod := h.podOf(t, "root"); pod != "pod-a" {
			t.Fatalf("pod_assignment = %q, want pod-a kept", pod)
		}
		if got := h.members(t); got != "[root sib]" {
			t.Fatalf("members = %s, want [root sib]", got)
		}
	})
}

// TestReleaseEarlierBindingGuardMissStillReleases_spec_7_3 pins the clear's
// guard miss: a resume_pending row that already names no pod while the
// executor holds the session's binding is left unchanged, the binding is
// released, and releaseEarlierBinding returns nil so the attempt reaches its
// claim.
//
// spec: §7.3 (Resume flow after pod failure), §5.2 (Post-recovery
// rehydration atomicity)
func TestReleaseEarlierBindingGuardMissStillReleases_spec_7_3(t *testing.T) {
	h := newSlotHolderFixture(t)
	h.seedSlotHolder(t, "sess-g", 0)
	row, err := h.store.Update(context.Background(), "acme", "sess-g", func(r *sessionstore.Session) error {
		r.State = session.StateResumePending
		r.PodAssignment = ""
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.srv.releaseEarlierBinding(context.Background(), row); err != nil {
		t.Fatalf("releaseEarlierBinding: %v", err)
	}
	if _, ok := h.registry.Get("sess-g"); ok {
		t.Fatal("the guard miss kept the binding; the release must still run")
	}
	after, err := h.store.Get(context.Background(), "acme", "sess-g")
	if err != nil {
		t.Fatal(err)
	}
	if after.State != session.StateResumePending || after.PodAssignment != "" {
		t.Fatalf("row = %s/%q, want unchanged resume_pending with no pod", after.State, after.PodAssignment)
	}
	if got := h.members(t); got != "[sib]" {
		t.Fatalf("members = %s, want [sib]", got)
	}
}
