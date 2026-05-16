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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/lifecycle"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

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
}

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
func (r *Reconciler) syncStatus(ctx context.Context, sb *lennyv1.Sandbox, decision lifecycle.Decision, pod *corev1.Pod, obs lifecycle.PodObservation) error {
	before := sb.Status

	if decision.Action == lifecycle.ActionSetPhase {
		sb.Status.Phase = string(decision.NextPhase)
	}
	switch {
	case decision.Action == lifecycle.ActionCreatePod:
		sb.Status.PodName = sb.Name
	case obs == lifecycle.PodAbsent:
		sb.Status.PodName = ""
		sb.Status.NodeName = ""
		sb.Status.PodIP = ""
	default:
		sb.Status.PodName = sb.Name
		sb.Status.NodeName = pod.Spec.NodeName
		sb.Status.PodIP = pod.Status.PodIP
	}
	sb.Status.ObservedGeneration = sb.Generation

	if sb.Status.Phase == before.Phase &&
		sb.Status.PodName == before.PodName &&
		sb.Status.NodeName == before.NodeName &&
		sb.Status.PodIP == before.PodIP &&
		sb.Status.ObservedGeneration == before.ObservedGeneration {
		return nil
	}
	return r.Client.Status().Update(ctx, sb)
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
