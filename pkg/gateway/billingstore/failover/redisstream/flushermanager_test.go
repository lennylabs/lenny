// SPDX-License-Identifier: MIT

package redisstream

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// mutableLister is a TenantLister whose tenant set and error can change
// between reconcile passes.
type mutableLister struct {
	mu      sync.Mutex
	tenants []string
	err     error
}

func (l *mutableLister) set(ts ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.tenants = ts
}

func (l *mutableLister) setErr(e error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.err = e
}

func (l *mutableLister) ListTenants(context.Context) ([]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return nil, l.err
	}
	return append([]string(nil), l.tenants...), nil
}

// runTracker records the per-tenant stub flusher's start and stop so a
// test can assert the manager launched and cancelled goroutines.
type runTracker struct {
	mu      sync.Mutex
	starts  map[string]int
	running map[string]bool
}

func newRunTracker() *runTracker {
	return &runTracker{starts: map[string]int{}, running: map[string]bool{}}
}

func (r *runTracker) run(ctx context.Context, tenantID string, _ time.Duration) {
	r.mu.Lock()
	r.starts[tenantID]++
	r.running[tenantID] = true
	r.mu.Unlock()
	<-ctx.Done()
	r.mu.Lock()
	r.running[tenantID] = false
	r.mu.Unlock()
}

func (r *runTracker) isRunning(tenantID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running[tenantID]
}

func (r *runTracker) startCount(tenantID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.starts[tenantID]
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met within deadline")
}

// spec: §11 line 144 — one flusher goroutine per tenant. reconcile
// starts exactly one flusher for each listed tenant and is idempotent.
func TestFlusherManagerStartsOnePerTenant(t *testing.T) {
	lister := &mutableLister{}
	lister.set("acme", "globex")
	tr := newRunTracker()
	m := newFlusherManager(lister, time.Millisecond, time.Hour, tr.run)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.reconcile(ctx)
	if got := m.activeCount(); got != 2 {
		t.Fatalf("activeCount after first reconcile = %d, want 2", got)
	}
	waitFor(t, func() bool { return tr.isRunning("acme") && tr.isRunning("globex") })

	// A second reconcile with the same set must not start duplicates.
	m.reconcile(ctx)
	if got := m.activeCount(); got != 2 {
		t.Errorf("activeCount after idempotent reconcile = %d, want 2", got)
	}
	if c := tr.startCount("acme"); c != 1 {
		t.Errorf("acme start count = %d, want 1 (no duplicate flusher)", c)
	}
}

// spec: §11 line 144 — a tenant removed from the set has its flusher
// goroutine cancelled.
func TestFlusherManagerStopsRemovedTenant(t *testing.T) {
	lister := &mutableLister{}
	lister.set("acme", "globex")
	tr := newRunTracker()
	m := newFlusherManager(lister, time.Millisecond, time.Hour, tr.run)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.reconcile(ctx)
	waitFor(t, func() bool { return tr.isRunning("globex") })

	lister.set("acme")
	m.reconcile(ctx)
	if got := m.activeCount(); got != 1 {
		t.Fatalf("activeCount after removal = %d, want 1", got)
	}
	waitFor(t, func() bool { return !tr.isRunning("globex") })
	if !tr.isRunning("acme") {
		t.Error("acme flusher must keep running")
	}
}

// spec: §11 line 144 — a transient tenant-store fault must not tear
// down healthy flushers.
func TestFlusherManagerListerErrorLeavesSetUntouched(t *testing.T) {
	lister := &mutableLister{}
	lister.set("acme")
	tr := newRunTracker()
	m := newFlusherManager(lister, time.Millisecond, time.Hour, tr.run)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.reconcile(ctx)
	waitFor(t, func() bool { return tr.isRunning("acme") })

	lister.setErr(errors.New("tenant store down"))
	m.reconcile(ctx)
	if got := m.activeCount(); got != 1 {
		t.Errorf("activeCount after lister error = %d, want 1 (untouched)", got)
	}
	if !tr.isRunning("acme") {
		t.Error("acme flusher must survive a lister error")
	}
}

// spec: §11 line 144 — Run cancels every per-tenant flusher on shutdown
// and waits for them to drain.
func TestFlusherManagerRunCancelsAllOnShutdown(t *testing.T) {
	lister := &mutableLister{}
	lister.set("acme", "globex")
	tr := newRunTracker()
	m := newFlusherManager(lister, time.Millisecond, time.Hour, tr.run)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.Run(ctx)
		close(done)
	}()
	waitFor(t, func() bool { return tr.isRunning("acme") && tr.isRunning("globex") })

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
	if tr.isRunning("acme") || tr.isRunning("globex") {
		t.Error("flushers must be stopped after Run returns")
	}
	if got := m.activeCount(); got != 0 {
		t.Errorf("activeCount after shutdown = %d, want 0", got)
	}
}
