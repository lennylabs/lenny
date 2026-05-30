// SPDX-License-Identifier: MIT

package connectoroauth

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newRedisStateStoreT(t *testing.T) (*RedisStateStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewRedisStateStore(client), mr
}

func redisSampleFlow() FlowContext {
	return FlowContext{
		ConnectorID:  "github",
		TenantID:     "acme",
		UserID:       "alice@acme.com",
		Environment:  "prod",
		SessionID:    "sess-1",
		CodeVerifier: "verifier-xyz",
		RedirectURI:  "https://gw.acme.com/v1/connectors/oauth/callback",
		Scopes:       []string{"repo", "read:org"},
		CreatedAt:    time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC),
		InitiatorIP:  "203.0.113.7",
		InitiatorUA:  "curl/8.0",
	}
}

// spec: §9.3 line 157 — the Redis StateStore round-trips the per-flow
// PKCE context so a callback resolves its flow even on another replica.
func TestRedisStateStore_PutConsumeRoundTrip_spec_9_3_157(t *testing.T) {
	store, _ := newRedisStateStoreT(t)
	want := redisSampleFlow()
	if err := store.Put("state-1", want, DefaultStateTTL); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := store.Consume("state-1", time.Now())
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if got.ConnectorID != want.ConnectorID || got.TenantID != want.TenantID ||
		got.UserID != want.UserID || got.Environment != want.Environment ||
		got.SessionID != want.SessionID || got.CodeVerifier != want.CodeVerifier ||
		got.RedirectURI != want.RedirectURI || got.InitiatorIP != want.InitiatorIP ||
		got.InitiatorUA != want.InitiatorUA {
		t.Fatalf("round-trip mismatch: got %+v want %+v", got, want)
	}
	if len(got.Scopes) != 2 || got.Scopes[0] != "repo" || got.Scopes[1] != "read:org" {
		t.Fatalf("scopes round-trip mismatch: %v", got.Scopes)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("CreatedAt round-trip mismatch: got %v want %v", got.CreatedAt, want.CreatedAt)
	}
}

// spec: §9.3 line 157 — an unknown state (never stored) is rejected.
func TestRedisStateStore_ConsumeUnknown_spec_9_3_157(t *testing.T) {
	store, _ := newRedisStateStoreT(t)
	if _, err := store.Consume("never-stored", time.Now()); !errors.Is(err, ErrStateUnknown) {
		t.Fatalf("want ErrStateUnknown, got %v", err)
	}
}

// spec: §9.3 — single-use consumption: a replayed callback returns
// ErrStateConsumed, distinguishing a replay attempt from an unknown
// state in the audit trail.
func TestRedisStateStore_ReplayIsConsumed_spec_9_3_157(t *testing.T) {
	store, _ := newRedisStateStoreT(t)
	if err := store.Put("state-2", redisSampleFlow(), DefaultStateTTL); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := store.Consume("state-2", time.Now()); err != nil {
		t.Fatalf("first Consume: %v", err)
	}
	if _, err := store.Consume("state-2", time.Now()); !errors.Is(err, ErrStateConsumed) {
		t.Fatalf("replay: want ErrStateConsumed, got %v", err)
	}
}

// spec: §9.3 line 157 — TTL=10min via Redis native key expiry; an entry
// past its TTL is evicted and reads back as unknown.
func TestRedisStateStore_TTLExpiry_spec_9_3_157(t *testing.T) {
	store, mr := newRedisStateStoreT(t)
	if err := store.Put("state-3", redisSampleFlow(), DefaultStateTTL); err != nil {
		t.Fatalf("Put: %v", err)
	}
	mr.FastForward(DefaultStateTTL + time.Minute)
	if _, err := store.Consume("state-3", time.Now()); !errors.Is(err, ErrStateUnknown) {
		t.Fatalf("post-expiry: want ErrStateUnknown, got %v", err)
	}
}

// A second Put for the same state replaces the entry (MemoryStateStore
// parity).
func TestRedisStateStore_PutReplaces(t *testing.T) {
	store, _ := newRedisStateStoreT(t)
	first := redisSampleFlow()
	first.SessionID = "old"
	if err := store.Put("state-4", first, DefaultStateTTL); err != nil {
		t.Fatalf("Put first: %v", err)
	}
	second := redisSampleFlow()
	second.SessionID = "new"
	if err := store.Put("state-4", second, DefaultStateTTL); err != nil {
		t.Fatalf("Put second: %v", err)
	}
	got, err := store.Consume("state-4", time.Now())
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if got.SessionID != "new" {
		t.Fatalf("want replaced entry SessionID=new, got %q", got.SessionID)
	}
}

func TestRedisStateStore_PutEmptyStateKey(t *testing.T) {
	store, _ := newRedisStateStoreT(t)
	if err := store.Put("", redisSampleFlow(), DefaultStateTTL); err == nil {
		t.Fatal("want error for empty state key")
	}
}

func TestRedisStateStore_PutNonPositiveTTLDefaults(t *testing.T) {
	store, mr := newRedisStateStoreT(t)
	if err := store.Put("state-5", redisSampleFlow(), 0); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// Just shy of the default TTL the entry is still live.
	mr.FastForward(DefaultStateTTL - time.Minute)
	if _, err := store.Consume("state-5", time.Now()); err != nil {
		t.Fatalf("entry should be live before default TTL: %v", err)
	}
}

// spec: §9.3 — concurrent callbacks for the same state: the atomic
// tombstone swap admits exactly one and rejects the rest as replays.
func TestRedisStateStore_ConcurrentConsumeSingleWinner_spec_9_3_157(t *testing.T) {
	store, _ := newRedisStateStoreT(t)
	if err := store.Put("state-6", redisSampleFlow(), DefaultStateTTL); err != nil {
		t.Fatalf("Put: %v", err)
	}
	const n = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			if _, err := store.Consume("state-6", time.Now()); err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("want exactly one successful consume, got %d", successes)
	}
}
