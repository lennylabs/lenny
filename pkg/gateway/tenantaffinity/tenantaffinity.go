// SPDX-License-Identifier: MIT

// Package tenantaffinity implements the §5.2 concurrent-stateless
// tenant-affinity routing layer: the gateway's in-memory mapping of
// tenantId → set of pinned pod IPs behind a runtime's Kubernetes
// Service. It decides which pod IP a stateless request routes to, pins
// an unpinned pod to a tenant on first use, and enforces the
// tenant-pinning isolation invariant that a pod serves exactly one
// tenant for its lifetime.
//
// The router holds no Kubernetes client and opens no connections. The
// EndpointSlice watch feeds the current Service endpoints in via
// UpdateEndpoints; the gateway's HTTP client performs the actual dial
// against the pod IP that Route returns. This package decides; the
// cluster wiring (EndpointSlice informer, HTTP routing to the pinned
// IP, the lenny.dev/tenant-id label SSA, the readiness-driven slot
// signal) acts.
//
// spec: §5.2 line 500 (concurrencyStyle: stateless routing), §5.2 line
// 502 (tenant isolation), §5.2 line 573 (stateless demand metrics).
package tenantaffinity

import (
	"errors"
	"sort"
	"sync"
)

var (
	// ErrNoAvailablePod reports that the pool's Service exposes no pod
	// the request can route to: the tenant has no ready pinned pod and
	// no unpinned ready pod remains. The caller scales the pool up or
	// rejects the request with WARM_POOL_EXHAUSTED. spec: §5.2 line 500.
	ErrNoAvailablePod = errors.New("tenantaffinity: no available pod for tenant")

	// ErrTenantMismatch reports that the target pod is already pinned to
	// a tenant other than the requesting one. The §5.2 line 502
	// isolation invariant forbids cross-tenant routing in stateless
	// mode because slots share a network namespace and process space.
	ErrTenantMismatch = errors.New("tenantaffinity: pod is pinned to a different tenant")
)

// StatelessMetrics is the metric sink the router writes the §5.2
// service-mode demand signals (lenny_service_requests_total and
// lenny_service_concurrent_active) to. *gatewaymetrics.Metrics satisfies
// it. A nil sink disables emission so the router is usable in isolation.
type StatelessMetrics interface {
	IncStatelessRequest(pool string)
	SetStatelessConcurrentActive(pool string, value float64)
}

// Endpoint is one pod behind the pool's Kubernetes Service, as
// discovered by the EndpointSlice watch. Ready reflects the pod's
// readiness probe: §5.2 line 500 makes a pod at slot capacity report
// readiness false, which removes it from the route-target set until it
// recovers.
type Endpoint struct {
	PodIP string
	Ready bool
}

// Decision is the routing result. PodIP is the pod the request must
// dial. NewlyPinned is true when this call pinned a previously-unpinned
// pod to the tenant (the cluster wiring applies the lenny.dev/tenant-id
// label on a newly pinned pod).
type Decision struct {
	PodIP       string
	NewlyPinned bool
}

// Router is the per-pool tenant-affinity router. It is safe for
// concurrent use by multiple gateway request goroutines and the
// EndpointSlice watch goroutine.
type Router struct {
	pool    string
	metrics StatelessMetrics

	mu           sync.Mutex
	endpoints    map[string]Endpoint            // podIP -> endpoint
	pinnedTenant map[string]string              // podIP -> tenantID (reverse index)
	tenantPods   map[string]map[string]struct{} // tenantID -> set of podIP (the spec map)
	active       int                            // pool-wide in-flight requests
}

// New returns a router for the named pool (the SandboxTemplate name used
// as the metric label). metrics may be nil.
func New(pool string, metrics StatelessMetrics) *Router {
	return &Router{
		pool:         pool,
		metrics:      metrics,
		endpoints:    map[string]Endpoint{},
		pinnedTenant: map[string]string{},
		tenantPods:   map[string]map[string]struct{}{},
	}
}

// UpdateEndpoints reconciles the router against the current set of pods
// behind the pool's Service. New pods become routable (unpinned); pods
// absent from the snapshot are removed and unpinned from any tenant
// they were serving; the readiness of surviving pods is refreshed.
// spec: §5.2 line 500 (EndpointSlice watch discovers pod IPs).
func (r *Router) UpdateEndpoints(eps []Endpoint) {
	r.mu.Lock()
	defer r.mu.Unlock()

	next := make(map[string]Endpoint, len(eps))
	for _, e := range eps {
		if e.PodIP == "" {
			continue
		}
		next[e.PodIP] = e
	}
	for ip := range r.endpoints {
		if _, ok := next[ip]; !ok {
			r.unpinLocked(ip)
		}
	}
	r.endpoints = next
}

