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
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	"github.com/lennylabs/lenny/pkg/adapter/sharedassets"
	"github.com/lennylabs/lenny/pkg/admission/ownership"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/controllermetrics"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/lifecycle"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/resourceclass"
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
	// AdapterUID, AgentUID, and CredReadersGID override the §13.1
	// non-root identities stamped onto the adapter container, the runtime
	// container, and the credential-tmpfs fsGroup. A zero value leaves
	// the podspec package default in force. The lenny-pod-security and
	// ephemeral-container-cred-guard webhooks MUST be wired with the same
	// values (from the same Helm source) so a built pod passes their UID
	// checks. spec: §13.1 line 7. F-13.1.16.
	AdapterUID     int64
	AgentUID       int64
	CredReadersGID int64
	// GatewayGRPCAddr is the §8.6/§9.1 gateway GatewayControl address
	// (host:port) stamped onto the adapter container so it forwards a
	// type:agent runtime's platform tool calls to the gateway. Empty
	// leaves the platform MCP server unstarted. spec: §9.1 lines 14-31.
	// F-9.1.1.
	GatewayGRPCAddr string
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
	// DedicatedDNSClusterIP is the ClusterIP of the lenny-agent-dns Service
	// in lenny-system (the chart's coredns.clusterIP value). spec: §13.2
	// lines 470-490 (K8S-033) — when set, the pod builder stamps
	// `dnsPolicy: None` plus a `dnsConfig` pointing the pod at the
	// dedicated CoreDNS instance, because the agent-namespace NetworkPolicy
	// blocks the kube-system kube-dns the default ClusterFirst policy would
	// target. Pools that opt out via `dnsPolicy: cluster-default` keep the
	// default behavior. Empty leaves the cluster default in force.
	DedicatedDNSClusterIP string
	// ReleaseNamespace is the Helm release namespace (lenny-system), used as
	// the first search domain in the dedicated-DNS dnsConfig so in-cluster
	// Service short-names resolve. Empty omits the namespace search domain.
	ReleaseNamespace string
	// StatusDedup is the §4.6.1 statusUpdateDeduplicationWindow gate. When
	// set, a Sandbox status write within the window of the previous write
	// for the same Sandbox is deferred and the reconcile requeued so the
	// trailing status lands once the window expires. A nil gate disables
	// deduplication.
	StatusDedup *statusdedup.Gate
	// RuntimeClassNameOverrides remaps the §5.3 isolation profile to a
	// cluster-specific RuntimeClass name (spec: §17.5 line 3). Set from
	// the chart's `isolation.runtimeClassNames` Helm values so a cluster
	// running gVisor as `runsc` or Kata as `kata-qemu` does not require
	// renaming in-cluster RuntimeClass objects to Lenny's literal
	// defaults. A nil or empty map preserves the §5.3 defaults
	// (standard→runc, sandboxed→gvisor, microvm→kata).
	RuntimeClassNameOverrides map[isolation.Profile]string
	// MaxConcurrentReconciles is the §4.6.1 worker count
	// (--max-concurrent-reconciles, default 1). Zero or negative selects
	// the controller-runtime default of 1.
	MaxConcurrentReconciles int
	// ResourceClasses maps a §5.2 resource-class name (small/medium/large or
	// a deployer-defined class) to container CPU/memory requests and limits.
	// spec: §6.4 line 413 — the reconciler resolves each Sandbox's class
	// through this registry and stamps the result on the agent containers so
	// the pod carries a per-pod memory cgroup limit that accounts for the
	// tmpfs volumes. A nil registry uses resourceclass.DefaultRegistry.
	ResourceClasses resourceclass.Registry
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

	// §6.1 lines 30-69: a pool whose runtime declares capabilities.preConnect
	// (and whose §6.1 circuit breaker has not disabled SDK-warm) warms
	// through sdk_connecting rather than straight to pod-warm idle. The
	// pod-warm planner governs every other pool.
	var decision lifecycle.Decision
	sdkWarm := r.resolveSDKWarm(ctx, &sb)
	if sdkWarm.sdkWarmActive() {
		// §6.1/§3.3: the watchdog clock and the non-failure terminus differ
		// per edge. On the recycle re-warm edge the per-pod SandboxClaim
		// carries a rewarmStartedAt stamp (binding state recycling); read it
		// so sdkWarmInputs re-anchors the clock to the re-warm start and
		// flips the success terminus to reserved. The edge distinction only
		// matters while the pod sits in sdk_connecting, so the claim read is
		// scoped to that phase: warm-fill entry (warming) and every phase
		// past idle carry no re-warm anchor.
		var rewarm *time.Time
		if state.State(sb.Status.Phase) == state.SDKConnecting {
			stamp, err := r.observeRewarm(ctx, &sb)
			if err != nil {
				return ctrl.Result{}, err
			}
			rewarm = stamp
		}
		in := sdkWarmInputs(&sb, obs, &pod, sdkWarm, rewarm, time.Now())
		decision = lifecycle.DecideSDKWarm(in)
		if in.TimedOut() {
			sdkConnectTimeoutTotal.WithLabelValues(sb.Spec.PoolRef).Inc()
		}
	} else {
		decision = lifecycle.Decide(state.State(sb.Status.Phase), obs)
	}

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

