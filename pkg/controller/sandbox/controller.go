// SPDX-License-Identifier: MIT

// Package sandbox holds the Sandbox-to-Pod reconciler — the part of the
// §4.6.1 WarmPoolController that materializes each Sandbox resource
// into a backing Pod and drives the §6.2 warm-path lifecycle.
//
// The reconcile decision is delegated to the pure planner in the
// lifecycle subpackage; the Pod is assembled by the podspec subpackage.
// This file is the controller-runtime adapter that observes the Pod,
// applies the plan, and writes the Sandbox status. The Pod is named
// after the Sandbox and carries an owner reference to it, so a Sandbox
// deletion garbage-collects its Pod.
package sandbox

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	"github.com/lennylabs/lenny/pkg/admission/ownership"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/controllermetrics"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/lifecycle"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
	"github.com/lennylabs/lenny/pkg/controller/statusdedup"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// retryOnConflictSSA implements the §4.6.3 SSA-conflict retry policy:
// always re-read before re-applying, never force-conflicts, bounded
// retry with jittered backoff (100ms initial, 2s ceiling, 5 attempts
// before logging the stuck-state event). The apply closure receives
// the freshly-read live object on each attempt and may either Apply
// the patch or return nil to short-circuit.
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
		sleep := delay + jitter
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleep):
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

// Reconciler materializes Sandbox resources into Pods and advances the
// §6.2 warm-path phase (§4.6.1).
type Reconciler struct {
	// Client is the controller-runtime client.
	Client client.Client
	// Scheme is required to stamp the owner reference on created Pods.
	Scheme *runtime.Scheme
	// AdapterImage is the lenny-adapter sidecar image stamped into
	// every agent Pod.
	AdapterImage string
	// EgressCaptureImage is the §12.9.8 egress-capture sidecar image.
	// Empty disables the sidecar globally; a non-empty value enables
	// the §12.9.8 tier-9 leakage probe path on Sandboxes whose
	// SandboxTemplate carries the egress-capture annotation
	// (EgressCaptureUpstreamAnnotation).
	EgressCaptureImage string
	// DevMode is the platform global.devMode (LENNY_DEV_MODE=true). When
	// true, a Sandbox that omits an isolation profile defaults its pod to
	// `standard` (runc) per §5.3 line 677, so a developer can launch pods
	// on a cluster without gVisor installed.
	DevMode bool
	// SATokenAudience is the §10.3 deployment-specific projected-token
	// audience (global.saTokenAudience). When set, every agent pod mounts a
	// §6.1 line 14 audience-bound projected service-account token. Empty
	// (test or unconfigured) leaves the pod without it rather than mounting
	// a wrong-audience token.
	SATokenAudience string
	// AgentServiceAccountName is the §10.3 zero-RBAC ServiceAccount bound to
	// agent pods. Empty uses the namespace default SA (no RBAC bindings in
	// agent namespaces).
	AgentServiceAccountName string
	// StatusDedup is the §4.6.1 statusUpdateDeduplicationWindow gate. When
	// set, a Sandbox status write within the window of the previous write
	// for the same Sandbox is deferred and the reconcile requeued so the
	// trailing status lands once the window expires. A nil gate disables
	// deduplication.
	StatusDedup *statusdedup.Gate
	// MaxConcurrentReconciles is the §4.6.1 worker count
	// (--max-concurrent-reconciles, default 1). Zero or negative selects
	// the controller-runtime default of 1.
	MaxConcurrentReconciles int
	// QueueFactory, when set, supplies the §4.6.1 bounded,
	// depth-instrumented reconciliation work queue (--workqueue-max-depth).
	// A nil factory uses the controller-runtime default queue.
	QueueFactory controllermetrics.QueueFactory
}

