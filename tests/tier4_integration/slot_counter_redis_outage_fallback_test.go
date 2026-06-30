// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for the §3.2 / §6.57 / §12.4 Redis-outage Postgres
// fallback behind the §5.2 intra-pod slot counter. It wires a real
// slotcounter.Counter against a real Redis container and a real Postgres
// container (the production migrations applied) as the FallbackSource, then
// injects a Redis outage by stopping the Redis container mid-flight. The test
// asserts the multi-service flow the proposal names: a Redis-only outage
// degrades intra-pod slot admission to the Postgres advisory-lock gate rather
// than rejecting all session dispatch, concurrent admissions during the outage
// serialize under the per-pod advisory lock so the bound is not overrun, and
// after the bounded fail-closed window the gate fails closed.
package tier4_integration_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/sessionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// newSessionUUID returns a fresh random UUIDv4 string; the sessions.id column
// is typed UUID per §12.6.
func newSessionUUID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// spec: §3.2 (Redis slot counter intra-pod gate with Postgres fallback),
// §6.57 (durable fallback for every Redis-backed role), §12.4 (bounded
// fail-closed window).
// diagnosis: a failure means the §5.2 slot counter did not degrade to the
// Postgres advisory-lock fallback on a Redis outage. Either a Redis-only
// outage rejected all slot dispatch (an unguarded mandatory increment), the
// per-pod advisory lock did not serialize concurrent admissions so the bound
// was overrun, or the bounded fail-closed window did not eventually fail
// closed against a sustained outage.
func TestSlotCounterRedisOutagePostgresFallback_spec_12_4(t *testing.T) {
	ctx := context.Background()

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	seedTenant(t, pg, "acme")
	store := sessionpg.New(pg.Pool)

	rd := containers.StartRedis(t, containers.RedisOptions{})

	// A test clock so the bounded fail-closed window is exercised without a
	// wall-clock sleep. The window is 60s; the clock advances past it on
	// demand.
	now := time.Now()
	clk := &advanceableClock{t: now}
	counter := slotcounter.New(rd.Client,
		slotcounter.WithSlotSource(store),
		slotcounter.WithFallbackSource(store),
		slotcounter.WithFallbackMaxWindow(60*time.Second),
		slotcounter.WithClockForTest(clk.now))

	const pod = "pod-outage-1"
	const maxConcurrent = 4

	// Fast path: Redis is healthy, so the reservation increments the Redis
	// counter. The rehydrated flag is absent on a fresh counter, so the first
	// reservation seeds from Postgres (zero live sessions) and admits.
	if count, _, err := counter.Reserve(ctx, pod, maxConcurrent); err != nil {
		t.Fatalf("Reserve on the Redis fast path: %v", err)
	} else if count != 1 {
		t.Fatalf("fast-path count = %d, want 1", count)
	}

	// Inject the Redis outage: stop the Redis container. Every subsequent
	// Reserve observes Redis as unreachable and routes to the Postgres gate.
	rd.Stop(t)

	// Seed two live sessions bound to the pod in Postgres so the fallback gate
	// reads a non-zero occupancy from the same source the rehydration path
	// reads. ReserveSlotUnderLock counts these live rows.
	mustSeedSlotSession(t, ctx, store, "acme", pod)
	mustSeedSlotSession(t, ctx, store, "acme", pod)

	// Degraded path: the Redis-only outage must not reject all dispatch. The
	// gate admits via Postgres (2 live + this admission = 3, below the bound).
	count, _, err := counter.Reserve(ctx, pod, maxConcurrent)
	if err != nil {
		t.Fatalf("Reserve during the Redis outage must degrade to the Postgres fallback, got: %v", err)
	}
	if count != 3 {
		t.Errorf("fallback count = %d, want 3 (2 live + this admission)", count)
	}

	// Concurrent dispatch during the outage on a pod with one free slot left:
	// the per-pod advisory lock must serialize the count-and-decide so at most
	// one of the racers wins the last slot. Each winner persists its session
	// row so the next gated count observes the committed occupancy.
	t.Run("concurrent dispatch serializes under the per-pod advisory lock", func(t *testing.T) {
		const racerPod = "pod-outage-race"
		// Three live sessions on a bound of 4 leave exactly one free slot.
		mustSeedSlotSession(t, ctx, store, "acme", racerPod)
		mustSeedSlotSession(t, ctx, store, "acme", racerPod)
		mustSeedSlotSession(t, ctx, store, "acme", racerPod)

		const racers = 8
		var admitted int32
		var wg sync.WaitGroup
		wg.Add(racers)
		for i := 0; i < racers; i++ {
			go func() {
				defer wg.Done()
				c, _, rErr := counter.Reserve(ctx, racerPod, maxConcurrent)
				if rErr != nil {
					return // ErrSlotsExhausted once the bound is reached.
				}
				if c >= 1 {
					mustSeedSlotSession(t, ctx, store, "acme", racerPod)
					atomic.AddInt32(&admitted, 1)
				}
			}()
		}
		wg.Wait()
		if got := atomic.LoadInt32(&admitted); got != 1 {
			t.Errorf("admitted = %d, want exactly 1; the per-pod advisory lock must serialize the count-and-decide so the bound is not overrun during the outage", got)
		}
	})

	// Bounded fail-closed window: a sustained Redis outage cannot keep gating
	// on Postgres latency forever. Advance the clock past the 60s window; the
	// next reservation fails closed.
	t.Run("fails closed after the bounded outage window", func(t *testing.T) {
		clk.advance(61 * time.Second)
		_, _, fErr := counter.Reserve(ctx, "pod-window", maxConcurrent)
		if fErr == nil {
			t.Fatal("Reserve after the bounded outage window must fail closed, got nil")
		}
	})
}

// mustSeedSlotSession persists a live (running) session row bound to pod so
// the Postgres fallback gate counts it. The non-terminal predicate in
// GetActiveSlotsByPod / ReserveSlotUnderLock matches the running state.
func mustSeedSlotSession(t *testing.T, ctx context.Context, store *sessionpg.Store, tenant, pod string) {
	t.Helper()
	if err := store.Create(ctx, sessionstore.Session{
		ID:            newSessionUUID(t),
		TenantID:      tenant,
		State:         session.StateRunning,
		RuntimeRef:    "echo",
		PodAssignment: pod,
	}); err != nil {
		t.Fatalf("seed slot session on %s: %v", pod, err)
	}
}

// advanceableClock is a test wall clock the §12.4 outage-window measurement
// uses so the bounded fail-closed window is exercised deterministically.
type advanceableClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *advanceableClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *advanceableClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}
