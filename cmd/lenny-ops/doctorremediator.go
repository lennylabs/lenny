// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/lennylabs/lenny/pkg/ops/diagnostics"
	"github.com/lennylabs/lenny/pkg/ops/doctor"
)

// The §25.6 doctor remediation coordinates. CoreDNS ships as the
// `coredns` Deployment fronted by the `kube-dns` Service in kube-system
// on every conformant cluster; the §25.6 line 2974 opt-out annotation
// and the standard rollout-restart annotation are well-known strings.
const (
	doctorOptOutAnnotation = "lenny.dev/doctor-optout"
	// doctorRedriveAnnotation is the annotation the warmPoolStuckReplenish fix
	// stamps on a stalled SandboxWarmPool to re-drive the WarmPoolController.
	// The apiserver honors an annotation write (advancing resourceVersion and
	// emitting a watch Update event) where a direct .metadata.generation write
	// is a no-op, so this achieves the §25.6 "triggers controller to re-drive"
	// mechanism against a real cluster.
	doctorRedriveAnnotation = "lenny.dev/doctor-redrive"
	corednsNamespace        = "kube-system"
	corednsDeployment       = "coredns"
	kubeDNSService          = "kube-dns"
	kubeDNSPodSelector      = "k8s-app=kube-dns"
	restartedAtAnnotation   = "kubectl.kubernetes.io/restartedAt"
	issueTempCertAnnotation = "cert-manager.io/issue-temporary-certificate"
	certExpiryWindow        = 7 * 24 * time.Hour
)

// poolDiagnosisSource is the §25.6.1 pool-diagnosis surface the
// warmPoolStuckReplenish detection reads its DEMAND_EXCEEDS_SUPPLY
// bottleneck classification and pod-state breakdown from. It is the
// consumer-side narrowing of diagnostics.DiagnosticService to the one
// method this detection needs, injected so the remediator does not
// re-derive the claim-rate-versus-replenishment-rate signal the §25.6.1
// DataSource already classifies. A nil source leaves warmPoolStuckReplenish
// undetected (reported not_detected), matching the Helm-dependent findings.
//
// spec: §25.6 line 2956, §25.6.1 (PoolBottleneck classification).
type poolDiagnosisSource interface {
	DiagnosePool(ctx context.Context, poolName string) (*diagnostics.PoolDiagnosis, error)
}

// k8sDoctorRemediator is the production §25.6 Remediator. It detects and
// idempotently remediates the five §25.6 fixable findings over the
// client-go typed and dynamic clients, plus an injected Helm-render
// source for the two findings that re-apply a rendered chart template and
// the §25.6.1 pool-diagnosis source for warmPoolStuckReplenish.
//
// Coverage:
//   - coreDnsStuckEndpoint: rolling restart of the CoreDNS Deployment.
//   - certManagerExpiring: force re-issuance (annotate + delete Secret).
//   - warmPoolStuckReplenish: stamp a re-drive annotation on the
//     SandboxWarmPool so the apiserver emits a watch event and the controller
//     re-drives the stalled pool (a direct generation write is server-side
//     no-op, so it never re-drives).
//   - bootstrapConfigDrift: re-apply the Helm-rendered lenny-bootstrap
//     ConfigMap when its live content diverges from the rendered value.
//   - prometheusRuleMissing: re-apply the Helm-rendered PrometheusRule /
//     ServiceMonitor when monitoring is enabled but they are absent.
//
// The bootstrapConfigDrift and prometheusRuleMissing findings need the
// Helm-rendered chart template lenny-ops does not itself hold; they are
// driven by the injected HelmRenderSource, which the operator threads
// through chart values. warmPoolStuckReplenish needs the §25.6.1
// pool-diagnosis source (`poolDx`) for its DEMAND_EXCEEDS_SUPPLY
// classification. When a required source is nil (or reports monitoring
// disabled / no bootstrap render), the affected finding is not detected,
// so the orchestrator reports it not_detected rather than a false success —
// the ErrManualRemediation `remediation: manual` path is only reached by
// a code outside the fixable table, never by these three, because that
// path requires a successful Detect first.
//
// spec: §25.6 lines 2952-2974. F-25.6.2, F-DR-1.
type k8sDoctorRemediator struct {
	clientset kubernetes.Interface
	dyn       dynamic.Interface
	// releaseNS is the namespace holding the cert-manager Certificate CRs
	// the certManagerExpiring fix acts on, the lenny-bootstrap ConfigMap
	// the bootstrapConfigDrift fix re-applies, and the PrometheusRule /
	// ServiceMonitor the prometheusRuleMissing fix asserts.
	releaseNS string
	// helm yields the §25.6 rendered references the bootstrapConfigDrift
	// and prometheusRuleMissing fixes compare against and re-apply. A nil
	// source leaves both findings undetected (reported not_detected).
	helm doctor.HelmRenderSource
	// poolDx classifies the §25.6.1 warm-pool bottleneck the
	// warmPoolStuckReplenish detection reads. A nil source leaves that
	// finding undetected (reported not_detected).
	poolDx poolDiagnosisSource
	now    func() time.Time
}