// EgressCaptureUpstreamAnnotation is the §12.9.8 opt-in annotation an
// operator stamps on a SandboxTemplate (and the reconciler propagates
// to the Sandbox) to enable the egress-capture sidecar on every pod
// created from that template. The value is the upstream the sidecar
// forwards to (e.g., `api.openai.com:443`). The sidecar is TEST-ONLY
// and the lenny-pod-security webhook rejects pods carrying it in
// production deployments.
const EgressCaptureUpstreamAnnotation = "lenny.dev/test-egress-capture-upstream"

// Reconcile drives one Sandbox: it observes the backing Pod, runs the
// lifecycle planner, and applies the resulting action.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var sb lennyv1.Sandbox
	if err := r.Client.Get(ctx, req.NamespacedName, &sb); err != nil {
		// The Sandbox is gone; its terminating-seconds gauge (if any) is
		// stale, so drop the series.
		if apierrors.IsNotFound(err) {
			finalizerTerminatingSeconds.DeleteLabelValues(req.Namespace, req.Name)
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// §4.6.1 Sandbox finalizers: a Sandbox under deletion stays in
	// Terminating until no active SandboxClaim references it, at which
	// point the session-cleanup finalizer is removed and Kubernetes
	// garbage-collects the backing pod.
	if !sb.DeletionTimestamp.IsZero() {
		return r.reconcileFinalizer(ctx, &sb)
	}

	var pod corev1.Pod
	podErr := r.Client.Get(ctx, client.ObjectKey{Namespace: sb.Namespace, Name: sb.Name}, &pod)
	if podErr != nil && !apierrors.IsNotFound(podErr) {
		return ctrl.Result{}, fmt.Errorf("get pod for sandbox %s: %w", sb.Name, podErr)
	}
	obs := observePod(&pod, podErr)

	decision := lifecycle.Decide(state.State(sb.Status.Phase), obs)

	switch decision.Action {
	case lifecycle.ActionCreatePod:
		if err := r.createPod(ctx, &sb); err != nil {
			return ctrl.Result{}, err
		}
	case lifecycle.ActionDeletePod:
		if err := r.Client.Delete(ctx, &pod); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("delete pod %s: %w", pod.Name, err)
		}
	}

	deferFor, err := r.syncStatus(ctx, &sb, decision, &pod, obs)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("update sandbox %s status: %w", sb.Name, err)
	}

	desiredPhase := sb.Status.Phase
	if decision.Action == lifecycle.ActionSetPhase {
		desiredPhase = string(decision.NextPhase)
	}
	// §6.1 line 18: flip the readiness gate once the containers are ready
	// so Pod.Ready (and the warming → idle transition that keys off it) can
	// proceed. This is the Lenny-controlled claimability handoff.
	if err := r.syncReadinessGate(ctx, &pod, obs); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.syncPodStateLabel(ctx, desiredPhase, &pod, obs); err != nil {
		return ctrl.Result{}, err
	}
	// §4.6.1 statusUpdateDeduplicationWindow: when the status write was
	// deferred, requeue after the remaining window so the trailing
	// (latest) status is written once the window expires.
	if deferFor > 0 {
		return ctrl.Result{RequeueAfter: deferFor}, nil
	}
	return ctrl.Result{}, nil
}

// observePod reduces a Pod's phase to the lifecycle planner's view. A
// failed Get (the §4.6.1 reconciler filters non-NotFound errors before
// calling observePod) means the Pod is absent.
func observePod(pod *corev1.Pod, getErr error) lifecycle.PodObservation {
	if getErr != nil {
		return lifecycle.PodAbsent
	}
	switch pod.Status.Phase {
	case corev1.PodRunning:
		if podReady(pod) {
			return lifecycle.PodReady
		}
		return lifecycle.PodNotReady
	case corev1.PodFailed:
		return lifecycle.PodFailed
	case corev1.PodSucceeded:
		return lifecycle.PodSucceeded
	default:
		// Pending, Unknown, or empty: the pod is still coming up.
		return lifecycle.PodPending
	}
}

