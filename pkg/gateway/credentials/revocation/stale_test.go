// SPDX-License-Identifier: MIT

package revocation

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
)

// staleSource is a revocation Source / TenantLister that returns a fixed
// set, or an error to simulate Postgres being unreachable.
type staleSource struct {
	err error
}

func (s staleSource) ListTenants(context.Context) ([]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []string{"acme"}, nil
}

func (s staleSource) ListRevoked(context.Context, string) ([]issuedtokenstore.IssuedToken, error) {
	if s.err != nil {
		return nil, s.err
	}
	return nil, nil
}

// spec: §13.3 line 601 — a replica that cannot reach Postgres refuses to
// validate tokens, keyed on the freshness of the in-memory revocation
// set. F-13.3.4.
func TestStale_F1334(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	c := NewCache(WithClock(clock), WithMaxStaleness(90*time.Second))

	// Before any successful rehydration the set is untrustworthy.
	if !c.Stale() {
		t.Fatal("a never-rehydrated cache must report stale (fail closed)")
	}

	// A successful rehydration marks the set fresh as of now.
	if err := c.Rehydrate(context.Background(), staleSource{}, staleSource{}); err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	if c.Stale() {
		t.Fatal("a just-rehydrated cache must report fresh")
	}

	// Within the freshness window it stays fresh.
	now = now.Add(89 * time.Second)
	if c.Stale() {
		t.Error("cache aged 89s with a 90s window must still be fresh")
	}

	// Past the window with no further success it goes stale.
	now = now.Add(2 * time.Second)
	if !c.Stale() {
		t.Error("cache aged 91s with a 90s window must report stale")
	}

	// A failed rehydration (Postgres unreachable) does not refresh the
	// timestamp, so the cache remains stale.
	if err := c.Rehydrate(context.Background(), staleSource{err: context.DeadlineExceeded}, staleSource{err: context.DeadlineExceeded}); err == nil {
		t.Fatal("Rehydrate with an unreachable source must return an error")
	}
	if !c.Stale() {
		t.Error("a failed rehydration must not refresh the freshness window")
	}

	// A subsequent success clears the staleness.
	if err := c.Rehydrate(context.Background(), staleSource{}, staleSource{}); err != nil {
		t.Fatalf("recovery Rehydrate: %v", err)
	}
	if c.Stale() {
		t.Error("a recovered cache must report fresh again")
	}
}
