// SPDX-License-Identifier: MIT

package opsservice_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
	"github.com/lennylabs/lenny/pkg/ops/opsservice"
)

// TestSubscriptionCacheAttachesSecretAndGeneration is the §25.5 lines
// 2715-2756 contract: the cache attaches each subscription's plaintext
// signing secret from the reveal cache and tracks its generation for the
// per-delivery freshness check.
func TestSubscriptionCacheAttachesSecretAndGeneration_spec_25_5_2751(t *testing.T) {
	store := &stubStore{rows: []eventsubscription.Record{
		{ID: "sub-1", CallbackURL: "https://h", Active: true, Generation: 4},
	}}
	secrets := opsservice.NewSecretCache()
	secrets.Put("sub-1", "whsec_live", 4)

	cache := opsservice.NewSubscriptionCache(context.Background(), opsservice.SubscriptionCacheConfig{
		Store: store, Secrets: secrets, RefreshInterval: time.Hour,
	})
	defer cache.Stop()

	subs := cache.Subscriptions()
	if len(subs) != 1 {
		t.Fatalf("subs = %d, want 1", len(subs))
	}
	if string(subs[0].Secret) != "whsec_live" {
		t.Errorf("secret = %q, want whsec_live", subs[0].Secret)
	}
	if subs[0].Generation != 4 {
		t.Errorf("generation = %d, want 4", subs[0].Generation)
	}
	// The generation checker accepts the live generation and rejects a
	// stale one or an unknown id.
	if !cache.Current("sub-1", 4) {
		t.Error("Current rejected the live generation")
	}
	if cache.Current("sub-1", 3) {
		t.Error("Current accepted a stale generation")
	}
	if cache.Current("absent", 0) {
		t.Error("Current accepted an unknown subscription")
	}
}

// TestSubscriptionCacheColdStartSignalsUnavailable is the §25.5 line 2753
// contract: a cache that cannot reach the store on startup signals
// subscriptionsUnavailable, and a later successful refresh signals
// recovery.
func TestSubscriptionCacheColdStartSignalsUnavailable_spec_25_5_2753(t *testing.T) {
	store := &stubStore{listErr: errors.New("postgres down")}

	var mu sync.Mutex
	var transitions []bool
	onAvail := func(available bool) {
		mu.Lock()
		defer mu.Unlock()
		transitions = append(transitions, available)
	}

	cache := opsservice.NewSubscriptionCache(context.Background(), opsservice.SubscriptionCacheConfig{
		Store: store, RefreshInterval: time.Hour, OnAvailabilityChange: onAvail,
	})
	defer cache.Stop()

	if cache.Available() {
		t.Error("Available() true after a failed cold start")
	}
	// Postgres recovers; an explicit refresh flips availability.
	store.listErr = nil
	store.setRows([]eventsubscription.Record{{ID: "sub-1", CallbackURL: "https://h", Active: true}})
	if err := cache.Invalidate(context.Background()); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if !cache.Available() {
		t.Error("Available() false after recovery")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(transitions) != 2 || transitions[0] != false || transitions[1] != true {
		t.Errorf("availability transitions = %v, want [false true]", transitions)
	}
}

// TestSubscriptionCacheHealthyStartIsQuiet confirms a cache that loads
// cleanly on startup still reports the cold-start determination once
// (available=true) so a recovery is distinguishable, but does not flap.
func TestSubscriptionCacheHealthyStartIsQuiet(t *testing.T) {
	store := &stubStore{rows: []eventsubscription.Record{
		{ID: "sub-1", CallbackURL: "https://h", Active: true},
	}}
	var mu sync.Mutex
	var transitions []bool
	cache := opsservice.NewSubscriptionCache(context.Background(), opsservice.SubscriptionCacheConfig{
		Store: store, RefreshInterval: time.Hour,
		OnAvailabilityChange: func(a bool) { mu.Lock(); transitions = append(transitions, a); mu.Unlock() },
	})
	defer cache.Stop()
	// A second successful refresh must not re-fire the callback.
	_ = cache.Invalidate(context.Background())
	mu.Lock()
	defer mu.Unlock()
	if len(transitions) != 1 || transitions[0] != true {
		t.Errorf("transitions = %v, want a single [true]", transitions)
	}
}

// TestSubscriptionCachePrunesSecretsOnRefresh confirms the cache asks the
// reveal cache to retain only the active subscriptions' secrets after a
// refresh. spec: §25.5 line 2752.
func TestSubscriptionCachePrunesSecretsOnRefresh(t *testing.T) {
	store := &stubStore{rows: []eventsubscription.Record{
		{ID: "keep", CallbackURL: "https://h", Active: true},
	}}
	secrets := opsservice.NewSecretCache()
	secrets.Put("keep", "a", 0)
	secrets.Put("gone", "b", 0)

	cache := opsservice.NewSubscriptionCache(context.Background(), opsservice.SubscriptionCacheConfig{
		Store: store, Secrets: secrets, RefreshInterval: time.Hour,
	})
	defer cache.Stop()

	if _, ok := secrets.Secret("gone"); ok {
		t.Error("refresh did not prune the secret for a subscription no longer present")
	}
	if _, ok := secrets.Secret("keep"); !ok {
		t.Error("refresh pruned an active subscription's secret")
	}
}
