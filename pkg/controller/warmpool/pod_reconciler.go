// SPDX-License-Identifier: MIT

package warmpool

import (
	"context"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/lennylabs/lenny/pkg/admission/ownership"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/controllermetrics"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// LabelHostSchedulable is the §4.6.1 WPC-owned pod label that surfaces
// per-pod host-node schedulability to the gateway without the gateway
// touching the Node API. It is "true" when the pod's node accepts new
// work and "false" when the node is cordoned. The §6.2 task_cleanup
// transition reads it as a precondition (a pod on a cordoned node is
// not promoted back to idle/sdk_connecting). It is deliberately absent
// from the lenny-label-immutability webhook's immutable set so the WPC
// can flip it on every cordon/uncordon event.
//
// spec: §4.6.1 "Host-node schedulability labeling"; consumed by §6.2.
const LabelHostSchedulable = "lenny.dev/host-schedulable"

// AnnotationCertNotAfter is an optional pod annotation carrying the
// agent-pod mTLS certificate's expiry as an RFC3339 timestamp. When a
// cert-delivery producer stamps it, the §4.6.1 cert-expiry replacement
// reads it directly; when it is absent, the expiry is derived from the
// pod creation time plus the configured certificate TTL (§10.3 line 338
// sets the agent-pod cert TTL to 4h). The annotation is the forward
// path for a per-pod cert producer; the creation-time fallback keeps
// the replacement working before that producer exists.
const AnnotationCertNotAfter = "lenny.dev/cert-not-after"

// podNodeNameIndex is the field-indexer key the PodReconciler installs
// on Pods so a Node event can enqueue every pod scheduled onto it
// without a full pod list scan.
const podNodeNameIndex = "spec.nodeName"

// Default cert-expiry parameters. §10.3 line 338 sets the agent-pod
// certificate TTL to 4h; §4.6.1 / §10.3 line 342 set the proactive
// replacement window to 30 minutes. Both are operator-tunable via the
// PodReconciler fields and the lenny-controller flags.
const (
	defaultCertTTL             = 4 * time.Hour
	defaultCertExpiryThreshold = 30 * time.Minute
	// defaultCertIssuanceGrace is the §10.3 line 342 cert-issuance grace:
	// "if cert-manager fails to issue a certificate within 60s of pod
	// creation, the pod is marked as unhealthy and replaced".
	defaultCertIssuanceGrace = 60 * time.Second
	// minCertRequeue floors the re-check interval so an idle pod whose
	// cert expiry is far out is re-evaluated periodically rather than
	// hot-looping when it is near the window edge.
	minCertRequeue = time.Minute
)

// CertExpiryMetric is the §10.3 lenny_cert_expiry_seconds gauge surface the
// per-pod reconciler drives. controllermetrics.CertExpiry satisfies it;
// tests inject fakes. Set records one managed pod's remaining certificate
// validity; Clear removes the series when the pod is gone.
//
// spec: §10.3 lines 342–343 — warm-pool cert awareness / CertExpiryImminent.
type CertExpiryMetric interface {
	Set(namespace, pod string, seconds float64)
	Clear(namespace, pod string)
}

// preIdleStates is the set of §4.6.1 pod states that precede idle. The
// §10.3 line 342 cert-issuance grace applies only to these: a pod that has
// not yet been admitted to the warm pool ("before marking them as idle").
// An empty state label (a pod the state machine has not stamped yet) is
// also treated as pre-idle.
var preIdleStates = map[string]bool{
	"":                          true,
	string(state.Warming):       true,
	string(state.SDKConnecting): true,
}

// PodReconciler is the §4.6.1 per-pod arm of the WarmPoolController. The
// controller is the sole evaluator of per-pod host-node schedulability
// and of idle-pod certificate expiry; this reconciler watches the
// managed agent Pods and the cluster Nodes, and on every pod reconcile:
//
//   - sets LabelHostSchedulable from the target Node's .spec.unschedulable
//     and the node.kubernetes.io/unschedulable taint (§4.6.1, §6.2). A
//     Node cordon/uncordon enqueues every pod on the node so the label
//     flips within one reconcile cycle.
//   - replaces an idle pod whose certificate will expire within the
//     configured threshold by transitioning its Sandbox to draining; the
//     SandboxWarmPool reconciler then recreates a fresh pod toward
//     minWarm (§4.6.1 "Track certificate expiry on idle pods", §10.3
//     line 342). A claimed or active pod is never disrupted — its cert is
//     refreshed in place via filesystem-watching TLS (§10.3 line 338).
//
// The reconciler is leader-elected (it carries no NeedLeaderElection
// override, so the controller-runtime default applies) so only one
// replica writes labels and drains pods, matching the §4.6.1 "the
// leader reads the target Node" wording.
type PodReconciler struct {
	// Client is the controller-runtime client backed by the manager
	// cache. The Pod informer is shared with the Sandbox-to-Pod
	// reconciler's Owns(&Pod{}) watch, so this reconciler adds no extra
	// cluster-wide pod caching.
	Client client.Client

	// CertTTL is the agent-pod certificate lifetime used to derive an
	// expiry when a pod carries no AnnotationCertNotAfter. Zero selects
	// defaultCertTTL (4h, §10.3 line 338).
	CertTTL time.Duration
	// CertExpiryThreshold is the §4.6.1 proactive-replacement window: an
	// idle pod whose certificate expires within this duration is drained
	// and recreated. Zero selects defaultCertExpiryThreshold (30m).
	CertExpiryThreshold time.Duration
	// CertIssuanceGrace is the §10.3 line 342 cert-issuance grace: a
	// pre-idle pod that has not presented a valid certificate within this
	// window of its creation is treated as a cert-issuance failure and
	// replaced. Zero selects defaultCertIssuanceGrace (60s). Enforcement
	// is gated on RequireCertIssuance.
	CertIssuanceGrace time.Duration
	// RequireCertIssuance enables the §10.3 line 342 cert-issuance grace
	// check. It is opt-in because the check keys on the
	// lenny.dev/cert-not-after annotation (the per-pod cert producer's
	// forward path documented on AnnotationCertNotAfter): a deployment
	// without that producer never stamps the annotation, so enabling the
	// check there would replace every pre-idle pod. An operator wires a
	// producer that stamps the annotation on cert delivery, then sets this
	// true. Default false preserves the creation-time-plus-TTL fallback and
	// leaves pre-idle pods untouched.
	RequireCertIssuance bool
	// CertMetrics, when set, receives the §10.3 line 342/343
	// lenny_cert_expiry_seconds gauge for every managed pod with a
	// derivable certificate expiry, and a clear when the pod is deleted.
	// Nil disables the gauge; the reconciler still drains expiring pods.
	CertMetrics CertExpiryMetric

	// MaxConcurrentReconciles is the §4.6.1 worker count
	// (--max-concurrent-reconciles). Zero or negative selects the
	// controller-runtime default of 1.
	MaxConcurrentReconciles int
	// QueueFactory, when set, supplies the §4.6.1 bounded,
	// depth-instrumented reconciliation work queue. A nil factory uses
	// the controller-runtime default queue.
	QueueFactory controllermetrics.QueueFactory

	// Now is injectable for tests; nil uses time.Now.
	Now func() time.Time
}

func (r *PodReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *PodReconciler) certTTL() time.Duration {
	if r.CertTTL > 0 {
		return r.CertTTL
	}
	return defaultCertTTL
}

func (r *PodReconciler) certExpiryThreshold() time.Duration {
	if r.CertExpiryThreshold > 0 {
		return r.CertExpiryThreshold
	}
	return defaultCertExpiryThreshold
}

func (r *PodReconciler) certIssuanceGrace() time.Duration {
	if r.CertIssuanceGrace > 0 {
		return r.CertIssuanceGrace
	}
	return defaultCertIssuanceGrace
}

// Reconcile evaluates one managed agent pod: it stamps the host-node
// schedulability label and, for idle pods, drains any pod whose
// certificate is inside the replacement window.
func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var pod corev1.Pod
	if err := r.Client.Get(ctx, req.NamespacedName, &pod); err != nil {
		// A deleted pod's lenny_cert_expiry_seconds series must be dropped
		// so its last (possibly near-zero) value cannot pin the
		// CertExpiryImminent alert in the firing state (§10.3 line 343).
		if apierrors.IsNotFound(err) {
			r.clearCertExpiry(req.Namespace, req.Name)
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// Only the controller-managed warm-pool pods carry the
	// schedulability and cert contracts. A foreign pod that slipped
	// through the watch predicate is ignored.
	if pod.Labels[LabelManaged] != "true" {
		return ctrl.Result{}, nil
	}
	// A pod already being torn down is left to the Sandbox-to-Pod
	// reconciler; re-labeling or re-draining it is pointless. Drop its
	// cert-expiry gauge series here (with the pod still in cache) so the
	// retiring pod stops contributing to the CertExpiryImminent min().
	if pod.DeletionTimestamp != nil {
		r.clearCertExpiry(pod.Namespace, pod.Name)
		return ctrl.Result{}, nil
	}

	// Host-node schedulability labeling requires a scheduled pod. An
	// unscheduled (Pending) pod has no spec.nodeName; §4.6.1 states such
	// pods are never eligible for the task_cleanup precondition, so the
	// absence of the label on them is never observed by the gateway.
	if pod.Spec.NodeName != "" {
		if err := r.reconcileHostSchedulable(ctx, &pod); err != nil {
			return ctrl.Result{}, err
		}
	}

	// §10.3 line 342/343 — publish remaining certificate validity for the
	// CertExpiryImminent alert before any drain decision so the gauge
	// reflects the observed pod even when it is about to be replaced.
	r.publishCertExpiry(&pod)

	// §10.3 line 342 — cert-issuance grace: a pre-idle pod that has not
	// presented a valid certificate within the grace window is a
	// cert-issuance failure and is replaced ahead of the expiry check.
	issueRequeue, replaced, err := r.reconcileCertIssuance(ctx, &pod)
	if err != nil {
		return ctrl.Result{}, err
	}
	if replaced {
		return ctrl.Result{}, nil
	}
	if issueRequeue > 0 {
		return ctrl.Result{RequeueAfter: issueRequeue}, nil
	}

	requeue, err := r.reconcileCertExpiry(ctx, &pod)
	if err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: requeue}, nil
}

// reconcileHostSchedulable reads the pod's node and SSA-patches
// LabelHostSchedulable to the spec-defined value. A missing node (the
// pod's node was deleted) is a no-op: the pod will be evicted and
// rescheduled, and the next reconcile re-labels it. The patch carries
// only the one label under the WPC field manager, so it never disturbs
// the pool/managed/state labels other writers own.
func (r *PodReconciler) reconcileHostSchedulable(ctx context.Context, pod *corev1.Pod) error {
	var node corev1.Node
	if err := r.Client.Get(ctx, client.ObjectKey{Name: pod.Spec.NodeName}, &node); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	want := strconv.FormatBool(hostSchedulable(&node))
	if pod.Labels[LabelHostSchedulable] == want {
		return nil
	}
	patch := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
			Labels:    map[string]string{LabelHostSchedulable: want},
		},
	}
	return retryOnConflictSSA(ctx, func(int) error {
		return r.Client.Patch(ctx, patch, client.Apply, client.FieldOwner(string(ownership.WarmPoolController)))
	})
}