// podReady reports whether the Pod's Ready condition is True.
func podReady(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// resolveTerminationGrace best-effort resolves the §5.2 grace-period
// settings for the Sandbox's pool: the deployer-set base
// (terminationGracePeriodSeconds) and the hard ceiling
// (maxTerminationGracePeriodSeconds). It walks Sandbox → SandboxWarmPool
// (by PoolRef) → SandboxTemplate (by TemplateRef). Any missing resource
// leaves both unset (nil), so a transient lookup miss falls back to the
// §4.6.1 120s default rather than failing pod creation. spec: §5.2 line
// 516, §4.6.1.
func (r *Reconciler) resolveTerminationGrace(ctx context.Context, sb *lennyv1.Sandbox) (base, max *int64) {
	if sb.Spec.PoolRef == "" {
		return nil, nil
	}
	var pool lennyv1.SandboxWarmPool
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: sb.Namespace, Name: sb.Spec.PoolRef}, &pool); err != nil {
		return nil, nil
	}
	if pool.Spec.TemplateRef == "" {
		return nil, nil
	}
	var tmpl lennyv1.SandboxTemplate
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: sb.Namespace, Name: pool.Spec.TemplateRef}, &tmpl); err != nil {
		return nil, nil
	}
	return tmpl.Spec.TerminationGracePeriodSeconds, tmpl.Spec.MaxTerminationGracePeriodSeconds
}

// createPod resolves the Sandbox's Runtime, builds the agent Pod, and
// creates it with an owner reference back to the Sandbox.
func (r *Reconciler) createPod(ctx context.Context, sb *lennyv1.Sandbox) error {
	var rt lennyv1.Runtime
	if err := r.Client.Get(ctx, client.ObjectKey{Name: sb.Spec.RuntimeRef}, &rt); err != nil {
		return fmt.Errorf("get runtime %s: %w", sb.Spec.RuntimeRef, err)
	}
	profile := sb.Spec.IsolationProfile
	if profile == "" {
		// spec: §5.3 line 677 — dev mode falls back to `standard` (runc).
		profile = string(isolation.DefaultForMode(r.DevMode))
	}
	graceBase, graceMax := r.resolveTerminationGrace(ctx, sb)
	pod, err := podspec.Build(podspec.Inputs{
		Name:             sb.Name,
		Namespace:        sb.Namespace,
		Labels:           sb.Labels,
		RuntimeImage:     rt.Spec.Image,
		AdapterImage:     r.AdapterImage,
		IsolationProfile: profile,
		DeploymentModel:  rt.Spec.DeploymentModel,
		EgressCapture:    r.resolveEgressCapture(sb),
		// spec: §5.2 line 516 — the SandboxTemplate's deployer-set
		// terminationGracePeriodSeconds replaces the 120s default base, and
		// the maxTerminationGracePeriodSeconds ceiling clamps the pod's
		// grace period down when the deployer has declared a hard cap.
		TerminationGraceSeconds:    graceBase,
		MaxTerminationGraceSeconds: graceMax,
		// spec: §5.2 lines 631-636 — the WarmPoolController copied the
		// pool's topology spread constraints onto Sandbox.spec; stamp them
		// onto the pod here.
		TopologySpreadConstraints: sb.Spec.TopologySpreadConstraints,
		// spec: §6.1 line 14 / §10.3 — mount the audience-bound projected
		// service-account token (when an audience is configured) under the
		// zero-RBAC agent ServiceAccount.
		SATokenAudience:    r.SATokenAudience,
		ServiceAccountName: r.AgentServiceAccountName,
	})
	if err != nil {
		return fmt.Errorf("build pod spec: %w", err)
	}
	if pod.Labels == nil {
		pod.Labels = map[string]string{}
	}
	// spec: §6.2 line 311 — stamp the immutable runtime-name label so
	// operators can select pods by runtime type.
	if sb.Spec.RuntimeRef != "" {
		pod.Labels[state.LabelRuntime] = sb.Spec.RuntimeRef
	}
	// §4.6.1 disruption protection / §6.2 lines 305-313: stamp the coarse
	// lenny.dev/state label so the per-pool PodDisruptionBudget can select
	// warm (idle) pods. A pre-ready pod (warming) has no coarse value, so
	// the label is omitted until the pod reaches a coarse state; the
	// reconciler keeps the label in sync on every subsequent transition.
	if v, ok := podStateLabel(sb); ok {
		pod.Labels[state.LabelState] = v
	}
	if err := ctrl.SetControllerReference(sb, pod, r.Scheme); err != nil {
		return fmt.Errorf("set controller reference: %w", err)
	}
	if err := r.Client.Create(ctx, pod); err != nil {
		return fmt.Errorf("create pod: %w", err)
	}
	return nil
}

