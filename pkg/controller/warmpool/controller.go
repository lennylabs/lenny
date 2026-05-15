// SPDX-License-Identifier: MIT

// Package warmpool holds the §4.6.1 WarmPoolController. The Reconciler
// drives one SandboxWarmPool toward its minWarm/maxWarm target by
// creating and draining Sandbox resources, and writes the observed
// warm and ready counts back to the pool's status subresource.
//
// The create/drain decision is delegated to the pure planner in the
// plan subpackage; this file is the controller-runtime adapter that
// reads the live Sandbox set, applies the plan through the API
// server, and registers the reconciler with a manager.
package warmpool

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool/plan"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// conditionPoolWarmingUp is the §5.2 bootstrap condition the
// WarmPoolController maintains on SandboxTemplate.status.
const conditionPoolWarmingUp = "PoolWarmingUp"

// Label keys the controller stamps on every Sandbox it creates. The
// pool label scopes the per-pool List; the managed label marks the
// resource as controller-owned for §17.2 admission targeting.
const (
	LabelPool    = "lenny.dev/pool"
	LabelManaged = "lenny.dev/managed"
)

// Reconciler is the §4.6.1 WarmPoolController. It reconciles one
// SandboxWarmPool per pass.
type Reconciler struct {
	// Client is the controller-runtime client backed by the manager
	// cache.
	Client client.Client
	// Scheme is required to stamp owner references on created
	// Sandboxes.
	Scheme *runtime.Scheme
}

// Reconcile sizes one pool: it lists the pool's Sandboxes, computes
// the create/drain plan, applies it, and updates the pool status.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var pool lennyv1.SandboxWarmPool
	if err := r.Client.Get(ctx, req.NamespacedName, &pool); err != nil {
		// The pool was deleted; its Sandboxes are garbage-collected via
		// owner references, so there is nothing left to reconcile.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// The SandboxTemplate is required: it supplies the pod spec for
	// created Sandboxes and hosts the PoolWarmingUp condition.
	tmpl, err := r.poolTemplate(ctx, &pool)
	if err != nil {
		return ctrl.Result{}, err
	}

	var sandboxes lennyv1.SandboxList
	if err := r.Client.List(ctx, &sandboxes,
		client.InNamespace(pool.Namespace),
		client.MatchingLabels{LabelPool: pool.Name}); err != nil {
		return ctrl.Result{}, fmt.Errorf("list sandboxes for pool %s: %w", pool.Name, err)
	}

	in := plan.Inputs{
		MinWarm: int(pool.Spec.MinWarm),
		MaxWarm: int(pool.Spec.MaxWarm),
	}
	for i := range sandboxes.Items {
		sb := &sandboxes.Items[i]
		in.Pods = append(in.Pods, plan.Pod{Name: sb.Name, Phase: observedPhase(sb)})
	}
	decision := plan.Compute(in)

	for i := 0; i < decision.Create; i++ {
		if err := r.createSandbox(ctx, &pool, tmpl); err != nil {
			return ctrl.Result{}, fmt.Errorf("create sandbox for pool %s: %w", pool.Name, err)
		}
	}

	for _, name := range decision.Drain {
		if err := r.drainSandbox(ctx, sandboxes.Items, name); err != nil {
			return ctrl.Result{}, fmt.Errorf("drain sandbox %s: %w", name, err)
		}
	}

	if err := r.updateStatus(ctx, &pool, decision); err != nil {
		return ctrl.Result{}, fmt.Errorf("update pool %s status: %w", pool.Name, err)
	}
	if err := r.updateTemplateCondition(ctx, tmpl, &pool, decision); err != nil {
		return ctrl.Result{}, fmt.Errorf("update template %s status: %w", tmpl.Name, err)
	}
	return ctrl.Result{}, nil
}

// observedPhase maps a Sandbox's reported phase to a state.State. A
// Sandbox that exists but has not yet reported a phase is still
// starting up, so it is treated as warming — this prevents the
// planner from creating a duplicate before the new pod's status lands.
func observedPhase(sb *lennyv1.Sandbox) state.State {
	if sb.Status.Phase == "" {
		return state.Warming
	}
	return state.State(sb.Status.Phase)
}

// poolTemplate fetches the SandboxTemplate the pool's pods are created
// from.
func (r *Reconciler) poolTemplate(ctx context.Context, pool *lennyv1.SandboxWarmPool) (*lennyv1.SandboxTemplate, error) {
	var tmpl lennyv1.SandboxTemplate
	key := client.ObjectKey{Namespace: pool.Namespace, Name: pool.Spec.TemplateRef}
	if err := r.Client.Get(ctx, key, &tmpl); err != nil {
		return nil, fmt.Errorf("get template %s for pool %s: %w", pool.Spec.TemplateRef, pool.Name, err)
	}
	return &tmpl, nil
}

