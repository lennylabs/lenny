// SPDX-License-Identifier: MIT

package warmpool_test

import (
	"context"
	"sort"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/agentpodstate"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
)

// recordingMirror captures the last ReconcileAll input for assertions.
type recordingMirror struct {
	reconciled []agentpodstate.PodState
	calls      int
}

func (m *recordingMirror) Sync(context.Context, string, []agentpodstate.PodState) error {
	return nil
}

func (m *recordingMirror) ReconcileAll(_ context.Context, observed []agentpodstate.PodState) error {
	m.calls++
	m.reconciled = observed
	return nil
}

func (m *recordingMirror) MirrorLagSeconds(context.Context, string) (float64, error) {
	return 0, nil
}

func (m *recordingMirror) GetByPodID(context.Context, string) (agentpodstate.PodState, bool, error) {
	return agentpodstate.PodState{}, false, nil
}

func (m *recordingMirror) ClaimIdle(context.Context, string, string, string, []string, agentpodstate.AdmitFunc) (agentpodstate.PodState, bool, error) {
	return agentpodstate.PodState{}, false, nil
}

func (m *recordingMirror) IncrementSessionsServed(context.Context, string) (int, bool, error) {
	return 0, false, nil
}

func (m *recordingMirror) IncrementScrubFailureCount(context.Context, string) (int, bool, error) {
	return 0, false, nil
}

func (m *recordingMirror) RecycleCounters(context.Context, string) (agentpodstate.RecycleCounters, bool, error) {
	return agentpodstate.RecycleCounters{}, false, nil
}

// spec: §4.6.1 "WarmPoolController mirror reconciliation on recovery" —
// the runnable re-lists every Sandbox in the agent namespaces and
// converges the full mirror in one bulk ReconcileAll.
func TestMirrorRecoveryReconcilesAllSandboxes(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, template(), pool(2, 5), idleSandbox("pod-1"), idleSandbox("pod-2"))

	mirror := &recordingMirror{}
	m := &warmpool.MirrorReconciler{
		Client:     c,
		Mirror:     mirror,
		Namespaces: []string{testNS},
	}
	if err := m.ReconcileForTest(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if mirror.calls != 1 {
		t.Fatalf("ReconcileAll calls = %d, want 1", mirror.calls)
	}
	got := podIDs(mirror.reconciled)
	want := []string{"pod-1", "pod-2"}
	if len(got) != len(want) {
		t.Fatalf("reconciled pods = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("reconciled pods = %v, want %v", got, want)
		}
	}
	for _, row := range mirror.reconciled {
		if row.PoolID != testPool {
			t.Errorf("row %s pool = %q, want %q", row.PodID, row.PoolID, testPool)
		}
	}
}

// spec: §4.6.1 — with no live Sandboxes the recovery pass still runs
// ReconcileAll (with an empty set), which prunes every stale mirror row.
func TestMirrorRecoveryEmptyClusterPrunesMirror(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, template(), pool(2, 5))

	mirror := &recordingMirror{}
	m := &warmpool.MirrorReconciler{Client: c, Mirror: mirror, Namespaces: []string{testNS}}
	if err := m.ReconcileForTest(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if mirror.calls != 1 {
		t.Fatalf("ReconcileAll calls = %d, want 1", mirror.calls)
	}
	if len(mirror.reconciled) != 0 {
		t.Errorf("reconciled rows = %d, want 0", len(mirror.reconciled))
	}
}

func TestMirrorRecoveryNeedsLeaderElection(t *testing.T) {
	m := &warmpool.MirrorReconciler{}
	if !m.NeedLeaderElection() {
		t.Error("mirror recovery must run only on the elected leader")
	}
}

// listOptionRecorder is a client whose List records the options of each call
// and returns an empty list. Every other method is the nil embedded client
// and panics if reached, so the test also proves the pass issues only Lists
// when no Sandbox exists.
type listOptionRecorder struct {
	client.Client
	lists []*client.ListOptions
}

func (r *listOptionRecorder) List(_ context.Context, _ client.ObjectList, opts ...client.ListOption) error {
	lo := &client.ListOptions{}
	lo.ApplyOptions(opts)
	r.lists = append(r.lists, lo)
	return nil
}

// spec: §4.6.1 "WarmPoolController mirror reconciliation on recovery"
//
// TestMirrorRecoveryListsWithoutDeepCopy pins that the recovery pass reads the
// cached Sandbox list without the per-object deep copy. The pass reads every
// Sandbox in every agent namespace, terminal ones included; with about 16,000
// Sandboxes on the e2e cluster the default deep copy doubled the informer's
// footprint in one burst and OOM-killed the controller at the chart's 256Mi
// limit as soon as the chart rendered --agent-namespaces (F-4.6.30). A pass
// that drops client.UnsafeDisableDeepCopy, or lists a namespace outside the
// configured set, fails here.
func TestMirrorRecoveryListsWithoutDeepCopy(t *testing.T) {
	rec := &listOptionRecorder{}
	mirror := &recordingMirror{}
	m := &warmpool.MirrorReconciler{Client: rec, Mirror: mirror, Namespaces: []string{"acme-agents", "globex-agents"}}
	if err := m.ReconcileForTest(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(rec.lists) != 2 {
		t.Fatalf("List calls = %d, want one per agent namespace (2)", len(rec.lists))
	}
	for i, want := range []string{"acme-agents", "globex-agents"} {
		lo := rec.lists[i]
		if lo.Namespace != want {
			t.Errorf("List %d namespace = %q, want %q", i, lo.Namespace, want)
		}
		if lo.UnsafeDisableDeepCopy == nil || !*lo.UnsafeDisableDeepCopy {
			t.Errorf("List %d in %s deep-copies every Sandbox; the recovery pass must pass client.UnsafeDisableDeepCopy", i, want)
		}
	}
	if mirror.calls != 1 {
		t.Errorf("ReconcileAll calls = %d, want 1", mirror.calls)
	}
}

func podIDs(rows []agentpodstate.PodState) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.PodID)
	}
	sort.Strings(out)
	return out
}
