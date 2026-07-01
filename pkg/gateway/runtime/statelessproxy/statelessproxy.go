// SPDX-License-Identifier: MIT

// Package statelessproxy is the §5.2 concurrent-stateless data-plane
// half: the gateway HTTP reverse proxy that routes a stateless request
// to the tenant-pinned pod IP the tenantaffinity.Router selects,
// bypassing the pool's Kubernetes Service load balancer. The Router
// decides which pod IP a tenant pins to; this package dials it,
// stamps the lenny.dev/tenant-id pin label on a newly-pinned pod, and
// releases the router's in-flight count when the request completes.
//
// The gateway's role in stateless mode is limited to load-balanced
// routing (§5.2 "Concurrent-stateless limitations (v1)"): it does not
// track individual task outcomes, materialize a workspace, or apply a
// slot retry policy. The request body and response are opaque — the
// proxy forwards them verbatim to the runtime's own HTTP surface.
//
// spec: §5.2 line 500 (tenant-affinity Service-bypass routing), §5.2
// line 519 (WARM_POOL_EXHAUSTED on exhaustion), §5.2 line 502 (tenant
// pinning).
package statelessproxy

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/errorclassify"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/tenantaffinity"
)

// DefaultPodPort is the port the proxy dials on a stateless pod when
// PodPort is unset. The runtime serves its stateless HTTP surface on
// the pod's agent container port; 8080 is the platform default agent
// container port.
const DefaultPodPort = 8080

// PodLabeler applies the §5.2 line 500 lenny.dev/tenant-id pin label to
// the pod at podIP when the router newly pins it to a tenant. The
// production implementation resolves podIP→pod via a status.podIP field
// selector and JSON-merge-patches the label; tests inject a fake. A nil
// labeler disables the Kubernetes-layer pin (the in-memory router pin
// still enforces affinity; the lenny-tenant-label-immutability webhook
// backstop is simply not engaged).
type PodLabeler interface {
	LabelTenant(ctx context.Context, podIP, tenantID string) error
}

// TenantResolver extracts the requesting tenant id from an inbound
// stateless request. The production wiring reads the tenant the auth
// middleware stamped on the request context.
type TenantResolver func(*http.Request) (string, error)

// Proxy reverse-proxies one stateless pool's requests to the
// tenant-pinned pod the router selects.
type Proxy struct {
	// Router is the per-pool tenant-affinity decision layer. Required.
	Router *tenantaffinity.Router
	// Tenant resolves the requesting tenant id. Required.
	Tenant TenantResolver
	// Labeler stamps the tenant pin label on a newly-pinned pod.
	// Optional (nil disables the Kubernetes-layer pin).
	Labeler PodLabeler
	// PodPort is the runtime's stateless serving port. Zero selects
	// DefaultPodPort.
	PodPort int
	// Transport is the round tripper the reverse proxy dials pod IPs
	// with. Zero selects http.DefaultTransport.
	Transport http.RoundTripper
	// Logf, when set, receives a one-line diagnostic on a label or dial
	// failure.
	Logf func(format string, args ...any)
}

// ServeHTTP routes the request to a tenant-pinned pod and reverse-
// proxies it. On a routing failure (no available pod, or — defensively
// — a tenant mismatch) it writes the §5.2 line 519 WARM_POOL_EXHAUSTED
// envelope; on a pod dial failure it writes BAD_GATEWAY.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p.Router == nil || p.Tenant == nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"stateless proxy is not configured", nil)
		return
	}

	tenantID, err := p.Tenant(r)
	if err != nil || tenantID == "" {
		writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED",
			"stateless routing requires an authenticated tenant", nil)
		return
	}

	decision, err := p.Router.Route(tenantID)
	if err != nil {
		// Route already counted the arrival as demand
		// (lenny_service_requests_total) before failing, so the
		// PoolScalingController scales up under this unmet load.
		reason := "no_idle_pods"
		status := http.StatusServiceUnavailable
		if errors.Is(err, tenantaffinity.ErrTenantMismatch) {
			reason = "concurrent_slots_exhausted"
		}
		w.Header().Set("Retry-After", strconv.Itoa(statelessRetryAfterSeconds))
		writeError(w, status, "WARM_POOL_EXHAUSTED",
			"no stateless pod is available to route this tenant's request",
			map[string]any{"reason": reason})
		return
	}
	// Route incremented the in-flight count; release it exactly once when
	// this request finishes (success or proxy error). ServeHTTP blocks
	// until the reverse proxy drains the response, so the deferred
	// Release fires after the request truly completes.
	defer p.Router.Release()

	if decision.NewlyPinned && p.Labeler != nil {
		// Best-effort: the in-memory router pin is the active affinity
		// guard; the label is the lenny-tenant-label-immutability webhook
		// backstop. A label failure does not abandon the request, but it
		// is logged so an operator notices the missing Kubernetes-layer
		// pin.
		if err := p.Labeler.LabelTenant(r.Context(), decision.PodIP, tenantID); err != nil && p.Logf != nil {
			p.Logf("statelessproxy: tenant-pin label failed for pod %s (tenant %s): %v", decision.PodIP, tenantID, err)
		}
	}

	port := p.PodPort
	if port <= 0 {
		port = DefaultPodPort
	}
	target := &url.URL{Scheme: "http", Host: net.JoinHostPort(decision.PodIP, strconv.Itoa(port))}

	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = target.Host
			// Forward the resolved pin so the runtime and any in-pod
			// observability can attribute the request to its tenant.
			pr.Out.Header.Set("X-Lenny-Tenant-Id", tenantID)
		},
		Transport: p.Transport,
		ErrorHandler: func(rw http.ResponseWriter, _ *http.Request, dialErr error) {
			if p.Logf != nil {
				p.Logf("statelessproxy: dial pod %s failed: %v", decision.PodIP, dialErr)
			}
			writeError(rw, http.StatusBadGateway, "BAD_GATEWAY",
				"the stateless pod did not accept the request", nil)
		},
	}
	rp.ServeHTTP(w, r)
}

// statelessRetryAfterSeconds is the Retry-After hint on a stateless
// WARM_POOL_EXHAUSTED: the pool needs to scale a fresh pod in, which is
// bounded by warm-pod startup. spec: §5.2 line 519.
const statelessRetryAfterSeconds = 5

// errorEnvelope mirrors the §15.2.1 gateway error envelope so a
// stateless routing failure carries the same {code, category, message,
// retryable, details} shape as the session-mode surface.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code      string         `json:"code"`
	Category  string         `json:"category"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	cat, retryable := errorclassify.Classify(code)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{Error: errorBody{
		Code:      code,
		Category:  string(cat),
		Message:   message,
		Retryable: retryable,
		Details:   details,
	}})
}
