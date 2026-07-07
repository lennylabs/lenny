// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §12.8 GDPR `erasure_job` suite driven
// through the real cmd/lenny-gateway binary against a live Postgres
// container. It seeds a user's session and transcript, runs the
// dependency-ordered DeleteByUser sequence via the admin erasure API,
// and confirms — against the real FK-constrained schema, not a fake
// store — that the job completes, the rows are gone, and the
// gdpr.erasure_* audit trail records the actor and the outcome. A
// second scenario seeds a legal hold, confirms it blocks erasure with
// ERASURE_BLOCKED_BY_LEGAL_HOLD, then exercises the platform-admin
// override and confirms erasure proceeds and is recorded.
package tier4_integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	"github.com/lennylabs/lenny/tests/testinfra/wait"
)

// spec: 12.8
// diagnosis: the §12.8 DeleteByUser dependency-ordered erasure sequence,
// the legal-hold preflight, the platform-admin override, or the
// gdpr.* audit receipt diverged from spec when driven through the real
// gateway binary against a live, FK-constrained Postgres schema. A
// fake-store unit test cannot catch an ordering bug that only manifests
// as a foreign-key violation against the real sessions/session_messages
// tables; this test can.
func TestErasureJobJourney(t *testing.T) {
	gateway.SkipUnlessAvailable(t)

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	// --postgres-billing-audit-ddl-dsn is the CREATE-privileged DDL
	// connection the gateway uses to provision a runtime-created tenant's
	// audit_seq_<40hex> sequence; without it every audit Append for a
	// freshly bootstrapped tenant fails on nextval of a nonexistent
	// relation. The single test Postgres instance plays both roles.
	gw := gateway.StartWith(t, "--dev-mode", "--postgres-dsn="+pg.DSN, "--postgres-billing-audit-ddl-dsn="+pg.DSN)
	base := gw.BaseURL()
	client := http.DefaultClient

	// do issues an admin-API request. It takes the calling (sub)test's
	// *testing.T explicitly rather than closing over the parent's, so a
	// failure inside a t.Run subtest fails that subtest, not the parent
	// (calling t.Fatal on a parent test from within a subtest panics).
	do := func(t *testing.T, method, path, roles string, body any) (int, map[string]any) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			b, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal request body: %v", err)
			}
			reader = bytes.NewReader(b)
		}
		req, err := http.NewRequest(method, base+path, reader)
		if err != nil {
			t.Fatalf("%s %s: build request: %v", method, path, err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		req.Header.Set("X-Lenny-User-ID", "ops@acme.com")
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
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Fatalf("%s %s: decode response %q: %v", method, path, raw, err)
			}
		}
		return resp.StatusCode, out
	}

	// ---- bootstrap: a tenant and a runtime the sessions will run under ----
	code, _ := do(t, http.MethodPost, "/v1/admin/bootstrap", "platform-admin", map[string]any{
		"tenants": []map[string]any{{"id": "acme", "displayName": "Acme Corp"}},
		"runtimes": []map[string]any{{
			"name":         "echo",
			"image":        "lenny/echo@sha256:abc",
			"labels":       map[string]string{"tier": "test"},
			"capabilities": map[string]any{"injection": map[string]any{"supported": true}},
		}},
	})
	if code != http.StatusOK {
		t.Fatalf("bootstrap: status %d", code)
	}

	// awaitPhase polls the erasure-job status endpoint until it reaches a
	// terminal phase (completed or failed) and returns the final job
	// payload.
	awaitPhase := func(t *testing.T, jobID string) map[string]any {
		t.Helper()
		var job map[string]any
		wait.For(t, 10*time.Second, "erasure job reaches a terminal phase", func() (bool, error) {
			code, got := do(t, http.MethodGet, "/v1/admin/erasure-jobs/"+jobID, "platform-admin", nil)
			if code != http.StatusOK {
				return false, nil
			}
			job = got
			phase, _ := got["phase"].(string)
			return phase == "completed" || phase == "failed", nil
		})
		return job
	}

	// findAuditDetail polls the audit-events list for a row of eventType
	// whose emitted detail carries jobId (empty jobID matches the first
	// row of that eventType), and returns that detail block: the
	// event-specific fields the erasure handler passed to emit(), such as
	// jobId, deleted, and justification.
	findAuditDetail := func(t *testing.T, eventType, jobID string) map[string]any {
		t.Helper()
		var detail map[string]any
		wait.For(t, 10*time.Second, "gdpr audit event "+eventType+" for job "+jobID, func() (bool, error) {
			code, got := do(t, http.MethodGet,
				"/v1/admin/audit-events?tenantId=acme&eventType="+eventType, "platform-admin", nil)
			if code != http.StatusOK {
				return false, nil
			}
			items, _ := got["items"].([]any)
			for _, raw := range items {
				rec, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				unmapped, _ := rec["unmapped"].(map[string]any)
				lenny, _ := unmapped["lenny"].(map[string]any)
				if lenny == nil {
					continue
				}
				// spec: §11.7 the admin audit sink nests the emitter's
				// event-specific fields under payload.detail; the OCSF
				// translator carries every unmapped top-level payload key
				// verbatim into unmapped.lenny, so the emitted jobId /
				// justification / deleted fields land at unmapped.lenny.detail.
				d, _ := lenny["detail"].(map[string]any)
				if d == nil {
					continue
				}
				if jobID == "" || d["jobId"] == jobID {
					detail = d
					return true, nil
				}
			}
			return false, nil
		})
		return detail
	}

	t.Run("DeleteByUser runs the dependency-ordered sequence against real stores and records the receipt", func(t *testing.T) {
		const user = "alice@acme.com"
		code, _ := do(t, http.MethodPost, "/v1/admin/bootstrap", "platform-admin", map[string]any{
			"users": []map[string]any{{
				// The erase path (/v1/admin/users/{userId}/erase) and the
				// session's userId are both matched against the userstore
				// Subject field, so all three use the same identifier.
				"subject": user, "tenantId": "acme", "email": user, "roles": []string{"tenant-admin"},
			}},
		})
		if code != http.StatusOK {
			t.Fatalf("bootstrap user: status %d", code)
		}

		// Seed a real session and a real transcript row (session_messages),
		// each FK-linked to the session. Erasure must delete the child
		// (transcript) before the parent (session) or Postgres rejects it.
		code, created := do(t, http.MethodPost, "/v1/sessions/start", "", map[string]any{
			"runtimeRef": "echo", "userId": user,
		})
		if code != http.StatusCreated {
			t.Fatalf("create session: %d (%v)", code, created)
		}
		sid, _ := created["id"].(string)
		if sid == "" {
			t.Fatal("session id missing")
		}
		code, msgResp := do(t, http.MethodPost, "/v1/sessions/"+sid+"/messages", "", map[string]any{
			"messages": []map[string]any{{"role": "user", "content": "hello erasure"}},
		})
		if code != http.StatusOK {
			t.Fatalf("send message: %d (%v)", code, msgResp)
		}
		if n := dbCountErasure(t, pg, `SELECT COUNT(*) FROM sessions WHERE id = $1::uuid`, sid); n != 1 {
			t.Fatalf("precondition: sessions row count for %s = %d, want 1", sid, n)
		}
		if n := dbCountErasure(t, pg, `SELECT COUNT(*) FROM session_messages WHERE session_id = $1::uuid`, sid); n == 0 {
			t.Fatalf("precondition: session_messages row count for %s = %d, want > 0", sid, n)
		}

		// ---- run DeleteByUser via the admin erasure endpoint ----
		code, resp := do(t, http.MethodPost, "/v1/admin/users/"+user+"/erase", "platform-admin",
			map[string]any{"tenantId": "acme"})
		if code != http.StatusAccepted {
			t.Fatalf("erase user: status %d (%v)", code, resp)
		}
		jobID, _ := resp["jobId"].(string)
		if jobID == "" {
			t.Fatal("erase response carried no jobId")
		}

		job := awaitPhase(t, jobID)
		if job["phase"] != "completed" {
			t.Fatalf("erasure job phase = %v, want completed (job=%v)", job["phase"], job)
		}

		// spec: §12.8 line 821 — "Each step is individually idempotent to
		// support crash recovery" and the FK-ordered sequence deletes
		// child stores (transcripts) before the parent (sessions). Both
		// rows must now be gone from the real, constraint-enforcing
		// tables — a fake-store unit test cannot exercise this FK check.
		if n := dbCountErasure(t, pg, `SELECT COUNT(*) FROM sessions WHERE id = $1::uuid`, sid); n != 0 {
			t.Errorf("sessions row for %s survived DeleteByUser: count = %d, want 0", sid, n)
		}
		if n := dbCountErasure(t, pg, `SELECT COUNT(*) FROM session_messages WHERE session_id = $1::uuid`, sid); n != 0 {
			t.Errorf("session_messages rows for %s survived DeleteByUser: count = %d, want 0", sid, n)
		}

		// spec: §12.8 (TESTING.md:1044) — "audit trail records actor and
		// justification". The gdpr.erasure_completed receipt is the
		// authoritative proof the erasure ran, carrying the job id and the
		// per-store deleted counts.
		receipt := findAuditDetail(t, "gdpr.erasure_completed", jobID)
		if receipt == nil {
			t.Fatal("no gdpr.erasure_completed audit event for this job")
		}
		if receipt["jobId"] != jobID {
			t.Errorf("receipt jobId = %v, want %s", receipt["jobId"], jobID)
		}
		deleted, _ := receipt["deleted"].(map[string]any)
		if deleted == nil || deleted["sessions"] == nil {
			t.Errorf("receipt deleted map missing a sessions entry: %v", receipt["deleted"])
		}
	})

	t.Run("a legal hold blocks erasure and a platform-admin override proceeds", func(t *testing.T) {
		const user = "bob@acme.com"
		code, _ := do(t, http.MethodPost, "/v1/admin/bootstrap", "platform-admin", map[string]any{
			"users": []map[string]any{{
				"subject": user, "tenantId": "acme", "email": user, "roles": []string{"tenant-admin"},
			}},
		})
		if code != http.StatusOK {
			t.Fatalf("bootstrap user: status %d", code)
		}
		code, created := do(t, http.MethodPost, "/v1/sessions/start", "", map[string]any{
			"runtimeRef": "echo", "userId": user,
		})
		if code != http.StatusCreated {
			t.Fatalf("create session: %d (%v)", code, created)
		}
		sid, _ := created["id"].(string)
		if sid == "" {
			t.Fatal("session id missing")
		}

		// spec: §12.8 line 735 — set a session-level legal hold.
		code, holdResp := do(t, http.MethodPost, "/v1/admin/legal-hold", "platform-admin", map[string]any{
			"tenantId": "acme", "sessionId": sid, "hold": true,
			"note": "preservation order pending litigation",
		})
		if code != http.StatusOK {
			t.Fatalf("set legal hold: %d (%v)", code, holdResp)
		}

		// spec: §12.8 line 823 — the step-0 preflight MUST abort before
		// step 1 with ERASURE_BLOCKED_BY_LEGAL_HOLD (HTTP 409) when the
		// user has a session under an active hold.
		code, blocked := do(t, http.MethodPost, "/v1/admin/users/"+user+"/erase", "platform-admin",
			map[string]any{"tenantId": "acme"})
		if code != http.StatusConflict {
			t.Fatalf("erase with active hold: status %d (%v), want 409", code, blocked)
		}
		if errs, _ := blocked["error"].(map[string]any); errs == nil || errs["code"] != "ERASURE_BLOCKED_BY_LEGAL_HOLD" {
			t.Errorf("blocked erase error = %v, want code ERASURE_BLOCKED_BY_LEGAL_HOLD", blocked)
		}
		if n := dbCountErasure(t, pg, `SELECT COUNT(*) FROM sessions WHERE id = $1::uuid`, sid); n != 1 {
			t.Errorf("session row deleted despite the hold blocking erasure: count = %d, want 1", n)
		}
		if findAuditDetail(t, "gdpr.erasure_blocked_by_hold", "") == nil {
			t.Error("no gdpr.erasure_blocked_by_hold audit event for the blocked attempt")
		}

		// spec: §12.8 line 825 — a platform-admin overrides the preflight
		// with acknowledgeHoldOverride + a non-empty justification; the
		// erasure proceeds to step 1 and the override is recorded in the
		// completion receipt and its own audit event.
		const justification = "regulatory deadline conflicts with pending hold release, ticket-42"
		code, overrideResp := do(t, http.MethodPost, "/v1/admin/users/"+user+"/erase", "platform-admin", map[string]any{
			"tenantId": "acme", "acknowledgeHoldOverride": true, "justification": justification,
		})
		if code != http.StatusAccepted {
			t.Fatalf("override erase: status %d (%v), want 202", code, overrideResp)
		}
		jobID, _ := overrideResp["jobId"].(string)
		if jobID == "" {
			t.Fatal("override erase response carried no jobId")
		}

		job := awaitPhase(t, jobID)
		if job["phase"] != "completed" {
			t.Fatalf("override erasure job phase = %v, want completed (job=%v)", job["phase"], job)
		}
		if n := dbCountErasure(t, pg, `SELECT COUNT(*) FROM sessions WHERE id = $1::uuid`, sid); n != 0 {
			t.Errorf("session row for %s survived the overridden erasure: count = %d, want 0", sid, n)
		}

		overrideEvent := findAuditDetail(t, "gdpr.legal_hold_overridden", jobID)
		if overrideEvent == nil {
			t.Fatal("no gdpr.legal_hold_overridden audit event for the override")
		}
		if overrideEvent["overrideJustification"] != justification {
			t.Errorf("override event overrideJustification = %v, want %q", overrideEvent["overrideJustification"], justification)
		}
		if overrideEvent["legalHoldOverride"] != true {
			t.Errorf("override event legalHoldOverride = %v, want true", overrideEvent["legalHoldOverride"])
		}

		receipt := findAuditDetail(t, "gdpr.erasure_completed", jobID)
		if receipt == nil {
			t.Fatal("no gdpr.erasure_completed audit event for the overridden job")
		}
		if receipt["legalHoldOverride"] != true {
			t.Errorf("completion receipt legalHoldOverride = %v, want true (§12.8 line 796)", receipt["legalHoldOverride"])
		}
	})
}

// dbCountErasure runs a COUNT(*) query against the live Postgres
// container and fails the test on a query error.
func dbCountErasure(t *testing.T, pg *containers.Postgres, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pg.Pool.QueryRow(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatalf("db query %q: %v", query, err)
	}
	return n
}