// sandboxWarmPoolGVR is the §5.2 SandboxWarmPool custom resource the
// warmPoolStuckReplenish fix enumerates and re-drives (re-drive annotation).
// Pools are platform-global CRs, so the fix acts cluster-wide.
var sandboxWarmPoolGVR = schema.GroupVersionResource{
	Group:    "lenny.dev",
	Version:  "v1alpha1",
	Resource: "sandboxwarmpools",
}

// sandboxTemplateGVR is the §5.2 SandboxTemplate the WarmPoolController
// writes the PoolDrained condition onto (`updateTemplateCondition`,
// pkg/controller/warmpool/controller.go). The pool's own status never
// carries that condition, so the warmPoolStuckReplenish dwell is read
// from the template the pool references (spec.templateRef).
var sandboxTemplateGVR = schema.GroupVersionResource{
	Group:    "lenny.dev",
	Version:  "v1alpha1",
	Resource: "sandboxtemplates",
}

// warmPoolStuckWindow is the §25.6 line 2956 dwell threshold: a pool in
// DEMAND_EXCEEDS_SUPPLY with zero in-flight warm-up claims for longer
// than this is a fixable finding.
const warmPoolStuckWindow = 5 * time.Minute

// poolDrainedConditionType is the §5.2 condition the WarmPoolController
// writes onto the SandboxTemplate (not the pool). Its True state (no idle
// and no warming pods) is exactly the §25.6 line 2956 "zero in-flight
// warm-up claims" state, and its lastTransitionTime supplies the durable
// >5m dwell timestamp the pool's own status does not carry.
//
// The dwell is keyed off this dedicated condition rather than the
// PoolWarmingUp condition's Drained reason because meta.SetStatusCondition
// refreshes lastTransitionTime only on a Status change: the common
// Available→Drained path keeps PoolWarmingUp at Status False, so its
// timestamp would be stale (the earlier False transition, often hours old)
// and the >5m dwell gate would fire the instant the pool drains. PoolDrained
// flips False→True on entry into the zero-in-flight state, so its
// lastTransitionTime marks entry and the >5m guard the spec mandates holds.
const poolDrainedConditionType = "PoolDrained"

func (r *k8sDoctorRemediator) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// Detect reports the fixable findings the remediator can resolve. A
// resource that is absent or that the service lacks RBAC to read is not
// a finding (the fix simply does not apply); only an otherwise-failing
// API read aborts the run.
func (r *k8sDoctorRemediator) Detect(ctx context.Context) ([]doctor.Detected, error) {
	var out []doctor.Detected
	if d, ok, err := r.detectCoreDNS(ctx); err != nil {
		return nil, err
	} else if ok {
		out = append(out, d)
	}
	certs, err := r.detectCertExpiring(ctx)
	if err != nil {
		return nil, err
	}
	out = append(out, certs...)
	pools, err := r.detectWarmPoolStuck(ctx)
	if err != nil {
		return nil, err
	}
	out = append(out, pools...)
	if d, ok, err := r.detectBootstrapDrift(ctx); err != nil {
		return nil, err
	} else if ok {
		out = append(out, d)
	}
	if d, ok, err := r.detectPrometheusRuleMissing(ctx); err != nil {
		return nil, err
	} else if ok {
		out = append(out, d)
	}
	return out, nil
}