// resourceClassName returns the §5.2 resource-class name to resolve for the
// Sandbox's pod. A §12.6 CreatePod pod stamps the resolved class on
// Sandbox.spec.resourceClass directly; a warm pod leaves it empty, so the
// reconciler reads the class from the pool's SandboxTemplate. When neither
// names a class the §5.1 line 357 deployer-safe default applies. spec:
// §5.1/§5.2, §6.4 line 413.
func (r *Reconciler) resourceClassName(ctx context.Context, sb *lennyv1.Sandbox) string {
	if sb.Spec.ResourceClass != "" {
		return sb.Spec.ResourceClass
	}
	if sb.Spec.PoolRef != "" {
		var pool lennyv1.SandboxWarmPool
		if err := r.Client.Get(ctx, client.ObjectKey{Namespace: sb.Namespace, Name: sb.Spec.PoolRef}, &pool); err == nil && pool.Spec.TemplateRef != "" {
			var tmpl lennyv1.SandboxTemplate
			if err := r.Client.Get(ctx, client.ObjectKey{Namespace: sb.Namespace, Name: pool.Spec.TemplateRef}, &tmpl); err == nil && tmpl.Spec.ResourceClass != "" {
				return tmpl.Spec.ResourceClass
			}
		}
	}
	return resourceclass.DefaultClass
}

