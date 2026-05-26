// SPDX-License-Identifier: MIT

package warmpool

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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

// ReconcileForTest runs one bulk mirror reconciliation pass.
func (m *MirrorReconciler) ReconcileForTest(ctx context.Context) error {
	return m.reconcile(ctx)
}
