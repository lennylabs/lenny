// SPDX-License-Identifier: MIT

package warmpool

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// PodsOnNodeForTest exposes the §4.6.1 Node→Pod fan-out mapping for the
// package's external tests.
func (r *PodReconciler) PodsOnNodeForTest(ctx context.Context, node client.Object) []reconcile.Request {
	return r.podsOnNode(ctx, node)
}

// SweepForTest runs one orphan-claim detection pass. It is the internal
// sweep exposed for the package's external tests.
func (g *ClaimGarbageCollector) SweepForTest(ctx context.Context) error {
	return g.sweep(ctx)
}

// ReclaimReservedForTest runs the §4.6.1 precondition-guarded reserved-claim
// reclaim against the supplied claim view, so the package's external tests can
// exercise the rebind-vs-reclaim precondition race with a stale claim
// resourceVersion.
func (g *ClaimGarbageCollector) ReclaimReservedForTest(ctx context.Context, claim *lennyv1.SandboxClaim) error {
	return g.reclaimReserved(ctx, claim)
}

// ReconcileForTest runs one bulk mirror reconciliation pass.
func (m *MirrorReconciler) ReconcileForTest(ctx context.Context) error {
	return m.reconcile(ctx)
}
