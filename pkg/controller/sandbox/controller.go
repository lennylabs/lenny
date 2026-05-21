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

	"github.com/lennylabs/lenny/pkg/admission/ownership"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/lifecycle"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
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
		return ctrl.Result{}, client.IgnoreNotFound(err)
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

	if err := r.syncStatus(ctx, &sb, decision, &pod, obs); err != nil {
		return ctrl.Result{}, fmt.Errorf("update sandbox %s status: %w", sb.Name, err)
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

// createPod resolves the Sandbox's Runtime, builds the agent Pod, and
// creates it with an owner reference back to the Sandbox.
func (r *Reconciler) createPod(ctx context.Context, sb *lennyv1.Sandbox) error {
	var rt lennyv1.Runtime
	if err := r.Client.Get(ctx, client.ObjectKey{Name: sb.Spec.RuntimeRef}, &rt); err != nil {
		return fmt.Errorf("get runtime %s: %w", sb.Spec.RuntimeRef, err)
	}
	profile := sb.Spec.IsolationProfile
	if profile == "" {
		profile = string(isolation.Default())
	}
	pod, err := podspec.Build(podspec.Inputs{
		Name:             sb.Name,
		Namespace:        sb.Namespace,
		Labels:           sb.Labels,
		RuntimeImage:     rt.Spec.Image,
		AdapterImage:     r.AdapterImage,
		IsolationProfile: profile,
		DeploymentModel:  rt.Spec.DeploymentModel,
		EgressCapture:    r.resolveEgressCapture(sb),
	})
	if err != nil {
		return fmt.Errorf("build pod spec: %w", err)
	}
	if err := ctrl.SetControllerReference(sb, pod, r.Scheme); err != nil {
		return fmt.Errorf("set controller reference: %w", err)
	}
	if err := r.Client.Create(ctx, pod); err != nil {
		return fmt.Errorf("create pod: %w", err)
	}
	return nil
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
func (r *Reconciler) syncStatus(ctx context.Context, sb *lennyv1.Sandbox, decision lifecycle.Decision, pod *corev1.Pod, obs lifecycle.PodObservation) error {
	return retryOnConflictSSA(ctx, func(attempt int) error {
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
	return ctrl.NewControllerManagedBy(mgr).
		For(&lennyv1.Sandbox{}).
		Owns(&corev1.Pod{}).
		Named("sandbox").
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
