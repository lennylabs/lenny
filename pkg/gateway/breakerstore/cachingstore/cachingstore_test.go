// SPDX-License-Identifier: MIT

package cachingstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/circuitbreaker"
	"github.com/lennylabs/lenny/pkg/gateway/breakerstore/cachingstore"
)

// fakeRegistry is an in-test cachingstore.Registry. Snapshot returns
// snapErr when set so the stale-retention path can be exercised
// without a Redis container.
type fakeRegistry struct {
	snapshot []circuitbreaker.Breaker
	snapErr  error
}

func (f *fakeRegistry) Open(_ context.Context, b circuitbreaker.Breaker) (circuitbreaker.Breaker, error) {
	return b, nil
}

func (f *fakeRegistry) Close(_ context.Context, name string) (circuitbreaker.Breaker, error) {
	return circuitbreaker.Breaker{Name: name}, nil
}

func (f *fakeRegistry) Get(_ context.Context, name string) (circuitbreaker.Breaker, error) {
	return circuitbreaker.Breaker{Name: name}, nil
}

func (f *fakeRegistry) List(_ context.Context) ([]circuitbreaker.Breaker, error) {
	return f.snapshot, nil
}

func (f *fakeRegistry) Snapshot(_ context.Context) ([]circuitbreaker.Breaker, error) {
	if f.snapErr != nil {
		return nil, f.snapErr
	}
	return f.snapshot, nil
}

func openBreaker(name string) circuitbreaker.Breaker {
	return circuitbreaker.Breaker{Name: name, State: circuitbreaker.StateOpen}
}

func TestRefreshPopulatesCache(t *testing.T) {
	reg := &fakeRegistry{snapshot: []circuitbreaker.Breaker{openBreaker("rt-emergency")}}
	store := cachingstore.New(reg, nil)
	if err := store.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	got, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(got) != 1 || got[0].Name != "rt-emergency" {
		t.Errorf("Snapshot: got %+v, want [rt-emergency]", got)
	}
}

func TestSnapshotIsEmptyBeforeRefresh(t *testing.T) {
	store := cachingstore.New(&fakeRegistry{}, nil)
	got, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Snapshot before Refresh: got %+v, want empty", got)
	}
}

func TestSnapshotReturnsACopy(t *testing.T) {
	reg := &fakeRegistry{snapshot: []circuitbreaker.Breaker{openBreaker("rt-a")}}
	store := cachingstore.New(reg, nil)
	if err := store.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	first, _ := store.Snapshot(context.Background())
	first[0].Name = "mutated"
	second, _ := store.Snapshot(context.Background())
	if second[0].Name != "rt-a" {
		t.Errorf("Snapshot returned an aliased slice: caller mutation leaked as %q", second[0].Name)
	}
}

func TestRefreshErrorRetainsStaleCache(t *testing.T) {
	reg := &fakeRegistry{snapshot: []circuitbreaker.Breaker{openBreaker("rt-stale")}}
	store := cachingstore.New(reg, nil)
	if err := store.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	// Simulate a Redis outage: the next refresh fails.
	reg.snapErr = errors.New("redis: connection refused")
	if err := store.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh should surface the registry error")
	}
	got, _ := store.Snapshot(context.Background())
	if len(got) != 1 || got[0].Name != "rt-stale" {
		t.Errorf("a failed refresh must retain the prior snapshot, got %+v", got)
	}
}

func TestLastRefreshAdvancesOnRefresh(t *testing.T) {
	store := cachingstore.New(&fakeRegistry{}, nil)
	if !store.LastRefresh().IsZero() {
		t.Error("LastRefresh should be zero before the first Refresh")
	}
	if err := store.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if store.LastRefresh().IsZero() {
		t.Error("LastRefresh should advance after a successful Refresh")
	}
}