// Route returns the pod IP a concurrent-stateless request for tenantID
// must use, applying §5.2 line 500: a request first routes to an
// already-pinned, ready pod for the tenant; when the tenant has no
// pinned pod, or every pinned pod is at slot capacity (readiness
// false), an unpinned ready pod is selected and pinned. When no
// routable pod exists, ErrNoAvailablePod is returned.
//
// Each call increments lenny_service_requests_total (the §5.2 demand
// signal counts every arrival, including unservable ones, so the
// PoolScalingController scales up under unmet demand). A successful call
// also increments the pool-wide in-flight count published as
// lenny_service_concurrent_active; the caller MUST call Release exactly
// once when that request completes.
func (r *Router) Route(tenantID string) (Decision, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.metrics != nil {
		r.metrics.IncStatelessRequest(r.pool)
	}

	if ip, ok := r.readyPinnedLocked(tenantID); ok {
		r.beginLocked()
		return Decision{PodIP: ip}, nil
	}

	ip, ok := r.unpinnedReadyLocked()
	if !ok {
		return Decision{}, ErrNoAvailablePod
	}
	r.pinLocked(tenantID, ip)
	r.beginLocked()
	return Decision{PodIP: ip, NewlyPinned: true}, nil
}

// Release records that one in-flight request has completed, decrementing
// the pool-wide concurrent-active count published as
// lenny_service_concurrent_active. spec: §5.2.
func (r *Router) Release() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active > 0 {
		r.active--
	}
	if r.metrics != nil {
		r.metrics.SetStatelessConcurrentActive(r.pool, float64(r.active))
	}
}

// CheckTenant enforces the §5.2 line 502 tenant-pinning invariant: a pod
// serves exactly one tenant for its lifetime. It returns
// ErrTenantMismatch when podIP is already pinned to a tenant other than
// tenantID. A pod not yet pinned passes — Route pins it on first use.
func (r *Router) CheckTenant(podIP, tenantID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if owner, ok := r.pinnedTenant[podIP]; ok && owner != tenantID {
		return ErrTenantMismatch
	}
	return nil
}

// PinnedPods returns the sorted set of pod IPs currently pinned to
// tenantID — the §5.2 line 500 "tenantId → set of pinned pod IPs" map
// projected for one tenant.
func (r *Router) PinnedPods(tenantID string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	pods := r.tenantPods[tenantID]
	out := make([]string, 0, len(pods))
	for ip := range pods {
		out = append(out, ip)
	}
	sort.Strings(out)
	return out
}

// ActiveCount returns the current pool-wide in-flight request count
// mirrored to lenny_service_concurrent_active.
func (r *Router) ActiveCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active
}

// readyPinnedLocked returns the lowest pod IP pinned to tenantID whose
// readiness is true, choosing deterministically for stable routing.
func (r *Router) readyPinnedLocked(tenantID string) (string, bool) {
	best := ""
	for ip := range r.tenantPods[tenantID] {
		if e, ok := r.endpoints[ip]; ok && e.Ready && (best == "" || ip < best) {
			best = ip
		}
	}
	return best, best != ""
}

// unpinnedReadyLocked returns the lowest unpinned, ready pod IP.
func (r *Router) unpinnedReadyLocked() (string, bool) {
	best := ""
	for ip, e := range r.endpoints {
		if !e.Ready {
			continue
		}
		if _, pinned := r.pinnedTenant[ip]; pinned {
			continue
		}
		if best == "" || ip < best {
			best = ip
		}
	}
	return best, best != ""
}

func (r *Router) pinLocked(tenantID, ip string) {
	r.pinnedTenant[ip] = tenantID
	pods := r.tenantPods[tenantID]
	if pods == nil {
		pods = map[string]struct{}{}
		r.tenantPods[tenantID] = pods
	}
	pods[ip] = struct{}{}
}

// unpinLocked removes a pod's tenant pin and the reverse index entry,
// dropping the tenant's set when it becomes empty.
func (r *Router) unpinLocked(ip string) {
	tenant, ok := r.pinnedTenant[ip]
	if !ok {
		return
	}
	delete(r.pinnedTenant, ip)
	pods := r.tenantPods[tenant]
	delete(pods, ip)
	if len(pods) == 0 {
		delete(r.tenantPods, tenant)
	}
}

func (r *Router) beginLocked() {
	r.active++
	if r.metrics != nil {
		r.metrics.SetStatelessConcurrentActive(r.pool, float64(r.active))
	}
}
