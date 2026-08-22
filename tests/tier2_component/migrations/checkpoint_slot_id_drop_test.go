//go:build component

// SPDX-License-Identifier: MIT

package migrations_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// dropSlotIDPriorVersion is the schema version immediately below the drop
// migration, the point the stepwise cases roll back to before seeding and
// re-applying.
const dropSlotIDPriorVersion = 179

// dropSlotIDUpFile is the drop migration's forward file, re-applied verbatim
// by the idempotency case.
const dropSlotIDUpFile = "0180_drop_checkpoint_slot_id.up.sql"

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

	after := []struct{ id, root, why string }{
		{retired, "/workspace/slots/" + retired + "/current", "the retired pod-global root is rewritten onto the session's own slot root"},
		{unset, "", "the column default records no reported root and is left alone"},
		{slotRoot, "/workspace/slots/" + slotRoot + "/current", "a root already under the slot tree is left alone"},
	}
	for _, want := range after {
		got := sessionWorkspaceRoot(t, ctx, pg, want.id)
		if got != want.root {
			t.Errorf("sessions.workspace_root for %s = %q, want %q (%s)", want.id, got, want.root, want.why)
		}
	}

	// Rolling the migration back leaves every recorded root as it stands. A
	// reverse rewrite keyed on the slot-root spelling cannot tell a row the
	// forward pass produced from a row that already held its own slot root, so
	// it would put the retired pod-global path onto a session whose value was
	// correct before the migration ran. The root is re-derived and
	// re-persisted at the next handshake, so the rollback writes nothing.
	pg.MigrateTo(t, dir, dropSlotIDPriorVersion)
	for _, want := range after {
		got := sessionWorkspaceRoot(t, ctx, pg, want.id)
		if got != want.root {
			t.Errorf("sessions.workspace_root for %s after rolling the migration back = %q, want it unchanged at %q", want.id, got, want.root)
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

// spec: 10.5 (a Phase 3 contract migration is idempotent, so a re-run after
// the gate passes is a no-op), 4.9 (the persisted duplicate slot columns are
// dropped)
// diagnosis: re-applying migration 0180's .up.sql against a schema it has
//
//	already contracted aborts instead of doing nothing. The gate index and the
//	gate COUNT both name the column the same file drops, so any statement of
//	either that sits outside the information_schema guard fails with an
//	undefined-column error on the second application. CREATE INDEX IF NOT
//	EXISTS does not help: it suppresses a duplicate index name and still parses
//	the column reference. An operator re-running the contract step of a rolling
//	deploy sees the whole migration roll back.
func TestDropCheckpointSlotIDUpIsIdempotent_spec_10_5(t *testing.T) {
	t.Parallel()
	dir := prodMigrations(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: dir})
	ctx := context.Background()

	src, err := os.ReadFile(filepath.Join(dir, dropSlotIDUpFile))
	if err != nil {
		t.Fatalf("read %s: %v", dropSlotIDUpFile, err)
	}
	if _, err := pg.Pool.Exec(ctx, string(src)); err != nil {
		t.Fatalf("re-applying %s against the contracted schema: %v", dropSlotIDUpFile, err)
	}

	// The re-run left the contracted schema exactly as it found it.
	mustNotHaveColumn(t, ctx, pg, "session_checkpoints", "slot_id")
	mustNotHaveColumn(t, ctx, pg, "checkpoint_manifest", "slot_id")
	for _, gate := range []string{
		"idx_session_checkpoints_slot_id_unmigrated",
		"idx_checkpoint_manifest_slot_id_unmigrated",
	} {
		if def := indexDef(t, ctx, pg, gate); def != "" {
			t.Errorf("gate index %s survived the re-run: %q", gate, def)
		}
	}
	for _, want := range []string{
		"idx_session_checkpoints_session_age",
		"partial_manifest_active_uniq",
		"idx_checkpoint_manifest_active",
	} {
		if indexDef(t, ctx, pg, want) == "" {
			t.Errorf("index %s is absent after the re-run", want)
		}
	}
}

// spec: 10.1 (the at-most-one-active-partial invariant, which the re-keyed
// unique index expresses on session_id alone), 11.2 (a reservation is released
// exactly once through the guarded release, which SQL alone cannot issue),
// 12.5 (the backstop reclaim sees only rows that are still active)
// diagnosis: migration 0180 did not refuse a schema in which one session holds
//
//	more than one active partial checkpoint_manifest row. The pre-drop unique
//	index was scoped on (session_id, slot_id), so that state is one the platform
//	produces: a crashed attempt at the 'default' sentinel is never superseded by
//	a later attempt that writes slot_id = session_id, and both rows pass the
//	Phase 3 column gates. The re-keyed index admits one such row per session, so
//	the migration has to resolve the duplicate before creating it. Retiring the
//	extra attempt is not a soft-delete: the reservation release is guarded on
//	reservation_released_at and the attempt's confirmed chunk objects and their
//	artifact_store rows have to be released, and once deleted_at is stamped the
//	§12.5 backstop can no longer reach the row. A migration that soft-deletes
//	the duplicate therefore leaks the attempt's reserved bytes and orphans its
//	chunk objects permanently, which is why this case asserts that the migration
//	refuses and leaves every row exactly as it found it.
func TestDropCheckpointSlotIDRefusesDuplicateActivePartials_spec_10_1(t *testing.T) {
	t.Parallel()
	dir := prodMigrations(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: dir})
	ctx := context.Background()

	pg.MigrateTo(t, dir, dropSlotIDPriorVersion)
	seedDropSlotIDTenant(t, ctx, pg, "acme")

	const session = "44444444-4444-4444-8444-444444444444"
	var (
		stale    = "55555555-5555-4555-8555-555555555555"
		survivor = "66666666-6666-4666-8666-666666666666"
	)
	// The crashed attempt sits at the 'default' sentinel and a lower fenced
	// generation; the later attempt on a concurrent pod wrote its own session
	// identifier, so the old (session_id, slot_id) index admitted both.
	seedActivePartial(t, ctx, pg, "acme", stale, session, "default", 1)
	seedActivePartial(t, ctx, pg, "acme", survivor, session, session, 2)

	err := applyDropSlotIDUp(ctx, pg, dir)
	if err == nil {
		t.Fatalf("migration 0180 applied against a session holding two active partial rows; want it refused")
	}
	if !strings.Contains(err.Error(), session) {
		t.Errorf("refusal = %v, want it to name session %s so an operator can retire the extra attempt", err, session)
	}

	// The refusal rolled the whole migration back, so no row was retired and
	// no column was dropped.
	mustHaveColumn(t, ctx, pg, "checkpoint_manifest", "slot_id")
	mustHaveColumn(t, ctx, pg, "session_checkpoints", "slot_id")
	for _, id := range []string{stale, survivor} {
		if deleted, reason := manifestTombstone(t, ctx, pg, "acme", id); deleted || reason != "in_progress" {
			t.Errorf("manifest row %s after the refusal: soft-deleted = %v, manifest_reason = %q; want it untouched and %q",
				id, deleted, reason, "in_progress")
		}
	}

	// Retiring the extra attempt through the abort path (its reservation
	// released and its row soft-deleted) lets the migration through.
	if err := execTenant(ctx, pg, "acme",
		`UPDATE checkpoint_manifest SET deleted_at = now(), manifest_reason = 'superseded',
			reservation_released_at = now()
			WHERE tenant_id = 'acme' AND checkpoint_id = $1`, stale); err != nil {
		t.Fatalf("retire the extra attempt: %v", err)
	}
	pg.MigrateTo(t, dir, dropSlotIDPriorVersion+1)
	mustNotHaveColumn(t, ctx, pg, "checkpoint_manifest", "slot_id")
}

