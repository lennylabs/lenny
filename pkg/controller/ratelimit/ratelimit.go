// SPDX-License-Identifier: MIT

// Package ratelimit implements the §4.6.1 controller API-server rate
// limiting: two dedicated client-side token buckets routed by request, so
// pod-creation traffic is never starved by status-update traffic.
//
//   - Pod creation bucket: Create calls for new Sandbox pods.
//   - Status update bucket: UpdateStatus calls on Sandbox and
//     SandboxWarmPool resources.
//
// All other API server requests (reads, deletions, finalizer updates)
// share the controller-runtime default rate limiter. The buckets are
// applied at the HTTP transport layer because the controller-runtime
// client multiplexes every verb through one rest.Config; a transport
// wrapper is the only place that sees the method and path needed to route
// a request to its bucket.
package ratelimit

import (
	"net/http"
	"strings"

	"golang.org/x/time/rate"
	"k8s.io/client-go/rest"
)

// Defaults from §4.6.1 "API server rate limiting".
const (
	DefaultCreateQPS   = 20.0
	DefaultCreateBurst = 50
	DefaultStatusQPS   = 30.0
	DefaultStatusBurst = 100
	// DefaultOtherQPS and DefaultOtherBurst mirror the controller-runtime
	// default rate limiter the spec assigns to all other requests.
	DefaultOtherQPS   = 10.0
	DefaultOtherBurst = 100
)

// Config carries the per-bucket token-bucket parameters. A zero value in
// any field selects the corresponding default.
type Config struct {
	CreateQPS   float64
	CreateBurst int
	StatusQPS   float64
	StatusBurst int
	OtherQPS    float64
	OtherBurst  int
}

func (c Config) withDefaults() Config {
	if c.CreateQPS <= 0 {
		c.CreateQPS = DefaultCreateQPS
	}
	if c.CreateBurst <= 0 {
		c.CreateBurst = DefaultCreateBurst
	}
	if c.StatusQPS <= 0 {
		c.StatusQPS = DefaultStatusQPS
	}
	if c.StatusBurst <= 0 {
		c.StatusBurst = DefaultStatusBurst
	}
	if c.OtherQPS <= 0 {
		c.OtherQPS = DefaultOtherQPS
	}
	if c.OtherBurst <= 0 {
		c.OtherBurst = DefaultOtherBurst
	}
	return c
}

// bucket names the token bucket a request routes to.
type bucket int

const (
	bucketOther bucket = iota
	bucketCreate
	bucketStatus
)

// transport is the http.RoundTripper that waits on the routed bucket
// before delegating to the wrapped RoundTripper.
type transport struct {
	base   http.RoundTripper
	create *rate.Limiter
	status *rate.Limiter
	other  *rate.Limiter
}

// RoundTrip waits for a token from the request's routed bucket, then
// delegates. A cancelled request context aborts the wait and the request.
func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	limiter := t.other
	switch classify(req.Method, req.URL.Path) {
	case bucketCreate:
		limiter = t.create
	case bucketStatus:
		limiter = t.status
	}
	if err := limiter.Wait(req.Context()); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(req)
}

// classify routes a request to its bucket from the HTTP method and path.
// POST to a sandboxes collection is a pod Create; PUT/PATCH to a
// sandboxes or sandboxwarmpools status subresource is a status update.
func classify(method, path string) bucket {
	switch method {
	case http.MethodPost:
		if isSandboxCollection(path) {
			return bucketCreate
		}
	case http.MethodPut, http.MethodPatch:
		if isStatusSubresource(path) {
			return bucketStatus
		}
	}
	return bucketOther
}

// isSandboxCollection reports whether path addresses the sandboxes
// collection (a Create target) rather than a named resource or
// subresource.
func isSandboxCollection(path string) bool {
	trimmed := strings.TrimRight(path, "/")
	return strings.HasSuffix(trimmed, "/sandboxes")
}

// isStatusSubresource reports whether path addresses the /status
// subresource of a sandbox or sandboxwarmpool resource.
func isStatusSubresource(path string) bool {
	if !strings.HasSuffix(path, "/status") {
		return false
	}
	return strings.Contains(path, "/sandboxes/") || strings.Contains(path, "/sandboxwarmpools/")
}

// WrapConfig installs the dual rate limiter on cfg. It disables the
// rest.Config client-side limiter (QPS = -1) so the transport-level
// buckets are the sole client-side throughput control, and wraps the
// transport so every request waits on its routed bucket. The returned
// config is cfg, mutated in place.
func WrapConfig(cfg *rest.Config, c Config) *rest.Config {
	c = c.withDefaults()
	// Disable the rest client's own rate limiter; the transport buckets
	// below replace it. Leaving it enabled would double-limit every
	// request and re-introduce the cross-bucket starvation this design
	// exists to prevent.
	cfg.QPS = -1
	cfg.Burst = 0
	cfg.RateLimiter = nil

	create := rate.NewLimiter(rate.Limit(c.CreateQPS), c.CreateBurst)
	status := rate.NewLimiter(rate.Limit(c.StatusQPS), c.StatusBurst)
	other := rate.NewLimiter(rate.Limit(c.OtherQPS), c.OtherBurst)

	prev := cfg.WrapTransport
	cfg.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
		if prev != nil {
			rt = prev(rt)
		}
		return &transport{base: rt, create: create, status: status, other: other}
	}
	return cfg
}
