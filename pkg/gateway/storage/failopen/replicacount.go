// SPDX-License-Identifier: MIT

package failopen

import (
	"context"
	"sync"
	"time"
)

// DefaultReplicaPollInterval is the §12.4 line 224 cadence the gateway
// polls the Kubernetes Endpoints object for the gateway Service. The spec
// fixes the maximum staleness of cached_replica_count at 30s.
const DefaultReplicaPollInterval = 30 * time.Second

// ReplicaCount holds the §12.4 line 224 cached_replica_count: the
// last-known good number of ready gateway replicas, sourced from the
// Kubernetes Endpoints object. It is in-memory (never in Redis) and
// persists across individual poll failures so a dual outage (Redis +
// Endpoints simultaneously unavailable) divides the per-replica ceiling by
// the last observed count rather than collapsing every replica to 1 and
// admitting an N× overshoot. A cold start with no successful poll yet
// reads as 1.
//
// spec: §12.4 line 224.
type ReplicaCount struct {
	mu    sync.Mutex
	count int // 0 until the first successful poll; Get floors at 1
}

// NewReplicaCount returns a ReplicaCount in the cold-start state (Get
// returns 1 until the first successful Observe).
func NewReplicaCount() *ReplicaCount { return &ReplicaCount{} }

// Get returns the last successfully observed replica count, floored at 1
// per §12.4 line 224 (cold start defaults to 1; the floor also guards the
// tenant_limit / max(cached_replica_count, 1) division).
func (r *ReplicaCount) Get() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.count < 1 {
		return 1
	}
	return r.count
}

// Observe records a successful Endpoints poll. A non-positive count is
// ignored so a transient "zero ready endpoints" read (which would briefly
// un-bound the ceiling) does not overwrite the last-known good value.
func (r *ReplicaCount) Observe(count int) {
	if count <= 0 {
		return
	}
	r.mu.Lock()
	r.count = count
	r.mu.Unlock()
}

// EndpointsLister reports the number of ready endpoints backing the
// gateway Service. The production implementation queries the Kubernetes
// API server (corev1 Endpoints / EndpointSlices); tests inject a stub.
// spec: §12.4 line 224.
type EndpointsLister interface {
	CountReady(ctx context.Context) (int, error)
}

// ReplicaPoller drives a periodic Endpoints poll, updating a ReplicaCount
// on every success and leaving the last-known value untouched on failure.
type ReplicaPoller struct {
	// Lister queries the ready endpoint count. Required.
	Lister EndpointsLister
	// Count receives each successful observation. Required.
	Count *ReplicaCount
	// Interval is the poll cadence. Zero selects
	// DefaultReplicaPollInterval.
	Interval time.Duration
	// Logf, when set, receives a one-line diagnostic on each poll error.
	Logf func(format string, args ...any)
}

// Run polls until ctx is cancelled. It blocks; callers start it in a
// goroutine. A poller missing a required seam is a no-op. The first poll
// fires immediately so cold start converges to the real count within one
// API round trip rather than one full interval.
func (p *ReplicaPoller) Run(ctx context.Context) {
	if p.Lister == nil || p.Count == nil {
		return
	}
	interval := p.Interval
	if interval <= 0 {
		interval = DefaultReplicaPollInterval
	}
	p.pollOnce(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.pollOnce(ctx)
		}
	}
}

func (p *ReplicaPoller) pollOnce(ctx context.Context) {
	n, err := p.Lister.CountReady(ctx)
	if err != nil {
		if p.Logf != nil {
			p.Logf("failopen: endpoints poll failed, retaining cached replica count %d: %v", p.Count.Get(), err)
		}
		return
	}
	p.Count.Observe(n)
}