// resolveResources resolves the Sandbox's §5.2 resource class to container
// CPU/memory requirements through the registry. An unknown class (one not
// in the registry) resolves to no explicit requirements rather than failing
// pod creation, matching the pre-registry behavior for deployments that
// have not configured the class. spec: §6.4 line 413.
func (r *Reconciler) resolveResources(ctx context.Context, sb *lennyv1.Sandbox) *corev1.ResourceRequirements {
	reg := r.ResourceClasses
	if reg == nil {
		reg = resourceclass.DefaultRegistry()
	}
	req, ok := reg.Resolve(r.resourceClassName(ctx, sb))
	if !ok {
		return nil
	}
	return &req
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
	// spec: §6.4 line 409 — encode the Runtime's inline sharedAssets for the
	// adapter's --shared-assets flag so it materializes them into the
	// read-only /workspace/shared tree at warm time. F-6.4.3.
	sharedAssetsArg, err := encodeSharedAssets(rt.Spec.SharedAssets)
	if err != nil {
		return fmt.Errorf("encode shared assets for runtime %s: %w", sb.Spec.RuntimeRef, err)
	}
	// spec: §4.7 — resolve the nonce-only-mode render decision. The flag is
	// rendered only when the resolved Runtime is a deploymentModel: sidecar
	// runtime carrying requireSoPeercred: false AND the pool's
	// SandboxTemplate carries the PSC-mirrored acknowledgeNonceOnlyAuth
	// acknowledgment. The gate is fail-closed: a missing acknowledgment, a
	// non-sidecar deployment model, or an unset/true runtime field all leave
	// the flag off, so the adapter default of true (enforce SO_PEERCRED)
	// governs.
	nonceOnly := r.resolveNonceOnly(ctx, sb, &rt)
	pod, err := podspec.Build(podspec.Inputs{
		Name:         sb.Name,
		Namespace:    sb.Namespace,
		Labels:       sb.Labels,
		RuntimeImage: rt.Spec.Image,
		AdapterImage: r.AdapterImage,
		// spec: §9.1 lines 14-31 — point the adapter at the gateway's
		// GatewayControl listener so a type:agent runtime's platform tool
		// calls reach the gateway platform tool surface. F-9.1.1.
		GatewayGRPCAddr:  r.GatewayGRPCAddr,
		IsolationProfile: profile,
		// spec: §17.5 line 3 — apply operator RuntimeClass-name
		// overrides (e.g. gvisor→runsc, kata→kata-qemu) so the chart's
		// `isolation.runtimeClassNames` Helm values reach the pod spec
		// without requiring rename of in-cluster RuntimeClass objects.
		RuntimeClassNameOverrides: r.RuntimeClassNameOverrides,
		DeploymentModel:           rt.Spec.DeploymentModel,
		// spec: §4.7 — the resolved nonce-only render decision. When false,
		// the builder renders --require-so-peercred=false into the sidecar
		// adapter container; nil leaves the adapter default of true in force.
		RequireSoPeercred: nonceOnly,
		EgressCapture:     r.resolveEgressCapture(sb),
		// spec: §5.2 line 516 — the SandboxTemplate's deployer-set
		// terminationGracePeriodSeconds replaces the 120s default base, and
		// the maxTerminationGracePeriodSeconds ceiling clamps the pod's
		// grace period down when the deployer has declared a hard cap.
		TerminationGraceSeconds:    graceBase,
		MaxTerminationGraceSeconds: graceMax,
		// spec: §6.1 line 67 — a preConnect (SDK-warm) runtime may reach
		// `sdk_connecting`, so the pod's grace period is floored at
		// `LENNY_DEMOTE_TIMEOUT_SECONDS + 5s` to give the adapter time to run
		// its bounded DemoteSDK teardown on SIGTERM before the kubelet sends
		// SIGKILL.
		PreConnect: rt.Spec.Capabilities != nil && rt.Spec.Capabilities.PreConnect,
		// spec: §5.2 lines 631-636 — the WarmPoolController copied the
		// pool's topology spread constraints onto Sandbox.spec; stamp them
		// onto the pod here.
		TopologySpreadConstraints: sb.Spec.TopologySpreadConstraints,
		// spec: §6.1 line 14 / §10.3 — mount the audience-bound projected
		// service-account token (when an audience is configured) under the
		// zero-RBAC agent ServiceAccount.
		SATokenAudience:    r.SATokenAudience,
		ServiceAccountName: r.AgentServiceAccountName,
		// spec: §6.4 lines 416-419 — pass the Runtime's workspaceTier through
		// so the pod builder stamps the `lenny.dev/workspace-tier: t4` label,
		// the T4 nodeSelector, and the T4 NoSchedule toleration when the
		// Runtime declares T4. The lenny-t4-node-isolation admission webhook
		// (failurePolicy: Fail) rejects any T4 pod missing these constraints
		// with the §6.4 STR-003 message.
		WorkspaceTier: rt.Spec.WorkspaceTier,
		// spec: §6.4 line 409 — the encoded inline shared-asset set the
		// adapter materializes into the read-only /workspace/shared tree.
		SharedAssetsArg: sharedAssetsArg,
		// spec: §13.2 lines 470-490 (K8S-033) — point the agent pod's
		// resolver at the dedicated lenny-system CoreDNS instance unless the
		// pool opted out via the lenny.dev/dns-policy: cluster-default label.
		DedicatedDNSClusterIP: r.DedicatedDNSClusterIP,
		ReleaseNamespace:      r.ReleaseNamespace,
		// spec: §5.2 / §6.4 line 413 — resolve the Sandbox's resource class
		// to container CPU/memory requests and limits so the pod has a
		// per-pod cgroup boundary that accounts for the memory-backed tmpfs
		// volumes charging against the pod memory limit.
		Resources: r.resolveResources(ctx, sb),
		// spec: §13.1 line 7 — carry the operator-tunable non-root
		// identities so the pod the controller stamps matches the UIDs the
		// lenny-pod-security / ephemeral-container-cred-guard webhooks
		// enforce (both wired from the same Helm value). F-13.1.16.
		AdapterUID:     r.AdapterUID,
		AgentUID:       r.AgentUID,
		CredReadersGID: r.CredReadersGID,
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
	// spec: §4.7 / §4.4 — record the resolved render decision on the
	// Sandbox.spec.requireSoPeercred carrier BEFORE creating the pod, so the
	// decision is recoverable. The carrier is WPC-owned (§4.6.3 Sandbox.spec.*),
	// so the write adds no RBAC. It lets the pool-level SecurityDegradedMode
	// trigger read the decision back from the member Sandboxes without resolving
	// the Runtime CR (§4.5), and the SOPeercredDisabled=True pod-level condition
	// is derived from it in buildSandboxStatusPatch (the single WPC status
	// apply), so the two same-manager status applies in this reconcile do not
	// clobber each other.
	//
	// Persisting the carrier first is what makes "set the condition for every
	// Sandbox rendered with the flag" (§4.4) crash-safe. The carrier is the sole
	// source of the SOPeercredDisabled condition, and createPod is reached only
	// on lifecycle.ActionCreatePod, which the planner emits only while the pod is
	// absent. If the carrier write failed after Create, a requeue would find the
	// pod present, never re-enter createPod, and leave the pod running in
	// nonce-only mode with no carrier and no condition forever (fail-open on a
	// security-degradation surfacing path). Writing the carrier first means a
	// failure leaves the pod uncreated, so the next reconcile re-enters
	// ActionCreatePod (pod still absent), re-resolves, and retries the carrier
	// write idempotently (recordNonceOnlyCarrier no-ops once the carrier records
	// false). The in-memory carrier is then set so the syncStatus pass later in
	// this reconcile derives the condition from it.
	//
	// The symmetric revert case uses the same persist-first ordering. When the
	// render decision is no longer nonce-only (resolveNonceOnly returns nil) but
	// the Sandbox already carries a stale false carrier — the narrow window where
	// a carrier write succeeded, the pod Create failed, the operator reverted the
	// Runtime field, and the create-retry re-enters here — the else branch clears
	// the carrier before Create so the carrier and the pod's render decision stay
	// coincident (§4.4) and a reverted, SO_PEERCRED-enforcing pod does not latch
	// SOPeercredDisabled=True or the pool-level SecurityDegradedMode (§4.5).
	if nonceOnly != nil && !*nonceOnly {
		if err := r.recordNonceOnlyCarrier(ctx, sb); err != nil {
			return fmt.Errorf("record nonce-only decision for sandbox %s: %w", sb.Name, err)
		}
		sb.Spec.RequireSoPeercred = nonceOnly
	} else if sb.Spec.RequireSoPeercred != nil && !*sb.Spec.RequireSoPeercred {
		// spec: §4.5 revert invariant — the no-flag render path must clear a
		// stale carrier so the carrier and the pod's actual render decision
		// coincide (§4.4). A Sandbox object reused across a carrier write that
		// preceded a failed pod Create plus an operator revert of the Runtime
		// field reaches this branch on the create retry: resolveNonceOnly now
		// returns nil (the reverted Runtime renders no flag), but the persisted
		// false carrier would otherwise survive and drive SOPeercredDisabled=True
		// in buildSandboxStatusPatch and latch the pool-level SecurityDegradedMode
		// (§4.5) on a pod that enforces SO_PEERCRED. Clear the carrier BEFORE the
		// pod is created, mirroring the persist-first ordering: a failed clear
		// leaves the pod uncreated, so the next reconcile re-enters
		// ActionCreatePod and retries the clear idempotently.
		if err := r.clearNonceOnlyCarrier(ctx, sb); err != nil {
			return fmt.Errorf("clear nonce-only decision for sandbox %s: %w", sb.Name, err)
		}
		sb.Spec.RequireSoPeercred = nil
	}
	if err := r.Client.Create(ctx, pod); err != nil {
		return fmt.Errorf("create pod: %w", err)
	}
	return nil
}

// resolveNonceOnly computes the §4.7 nonce-only-mode render decision for a
// Sandbox's pod. It returns a pointer to false only when the resolved
// Runtime is a deploymentModel: sidecar runtime carrying
// requireSoPeercred: false AND the pool's SandboxTemplate carries the
// PSC-mirrored acknowledgeNonceOnlyAuth acknowledgment; otherwise it returns
// nil, which leaves the adapter default of true (enforce SO_PEERCRED) in
// force. The gate is fail-closed: any unresolved precondition leaves the
// flag off. spec: §4.7.
func (r *Reconciler) resolveNonceOnly(ctx context.Context, sb *lennyv1.Sandbox, rt *lennyv1.Runtime) *bool {
	// The activation field applies to the sidecar model only; the embedded
	// model has no adapter-agent socket boundary to fall back from (§4.7),
	// and the RuntimeReconciler refuses to mirror an embedded runtime that
	// sets the field. An empty deploymentModel defaults to sidecar.
	if rt.Spec.DeploymentModel == string(podspec.DeploymentEmbedded) {
		return nil
	}
	if rt.Spec.RequireSoPeercred == nil || *rt.Spec.RequireSoPeercred {
		return nil
	}
	// The render gate requires the PSC-mirrored pool acknowledgment. Without
	// it the pool keeps full SO_PEERCRED enforcement even when the runtime
	// requests nonce-only mode (the §4.7 two-point gate against a Runtime CR
	// applied directly with kubectl).
	if !r.poolAcknowledgesNonceOnly(ctx, sb) {
		return nil
	}
	disabled := false
	return &disabled
}

// poolAcknowledgesNonceOnly reports whether the Sandbox's pool carries the
// §5.3 acknowledgeNonceOnlyAuth acknowledgment, mirrored by the
// PoolScalingController onto the pool's SandboxTemplate.spec. It walks
// Sandbox → SandboxWarmPool → SandboxTemplate; any missing resource fails
// closed (no acknowledgment), so a transient lookup miss leaves full
// SO_PEERCRED enforcement rather than activating nonce-only mode. spec:
// §4.7, §5.3.
func (r *Reconciler) poolAcknowledgesNonceOnly(ctx context.Context, sb *lennyv1.Sandbox) bool {
	if sb.Spec.PoolRef == "" {
		return false
	}
	var pool lennyv1.SandboxWarmPool
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: sb.Namespace, Name: sb.Spec.PoolRef}, &pool); err != nil {
		return false
	}
	if pool.Spec.TemplateRef == "" {
		return false
	}
	var tmpl lennyv1.SandboxTemplate
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: sb.Namespace, Name: pool.Spec.TemplateRef}, &tmpl); err != nil {
		return false
	}
	return tmpl.Spec.AcknowledgeNonceOnlyAuth
}

