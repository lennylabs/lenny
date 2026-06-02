// SPDX-License-Identifier: MIT

package opsidem_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/opsidem"
)

func TestMemoryStoreClaimCompleteReplay_spec_25_4(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := opsidem.NewMemoryStore()

	rec, res, err := s.Claim(ctx, "k", "alice", "POST /v1/admin/backups", time.Hour, now)
	if err != nil || res != opsidem.ClaimInserted {
		t.Fatalf("first claim: res=%v err=%v, want inserted", res, err)
	}
	if rec.Status != opsidem.StatusInProgress {
		t.Errorf("inserted status = %q, want in_progress", rec.Status)
	}

	// Second claim before completion -> in-progress.
	_, res, _ = s.Claim(ctx, "k", "alice", "x", time.Hour, now.Add(time.Second))
	if res != opsidem.ClaimInProgress {
		t.Fatalf("second claim res = %v, want in-progress", res)
	}

	if err := s.Complete(ctx, "k", "alice", 201, []byte(`{"ok":true}`), now); err != nil {
		t.Fatalf("complete: %v", err)
	}
	rec, res, _ = s.Claim(ctx, "k", "alice", "x", time.Hour, now.Add(2*time.Second))
	if res != opsidem.ClaimReplay {
		t.Fatalf("post-complete claim res = %v, want replay", res)
	}
	if rec.StatusCode != 201 || string(rec.Response) != `{"ok":true}` {
		t.Errorf("replay rec = %+v, want 201/{\"ok\":true}", rec)
	}
}

func TestMemoryStoreFailIsRetryable_spec_25_4(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := opsidem.NewMemoryStore()
	s.Claim(ctx, "k", "alice", "x", time.Hour, now)
	if err := s.Fail(ctx, "k", "alice", now); err != nil {
		t.Fatalf("fail: %v", err)
	}
	// After Fail the row is gone, so a retry inserts afresh (re-executes).
	_, res, _ := s.Claim(ctx, "k", "alice", "x", time.Hour, now.Add(time.Second))
	if res != opsidem.ClaimInserted {
		t.Errorf("post-fail claim res = %v, want inserted (retryable)", res)
	}
}

func TestMemoryStoreExpiryAllowsFreshClaim_spec_25_4(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := opsidem.NewMemoryStore()
	s.Claim(ctx, "k", "alice", "x", time.Minute, now)
	s.Complete(ctx, "k", "alice", 200, []byte(`{}`), now)
	// Past expiry: the lazy cleanup on Claim drops the stale row.
	_, res, _ := s.Claim(ctx, "k", "alice", "x", time.Minute, now.Add(2*time.Minute))
	if res != opsidem.ClaimInserted {
		t.Errorf("post-expiry claim res = %v, want inserted", res)
	}
}

func TestMemoryStoreOwnedByOther_spec_25_4(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := opsidem.NewMemoryStore()
	s.Claim(ctx, "shared", "alice", "x", time.Hour, now)
	_, res, _ := s.Claim(ctx, "shared", "bob", "x", time.Hour, now.Add(time.Second))
	if res != opsidem.ClaimOwnedByOther {
		t.Errorf("bob claim res = %v, want owned-by-other", res)
	}
}

func TestMemoryStorePruneExpired_spec_25_4(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := opsidem.NewMemoryStore()
	s.Claim(ctx, "a", "alice", "x", time.Minute, now)
	s.Claim(ctx, "b", "alice", "x", time.Hour, now)
	n, err := s.PruneExpired(ctx, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 1 {
		t.Errorf("pruned %d, want 1 (only the 1-minute key expired)", n)
	}
	// The still-live key survives.
	if _, res, _ := s.Claim(ctx, "b", "alice", "x", time.Hour, now.Add(2*time.Minute)); res != opsidem.ClaimInProgress {
		t.Errorf("live key after prune res = %v, want in-progress (survived)", res)
	}
}