// podStateLabel returns the coarse lenny.dev/state label value for a
// Sandbox (spec §6.2 line 309: idle/active/draining) and whether the
// Sandbox's §6.2 phase maps to a coarse value at all. An unset phase (a
// freshly-created Sandbox whose pod is being materialized) is treated as
// warming, which has no coarse operational value, so the second return is
// false and the label is omitted.
func podStateLabel(sb *lennyv1.Sandbox) (string, bool) {
	phase := sb.Status.Phase
	if phase == "" {
		phase = string(state.Warming)
	}
	return state.CoarseState(state.State(phase))
}

// syncPodStateLabel keeps the backing pod's coarse lenny.dev/state label
// in sync with the Sandbox's §6.2 phase so the §4.6.1 warm-pool
// PodDisruptionBudget's idle selector and §6.2 NetworkPolicy/monitoring
// selectors track pod lifecycle. desiredPhase is the phase syncStatus
// wrote this pass; it is mapped to the coarse idle/active/draining value
// set (spec §6.2 lines 305-313). When the phase has no coarse value
// (warming, sdk_connecting, or a terminal phase) the label is removed
// rather than carrying a value outside the documented set. The patch is
// skipped when the pod is absent or the label already matches. spec:
// §4.6.1 "Disruption protection for agent pods", §6.2 lines 305-313.
func (r *Reconciler) syncPodStateLabel(ctx context.Context, desiredPhase string, pod *corev1.Pod, obs lifecycle.PodObservation) error {
	if obs == lifecycle.PodAbsent || pod == nil || pod.Name == "" {
		return nil
	}
	want, ok := state.CoarseState(state.State(desiredPhase))
	current, has := pod.Labels[state.LabelState]
	if ok && current == want {
		return nil
	}
	if !ok && !has {
		return nil
	}
	patch := client.MergeFrom(pod.DeepCopy())
	if pod.Labels == nil {
		pod.Labels = map[string]string{}
	}
	if ok {
		pod.Labels[state.LabelState] = want
	} else {
		delete(pod.Labels, state.LabelState)
	}
	if err := r.Client.Patch(ctx, pod, patch); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("patch pod %s state label: %w", pod.Name, err)
	}
	return nil
}

