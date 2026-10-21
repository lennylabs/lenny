// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
	"github.com/lennylabs/lenny/pkg/gateway/storage/sqlitestore"
)

var erasureWiringAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// sqlitePostureWiring runs buildPersistenceStores with --sqlite-path set to
// path, which selects Source Mode, and stops the flush loop and closes the
// file when the test ends.
func sqlitePostureWiring(t *testing.T, path string) *gatewayWiring {
	t.Helper()
	savedCmdLine, savedArgs := flag.CommandLine, os.Args
	t.Cleanup(func() { flag.CommandLine, os.Args = savedCmdLine, savedArgs })
	flag.CommandLine = flag.NewFlagSet(savedArgs[0], flag.ContinueOnError)
	os.Args = []string{savedArgs[0], "--sqlite-path=" + path}
	w := &gatewayWiring{f: parseFlags()}
	w.buildPersistenceStores()
	if w.sqliteDB == nil {
		t.Fatal("--sqlite-path did not select Source Mode")
	}
	t.Cleanup(func() { closeSourceMode(w) })
	return w
}

// closeSourceMode stops the flush loop and closes the SQLite file; it is
// idempotent so a test can close before its cleanup runs.
func closeSourceMode(w *gatewayWiring) {
	if w.sqliteDB == nil {
		return
	}
	w.sqliteFlushCancel()
	_ = w.sqliteDB.Close(context.Background())
	w.sqliteDB = nil
}

// startErasure starts an in-progress job for tenant/user on jobs.
func startErasure(t *testing.T, jobs erasurejob.Store, id, tenant, user string) erasurejob.Job {
	t.Helper()
	j, err := jobs.Start(context.Background(), erasurejob.Job{
		ID: id, TenantID: tenant, UserID: user, Phase: erasurejob.PhaseInitiated, StartedAt: erasureWiringAt,
		PhaseLog: []erasurejob.PhaseTransition{{Phase: erasurejob.PhaseInitiated, At: erasureWiringAt}},
	})
	if err != nil {
		t.Fatalf("start erasure job %s: %v", id, err)
	}
	return j
}