// Apply runs the idempotent remediation for d.
func (r *k8sDoctorRemediator) Apply(ctx context.Context, d doctor.Detected) error {
	switch d.Code {
	case doctor.FindingCoreDNSStuckEndpoint:
		return r.applyCoreDNS(ctx)
	case doctor.FindingCertManagerExpiring:
		return r.applyCertExpiring(ctx, d.Resource)
	case doctor.FindingWarmPoolStuckReplenish:
		return r.applyWarmPoolStuck(ctx, d.Resource)
	case doctor.FindingBootstrapConfigDrift:
		return r.applyBootstrapDrift(ctx)
	case doctor.FindingPrometheusRuleMissing:
		return r.applyPrometheusRuleMissing(ctx)
	default:
		// A code outside the fixable table reaches here only through the
		// orchestrator's manual path (§25.6 non-fixable findings). The three
		// findings above are never routed here: an undetectable finding
		// (nil Helm source, monitoring disabled, or nil §25.6.1 pool-diagnosis
		// source) reports not_detected rather than this manual recommendation.
		return doctor.ErrManualRemediation
	}
}

// detectCoreDNS reports coreDnsStuckEndpoint when the CoreDNS Service
// Endpoints carry fewer ready addresses than there are Ready CoreDNS
// pods — a Ready pod whose endpoint never propagated (spec line 2962).
func (r *k8sDoctorRemediator) detectCoreDNS(ctx context.Context) (doctor.Detected, bool, error) {
	if r.clientset == nil {
		return doctor.Detected{}, false, nil
	}
	dep, err := r.clientset.AppsV1().Deployments(corednsNamespace).Get(ctx, corednsDeployment, metav1.GetOptions{})
	if err != nil {
		if isAbsent(err) {
			return doctor.Detected{}, false, nil
		}
		return doctor.Detected{}, false, err
	}
	eps, err := r.clientset.CoreV1().Endpoints(corednsNamespace).Get(ctx, kubeDNSService, metav1.GetOptions{})
	if err != nil {
		if isAbsent(err) {
			return doctor.Detected{}, false, nil
		}
		return doctor.Detected{}, false, err
	}
	readyAddrs := 0
	for _, ss := range eps.Subsets {
		readyAddrs += len(ss.Addresses)
	}
	pods, err := r.clientset.CoreV1().Pods(corednsNamespace).List(ctx, metav1.ListOptions{LabelSelector: kubeDNSPodSelector})
	if err != nil {
		if isAbsent(err) {
			return doctor.Detected{}, false, nil
		}
		return doctor.Detected{}, false, err
	}
	readyPods := 0
	for i := range pods.Items {
		if podReady(&pods.Items[i]) {
			readyPods++
		}
	}
	if readyAddrs >= readyPods {
		// Endpoints are in sync with (or ahead of) the Ready pods.
		return doctor.Detected{}, false, nil
	}
	return doctor.Detected{
		Code:     doctor.FindingCoreDNSStuckEndpoint,
		Resource: corednsNamespace + "/" + corednsDeployment,
		OptOut:   dep.Annotations[doctorOptOutAnnotation] == "true",
		Detail:   fmt.Sprintf("%d ready CoreDNS pods but %d endpoint addresses", readyPods, readyAddrs),
	}, true, nil
}