// hostSchedulable reports whether a node accepts new work: it is
// schedulable when .spec.unschedulable is false and it carries no
// node.kubernetes.io/unschedulable taint. A cordon sets both; the taint
// check also catches the kubelet-applied taint that can lag the field.
//
// spec: §4.6.1 "Host-node schedulability labeling".
func hostSchedulable(node *corev1.Node) bool {
	if node.Spec.Unschedulable {
		return false
	}
	for _, t := range node.Spec.Taints {
		if t.Key == corev1.TaintNodeUnschedulable {
			return false
		}
	}
	return true
}

// reconcileCertExpiry drains an idle pod whose certificate is inside the
// replacement window so the pool reconciler recreates a fresh pod. It
// returns the duration after which the pod should be re-checked when the
// cert is still outside the window (so an idle pod with no further events
// is still re-evaluated as it approaches expiry). A non-idle pod and a
// pod with no derivable expiry return a zero requeue.
func (r *PodReconciler) reconcileCertExpiry(ctx context.Context, pod *corev1.Pod) (time.Duration, error) {
	// Only idle pods are proactively replaced. A claimed or active pod is
	// serving a session; its cert is renewed in place (§10.3 line 338),
	// and idle is the only legal source state for the idle→draining edge.
	if pod.Labels[state.LabelState] != string(state.Idle) {
		return 0, nil
	}
	expiry, ok := r.certExpiry(pod)
	if !ok {
		return 0, nil
	}
	threshold := r.certExpiryThreshold()
	remaining := expiry.Sub(r.now())
	if remaining > threshold {
		// Re-check right as the window opens; floor the interval so a
		// far-out expiry does not hot-loop.
		requeue := remaining - threshold
		if requeue < minCertRequeue {
			requeue = minCertRequeue
		}
		return requeue, nil
	}
	// Inside the window (or already expired): drain the backing Sandbox.
	// The Sandbox shares the pod's name and namespace (the Sandbox-to-Pod
	// reconciler builds the pod from the Sandbox name).
	key := client.ObjectKey{Namespace: pod.Namespace, Name: pod.Name}
	return 0, patchSandboxDraining(ctx, r.Client, key, true)
}

