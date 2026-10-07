// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// spec: §8.10 (Delegation Tree Recovery), §10.1.1 (Stateless Replicas and
// Per-Session Coordination)
// diagnosis: the §8.10 tree-recovery reattach reaches resumeOnPod directly
// and runs no release of an earlier binding: it makes no executor release
// call before it claims, and the coordination lease of the descendant it
// reattaches names this replica throughout. A failure means tree recovery
// tears down a binding the resume path owns, or gives away the lease of a
// running descendant.
func TestTreeRecoveryReattachRunsNoEarlierBindingRelease_spec_8_10(t *testing.T) {
	f := newFunnelFixture(t, funnelOpts{noPool: true})
	if err := f.store.Create(context.Background(), sessionstore.Session{
		ID: "sess-child", TenantID: "acme", RuntimeRef: "echo", State: session.StateRunning,
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{Ref: "ckpt-1", Source: sessionstore.WorkspaceSnapshotCheckpoint},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	f.leases.holders[leaseK("acme", "sess-child")] = "rep-1"
	node := f.state(t, "sess-child")

	if err := (sessionNodeReattacher{s: f.srv}).ReattachNode(context.Background(), node); err == nil {
		t.Fatal("ReattachNode succeeded with no pool to claim from")
	}
	if calls := f.exec.callList(); len(calls) != 0 {
		t.Errorf("executor release calls = %v, want none", calls)
	}
	if h := f.leaseHolder("sess-child"); h != "rep-1" {
		t.Errorf("lease holder = %q, want rep-1", h)
	}
}