// recordNonceOnlyCarrier records the render decision on
// Sandbox.spec.requireSoPeercred. The carrier is the pool-level
// SecurityDegradedMode trigger's source, so the WarmPoolController can read
// the decision back from the member Sandboxes without resolving the Runtime
// CR (§4.5). The write is skipped when the carrier already records false.
func (r *Reconciler) recordNonceOnlyCarrier(ctx context.Context, sb *lennyv1.Sandbox) error {
	if sb.Spec.RequireSoPeercred != nil && !*sb.Spec.RequireSoPeercred {
		return nil
	}
	// The carrier is a single optional Sandbox.spec field. A typed SSA apply
	// patch built from a Go struct would serialize the required runtimeRef
	// and poolRef fields as empty strings, which SSA reads as an intent to
	// set them and which conflicts with the field manager that created the
	// Sandbox (the WarmPool reconciler's plain Create). A raw apply patch
	// carrying only spec.requireSoPeercred claims that one field without
	// touching the required fields, so it coexists with the creating manager.
	body := []byte(fmt.Sprintf(
		`{"apiVersion":%q,"kind":"Sandbox","metadata":{"name":%q,"namespace":%q},"spec":{"requireSoPeercred":false}}`,
		lennyv1.GroupVersion.String(), sb.Name, sb.Namespace,
	))
	patch := &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: sb.Name, Namespace: sb.Namespace},
	}
	return retryOnConflictSSA(ctx, func(int) error {
		return r.Client.Patch(ctx, patch, client.RawPatch(types.ApplyPatchType, body),
			client.FieldOwner(string(ownership.WarmPoolController)))
	})
}