// createSandbox creates one Sandbox for the pool, derived from the
// pool's SandboxTemplate and owned by the SandboxWarmPool so it is
// garbage-collected with the pool.
func (r *Reconciler) createSandbox(ctx context.Context, pool *lennyv1.SandboxWarmPool, tmpl *lennyv1.SandboxTemplate) error {
	sb := &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: pool.Name + "-",
			Namespace:    pool.Namespace,
			Labels: map[string]string{
				LabelPool:    pool.Name,
				LabelManaged: "true",
			},
		},
		Spec: lennyv1.SandboxSpec{
			RuntimeRef:       tmpl.Spec.RuntimeRef,
			PoolRef:          pool.Name,
			IsolationProfile: tmpl.Spec.IsolationProfile,
			DeliveryMode:     tmpl.Spec.DeliveryMode,
		},
	}
	if err := ctrl.SetControllerReference(pool, sb, r.Scheme); err != nil {
		return err
	}
	return r.Client.Create(ctx, sb)
}

// drainSandbox transitions the named Sandbox to the draining phase so
// the pod-lifecycle layer retires it. The planner only ever names idle
// Sandboxes, for which idle → draining is a valid §6.2 transition.
func (r *Reconciler) drainSandbox(ctx context.Context, items []lennyv1.Sandbox, name string) error {
	for i := range items {
		sb := &items[i]
		if sb.Name != name {
			continue
		}
		sb.Status.Phase = string(state.Draining)
		return r.Client.Status().Update(ctx, sb)
	}
	return nil
}

// updateStatus writes the post-action warm and ready counts and the
// observed generation to the pool status. The write is skipped when
// nothing changed, to avoid generating spurious etcd writes and
// reconcile events.
func (r *Reconciler) updateStatus(ctx context.Context, pool *lennyv1.SandboxWarmPool, decision plan.Plan) error {
	drained := len(decision.Drain)
	warm := int32(decision.WarmCount + decision.Create - drained)
	ready := int32(decision.ReadyCount - drained)

	if pool.Status.WarmCount == warm &&
		pool.Status.ReadyCount == ready &&
		pool.Status.ObservedGeneration == pool.Generation {
		return nil
	}
	pool.Status.WarmCount = warm
	pool.Status.ReadyCount = ready
	pool.Status.ObservedGeneration = pool.Generation
	return r.Client.Status().Update(ctx, pool)
}

// updateTemplateCondition writes the §5.2 PoolWarmingUp condition to
// the SandboxTemplate status. The condition is True while a pool with
// a positive minWarm has no idle pods and at least one pod still
// warming; the gateway reads it to answer session requests with a 503
// during the bootstrap window. The write is skipped when neither the
// condition nor the observed generation changed.
func (r *Reconciler) updateTemplateCondition(ctx context.Context, tmpl *lennyv1.SandboxTemplate, pool *lennyv1.SandboxWarmPool, decision plan.Plan) error {
	drained := len(decision.Drain)
	warm := decision.WarmCount + decision.Create - drained
	ready := decision.ReadyCount - drained

	cond := poolWarmingUpCondition(int(pool.Spec.MinWarm), warm, ready)
	cond.ObservedGeneration = tmpl.Generation

	changed := meta.SetStatusCondition(&tmpl.Status.Conditions, cond)
	if tmpl.Status.ObservedGeneration != tmpl.Generation {
		tmpl.Status.ObservedGeneration = tmpl.Generation
		changed = true
	}
	if !changed {
		return nil
	}
	return r.Client.Status().Update(ctx, tmpl)
}

// poolWarmingUpCondition derives the §5.2 PoolWarmingUp condition from
// the pool's minWarm and its current warm and ready pod counts.
func poolWarmingUpCondition(minWarm, warm, ready int) metav1.Condition {
	warming := warm - ready
	switch {
	case minWarm > 0 && ready == 0 && warming > 0:
		return metav1.Condition{
			Type:    conditionPoolWarmingUp,
			Status:  metav1.ConditionTrue,
			Reason:  "Provisioning",
			Message: fmt.Sprintf("Pool has no idle pods; %d warming.", warming),
		}
	case minWarm > 0 && ready == 0 && warming == 0:
		return metav1.Condition{
			Type:    conditionPoolWarmingUp,
			Status:  metav1.ConditionFalse,
			Reason:  "Drained",
			Message: "Pool has no idle or warming pods.",
		}
	default:
		return metav1.Condition{
			Type:    conditionPoolWarmingUp,
			Status:  metav1.ConditionFalse,
			Reason:  "Available",
			Message: fmt.Sprintf("Pool has %d idle pods.", ready),
		}
	}
}

// SetupWithManager registers the reconciler with the manager. It
// reconciles SandboxWarmPool resources and additionally wakes on
// changes to any Sandbox it owns, so a pod phase change re-sizes the
// pool.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&lennyv1.SandboxWarmPool{}).
		Owns(&lennyv1.Sandbox{}).
		Named("warmpool").
		Complete(r)
}