// applyCoreDNS stamps the CoreDNS Deployment pod template with a fresh
// restart annotation, which the Deployment controller rolls out. Re-
// stamping with the same value is a no-op, so the fix is idempotent.
func (r *k8sDoctorRemediator) applyCoreDNS(ctx context.Context) error {
	patch := fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{%q:%q}}}}}`,
		restartedAtAnnotation, r.clock().UTC().Format(time.RFC3339))
	_, err := r.clientset.AppsV1().Deployments(corednsNamespace).Patch(
		ctx, corednsDeployment, types.StrategicMergePatchType, []byte(patch), metav1.PatchOptions{},
	)
	return err
}

// detectCertExpiring reports certManagerExpiring for each cert-manager
// Certificate within the 7-day window that cert-manager still reports
// Ready (spec line 2964 requires cert-manager healthy).
func (r *k8sDoctorRemediator) detectCertExpiring(ctx context.Context) ([]doctor.Detected, error) {
	if r.dyn == nil {
		return nil, nil
	}
	list, err := r.dyn.Resource(certManagerGVR).Namespace(r.releaseNS).List(ctx, metav1.ListOptions{})
	if err != nil {
		if isAbsent(err) {
			return nil, nil
		}
		return nil, err
	}
	now := r.clock()
	var out []doctor.Detected
	for i := range list.Items {
		u := &list.Items[i]
		cs := certStatusFromUnstructured(u)
		// RenewalFailed is set when the Ready condition is not True, so
		// !RenewalFailed is the "cert-manager healthy" gate the spec names.
		if cs.NotAfter.IsZero() || cs.RenewalFailed {
			continue
		}
		if cs.NotAfter.Sub(now) > certExpiryWindow {
			continue
		}
		out = append(out, doctor.Detected{
			Code:     doctor.FindingCertManagerExpiring,
			Resource: u.GetNamespace() + "/" + u.GetName(),
			OptOut:   u.GetAnnotations()[doctorOptOutAnnotation] == "true",
			Detail:   fmt.Sprintf("certificate expires %s", cs.NotAfter.UTC().Format(time.RFC3339)),
		})
	}
	return out, nil
}

// applyCertExpiring forces re-issuance: it annotates the Certificate to
// request a temporary certificate and deletes the backing Secret so
// cert-manager re-issues (spec line 2964). Both steps tolerate being
// repeated, so the fix is idempotent.
func (r *k8sDoctorRemediator) applyCertExpiring(ctx context.Context, resource string) error {
	ns, name, ok := splitNSName(resource)
	if !ok {
		return fmt.Errorf("malformed certificate resource %q", resource)
	}
	cert, err := r.dyn.Resource(certManagerGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	secretName, _, _ := unstructured.NestedString(cert.Object, "spec", "secretName")
	annPatch := fmt.Sprintf(`{"metadata":{"annotations":{%q:"true"}}}`, issueTempCertAnnotation)
	if _, err := r.dyn.Resource(certManagerGVR).Namespace(ns).Patch(
		ctx, name, types.MergePatchType, []byte(annPatch), metav1.PatchOptions{},
	); err != nil {
		return err
	}
	if secretName != "" {
		if err := r.clientset.CoreV1().Secrets(ns).Delete(ctx, secretName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// detectWarmPoolStuck reports warmPoolStuckReplenish for each SandboxWarmPool
// that is stalled per §25.6 line 2956: the §25.6.1 pool diagnosis classifies
// its bottleneck as DEMAND_EXCEEDS_SUPPLY (claim rate outpaces replenishment
// rate) with zero in-flight warm-up claims (no warming and no claimed pods),
// and the pool has dwelt in that no-progress state for longer than the 5m
// window. The DEMAND_EXCEEDS_SUPPLY classification and the pod-state
// breakdown come from the injected §25.6.1 DataSource; the durable >5m dwell
// comes from the PoolWarmingUp/Drained condition the WarmPoolController
// writes onto the referenced SandboxTemplate (the pool's own status never
// carries it). A nil pool-diagnosis source leaves the finding undetected.
func (r *k8sDoctorRemediator) detectWarmPoolStuck(ctx context.Context) ([]doctor.Detected, error) {
	if r.dyn == nil || r.poolDx == nil {
		return nil, nil
	}
	list, err := r.dyn.Resource(sandboxWarmPoolGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		if isAbsent(err) {
			return nil, nil
		}
		return nil, err
	}
	now := r.clock()
	var out []doctor.Detected
	for i := range list.Items {
		u := &list.Items[i]
		stuck, err := r.warmPoolStuck(ctx, u, now)
		if err != nil {
			return nil, err
		}
		if !stuck {
			continue
		}
		out = append(out, doctor.Detected{
			Code:     doctor.FindingWarmPoolStuckReplenish,
			Resource: u.GetNamespace() + "/" + u.GetName(),
			OptOut:   u.GetAnnotations()[doctorOptOutAnnotation] == "true",
			Detail:   "warm-pool replenishment stalled: demand exceeds supply with zero in-flight warm-up claims",
		})
	}
	return out, nil
}

// warmPoolStuck reports whether a SandboxWarmPool is in the §25.6 line
// 2956 stuck-replenish state. It requires all three conjuncts the spec
// names: the §25.6.1 bottleneck classification is DEMAND_EXCEEDS_SUPPLY;
// the pod-state breakdown shows zero in-flight warm-up claims (no warming
// and no claimed pods); and the referenced SandboxTemplate's
// PoolWarmingUp/Drained condition (the zero-in-flight state) has dwelt
// past warmPoolStuckWindow. A pool that is actively warming pods (a
// non-zero warming or claimed count, or a Provisioning condition) is not
// stuck: it is making progress, which is why the spec keys detection on
// the zero-in-flight state rather than on a below-floor warm count.
func (r *k8sDoctorRemediator) warmPoolStuck(ctx context.Context, u *unstructured.Unstructured, now time.Time) (bool, error) {
	diag, err := r.poolDx.DiagnosePool(ctx, u.GetName())
	if err != nil {
		// A pool the diagnosis source cannot classify (not registered, or a
		// degraded read that could not compute the demand bottleneck) is not
		// a finding: fail closed on ambiguity rather than re-kick a pool that
		// may be healthy.
		return false, nil
	}
	if diag.Bottleneck == nil || diag.Bottleneck.Category != diagnostics.BottleneckDemandExceedsSupply {
		return false, nil
	}
	// spec: §25.6 line 2956 — "zero in-flight warm-up claims". A pool with
	// any warming or claimed pod is making progress, so it is not stuck.
	if diag.PodCounts.Warming > 0 || diag.PodCounts.Claimed > 0 {
		return false, nil
	}
	// The >5m dwell is read from the SandboxTemplate's PoolWarmingUp/Drained
	// condition, the durable timestamp for the zero-in-flight state.
	dwelt, err := r.warmPoolDrainedPastWindow(ctx, u, now)
	if err != nil {
		return false, err
	}
	return dwelt, nil
}

// warmPoolDrainedPastWindow reports whether the SandboxTemplate the pool
// references carries a PoolDrained condition in its True state (the §25.6
// zero-in-flight state) whose lastTransitionTime is older than
// warmPoolStuckWindow. It reads the condition from the template rather than
// the pool because the WarmPoolController writes PoolDrained onto the
// SandboxTemplate status, never onto the pool's own status. Because the
// condition's Status flips False→True on entry into the drained state,
// lastTransitionTime marks entry and the >5m dwell gate holds even for the
// common Available→Drained path (unlike the PoolWarmingUp condition, whose
// Available and Drained states share a False status). A pool with no
// templateRef, an unreadable template, or no PoolDrained=True condition is
// treated as not-yet-dwelt (no finding), failing closed.
func (r *k8sDoctorRemediator) warmPoolDrainedPastWindow(ctx context.Context, pool *unstructured.Unstructured, now time.Time) (bool, error) {
	templateRef, _, _ := unstructured.NestedString(pool.Object, "spec", "templateRef")
	if templateRef == "" {
		return false, nil
	}
	tmpl, err := r.dyn.Resource(sandboxTemplateGVR).Namespace(pool.GetNamespace()).Get(ctx, templateRef, metav1.GetOptions{})
	if err != nil {
		if isAbsent(err) {
			return false, nil
		}
		return false, err
	}
	conds, ok, _ := unstructured.NestedSlice(tmpl.Object, "status", "conditions")
	if !ok {
		return false, nil
	}
	for _, c := range conds {
		cm, ok := c.(map[string]any)
		if !ok || cm["type"] != poolDrainedConditionType {
			continue
		}
		// Only a True PoolDrained is the zero-in-flight state; a False
		// condition (idle pods present or pods still warming) is making
		// progress or already healthy, so it is not the stuck signal.
		if cm["status"] != string(metav1.ConditionTrue) {
			return false, nil
		}
		ltt, _ := cm["lastTransitionTime"].(string)
		t, err := time.Parse(time.RFC3339, ltt)
		if err != nil {
			return false, nil
		}
		return now.Sub(t) > warmPoolStuckWindow, nil
	}
	return false, nil
}

// applyWarmPoolStuck re-drives the stalled SandboxWarmPool (spec line 2956:
// "Bumps pool generation (triggers controller to re-drive)"). It stamps a
// re-drive annotation carrying the current timestamp onto .metadata, which
// the apiserver honors: an annotation write is a spec-adjacent metadata
// change that advances .metadata.resourceVersion and (as a spec-file change)
// bumps .metadata.generation server-side, and it emits a watch Update event.
// The WarmPoolController is registered For(SandboxWarmPool) (controller.go
// SetupWithManager) and enqueues on that event, so the stalled pool is
// re-driven. A direct .metadata.generation write would be a no-op against a
// real apiserver — generation is server-managed and recomputed on update — so
// it emits no watch event and never re-drives the controller. Re-stamping a
// later timestamp on a subsequent pass is a fresh annotation value, so the
// fix converges without depending on the exact stored value.
func (r *k8sDoctorRemediator) applyWarmPoolStuck(ctx context.Context, resource string) error {
	ns, name, ok := splitNSName(resource)
	if !ok {
		return fmt.Errorf("malformed warm-pool resource %q", resource)
	}
	client := r.dyn.Resource(sandboxWarmPoolGVR).Namespace(ns)
	patch := fmt.Sprintf(`{"metadata":{"annotations":{%q:%q}}}`,
		doctorRedriveAnnotation, r.clock().UTC().Format(time.RFC3339Nano))
	if _, err := client.Patch(ctx, name, types.MergePatchType, []byte(patch), metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("re-drive warm pool %s/%s: %w", ns, name, err)
	}
	return nil
}

// detectBootstrapDrift reports bootstrapConfigDrift when the live
// lenny-bootstrap ConfigMap's content diverges from the Helm-rendered
// value (spec line 2953). The rendered reference comes from the injected
// HelmRenderSource; a nil source or an operator who supplied no bootstrap
// render leaves the finding undetected (reported not_detected), never a
// false success.
func (r *k8sDoctorRemediator) detectBootstrapDrift(ctx context.Context) (doctor.Detected, bool, error) {
	if r.helm == nil {
		return doctor.Detected{}, false, nil
	}
	rendered, ok, err := r.helm.BootstrapConfigMap(ctx)
	if err != nil {
		return doctor.Detected{}, false, err
	}
	if !ok {
		return doctor.Detected{}, false, nil
	}
	live, err := r.clientset.CoreV1().ConfigMaps(r.releaseNS).Get(ctx, rendered.Name, metav1.GetOptions{})
	if err != nil {
		if isAbsent(err) {
			// An absent ConfigMap is itself drift: the fix re-applies it.
			return doctor.Detected{
				Code:     doctor.FindingBootstrapConfigDrift,
				Resource: r.releaseNS + "/" + rendered.Name,
				Detail:   "lenny-bootstrap ConfigMap is absent",
			}, true, nil
		}
		return doctor.Detected{}, false, err
	}
	if hashConfigMapData(live.Data) == renderedHash(rendered) {
		// Live content matches the rendered value: no drift.
		return doctor.Detected{}, false, nil
	}
	return doctor.Detected{
		Code:     doctor.FindingBootstrapConfigDrift,
		Resource: r.releaseNS + "/" + rendered.Name,
		OptOut:   live.Annotations[doctorOptOutAnnotation] == "true",
		Detail:   "lenny-bootstrap ConfigMap content diverges from the Helm-rendered value",
	}, true, nil
}

// applyBootstrapDrift re-applies the Helm-rendered lenny-bootstrap
// ConfigMap (spec line 2953). It does not restart the gateway: the
// bootstrap reload is watch-driven. Re-applying identical content is a
// no-op update, so the fix is idempotent.
func (r *k8sDoctorRemediator) applyBootstrapDrift(ctx context.Context) error {
	if r.helm == nil {
		return doctor.ErrManualRemediation
	}
	rendered, ok, err := r.helm.BootstrapConfigMap(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return doctor.ErrManualRemediation
	}
	cms := r.clientset.CoreV1().ConfigMaps(r.releaseNS)
	live, err := cms.Get(ctx, rendered.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = cms.Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: r.releaseNS, Name: rendered.Name},
			Data:       rendered.Data,
		}, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	live.Data = rendered.Data
	_, err = cms.Update(ctx, live, metav1.UpdateOptions{})
	return err
}

// detectPrometheusRuleMissing reports prometheusRuleMissing when
// monitoring is enabled in the release but no PrometheusRule /
// ServiceMonitor is present in the release namespace (spec line 2955).
// The rendered bundle comes from the injected HelmRenderSource; a nil
// source or monitoring-disabled release leaves the finding undetected.
func (r *k8sDoctorRemediator) detectPrometheusRuleMissing(ctx context.Context) (doctor.Detected, bool, error) {
	if r.helm == nil || r.dyn == nil {
		return doctor.Detected{}, false, nil
	}
	m, ok, err := r.helm.Monitoring(ctx)
	if err != nil {
		return doctor.Detected{}, false, err
	}
	if !ok || len(m.Objects) == 0 {
		return doctor.Detected{}, false, nil
	}
	for i := range m.Objects {
		present, err := r.monitoringObjectPresent(ctx, m.Objects[i])
		if err != nil {
			return doctor.Detected{}, false, err
		}
		if !present {
			return doctor.Detected{
				Code:     doctor.FindingPrometheusRuleMissing,
				Resource: r.releaseNS,
				Detail:   "monitoring is enabled but a PrometheusRule/ServiceMonitor is absent",
			}, true, nil
		}
	}
	return doctor.Detected{}, false, nil
}

// monitoringObjectPresent reports whether the rendered monitoring object
// exists in the cluster. A NotFound (object or its CRD absent) means it
// must be re-applied; any other API error propagates.
func (r *k8sDoctorRemediator) monitoringObjectPresent(ctx context.Context, o doctor.RenderedObject) (bool, error) {
	gvr := schema.GroupVersionResource{Group: o.Group, Version: o.Version, Resource: o.Resource}
	_, err := r.dyn.Resource(gvr).Namespace(o.Namespace).Get(ctx, o.Name, metav1.GetOptions{})
	if err == nil {
		return true, nil
	}
	if isAbsent(err) {
		return false, nil
	}
	return false, err
}

// applyPrometheusRuleMissing re-applies the Helm-rendered
// PrometheusRule / ServiceMonitor bundle (spec line 2955) through a
// server-side apply, creating each object that is absent and leaving a
// present object untouched. Applying an identical manifest is a no-op, so
// the fix is idempotent.
func (r *k8sDoctorRemediator) applyPrometheusRuleMissing(ctx context.Context) error {
	if r.helm == nil {
		return doctor.ErrManualRemediation
	}
	m, ok, err := r.helm.Monitoring(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return doctor.ErrManualRemediation
	}
	for i := range m.Objects {
		if err := r.applyMonitoringObject(ctx, m.Objects[i]); err != nil {
			return err
		}
	}
	return nil
}

// applyMonitoringObject creates the rendered object when it is absent and
// leaves it in place when it is already present, so the fix converges
// without overwriting an operator-customised object.
func (r *k8sDoctorRemediator) applyMonitoringObject(ctx context.Context, o doctor.RenderedObject) error {
	present, err := r.monitoringObjectPresent(ctx, o)
	if err != nil {
		return err
	}
	if present {
		return nil
	}
	gvr := schema.GroupVersionResource{Group: o.Group, Version: o.Version, Resource: o.Resource}
	obj := &unstructured.Unstructured{Object: o.Manifest}
	_, err = r.dyn.Resource(gvr).Namespace(o.Namespace).Create(ctx, obj, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

// hashConfigMapData produces a stable content hash of a ConfigMap's data
// map, so bootstrapConfigDrift detection compares live content against
// the rendered value deterministically regardless of key order.
func hashConfigMapData(data map[string]string) string {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write([]byte(data[k]))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// renderedHash returns the rendered ConfigMap's content hash, computing
// it from Data when the source did not supply one so the comparison never
// silently matches on an empty Hash.
func renderedHash(cm doctor.RenderedConfigMap) string {
	if cm.Hash != "" {
		return cm.Hash
	}
	return hashConfigMapData(cm.Data)
}

// podReady reports whether p carries a True PodReady condition.
func podReady(p *corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// isAbsent reports whether err means the resource is not present or not
// readable by this service (CRD not installed, object absent, or RBAC
// denied) — none of which is a fixable finding, so detection skips it.
func isAbsent(err error) bool {
	return apierrors.IsNotFound(err) || apierrors.IsForbidden(err)
}

// splitNSName splits a "namespace/name" resource id.
func splitNSName(resource string) (ns, name string, ok bool) {
	i := strings.IndexByte(resource, '/')
	if i <= 0 || i == len(resource)-1 {
		return "", "", false
	}
	return resource[:i], resource[i+1:], true
}