// clearNonceOnlyCarrier drops a stale Sandbox.spec.requireSoPeercred carrier
// when the resolved render decision is no longer nonce-only (§4.5 revert
// invariant). It re-applies the WarmPoolController's SSA patch with an empty
// spec, which removes the WPC-owned requireSoPeercred field from the live
// object so the carrier returns to nil and the SOPeercredDisabled condition
// and the pool-level SecurityDegradedMode trigger no longer derive from it.
// The same raw-patch reasoning as recordNonceOnlyCarrier applies: a typed SSA
// apply would serialize the required runtimeRef and poolRef fields as empty
// strings and conflict with the creating manager, so the patch body carries
// only the (now empty) spec, claiming no field while dropping the carrier.
func (r *Reconciler) clearNonceOnlyCarrier(ctx context.Context, sb *lennyv1.Sandbox) error {
	body := []byte(fmt.Sprintf(
		`{"apiVersion":%q,"kind":"Sandbox","metadata":{"name":%q,"namespace":%q},"spec":{}}`,
		lennyv1.GroupVersion.String(), sb.Name, sb.Namespace,
	))
	patch := &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: sb.Name, Namespace: sb.Namespace},
	}
	return retryOnConflictSSA(ctx, func(int) error {
		return r.Client.Patch(ctx, patch, client.RawPatch(types.ApplyPatchType, body),
			client.FieldOwner(string(ownership.WarmPoolController)))
	})
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
// zero-value field as an intentional set ("Phase=”"), so omitting a
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
	// spec: §4.7 / §4.4 — the SOPeercredDisabled condition is derived from
	// the WPC-owned Sandbox.spec.requireSoPeercred carrier rather than written
	// in a separate status apply. Folding it into the single WPC status apply
	// keeps the two same-manager applies in one reconcile from clobbering each
	// other (an apply from a field manager sets exactly that manager's owned
	// field set, so a later apply omitting the condition would drop it).
	conds := nonceOnlyConditions(live)
	conditionsChanged := !equalSOPeercredCondition(before.Conditions, conds)

	if !conditionsChanged &&
		want.Phase == before.Phase &&
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
	patch.Status.Conditions = conds
	return patch
}