// publishCertExpiry sets the §10.3 lenny_cert_expiry_seconds gauge for the
// pod to its remaining certificate validity. A pod with no derivable
// expiry (no annotation and a zero creation timestamp) emits no sample.
func (r *PodReconciler) publishCertExpiry(pod *corev1.Pod) {
	if r.CertMetrics == nil {
		return
	}
	expiry, ok := r.certExpiry(pod)
	if !ok {
		return
	}
	r.CertMetrics.Set(pod.Namespace, pod.Name, expiry.Sub(r.now()).Seconds())
}

// clearCertExpiry removes the §10.3 lenny_cert_expiry_seconds series for a
// pod that no longer exists or is terminating.
func (r *PodReconciler) clearCertExpiry(namespace, name string) {
	if r.CertMetrics == nil {
		return
	}
	r.CertMetrics.Clear(namespace, name)
}

// reconcileCertIssuance enforces the §10.3 line 342 cert-issuance grace: a
// pre-idle pod (no state label yet, warming, or sdk_connecting) that has
// not presented a valid certificate within CertIssuanceGrace of its
// creation is a cert-issuance failure and is drained so the pool reconciler
// replaces it. The check is gated on RequireCertIssuance because it keys on
// the lenny.dev/cert-not-after annotation — a deployment without a per-pod
// cert producer never stamps it, and enforcing the check there would
// replace every pre-idle pod (see AnnotationCertNotAfter). It returns
// replaced=true when the pod was drained, or a positive requeue to re-check
// a pod still inside the grace window.
func (r *PodReconciler) reconcileCertIssuance(ctx context.Context, pod *corev1.Pod) (time.Duration, bool, error) {
	if !r.RequireCertIssuance {
		return 0, false, nil
	}
	if !preIdleStates[pod.Labels[state.LabelState]] {
		return 0, false, nil
	}
	if r.certIssued(pod) {
		return 0, false, nil
	}
	created := pod.CreationTimestamp.Time
	if created.IsZero() {
		// No creation time to measure the grace against; leave the pod for
		// a later reconcile once the timestamp is observed.
		return 0, false, nil
	}
	grace := r.certIssuanceGrace()
	age := r.now().Sub(created)
	if age < grace {
		// Still inside the grace window: re-check right as it closes.
		return grace - age, false, nil
	}
	// Grace exhausted with no valid certificate: cert-manager failed to
	// issue. Drain the backing Sandbox for replacement (idle is not
	// required — the pod never reached idle). spec: §10.3 line 342.
	key := client.ObjectKey{Namespace: pod.Namespace, Name: pod.Name}
	if err := patchSandboxDraining(ctx, r.Client, key, false); err != nil {
		return 0, false, err
	}
	return 0, true, nil
}