// spec: 10.1 (the at-most-one-active-partial invariant), 4.9 (the unique index
// is re-keyed on session_id alone, with no tenant column)
// diagnosis: migration 0180's uniqueness gate is keyed on a wider column set
//
//	than the index it prepares for. The re-keyed unique index carries no tenant
//	column, so it admits one active partial row per session_id across every
//	tenant. A gate that partitions per tenant passes a pair of rows the index
//	then rejects, and the migration aborts on a bare unique violation that names
//	nothing an operator can act on instead of on the gate's own message.
func TestDropCheckpointSlotIDRefusesCrossTenantDuplicateActivePartials_spec_4_9(t *testing.T) {
	t.Parallel()
	dir := prodMigrations(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: dir})
	ctx := context.Background()

	pg.MigrateTo(t, dir, dropSlotIDPriorVersion)
	seedDropSlotIDTenant(t, ctx, pg, "acme")
	seedDropSlotIDTenant(t, ctx, pg, "globex")

	const session = "77777777-7777-4777-8777-777777777777"
	seedActivePartial(t, ctx, pg, "acme", "88888888-8888-4888-8888-888888888888", session, "default", 1)
	seedActivePartial(t, ctx, pg, "globex", "99999999-9999-4999-8999-999999999999", session, session, 1)

	err := applyDropSlotIDUp(ctx, pg, dir)
	if err == nil {
		t.Fatalf("migration 0180 applied against two tenants holding an active partial row for one session; want it refused")
	}
	if !strings.Contains(err.Error(), session) {
		t.Errorf("refusal = %v, want the gate's message naming session %s rather than a bare unique violation", err, session)
	}
	if strings.Contains(err.Error(), "partial_manifest_active_uniq") {
		t.Errorf("refusal = %v, want the gate to reject the pair before the unique index is created", err)
	}
}