// nonceOnlyConditions returns the WPC-owned condition entries derived from
// the Sandbox's §4.7 nonce-only carrier. A Sandbox whose
// Sandbox.spec.requireSoPeercred is explicitly false was rendered in
// nonce-only mode, so it carries SOPeercredDisabled=True; any other carrier
// value carries no entry, so the WPC owns no SOPeercredDisabled condition and
// the apply removes a stale one. The condition is the authoritative pod-level
// signal: the adapter holds no apiserver access (§10.3), so the render
// decision and the pod's runtime state coincide. spec: §4.4, §4.7.
func nonceOnlyConditions(live *lennyv1.Sandbox) []metav1.Condition {
	if live.Spec.RequireSoPeercred == nil || *live.Spec.RequireSoPeercred {
		return nil
	}
	return []metav1.Condition{{
		Type:               lennyv1.SandboxConditionSOPeercredDisabled,
		Status:             metav1.ConditionTrue,
		Reason:             "NonceOnlyMode",
		Message:            "Pod rendered with --require-so-peercred=false; SO_PEERCRED enforcement is disabled.",
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: live.Generation,
	}}
}

// equalSOPeercredCondition reports whether the live conditions already carry
// the desired SOPeercredDisabled state, ignoring LastTransitionTime so an
// unchanged condition does not force a status write every reconcile.
func equalSOPeercredCondition(live, want []metav1.Condition) bool {
	find := func(cs []metav1.Condition) *metav1.Condition {
		for i := range cs {
			if cs[i].Type == lennyv1.SandboxConditionSOPeercredDisabled {
				return &cs[i]
			}
		}
		return nil
	}
	lc, wc := find(live), find(want)
	if lc == nil || wc == nil {
		return lc == nil && wc == nil
	}
	return lc.Status == wc.Status && lc.Reason == wc.Reason && lc.Message == wc.Message
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

// encodeSharedAssets converts a Runtime's inline §6.4 sharedAssets into
// the transport-safe form the pod spec carries to the adapter on the
// --shared-assets flag. An empty list yields the empty string, which the
// pod builder reads as "mount /workspace/shared empty and read-only".
// spec: §6.4 line 409 — F-6.4.3.
func encodeSharedAssets(assets []lennyv1.SharedAsset) (string, error) {
	if len(assets) == 0 {
		return "", nil
	}
	specs := make([]sharedassets.FileSpec, 0, len(assets))
	for _, a := range assets {
		specs = append(specs, sharedassets.FileSpec{
			Path:    a.Path,
			Content: a.Content,
			Mode:    a.Mode,
		})
	}
	return sharedassets.Encode(specs)
}
