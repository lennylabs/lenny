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

// stubStore is a recording eventsubscription.Store backed by an
// in-memory slice. The cache test uses it to control what each List
// call returns.
type stubStore struct {
	mu      sync.Mutex
	calls   int
	rows    []eventsubscription.Record
	listErr error
}

func (s *stubStore) Create(_ context.Context, _ eventsubscription.Record) error {
	return errors.New("stub: Create not implemented")
}

func (s *stubStore) Get(_ context.Context, _ string) (eventsubscription.Record, error) {
	return eventsubscription.Record{}, errors.New("stub: Get not implemented")
}

func (s *stubStore) List(_ context.Context) ([]eventsubscription.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := make([]eventsubscription.Record, len(s.rows))
	copy(out, s.rows)
	return out, nil
}

func (s *stubStore) Update(_ context.Context, _ string, _ func(*eventsubscription.Record) error) (eventsubscription.Record, error) {
	return eventsubscription.Record{}, errors.New("stub: Update not implemented")
}

func (s *stubStore) Delete(_ context.Context, _ string) (eventsubscription.Record, error) {
	return eventsubscription.Record{}, errors.New("stub: Delete not implemented")
}

func (s *stubStore) RecordDelivery(_ context.Context, d eventsubscription.Delivery) (eventsubscription.Delivery, error) {
	return d, nil
}

func (s *stubStore) ListDeliveries(_ context.Context, _ string, _ int) ([]eventsubscription.Delivery, error) {
	return nil, nil
}

func (s *stubStore) setRows(rows []eventsubscription.Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = rows
}

// spec: 25.5
// diagnosis: the SubscriptionCache did not bridge the request-scoped
// eventsubscription.Store API to the synchronous webhookloop
// SubscriptionSource interface. The cache must do an initial
// synchronous load, expose the cached set without holding the Store
// open, and survive Store outages without dropping the last known
// subscription set.
func TestSubscriptionCacheInitialLoad(t *testing.T) {
	store := &stubStore{rows: []eventsubscription.Record{
		{ID: "sub_a", CallbackURL: "https://acme.example/a", Types: []string{"x"}, Active: true},
	}}
	cache := opsservice.NewSubscriptionCache(context.Background(), opsservice.SubscriptionCacheConfig{
		Store:           store,
		RefreshInterval: time.Hour,
	})
	defer cache.Stop()
	got := cache.Subscriptions()
	if len(got) != 1 || got[0].ID != "sub_a" {
		t.Fatalf("Subscriptions: got %+v, want one row sub_a", got)
	}
	// §25.5 stores the secret as a hash at rest; the store-backed reload
	// carries no plaintext signing key (F-25.5.13 in-memory reveal cache).
	if len(got[0].Secret) != 0 {
		t.Errorf("Secret: got %q, want empty (hash-at-rest)", got[0].Secret)
	}
}

// spec: §25.5 line 2645 — an inactive subscription is excluded from the
// delivery set the worker reads.
func TestSubscriptionCacheSkipsInactive(t *testing.T) {
	store := &stubStore{rows: []eventsubscription.Record{
		{ID: "sub_a", CallbackURL: "https://acme.example/a", Active: true},
		{ID: "sub_b", CallbackURL: "https://acme.example/b", Active: false},
	}}
	cache := opsservice.NewSubscriptionCache(context.Background(), opsservice.SubscriptionCacheConfig{
		Store: store, RefreshInterval: time.Hour,
	})
	defer cache.Stop()
	got := cache.Subscriptions()
	if len(got) != 1 || got[0].ID != "sub_a" {
		t.Fatalf("Subscriptions: got %+v, want only active sub_a", got)
	}
}

func TestSubscriptionCacheReturnsDefensiveCopy(t *testing.T) {
	store := &stubStore{rows: []eventsubscription.Record{
		{ID: "sub_a", CallbackURL: "https://acme.example/a", Types: []string{"x"}, Active: true},
	}}
	cache := opsservice.NewSubscriptionCache(context.Background(), opsservice.SubscriptionCacheConfig{
		Store:           store,
		RefreshInterval: time.Hour,
	})
	defer cache.Stop()
	a := cache.Subscriptions()
	a[0].ID = "tampered"
	b := cache.Subscriptions()
	if b[0].ID != "sub_a" {
		t.Errorf("cache is mutable: %+v", b)
	}
}

func TestSubscriptionCacheRefreshUpdates(t *testing.T) {
	store := &stubStore{}
	cache := opsservice.NewSubscriptionCache(context.Background(), opsservice.SubscriptionCacheConfig{
		Store:           store,
		RefreshInterval: time.Hour,
	})
	defer cache.Stop()
	if len(cache.Subscriptions()) != 0 {
		t.Fatal("initial load: should be empty")
	}
	store.setRows([]eventsubscription.Record{
		{ID: "sub_a", CallbackURL: "https://acme.example/a", Active: true},
		{ID: "sub_b", CallbackURL: "https://acme.example/b", Active: true},
	})
	if err := cache.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	got := cache.Subscriptions()
	if len(got) != 2 {
		t.Errorf("after refresh: got %d rows, want 2", len(got))
	}
}

func TestSubscriptionCacheSurvivesListError(t *testing.T) {
	store := &stubStore{rows: []eventsubscription.Record{
		{ID: "sub_a", CallbackURL: "https://acme.example/a", Active: true},
	}}
	cache := opsservice.NewSubscriptionCache(context.Background(), opsservice.SubscriptionCacheConfig{
		Store:           store,
		RefreshInterval: time.Hour,
	})
	defer cache.Stop()
	store.mu.Lock()
	store.listErr = errors.New("postgres unreachable")
	store.mu.Unlock()
	if err := cache.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh: expected error")
	}
	// The cache still has the last known set.
	got := cache.Subscriptions()
	if len(got) != 1 || got[0].ID != "sub_a" {
		t.Errorf("cache lost prior set on error: %+v", got)
	}
}

func TestSubscriptionCacheStopIsIdempotent(t *testing.T) {
	store := &stubStore{}
	cache := opsservice.NewSubscriptionCache(context.Background(), opsservice.SubscriptionCacheConfig{
		Store: store, RefreshInterval: time.Hour,
	})
	cache.Stop()
	cache.Stop()
}
