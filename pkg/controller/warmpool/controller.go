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
	"encoding/json"
	"fmt"
	"math/rand"
	"strconv"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/lennylabs/lenny/pkg/admission/ownership"
	"github.com/lennylabs/lenny/pkg/agentpodstate"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool/plan"
	"github.com/lennylabs/lenny/pkg/gateway/opsevents"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// retryOnConflictSSA implements the §4.6.3 SSA-conflict retry policy
// for the WarmPoolController status applies: always re-read before
// re-applying, never force-conflicts, bounded retry with jittered
// backoff (100ms initial, 2s ceiling, 5 attempts).
func retryOnConflictSSA(ctx context.Context, apply func(attempt int) error) error {
	const maxAttempts = 5
	delay := 100 * time.Millisecond
	const maxDelay = 2 * time.Second
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := apply(attempt); err == nil {
			return nil
		} else if !apierrors.IsConflict(err) {
			return err
		} else {
			lastErr = err
		}
		jitter := time.Duration(rand.Int63n(int64(delay) / 4))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay + jitter):
		}
		if delay < maxDelay {
			delay *= 2
			if delay > maxDelay {
				delay = maxDelay
			}
		}
	}
	return lastErr
}

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

// PoolPhase is the §4.0 warm-pool derived phase the pool state manager
// surfaces as pool_state_changed event data. It collapses the per-pod
// §6.2 phases into one of the four pool-level states from §4.0 plus the
// healthy steady state ("ready").
type PoolPhase string

const (
	// PoolPhaseReady is the steady-state phase: at least one idle pod is
	// available to serve a claim.
	PoolPhaseReady PoolPhase = "ready"
	// PoolPhaseWarming is the bootstrap phase: no idle pods yet, but at
	// least one warming pod is converging toward idle.
	PoolPhaseWarming PoolPhase = "warming"
	// PoolPhaseDraining is the shrinking phase: at least one idle pod
	// has been transitioned to draining to shed warm capacity.
	PoolPhaseDraining PoolPhase = "draining"
	// PoolPhaseExhausted is the §4.0 exhausted phase: the pool has a
	// positive minWarm target but no idle and no warming pods (e.g.,
	// maxWarm caps creation at zero, or every pod failed to warm).
	PoolPhaseExhausted PoolPhase = "exhausted"
)

// EventEmitter is the §4.0 events sink the WarmPoolController publishes
// pool_state_changed events through. *opsevents.Emitter satisfies it. A
// nil EventEmitter on Reconciler disables emission.
type EventEmitter interface {
	Emit(opsevents.OperationalEvent) uint64
}

// Reconciler is the §4.6.1 WarmPoolController. It reconciles one
// SandboxWarmPool per pass.
type Reconciler struct {
	// Client is the controller-runtime client backed by the manager
	// cache.
	Client client.Client
	// Scheme is required to stamp owner references on created
	// Sandboxes.
	Scheme *runtime.Scheme
	// Mirror is the optional §4.6.1 agent_pod_state mirror store. When
	// set, every reconcile converges the Postgres-side mirror of this
	// pool's Sandbox status. It is nil when no Postgres is configured;
	// every call site treats nil as "mirroring disabled". The mirror is
	// a read-optimized copy for the gateway's fallback claim path, so a
	// mirror write failure never fails the reconcile.
	Mirror agentpodstate.Store
	// Events, when set, publishes §16.6 pool_state_changed events on
	// each derived PoolPhase transition per §4.0 pool state manager. A
	// nil Events is a no-op; the reconcile is unaffected.
	Events EventEmitter

	// phaseMu guards lastPhase against concurrent reconciles. The
	// controller-runtime queue serializes Reconcile per object, so the
	// guard only matters across pools, but the mutex is cheap.
	phaseMu sync.Mutex
	// lastPhase records the previously observed pool phase per pool
	// (keyed by namespace/name). Emission fires only on a phase change.
	lastPhase map[string]PoolPhase
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

	// Mirror the observed Sandbox status to the §4.6.1 agent_pod_state
	// table. The mirror is a read-optimized copy for the gateway's
	// fallback claim path; a write failure is logged and the reconcile
	// continues, because the authoritative store is the Sandbox status
	// subresource, not the mirror.
	r.syncMirror(ctx, &pool, tmpl, sandboxes.Items)

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
	r.observePoolPhase(&pool, decision)
	return ctrl.Result{}, nil
}

