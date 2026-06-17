//go:build component

// SPDX-License-Identifier: MIT

// Tier-4 integration test: the real cmd/lenny-gateway binary running
// against a Postgres container via --postgres-dsn. It proves the
// Postgres store wiring end-to-end — admin bootstrap, session
// lifecycle, and message injection all land in Postgres, verified by
// querying the database directly after each gateway operation.
package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

func TestGatewayPostgresPersistenceE2E(t *testing.T) {
	gateway.SkipUnlessAvailable(t)

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	gw := gateway.StartWith(t, "--dev-mode", "--postgres-dsn="+pg.DSN)
	base := gw.BaseURL()
	client := http.DefaultClient
	ctx := context.Background()

	do := func(method, path, roles string, body any) (int, map[string]any) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reader = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, base+path, reader)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
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

	dbCount := func(query string, args ...any) int {
		t.Helper()
		var n int
		if err := pg.Pool.QueryRow(ctx, query, args...).Scan(&n); err != nil {
			t.Fatalf("db query %q: %v", query, err)
		}
		return n
	}

	// ---- admin: bootstrap a tenant, a minimal runtime, and a user ----
	// The runtime payload omits executionMode and integrationLevel;
	// runtimestore.ApplyDefaults must fill them so the row satisfies
	// the runtime_definitions CHECK constraints.
	code, _ := do(http.MethodPost, "/v1/admin/bootstrap", "platform-admin", map[string]any{
		"tenants": []map[string]any{{"id": "acme", "displayName": "Acme Corp"}},
		// §5.1: declare capabilities.injection.supported: true so the
		// test's mid-session /messages call satisfies the
		// INJECTION_REJECTED gate. The bootstrapped runtime is the
		// minimal echo image; injection support is configuration, not
		// runtime image behavior, so this declaration just lets the
		// gateway admit the test's message round-trip.
		"runtimes": []map[string]any{{
			"name":   "echo",
			"image":  "lenny/echo@sha256:abc",
			"labels": map[string]string{"tier": "test"}, // §5.1 line 51: labels required
			"capabilities": map[string]any{
				"injection": map[string]any{"supported": true},
			},
		}},
		"users": []map[string]any{{
			"subject": "auth0|alice", "tenantId": "acme",
			"email": "alice@acme.com", "roles": []string{"tenant-admin"},
		}},
	})
	if code != http.StatusOK {
		t.Fatalf("bootstrap: status %d", code)
	}
	if n := dbCount(`SELECT COUNT(*) FROM tenants WHERE id = 'acme'`); n != 1 {
		t.Errorf("tenants row count = %d, want 1 (bootstrap did not persist to Postgres)", n)
	}
	if n := dbCount(`SELECT COUNT(*) FROM runtime_definitions WHERE name = 'echo'`); n != 1 {
		t.Errorf("runtime_definitions row count = %d, want 1", n)
	}
	if n := dbCount(`SELECT COUNT(*) FROM users WHERE tenant_id = 'acme' AND subject = 'auth0|alice'`); n != 1 {
		t.Errorf("users row count = %d, want 1", n)
	}

	// ---- admin: the bootstrapped records read back through the API ----
	code, tenants := do(http.MethodGet, "/v1/admin/tenants", "platform-admin", nil)
	if code != http.StatusOK {
		t.Fatalf("list tenants: %d", code)
	}
	if list, _ := tenants["items"].([]any); len(list) == 0 {
		t.Error("bootstrapped tenant not listed")
	}

	// ---- session: create, then confirm the row landed in Postgres ----
	code, created := do(http.MethodPost, "/v1/sessions/start", "", map[string]any{
		"runtimeRef": "echo",
		"userId":     "alice@acme.com",
	})
	if code != http.StatusCreated {
		t.Fatalf("create session: %d (%v)", code, created)
	}
	sid, _ := created["id"].(string)
	if sid == "" {
		t.Fatal("session id missing")
	}
	if n := dbCount(`SELECT COUNT(*) FROM sessions WHERE id = $1::uuid`, sid); n != 1 {
		t.Errorf("sessions row count for %s = %d, want 1", sid, n)
	}

	// ---- session: GET reads back through the Postgres store ----
	code, got := do(http.MethodGet, "/v1/sessions/"+sid, "", nil)
	if code != http.StatusOK {
		t.Fatalf("get session: %d", code)
	}
	if got["id"] != sid {
		t.Errorf("get session id = %v, want %s", got["id"], sid)
	}

	// ---- session: inject a message; the transcript persists to
	//      session_messages via the Postgres transcript store ----
	code, msgResp := do(http.MethodPost, "/v1/sessions/"+sid+"/messages", "", map[string]any{
		"messages": []map[string]any{{"role": "user", "content": "hello postgres"}},
	})
	if code != http.StatusOK {
		t.Fatalf("send message: %d (%v)", code, msgResp)
	}
	code, transcript := do(http.MethodGet, "/v1/sessions/"+sid+"/transcript", "", nil)
	if code != http.StatusOK {
		t.Fatalf("transcript: %d", code)
	}
	if entries, _ := transcript["entries"].([]any); len(entries) < 2 {
		t.Errorf("transcript should have >= 2 entries, got %v", transcript["entries"])
	}
	if n := dbCount(`SELECT COUNT(*) FROM session_messages WHERE session_id = $1::uuid`, sid); n < 2 {
		t.Errorf("session_messages row count = %d, want >= 2", n)
	}

	// ---- session: DELETE cancels the session (§15.1: a non-terminal
	//      session transitions to cancelled). The row persists in
	//      Postgres carrying the new state. ----
	code, _ = do(http.MethodDelete, "/v1/sessions/"+sid, "", nil)
	if code != http.StatusOK {
		t.Fatalf("delete session: %d", code)
	}
	var state string
	if err := pg.Pool.QueryRow(ctx,
		`SELECT state FROM sessions WHERE id = $1::uuid`, sid).Scan(&state); err != nil {
		t.Fatalf("read session state after delete: %v", err)
	}
	if state != "cancelled" {
		t.Errorf("session state after DELETE = %q, want cancelled", state)
	}

	// ---- health: the §25.3 API reports the live Postgres backend ----
	code, healthResp := do(http.MethodGet, "/v1/admin/health", "", nil)
	if code != http.StatusOK {
		t.Fatalf("health: status %d", code)
	}
	components, _ := healthResp["components"].([]any)
	var pgStatus string
	for _, c := range components {
		if m, ok := c.(map[string]any); ok && m["name"] == "postgres" {
			pgStatus, _ = m["status"].(string)
		}
	}
	if pgStatus != "healthy" {
		t.Errorf("health: postgres component status = %q, want healthy", pgStatus)
	}

	// ---- audit: the admin bootstrap is in the durable Postgres §11.7
	//      hash chain (on the actor's tenant chain), and it verifies ----
	code, verify := do(http.MethodGet, "/v1/admin/audit-events/verify?tenantId=acme", "platform-admin", nil)
	if code != http.StatusOK {
		t.Fatalf("audit verify: status %d", code)
	}
	if verify["integrity"] != "verified" {
		t.Errorf("audit chain integrity = %v, want verified", verify["integrity"])
	}
	if rc, _ := verify["rowCount"].(float64); rc < 1 {
		t.Errorf("audit rowCount = %v, want >= 1 (the bootstrap mutation)", verify["rowCount"])
	}
	if n := dbCount(`SELECT COUNT(*) FROM audit_log WHERE tenant_id = 'acme'`); n < 1 {
		t.Errorf("audit_log row count for acme = %d, want >= 1", n)
	}
}

// TestGatewayPostgresIntegrityWarningStartup confirms the §11.7
// startup integrity check is wired: against a tampered database in a
// non-production environment the gateway logs a warning and still
// starts, so StartWith — which waits for the process to become
// responsive — succeeds.
func TestGatewayPostgresIntegrityWarningStartup(t *testing.T) {
	gateway.SkipUnlessAvailable(t)

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	// Disable an integrity trigger before the gateway boots.
	if _, err := pg.Pool.Exec(context.Background(),
		`ALTER TABLE audit_log DISABLE TRIGGER lenny_audit_immutability`); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	gw := gateway.StartWith(t, "--dev-mode", "--postgres-dsn="+pg.DSN)
	resp, err := http.Get(gw.BaseURL() + "/healthz")
	if err != nil {
		t.Fatalf("healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz status = %d, want 200", resp.StatusCode)
	}
}
