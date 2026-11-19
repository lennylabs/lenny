// SPDX-License-Identifier: MIT

package platformtools_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/platformtools"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/session/state"
)

// fakeNodes is the session core's side of the tree recovery.
type fakeNodes struct {
	mu       sync.Mutex
	tree     []seam.Session
	listErr  error
	started  []string
	failed   []string
	reattach func(seam.Session) error
}

func (f *fakeNodes) ListTree(context.Context, string, string) ([]seam.Session, error) {
	return f.tree, f.listErr
}

func (f *fakeNodes) StartNode(_ context.Context, n seam.Session) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, n.ID)
}

func (f *fakeNodes) ReattachNode(_ context.Context, n seam.Session) error {
	return f.reattach(n)
}

func (f *fakeNodes) FailNode(_ context.Context, n seam.Session, _ string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = append(f.failed, n.ID)
}

// recoveryMetrics records the recovery's outcome.
type recoveryMetrics struct {
	pools, outcomes []string
}

func (m *recoveryMetrics) ObserveTreeRecoveryDuration(pool, outcome string, _ float64) {
	m.pools = append(m.pools, pool)
	m.outcomes = append(m.outcomes, outcome)
}

func (m *recoveryMetrics) IncTreeRecoveryTimeout(string, string) {}

// TestRecoverTreeDrivesOnlyResumePendingNodesThroughTheCore checks that the
// recovery selects the tree's resume_pending nodes, starts and waits for
// each through the core's tree node operations, fails the one the core
// could not restore, and reports the summary and the root's pool.
//
// spec: GWSEAM-RecoverTree, GWSEAM-ListTree, GWSEAM-StartNode, GWSEAM-ReattachNode, GWSEAM-FailNode
func TestRecoverTreeDrivesOnlyResumePendingNodesThroughTheCore(t *testing.T) {
	nodes := &fakeNodes{
		tree: []seam.Session{
			{ID: "root", TenantID: "acme", Pool: "pool-a", State: "running"},
			{ID: "ok", TenantID: "acme", ParentSessionID: "root", DelegationDepth: 1, State: state.State("resume_pending")},
			{ID: "bad", TenantID: "acme", ParentSessionID: "root", DelegationDepth: 1, State: state.State("resume_pending")},
			{ID: "running", TenantID: "acme", ParentSessionID: "root", DelegationDepth: 1, State: "running"},
			{ID: "done", TenantID: "acme", ParentSessionID: "root", DelegationDepth: 1, State: "completed"},
		},
		reattach: func(n seam.Session) error {
			if n.ID == "bad" {
				return errors.New("restore ended in awaiting_client_action")
			}
			return nil
		},
	}
	m := &recoveryMetrics{}
	r := platformtools.NewTreeRecovery(platformtools.RecoveryConfig{Metrics: m, LevelTimeout: time.Second, TreeTimeout: time.Minute})
	sum, err := r.RecoverTree(context.Background(), "acme", "root", nodes)
	if err != nil {
		t.Fatal(err)
	}
	if sum != (seam.TreeRecoverySummary{Recoverable: 2, Recovered: 1, Failed: 1}) {
		t.Errorf("summary = %+v, want 2 recoverable, 1 recovered, 1 failed", sum)
	}
	if len(nodes.started) != 2 {
		t.Errorf("started = %v, want only the resume_pending nodes", nodes.started)
	}
	if len(nodes.failed) != 1 || nodes.failed[0] != "bad" {
		t.Errorf("failed = %v, want [bad]", nodes.failed)
	}
	if len(m.pools) != 1 || m.pools[0] != "pool-a" || m.outcomes[0] != "partial_failure" {
		t.Errorf("metrics = %v %v, want pool-a partial_failure", m.pools, m.outcomes)
	}
}

// TestRecoverTreeReportsAListingFailure checks that a tree the core cannot
// list fails the recovery with the core's error and touches no node.
//
// spec: GWSEAM-RecoverTree
func TestRecoverTreeReportsAListingFailure(t *testing.T) {
	nodes := &fakeNodes{listErr: errors.New("store down")}
	if _, err := platformtools.NewTreeRecovery(platformtools.RecoveryConfig{}).RecoverTree(context.Background(), "acme", "root", nodes); !errors.Is(err, nodes.listErr) {
		t.Fatalf("err = %v, want the listing error", err)
	}
	if len(nodes.started)+len(nodes.failed) != 0 {
		t.Errorf("a failed listing touched nodes: started %v failed %v", nodes.started, nodes.failed)
	}
}
