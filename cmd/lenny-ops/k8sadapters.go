// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"io"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/lennylabs/lenny/pkg/ops/opsserver"
	"github.com/lennylabs/lenny/pkg/ops/opsservice"
)

// k8sPodLogReader backs the §25.4 log-proxy endpoint with the Kubernetes
// pod-log API. It satisfies opsserver.PodLogReader.
//
// spec: §25.4 lines 2528-2534.
type k8sPodLogReader struct {
	pods corev1client.PodsGetter
}

// ReadPodLogs streams the named pod's container logs. A not-found pod is
// translated to opsserver.ErrPodLogNotFound so the handler returns the
// §25.2 404 POD_NOT_FOUND envelope.
func (r k8sPodLogReader) ReadPodLogs(ctx context.Context, namespace, name string, opts opsserver.PodLogOptions) (io.ReadCloser, error) {
	req := r.pods.Pods(namespace).GetLogs(name, &corev1.PodLogOptions{
		Container:    opts.Container,
		Previous:     opts.Previous,
		SinceSeconds: opts.SinceSeconds,
		TailLines:    opts.TailLines,
	})
	stream, err := req.Stream(ctx)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("%w: %v", opsserver.ErrPodLogNotFound, err)
		}
		return nil, err
	}
	return stream, nil
}

// certManagerGVR is the cert-manager Certificate resource the §25.8
// health probe lists. cert-manager is not a Lenny dependency, so the
// resource is read dynamically rather than via a typed client.
var certManagerGVR = schema.GroupVersionResource{
	Group:    "cert-manager.io",
	Version:  "v1",
	Resource: "certificates",
}

// certManagerSource backs the §25.8 cert_manager self-health check with
// the cert-manager Certificate CRs in the lenny-system namespace. It
// satisfies opsservice.CertStatusSource.
//
// spec: §25.8 lines 3456-3461.
type certManagerSource struct {
	client    dynamic.Interface
	namespace string
	// onExpiry records each certificate's remaining lifetime on the
	// lenny_cert_expiry_seconds gauge the CertExpiryImminent alert reads.
	// A nil hook skips metric emission.
	onExpiry func(certificate string, secondsRemaining float64)
}

// CertStatuses lists the cert-manager Certificate resources and maps each
// status into the §25.8 CertStatus the check classifies. A list error
// (cert-manager CRD absent, RBAC denied, API unreachable) propagates so
// the check reports the probe could not reach cert-manager. When the CRD
// is not installed the dynamic client returns a NotFound error, which the
// caller surfaces as unhealthy — but the probe is only wired when
// cert-manager use is expected.
func (s certManagerSource) CertStatuses(ctx context.Context) ([]opsservice.CertStatus, error) {
	list, err := s.client.Resource(certManagerGVR).Namespace(s.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]opsservice.CertStatus, 0, len(list.Items))
	now := time.Now()
	for i := range list.Items {
		cs := certStatusFromUnstructured(&list.Items[i])
		out = append(out, cs)
		if s.onExpiry != nil && !cs.NotAfter.IsZero() {
			s.onExpiry(cs.Name, cs.NotAfter.Sub(now).Seconds())
		}
	}
	return out, nil
}

// certStatusFromUnstructured reads status.notAfter and the Ready
// condition from a cert-manager Certificate. RenewalFailed is true when
// the Ready condition is present and not "True" (cert-manager has not
// produced a currently-valid certificate).
func certStatusFromUnstructured(u *unstructured.Unstructured) opsservice.CertStatus {
	cs := opsservice.CertStatus{Name: u.GetNamespace() + "/" + u.GetName()}
	if notAfter, ok, _ := unstructured.NestedString(u.Object, "status", "notAfter"); ok {
		if t, err := time.Parse(time.RFC3339, notAfter); err == nil {
			cs.NotAfter = t
		}
	}
	conds, ok, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	if !ok {
		// No conditions reported yet: treat an unissued certificate as
		// renewal-pending so the check can flag a stuck issuance.
		cs.RenewalFailed = cs.NotAfter.IsZero()
		return cs
	}
	ready := false
	for _, c := range conds {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if cm["type"] == "Ready" {
			ready = cm["status"] == "True"
		}
	}
	cs.RenewalFailed = !ready
	return cs
}
