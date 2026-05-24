// SPDX-License-Identifier: MIT

package warmpool_test

import (
	"context"
	"sort"
	"testing"

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

func (m *recordingMirror) ClaimIdle(context.Context, string, string, string) (agentpodstate.PodState, bool, error) {
	return agentpodstate.PodState{}, false, nil
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

func podIDs(rows []agentpodstate.PodState) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.PodID)
	}
	sort.Strings(out)
	return out
}
