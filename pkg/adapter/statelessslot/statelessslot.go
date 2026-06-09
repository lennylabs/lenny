// SPDX-License-Identifier: MIT

// Package statelessslot is the in-pod slot accountant for §5.2
// concurrent-stateless pods. The gateway reverse-proxies stateless
// requests straight to the pod (bypassing the Service load balancer);
// this package fronts the runtime's stateless HTTP surface, counts the
// in-flight requests as slots, and drives the pod's readiness probe so
// it reports NotReady once the pod reaches maxConcurrent.
//
// The §5.2 line 500 model relies on this signal: "Pod readiness probe
// reflects slot availability." When a pod hits slot capacity its
// readiness flips false, the pod drops out of the Service's
// EndpointSlice, the gateway's tenantaffinity.Router stops routing new
// requests to it, and a fresh unpinned pod is selected instead. When a
// slot frees, readiness flips back true and the pod re-enters routing.
//
// spec: §5.2 line 500 (readiness reflects slot availability), §5.2
// "Concurrent-stateless limitations (v1)" (the platform tracks only
// slot occupancy, not individual task outcomes).
package statelessslot

import (
	"net/http"
	"sync"
)

// Gate is a bounded in-flight counter. It admits up to MaxConcurrent
// simultaneous slots and reports readiness as `active < max`. It is safe
// for concurrent use by the serving goroutines and the readiness-probe
// goroutine.
type Gate struct {
	mu     sync.Mutex
	max    int
	active int
}

// NewGate returns a slot gate that admits up to maxConcurrent
// simultaneous requests. A non-positive maxConcurrent is treated as 1
// (the smallest valid §5.2 maxConcurrent), so a misconfigured pod
// serializes rather than admitting unbounded concurrency.
func NewGate(maxConcurrent int) *Gate {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &Gate{max: maxConcurrent}
}

// Acquire reserves one slot, returning false when the pod is already at
// MaxConcurrent. A false return is the defensive backstop for the race
// where the readiness probe has not yet removed a full pod from routing
// and the gateway delivers one extra request.
func (g *Gate) Acquire() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active >= g.max {
		return false
	}
	g.active++
	return true
}

// Release frees one slot. It floors at zero so a double Release cannot
// drive the count negative.
func (g *Gate) Release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active > 0 {
		g.active--
	}
}

// Active returns the current in-flight slot count.
func (g *Gate) Active() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.active
}

// Max returns the pod's configured slot capacity.
func (g *Gate) Max() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.max
}

// Ready reports the §5.2 line 500 readiness signal: a pod is ready while
// it has a free slot (active < max) and NotReady at capacity.
func (g *Gate) Ready() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.active < g.max
}

// Serve wraps the runtime's stateless handler so each request occupies a
// slot for its duration. A request that arrives at capacity (the
// readiness-probe race) is rejected with 503 rather than admitted past
// the cap, so the pod never exceeds maxConcurrent simultaneous slots.
func (g *Gate) Serve(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.Acquire() {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "slot capacity reached", http.StatusServiceUnavailable)
			return
		}
		defer g.Release()
		next.ServeHTTP(w, r)
	})
}

// ReadyHandler is the pod's readinessProbe target. It returns 200 while a
// slot is free and 503 at capacity, so the kubelet marks the pod
// NotReady and the EndpointSlice drops it from the route-target set.
func (g *Gate) ReadyHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if g.Ready() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ready"))
			return
		}
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("slot capacity reached"))
	}
}
