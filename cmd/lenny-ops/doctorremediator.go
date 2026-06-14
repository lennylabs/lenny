// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/lennylabs/lenny/pkg/ops/doctor"
)

// The §25.6 doctor remediation coordinates. CoreDNS ships as the
// `coredns` Deployment fronted by the `kube-dns` Service in kube-system
// on every conformant cluster; the §25.6 line 2974 opt-out annotation
// and the standard rollout-restart annotation are well-known strings.
const (
	doctorOptOutAnnotation  = "lenny.dev/doctor-optout"
	corednsNamespace        = "kube-system"
	corednsDeployment       = "coredns"
	kubeDNSService          = "kube-dns"
	kubeDNSPodSelector      = "k8s-app=kube-dns"
	restartedAtAnnotation   = "kubectl.kubernetes.io/restartedAt"
	issueTempCertAnnotation = "cert-manager.io/issue-temporary-certificate"
	certExpiryWindow        = 7 * 24 * time.Hour
)

// k8sDoctorRemediator is the production §25.6 Remediator. It detects the
// fixable findings whose cluster coordinates lenny-ops can resolve
// without the Helm-rendered chart source and applies their idempotent
// remediations over the client-go typed and dynamic clients.
//
// Live coverage: coreDnsStuckEndpoint (rolling restart of the CoreDNS
// Deployment) and certManagerExpiring (force re-issuance). The remaining
// §25.6 fixable findings — bootstrapConfigDrift and prometheusRuleMissing
// (re-apply the Helm-rendered template) and warmPoolStuckReplenish
// (cluster-wide pool enumeration) — require sources this service does not
// hold; Apply returns ErrManualRemediation for them so the orchestrator
// reports a manual recommendation rather than a false success.
//
// spec: §25.6 lines 2961-2974. F-25.6.2.
type k8sDoctorRemediator struct {
	clientset kubernetes.Interface
	dyn       dynamic.Interface
	// releaseNS is the namespace holding the cert-manager Certificate CRs
	// the certManagerExpiring fix acts on.
	releaseNS string
	now       func() time.Time
}

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
	return out, nil
}

// Apply runs the idempotent remediation for d.
func (r *k8sDoctorRemediator) Apply(ctx context.Context, d doctor.Detected) error {
	switch d.Code {
	case doctor.FindingCoreDNSStuckEndpoint:
		return r.applyCoreDNS(ctx)
	case doctor.FindingCertManagerExpiring:
		return r.applyCertExpiring(ctx, d.Resource)
	default:
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