// syncReadinessGate flips the §6.1 line 18 lenny.dev/sandbox-ready pod
// readiness gate to True once the pod's containers are ready. The pod spec
// declares the gate (podspec.ReadinessGateSandboxReady), so the kubelet
// holds Pod.Ready False — keeping the pod un-claimable and the warming →
// idle transition (which keys off Pod.Ready) from firing — until the
// WarmPoolController asserts the pod is warm. v1 flips the gate as soon as
// the containers report ready; the gate exists so claimability is a
// Lenny-controlled signal rather than implied by container readiness
// alone. The patch is skipped when the pod is absent, its containers are
// not yet ready, or the gate is already True. spec: §6.1 line 18.
func (r *Reconciler) syncReadinessGate(ctx context.Context, pod *corev1.Pod, obs lifecycle.PodObservation) error {
	if obs == lifecycle.PodAbsent || pod == nil || pod.Name == "" {
		return nil
	}
	if !podConditionTrue(pod, corev1.ContainersReady) {
		return nil
	}
	if podConditionTrue(pod, corev1.PodConditionType(podspec.ReadinessGateSandboxReady)) {
		return nil
	}
	patch := client.MergeFrom(pod.DeepCopy())
	setPodCondition(pod, corev1.PodCondition{
		Type:               corev1.PodConditionType(podspec.ReadinessGateSandboxReady),
		Status:             corev1.ConditionTrue,
		Reason:             "SandboxWarm",
		Message:            "containers ready; sandbox is idle and claimable",
		LastTransitionTime: metav1.Now(),
	})
	if err := r.Client.Status().Patch(ctx, pod, patch); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("patch pod %s readiness gate: %w", pod.Name, err)
	}
	return nil
}

