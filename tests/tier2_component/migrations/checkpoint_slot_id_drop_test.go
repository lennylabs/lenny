//go:build component

// SPDX-License-Identifier: MIT

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// dropSlotIDPriorVersion is the schema version immediately below the drop
// migration, the point the stepwise cases roll back to before seeding and
// re-applying.
const dropSlotIDPriorVersion = 179

// spec: 4.9 (the persisted duplicate slot columns are dropped and the three
// checkpoint indexes are re-keyed on session_id), 10.1 (the at-most-one-
// active-partial invariant and the resume-selection walk), 12.5 (the "latest
// 2" retention cap)
// diagnosis: migration 0180 did not drop session_checkpoints.slot_id and
//
//	checkpoint_manifest.slot_id, or did not re-key the rotation index, the
//	partial-manifest unique index, and the resume-selection index onto
//	session_id alone. The Go stores name these columns in string literals, so a
//	schema that still carries the column, or an index the drop left keyed on
//	it, is not caught by the compiler: it surfaces as an undefined-column error
//	on every checkpoint insert, or as a per-slot uniqueness scope the supersede
//	path no longer expresses. A failure on the rollback half means the
//	.down.sql did not restore the pre-drop column and index set.
func TestDropCheckpointSlotIDMigration_spec_4_9(t *testing.T) {
	t.Parallel()
	dir := prodMigrations(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: dir})
	ctx := context.Background()

	// Forward: both columns are gone.
	mustNotHaveColumn(t, ctx, pg, "session_checkpoints", "slot_id")
	mustNotHaveColumn(t, ctx, pg, "checkpoint_manifest", "slot_id")

	// The three re-keyed indexes exist and name session_id without a slot
	// dimension. Each definition is asserted rather than only the index name,
	// because a surviving slot-keyed index under a re-keyed name would leave
	// the retention cap and the uniqueness scope per slot.
	for _, want := range []struct {
		index    string
		contains string
	}{
		// §12.5 rotation: the "latest 2" cap walks a session's checkpoints by
		// descending created_at.
		{"idx_session_checkpoints_session_age", "(tenant_id, session_id, created_at DESC)"},
		// §10.1 at-most-one-active-partial invariant, keyed on session_id.
		{"partial_manifest_active_uniq", "(session_id)"},
		// §10.1 resume selection: the highest-fenced active row for a session.
		{"idx_checkpoint_manifest_active", "(tenant_id, session_id, coordination_generation DESC)"},
	} {
		def := indexDef(t, ctx, pg, want.index)
		if def == "" {
			t.Errorf("index %s is absent after migration 0180", want.index)
			continue
		}
		if !strings.Contains(def, want.contains) {
			t.Errorf("index %s definition = %q, want it keyed on %s", want.index, def, want.contains)
		}
		if strings.Contains(def, "slot_id") {
			t.Errorf("index %s still names slot_id: %q", want.index, def)
		}
	}
	// The slot-keyed indexes the drop replaces are gone.
	for _, gone := range []string{"idx_session_checkpoints_slot_age"} {
		if def := indexDef(t, ctx, pg, gone); def != "" {
			t.Errorf("index %s survived migration 0180: %q", gone, def)
		}
	}

	// Rolling 0180 back restores both columns and the slot-keyed indexes, so
	// the migration is reversible per step.
	pg.MigrateTo(t, dir, dropSlotIDPriorVersion)
	mustHaveColumn(t, ctx, pg, "session_checkpoints", "slot_id")
	mustHaveColumn(t, ctx, pg, "checkpoint_manifest", "slot_id")
	for _, restored := range []string{
		"idx_session_checkpoints_slot_age",
		"partial_manifest_active_uniq",
		"idx_checkpoint_manifest_active",
	} {
		def := indexDef(t, ctx, pg, restored)
		if def == "" {
			t.Errorf("index %s absent after rolling migration 0180 back", restored)
			continue
		}
		if !strings.Contains(def, "slot_id") {
			t.Errorf("index %s after rollback = %q, want the slot-keyed definition", restored, def)
		}
	}
}

