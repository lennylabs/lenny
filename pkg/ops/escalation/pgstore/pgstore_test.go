// SPDX-License-Identifier: MIT

package pgstore_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/migrations"
	"github.com/lennylabs/lenny/pkg/ops/escalation"
	"github.com/lennylabs/lenny/pkg/ops/escalation/pgstore"
	embpostgres "github.com/lennylabs/lenny/tests/testinfra/embpg"
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
		Port:         0, // ephemeral; §17.4 forbids hardcoded ports and they collide under parallel tests
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
	allPage, _ := s.List(ctx, escalation.Filter{}, "", 0)
	all := allPage.Items
	if len(all) != 2 || all[0].ID != "esc-b" {
		t.Errorf("list = %v, want newest-first [esc-b, esc-a]", all)
	}
	crit, _ := s.List(ctx, escalation.Filter{Severity: "critical"}, "", 0)
	if len(crit.Items) != 1 || crit.Items[0].ID != "esc-a" {
		t.Errorf("severity filter = %v, want [esc-a]", crit.Items)
	}
	open, _ := s.List(ctx, escalation.Filter{Status: "open"}, "", 0)
	if len(open.Items) != 2 {
		t.Errorf("status=open filter = %d, want 2", len(open.Items))
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
	if allPage, _ = s.List(ctx, escalation.Filter{}, "", 0); len(allPage.Items) != 2 {
		t.Errorf("after idempotent re-put list = %d, want 2", len(allPage.Items))
	}
}

// TestPgStoreKeysetPagination brings up an embedded Postgres, seeds more
// escalations than the page limit, and pages through the durable Tier 1
// query path. The §25.4 query path requires the Postgres tier to support
// "full query with pagination": the first page must report hasMore with a
// "pk" cursorKind and a continuation cursor, the cursor must advance
// without dropping or repeating a record, and the final page must report
// hasMore=false. It downloads the PostgreSQL bundle, so it is skipped under
// -short.
//
// spec: §25.4 lines 2427-2428 (Storage Tiers, Query Path; Postgres path is
// "full query with pagination" with cursorKind "pk").
func TestPgStoreKeysetPagination_spec_25_4(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Port:         0,
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
	up, err := migrations.FS.ReadFile("0122_ops_escalations.up.sql")
	if err != nil {
		t.Fatalf("read 0122: %v", err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("apply 0122: %v", err)
	}

	s := pgstore.New(pool)
	base := time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC)
	const total = 5
	// Some rows share a created_at instant so the keyset cursor must break
	// ties on the primary key rather than the timestamp alone.
	for i := 0; i < total; i++ {
		id := "esc-" + string(rune('a'+i))
		at := base.Add(time.Duration(i/2) * time.Minute)
		if err := s.Put(ctx, escalation.Escalation{
			ID: id, Severity: escalation.SeverityInfo, Source: "watchdog",
			Summary: "seeded", Status: escalation.StatusOpen,
			Persistence: escalation.PersistenceDurablePostgres,
			CreatedAt:   at, UpdatedAt: at,
		}); err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
	}

	const pageSize = 2
	var (
		seen   []string
		cursor string
		pages  int
	)
	for {
		page, err := s.List(ctx, escalation.Filter{}, cursor, pageSize)
		if err != nil {
			t.Fatalf("list page %d: %v", pages, err)
		}
		pages++
		if page.CursorKind != escalation.CursorKindPK {
			t.Errorf("page %d cursorKind = %q, want %q", pages, page.CursorKind, escalation.CursorKindPK)
		}
		if len(page.Items) > pageSize {
			t.Fatalf("page %d returned %d items, want at most %d", pages, len(page.Items), pageSize)
		}
		for _, e := range page.Items {
			seen = append(seen, e.ID)
		}
		if !page.HasMore {
			if page.NextCursor != "" {
				t.Errorf("terminal page carries a cursor %q, want none", page.NextCursor)
			}
			break
		}
		if page.NextCursor == "" {
			t.Fatal("page reports hasMore but carries no continuation cursor")
		}
		cursor = page.NextCursor
		if pages > total+2 {
			t.Fatal("pagination did not terminate")
		}
	}

	// Every record appears exactly once, newest-first, across the pages.
	if len(seen) != total {
		t.Fatalf("paged %d records (%v), want %d unique", len(seen), seen, total)
	}
	unique := map[string]bool{}
	for _, id := range seen {
		if unique[id] {
			t.Errorf("record %s returned on more than one page", id)
		}
		unique[id] = true
	}
	// Seeded ids esc-a..esc-e ascend with created_at, so newest-first is the
	// reverse: esc-e first, esc-a last.
	if seen[0] != "esc-e" || seen[total-1] != "esc-a" {
		t.Errorf("page order = %v, want newest-first esc-e..esc-a", seen)
	}

	// A cursor this store did not produce is a malformed-cursor rejection,
	// not a silent full scan.
	if _, err := s.List(ctx, escalation.Filter{}, "not-a-real-cursor", pageSize); err == nil ||
		escalation.CodeOf(err) != escalation.ErrCodeInvalid {
		t.Errorf("malformed cursor error = %v, want ESCALATION_INVALID", err)
	}
}
