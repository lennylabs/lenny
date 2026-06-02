// SPDX-License-Identifier: MIT

package opsservice

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
)

// ReplicaPollInterval is the §25.4 line 2208 cadence at which the
// single-replica-only lock policy re-checks the lenny-ops replica count.
const ReplicaPollInterval = 30 * time.Second

// EndpointsReplicaCounter reports the number of ready lenny-ops replicas
// by reading the lenny-ops Service Endpoints. It satisfies
// coordination.ReplicaCounter so the §25.4 ops.locks.memoryTier
// single-replica-only policy can reject in-memory (Tier-3) lock
// acquisitions in a multi-replica deployment.
//
// spec: §25.4 line 2208 — "detected via K8s Endpoints lookup at startup
// and re-checked every 30s".
type EndpointsReplicaCounter struct {
	endpoints corev1client.EndpointsGetter
	namespace string
	service   string
	count     atomic.Int64
}

// NewEndpointsReplicaCounter returns a counter that resolves the ready
// replica count from the Endpoints object named service in namespace.
// The count starts at 1 (the local replica) until the first Refresh so
// the single-replica-only policy admits acquisitions during the startup
// window rather than failing closed before the first lookup completes.
func NewEndpointsReplicaCounter(ep corev1client.EndpointsGetter, namespace, service string) *EndpointsReplicaCounter {
	c := &EndpointsReplicaCounter{endpoints: ep, namespace: namespace, service: service}
	c.count.Store(1)
	return c
}

// ReplicaCount returns the most recently observed ready replica count.
func (c *EndpointsReplicaCounter) ReplicaCount() int { return int(c.count.Load()) }

// Refresh reads the Service Endpoints and updates the cached ready
// count. A read error leaves the prior count in place (best-effort, per
// the §25.4 "re-checked every 30s" model) and is returned to the caller.
func (c *EndpointsReplicaCounter) Refresh(ctx context.Context) error {
	ep, err := c.endpoints.Endpoints(c.namespace).Get(ctx, c.service, metav1.GetOptions{})
	if err != nil {
		return err
	}
	ready := 0
	for _, subset := range ep.Subsets {
		ready += len(subset.Addresses)
	}
	// A zero ready count means the Endpoints object exists but lists no
	// ready addresses (the local replica is still becoming ready). Never
	// report fewer than one — the calling replica is itself a replica, and
	// reporting zero would misclassify a single-replica deployment.
	if ready < 1 {
		ready = 1
	}
	c.count.Store(int64(ready))
	return nil
}

// Run performs the §25.4 startup lookup, then re-checks the replica
// count every interval until ctx is cancelled. It blocks, so callers run
// it on its own goroutine. The startup lookup also runs here, so a
// synchronous pre-traffic read should call Refresh directly before Run.
func (c *EndpointsReplicaCounter) Run(ctx context.Context, interval time.Duration) {
	if err := c.Refresh(ctx); err != nil {
		log.Printf("lenny-ops: §25.4 replica-count lookup failed (assuming %d): %v", c.ReplicaCount(), err)
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := c.Refresh(ctx); err != nil {
				log.Printf("lenny-ops: §25.4 replica-count re-check failed (keeping %d): %v", c.ReplicaCount(), err)
			}
		}
	}
}
