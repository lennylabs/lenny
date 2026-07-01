// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos test for the §3.2 / §6.57 / §12.4 Redis-outage Postgres
// fallback behind the §5.2 intra-pod slot counter. It is the failure/recovery
// path the proposal names: a Redis outage injected mid-flight under concurrent
// dispatch, then a Redis recovery. The Kind e2e overlay's store-outage tests
// (store_failure_test.go::TestRedisClusterDegraded) assert the gateway process
// survives a Redis outage and the §25.3 health report flags Redis; this test
// drives the slot counter directly against real Redis and Postgres containers
// so the §12.4 fail-closed window and the post-recovery rehydration are
// exercised with an injected clock, the fault-injection layer the scaffolds
// note the Kind overlay does not provide.
package tier8_chaos_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: 3.2 (Redis slot counter intra-pod gate with Postgres fallback), 6.57
// (durable fallback for every Redis-backed role), 12.4 (Redis HA and failure
// modes, bounded fail-closed window).
// diagnosis: a failure means the §5.2 slot counter did not survive a Redis
// outage per §12.4. Either a Redis-only outage rejected all slot dispatch
// instead of degrading to the Postgres advisory-lock gate, concurrent
// admissions during the outage overran the per-pod bound, the bounded
// fail-closed window did not eventually fail closed against a sustained
// outage, or the counter did not resume the Redis fast path after Redis
// recovered (rehydrating its count from Postgres).
func TestSlotCounterSurvivesRedisOutageAndRecovers(t *testing.T) {
	ctx := context.Background()

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	seedChaosTenant(t, pg, "acme")
	store := sessionpg.New(pg.Pool)

	rd := containers.StartRedis(t, containers.RedisOptions{})

	clk := &chaosClock{t: time.Now()}
	counter := slotcounter.New(rd.Client,
		slotcounter.WithSlotSource(store),
		slotcounter.WithFallbackSource(store),
		slotcounter.WithFallbackMaxWindow(60*time.Second),
		slotcounter.WithClockForTest(clk.now))

	const pod = "pod-chaos-1"
	const maxConcurrent = 4

	// Healthy: the first reservation seeds the counter from Postgres (zero
	// live sessions) and admits via Redis.
	if c, _, err := counter.Reserve(ctx, pod, maxConcurrent); err != nil || c != 1 {
		t.Fatalf("healthy Reserve = (%d, %v), want (1, nil)", c, err)
	}

	// Inject: stop Redis. Every subsequent Reserve observes Redis unreachable.
	rd.Stop(t)

	// Two live slots persisted in Postgres on the pod so the fallback gate
	// reads a real occupancy; the gate admits the third (below the bound of 4).
	mustSeedChaosSlot(t, ctx, store, "acme", pod)
	mustSeedChaosSlot(t, ctx, store, "acme", pod)
	if c, _, err := counter.Reserve(ctx, pod, maxConcurrent); err != nil {
		t.Fatalf("Reserve during the Redis outage must degrade to Postgres, got: %v", err)
	} else if c != 3 {
		t.Errorf("outage fallback count = %d, want 3 (2 live + this admission)", c)
	}

	// Concurrent dispatch during the outage on a pod with one free slot: the
	// per-pod advisory lock serializes the count-and-decide so only one racer
	// wins the last slot.
	t.Run("concurrent dispatch under the outage does not overrun the bound", func(t *testing.T) {
		const racePod = "pod-chaos-race"
		mustSeedChaosSlot(t, ctx, store, "acme", racePod)
		mustSeedChaosSlot(t, ctx, store, "acme", racePod)
		mustSeedChaosSlot(t, ctx, store, "acme", racePod)
		const racers = 12
		var admitted int32
		var wg sync.WaitGroup
		wg.Add(racers)
		for i := 0; i < racers; i++ {
			go func() {
				defer wg.Done()
				c, _, err := counter.Reserve(ctx, racePod, maxConcurrent)
				if err != nil {
					return
				}
				if c >= 1 {
					mustSeedChaosSlot(t, ctx, store, "acme", racePod)
					atomic.AddInt32(&admitted, 1)
				}
			}()
		}
		wg.Wait()
		if got := atomic.LoadInt32(&admitted); got != 1 {
			t.Errorf("admitted = %d during the outage, want exactly 1 (the advisory lock must serialize the gate)", got)
		}
	})

	// Bounded fail-closed window: advance the clock past 60s with Redis still
	// down; the gate fails closed.
	t.Run("fails closed after the bounded outage window", func(t *testing.T) {
		clk.advance(61 * time.Second)
		if _, _, err := counter.Reserve(ctx, "pod-chaos-window", maxConcurrent); err == nil {
			t.Fatal("Reserve after the bounded outage window must fail closed, got nil")
		}
	})

	// Recover: bring Redis back. The counter resumes the Redis fast path,
	// rehydrating the pod's count from Postgres on the first post-recovery
	// reservation so the stale-zero counter does not over-admit. The
	// recovered pod (`pod-recovered`) has one live session seeded in Postgres,
	// so the first post-recovery Reserve seeds the counter to 1 and admits
	// slot 2.
	rd2 := containers.StartRedis(t, containers.RedisOptions{})
	counter2 := slotcounter.New(rd2.Client,
		slotcounter.WithSlotSource(store),
		slotcounter.WithFallbackSource(store),
		slotcounter.WithClockForTest(clk.now))

	const recoveredPod = "pod-recovered"
	mustSeedChaosSlot(t, ctx, store, "acme", recoveredPod)
	c, rehydrated, err := counter2.Reserve(ctx, recoveredPod, maxConcurrent)
	if err != nil {
		t.Fatalf("post-recovery Reserve: %v", err)
	}
	if !rehydrated {
		t.Error("the first post-recovery reservation must rehydrate the counter from Postgres")
	}
	if c != 2 {
		t.Errorf("post-recovery count = %d, want 2 (1 live seeded in Postgres + this admission)", c)
	}
	// A further admission past the bound on the recovered fast path is
	// rejected with ErrSlotsExhausted, confirming the Redis cap enforcement
	// resumed.
	mustSeedChaosSlot(t, ctx, store, "acme", recoveredPod) // now 2 live + 1 reserved.
	if _, _, err := counter2.Reserve(ctx, recoveredPod, 2); !errors.Is(err, slotcounter.ErrSlotsExhausted) {
		t.Errorf("post-recovery over-bound Reserve = %v, want ErrSlotsExhausted (Redis cap enforcement resumed)", err)
	}
}

