// SPDX-License-Identifier: MIT

package experimentsticky

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newCacheT(t *testing.T, opts ...Option) (*RedisCache, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewRedis(client, opts...), mr
}

// spec: §12.4 — the sticky-cache key is
// `t:{tenant_id}:exp:{experiment_id}:sticky:{user_id}`.
func TestKeyFormat_spec_12_4(t *testing.T) {
	got := assignmentKey("acme", "exp-1", "alice@acme.com")
	want := "t:acme:exp:exp-1:sticky:alice@acme.com"
	if got != want {
		t.Fatalf("assignmentKey = %q, want %q", got, want)
	}
	gotPat := flushPattern("acme", "exp-1")
	wantPat := "t:acme:exp:exp-1:sticky:*"
	if gotPat != wantPat {
		t.Fatalf("flushPattern = %q, want %q", gotPat, wantPat)
	}
}

// spec: §10.7 line 831 — a cached assignment is read back so the provider is
// not re-evaluated.
func TestPutGetRoundTrip_spec_10_7(t *testing.T) {
	c, _ := newCacheT(t)
	ctx := context.Background()
	if _, ok, err := c.Get(ctx, "acme", "exp-1", "alice"); err != nil || ok {
		t.Fatalf("miss expected on empty cache: ok=%v err=%v", ok, err)
	}
	if err := c.Put(ctx, "acme", "exp-1", "alice", "variant-b"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	v, ok, err := c.Get(ctx, "acme", "exp-1", "alice")
	if err != nil || !ok || v != "variant-b" {
		t.Fatalf("Get = (%q,%v,%v), want (variant-b,true,nil)", v, ok, err)
	}
}

// spec: §10.7 — assignments are scoped per (tenant, experiment, user); a
// different user, experiment, or tenant does not collide.
func TestGetIsScoped(t *testing.T) {
	c, _ := newCacheT(t)
	ctx := context.Background()
	if err := c.Put(ctx, "acme", "exp-1", "alice", "variant-b"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	for _, tc := range []struct{ tenant, exp, user string }{
		{"globex", "exp-1", "alice"},
		{"acme", "exp-2", "alice"},
		{"acme", "exp-1", "bob"},
	} {
		if _, ok, _ := c.Get(ctx, tc.tenant, tc.exp, tc.user); ok {
			t.Fatalf("unexpected hit for %v", tc)
		}
	}
}

// spec: §10.7 line 1096 — flush DELs all keys matching
// `t:{tenant}:exp:{exp}:sticky:*` and only that experiment's keys.
func TestFlushDeletesOnlyExperimentKeys_spec_10_7(t *testing.T) {
	rec := &countingRecorder{}
	c, _ := newCacheT(t, WithInvalidationRecorder(rec))
	ctx := context.Background()
	mustPut(t, c, "acme", "exp-1", "alice", "a")
	mustPut(t, c, "acme", "exp-1", "bob", "b")
	mustPut(t, c, "acme", "exp-2", "alice", "c") // different experiment, survives
	mustPut(t, c, "globex", "exp-1", "alice", "d") // different tenant, survives

	n, err := c.Flush(ctx, "acme", "exp-1", "paused")
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if n != 2 {
		t.Fatalf("deleted = %d, want 2", n)
	}
	if _, ok, _ := c.Get(ctx, "acme", "exp-1", "alice"); ok {
		t.Fatal("exp-1/alice should be flushed")
	}
	if _, ok, _ := c.Get(ctx, "acme", "exp-1", "bob"); ok {
		t.Fatal("exp-1/bob should be flushed")
	}
	if _, ok, _ := c.Get(ctx, "acme", "exp-2", "alice"); !ok {
		t.Fatal("exp-2/alice must survive the exp-1 flush")
	}
	if _, ok, _ := c.Get(ctx, "globex", "exp-1", "alice"); !ok {
		t.Fatal("globex/exp-1/alice must survive the acme flush")
	}
}

// spec: §10.7 line 1096 — the invalidation counter is incremented on each
// flush, even when the experiment has no cached users.
func TestFlushRecordsInvalidationEvenWhenEmpty_spec_10_7(t *testing.T) {
	rec := &countingRecorder{}
	c, _ := newCacheT(t, WithInvalidationRecorder(rec))
	n, err := c.Flush(context.Background(), "acme", "exp-empty", "concluded")
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if n != 0 {
		t.Fatalf("deleted = %d, want 0", n)
	}
	if rec.count != 1 {
		t.Fatalf("invalidations recorded = %d, want 1", rec.count)
	}
	if rec.lastExp != "exp-empty" || rec.lastTransition != "concluded" {
		t.Fatalf("recorded (exp=%q, transition=%q), want (exp-empty, concluded)", rec.lastExp, rec.lastTransition)
	}
}

// spec: §12.4 failure behavior — on Redis unavailability Get/Put surface the
// error so the caller falls open to fresh evaluation.
func TestRedisOutageSurfacesError_spec_12_4(t *testing.T) {
	c, mr := newCacheT(t)
	ctx := context.Background()
	mr.Close() // simulate the Redis outage window
	if _, ok, err := c.Get(ctx, "acme", "exp-1", "alice"); err == nil || ok {
		t.Fatalf("Get during outage: ok=%v err=%v, want a non-nil error", ok, err)
	}
	if err := c.Put(ctx, "acme", "exp-1", "alice", "b"); err == nil {
		t.Fatal("Put during outage should surface the Redis error")
	}
	if _, err := c.Flush(ctx, "acme", "exp-1", "paused"); err == nil {
		t.Fatal("Flush during outage should surface the Redis error")
	}
}

// Empty key components must never reach Redis: a blank experiment id would
// make the flush glob match a broader keyspace, and a blank user/tenant
// produces a malformed key.
func TestEmptyComponentsRejected(t *testing.T) {
	c, _ := newCacheT(t)
	ctx := context.Background()
	if _, _, err := c.Get(ctx, "", "exp-1", "alice"); err == nil {
		t.Fatal("Get with empty tenant must error")
	}
	if err := c.Put(ctx, "acme", "", "alice", "b"); err == nil {
		t.Fatal("Put with empty experiment must error")
	}
	if _, err := c.Flush(ctx, "acme", "", "paused"); err == nil {
		t.Fatal("Flush with empty experiment must error")
	}
}

// spec: §10.7 — a cached assignment expires under the configured TTL so an
// experiment deleted without a clean transition cannot leak keys forever.
func TestTTLExpiry(t *testing.T) {
	c, mr := newCacheT(t, WithTTL(time.Minute))
	ctx := context.Background()
	mustPut(t, c, "acme", "exp-1", "alice", "b")
	if _, ok, _ := c.Get(ctx, "acme", "exp-1", "alice"); !ok {
		t.Fatal("expected hit before TTL elapses")
	}
	mr.FastForward(time.Minute + time.Second)
	if _, ok, _ := c.Get(ctx, "acme", "exp-1", "alice"); ok {
		t.Fatal("expected miss after TTL elapses")
	}
}

func mustPut(t *testing.T, c *RedisCache, tenant, exp, user, variant string) {
	t.Helper()
	if err := c.Put(context.Background(), tenant, exp, user, variant); err != nil {
		t.Fatalf("Put(%s,%s,%s): %v", tenant, exp, user, err)
	}
}

type countingRecorder struct {
	count          int
	lastExp        string
	lastTransition string
}

func (r *countingRecorder) RecordExperimentStickyCacheInvalidation(experimentID, transition string) {
	r.count++
	r.lastExp = experimentID
	r.lastTransition = transition
}
