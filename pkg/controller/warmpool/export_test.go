// SPDX-License-Identifier: MIT

package warmpool

import "context"

// SweepForTest runs one orphan-claim detection pass. It is the internal
// sweep exposed for the package's external tests.
func (g *ClaimGarbageCollector) SweepForTest(ctx context.Context) error {
	return g.sweep(ctx)
}

// ReconcileForTest runs one bulk mirror reconciliation pass.
func (m *MirrorReconciler) ReconcileForTest(ctx context.Context) error {
	return m.reconcile(ctx)
}