// seedTenantUser creates the tenant and the user on the wiring's stores.
func seedTenantUser(t *testing.T, w *gatewayWiring, tenant, user string) {
	t.Helper()
	ctx := context.Background()
	if err := w.tenants.Create(ctx, tenantstore.Tenant{ID: tenant}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := w.users.Create(ctx, userstore.User{TenantID: tenant, Subject: user}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

// TestMemoryPostureErasureJobsWriteTheFlag_spec_12_8 pins the memory branch
// of buildPersistenceStores: Start on the built erasure job store sets
// processing_restricted, naming the job, on the built user store. It fails
// when the branch builds the job store without its restriction writer.
// spec: 12.8 (Compliance Interfaces: Processing restriction during erasure (GDPR Article 18))
func TestMemoryPostureErasureJobsWriteTheFlag_spec_12_8(t *testing.T) {
	w := memoryPostureWiring(t)
	if w.erasureJobs == nil {
		t.Fatal("buildPersistenceStores recorded no erasure job store")
	}
	seedTenantUser(t, w, "acme", "alice@acme.com")
	job := startErasure(t, w.erasureJobs, "erasure_mem", "acme", "alice@acme.com")
	u, err := w.users.Get(context.Background(), "acme", "alice@acme.com")
	if err != nil || !u.ProcessingRestricted || u.ErasureJobID != job.ID {
		t.Fatalf("user after Start = %+v (%v), want restricted naming %s", u, err, job.ID)
	}
}

// TestSourceModeErasureJobsSurviveRestart_spec_17_4 pins the Source Mode
// branch: a started job and its flag survive a flush and a second build
// over the same file, which fails when the branch does not register the
// erasure job store, and the store is registered first, so the first flush
// of a fresh file writes the erasure_jobs row before any other.
// spec: 17.4 (Local Development Mode: Source Mode), 12.8 (Compliance Interfaces: Erasure job phase field (crash recovery))
func TestSourceModeErasureJobsSurviveRestart_spec_17_4(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "lenny.db")
	first := sqlitePostureWiring(t, path)
	seedTenantUser(t, first, "acme", "alice@acme.com")
	job := startErasure(t, first.erasureJobs, "erasure_src", "acme", "alice@acme.com")
	if err := first.sqliteDB.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}
	closeSourceMode(first)

	names := snapshotNames(t, path)
	if len(names) == 0 || names[0] != "erasure_jobs" {
		t.Fatalf("store_snapshots rows in write order = %v, want erasure_jobs first", names)
	}

	second := sqlitePostureWiring(t, path)
	active, err := second.erasureJobs.ListActive(ctx)
	if err != nil || len(active) != 1 || active[0].ID != job.ID {
		t.Fatalf("restored ListActive = %v (%v), want %s", active, err, job.ID)
	}
	u, err := second.users.Get(ctx, "acme", "alice@acme.com")
	if err != nil || !u.ProcessingRestricted || u.ErasureJobID != job.ID {
		t.Fatalf("restored user = %+v (%v), want restricted naming %s", u, err, job.ID)
	}
}

// snapshotNames returns the store_snapshots names in insertion order.
func snapshotNames(t *testing.T, path string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT name FROM store_snapshots ORDER BY rowid`)
	if err != nil {
		t.Fatalf("query store_snapshots: %v", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return names
}

// writeSnapshotPair writes a SQLite file whose erasure_jobs, users, and
// tenants snapshots are the given stores' contents, as two exports taken at
// different instants can leave them.
func writeSnapshotPair(t *testing.T, path string, jobs *erasurejob.Memory, users *userstore.Memory, tenants *tenantstore.Memory) {
	t.Helper()
	db, err := sqlitestore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.Register("erasure_jobs", jobs)
	db.Register("users", users)
	db.Register("tenants", tenants)
	if err := db.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// snapshotStores returns a tenant store holding acme, a user store holding
// alice, and a job store writing its flag on that user store.
func snapshotStores(t *testing.T) (*erasurejob.Memory, *userstore.Memory, *tenantstore.Memory) {
	t.Helper()
	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: "acme"}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	users := userstore.NewMemory()
	if err := users.Create(ctx, userstore.User{TenantID: "acme", Subject: "alice@acme.com"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return erasurejob.NewMemory(erasurejob.WithRestrictionWriter(users)), users, tenants
}

// TestSourceModeRestoreReassertsRestriction_spec_12_8 pins the Source Mode
// restore rule. A file whose erasure_jobs snapshot holds an in-progress job
// beside a users snapshot whose flag is clear restores with the flag set,
// naming the job, and the built session store refuses that user's create
// with ERASURE_IN_PROGRESS and inserts nothing; this case fails when the
// restore rule is not wired. A flag naming a completed job keeps its
// restored value, because the restore rule clears no flag.
// spec: 12.8 (Compliance Interfaces: Processing restriction during erasure (GDPR Article 18)), 12.8 (Compliance Interfaces: Lifecycle fence and drain), 17.4 (Local Development Mode: Source Mode)
func TestSourceModeRestoreReassertsRestriction_spec_12_8(t *testing.T) {
	ctx := context.Background()
	t.Run("in-progress job beside a clear flag", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "lenny.db")
		jobs, users, tenants := snapshotStores(t)
		job := startErasure(t, jobs, "erasure_live", "acme", "alice@acme.com")
		if _, err := users.SetErasureRestriction(ctx, "acme", "alice@acme.com", "", false, ""); err != nil {
			t.Fatalf("clear flag in the users snapshot: %v", err)
		}
		writeSnapshotPair(t, path, jobs, users, tenants)

		w := sqlitePostureWiring(t, path)
		u, err := w.users.Get(ctx, "acme", "alice@acme.com")
		if err != nil || !u.ProcessingRestricted || u.ErasureJobID != job.ID {
			t.Fatalf("restored user = %+v (%v), want restricted naming %s", u, err, job.ID)
		}
		row := sessionstore.Session{
			ID: "sess_after_restore", TenantID: "acme", UserID: "alice@acme.com",
			State: session.StateCreated, CreatedAt: erasureWiringAt, UpdatedAt: erasureWiringAt,
		}
		var ref *sessionstore.CreationRefusal
		if err := w.sessions.Create(ctx, row); !errors.As(err, &ref) || ref.Code != sessionstore.RefusalErasureInProgress {
			t.Fatalf("Create for the restricted user = %v, want ERASURE_IN_PROGRESS", err)
		}
		if _, err := w.sessions.Get(ctx, "acme", row.ID); !errors.Is(err, sessionstore.ErrNotFound) {
			t.Fatalf("refused Create inserted a row: %v", err)
		}
	})
	t.Run("flag naming a completed job stays set", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "lenny.db")
		jobs, users, tenants := snapshotStores(t)
		job := startErasure(t, jobs, "erasure_done", "acme", "alice@acme.com")
		if _, err := jobs.Complete(ctx, job.ID, erasureWiringAt.Add(time.Hour), job); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if _, err := users.SetErasureRestriction(ctx, "acme", "alice@acme.com", job.ID, true, ""); err != nil {
			t.Fatalf("set flag in the users snapshot: %v", err)
		}
		writeSnapshotPair(t, path, jobs, users, tenants)

		w := sqlitePostureWiring(t, path)
		u, err := w.users.Get(ctx, "acme", "alice@acme.com")
		if err != nil || !u.ProcessingRestricted || u.ErasureJobID != job.ID {
			t.Fatalf("restored user = %+v (%v), want the flag kept naming %s", u, err, job.ID)
		}
	})
}
