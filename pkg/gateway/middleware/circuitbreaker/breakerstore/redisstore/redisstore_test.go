// SPDX-License-Identifier: MIT

package redisstore_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/circuitbreaker"
	"github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker/breakerstore/redisstore"
	"github.com/lennylabs/lenny/pkg/redisconn/clustertest"
	"github.com/lennylabs/lenny/pkg/redisconn/slotguard"
)

func runtimeBreaker(name string) circuitbreaker.Breaker {
	return circuitbreaker.Breaker{
		Name:             name,
		State:            circuitbreaker.StateOpen,
		Reason:           "runaway runtime",
		OpenedBySub:      "alice@acme.com",
		OpenedByTenantID: "platform",
		LimitTier:        circuitbreaker.TierRuntime,
		Scope:            circuitbreaker.Scope{Runtime: "echo"},
	}
}

// spec: §11.6 Circuit Breakers (Storage and propagation); §12.4 Redis HA
// and Failure Modes (Redis Cluster at Tier 3). The cb:{name} keys spread
// over every master of a cluster. Snapshot and List read every master, so
// a breaker opened on any master is seen by admission, and the reads use
// single-key GETs that the cluster client and the slot guard both accept.
func TestCollectSeesBreakersOnEveryMaster_spec_11_6(t *testing.T) {
	c := clustertest.New(t, 3)
	store := redisstore.New(c.Client)
	ctx := context.Background()

	const n = 24
	masters := map[string]bool{}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("breaker-%02d", i)
		if _, err := store.Open(ctx, runtimeBreaker(name)); err != nil {
			t.Fatalf("Open(%s): %v", name, err)
		}
		masters[c.ServerFor("cb:"+name).Addr()] = true
	}
	if len(masters) < 2 {
		t.Fatalf("fixture: breakers landed on %d master(s), want them spread", len(masters))
	}
	if _, err := store.Close(ctx, "breaker-00"); err != nil {
		t.Fatalf("Close: %v", err)
	}

	open, err := store.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(open) != n-1 {
		t.Fatalf("Snapshot = %d open breakers, want %d", len(open), n-1)
	}
	all, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != n || all[0].Name != "breaker-00" || all[0].State != circuitbreaker.StateClosed {
		t.Fatalf("List = %d breakers (first %+v), want %d sorted with breaker-00 closed", len(all), all[0], n)
	}
}

// vanishHook deletes one key from the server just before the first
// pipeline runs, modelling a breaker removed between the SCAN and the GETs.
type vanishHook struct {
	mr   *miniredis.Miniredis
	key  string
	done bool
}

func (h *vanishHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *vanishHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return next
}

func (h *vanishHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		if !h.done {
			h.done = true
			h.mr.Del(h.key)
		}
		return next(ctx, cmds)
	}
}

// spec: §11.6 Circuit Breakers (Storage and propagation). A breaker key
// that vanishes between the SCAN and the GET pipeline is skipped, and the
// remaining breakers are still returned; a transport failure surfaces as
// an error.
func TestCollectSkipsVanishedKeyAndSurfacesErrors_spec_11_6(t *testing.T) {
	mr := miniredis.RunT(t)
	cl := slotguard.Install(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	t.Cleanup(func() { _ = cl.Close() })
	store := redisstore.New(cl)
	ctx := context.Background()
	for _, name := range []string{"a", "b", "c"} {
		if _, err := store.Open(ctx, runtimeBreaker(name)); err != nil {
			t.Fatalf("Open(%s): %v", name, err)
		}
	}
	cl.AddHook(&vanishHook{mr: mr, key: "cb:b"})

	got, err := store.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "c" {
		t.Fatalf("Snapshot = %+v, want breakers a and c", got)
	}

	mr.Close()
	if _, err := store.List(ctx); err == nil {
		t.Fatal("List on a closed server returned nil error")
	}
}
