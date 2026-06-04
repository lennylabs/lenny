// SPDX-License-Identifier: MIT

package warmpool

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/lennylabs/lenny/pkg/agentpodstate"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// MirrorReconciler is the §4.6.1 "WarmPoolController mirror reconciliation
// on recovery" runnable. On startup and on every leader-election
// acquisition it re-lists all Sandbox resources in the agent namespaces,
// re-derives the full agent_pod_state row set, and converges the Postgres
// mirror in a single bulk UPSERT, deleting rows whose pod_id no longer
// corresponds to any live Sandbox. This re-establishes the steady-state
// invariant that the mirror matches the authoritative etcd state before
// the per-pool reconciler resumes incremental state-transition writes.
//
// The per-pool Reconciler.syncMirror handles incremental convergence once
// the controller is running; this runnable handles the bulk catch-up a
// crash or failover leaves behind, where the mirror would otherwise stay
// stale until each pool's next SandboxWarmPool event.
type MirrorReconciler struct {
	// Client is the controller-runtime client.
	Client client.Client
	// Mirror is the §4.6.1 agent_pod_state store. When nil the runnable is
	// disabled (no Postgres configured, so no mirror to reconcile).
	Mirror agentpodstate.Store
	// Namespaces is the set of agent namespaces whose Sandbox resources
	// back the mirror. An empty slice disables the runnable.
	Namespaces []string
}

var (
	_ manager.Runnable               = (*MirrorReconciler)(nil)
	_ manager.LeaderElectionRunnable = (*MirrorReconciler)(nil)
)

// NeedLeaderElection reports that the bulk reconciliation runs only in the
// elected leader, so the controller-runtime invokes Start once per
// leadership acquisition. spec: §4.6.1 — "On startup and on every
// leader-election acquisition".
func (m *MirrorReconciler) NeedLeaderElection() bool { return true }

// Start runs the bulk mirror reconciliation once, then blocks until ctx is
// cancelled. Because NeedLeaderElection is true, the manager calls Start
// after acquiring the lease, so the single pass executes exactly on
// leadership acquisition. A failure is logged; the per-pool reconciler's
// incremental syncMirror still converges each pool on its next event.
func (m *MirrorReconciler) Start(ctx context.Context) error {
	logger := logf.FromContext(ctx).WithName("mirror-recovery")
	if m.Mirror == nil || len(m.Namespaces) == 0 {
		logger.Info("no mirror store or agent namespaces configured; mirror recovery disabled")
		return nil
	}
	if err := m.reconcile(ctx); err != nil {
		logger.Error(err, "mirror reconciliation on leader acquisition failed")
	} else {
		logger.Info("mirror reconciliation on leader acquisition complete")
	}
	<-ctx.Done()
	return nil
}

// reconcile re-derives the full agent_pod_state row set from the live
// Sandbox resources across the agent namespaces and bulk-converges the
// mirror. Rows are derived per pool (reusing derivePodStates) so the
// projection matches the per-pool incremental sync exactly; execution
// mode and isolation profile come from each pool's SandboxTemplate.
func (m *MirrorReconciler) reconcile(ctx context.Context) error {
	var observed []agentpodstate.PodState
	tmplCache := map[string]*lennyv1.SandboxTemplate{}

	for _, ns := range m.Namespaces {
		var sandboxes lennyv1.SandboxList
		if err := m.Client.List(ctx, &sandboxes, client.InNamespace(ns)); err != nil {
			return fmt.Errorf("list sandboxes in %s: %w", ns, err)
		}
		byPool := map[string][]lennyv1.Sandbox{}
		for i := range sandboxes.Items {
			sb := sandboxes.Items[i]
			pool := sb.Labels[LabelPool]
			if pool == "" {
				pool = sb.Spec.PoolRef
			}
			byPool[pool] = append(byPool[pool], sb)
		}
		for poolName, items := range byPool {
			pool := &lennyv1.SandboxWarmPool{}
			pool.Name = poolName
			pool.Namespace = ns
			tmpl := m.templateFor(ctx, ns, poolName, tmplCache)
			observed = append(observed, derivePodStates(pool, tmpl, items)...)
		}
	}

	return m.Mirror.ReconcileAll(ctx, observed)
}

// templateFor resolves the SandboxTemplate backing a pool, caching the
// lookup across namespaces. A pool whose template cannot be read derives
// rows with an empty execution mode rather than failing the whole
// reconciliation; the recovery pass favors converging the mirror over
// strict per-field fidelity.
func (m *MirrorReconciler) templateFor(ctx context.Context, namespace, poolName string, cache map[string]*lennyv1.SandboxTemplate) *lennyv1.SandboxTemplate {
	key := namespace + "/" + poolName
	if t, ok := cache[key]; ok {
		return t
	}
	var pool lennyv1.SandboxWarmPool
	if err := m.Client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: poolName}, &pool); err != nil {
		cache[key] = nil
		return nil
	}
	var tmpl lennyv1.SandboxTemplate
	if err := m.Client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: pool.Spec.TemplateRef}, &tmpl); err != nil {
		cache[key] = nil
		return nil
	}
	cache[key] = &tmpl
	return &tmpl
}