// DerivePoolPhase reduces one reconcile's plan output to the §4.0 pool
// state manager's phase. The mapping (in priority order):
//
//   - draining when at least one idle pod was named for drain this pass.
//   - exhausted when minWarm > 0 and the post-action pool has neither
//     ready (idle) pods nor warming pods to back the floor.
//   - warming when no idle pods are available yet but warming pods are
//     converging toward idle.
//   - ready otherwise (the steady state: at least one idle pod).
//
// This is the §4.0 closed enumeration plus the steady "ready" state,
// derived per spec §4.0 from the warm pool's observed pod set.
func DerivePoolPhase(minWarm int, decision plan.Plan) PoolPhase {
	drained := len(decision.Drain)
	warm := decision.WarmCount + decision.Create - drained
	ready := decision.ReadyCount - drained
	warming := warm - ready
	switch {
	case drained > 0:
		return PoolPhaseDraining
	case minWarm > 0 && ready == 0 && warming == 0:
		return PoolPhaseExhausted
	case ready == 0 && warming > 0:
		return PoolPhaseWarming
	default:
		return PoolPhaseReady
	}
}

// observePoolPhase derives the pool's current phase and emits a §16.6
// pool_state_changed event when the phase differs from the prior pass.
// Emission is per spec §4.0 pool state manager. A nil Events sink is a
// no-op.
func (r *Reconciler) observePoolPhase(pool *lennyv1.SandboxWarmPool, decision plan.Plan) {
	current := DerivePoolPhase(int(pool.Spec.MinWarm), decision)
	key := pool.Namespace + "/" + pool.Name

	r.phaseMu.Lock()
	if r.lastPhase == nil {
		r.lastPhase = make(map[string]PoolPhase)
	}
	prev, seen := r.lastPhase[key]
	r.lastPhase[key] = current
	r.phaseMu.Unlock()

	if !seen || prev == current {
		// The first observation of a pool sets the baseline without
		// emitting; spurious "ready→ready" notifications carry no signal.
		return
	}
	r.emitPoolStateChanged(pool.Name, prev, current)
}

