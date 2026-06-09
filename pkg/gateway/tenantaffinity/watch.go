// SPDX-License-Identifier: MIT

package tenantaffinity

import (
	"context"
	"time"
)

// DefaultEndpointPollInterval is the cadence the gateway re-lists a
// stateless pool's EndpointSlices to refresh the router's pod-IP and
// readiness snapshot. The §5.2 line 500 model discovers pod IPs behind
// the pool Service "using Kubernetes EndpointSlice watches"; a short
// poll bounds the staleness of the readiness flag (a pod that flips to
// slot-capacity readiness=false is removed from the route-target set
// within one interval), which is sufficient because Route already
// returns ErrNoAvailablePod for a transiently-stale full pod and the
// caller retries or scales.
//
// spec: §5.2 line 500.
const DefaultEndpointPollInterval = 5 * time.Second

// EndpointLister returns the current pod endpoints behind a stateless
// pool's Kubernetes Service, read from the pool Service's
// discovery.k8s.io/v1 EndpointSlice objects. The production
// implementation queries the API server; tests inject a fake.
//
// spec: §5.2 line 500.
type EndpointLister interface {
	ListEndpoints(ctx context.Context) ([]Endpoint, error)
}

// EndpointPoller drives a periodic EndpointSlice re-list for one
// stateless pool, feeding each snapshot into Router.UpdateEndpoints so
// the router routes against the live pod-IP/readiness set. It is the
// §5.2 line 500 EndpointSlice-watch half of the concurrent-stateless
// data plane; Router is the decision half.
//
// spec: §5.2 line 500.
type EndpointPoller struct {
	// Lister enumerates the pool Service's endpoints. Required.
	Lister EndpointLister
	// Router receives each snapshot via UpdateEndpoints. Required.
	Router *Router
	// Interval is the poll cadence. Zero selects
	// DefaultEndpointPollInterval.
	Interval time.Duration
	// Logf, when set, receives a one-line diagnostic on each list error.
	// A failed list retains the router's last-known endpoint set rather
	// than draining it, so a transient API-server blip does not collapse
	// routing.
	Logf func(format string, args ...any)
}

// Run polls until ctx is cancelled. It blocks; callers start it in a
// goroutine. A poller missing a required seam is a no-op. The first
// list fires immediately so cold start converges to the real endpoint
// set within one API round trip rather than one full interval.
func (p *EndpointPoller) Run(ctx context.Context) {
	if p.Lister == nil || p.Router == nil {
		return
	}
	interval := p.Interval
	if interval <= 0 {
		interval = DefaultEndpointPollInterval
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

func (p *EndpointPoller) pollOnce(ctx context.Context) {
	eps, err := p.Lister.ListEndpoints(ctx)
	if err != nil {
		if p.Logf != nil {
			p.Logf("tenantaffinity: endpoint list failed for pool %q, retaining last snapshot: %v", p.Router.pool, err)
		}
		return
	}
	p.Router.UpdateEndpoints(eps)
}
