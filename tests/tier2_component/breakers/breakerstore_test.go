//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §11.6 / §12.4 circuit-breaker registry,
// exercising the Redis-backed pkg/gateway/breakerstore/redisstore
// against a real Redis container. Covers open/close/get, the §11.6
// scope-immutability invariant, validation, and the List / Snapshot
// projections.
package breakers_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/circuitbreaker"
	"github.com/lennylabs/lenny/pkg/gateway/breakerstore"
	"github.com/lennylabs/lenny/pkg/gateway/breakerstore/redisstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

func randHex(t *testing.T) string {
	t.Helper()
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hex.EncodeToString(b[:])
}

// runtimeBreaker builds a valid §11.6 runtime-tier breaker.
func runtimeBreaker(name, runtime string) circuitbreaker.Breaker {
	return circuitbreaker.Breaker{
		Name:             name,
		State:            circuitbreaker.StateOpen,
		Reason:           "runaway runtime",
		OpenedBySub:      "ops@acme.com",
		OpenedByTenantID: "platform",
		LimitTier:        circuitbreaker.TierRuntime,
		Scope:            circuitbreaker.Scope{Runtime: runtime},
	}
}

func TestBreakerStoreContract(t *testing.T) {
	t.Parallel()
	rd := containers.StartRedis(t, containers.RedisOptions{})
	store := redisstore.New(rd.Client)
	ctx := context.Background()

	t.Run("open and get round-trip", func(t *testing.T) {
		name := "og-" + randHex(t)
		b := runtimeBreaker(name, "echo")
		b.OpenedAt = time.Now().UTC().Truncate(time.Second)
		opened, err := store.Open(ctx, b)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if opened.State != circuitbreaker.StateOpen {
			t.Errorf("Open returned state %q, want open", opened.State)
		}
		got, err := store.Get(ctx, name)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Reason != b.Reason || got.OpenedBySub != b.OpenedBySub ||
			got.LimitTier != b.LimitTier || got.Scope != b.Scope {
			t.Errorf("breaker mismatch:\n got %+v\nwant %+v", got, b)
		}
		if !got.OpenedAt.Equal(b.OpenedAt) {
			t.Errorf("OpenedAt mismatch: got %v want %v", got.OpenedAt, b.OpenedAt)
		}
	})

	t.Run("scope is immutable across opens", func(t *testing.T) {
		name := "imm-" + randHex(t)
		if _, err := store.Open(ctx, runtimeBreaker(name, "echo")); err != nil {
			t.Fatalf("first Open: %v", err)
		}
		// Re-opening with the same scope is allowed.
		if _, err := store.Open(ctx, runtimeBreaker(name, "echo")); err != nil {
			t.Errorf("re-Open with matching scope: %v", err)
		}
		// Re-opening with a different limit tier is rejected.
		poolScoped := circuitbreaker.Breaker{
			Name:      name,
			State:     circuitbreaker.StateOpen,
			LimitTier: circuitbreaker.TierPool,
			Scope:     circuitbreaker.Scope{Pool: "pool-1"},
		}
		if _, err := store.Open(ctx, poolScoped); !errors.Is(err, breakerstore.ErrScopeImmutable) {
			t.Errorf("Open with changed scope: got %v, want ErrScopeImmutable", err)
		}
	})

	t.Run("invalid breaker is rejected", func(t *testing.T) {
		// limit_tier=runtime with an empty scope fails §11.6 validation.
		bad := circuitbreaker.Breaker{
			Name:      "bad-" + randHex(t),
			State:     circuitbreaker.StateOpen,
			LimitTier: circuitbreaker.TierRuntime,
		}
		if _, err := store.Open(ctx, bad); err == nil {
			t.Error("Open of a breaker with an invalid scope should be rejected")
		}
	})

	t.Run("close transitions state and rejects missing", func(t *testing.T) {
		name := "cl-" + randHex(t)
		if _, err := store.Open(ctx, runtimeBreaker(name, "echo")); err != nil {
			t.Fatalf("Open: %v", err)
		}
		closed, err := store.Close(ctx, name)
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
		if closed.State != circuitbreaker.StateClosed {
			t.Errorf("Close returned state %q, want closed", closed.State)
		}
		got, _ := store.Get(ctx, name)
		if got.State != circuitbreaker.StateClosed {
			t.Errorf("persisted state %q, want closed", got.State)
		}
		if _, err := store.Close(ctx, "cl-absent-"+randHex(t)); !errors.Is(err, breakerstore.ErrNotFound) {
			t.Errorf("Close missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("get missing returns ErrNotFound", func(t *testing.T) {
		if _, err := store.Get(ctx, "absent-"+randHex(t)); !errors.Is(err, breakerstore.ErrNotFound) {
			t.Errorf("Get missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("list and snapshot project the registry", func(t *testing.T) {
		marker := "ls" + randHex(t)
		names := []string{marker + "-a", marker + "-b", marker + "-c"}
		for _, n := range names {
			if _, err := store.Open(ctx, runtimeBreaker(n, "echo")); err != nil {
				t.Fatalf("Open %s: %v", n, err)
			}
		}
		if _, err := store.Close(ctx, names[1]); err != nil {
			t.Fatalf("Close: %v", err)
		}
		marked := func(in []circuitbreaker.Breaker) []circuitbreaker.Breaker {
			var out []circuitbreaker.Breaker
			for _, b := range in {
				if strings.HasPrefix(b.Name, marker) {
					out = append(out, b)
				}
			}
			return out
		}
		all, err := store.List(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		listed := marked(all)
		if len(listed) != 3 || listed[0].Name != names[0] || listed[2].Name != names[2] {
			t.Fatalf("List: not name-ascending or wrong count: %d", len(listed))
		}
		snap, err := store.Snapshot(ctx)
		if err != nil {
			t.Fatalf("Snapshot: %v", err)
		}
		open := marked(snap)
		if len(open) != 2 {
			t.Errorf("Snapshot: %d open breakers, want 2 (the closed one excluded)", len(open))
		}
		for _, b := range open {
			if b.State != circuitbreaker.StateOpen {
				t.Errorf("Snapshot returned a %q breaker", b.State)
			}
		}
	})
}
