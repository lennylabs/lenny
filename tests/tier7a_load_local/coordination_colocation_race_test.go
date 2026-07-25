// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a local concurrency coverage for the §10.1 co-location invariant under
// a concurrent coordinator handoff. Two replicas share an in-process
// thread-safe lease store and session store, a real in-process §4.7 adapter
// models the still-running pod, and the survivor's Sweeper runs concurrently
// with request-path probes. A second session is seeded terminal with a pod
// still assigned, so the survivor's takeover sweep enumerates it every pass but
// its §10.1 terminal-skip gate must exclude it from adoption. The test asserts,
// under -race, that the lease holder holds the binding it re-established at
// takeover, that a request landing in the pre-publish window fails closed rather
// than serving over an unpublished binding, and that the terminal session is
// never re-adopted (its readopter is never called and its
// coordination_generation is never bumped), so a terminal session is not
// resurrected by the takeover.
package tier7a_load_local_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordination"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
	"github.com/lennylabs/lenny/tests/testinfra/coordfixture"
)

// syncLeases is a concurrency-safe in-memory leasestore.LeaseStore for the
// race test: Acquire is holder-fenced (a held lease is re-acquired only by its
// current holder), matching the real leasestore contract the co-location
// invariant relies on.
type syncLeases struct {
	mu      sync.Mutex
	holders map[string]string
}

func newSyncLeases() *syncLeases { return &syncLeases{holders: map[string]string{}} }

func slk(t, s string) string { return t + "/" + s }

func (l *syncLeases) Acquire(_ context.Context, tenantID, sessionID, holder string, _ time.Duration) (leasestore.Lease, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	k := slk(tenantID, sessionID)
	if cur, ok := l.holders[k]; ok && cur != holder {
		return leasestore.Lease{}, leasestore.ErrHeld
	}
	l.holders[k] = holder
	return leasestore.Lease{TenantID: tenantID, SessionID: sessionID, Holder: holder}, nil
}

func (l *syncLeases) Renew(_ context.Context, tenantID, sessionID, holder string, _ time.Duration) (leasestore.Lease, error) {
	return leasestore.Lease{TenantID: tenantID, SessionID: sessionID, Holder: holder}, nil
}

func (l *syncLeases) Release(_ context.Context, tenantID, sessionID, holder string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	k := slk(tenantID, sessionID)
	if cur, ok := l.holders[k]; ok && cur == holder {
		delete(l.holders, k)
	}
	return nil
}

func (l *syncLeases) Get(_ context.Context, tenantID, sessionID string) (leasestore.Lease, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	h, ok := l.holders[slk(tenantID, sessionID)]
	if !ok {
		return leasestore.Lease{}, leasestore.ErrNotFound
	}
	return leasestore.Lease{TenantID: tenantID, SessionID: sessionID, Holder: h}, nil
}

func (l *syncLeases) holder(tenantID, sessionID string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	h, ok := l.holders[slk(tenantID, sessionID)]
	return h, ok
}

func (l *syncLeases) DeleteByUser(context.Context, string, string) (int, error) { return 0, nil }
func (l *syncLeases) DeleteByTenant(context.Context, string) (int, error)       { return 0, nil }

// staticTenants is a coordination.TenantLister over a fixed slice.
type staticTenants []string

func (s staticTenants) ListTenants(context.Context) ([]string, error) { return []string(s), nil }

