// SPDX-License-Identifier: MIT

// Package statelessrouting assembles the §5.2 concurrent-stateless data
// plane into the gateway's HTTP surface. It owns one
// tenantaffinity.Router, EndpointPoller, and statelessproxy.Proxy per
// stateless pool, lazily building a pool's route on first request and
// dispatching `/v1/stateless/{pool}/...` to that pool's proxy.
//
// The route is built lazily (rather than from a startup pool snapshot)
// so a stateless pool created through the admin API after the gateway
// started becomes routable without a restart. A pool that does not
// exist, or that is not executionMode=service, is rejected so the
// stateless ingress never proxies to a session-mode pool.
//
// spec: §5.2.
package statelessrouting

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/errorclassify"
	"github.com/lennylabs/lenny/pkg/gateway/statelessproxy"
	"github.com/lennylabs/lenny/pkg/gateway/tenantaffinity"
)

// PoolInfo describes a resolved stateless pool. Stateless is false for a
// pool that exists but is not executionMode=service.
type PoolInfo struct {
	// Stateless is true only for a service-mode pool.
	Stateless bool
	// MaxConcurrent is the pod slot cap (informational here; the pod's
	// readiness probe enforces it).
	MaxConcurrent int
}

// PoolResolver resolves a pool name to its stateless routing config. It
// returns found=false when the pool does not exist. The production
// implementation reads the §5.2 poolstore.
type PoolResolver func(ctx context.Context, pool string) (info PoolInfo, found bool, err error)

// ListerFactory builds the EndpointLister for one pool. The production
// implementation binds a pod-label lister to the cluster client; tests
// inject a fake.
type ListerFactory func(pool string) tenantaffinity.EndpointLister

// Options configure a Manager. Resolver, NewLister, and Tenant are
// required; the rest are optional.
type Options struct {
	Resolver  PoolResolver
	NewLister ListerFactory
	Tenant    statelessproxy.TenantResolver
	Labeler   statelessproxy.PodLabeler
	Metrics   tenantaffinity.StatelessMetrics
	PodPort   int
	// PollInterval overrides the EndpointPoller cadence (0 = default).
	PollInterval time.Duration
	Logf         func(format string, args ...any)
}

// Manager dispatches stateless ingress requests to per-pool routes,
// building each route on first use.
type Manager struct {
	opts Options
	// baseCtx bounds the lifetime of every per-pool poller goroutine.
	baseCtx context.Context

	mu     sync.Mutex
	routes map[string]*route
}

type route struct {
	router *tenantaffinity.Router
	proxy  *statelessproxy.Proxy
}

// New returns a Manager. baseCtx bounds every per-pool EndpointPoller; on
// cancellation all pollers stop.
func New(baseCtx context.Context, opts Options) *Manager {
	return &Manager{
		opts:    opts,
		baseCtx: baseCtx,
		routes:  map[string]*route{},
	}
}

// ServeHTTP handles `/v1/stateless/{pool}/...`. It strips the
// `/v1/stateless/{pool}` prefix, rebases the request path on the
// remainder, and proxies to the pool's tenant-pinned pod.
func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	pool, rest, ok := splitPoolPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND",
			"stateless ingress path must be /v1/stateless/{pool}/...", nil)
		return
	}

	rt, err := m.routeFor(r.Context(), pool)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "INTERNAL_ERROR",
			"could not resolve stateless pool: "+err.Error(), nil)
		return
	}
	if rt == nil {
		writeError(w, http.StatusNotFound, "RUNTIME_NOT_FOUND",
			"no concurrent-stateless pool named "+pool, map[string]any{"pool": pool})
		return
	}

	// Rebase the path so the runtime sees its own request path, not the
	// gateway's ingress prefix.
	r2 := r.Clone(r.Context())
	r2.URL.Path = rest
	rt.proxy.ServeHTTP(w, r2)
}

// routeFor returns the cached route for pool, building it on first use.
// It returns (nil, nil) when the pool is not a concurrent-stateless pool.
func (m *Manager) routeFor(ctx context.Context, pool string) (*route, error) {
	m.mu.Lock()
	if rt, ok := m.routes[pool]; ok {
		m.mu.Unlock()
		return rt, nil
	}
	m.mu.Unlock()

	info, found, err := m.opts.Resolver(ctx, pool)
	if err != nil {
		return nil, err
	}
	if !found || !info.Stateless {
		return nil, nil
	}

	router := tenantaffinity.New(pool, m.opts.Metrics)
	proxy := &statelessproxy.Proxy{
		Router:  router,
		Tenant:  m.opts.Tenant,
		Labeler: m.opts.Labeler,
		PodPort: m.opts.PodPort,
		Logf:    m.opts.Logf,
	}

	m.mu.Lock()
	// Re-check under the lock so two concurrent first-requests build one
	// route and start one poller.
	if rt, ok := m.routes[pool]; ok {
		m.mu.Unlock()
		return rt, nil
	}
	rt := &route{router: router, proxy: proxy}
	m.routes[pool] = rt
	m.mu.Unlock()

	poller := &tenantaffinity.EndpointPoller{
		Lister:   m.opts.NewLister(pool),
		Router:   router,
		Interval: m.opts.PollInterval,
		Logf:     m.opts.Logf,
	}
	go poller.Run(m.baseCtx)
	return rt, nil
}

// splitPoolPath parses `/v1/stateless/{pool}/rest...` into ("{pool}",
// "/rest...", true). A request with no pool segment returns ok=false.
func splitPoolPath(p string) (pool, rest string, ok bool) {
	const prefix = "/v1/stateless/"
	if !strings.HasPrefix(p, prefix) {
		return "", "", false
	}
	tail := p[len(prefix):]
	if tail == "" {
		return "", "", false
	}
	if i := strings.IndexByte(tail, '/'); i >= 0 {
		pool = tail[:i]
		rest = tail[i:]
	} else {
		pool = tail
		rest = "/"
	}
	if pool == "" {
		return "", "", false
	}
	return pool, rest, true
}

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
