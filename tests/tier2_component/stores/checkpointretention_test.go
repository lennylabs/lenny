//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §4.4 / §12.5 session_checkpoints retention
// catalog, exercising the Postgres-backed
// pkg/gateway/checkpoint/checkpointretention/pgstore against a real
// container with the production migrations applied. The package's SQL names
// its columns in string literals, so the compiler checks none of it: this
// case is what holds the insert, the rotation, the listing, and the hard
// delete to the post-drop schema, where the retention cap is keyed on the
// session alone.
package stores_test

import (
	"context"
	"testing"
	"time"

	retentionpg "github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointretention/pgstore"

	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointretention"
)

// spec: §4.4 (checkpoint catalog durability), §12.5 (the "latest 2"
// retention cap, keyed on the session)
// diagnosis: the retention catalog's SQL no longer matches the
// session_checkpoints schema. Every statement in the package names its
// columns in a string literal, so a column the schema does not carry, or a
// rotation still scoped to a dropped dimension, is not a compile error: it
// surfaces as an undefined-column failure on the first checkpoint insert, or
// as a cap that retains more than the two most recent rows for a session.
func TestCheckpointRetentionStoreIsSessionKeyed(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	ctx := context.Background()
	store := retentionpg.New(pg.Pool, nil)
	tenant := freshTenant(t, ctx, pg)
	const sessionID = "sess-retention"

	// Six rows for one session. Against a schema still carrying the dropped
	// column, or SQL still naming it, the first Insert fails outright.
	refs := []string{"r1", "r2", "r3", "r4", "r5", "r6"}
	for _, ref := range refs {
		if err := store.Insert(ctx, checkpointretention.Record{
			TenantID: tenant, SessionID: sessionID, Ref: ref,
		}); err != nil {
			t.Fatalf("insert %s: %v", ref, err)
		}
		// Postgres stamps created_at, and the rotation orders on it, so the
		// inserts are separated rather than collapsed onto one instant.
		time.Sleep(2 * time.Millisecond)
	}

	// The cap applies to the session as a whole: the two most recent rows
	// stay retained and the four older ones transition.
	transitioned, err := store.Rotate(ctx, tenant, sessionID)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if len(transitioned) != len(refs)-checkpointretention.RetainedCount {
		t.Errorf("Rotate transitioned %d rows, want %d", len(transitioned), len(refs)-checkpointretention.RetainedCount)
	}
	rows, err := store.List(ctx, tenant, sessionID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != len(refs) {
		t.Fatalf("List returned %d rows, want %d", len(rows), len(refs))
	}
	retained := map[string]bool{}
	for _, row := range rows {
		if row.Retained {
			retained[row.Ref] = true
			if !row.DeletedAt.IsZero() {
				t.Errorf("row %s is retained and tombstoned", row.Ref)
			}
		} else if row.DeletedAt.IsZero() {
			t.Errorf("row %s transitioned without a deleted_at tombstone", row.Ref)
		}
	}
	if len(retained) != checkpointretention.RetainedCount || !retained["r5"] || !retained["r6"] {
		t.Errorf("retained set = %v, want the two most recent rows r5 and r6", retained)
	}

	// A second session's rows are its own: rotating one session leaves the
	// other's cap untouched.
	const otherSession = "sess-retention-other"
	for _, ref := range []string{"o1", "o2", "o3"} {
		if err := store.Insert(ctx, checkpointretention.Record{
			TenantID: tenant, SessionID: otherSession, Ref: ref,
		}); err != nil {
			t.Fatalf("insert %s: %v", ref, err)
		}
	}
	otherRows, err := store.List(ctx, tenant, otherSession)
	if err != nil {
		t.Fatalf("List other session: %v", err)
	}
	for _, row := range otherRows {
		if !row.Retained || !row.DeletedAt.IsZero() {
			t.Errorf("row %s of the un-rotated session was rotated: %+v", row.Ref, row)
		}
	}

	// The §12.5 hard prune removes a row by its (tenant, session, ref) key.
	if err := store.HardDelete(ctx, tenant, sessionID, "r1"); err != nil {
		t.Fatalf("HardDelete: %v", err)
	}
	after, err := store.List(ctx, tenant, sessionID)
	if err != nil {
		t.Fatalf("List after hard delete: %v", err)
	}
	if len(after) != len(refs)-1 {
		t.Errorf("after hard delete List returned %d rows, want %d", len(after), len(refs)-1)
	}
	for _, row := range after {
		if row.Ref == "r1" {
			t.Errorf("hard-deleted row r1 is still in the catalog")
		}
	}
}
