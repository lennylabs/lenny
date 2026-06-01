// SPDX-License-Identifier: MIT

package redisstream

import (
	"context"
	"sync"
	"time"
)

// DefaultReconcileInterval is how often FlusherManager re-reads the
// tenant set to start flushers for new tenants and stop them for
// removed tenants. The §11.2.1 design specifies one flusher goroutine
// per tenant; this reconcile cadence keeps that set in sync without a
// per-publish hook.
const DefaultReconcileInterval = 60 * time.Second

// TenantLister enumerates the tenants whose §11.2.1 Tier 1 billing
// streams need a flusher. The gateway's tenant store satisfies it.
type TenantLister interface {
	ListTenants(ctx context.Context) ([]string, error)
}

// FlusherManager runs one §11.2.1 per-tenant RunFlusher goroutine for
// every active tenant, the missing piece between the published Tier 1
// stream and Postgres recovery. The spec mandates "a background flusher
// goroutine per tenant"; without the manager the stream accumulates
// during a Postgres outage and the entries are lost at the stream TTL.
//
// The manager reconciles the running set against the tenant lister on a
// fixed interval: a tenant that appears gets a RunFlusher goroutine
// (which performs the startup fast-recovery XAUTOCLAIM, then alternates
// XREADGROUP flush with periodic XAUTOCLAIM reclaim); a tenant that
// disappears has its goroutine cancelled. The RunFlusher loop itself
// runs the §11.2.1 flush schedule.
//
// spec: §11 line 144 — background flusher goroutine per tenant.
type FlusherManager struct {
	tenants        TenantLister
	flushInterval  time.Duration
	reconcileEvery time.Duration
	// run starts the per-tenant flush loop, blocking until ctx is
	// cancelled. It is Tier.RunFlusher in production and an injected stub
	// in tests.
	run func(ctx context.Context, tenantID string, flushInterval time.Duration)

	mu      sync.Mutex
	running map[string]context.CancelFunc
	wg      sync.WaitGroup
}

// NewFlusherManager builds a manager that drives RunFlusher on tier for
// every tenant the lister returns. flushInterval is the §11.2.1
// billingFlushIntervalMs cadence; a non-positive reconcileEvery selects
// DefaultReconcileInterval.
//
// spec: §11 line 144.
func (t *Tier) NewFlusherManager(tenants TenantLister, flushInterval, reconcileEvery time.Duration) *FlusherManager {
	return newFlusherManager(tenants, flushInterval, reconcileEvery, t.RunFlusher)
}

// newFlusherManager is the injectable constructor the tests use to
// substitute a stub run function for Tier.RunFlusher.
func newFlusherManager(
	tenants TenantLister,
	flushInterval, reconcileEvery time.Duration,
	run func(ctx context.Context, tenantID string, flushInterval time.Duration),
) *FlusherManager {
	if reconcileEvery <= 0 {
		reconcileEvery = DefaultReconcileInterval
	}
	return &FlusherManager{
		tenants:        tenants,
		flushInterval:  flushInterval,
		reconcileEvery: reconcileEvery,
		run:            run,
		running:        make(map[string]context.CancelFunc),
	}
}

// Run reconciles the per-tenant flusher set immediately and then on the
// reconcile interval until ctx is cancelled. On exit it cancels every
// per-tenant goroutine and waits for them to return so a graceful
// shutdown does not leak flushers.
//
// spec: §11 line 144.
func (m *FlusherManager) Run(ctx context.Context) {
	defer m.stopAll()
	m.reconcile(ctx)
	ticker := time.NewTicker(m.reconcileEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.reconcile(ctx)
		}
	}
}

// reconcile starts a flusher for each tenant not already running and
// stops the flusher of any tenant no longer listed. A lister error
// leaves the current set untouched: a transient tenant-store fault must
// not tear down healthy flushers.
func (m *FlusherManager) reconcile(ctx context.Context) {
	tenants, err := m.tenants.ListTenants(ctx)
	if err != nil {
		return
	}
	want := make(map[string]bool, len(tenants))
	for _, id := range tenants {
		if id != "" {
			want[id] = true
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for id := range want {
		if _, ok := m.running[id]; ok {
			continue
		}
		tenantCtx, cancel := context.WithCancel(ctx)
		m.running[id] = cancel
		m.wg.Add(1)
		go func(tenantID string) {
			defer m.wg.Done()
			m.run(tenantCtx, tenantID, m.flushInterval)
		}(id)
	}
	for id, cancel := range m.running {
		if !want[id] {
			cancel()
			delete(m.running, id)
		}
	}
}

// stopAll cancels every per-tenant flusher and waits for them to drain.
func (m *FlusherManager) stopAll() {
	m.mu.Lock()
	for id, cancel := range m.running {
		cancel()
		delete(m.running, id)
	}
	m.mu.Unlock()
	m.wg.Wait()
}

// activeCount returns the number of running per-tenant flushers. Tests
// use it to assert reconcile started/stopped the expected goroutines.
func (m *FlusherManager) activeCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.running)
}
