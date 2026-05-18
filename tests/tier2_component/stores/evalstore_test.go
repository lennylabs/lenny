//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §10.7 built-in eval-result registry,
// exercising the Postgres-backed pkg/gateway/evalstore/pgstore against
// a real container. Covers Put with the per-session storage bound, the
// session foreign key, CountBySession, ListBySession ordering,
// ListByExperiment attribution filtering, cross-tenant isolation, and
// the §12.8 DeleteBySession erasure.
package stores_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/evalstore"
	evalpg "github.com/lennylabs/lenny/pkg/gateway/evalstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// evalSeedSession inserts a session row so the eval_results session
// foreign key resolves, and returns its id.
func evalSeedSession(t *testing.T, ctx context.Context, ss sessionstore.Store, tenant string) string {
	t.Helper()
	id := newUUID(t)
	if err := ss.Create(ctx, sessionstore.Session{
		ID:         id,
		TenantID:   tenant,
		State:      session.StateRunning,
		RuntimeRef: "echo",
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return id
}

func evalScore(v float64) *float64 { return &v }

// spec: 10.7
// diagnosis: the Postgres-backed eval-result registry in
// pkg/gateway/evalstore/pgstore did not behave as specified. Put must
// assign an ID and round-trip the score and per-dimension maps,
// CountBySession and ListBySession must report the session's results
// oldest-first, ListByExperiment must return only experiment-attributed
// results, cross-tenant reads must not leak, and DeleteBySession must
// erase the session's results per §12.8.
func TestEvalResultStoreContract(t *testing.T) {
	t.Parallel()
	sessionStore, pg := startStore(t)
	store := evalpg.New(pg.Pool)
	ctx := context.Background()

	t.Run("put assigns id and round-trips the score", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		sess := evalSeedSession(t, ctx, sessionStore, tenant)
		stored, err := store.Put(ctx, evalstore.EvalResult{
			TenantID:  tenant,
			SessionID: sess,
			Scorer:    "llm-judge",
			Score:     evalScore(0.875),
			Scores:    map[string]float64{"accuracy": 0.9, "tone": 0.85},
			Metadata:  map[string]any{"run": "a"},
		})
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		if stored.ID == "" {
			t.Error("Put did not assign an ID")
		}
		if stored.CreatedAt.IsZero() {
			t.Error("Put did not stamp CreatedAt")
		}
		got, err := store.ListBySession(ctx, tenant, sess)
		if err != nil {
			t.Fatalf("ListBySession: %v", err)
		}
		if len(got) != 1 || got[0].Score == nil || *got[0].Score != 0.875 {
			t.Fatalf("round-trip mismatch: %+v", got)
		}
		if got[0].Scores["accuracy"] != 0.9 {
			t.Errorf("per-dimension scores lost: %v", got[0].Scores)
		}
	})

	t.Run("count and list are oldest-first and tenant-scoped", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		sess := evalSeedSession(t, ctx, sessionStore, tenant)
		for i := 0; i < 3; i++ {
			if _, err := store.Put(ctx, evalstore.EvalResult{
				TenantID: tenant, SessionID: sess, Scorer: "s", Score: evalScore(float64(i)),
			}); err != nil {
				t.Fatalf("Put %d: %v", i, err)
			}
		}
		n, err := store.CountBySession(ctx, tenant, sess)
		if err != nil || n != 3 {
			t.Fatalf("CountBySession: got %d err=%v, want 3", n, err)
		}
		got, _ := store.ListBySession(ctx, tenant, sess)
		for i := range got {
			if got[i].Score == nil || *got[i].Score != float64(i) {
				t.Errorf("ListBySession not oldest-first at %d: %+v", i, got)
			}
		}
		// A different tenant sees nothing for this session id.
		intruder := freshTenant(t, ctx, pg)
		if n, _ := store.CountBySession(ctx, intruder, sess); n != 0 {
			t.Errorf("cross-tenant CountBySession leaked %d results", n)
		}
	})

	t.Run("list by experiment returns only attributed results", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		sess := evalSeedSession(t, ctx, sessionStore, tenant)
		if _, err := store.Put(ctx, evalstore.EvalResult{
			TenantID: tenant, SessionID: sess, Scorer: "s", Score: evalScore(1),
			ExperimentID: "exp-a", VariantID: "v1",
		}); err != nil {
			t.Fatalf("Put attributed: %v", err)
		}
		if _, err := store.Put(ctx, evalstore.EvalResult{
			TenantID: tenant, SessionID: sess, Scorer: "s", Score: evalScore(1),
		}); err != nil {
			t.Fatalf("Put unattributed: %v", err)
		}
		got, err := store.ListByExperiment(ctx, tenant, "exp-a")
		if err != nil {
			t.Fatalf("ListByExperiment: %v", err)
		}
		if len(got) != 1 || got[0].ExperimentID != "exp-a" {
			t.Errorf("ListByExperiment: got %d results %+v, want 1 attributed to exp-a", len(got), got)
		}
	})

	t.Run("delete by session erases the session's results", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		sess := evalSeedSession(t, ctx, sessionStore, tenant)
		for i := 0; i < 4; i++ {
			if _, err := store.Put(ctx, evalstore.EvalResult{
				TenantID: tenant, SessionID: sess, Scorer: "s", Score: evalScore(1),
			}); err != nil {
				t.Fatalf("Put: %v", err)
			}
		}
		deleted, err := store.DeleteBySession(ctx, tenant, sess)
		if err != nil || deleted != 4 {
			t.Fatalf("DeleteBySession: got %d err=%v, want 4", deleted, err)
		}
		if n, _ := store.CountBySession(ctx, tenant, sess); n != 0 {
			t.Errorf("results remain after DeleteBySession: %d", n)
		}
		// Idempotent: deleting again removes nothing.
		if again, _ := store.DeleteBySession(ctx, tenant, sess); again != 0 {
			t.Errorf("second DeleteBySession removed %d, want 0", again)
		}
	})
}