// spec: 6.4 (the per-slot workspace tree), 7.3 (step (d) recreates the same
// absolute cwd path and the adapter compares the persisted root against the
// root it resolves)
// diagnosis: migration 0180 did not rewrite sessions.workspace_root onto the
//
//	per-slot path, or rewrote a row it must leave alone. The persisted root is
//	replayed verbatim as the resume-time expectation, so a row left holding the
//	retired pod-global /workspace/current fails every resume against a pod that
//	resolves a slot root, and a row rewritten from the empty default or from a
//	root already under the slot tree records a path no pod resolves. No other
//	case asserts the rewritten value: the resume path writes its own root, so a
//	wrong UPDATE predicate or a wrong derived expression ships green without
//	this case.
func TestDropCheckpointSlotIDRewritesWorkspaceRoot_spec_7_3(t *testing.T) {
	t.Parallel()
	dir := prodMigrations(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: dir})
	ctx := context.Background()

	// Roll back to the schema the rewrite runs against, so the three rows are
	// seeded before the migration rather than after it.
	pg.MigrateTo(t, dir, dropSlotIDPriorVersion)

	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ('acme', '\x00')`); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	const insertSession = `INSERT INTO sessions (id, tenant_id, state, runtime_ref, root_session_id, workspace_root)
		VALUES ($1, 'acme', 'created', 'echo', $1, $2)`
	var (
		retired  = "11111111-1111-4111-8111-111111111111"
		unset    = "22222222-2222-4222-8222-222222222222"
		slotRoot = "33333333-3333-4333-8333-333333333333"
	)
	for _, row := range []struct{ id, root string }{
		// The retired pod-global path: the row the rewrite exists for.
		{retired, "/workspace/current"},
		// The column default: no root was ever reported for this session.
		{unset, ""},
		// A root already under the slot tree, for a different session's
		// identifier than its own is not a case the platform produces; this row
		// holds its own slot root and must survive untouched.
		{slotRoot, "/workspace/slots/" + slotRoot + "/current"},
	} {
		if err := execTenant(ctx, pg, "acme", insertSession, row.id, row.root); err != nil {
			t.Fatalf("seed session %s: %v", row.id, err)
		}
	}

	pg.MigrateTo(t, dir, dropSlotIDPriorVersion+1)

	for _, want := range []struct{ id, root, why string }{
		{retired, "/workspace/slots/" + retired + "/current", "the retired pod-global root is rewritten onto the session's own slot root"},
		{unset, "", "the column default records no reported root and is left alone"},
		{slotRoot, "/workspace/slots/" + slotRoot + "/current", "a root already under the slot tree is left alone"},
	} {
		got := sessionWorkspaceRoot(t, ctx, pg, want.id)
		if got != want.root {
			t.Errorf("sessions.workspace_root for %s = %q, want %q (%s)", want.id, got, want.root, want.why)
		}
	}
}

// indexDef returns the CREATE INDEX definition Postgres holds for name, or
// the empty string when no such index exists.
func indexDef(t *testing.T, ctx context.Context, pg *containers.Postgres, name string) string {
	t.Helper()
	var def string
	err := pg.Pool.QueryRow(ctx,
		`SELECT COALESCE(max(indexdef), '') FROM pg_indexes
			WHERE schemaname = 'public' AND indexname = $1`, name).Scan(&def)
	if err != nil {
		t.Fatalf("read definition of index %s: %v", name, err)
	}
	return def
}

// sessionWorkspaceRoot reads the persisted §7.3 workspace root of one session.
func sessionWorkspaceRoot(t *testing.T, ctx context.Context, pg *containers.Postgres, id string) string {
	t.Helper()
	var root string
	if err := pg.Pool.QueryRow(ctx,
		`SELECT workspace_root FROM sessions WHERE id = $1`, id).Scan(&root); err != nil {
		t.Fatalf("read workspace_root of session %s: %v", id, err)
	}
	return root
}