// certIssued reports whether the pod carries a valid (present, parseable,
// not-yet-expired) lenny.dev/cert-not-after annotation. Unlike certExpiry
// it has no creation-time fallback: for the §10.3 line 342 issuance check,
// the absence of the annotation is the signal that no certificate has been
// delivered yet.
func (r *PodReconciler) certIssued(pod *corev1.Pod) bool {
	v := pod.Annotations[AnnotationCertNotAfter]
	if v == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return false
	}
	return t.After(r.now())
}

// certExpiry resolves a pod's certificate expiry. It prefers an explicit
// AnnotationCertNotAfter (RFC3339); absent or unparseable, it falls back
// to the pod creation time plus the configured cert TTL. It reports
// ok=false when neither source yields a time (no annotation and a
// zero creation timestamp), so the caller leaves the pod untouched.
func (r *PodReconciler) certExpiry(pod *corev1.Pod) (time.Time, bool) {
	if v := pod.Annotations[AnnotationCertNotAfter]; v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t, true
		}
	}
	created := pod.CreationTimestamp.Time
	if created.IsZero() {
		return time.Time{}, false
	}
	return created.Add(r.certTTL()), true
}

// patchSandboxDraining transitions a Sandbox to the draining phase via
// SSA under the WPC field manager, re-applying the live WPC-owned status
// fields so the partial apply does not clobber PodName/NodeName/PodIP.
// It is a no-op when the Sandbox is absent or already draining. When
// requireIdle is true the transition is skipped unless the live phase is
// idle, so a pod claimed between observation and apply is never disrupted
// (the idle→draining edge is the only legal one for proactive retirement).
func patchSandboxDraining(ctx context.Context, c client.Client, key client.ObjectKey, requireIdle bool) error {
	return retryOnConflictSSA(ctx, func(int) error {
		var live lennyv1.Sandbox
		if err := c.Get(ctx, key, &live); err != nil {
			return client.IgnoreNotFound(err)
		}
		if live.Status.Phase == string(state.Draining) {
			return nil
		}
		if requireIdle && live.Status.Phase != string(state.Idle) {
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
		patch.Status.Phase = string(state.Draining)
		patch.Status.PodName = live.Status.PodName
		patch.Status.NodeName = live.Status.NodeName
		patch.Status.PodIP = live.Status.PodIP
		patch.Status.ObservedGeneration = live.Generation
		return c.Status().Patch(ctx, patch, client.Apply, client.FieldOwner(string(ownership.WarmPoolController)))
	})
}

// podsOnNode maps a Node event to a reconcile request for every managed
// pod scheduled onto that node, so a cordon/uncordon re-labels each
// affected pod within one reconcile cycle (§4.6.1).
func (r *PodReconciler) podsOnNode(ctx context.Context, obj client.Object) []reconcile.Request {
	node, ok := obj.(*corev1.Node)
	if !ok {
		return nil
	}
	var pods corev1.PodList
	if err := r.Client.List(ctx, &pods, client.MatchingFields{podNodeNameIndex: node.Name}); err != nil {
		return nil
	}
	reqs := make([]reconcile.Request, 0, len(pods.Items))
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.Labels[LabelManaged] != "true" {
			continue
		}
		reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(p)})
	}
	return reqs
}

// managedPodPredicate restricts the pod watch to controller-managed
// warm-pool pods so the reconciler never wakes on unrelated pods.
func managedPodPredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetLabels()[LabelManaged] == "true"
	})
}

// SetupWithManager registers the per-pod reconciler. It reconciles
// managed Pods and additionally wakes on Node changes (mapped to the
// pods on the changed node). A field index on spec.nodeName backs the
// Node→Pod fan-out.
func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &corev1.Pod{}, podNodeNameIndex,
		func(o client.Object) []string {
			p, ok := o.(*corev1.Pod)
			if !ok || p.Spec.NodeName == "" {
				return nil
			}
			return []string{p.Spec.NodeName}
		}); err != nil {
		return err
	}
	opts := controller.Options{}
	if r.MaxConcurrentReconciles > 0 {
		opts.MaxConcurrentReconciles = r.MaxConcurrentReconciles
	}
	if r.QueueFactory != nil {
		opts.NewQueue = r.QueueFactory
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}, builder.WithPredicates(managedPodPredicate())).
		Watches(&corev1.Node{}, handler.EnqueueRequestsFromMapFunc(r.podsOnNode)).
		Named("warmpool-pod").
		WithOptions(opts).
		Complete(r)
}
