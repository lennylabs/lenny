// SPDX-License-Identifier: MIT

package inproc

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
)

// TestHarnessSessionStoreIsThePostgresAdapter pins the storage layer
// TESTING.md §12.7.a names for the tier-7a multi-component harness.
//
// §12.7.a: "Every component is exercised either as a per-package bench
// (pkg/<package>/bench_load_test.go) or through an in-process
// multi-component harness (tests/testinfra/inproc) that boots a
// single-binary Lenny with `miniredis`, an embedded Postgres adapter,
// and a fake Kubernetes API surface (tests/testinfra/fakekube)."
//
// The session rows are the only store the harness gateway keeps, so
// "an embedded Postgres adapter" resolves to the Postgres SessionStore
// adapter (pkg/gateway/session/sessionstore/pgstore) running against an
// embedded PostgreSQL instance. A harness that keeps session rows in
// the in-memory adapter never transacts against SQL, so the tier's
// concurrency, ordering, and atomicity results say nothing about the
// storage layer the shipped gateway uses.
//
// The same section bounds the cost: "Each scenario completes within 15
// seconds. The full tier completes within 5 minutes on a developer
// laptop." Bringing the environment up is part of a scenario's Setup,
// so Start must leave the scenario room to run its load profile.
//
// spec: TESTING.md §12.7.a (in-process multi-component harness storage
// layer and wall-clock budget); §4.2 (SessionStore); §12.3 (tenant
// guard the Postgres adapter transacts under)
func TestHarnessSessionStoreIsThePostgresAdapter(t *testing.T) {
	ctx := context.Background()
	env := New(Config{})
	start := time.Now()
	if err := env.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = env.Stop(ctx) }()
	elapsed := time.Since(start)

	if _, ok := adapterUnder(env).(*pgstore.Store); !ok {
		t.Fatalf("tier-7a harness session store is %T; TESTING.md §12.7.a requires the embedded Postgres adapter (*pgstore.Store)", adapterUnder(env))
	}

	// The §12.7.a per-scenario budget is 15 seconds and covers Setup,
	// the load profile, and Teardown. Bringing the environment up must
	// stay a small fraction of that. The embedded PostgreSQL starts once
	// per test binary, so the first Env pays the process start and every
	// later one pays a template clone.
	if elapsed > 10*time.Second {
		t.Errorf("Env.Start took %s; the §12.7.a per-scenario budget is 15s and the load profile still has to run inside it", elapsed)
	}

	// A session created through the gateway must be a row in the
	// embedded PostgreSQL, readable by a connection the harness gateway
	// does not own. This is what an in-memory adapter cannot produce.
	status, body := postJSON(t, http.MethodPost, env.GatewayURL()+"/v1/sessions",
		`{"runtimeRef":"echo","userId":"alice@acme.com"}`)
	if status != http.StatusCreated {
		t.Fatalf("POST /v1/sessions: status=%d want 201 (body %v)", status, body)
	}
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatalf("§15.1 create envelope: missing id (body %v)", body)
	}

	dsn := env.PostgresDSN()
	if dsn == "" {
		t.Fatal("Env.PostgresDSN empty after Start: the harness has no embedded PostgreSQL to point at")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to the harness session store: %v", err)
	}
	defer pool.Close()

	var tenant, state string
	err = pool.QueryRow(ctx,
		`SELECT tenant_id, state FROM sessions WHERE id = $1::uuid`, id).Scan(&tenant, &state)
	if err != nil {
		t.Fatalf("read session %s back out of the embedded PostgreSQL: %v", id, err)
	}
	if tenant != "acme" {
		t.Errorf("sessions.tenant_id = %q, want %q", tenant, "acme")
	}
	if state != "created" {
		t.Errorf("sessions.state = %q, want %q", state, "created")
	}

	// A §15.1 transition is a SQL UPDATE under the §12.3 tenant guard,
	// so the terminal state is visible on the row as well.
	status, body = postJSON(t, http.MethodDelete, env.GatewayURL()+"/v1/sessions/"+id, "")
	if status != http.StatusOK {
		t.Fatalf("DELETE /v1/sessions/{id}: status=%d want 200 (body %v)", status, body)
	}
	if err := pool.QueryRow(ctx, `SELECT state FROM sessions WHERE id = $1::uuid`, id).Scan(&state); err != nil {
		t.Fatalf("re-read session %s: %v", id, err)
	}
	if state != "cancelled" {
		t.Errorf("sessions.state after DELETE = %q, want %q", state, "cancelled")
	}
}

// TestEnvsDoNotShareSessionRows pins the isolation the per-Env database
// preserves. Tier-7a scenarios each boot their own Env and assert on
// their own session counts, so one scenario's rows must not be visible
// to the next.
//
// spec: TESTING.md §12.7.a (per-scenario in-process harness)
func TestEnvsDoNotShareSessionRows(t *testing.T) {
	ctx := context.Background()
	first := New(Config{})
	if err := first.Start(ctx); err != nil {
		t.Fatalf("Start first env: %v", err)
	}
	defer func() { _ = first.Stop(ctx) }()

	status, body := postJSON(t, http.MethodPost, first.GatewayURL()+"/v1/sessions",
		`{"runtimeRef":"echo","userId":"alice@acme.com"}`)
	if status != http.StatusCreated {
		t.Fatalf("POST /v1/sessions: status=%d want 201 (body %v)", status, body)
	}

	second := New(Config{})
	if err := second.Start(ctx); err != nil {
		t.Fatalf("Start second env: %v", err)
	}
	defer func() { _ = second.Stop(ctx) }()

	if first.PostgresDSN() == second.PostgresDSN() {
		t.Fatalf("both envs share the database %q; scenarios would observe each other's session rows", first.PostgresDSN())
	}

	pool, err := pgxpool.New(ctx, second.PostgresDSN())
	if err != nil {
		t.Fatalf("connect to the second env's session store: %v", err)
	}
	defer pool.Close()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sessions`).Scan(&n); err != nil {
		t.Fatalf("count sessions in the second env: %v", err)
	}
	if n != 0 {
		t.Errorf("second env sees %d session rows from another env, want 0", n)
	}
}

// adapterUnder returns the SessionStore the harness gateway writes
// through, with the harness's tenant-anchoring wrapper peeled off.
func adapterUnder(e *Env) any {
	store := e.gw.store
	if w, ok := store.(*tenantAnchoringStore); ok {
		return w.Store
	}
	return store
}
