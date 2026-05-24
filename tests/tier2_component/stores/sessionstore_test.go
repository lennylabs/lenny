//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §12.2.1 SessionStore, exercising the
// Postgres-backed pkg/gateway/sessionstore/pgstore against a real
// container with the production migrations applied. Covers CRUD, the
// sentinel errors, cross-tenant isolation, the SELECT ... FOR UPDATE
// mutate path, strict-monotonic UpdatedAt, List filters, and the
// session_messages FK cascade.
package stores_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// newUUID returns a fresh random UUIDv4 string. Session IDs are UUIDs
// per §12.6; the sessions.id column is typed UUID.
func newUUID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// freshTenant inserts a uniquely-named tenant and returns its id so
// each sub-test operates on an isolated tenant.
func freshTenant(t *testing.T, ctx context.Context, pg *containers.Postgres) string {
	t.Helper()
	id := "t-" + newUUID(t)[:8]
	if _, err := pg.Pool.Exec(
		ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2)`, id, []byte{0x01},
	); err != nil {
		t.Fatalf("seed tenant %q: %v", id, err)
	}
	return id
}

// startStore brings up a Postgres container with the production
// migrations and returns the pgstore plus the raw handle.
func startStore(t *testing.T) (*pgstore.Store, *containers.Postgres) {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	return pgstore.New(pg.Pool), pg
}

// spec: 12.2.1
// diagnosis: the Postgres-backed SessionStore in
// pkg/gateway/sessionstore/pgstore did not behave as specified. Create
// and Get must round-trip a session including its jsonb workspace
// plan, the sentinel errors and cross-tenant isolation must hold, the
// SELECT ... FOR UPDATE mutate path must strictly advance UpdatedAt,
// List filters must apply, and Delete must cascade to session_messages.
func TestSessionStoreContract(t *testing.T) {
	t.Parallel()
	store, pg := startStore(t)
	ctx := context.Background()

	t.Run("create and get round-trip", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		ts := time.Now().UTC().Truncate(time.Microsecond)
		want := sessionstore.Session{
			ID:                 newUUID(t),
			TenantID:           tenant,
			UserID:             "alice@acme.com",
			State:              session.StateRunning,
			RuntimeRef:         "echo",
			PoolRef:            "pool-standard",
			IsolationProfile:   isolation.ProfileSandboxed,
			ParentWorkspaceRef: "lenny-blob://acme/workspace/parent",
			RetentionExpiresAt: ts.Add(48 * time.Hour),
			UploadTokenDigest:  "sha256:deadbeef",
			UploadTokenExpiry:  ts.Add(time.Hour),
			CreatedAt:          ts,
			UpdatedAt:          ts,
			WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{
				Ref:       "lenny-blob://acme/snapshot/x",
				Source:    sessionstore.WorkspaceSnapshotSealed,
				Timestamp: ts.Add(-time.Minute),
			},
		}
		if err := store.Create(ctx, want); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := store.Get(ctx, tenant, want.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.UserID != want.UserID || got.State != want.State ||
			got.RuntimeRef != want.RuntimeRef || got.PoolRef != want.PoolRef ||
			got.IsolationProfile != want.IsolationProfile ||
			got.ParentWorkspaceRef != want.ParentWorkspaceRef ||
			got.UploadTokenDigest != want.UploadTokenDigest {
			t.Errorf("scalar field mismatch:\n got %+v\nwant %+v", got, want)
		}
		if !got.RetentionExpiresAt.Equal(want.RetentionExpiresAt) ||
			!got.UploadTokenExpiry.Equal(want.UploadTokenExpiry) ||
			!got.CreatedAt.Equal(want.CreatedAt) {
			t.Errorf("timestamp mismatch: got %+v want %+v", got, want)
		}
		if got.WorkspaceSnapshot == nil {
			t.Fatal("WorkspaceSnapshot lost in round-trip")
		}
		if got.WorkspaceSnapshot.Ref != want.WorkspaceSnapshot.Ref ||
			got.WorkspaceSnapshot.Source != want.WorkspaceSnapshot.Source ||
			!got.WorkspaceSnapshot.Timestamp.Equal(want.WorkspaceSnapshot.Timestamp) {
			t.Errorf("WorkspaceSnapshot mismatch: got %+v want %+v",
				got.WorkspaceSnapshot, want.WorkspaceSnapshot)
		}
	})

	t.Run("workspace plan round-trip", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		want := sessionstore.Session{
			ID:         newUUID(t),
			TenantID:   tenant,
			State:      session.StateCreated,
			RuntimeRef: "echo",
			WorkspacePlan: json.RawMessage(
				`{"schemaVersion":1,"sources":[{"type":"mkdir","path":"out/"}]}`,
			),
		}
		if err := store.Create(ctx, want); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := store.Get(ctx, tenant, want.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if len(got.WorkspacePlan) == 0 {
			t.Fatal("WorkspacePlan lost in round-trip")
		}
		// jsonb normalizes whitespace and key order, so compare the
		// decoded documents rather than the raw bytes.
		var gotDoc, wantDoc any
		if err := json.Unmarshal(got.WorkspacePlan, &gotDoc); err != nil {
			t.Fatalf("stored plan is not valid JSON: %v", err)
		}
		if err := json.Unmarshal(want.WorkspacePlan, &wantDoc); err != nil {
			t.Fatalf("source plan is not valid JSON: %v", err)
		}
		if !reflect.DeepEqual(gotDoc, wantDoc) {
			t.Errorf("WorkspacePlan mismatch:\n got %s\nwant %s", got.WorkspacePlan, want.WorkspacePlan)
		}
	})

	t.Run("absent workspace plan stays nil", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		want := sessionstore.Session{
			ID: newUUID(t), TenantID: tenant, State: session.StateCreated, RuntimeRef: "echo",
		}
		if err := store.Create(ctx, want); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := store.Get(ctx, tenant, want.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.WorkspacePlan != nil {
			t.Errorf("WorkspacePlan = %s, want nil for a session created without a plan", got.WorkspacePlan)
		}
	})

	t.Run("duplicate create is rejected", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		s := sessionstore.Session{ID: newUUID(t), TenantID: tenant, State: session.StateCreated, RuntimeRef: "echo"}
		if err := store.Create(ctx, s); err != nil {
			t.Fatalf("first Create: %v", err)
		}
		if err := store.Create(ctx, s); !errors.Is(err, sessionstore.ErrAlreadyExists) {
			t.Errorf("duplicate Create: got %v, want ErrAlreadyExists", err)
		}
	})

	t.Run("get missing returns ErrNotFound", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		if _, err := store.Get(ctx, tenant, newUUID(t)); !errors.Is(err, sessionstore.ErrNotFound) {
			t.Errorf("Get missing: got %v, want ErrNotFound", err)
		}
		// A malformed (non-UUID) id is also "no such session".
		if _, err := store.Get(ctx, tenant, "not-a-uuid"); !errors.Is(err, sessionstore.ErrNotFound) {
			t.Errorf("Get malformed id: got %v, want ErrNotFound", err)
		}
	})

	t.Run("cross-tenant get is isolated", func(t *testing.T) {
		owner := freshTenant(t, ctx, pg)
		intruder := freshTenant(t, ctx, pg)
		id := newUUID(t)
		if err := store.Create(ctx, sessionstore.Session{
			ID: id, TenantID: owner, State: session.StateRunning, RuntimeRef: "echo",
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := store.Get(ctx, intruder, id); !errors.Is(err, sessionstore.ErrNotFound) {
			t.Errorf("cross-tenant Get: got %v, want ErrNotFound", err)
		}
	})

	t.Run("update mutates and rejects missing", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		id := newUUID(t)
		if err := store.Create(ctx, sessionstore.Session{
			ID: id, TenantID: tenant, State: session.StateRunning, RuntimeRef: "echo",
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		updated, err := store.Update(ctx, tenant, id, func(s *sessionstore.Session) error {
			s.State = session.StateCompleted
			return nil
		})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if updated.State != session.StateCompleted {
			t.Errorf("Update returned state %q, want completed", updated.State)
		}
		got, _ := store.Get(ctx, tenant, id)
		if got.State != session.StateCompleted {
			t.Errorf("persisted state %q, want completed", got.State)
		}
		if _, err := store.Update(ctx, tenant, newUUID(t), func(*sessionstore.Session) error {
			return nil
		}); !errors.Is(err, sessionstore.ErrNotFound) {
			t.Errorf("Update missing: got %v, want ErrNotFound", err)
		}
		// A mutate error aborts the write.
		sentinel := errors.New("mutate boom")
		if _, err := store.Update(ctx, tenant, id, func(*sessionstore.Session) error {
			return sentinel
		}); !errors.Is(err, sentinel) {
			t.Errorf("Update mutate error: got %v, want sentinel", err)
		}
	})

	t.Run("updated_at strictly advances", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		id := newUUID(t)
		if err := store.Create(ctx, sessionstore.Session{
			ID: id, TenantID: tenant, State: session.StateRunning, RuntimeRef: "echo",
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		nop := func(*sessionstore.Session) error { return nil }
		first, err := store.Update(ctx, tenant, id, nop)
		if err != nil {
			t.Fatalf("first Update: %v", err)
		}
		second, err := store.Update(ctx, tenant, id, nop)
		if err != nil {
			t.Fatalf("second Update: %v", err)
		}
		if !second.UpdatedAt.After(first.UpdatedAt) {
			t.Errorf("UpdatedAt did not advance: first=%v second=%v",
				first.UpdatedAt, second.UpdatedAt)
		}
	})

	t.Run("list orders newest-first and applies filters", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		base := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
		oldest := sessionstore.Session{
			ID: newUUID(t), TenantID: tenant, State: session.StateCompleted,
			RuntimeRef: "echo", CreatedAt: base, UpdatedAt: base,
		}
		middle := sessionstore.Session{
			ID: newUUID(t), TenantID: tenant, State: session.StateRunning,
			RuntimeRef: "claude", CreatedAt: base.Add(time.Minute), UpdatedAt: base.Add(time.Minute),
		}
		newest := sessionstore.Session{
			ID: newUUID(t), TenantID: tenant, State: session.StateFailed,
			RuntimeRef: "echo", FailureClass: session.FailureClassRuntime,
			CreatedAt: base.Add(2 * time.Minute), UpdatedAt: base.Add(2 * time.Minute),
		}
		for _, s := range []sessionstore.Session{oldest, middle, newest} {
			if err := store.Create(ctx, s); err != nil {
				t.Fatalf("Create %s: %v", s.ID, err)
			}
		}
		all, err := store.List(ctx, tenant, sessionstore.ListFilter{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(all) != 3 {
			t.Fatalf("List returned %d sessions, want 3", len(all))
		}
		if all[0].ID != newest.ID || all[2].ID != oldest.ID {
			t.Errorf("List order wrong: got %s..%s, want newest..oldest",
				all[0].ID, all[2].ID)
		}
		byState, err := store.List(ctx, tenant, sessionstore.ListFilter{State: session.StateRunning})
		if err != nil || len(byState) != 1 || byState[0].ID != middle.ID {
			t.Errorf("List by state: got %d rows (%v), want [middle]", len(byState), err)
		}
		byRuntime, err := store.List(ctx, tenant, sessionstore.ListFilter{RuntimeRef: "echo"})
		if err != nil || len(byRuntime) != 2 {
			t.Errorf("List by runtime echo: got %d rows, want 2", len(byRuntime))
		}
		byFailure, err := store.List(ctx, tenant,
			sessionstore.ListFilter{FailureClass: session.FailureClassRuntime})
		if err != nil || len(byFailure) != 1 || byFailure[0].ID != newest.ID {
			t.Errorf("List by failure class: got %d rows (%v), want [newest]", len(byFailure), err)
		}
	})

	t.Run("delete removes the row", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		id := newUUID(t)
		if err := store.Create(ctx, sessionstore.Session{
			ID: id, TenantID: tenant, State: session.StateCompleted, RuntimeRef: "echo",
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := store.Delete(ctx, tenant, id); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := store.Get(ctx, tenant, id); !errors.Is(err, sessionstore.ErrNotFound) {
			t.Errorf("Get after Delete: got %v, want ErrNotFound", err)
		}
		if err := store.Delete(ctx, tenant, id); !errors.Is(err, sessionstore.ErrNotFound) {
			t.Errorf("Delete missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("delete cascades to session_messages", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		id := newUUID(t)
		if err := store.Create(ctx, sessionstore.Session{
			ID: id, TenantID: tenant, State: session.StateRunning, RuntimeRef: "echo",
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		insertMessage(t, ctx, pg, tenant, id)
		if n := messageCount(t, ctx, pg, id); n != 1 {
			t.Fatalf("seeded message count = %d, want 1", n)
		}
		if err := store.Delete(ctx, tenant, id); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if n := messageCount(t, ctx, pg, id); n != 0 {
			t.Errorf("session_messages count after cascade = %d, want 0", n)
		}
	})

	// spec: §4.4 line 258 — `last_successful_checkpoint_at` round-trips
	// through Postgres and is nullable so a session that has never
	// been checkpointed reads as zero rather than the UNIX epoch.
	t.Run("last_successful_checkpoint_at round-trip", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		id := newUUID(t)
		ts := time.Now().UTC().Truncate(time.Microsecond)
		if err := store.Create(ctx, sessionstore.Session{
			ID: id, TenantID: tenant, State: session.StateRunning, RuntimeRef: "echo",
			LastSuccessfulCheckpointAt: ts,
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := store.Get(ctx, tenant, id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if !got.LastSuccessfulCheckpointAt.Equal(ts) {
			t.Errorf("LastSuccessfulCheckpointAt = %v, want %v", got.LastSuccessfulCheckpointAt, ts)
		}

		// Update bumps the timestamp and the read path observes the
		// new value.
		when := ts.Add(2 * time.Minute)
		updated, err := store.Update(ctx, tenant, id, func(s *sessionstore.Session) error {
			s.LastSuccessfulCheckpointAt = when
			return nil
		})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if !updated.LastSuccessfulCheckpointAt.Equal(when) {
			t.Errorf("Update LastSuccessfulCheckpointAt = %v, want %v", updated.LastSuccessfulCheckpointAt, when)
		}
	})

	// spec: §4.4 line 258 — never-checkpointed sessions read NULL on
	// the column, which the sessionstore maps to the zero time.Time
	// so the `FreshnessCheck` helper can treat them as stale.
	t.Run("last_successful_checkpoint_at defaults to zero when unset", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		id := newUUID(t)
		if err := store.Create(ctx, sessionstore.Session{
			ID: id, TenantID: tenant, State: session.StateRunning, RuntimeRef: "echo",
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := store.Get(ctx, tenant, id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if !got.LastSuccessfulCheckpointAt.IsZero() {
			t.Errorf("LastSuccessfulCheckpointAt = %v, want zero", got.LastSuccessfulCheckpointAt)
		}
	})
}

// insertMessage writes one session_messages row under the tenant
// context required by lenny_tenant_guard.
func insertMessage(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant, sessionID string) {
	t.Helper()
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("insertMessage begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		"SELECT set_config('app.current_tenant', $1, true)", tenant); err != nil {
		t.Fatalf("insertMessage set context: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO session_messages (id, session_id, tenant_id, seq, role, content)
		 VALUES (gen_random_uuid(), $1::uuid, $2, 1, 'user', 'hello')`,
		sessionID, tenant); err != nil {
		t.Fatalf("insertMessage insert: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("insertMessage commit: %v", err)
	}
}

func messageCount(t *testing.T, ctx context.Context, pg *containers.Postgres, sessionID string) int {
	t.Helper()
	var n int
	if err := pg.Pool.QueryRow(
		ctx,
		`SELECT COUNT(*) FROM session_messages WHERE session_id = $1::uuid`, sessionID,
	).Scan(&n); err != nil {
		t.Fatalf("messageCount: %v", err)
	}
	return n
}
