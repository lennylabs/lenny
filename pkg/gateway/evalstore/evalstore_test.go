// SPDX-License-Identifier: MIT

package evalstore_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/evalstore"
)

// spec: §10.7 built-in eval-result registry.

func score(v float64) *float64 { return &v }

func result(session, scorer string) evalstore.EvalResult {
	return evalstore.EvalResult{TenantID: "acme", SessionID: session, Scorer: scorer, Score: score(0.8)}
}

func TestPutAssignsIDAndTimestamp(t *testing.T) {
	m := evalstore.NewMemory(0, nil)
	stored, err := m.Put(context.Background(), result("sess_1", "llm-judge"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if !strings.HasPrefix(stored.ID, "eval_") {
		t.Errorf("Put did not assign an eval_ id: %q", stored.ID)
	}
	if stored.CreatedAt.IsZero() {
		t.Error("Put did not stamp CreatedAt")
	}
}

func TestPutAndListBySession(t *testing.T) {
	m := evalstore.NewMemory(0, nil)
	ctx := context.Background()
	t0 := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	first := result("sess_1", "exact-match")
	first.CreatedAt = t0
	second := result("sess_1", "llm-judge")
	second.CreatedAt = t0.Add(time.Minute)
	if _, err := m.Put(ctx, second); err != nil { // insert newer first
		t.Fatalf("Put second: %v", err)
	}
	if _, err := m.Put(ctx, first); err != nil {
		t.Fatalf("Put first: %v", err)
	}
	got, err := m.ListBySession(ctx, "acme", "sess_1")
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	if len(got) != 2 || got[0].Scorer != "exact-match" || got[1].Scorer != "llm-judge" {
		t.Errorf("ListBySession = %v, want oldest-first [exact-match llm-judge]", scorers(got))
	}
}

func TestCountBySession(t *testing.T) {
	m := evalstore.NewMemory(0, nil)
	ctx := context.Background()
	_, _ = m.Put(ctx, result("sess_1", "s"))
	_, _ = m.Put(ctx, result("sess_1", "s"))
	_, _ = m.Put(ctx, result("sess_2", "s"))
	if n, _ := m.CountBySession(ctx, "acme", "sess_1"); n != 2 {
		t.Errorf("CountBySession(sess_1) = %d, want 2", n)
	}
	if n, _ := m.CountBySession(ctx, "acme", "sess_2"); n != 1 {
		t.Errorf("CountBySession(sess_2) = %d, want 1", n)
	}
}

func TestPutEnforcesPerSessionQuota(t *testing.T) {
	m := evalstore.NewMemory(2, nil)
	ctx := context.Background()
	if _, err := m.Put(ctx, result("sess_q", "s")); err != nil {
		t.Fatalf("Put 1: %v", err)
	}
	if _, err := m.Put(ctx, result("sess_q", "s")); err != nil {
		t.Fatalf("Put 2: %v", err)
	}
	if _, err := m.Put(ctx, result("sess_q", "s")); !errors.Is(err, evalstore.ErrQuotaExceeded) {
		t.Errorf("Put 3 over the cap: got %v, want ErrQuotaExceeded", err)
	}
}

func TestQuotaIsPerSession(t *testing.T) {
	m := evalstore.NewMemory(2, nil)
	ctx := context.Background()
	for _, sess := range []string{"sess_a", "sess_b"} {
		for i := 0; i < 2; i++ {
			if _, err := m.Put(ctx, result(sess, "s")); err != nil {
				t.Fatalf("Put %s #%d: %v", sess, i, err)
			}
		}
	}
	// Each session is independently at its cap of 2 — no cross-session bleed.
	if _, err := m.Put(ctx, result("sess_a", "s")); !errors.Is(err, evalstore.ErrQuotaExceeded) {
		t.Errorf("sess_a over cap: got %v, want ErrQuotaExceeded", err)
	}
}

func TestDeleteBySession(t *testing.T) {
	m := evalstore.NewMemory(0, nil)
	ctx := context.Background()
	_, _ = m.Put(ctx, result("sess_d", "s"))
	_, _ = m.Put(ctx, result("sess_d", "s"))
	_, _ = m.Put(ctx, result("sess_keep", "s"))

	deleted, err := m.DeleteBySession(ctx, "acme", "sess_d")
	if err != nil {
		t.Fatalf("DeleteBySession: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}
	if n, _ := m.CountBySession(ctx, "acme", "sess_d"); n != 0 {
		t.Errorf("sess_d count after delete = %d, want 0", n)
	}
	if n, _ := m.CountBySession(ctx, "acme", "sess_keep"); n != 1 {
		t.Errorf("another session's results were deleted: count = %d, want 1", n)
	}
}

func TestListBySessionIsTenantScoped(t *testing.T) {
	m := evalstore.NewMemory(0, nil)
	ctx := context.Background()
	_, _ = m.Put(ctx, result("shared_sess", "s"))
	got, _ := m.ListBySession(ctx, "globex", "shared_sess")
	if len(got) != 0 {
		t.Errorf("ListBySession returned %d results to another tenant", len(got))
	}
}

func TestCloneIsolatesScores(t *testing.T) {
	m := evalstore.NewMemory(0, nil)
	r := result("sess_iso", "s")
	r.Scores = map[string]float64{"coherence": 0.9}
	stored, _ := m.Put(context.Background(), r)
	stored.Scores["coherence"] = 0.0 // mutate the returned copy
	reread, _ := m.ListBySession(context.Background(), "acme", "sess_iso")
	if reread[0].Scores["coherence"] != 0.9 {
		t.Error("mutating a returned Scores map reached into stored state")
	}
}

func TestNewIDFormat(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		id := evalstore.NewID()
		if !strings.HasPrefix(id, "eval_") || len(id) != len("eval_")+16 {
			t.Fatalf("NewID = %q", id)
		}
		if seen[id] {
			t.Fatalf("NewID returned a duplicate %q", id)
		}
		seen[id] = true
	}
}

func scorers(rs []evalstore.EvalResult) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Scorer
	}
	return out
}

// spec: §12.1 line 5 — eval results carry no user_id, so the
// orchestrator walks the user's sessions and calls DeleteBySession.
// DeleteByUser at this layer is a no-op that returns 0.
func TestDeleteByUserIsNoOp_spec_12_1(t *testing.T) {
	m := evalstore.NewMemory(0, nil)
	if _, err := m.Put(context.Background(), result("sess_1", "llm-judge")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	n, err := m.DeleteByUser(context.Background(), "acme", "alice")
	if err != nil || n != 0 {
		t.Errorf("DeleteByUser: n=%d err=%v", n, err)
	}
	if got, _ := m.CountBySession(context.Background(), "acme", "sess_1"); got != 1 {
		t.Errorf("eval result should survive DeleteByUser, count = %d", got)
	}
}

// spec: §12.1 line 5 / §12.8 Phase 4 — DeleteByTenant removes every
// eval result belonging to the tenant.
func TestDeleteByTenantRemovesAll_spec_12_1(t *testing.T) {
	m := evalstore.NewMemory(0, nil)
	ctx := context.Background()
	_, _ = m.Put(ctx, result("sess_a", "exact-match"))
	_, _ = m.Put(ctx, result("sess_b", "llm-judge"))
	other := result("sess_c", "exact-match")
	other.TenantID = "globex"
	_, _ = m.Put(ctx, other)
	n, err := m.DeleteByTenant(ctx, "acme")
	if err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if n != 2 {
		t.Errorf("DeleteByTenant should remove 2 acme rows, got %d", n)
	}
	if got, _ := m.CountBySession(ctx, "acme", "sess_a"); got != 0 {
		t.Errorf("acme/sess_a should be gone, count = %d", got)
	}
	if got, _ := m.CountBySession(ctx, "globex", "sess_c"); got != 1 {
		t.Errorf("globex/sess_c should survive, count = %d", got)
	}
}