// applyDropSlotIDUp applies the drop migration's forward file directly, so a
// refusal is returned rather than failing the test the way MigrateTo does.
// The file is one implicit transaction, matching how the migrator applies it.
func applyDropSlotIDUp(ctx context.Context, pg *containers.Postgres, dir string) error {
	src, err := os.ReadFile(filepath.Join(dir, dropSlotIDUpFile))
	if err != nil {
		return err
	}
	_, err = pg.Pool.Exec(ctx, string(src))
	return err
}

// seedDropSlotIDTenant inserts the tenant row the seeded manifest rows
// reference.
func seedDropSlotIDTenant(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant string) {
	t.Helper()
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, tenant); err != nil {
		t.Fatalf("seed tenant %s: %v", tenant, err)
	}
}

// seedActivePartial inserts one active partial manifest row at the pre-drop
// schema, which still carries slot_id.
func seedActivePartial(t *testing.T, ctx context.Context, pg *containers.Postgres,
	tenant, checkpointID, sessionID, slotID string, generation int64,
) {
	t.Helper()
	const insertManifest = `INSERT INTO checkpoint_manifest
		(tenant_id, checkpoint_id, session_id, slot_id, coordination_generation, partial,
		 chunk_object_key_prefix, chunk_size_bytes, checkpoint_started_at, checkpoint_timeout_at)
		VALUES ($1, $2, $3, $4, $5, TRUE, '/checkpoints/', 1048576, now(), now() + interval '1 hour')`
	if err := execTenant(ctx, pg, tenant, insertManifest, tenant, checkpointID, sessionID, slotID, generation); err != nil {
		t.Fatalf("seed manifest row %s: %v", checkpointID, err)
	}
}

// manifestTombstone reads one manifest row's soft-delete state and reason.
func manifestTombstone(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant, checkpointID string) (bool, string) {
	t.Helper()
	var (
		deleted bool
		reason  string
	)
	if err := pg.Pool.QueryRow(ctx,
		`SELECT deleted_at IS NOT NULL, manifest_reason FROM checkpoint_manifest
			WHERE tenant_id = $1 AND checkpoint_id = $2`, tenant, checkpointID).
		Scan(&deleted, &reason); err != nil {
		t.Fatalf("read manifest row %s: %v", checkpointID, err)
	}
	return deleted, reason
}