// podConditionTrue reports whether the pod carries condition t with status
// True.
func podConditionTrue(pod *corev1.Pod, t corev1.PodConditionType) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == t {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// setPodCondition sets cond on the pod, replacing any existing condition
// of the same type.
func setPodCondition(pod *corev1.Pod, cond corev1.PodCondition) {
	for i, c := range pod.Status.Conditions {
		if c.Type == cond.Type {
			pod.Status.Conditions[i] = cond
			return
		}
	}
	pod.Status.Conditions = append(pod.Status.Conditions, cond)
}

// syncStatus writes the post-action phase, the backing pod name and
// node, and the observed generation to the Sandbox status, skipping the
// write when nothing changed.
//
// Per spec §4.6.3, the write goes through Kubernetes Server-Side Apply
// with the `lenny-warm-pool-controller` field manager so the API
// server enforces the per-field ownership boundary against the
// gateway's parallel slot-claim writes (which use the
// `lenny-gateway` field manager). On HTTP 409 (a concurrent apply
// from another manager touched a controller-owned field) the spec
// retry policy applies: re-read, re-compute the patch against the
// freshly-read state, never force-conflicts, bounded retry with
// jittered backoff up to 5 attempts.
// syncStatus writes the controller-owned Sandbox.status fields, subject to
// the §4.6.1 statusUpdateDeduplicationWindow. It returns the duration the
// reconcile should requeue after when the write was deferred (zero when
// the write happened or no change was needed).
func (r *Reconciler) syncStatus(ctx context.Context, sb *lennyv1.Sandbox, decision lifecycle.Decision, pod *corev1.Pod, obs lifecycle.PodObservation) (time.Duration, error) {
	// Nothing to write: skip the dedup gate so a no-op reconcile never
	// consumes the window or requeues.
	if buildSandboxStatusPatch(sb, decision, pod, obs) == nil {
		return 0, nil
	}
	// A status change is pending. Consult the dedup gate once (outside the
	// conflict-retry loop, so a 409 retry within this reconcile does not
	// re-arm the window) and defer the write when within the window.
	key := "Sandbox/" + sb.Namespace + "/" + sb.Name
	if ok, remaining := r.StatusDedup.Allow(key); !ok {
		return remaining, nil
	}
	err := retryOnConflictSSA(ctx, func(attempt int) error {
		var live lennyv1.Sandbox
		if attempt == 0 {
			live = *sb
		} else if err := r.Client.Get(ctx, client.ObjectKeyFromObject(sb), &live); err != nil {
			return err
		}
		patch := buildSandboxStatusPatch(&live, decision, pod, obs)
		if patch == nil {
			return nil
		}
		return r.Client.Status().Patch(ctx, patch, client.Apply, client.FieldOwner(string(ownership.WarmPoolController)))
	})
	return 0, err
}

// buildSandboxStatusPatch returns an SSA Apply patch object carrying
// the controller-owned Sandbox.status fields, or nil when none change.
// The patch is a minimal Sandbox with name/namespace metadata and the
// controller-owned status fields; the API server merges it onto the
// live object under the WPC's field manager, leaving every other
// field manager's contributions intact.
//
// Every controller-owned field present in the patch carries either
// the planner's new value or, when the planner is not transitioning
// that field, the live value re-applied. SSA treats a struct's Go
// zero-value field as an intentional set ("Phase=''"), so omitting a
// field by leaving it zero would clobber the live value and erase
// the controller's claim onto it. Re-including the live value keeps
// the WPC's ownership of the field intact without overwriting
// the planner's intent.
func buildSandboxStatusPatch(live *lennyv1.Sandbox, decision lifecycle.Decision, pod *corev1.Pod, obs lifecycle.PodObservation) *lennyv1.Sandbox {
	before := live.Status
	// Start with the live values for every controller-owned field so
	// the SSA patch is a no-op on fields the planner does not touch.
	want := sandboxStatusFields{
		Phase:              before.Phase,
		PodName:            before.PodName,
		NodeName:           before.NodeName,
		PodIP:              before.PodIP,
		ObservedGeneration: live.Generation,
	}
	if decision.Action == lifecycle.ActionSetPhase {
		want.Phase = string(decision.NextPhase)
	}
	switch {
	case decision.Action == lifecycle.ActionCreatePod:
		want.PodName = live.Name
	case obs == lifecycle.PodAbsent:
		want.PodName = ""
		want.NodeName = ""
		want.PodIP = ""
	default:
		want.PodName = live.Name
		if pod != nil {
			want.NodeName = pod.Spec.NodeName
			want.PodIP = pod.Status.PodIP
		}
	}
	if want.Phase == before.Phase &&
		want.PodName == before.PodName &&
		want.NodeName == before.NodeName &&
		want.PodIP == before.PodIP &&
		want.ObservedGeneration == before.ObservedGeneration {
		return nil
	}
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
	patch.Status.Phase = want.Phase
	patch.Status.PodName = want.PodName
	patch.Status.NodeName = want.NodeName
	patch.Status.PodIP = want.PodIP
	patch.Status.ObservedGeneration = want.ObservedGeneration
	return patch
}

// sandboxStatusFields is the in-memory carrier for the per-attempt
// status computation.
type sandboxStatusFields struct {
	Phase              string
	PodName            string
	NodeName           string
	PodIP              string
	ObservedGeneration int64
}

// SetupWithManager registers the reconciler. It reconciles Sandbox
// resources and wakes on changes to any Pod it owns.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	opts := controller.Options{}
	if r.MaxConcurrentReconciles > 0 {
		opts.MaxConcurrentReconciles = r.MaxConcurrentReconciles
	}
	if r.QueueFactory != nil {
		opts.NewQueue = r.QueueFactory
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&lennyv1.Sandbox{}).
		Owns(&corev1.Pod{}).
		Named("sandbox").
		WithOptions(opts).
		Complete(r)
}

// resolveEgressCapture returns the §12.9.8 egress-capture configuration
// for the pod the reconciler is about to create, or nil when capture is
// not enabled. Capture activates when the Sandbox carries the
// EgressCaptureUpstreamAnnotation and the reconciler is configured with
// an egress-capture image. The annotation's value is the upstream
// host:port the sidecar forwards every accepted connection to.
func (r *Reconciler) resolveEgressCapture(sb *lennyv1.Sandbox) *podspec.EgressCapture {
	if r.EgressCaptureImage == "" {
		return nil
	}
	if sb.Annotations == nil {
		return nil
	}
	upstream := sb.Annotations[EgressCaptureUpstreamAnnotation]
	if upstream == "" {
		return nil
	}
	return &podspec.EgressCapture{
		Image:    r.EgressCaptureImage,
		Upstream: upstream,
	}
}
