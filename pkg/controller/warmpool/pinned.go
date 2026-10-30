// SPDX-License-Identifier: MIT

package warmpool

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/admission/label_immutability"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool/plan"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// executionModeService is the §5.2 service execution mode. A service-mode
// pool reads no tenant pin: the stateless router labels service pods that
// stay idle while serving, so a labelled idle service pod is not pinned
// inventory.
const executionModeService = "service"

// markPinnedIdle sets plan.Pod.Pinned on each idle pod of a session-mode pool
// that is a §5.2 pinned idle pod: its Pod carries a non-empty
// lenny.dev/tenant-id label (including `unassigned`, which no acquisition
// admits) and no claim-<name> SandboxClaim exists.
//
// The claim condition keeps an acquisition in flight counted as unpinned:
// the gateway creates the claim, writes its `bound` status, and only then
// stamps the pin. The cached client's Pod and SandboxClaim informers are not
// ordered, so the cache can show the pin before the claim it follows; a
// cached NotFound is therefore confirmed through the uncached APIReader, and
// only that read sets Pinned. A nil APIReader leaves every pod unpinned. Any
// error other than NotFound from the Pod list or either claim Get is
// returned, so the reconcile fails before the plan and is retried.
//
// spec: §5.2 (Pinned idle inventory), §4.6.1 (Warm Pool Controller).
func (r *Reconciler) markPinnedIdle(ctx context.Context, pool *lennyv1.SandboxWarmPool, tmpl *lennyv1.SandboxTemplate, pods []plan.Pod) error {
	if tmpl.Spec.ExecutionMode == executionModeService || r.APIReader == nil {
		return nil
	}
	labelled, err := r.tenantLabelledPods(ctx, pool)
	if err != nil {
		return err
	}
	for i := range pods {
		p := &pods[i]
		if p.Phase != state.Idle || !labelled[p.Name] {
			continue
		}
		claimed, err := r.podHasClaim(ctx, pool.Namespace, p.Name)
		if err != nil {
			return err
		}
		p.Pinned = !claimed
	}
	return nil
}

// tenantLabelledPods lists the pool's Pods by the pool label (the selector
// the pool's PodDisruptionBudget uses) and returns the names of those whose
// tenant pin is set.
func (r *Reconciler) tenantLabelledPods(ctx context.Context, pool *lennyv1.SandboxWarmPool) (map[string]bool, error) {
	var pods corev1.PodList
	if err := r.Client.List(ctx, &pods,
		client.InNamespace(pool.Namespace),
		client.MatchingLabels{LabelPool: pool.Name}); err != nil {
		return nil, fmt.Errorf("list pods for pool %s: %w", pool.Name, err)
	}
	labelled := make(map[string]bool, len(pods.Items))
	for i := range pods.Items {
		if pods.Items[i].Labels[label_immutability.LabelTenantID] != "" {
			labelled[pods.Items[i].Name] = true
		}
	}
	return labelled, nil
}

// podHasClaim reports whether claim-<podName> exists. It reads the cache
// first and confirms a cached NotFound through the uncached APIReader, so a
// claim the cache has not yet observed still counts.
func (r *Reconciler) podHasClaim(ctx context.Context, namespace, podName string) (bool, error) {
	key := client.ObjectKey{Namespace: namespace, Name: occupancyClaimName(podName)}
	var claim lennyv1.SandboxClaim
	err := r.Client.Get(ctx, key, &claim)
	if err == nil {
		return true, nil
	}
	if !apierrors.IsNotFound(err) {
		return false, fmt.Errorf("get claim %s: %w", key.Name, err)
	}
	err = r.APIReader.Get(ctx, key, &claim)
	if err == nil {
		return true, nil
	}
	if !apierrors.IsNotFound(err) {
		return false, fmt.Errorf("get claim %s from the API server: %w", key.Name, err)
	}
	return false, nil
}
