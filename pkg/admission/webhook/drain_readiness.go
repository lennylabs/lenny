// SPDX-License-Identifier: MIT

package webhook

import (
	"context"
	"net/http"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dr "github.com/lennylabs/lenny/pkg/admission/drain_readiness"
)

// drainForceAnnotation is the §12.5 node annotation that overrides the
// pre-drain MinIO health check for an emergency drain.
const drainForceAnnotation = "lenny.dev/drain-force"

// drainProbeTimeout bounds the webhook's call to the gateway
// drain-readiness endpoint when HTTPDrainProbe has no client configured.
const drainProbeTimeout = 5 * time.Second

// DrainProbe queries the gateway's §12.5 GET /internal/drain-readiness
// endpoint and reports the artifact-store drain readiness.
type DrainProbe interface {
	Probe(ctx context.Context) dr.MinIOStatus
}

// HTTPDrainProbe queries the gateway drain-readiness endpoint over HTTP.
// A non-2xx, non-503 response or a transport failure reports the
// artifact store unreachable, which the §12.5 decision treats as a
// blocked drain.
type HTTPDrainProbe struct {
	// URL is the gateway GET /internal/drain-readiness endpoint.
	URL string
	// Client is the HTTP client; nil selects a client bounded by
	// drainProbeTimeout.
	Client *http.Client
}

// Probe calls the gateway endpoint and maps its status to a MinIOStatus.
func (p HTTPDrainProbe) Probe(ctx context.Context) dr.MinIOStatus {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL, nil)
	if err != nil {
		return dr.MinIOUnreachable
	}
	c := p.Client
	if c == nil {
		c = &http.Client{Timeout: drainProbeTimeout}
	}
	resp, err := c.Do(req)
	if err != nil {
		return dr.MinIOUnreachable
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return dr.MinIOHealthy
	case http.StatusServiceUnavailable:
		return dr.MinIOUnhealthy
	default:
		return dr.MinIOUnreachable
	}
}

// DrainReadiness returns the Decider for the lenny-drain-readiness
// ValidatingAdmissionWebhook (§12.5). It is installed on the
// pods/eviction subresource in agent namespaces and blocks a planned
// node drain while the artifact store is degraded, so a drain cannot
// evict agent pods into a MinIO outage and lose un-checkpointed
// workspace state.
//
// For each eviction the decider resolves the evicted pod's node and
// reads the §12.5 drain-force override, queries the gateway
// drain-readiness endpoint, and applies drain_readiness.Decide.
// onForced, when non-nil, is invoked with the evicted pod's namespace
// and name whenever a drain-force override admits an eviction the MinIO
// health check would otherwise have blocked, so the caller can record
// the §12.5 node.drain.forced audit event.
func DrainReadiness(reader client.Reader, probe DrainProbe, onForced func(podNamespace, podName string)) Decider {
	return func(ctx context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
		decision := dr.Decide(dr.Request{
			DrainForced: nodeDrainForced(ctx, reader, req.Namespace, req.Name),
			MinIO:       probe.Probe(ctx),
		})
		if decision.Forced && onForced != nil {
			onForced(req.Namespace, req.Name)
		}
		if decision.Allowed {
			return Allow()
		}
		return Deny(int32(decision.Code), decision.Reason)
	}
}

// nodeDrainForced reports whether the node hosting the evicted pod
// carries the §12.5 drain-force override annotation. Any resolution
// failure reports false: the MinIO health probe is the primary control
// and the override is a best-effort operator convenience.
func nodeDrainForced(ctx context.Context, reader client.Reader, podNamespace, podName string) bool {
	if reader == nil || podName == "" {
		return false
	}
	var pod corev1.Pod
	if err := reader.Get(ctx, client.ObjectKey{Namespace: podNamespace, Name: podName}, &pod); err != nil {
		return false
	}
	if pod.Spec.NodeName == "" {
		return false
	}
	var node corev1.Node
	if err := reader.Get(ctx, client.ObjectKey{Name: pod.Spec.NodeName}, &node); err != nil {
		return false
	}
	return node.Annotations[drainForceAnnotation] == "true"
}
