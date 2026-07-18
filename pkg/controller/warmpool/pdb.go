// SPDX-License-Identifier: MIT

package warmpool

import (
	"context"
	"fmt"

	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// pdbName is the name of the §4.6.1 per-pool PodDisruptionBudget. One
// PDB per SandboxWarmPool protects that pool's warm pods.
func pdbName(pool string) string { return pool + "-warm" }

// reconcilePDB maintains the §4.6.1 per-pool PodDisruptionBudget for
// warm (idle) pods. For a pool with minWarm >= 2 it owns a PDB with an
// integer minAvailable of minWarm-1 selecting that pool's idle pods. An
// integer minAvailable is evaluated directly from the count of selected
// healthy pods, so it needs no /scale subresource; the selected warm
// pods are owned by a status-only Sandbox CR that has none, and a
// maxUnavailable budget would deadlock at disruptionsAllowed: 0. At the
// steady state of exactly minWarm idle pods, minWarm-1 admits one
// eviction and blocks a concurrent second, giving one-at-a-time
// voluntary disruption. A pool with minWarm below 2 gets no PDB, because
// a single warm pod cannot be given one-at-a-time protection without
// deadlocking, so the PDB is torn down in that case. spec: §4.6.1
// "Disruption protection for agent pods".
func (r *Reconciler) reconcilePDB(ctx context.Context, pool *lennyv1.SandboxWarmPool) error {
	key := client.ObjectKey{Namespace: pool.Namespace, Name: pdbName(pool.Name)}

	if pool.Spec.MinWarm < 2 {
		var existing policyv1.PodDisruptionBudget
		if err := r.Client.Get(ctx, key, &existing); err != nil {
			return client.IgnoreNotFound(err)
		}
		if err := r.Client.Delete(ctx, &existing); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete pdb for pool %s: %w", pool.Name, err)
		}
		return nil
	}

	minAvailable := intstr.FromInt(int(pool.Spec.MinWarm) - 1)
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      key.Name,
			Namespace: key.Namespace,
			Labels: map[string]string{
				LabelPool:    pool.Name,
				LabelManaged: "true",
			},
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			// spec: §4.6.1 — an integer minAvailable of minWarm-1. A
			// maxUnavailable budget cannot resolve expectedPods because the
			// selected warm pods are owned by a status-only Sandbox CR with no
			// /scale subresource; an integer minAvailable is evaluated from the
			// selected healthy-pod count. minWarm-1 admits exactly one eviction
			// at steady state (minWarm idle pods) and avoids the minAvailable:
			// minWarm deadlock.
			MinAvailable: &minAvailable,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					LabelPool:        pool.Name,
					state.LabelState: string(state.Idle),
				},
			},
		},
	}
	if err := ctrl.SetControllerReference(pool, pdb, r.Scheme); err != nil {
		return fmt.Errorf("set pdb owner for pool %s: %w", pool.Name, err)
	}

	var existing policyv1.PodDisruptionBudget
	err := r.Client.Get(ctx, key, &existing)
	if apierrors.IsNotFound(err) {
		if err := r.Client.Create(ctx, pdb); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create pdb for pool %s: %w", pool.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("get pdb for pool %s: %w", pool.Name, err)
	}
	// Converge the spec in case minWarm/selector drifted; the selector,
	// minAvailable, and the cleared maxUnavailable are the fields the
	// controller owns. maxUnavailable is set to nil so an object previously
	// reconciled with maxUnavailable: 1 passes the exactly-one-of validation.
	existing.Spec.MinAvailable = &minAvailable
	existing.Spec.MaxUnavailable = nil
	existing.Spec.Selector = pdb.Spec.Selector
	if err := r.Client.Update(ctx, &existing); err != nil && !apierrors.IsConflict(err) {
		return fmt.Errorf("update pdb for pool %s: %w", pool.Name, err)
	}
	return nil
}
