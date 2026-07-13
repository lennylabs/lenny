// SPDX-License-Identifier: MIT

package redisstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/ops/escalation"
	"github.com/lennylabs/lenny/pkg/ops/escalation/redisstore"
)

func newStore(t *testing.T) (*redisstore.Store, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = cl.Close() })
	return redisstore.New(cl), mr
}

func sample(id, sev, status string, created time.Time) escalation.Escalation {
	return escalation.Escalation{
		ID: id, Severity: sev, Source: "watchdog", Summary: "s",
		Status: status, Persistence: escalation.PersistenceDurableRedis,
		CreatedAt: created, UpdatedAt: created,
	}
}

// TestRedisStoreRoundTrip exercises the §25.4 Tier 2 put/get/list/update
// lifecycle against a miniredis backend, including the 24h TTL on the
// ops:escalations:{id} key.
// spec: §25.4 lines 2383, 2419-2422.
func TestRedisStoreRoundTrip_spec_25_4(t *testing.T) {
	s, mr := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 5, 18, 9, 0, 0, 0, time.UTC)

	if s.Tier() != escalation.PersistenceDurableRedis {
		t.Fatalf("tier = %q, want durable-redis", s.Tier())
	}
	if err := s.Put(ctx, sample("esc-a", escalation.SeverityCritical, escalation.StatusOpen, base)); err != nil {
		t.Fatalf("put: %v", err)
	}
	// §25.4 line 2383: the key carries a 24h TTL.
	ttl := mr.TTL("ops:escalations:esc-a")
	if ttl <= 0 || ttl > 24*time.Hour {
		t.Errorf("ttl = %v, want a positive value within 24h", ttl)
	}

	got, err := s.Get(ctx, "esc-a")
	if err != nil || got == nil {
		t.Fatalf("get: %v rec=%v", err, got)
	}
	if !got.CreatedAt.Equal(base) {
		t.Errorf("createdAt = %v, want %v", got.CreatedAt, base)
	}

	// A missing key is (nil, nil), not an error.
	if rec, err := s.Get(ctx, "esc-missing"); err != nil || rec != nil {
		t.Errorf("get(missing) = (%v, %v), want (nil, nil)", rec, err)
	}

	// Status update is readable on the next get.
	now := base.Add(time.Hour)
	if _, err := s.SetStatus(ctx, "esc-a", escalation.StatusResolved, now); err != nil {
		t.Fatalf("set status: %v", err)
	}
	got, _ = s.Get(ctx, "esc-a")
	if got.Status != escalation.StatusResolved || got.ResolvedAt == nil {
		t.Errorf("status=%q resolvedAt=%v, want resolved with a timestamp", got.Status, got.ResolvedAt)
	}
}

// TestRedisStoreListFilterAndPending covers the §25.4 list filters,
// newest-first ordering, and the emission-retry projection.
// spec: §25.4 lines 2400-2404, 2419-2422.
func TestRedisStoreListFilterAndPending_spec_25_4(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 5, 18, 9, 0, 0, 0, time.UTC)

	_ = s.Put(ctx, sample("esc-old", escalation.SeverityWarning, escalation.StatusOpen, base))
	_ = s.Put(ctx, sample("esc-new", escalation.SeverityCritical, escalation.StatusOpen, base.Add(time.Minute)))

	allPage, err := s.List(ctx, escalation.Filter{}, "", 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	all := allPage.Items
	if len(all) != 2 || all[0].ID != "esc-new" {
		t.Errorf("list = %v, want newest-first [esc-new, esc-old]", ids(all))
	}
	if allPage.CursorKind != escalation.CursorKindNone {
		t.Errorf("cursorKind = %q, want %q on the Redis scan path", allPage.CursorKind, escalation.CursorKindNone)
	}
	critPage, _ := s.List(ctx, escalation.Filter{Severity: "critical"}, "", 0)
	if crit := critPage.Items; len(crit) != 1 || crit[0].ID != "esc-new" {
		t.Errorf("severity filter = %v, want [esc-new]", ids(crit))
	}
	// A page limit below the match count reports HasMore but no cursor: the
	// Redis path paginates by limit only (§25.4 line 2428).
	limited, _ := s.List(ctx, escalation.Filter{}, "", 1)
	if len(limited.Items) != 1 {
		t.Errorf("limit=1 returned %d, want 1", len(limited.Items))
	}
	if !limited.HasMore || limited.NextCursor != "" {
		t.Errorf("limit=1 page = {hasMore:%v cursor:%q}, want hasMore=true with no cursor", limited.HasMore, limited.NextCursor)
	}

	// Both records are unemitted, so PendingEmission returns both; SetEmitted
	// removes one from the projection.
	pending, _ := s.PendingEmission(ctx)
	if len(pending) != 2 {
		t.Fatalf("pending = %d, want 2", len(pending))
	}
	if err := s.SetEmitted(ctx, "esc-new"); err != nil {
		t.Fatalf("set emitted: %v", err)
	}
	pending, _ = s.PendingEmission(ctx)
	if len(pending) != 1 || pending[0].ID != "esc-old" {
		t.Errorf("pending after SetEmitted = %v, want [esc-old]", ids(pending))
	}
}

// TestRedisStoreUnavailableOnOutage asserts a Redis outage surfaces as
// escalation.ErrStoreUnavailable so the tiered Service falls back.
// spec: §25.4 lines 2376-2384.
func TestRedisStoreUnavailableOnOutage_spec_25_4(t *testing.T) {
	s, mr := newStore(t)
	ctx := context.Background()
	mr.Close() // simulate the Redis outage
	err := s.Put(ctx, sample("esc-x", escalation.SeverityInfo, escalation.StatusOpen, time.Now()))
	if err == nil || err != escalation.ErrStoreUnavailable {
		t.Fatalf("put after outage = %v, want ErrStoreUnavailable", err)
	}
	if _, err := s.List(ctx, escalation.Filter{}, "", 0); err != escalation.ErrStoreUnavailable {
		t.Errorf("list after outage = %v, want ErrStoreUnavailable", err)
	}
}

func ids(escs []escalation.Escalation) []string {
	out := make([]string, len(escs))
	for i, e := range escs {
		out[i] = e.ID
	}
	return out
}
