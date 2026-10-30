// SPDX-License-Identifier: MIT

package sandbox

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/lennylabs/lenny/pkg/admission/sandboxclaim_guard"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// finalizerTerminatingSeconds is the gauge the FinalizerStuck alert
// reads: seconds a Sandbox has been in Terminating with the
// session-cleanup finalizer still present. The §16.5 FinalizerStuck
// alert fires on `lenny_sandbox_finalizer_terminating_seconds > 300`.
// spec: §4.6.1 "Sandbox finalizers" — "If the finalizer is not removed
// within 5 minutes (pod stuck in Terminating), the controller fires a
// FinalizerStuck alert." The gauge is per Sandbox (namespace, name) so
// the alert can name the stuck pod; the series is cleared once the
// finalizer is removed.
var finalizerTerminatingSeconds = func() *prometheus.GaugeVec {
	g, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_sandbox_finalizer_terminating_seconds",
		Help: "Seconds a Sandbox has been Terminating with the session-cleanup finalizer present.",
	}, []string{"namespace", "sandbox"})
	if err != nil {
		panic(fmt.Sprintf("sandbox: build finalizer-terminating gauge: %v", err))
	}
	metrics.MustRegister(ctrlmetrics.Registry, g)
	return g
}()

// finalizerStuckRequeue is how often the reconciler re-examines a
// Sandbox stuck in Terminating with an unremovable finalizer so the
// terminating-seconds gauge keeps advancing toward the §16.5 5-minute
// FinalizerStuck threshold even without an intervening claim event.
const finalizerStuckRequeue = 30 * time.Second

// reconcileFinalizer handles a Sandbox under deletion. It removes the
// session-cleanup finalizer once no active SandboxClaim references the
// Sandbox; until then it leaves the finalizer in place and advances the
// terminating-seconds gauge so the §16.5 FinalizerStuck alert fires
// after 5 minutes. spec: §4.6.1 "Sandbox finalizers".
func (r *Reconciler) reconcileFinalizer(ctx context.Context, sb *lennyv1.Sandbox) (ctrl.Result, error) {
	if !hasFinalizer(sb.Finalizers) {
		// Another writer already removed the finalizer; nothing left to
		// guard and Kubernetes will complete the deletion.
		finalizerTerminatingSeconds.DeleteLabelValues(sb.Namespace, sb.Name)
		return ctrl.Result{}, nil
	}

	var claims lennyv1.SandboxClaimList
	if err := r.Client.List(ctx, &claims, client.InNamespace(sb.Namespace)); err != nil {
		return ctrl.Result{}, fmt.Errorf("list claims for sandbox %s: %w", sb.Name, err)
	}
	if activeClaimReferences(claims.Items, sb.Name) {
		// An active session still binds this pod. Hold the finalizer and
		// surface how long the Sandbox has been stuck Terminating so the
		// FinalizerStuck alert can page after the §16.5 5-minute threshold.
		if !sb.DeletionTimestamp.IsZero() {
			elapsed := time.Since(sb.DeletionTimestamp.Time).Seconds()
			if elapsed < 0 {
				elapsed = 0
			}
			finalizerTerminatingSeconds.WithLabelValues(sb.Namespace, sb.Name).Set(elapsed)
		}
		return ctrl.Result{RequeueAfter: finalizerStuckRequeue}, nil
	}

	sb.Finalizers = removeFinalizer(sb.Finalizers)
	if err := r.Client.Update(ctx, sb); err != nil {
		return ctrl.Result{}, fmt.Errorf("remove finalizer from sandbox %s: %w", sb.Name, err)
	}
	finalizerTerminatingSeconds.DeleteLabelValues(sb.Namespace, sb.Name)
	return ctrl.Result{}, nil
}

// hasFinalizer reports whether obj carries the session-cleanup
// finalizer.
func hasFinalizer(finalizers []string) bool {
	for _, f := range finalizers {
		if f == lennyv1.FinalizerSessionCleanup {
			return true
		}
	}
	return false
}

// removeFinalizer returns finalizers with the session-cleanup finalizer
// dropped, preserving order and any other finalizers.
func removeFinalizer(finalizers []string) []string {
	out := make([]string, 0, len(finalizers))
	for _, f := range finalizers {
		if f != lennyv1.FinalizerSessionCleanup {
			out = append(out, f)
		}
	}
	return out
}

// activeClaimReferences reports whether any SandboxClaim in claims
// binds the named Sandbox with a non-terminal phase. spec: §4.6.1
// "Sandbox finalizers" — the controller removes the finalizer only
// after confirming no active SandboxClaim still references the pod
// (condition (a)). A claim whose phase is released or failed is
// terminal (§4.6.3) and does not block removal; an empty phase is the
// freshly-created bound state and counts as active.
func activeClaimReferences(claims []lennyv1.SandboxClaim, sandboxName string) bool {
	for i := range claims {
		c := &claims[i]
		if c.Spec.SandboxRef != sandboxName {
			continue
		}
		if !sandboxclaim_guard.ClaimStatus(c.Status.Phase).IsTerminal() {
			return true
		}
	}
	return false
}
