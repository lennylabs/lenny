// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §4.2 startup tenant-isolation guarantee
// exercised through the LIVE gateway path. The real cmd/lenny-gateway
// binary boots against a Postgres container via --postgres-dsn, so the
// session store wiring, the auth middleware ordering, and the
// SET LOCAL app.current_tenant transaction wrapper are all in the loop.
//
// This complements the pure-database RLS check (rls_tenant_guard_test.go,
// which drives Postgres directly with pgx) and the memstore lifecycle
// check (session_lifecycle_test.go, which never touches Postgres): a
// middleware-ordering or store-wiring regression that bypassed
// SET LOCAL would slip past both, but not past this test, because it
// drives session create-and-read across two tenants through the real
// HTTP surface and confirms the cross-tenant read is filtered by RLS.
package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: §4.2 line 164 — "An integration test must verify tenant
// isolation at startup by confirming that a query without SET LOCAL is
// rejected and that cross-tenant reads return zero rows." The primary
// isolation mechanism is PostgreSQL Row-Level Security tied to the
// database session role, with every database call wrapped in a
// transaction that begins with SET LOCAL app.current_tenant.
//
// diagnosis: a failure means the §4.2 startup tenant-isolation
// guarantee does not hold through the live gateway stack. If the
// cross-tenant GET under globex returns 200 (or the acme session body),
// the Postgres session store did not run its read inside a
// SET LOCAL-scoped transaction, so a store-wiring or middleware-ordering
// regression let one tenant read another tenant's session. If the
// no-SET-LOCAL probe query succeeds, the gateway's backing Postgres was
// migrated without the lenny_tenant_isolation RLS policy, so the
// database no longer fails closed when the tenant context is unset.
func TestSessionRLSStartupIsolationThroughGateway(t *testing.T) {
	gateway.SkipUnlessAvailable(t)

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	gw := gateway.StartWith(t, "--dev-mode", "--postgres-dsn="+pg.DSN)
	base := gw.BaseURL()
	client := http.DefaultClient
	ctx := context.Background()

	do := func(method, path, tenant, roles string, body any) (int, map[string]any) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reader = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, base+path, reader)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Lenny-Tenant-ID", tenant)
		req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
		if roles != "" {
			req.Header.Set("X-Lenny-Roles", roles)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &out)
		}
		return resp.StatusCode, out
	}

	// ---- bootstrap two registered tenants so the cross-tenant read
	//      reaches the RLS-protected store rather than the tenant
	//      registry gate (an unregistered tenant would 403
	//      TENANT_NOT_FOUND before ever touching the sessions table). ----
	code, boot := do(http.MethodPost, "/v1/admin/bootstrap", "acme", "platform-admin", map[string]any{
		"tenants": []map[string]any{
			{"id": "acme", "displayName": "Acme Corp"},
			{"id": "globex", "displayName": "Globex Corp"},
		},
		"runtimes": []map[string]any{{
			"name":   "echo",
			"image":  "lenny/echo@sha256:abc",
			"labels": map[string]string{"tier": "test"}, // §5.1: labels required
		}},
		"users": []map[string]any{{
			"subject": "auth0|alice", "tenantId": "acme",
			"email": "alice@acme.com", "roles": []string{"tenant-admin"},
		}},
	})
	if code != http.StatusOK {
		t.Fatalf("bootstrap: status %d (%v)", code, boot)
	}

	// ---- §4.2: a query that reaches a tenant-scoped table without a
	//      prior SET LOCAL app.current_tenant must be rejected by the
	//      database. This is asserted against the gateway's own backing
	//      Postgres, as the non-superuser lenny_app role that carries the
	//      RLS policy (the superuser pool bypasses RLS). ----
	rejectedWithoutSetLocal := func() {
		conn, err := pgx.Connect(ctx, pg.DSN)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, "SET ROLE lenny_app"); err != nil {
			t.Fatalf("set role lenny_app: %v", err)
		}
		var n int
		err = conn.QueryRow(ctx, "SELECT count(*) FROM sessions").Scan(&n)
		if err == nil {
			t.Fatalf("expected the sessions SELECT with no SET LOCAL app.current_tenant to be rejected by RLS (got count=%d)", n)
		}
		if !strings.Contains(err.Error(), "app.current_tenant") {
			t.Fatalf("rejection %q does not mention the missing app.current_tenant setting", err.Error())
		}
	}
	rejectedWithoutSetLocal()

	// ---- drive alice's session create-and-read under tenant acme
	//      through the live gateway; the store persists and reads it back
	//      inside a SET LOCAL app.current_tenant = 'acme' transaction. ----
	code, created := do(http.MethodPost, "/v1/sessions/start", "acme", "", map[string]any{
		"runtimeRef": "echo",
		"userId":     "alice@acme.com",
	})
	if code != http.StatusCreated {
		t.Fatalf("create session under acme: status %d (%v)", code, created)
	}
	sid, _ := created["id"].(string)
	if sid == "" {
		t.Fatalf("created session id missing (%v)", created)
	}
	// The row is present in Postgres under acme's tenant_id.
	var owner string
	if err := pg.Pool.QueryRow(ctx,
		`SELECT tenant_id FROM sessions WHERE id = $1::uuid`, sid).Scan(&owner); err != nil {
		t.Fatalf("read persisted session tenant_id: %v", err)
	}
	if owner != "acme" {
		t.Fatalf("persisted session tenant_id = %q, want acme", owner)
	}

	// The owning tenant reads its own session back (200).
	code, got := do(http.MethodGet, "/v1/sessions/"+sid, "acme", "", nil)
	if code != http.StatusOK {
		t.Fatalf("acme read own session: status %d (%v)", code, got)
	}
	if got["id"] != sid {
		t.Fatalf("acme read own session id = %v, want %s", got["id"], sid)
	}

	// ---- §4.2: a cross-tenant read returns zero rows through the live
	//      RLS path. globex is a registered tenant, so the request passes
	//      auth and reaches the sessions table; RLS scoped to globex
	//      filters out acme's row, so the store finds nothing and the
	//      gateway reports 404. A 200 here means the store read escaped
	//      its SET LOCAL scope and leaked acme's session to globex. ----
	code, leaked := do(http.MethodGet, "/v1/sessions/"+sid, "globex", "", nil)
	if code != http.StatusNotFound {
		t.Fatalf("globex cross-tenant read: status %d, want 404 (RLS leaked acme's session; body=%v)", code, leaked)
	}
}