// seedChaosTenant inserts a tenant registry row for the chaos slot test.
func seedChaosTenant(t *testing.T, pg *containers.Postgres, id string) {
	t.Helper()
	if _, err := pg.Pool.Exec(context.Background(),
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')
		 ON CONFLICT (id) DO NOTHING`, id); err != nil {
		t.Fatalf("seed tenant %q: %v", id, err)
	}
}

// mustSeedChaosSlot persists a live (running) session row bound to pod so the
// Postgres fallback / rehydration count observes it.
func mustSeedChaosSlot(t *testing.T, ctx context.Context, store *sessionpg.Store, tenant, pod string) {
	t.Helper()
	if err := store.Create(ctx, sessionstore.Session{
		ID:            newChaosUUID(t),
		TenantID:      tenant,
		State:         session.StateRunning,
		RuntimeRef:    "echo",
		PodAssignment: pod,
	}); err != nil {
		t.Fatalf("seed slot session on %s: %v", pod, err)
	}
}

// newChaosUUID returns a fresh random UUIDv4 string for a session ID.
func newChaosUUID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// chaosClock is a test wall clock the §12.4 outage-window measurement uses so
// the bounded fail-closed window is exercised without a wall-clock sleep.
type chaosClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *chaosClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *chaosClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}
