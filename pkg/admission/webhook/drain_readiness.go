// SPDX-License-Identifier: MIT

package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

// DrainReadinessMetricsSink emits the §12.5 ll. 291
// `lenny_drain_readiness_checks_total{outcome=...}` counter once per
// admission decision the webhook makes. outcome is `allowed`,
// `blocked`, or `forced`. The webhook leaves the sink nil when the
// metric is not wired (the lenny-webhook binary is configured at
// startup; the field is set to a no-op sink in the bootstrap).
//
// spec: §12.5 line 291.
type DrainReadinessMetricsSink interface {
	IncDrainReadinessCheck(outcome string)
}

// DrainReadinessAuditSink emits the §12.5 ll. 291 / §16.7
// `node.drain.forced` critical audit event when a drain-force
// override admits an eviction the MinIO health check would otherwise
// have blocked. The sink is invoked synchronously so the webhook
// response stalls until the audit row commits — fail-closed audit
// semantics per §11.7. A nil sink falls back to a log line.
//
// spec: §12.5 line 291; §16.7 node.drain.forced.
type DrainReadinessAuditSink interface {
	RecordForcedDrain(ctx context.Context, evt DrainForcedEvent) error
}

// DrainForcedEvent is the §16.7 audit payload for a drain-force
// override admission. It carries the operator-resolvable tuple the
// runbook needs to attribute the override.
//
// spec: §12.5 line 291; §16.7 node.drain.forced.
type DrainForcedEvent struct {
	// PodNamespace is the namespace of the evicted pod.
	PodNamespace string
	// PodName is the name of the evicted pod.
	PodName string
	// NodeName is the node that carries the
	// lenny.dev/drain-force: "true" override.
	NodeName string
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
// drain-readiness endpoint, and applies drain_readiness.Decide. Every
// decision bumps the §12.5 line 291 counter through metrics (when
// non-nil). On a forced admission audit emits the §16.7
// `node.drain.forced` event (when non-nil); a synchronous audit
// failure flips the decision to a deny so the override never escapes
// the trail.
func DrainReadiness(reader client.Reader, probe DrainProbe, metrics DrainReadinessMetricsSink, audit DrainReadinessAuditSink) Decider {
	return func(ctx context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
		nodeName, forced := resolveDrainContext(ctx, reader, req.Namespace, req.Name)
		decision := dr.Decide(dr.Request{
			DrainForced: forced,
			MinIO:       probe.Probe(ctx),
		})
		outcome := decisionOutcome(decision)
		if metrics != nil {
			metrics.IncDrainReadinessCheck(outcome)
		}
		if decision.Forced && audit != nil {
			if err := audit.RecordForcedDrain(ctx, DrainForcedEvent{
				PodNamespace: req.Namespace,
				PodName:      req.Name,
				NodeName:     nodeName,
			}); err != nil {
				// §11.7 fail-closed audit: if the durable audit write
				// fails, do not let the forced eviction proceed.
				if metrics != nil {
					metrics.IncDrainReadinessCheck("audit_failed")
				}
				return Deny(503, "Node drain blocked: §16.7 node.drain.forced audit write failed — defer drain until the audit chain is reachable")
			}
		}
		if decision.Allowed {
			return Allow()
		}
		return Deny(int32(decision.Code), decision.Reason)
	}
}

// decisionOutcome maps a Decision onto the §12.5 line 291 outcome
// label vocabulary (allowed | blocked | forced).
func decisionOutcome(d dr.Decision) string {
	switch {
	case d.Forced:
		return "forced"
	case d.Allowed:
		return "allowed"
	default:
		return "blocked"
	}
}

// HTTPForcedDrainAuditSink posts the §16.7 node.drain.forced audit
// event to the gateway's POST /internal/audit/node-drain-forced
// endpoint. The webhook uses this sink to commit the drain-force
// override into the per-tenant §11.7 hash chain.
//
// spec: §12.5 line 291; §16.7 node.drain.forced.
type HTTPForcedDrainAuditSink struct {
	// URL is the gateway POST /internal/audit/node-drain-forced
	// endpoint.
	URL string
	// Client is the HTTP client; nil selects a client bounded by
	// drainProbeTimeout.
	Client *http.Client
	// Tenant scopes the §11.7 chain the event lands on; empty selects
	// the gateway's platform tenant.
	Tenant string
}

// RecordForcedDrain posts the §16.7 event payload. A non-2xx
// response or transport failure surfaces as an error so the webhook
// can deny the eviction fail-closed.
func (s HTTPForcedDrainAuditSink) RecordForcedDrain(ctx context.Context, evt DrainForcedEvent) error {
	if s.URL == "" {
		// No endpoint configured: degrade to nil so the caller can
		// fall back to a log line without crashing the webhook.
		return nil
	}
	body, err := json.Marshal(map[string]any{
		"tenant":       s.Tenant,
		"podNamespace": evt.PodNamespace,
		"podName":      evt.PodName,
		"nodeName":     evt.NodeName,
	})
	if err != nil {
		return fmt.Errorf("forced-drain audit: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("forced-drain audit: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c := s.Client
	if c == nil {
		c = &http.Client{Timeout: drainProbeTimeout}
	}
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("forced-drain audit: do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("forced-drain audit: gateway returned %d", resp.StatusCode)
}

// resolveDrainContext fetches the node hosting the evicted pod and
// reports whether it carries the §12.5 drain-force override
// annotation. The node name is returned alongside so the §16.7
// `node.drain.forced` audit payload carries the operator-resolvable
// identity. Any resolution failure reports ("", false): the MinIO
// health probe is the primary control and the override is a
// best-effort operator convenience.
func resolveDrainContext(ctx context.Context, reader client.Reader, podNamespace, podName string) (string, bool) {
	if reader == nil || podName == "" {
		return "", false
	}
	var pod corev1.Pod
	if err := reader.Get(ctx, client.ObjectKey{Namespace: podNamespace, Name: podName}, &pod); err != nil {
		return "", false
	}
	if pod.Spec.NodeName == "" {
		return "", false
	}
	var node corev1.Node
	if err := reader.Get(ctx, client.ObjectKey{Name: pod.Spec.NodeName}, &node); err != nil {
		return pod.Spec.NodeName, false
	}
	return pod.Spec.NodeName, node.Annotations[drainForceAnnotation] == "true"
}
