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

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// pdbName is the name of the §4.6.1 per-pool PodDisruptionBudget. One
// PDB per SandboxWarmPool protects that pool's warm pods.
func pdbName(pool string) string { return pool + "-warm" }

// reconcilePDB maintains the §4.6.1 per-pool PodDisruptionBudget for
// warm (idle) pods. For a pool with a positive minWarm it owns a PDB
// with maxUnavailable: 1 selecting that pool's idle pods, so a node
// drain evicts warm pods one at a time rather than all at once. The
// spec forbids minAvailable: minWarm because it deadlocks node drains at
// steady state. When minWarm is zero the PDB is torn down so a
// scaled-to-zero pool imposes no disruption budget. spec: §4.6.1
// "Disruption protection for agent pods".
func (r *Reconciler) reconcilePDB(ctx context.Context, pool *lennyv1.SandboxWarmPool) error {
	key := client.ObjectKey{Namespace: pool.Namespace, Name: pdbName(pool.Name)}

	if pool.Spec.MinWarm <= 0 {
		var existing policyv1.PodDisruptionBudget
		if err := r.Client.Get(ctx, key, &existing); err != nil {
			return client.IgnoreNotFound(err)
		}
		if err := r.Client.Delete(ctx, &existing); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete pdb for pool %s: %w", pool.Name, err)
		}
		return nil
	}

	maxUnavailable := intstr.FromInt(1)
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
			// spec: §4.6.1 — "The PDB MUST use maxUnavailable: 1 rather
			// than minAvailable: minWarm." minAvailable: minWarm deadlocks
			// node drains when exactly minWarm idle pods exist.
			MaxUnavailable: &maxUnavailable,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					LabelPool:       pool.Name,
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
	// Converge the spec in case minWarm/selector drifted; the selector and
	// maxUnavailable are the only fields the controller owns.
	existing.Spec.MaxUnavailable = &maxUnavailable
	existing.Spec.Selector = pdb.Spec.Selector
	if err := r.Client.Update(ctx, &existing); err != nil && !apierrors.IsConflict(err) {
		return fmt.Errorf("update pdb for pool %s: %w", pool.Name, err)
	}
	return nil
}
