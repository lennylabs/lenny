// SPDX-License-Identifier: MIT

package memstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// TestPoolDrainStats verifies the §15.1 line 797 pool-drain accounting:
// the count of live (non-terminal) sessions bound to a pool across
// tenants and the oldest created_at among them. Terminal sessions and
// sessions in other pools are excluded; an empty poolRef matches none.
func TestPoolDrainStats_spec_15_1_797(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seed := func(id, tenant, pool string, st session.State, createdAt time.Time) {
		if err := s.Create(ctx, sessionstore.Session{ID: id, TenantID: tenant, PoolRef: pool, State: st, CreatedAt: createdAt}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	// Two live sessions in pool p across two tenants; the oldest is s_old.
	seed("s_old", "acme", "p", session.StateRunning, base)
	seed("s_new", "globex", "p", session.StateRunning, base.Add(5*time.Minute))
	// Terminal session in p — excluded.
	seed("s_done", "acme", "p", session.StateCompleted, base.Add(-time.Hour))
	// Live session in another pool — excluded.
	seed("s_other", "acme", "other", session.StateRunning, base.Add(-2*time.Hour))

	count, oldest, err := s.PoolDrainStats(ctx, "p")
	if err != nil {
		t.Fatalf("PoolDrainStats: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
	if !oldest.Equal(base) {
		t.Errorf("oldest = %v, want %v", oldest, base)
	}
}

// TestPoolDrainStatsEmpty covers the no-sessions and empty-poolRef paths.
// spec: §15.1 line 797.
func TestPoolDrainStatsEmpty_spec_15_1_797(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()

	count, oldest, err := s.PoolDrainStats(ctx, "p")
	if err != nil || count != 0 || !oldest.IsZero() {
		t.Errorf("no sessions: count=%d oldest=%v err=%v", count, oldest, err)
	}
	count, oldest, err = s.PoolDrainStats(ctx, "")
	if err != nil || count != 0 || !oldest.IsZero() {
		t.Errorf("empty poolRef: count=%d oldest=%v err=%v", count, oldest, err)
	}
}