// spec: §10.1 (coordinator handoff re-adopts the still-running pod; no
// operational RPC before the fence acknowledges), §4.6.1 (coordinating replica
// holds the lease), §4.7 (single content consumer / Attach content stream).
//
// diagnosis: a failure means the co-location invariant broke under a concurrent
// handoff — the lease holder served without the re-established binding, a
// request served over an unpublished pre-fence binding, or a terminal session
// was resurrected — or the Sweeper's takeover path is not race-clean.
func TestColocationInvariantUnderConcurrentHandoff_spec_10_1(t *testing.T) {
	const tenant = "acme"
	const live = "live"
	const terminating = "terminating"
	const ttl = 30 * time.Second

	leases := newSyncLeases()
	sessions := memstore.New()
	ctx := context.Background()

	if err := sessions.Create(ctx, sessionstore.Session{
		ID: live, TenantID: tenant, State: session.StateRunning,
		PodAssignment: "pod-" + live, CoordinationGeneration: 1, CreatedAt: time.Unix(1, 0).UTC(),
	}); err != nil {
		t.Fatalf("seed live session: %v", err)
	}
	// A second session that is already terminal yet still carries a pod
	// assignment (a running pod whose lifecycle ended before this window). It is
	// enumerated by every survivor sweep, but the §10.1 terminal-skip gate must
	// exclude a terminal session from adoption so the survivor never resurrects
	// it. Seeding it terminal (rather than forcing the readopter to relinquish)
	// pins the gate directly: an adoptable predicate that wrongly admitted a
	// terminal state would call the readopter and bump the generation, which the
	// assertions below reject.
	if err := sessions.Create(ctx, sessionstore.Session{
		ID: terminating, TenantID: tenant, State: session.StateCompleted,
		PodAssignment: "pod-" + terminating, CreatedAt: time.Unix(1, 0).UTC(),
	}); err != nil {
		t.Fatalf("seed terminal session: %v", err)
	}

	pod := coordfixture.StartPod(t, live)
	if _, err := pod.Fence(ctx, 1); err != nil {
		t.Fatalf("initial fence: %v", err)
	}

	// replica-1 crashed before the test window: its lease already lapsed, so
	// the live session is a lapsed-lease orphan replica-2 will take over.
	bound2 := coordfixture.NewBindings()
	readopter := &coordfixture.FenceReadopter{
		Pod: pod, Bindings: bound2, Leases: leases, ReplicaID: "replica-2", TenantID: tenant,
	}
	sweep2 := coordination.NewSweeper(staticTenants{tenant}, sessions, leases, coordination.Options{
		ReplicaID: "replica-2",
		TTL:       ttl,
		Interval:  time.Hour,
		Bindings:  bound2,
		Readopter: readopter,
	})

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Takeover driver: sweep replica-2 repeatedly until the takeover publishes.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := sweep2.Sweep(ctx); err != nil {
				t.Errorf("concurrent Sweep: %v", err)
				return
			}
			if bound2.Bound(live) {
				return
			}
		}
	}()

	// Request-path probes: a request serves only over a published binding. In
	// the pre-publish window Bound is false and the request fails closed
	// (retries). Whenever it observes a bound session (a served request), the
	// co-location invariant must hold: replica-2 holds the lease.
	var failClosed, served int64
	var probeMu sync.Mutex
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if bound2.Bound(live) {
					h, ok := leases.holder(tenant, live)
					if !ok || h != "replica-2" {
						t.Errorf("served request over a binding held by replica-2 but lease holder = %q ok=%v (co-location broken)", h, ok)
					}
					probeMu.Lock()
					served++
					probeMu.Unlock()
				} else {
					probeMu.Lock()
					failClosed++
					probeMu.Unlock()
				}
			}
		}()
	}

	// Let the concurrent window run, then quiesce.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !bound2.Bound(live) {
		time.Sleep(2 * time.Millisecond)
	}
	// Give the probes a moment to observe the published binding.
	time.Sleep(20 * time.Millisecond)
	close(stop)
	wg.Wait()

	// The takeover published the binding and the lease is co-located.
	if !bound2.Bound(live) {
		t.Fatalf("takeover did not publish the binding within the window")
	}
	if h, ok := leases.holder(tenant, live); !ok || h != "replica-2" {
		t.Fatalf("post-handoff lease holder = %q ok=%v, want replica-2", h, ok)
	}
	if pod.LastFenced() != 2 {
		t.Fatalf("pod fenced generation = %d, want 2 (fenced to the post-handoff generation)", pod.LastFenced())
	}
	got, _ := sessions.Get(ctx, tenant, live)
	if got.CoordinationGeneration != 2 {
		t.Fatalf("coordination_generation = %d, want 2 (handoff bumped once)", got.CoordinationGeneration)
	}

	// The terminal session was not resurrected: the survivor's takeover sweep
	// enumerated it every pass but its terminal-skip gate excluded it, so the
	// readopter was never called for it, its coordination_generation was never
	// bumped, and it acquired neither a lease nor a binding. Asserting the
	// readopter call count and the generation directly pins the terminal gate,
	// rather than inferring non-resurrection from a relinquished lease.
	if n := readopter.CalledFor(terminating); n != 0 {
		t.Errorf("readopter called %d times for the terminal session, want 0 (terminal session must not be adopted)", n)
	}
	gotTerm, _ := sessions.Get(ctx, tenant, terminating)
	if gotTerm.CoordinationGeneration != 0 {
		t.Errorf("terminal session coordination_generation = %d, want 0 (never adopted/bumped)", gotTerm.CoordinationGeneration)
	}
	if _, ok := leases.holder(tenant, terminating); ok {
		t.Errorf("terminal session acquired a coordination lease, want none (not resurrected)")
	}
	if bound2.Bound(terminating) {
		t.Errorf("terminal session was bound by the survivor, want unbound (not resurrected)")
	}

	// The pre-publish window fails closed: the probes saw at least one
	// fail-closed observation before the binding was published, and every
	// served observation held the co-location invariant (asserted inline).
	probeMu.Lock()
	fc, sv := failClosed, served
	probeMu.Unlock()
	if fc == 0 {
		t.Errorf("no fail-closed observation recorded; the pre-publish window was not exercised")
	}
	if sv == 0 {
		t.Errorf("no served observation recorded after the takeover published the binding")
	}
}
