// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §12.4 mandated TestRedisTenantKeyIsolation,
// exercising the Redis wrapper-layer Guard against the real DLQ, durable
// inbox, semantic cache, and EventBus stores over a miniredis backend.

package tier4_integration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/semanticcache"
	scredis "github.com/lennylabs/lenny/pkg/gateway/semanticcache/redisstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/storage/eventbus"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pubsub"
	"github.com/lennylabs/lenny/pkg/gateway/storage/rediskeys"
)

// guardedRedis returns a miniredis-backed client with the §12.4 Guard
// hook installed, mirroring the wrapper layer NewSingleShardRouter wires
// onto the shared client in production.
func guardedRedis(t *testing.T) redis.UniversalClient {
	t.Helper()
	mr := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	cl.AddHook(rediskeys.NewGuard())
	t.Cleanup(func() { _ = cl.Close() })
	return cl
}

// TestRedisTenantKeyIsolation is the §12.4 line 195 mandated integration
// test. It verifies that operations scoped to one tenant cannot read or
// mutate keys belonging to another tenant. The spec requires coverage of
// DLQ (a, b), durable inbox (c), semantic cache (d), delegation budget
// (e), and EventBus (f); each is a subtest below.
//
// spec: §12.4 line 195.
// diagnosis: a failure means Redis key namespacing does not isolate
// tenants, so one tenant could read or mutate another tenant's DLQ,
// inbox, cache, delegation-budget, or EventBus keys.
func TestRedisTenantKeyIsolation(t *testing.T) {
	const (
		tenantA = "acme"
		tenantB = "globex"
		session = "sess-1"
	)
	ctxA := func() context.Context {
		return rediskeys.WithScope(context.Background(), rediskeys.TenantScope(tenantA))
	}
	ctxB := func() context.Context {
		return rediskeys.WithScope(context.Background(), rediskeys.TenantScope(tenantB))
	}

	// (a) A DLQ write for tenant A must not be readable by a DLQ
	//     processor scoped to tenant B (the read of A's key is rejected).
	t.Run("a_dlq_cross_tenant_read_rejected", func(t *testing.T) {
		cl := guardedRedis(t)
		dlq := sessioninbox.NewDLQ(cl, 100)
		msg := sessioninbox.Message{MessageID: "m1", Payload: []byte("x"), EnqueuedAt: time.Unix(1, 0)}
		if _, err := dlq.Enqueue(ctxA(), tenantA, session, msg, time.Hour); err != nil {
			t.Fatalf("tenant A enqueue: %v", err)
		}
		// Processor scoped to B reading A's DLQ key is rejected.
		if _, err := dlq.DrainAll(ctxB(), tenantA, session); !errors.Is(err, rediskeys.ErrCrossTenant) {
			t.Fatalf("cross-tenant DrainAll err = %v, want ErrCrossTenant", err)
		}
	})

	// (b) A DLQ processor performing a cross-tenant scan must return zero
	//     results when the key prefix belongs to a different tenant: B
	//     scanning its own (empty) key sees none of A's messages.
	t.Run("b_dlq_scan_scoped_returns_zero", func(t *testing.T) {
		cl := guardedRedis(t)
		dlq := sessioninbox.NewDLQ(cl, 100)
		msg := sessioninbox.Message{MessageID: "m1", Payload: []byte("x"), EnqueuedAt: time.Unix(1, 0)}
		if _, err := dlq.Enqueue(ctxA(), tenantA, session, msg, time.Hour); err != nil {
			t.Fatalf("tenant A enqueue: %v", err)
		}
		// B scans its own DLQ key (different prefix) — zero results.
		got, err := dlq.SweepExpired(ctxB(), tenantB, session, time.Unix(1<<30, 0))
		if err != nil {
			t.Fatalf("tenant B sweep: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("tenant B sweep returned %d of A's messages, want 0", len(got))
		}
	})

	// (c) A durable inbox enqueue for tenant A's session must not be
	//     visible to a coordinator scoped to tenant B.
	t.Run("c_inbox_cross_tenant_drain_rejected", func(t *testing.T) {
		cl := guardedRedis(t)
		inbox := sessioninbox.NewRedisInbox(cl, 100)
		msg := sessioninbox.Message{MessageID: "m1", Payload: []byte("x"), EnqueuedAt: time.Unix(1, 0)}
		if _, err := inbox.Enqueue(ctxA(), tenantA, session, msg); err != nil {
			t.Fatalf("tenant A enqueue: %v", err)
		}
		if _, err := inbox.Drain(ctxB(), tenantA, session); !errors.Is(err, rediskeys.ErrCrossTenant) {
			t.Fatalf("cross-tenant Drain err = %v, want ErrCrossTenant", err)
		}
		// B's own (empty) inbox sees none of A's messages.
		n, err := inbox.Len(ctxB(), tenantB, session)
		if err != nil {
			t.Fatalf("tenant B Len: %v", err)
		}
		if n != 0 {
			t.Fatalf("tenant B inbox holds %d of A's messages, want 0", n)
		}
	})

	// (d) A semantic cache write for tenant A must not produce a cache
	//     hit for a semantically identical query from tenant B.
	t.Run("d_semantic_cache_no_cross_tenant_hit", func(t *testing.T) {
		cl := guardedRedis(t)
		store := scredis.New(cl, nil, time.Hour, 0)
		const query = "what is the capital of france"
		keyA := semanticcache.Key{TenantID: tenantA, Scope: semanticcache.ScopeTenant, Model: "m", Provider: "p"}
		if err := store.Put(ctxA(), keyA, query, "paris"); err != nil {
			t.Fatalf("tenant A Put: %v", err)
		}
		// Identical query from tenant B must miss (its key prefix differs).
		keyB := semanticcache.Key{TenantID: tenantB, Scope: semanticcache.ScopeTenant, Model: "m", Provider: "p"}
		if _, hit, err := store.Get(ctxB(), keyB, query); err != nil {
			t.Fatalf("tenant B Get: %v", err)
		} else if hit {
			t.Fatal("tenant B observed a cross-tenant cache hit")
		}
		// And a B-scoped context reaching for A's key is rejected outright.
		if _, _, err := store.Get(ctxB(), keyA, query); !errors.Is(err, rediskeys.ErrCrossTenant) {
			t.Fatalf("cross-tenant Get err = %v, want ErrCrossTenant", err)
		}
	})

	// (e) A delegation budget operation for a root_session_id owned by
	//     tenant A must be rejected when the gateway context is scoped to
	//     tenant B. The §12.4 delegation keys carry no `t:{tenant_id}:`
	//     prefix; the compensating control is the application-layer
	//     ownership check the Guard performs on `{root}:dlg:` keys.
	//
	//     Full budget_reserve.lua / budget_return.lua script coverage
	//     lands with the delegation-budget Redis enforcement (F-12.4.8);
	//     this subtest exercises the ownership gate the scripts run under.
	t.Run("e_delegation_budget_ownership_enforced", func(t *testing.T) {
		const rootOwnedByA = "root-A"
		scopeB := rediskeys.DelegationScope(tenantB, "root-B")
		// budget_reserve target: B may not touch A's tree counters.
		if err := rediskeys.ValidateKey(scopeB, "{"+rootOwnedByA+"}:dlg:tokens"); !errors.Is(err, rediskeys.ErrDelegationOwnership) {
			t.Fatalf("budget_reserve ownership err = %v, want ErrDelegationOwnership", err)
		}
		// budget_return target: same rejection (distinct attack profile).
		if err := rediskeys.ValidateKey(scopeB, "{"+rootOwnedByA+"}:dlg:tree_memory"); !errors.Is(err, rediskeys.ErrDelegationOwnership) {
			t.Fatalf("budget_return ownership err = %v, want ErrDelegationOwnership", err)
		}
		// A scope that owns the root is admitted.
		scopeA := rediskeys.DelegationScope(tenantA, rootOwnedByA)
		if err := rediskeys.ValidateKey(scopeA, "{"+rootOwnedByA+"}:dlg:tokens"); err != nil {
			t.Fatalf("owner ValidateKey err = %v, want nil", err)
		}
	})

	// (f) An EventBus publish on tenant A's channel must not be issued by
	//     a publisher scoped to tenant B for the same topic. The Guard
	//     rejects the PUBLISH command before it reaches Redis.
	t.Run("f_eventbus_cross_tenant_publish_rejected", func(t *testing.T) {
		cl := guardedRedis(t)
		bus := eventbus.NewRedisEventBus(pubsub.New(cl), nil)
		ev, err := eventbus.NewEvent(eventbus.NewEventInput{
			TenantID: tenantA, PublisherID: "gw-1", ShortName: "x", Subject: "s/1",
			Data: json.RawMessage(`{}`),
		})
		if err != nil {
			t.Fatalf("NewEvent: %v", err)
		}
		// Publisher scoped to B emitting on tenant A's channel is rejected.
		perr := bus.Publish(ctxB(), tenantA, eventbus.TopicDelegationTree, ev)
		if perr == nil || !errors.Is(perr, rediskeys.ErrCrossTenant) {
			t.Fatalf("cross-tenant Publish err = %v, want ErrCrossTenant", perr)
		}
	})
}