// emitPoolStateChanged publishes the §16.6 pool_state_changed event per
// spec §4.0 with pool name, oldState, and newState.
func (r *Reconciler) emitPoolStateChanged(pool string, oldPhase, newPhase PoolPhase) {
	if r.Events == nil {
		return
	}
	severity := "info"
	if newPhase == PoolPhaseExhausted {
		severity = "warning"
	}
	data, _ := json.Marshal(map[string]any{
		"pool":     pool,
		"oldState": string(oldPhase),
		"newState": string(newPhase),
	})
	r.Events.Emit(opsevents.OperationalEvent{
		Source:          "//lenny.dev/warmpool",
		Type:            opsevents.EventPoolStateChanged.CloudEventsType(),
		Severity:        severity,
		DataContentType: "application/json",
		Data:            data,
	})
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

// syncMirror converges the §4.6.1 agent_pod_state mirror for the pool
// to the observed Sandbox set. It is a no-op when no Mirror store is
// configured. A mirror write failure is logged and swallowed: the
// mirror is a read-optimized copy for the gateway's fallback claim
// path, so a stale mirror must never block pod-pool reconciliation.
func (r *Reconciler) syncMirror(ctx context.Context, pool *lennyv1.SandboxWarmPool, tmpl *lennyv1.SandboxTemplate, sandboxes []lennyv1.Sandbox) {
	if r.Mirror == nil {
		return
	}
	observed := derivePodStates(pool, tmpl, sandboxes)
	if err := r.Mirror.Sync(ctx, pool.Name, observed); err != nil {
		logf.FromContext(ctx).Error(err, "agent_pod_state mirror sync failed; continuing",
			"pool", pool.Name)
	}
}

// derivePodStates projects the pool's live Sandbox set onto the
// §4.6.1 agent_pod_state row set. Each row mirrors one Sandbox: pod_id
// is the Sandbox name, pool_id is the pool name, state is the observed
// §6.2 phase, and isolation_profile / execution_mode are the pool-level
// properties (the Sandbox spec carries the isolation profile; execution
// mode is a §5.2 pool property sourced from the SandboxTemplate).
// tenant_id and session_id are left empty because a warm-pool reconcile
// only ever observes idle and warming pods, which carry no session; the
// claim path writes those columns when a pod is claimed.
//
// derivePodStates is a pure function with no I/O so the projection can
// be unit-tested without a cluster or a database.
func derivePodStates(pool *lennyv1.SandboxWarmPool, tmpl *lennyv1.SandboxTemplate, sandboxes []lennyv1.Sandbox) []agentpodstate.PodState {
	execMode := ""
	if tmpl != nil {
		execMode = tmpl.Spec.ExecutionMode
	}
	out := make([]agentpodstate.PodState, 0, len(sandboxes))
	for i := range sandboxes {
		sb := &sandboxes[i]
		out = append(out, agentpodstate.PodState{
			PodID:            sb.Name,
			PoolID:           pool.Name,
			State:            string(observedPhase(sb)),
			IsolationProfile: sb.Spec.IsolationProfile,
			ExecutionMode:    execMode,
			ResourceVersion:  parseResourceVersion(sb.ResourceVersion),
			NodeName:         sb.Status.NodeName,
		})
	}
	return out
}

// parseResourceVersion parses a Sandbox metadata.resourceVersion into
// the BIGINT the agent_pod_state.resource_version column expects. The
// Kubernetes API server's resourceVersion is an opaque string;
// etcd-backed clusters return a decimal integer, and the fake client
// used in tests returns the same form. An unparseable or empty value
// mirrors as 0 rather than failing the reconcile.
func parseResourceVersion(rv string) int64 {
	n, err := strconv.ParseInt(rv, 10, 64)
	if err != nil {
		return 0
	}
	return n
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
			Annotations: propagatedAnnotations(tmpl),
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

// propagatedAnnotations carries the small set of opt-in annotations
// from a SandboxTemplate onto every Sandbox it warms. The
// reconciler's createPod path reads these to decide whether to inject
// optional features (the §12.9.8 egress-capture sidecar today; more
// per-template knobs land alongside).
func propagatedAnnotations(tmpl *lennyv1.SandboxTemplate) map[string]string {
	if tmpl == nil {
		return nil
	}
	src := tmpl.Annotations
	if len(src) == 0 {
		return nil
	}
	keys := []string{
		// §12.9.8: a SandboxTemplate annotated with the egress-capture
		// upstream propagates that annotation to every Sandbox it
		// warms, and the Sandbox reconciler reads it on createPod.
		"lenny.dev/test-egress-capture-upstream",
	}
	var out map[string]string
	for _, k := range keys {
		if v, ok := src[k]; ok && v != "" {
			if out == nil {
				out = make(map[string]string, len(keys))
			}
			out[k] = v
		}
	}
	return out
}

// drainSandbox transitions the named Sandbox to the draining phase so
// the pod-lifecycle layer retires it. The planner only ever names idle
// Sandboxes, for which idle → draining is a valid §6.2 transition.
//
// Per spec §4.6.3, the write goes through SSA with the
// `lenny-warm-pool-controller` field manager; the patch carries only
// the controller-owned Phase field so the API server merges it onto
// the live Sandbox under WPC's ownership boundary. The §4.6.3
// retry policy applies on HTTP 409.
func (r *Reconciler) drainSandbox(ctx context.Context, items []lennyv1.Sandbox, name string) error {
	namespace := ""
	found := false
	for i := range items {
		if items[i].Name == name {
			namespace = items[i].Namespace
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	key := client.ObjectKey{Namespace: namespace, Name: name}
	return retryOnConflictSSA(ctx, func(attempt int) error {
		var live lennyv1.Sandbox
		if err := r.Client.Get(ctx, key, &live); err != nil {
			return err
		}
		if live.Status.Phase == string(state.Draining) {
			return nil
		}
		// Re-include every WPC-owned status field in the patch so SSA's
		// Go-zero-value-is-set semantics don't clobber PodName/
		// NodeName/PodIP/ObservedGeneration when we only intend to
		// transition Phase. Including the live values keeps the WPC
		// claim on those fields without overwriting them.
		patch := &lennyv1.Sandbox{
			TypeMeta: metav1.TypeMeta{
				APIVersion: lennyv1.GroupVersion.String(),
				Kind:       "Sandbox",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      live.Name,
				Namespace: live.Namespace,
			},
		}
		patch.Status.Phase = string(state.Draining)
		patch.Status.PodName = live.Status.PodName
		patch.Status.NodeName = live.Status.NodeName
		patch.Status.PodIP = live.Status.PodIP
		patch.Status.ObservedGeneration = live.Generation
		return r.Client.Status().Patch(ctx, patch, client.Apply, client.FieldOwner(string(ownership.WarmPoolController)))
	})
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
	key := client.ObjectKeyFromObject(pool)
	return retryOnConflictSSA(ctx, func(attempt int) error {
		var live lennyv1.SandboxWarmPool
		if attempt == 0 {
			live = *pool
		} else if err := r.Client.Get(ctx, key, &live); err != nil {
			return err
		}
		if live.Status.WarmCount == warm &&
			live.Status.ReadyCount == ready &&
			live.Status.ObservedGeneration == live.Generation {
			return nil
		}
		patch := &lennyv1.SandboxWarmPool{
			TypeMeta: metav1.TypeMeta{
				APIVersion: lennyv1.GroupVersion.String(),
				Kind:       "SandboxWarmPool",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      live.Name,
				Namespace: live.Namespace,
			},
		}
		patch.Status.WarmCount = warm
		patch.Status.ReadyCount = ready
		patch.Status.ObservedGeneration = live.Generation
		return r.Client.Status().Patch(ctx, patch, client.Apply, client.FieldOwner(string(ownership.WarmPoolController)))
	})
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
	key := client.ObjectKeyFromObject(tmpl)
	return retryOnConflictSSA(ctx, func(attempt int) error {
		var live lennyv1.SandboxTemplate
		if attempt == 0 {
			live = *tmpl
		} else if err := r.Client.Get(ctx, key, &live); err != nil {
			return err
		}
		desired := cond
		desired.ObservedGeneration = live.Generation
		mergedConditions := append([]metav1.Condition{}, live.Status.Conditions...)
		changed := meta.SetStatusCondition(&mergedConditions, desired)
		if live.Status.ObservedGeneration != live.Generation {
			changed = true
		}
		if !changed {
			return nil
		}
		patch := &lennyv1.SandboxTemplate{
			TypeMeta: metav1.TypeMeta{
				APIVersion: lennyv1.GroupVersion.String(),
				Kind:       "SandboxTemplate",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      live.Name,
				Namespace: live.Namespace,
			},
		}
		patch.Status.Conditions = mergedConditions
		patch.Status.ObservedGeneration = live.Generation
		return r.Client.Status().Patch(ctx, patch, client.Apply, client.FieldOwner(string(ownership.WarmPoolController)))
	})
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
