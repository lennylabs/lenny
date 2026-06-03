// SPDX-License-Identifier: MIT

package pgstore_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/migrations"
	embpostgres "github.com/lennylabs/lenny/pkg/embedded/postgres"
	"github.com/lennylabs/lenny/pkg/ops/escalation"
	"github.com/lennylabs/lenny/pkg/ops/escalation/pgstore"
)

// TestPgStoreRoundTrip brings up an embedded Postgres, creates the
// platform-scoped ops_escalations table from migration 0122, and
// exercises the §25.4 Tier 1 create/list/update/emission lifecycle
// against the durable store. It downloads the PostgreSQL bundle, so it
// is skipped under -short.
//
// spec: §25.4 lines 2376-2455.
func TestPgStoreRoundTrip_spec_25_4(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Port:         15498,
		Database:     "lenny",
		Username:     "lenny",
		Password:     "lenny",
		StartTimeout: 3 * time.Minute,
	})
	if err := pg.Start(); err != nil {
		t.Fatalf("embedded postgres Start: %v", err)
	}
	defer func() { _ = pg.Stop() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, pg.DSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	// ops_escalations is platform-scoped (no FK / role deps), so apply just
	// the 0122 up migration directly.
	up, err := migrations.FS.ReadFile("0122_ops_escalations.up.sql")
	if err != nil {
		t.Fatalf("read 0122: %v", err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("apply 0122: %v", err)
	}

	s := pgstore.New(pool)
	if s.Tier() != escalation.PersistenceDurablePostgres {
		t.Fatalf("tier = %q, want durable-postgres", s.Tier())
	}
	created := time.Date(2026, 5, 18, 9, 0, 0, 0, time.UTC)

	esc := escalation.Escalation{
		ID: "esc-a", Severity: escalation.SeverityCritical, Source: "watchdog",
		OperationID: "op-1", AlertName: "WarmPoolExhausted", Summary: "scaling failed",
		DiagnosticData: json.RawMessage(`{"pool":"default"}`),
		FailedActions:  []escalation.FailedAction{{Action: "scale", Error: "quota"}},
		Status:         escalation.StatusOpen, Persistence: escalation.PersistenceDurablePostgres,
		Emitted: false, CreatedAt: created, UpdatedAt: created,
	}
	if err := s.Put(ctx, esc); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, err := s.Get(ctx, "esc-a")
	if err != nil || got == nil {
		t.Fatalf("get: %v rec=%v", err, got)
	}
	if !got.CreatedAt.Equal(created) {
		t.Errorf("createdAt = %v, want preserved %v", got.CreatedAt, created)
	}
	// JSONB canonicalizes whitespace, so compare semantically.
	var diag map[string]string
	if err := json.Unmarshal(got.DiagnosticData, &diag); err != nil || diag["pool"] != "default" {
		t.Errorf("diagnosticData = %s, want round-tripped {pool: default}", got.DiagnosticData)
	}
	if len(got.FailedActions) != 1 || got.FailedActions[0].Action != "scale" {
		t.Errorf("failedActions = %v, want the round-tripped step", got.FailedActions)
	}

	// A missing id is (nil, nil), not an error.
	if rec, err := s.Get(ctx, "esc-missing"); err != nil || rec != nil {
		t.Errorf("get(missing) = (%v, %v), want (nil, nil)", rec, err)
	}

	// PendingEmission returns the unemitted record; SetEmitted clears it.
	pending, _ := s.PendingEmission(ctx)
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(pending))
	}
	if err := s.SetEmitted(ctx, "esc-a"); err != nil {
		t.Fatalf("set emitted: %v", err)
	}
	if pending, _ = s.PendingEmission(ctx); len(pending) != 0 {
		t.Errorf("pending after SetEmitted = %d, want 0", len(pending))
	}

	// A second unemitted record exercises list filtering + ordering.
	_ = s.Put(ctx, escalation.Escalation{
		ID: "esc-b", Severity: escalation.SeverityWarning, Source: "watchdog",
		Summary: "y", Status: escalation.StatusOpen, Persistence: escalation.PersistenceDurablePostgres,
		CreatedAt: created.Add(time.Minute), UpdatedAt: created.Add(time.Minute),
	})
	all, _ := s.List(ctx, escalation.Filter{}, 0)
	if len(all) != 2 || all[0].ID != "esc-b" {
		t.Errorf("list = %v, want newest-first [esc-b, esc-a]", all)
	}
	crit, _ := s.List(ctx, escalation.Filter{Severity: "critical"}, 0)
	if len(crit) != 1 || crit[0].ID != "esc-a" {
		t.Errorf("severity filter = %v, want [esc-a]", crit)
	}
	open, _ := s.List(ctx, escalation.Filter{Status: "open"}, 0)
	if len(open) != 2 {
		t.Errorf("status=open filter = %d, want 2", len(open))
	}

	// Status update stamps resolved_at on the first transition.
	now := created.Add(2 * time.Hour)
	res, err := s.SetStatus(ctx, "esc-a", escalation.StatusResolved, now)
	if err != nil || res == nil {
		t.Fatalf("set status: %v rec=%v", err, res)
	}
	if res.Status != escalation.StatusResolved || res.ResolvedAt == nil || !res.ResolvedAt.Equal(now) {
		t.Errorf("status=%q resolvedAt=%v, want resolved at %v", res.Status, res.ResolvedAt, now)
	}
	// Updating an unknown id is (nil, nil).
	if rec, err := s.SetStatus(ctx, "esc-missing", escalation.StatusResolved, now); err != nil || rec != nil {
		t.Errorf("set status(missing) = (%v, %v), want (nil, nil)", rec, err)
	}

	// Idempotent re-put (the flush no-op): a second Put of esc-b leaves the
	// row count unchanged.
	_ = s.Put(ctx, escalation.Escalation{
		ID: "esc-b", Severity: escalation.SeverityWarning, Source: "watchdog",
		Summary: "y", Status: escalation.StatusOpen, Persistence: escalation.PersistenceDurablePostgres,
		CreatedAt: created.Add(time.Minute), UpdatedAt: created.Add(time.Minute),
	})
	if all, _ = s.List(ctx, escalation.Filter{}, 0); len(all) != 2 {
		t.Errorf("after idempotent re-put list = %d, want 2", len(all))
	}
}
