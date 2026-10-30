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
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
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
	t.Parallel()
	gateway.SkipUnlessAvailable(t)

	pg := sharedDatabase(t)
	// --postgres-billing-audit-ddl-dsn is the CREATE-privileged DDL
	// connection the gateway uses to provision a runtime-created tenant's
	// audit_seq_<40hex> sequence; without it every audit Append for a
	// freshly bootstrapped tenant fails on nextval of a nonexistent
	// relation. The single test Postgres instance plays both roles.
	// --tenant-deletion-interval is also the erasure sweep's poll cadence;
	// the leader runs a recorded job on its next poll.
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all", "--dev-mode", "--postgres-dsn="+pg.DSN,
		"--postgres-billing-audit-ddl-dsn="+pg.DSN, "--tenant-deletion-interval=1s")
	base := gw.BaseURL()

	do := func(t *testing.T, method, path, roles string, body any) (int, map[string]any) {
		t.Helper()
		return erasureAdminDo(t, base, method, path, roles, body)
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

		// spec: §12.8 — "Each step is individually idempotent to
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

		// spec: §12.8 — set a session-level legal hold.
		code, holdResp := do(t, http.MethodPost, "/v1/admin/legal-hold", "platform-admin", map[string]any{
			"tenantId": "acme", "sessionId": sid, "hold": true,
			"note": "preservation order pending litigation",
		})
		if code != http.StatusOK {
			t.Fatalf("set legal hold: %d (%v)", code, holdResp)
		}

		// spec: §12.8 — the step-0 preflight MUST abort before
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

		// spec: §12.8 — a platform-admin overrides the preflight
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
			t.Errorf("completion receipt legalHoldOverride = %v, want true (§12.8)", receipt["legalHoldOverride"])
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

// erasureAdminDo issues one request to the gateway at base as the acme
// operator, with roles when set, and returns the status and decoded body. It
// takes the calling (sub)test's *testing.T explicitly, so a failure inside a
// t.Run subtest fails that subtest rather than its parent.
func erasureAdminDo(t *testing.T, base, method, path, roles string, body any) (int, map[string]any) {
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
	resp, err := http.DefaultClient.Do(req)
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

// spec: 12.8 (Compliance Interfaces)
// diagnosis: the user-erasure job left the erased user's session DLQ or
// durable inbox in Redis, or deleted another user's. Either a held message
// carrying the erased user's content survives the erasure receipt, or an
// unrelated user's queued messages are lost.
func TestErasureDeletesTheErasedUsersSessionMessageQueues_spec_12_8(t *testing.T) {
	t.Parallel()
	gateway.SkipUnlessAvailable(t)

	pg := sharedDatabase(t)
	rd := containers.StartRedis(t, containers.RedisOptions{})
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all", "--dev-mode",
		"--postgres-dsn="+pg.DSN, "--postgres-billing-audit-ddl-dsn="+pg.DSN,
		"--redis-url=redis://"+rd.Addr+"/0", "--messaging-durable-inbox", "--tenant-deletion-interval=1s")
	base := gw.BaseURL()

	const alice, bob = "alice@acme.com", "bob@acme.com"
	code, _ := erasureAdminDo(t, base, http.MethodPost, "/v1/admin/bootstrap", "platform-admin", map[string]any{
		"tenants": []map[string]any{{"id": "acme", "displayName": "Acme Corp"}},
		"runtimes": []map[string]any{{
			"name": "echo", "image": "lenny/echo@sha256:abc", "labels": map[string]string{"tier": "test"},
		}},
		"users": []map[string]any{
			{"subject": alice, "tenantId": "acme", "email": alice, "roles": []string{"tenant-admin"}},
			{"subject": bob, "tenantId": "acme", "email": bob, "roles": []string{"tenant-admin"}},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("bootstrap: status %d", code)
	}
	start := func(user string) string {
		t.Helper()
		code, created := erasureAdminDo(t, base, http.MethodPost, "/v1/sessions/start", "", map[string]any{
			"runtimeRef": "echo", "userId": user,
		})
		sid, _ := created["id"].(string)
		if code != http.StatusCreated || sid == "" {
			t.Fatalf("create session for %s: %d (%v)", user, code, created)
		}
		return sid
	}
	aliceSID, bobSID := start(alice), start(bob)

	// Seed each session's DLQ and durable inbox under the §12.4 key layout.
	ctx := t.Context()
	keys := func(sid string) (dlq, inbox string) {
		return sessioninbox.DLQKey("acme", sid), sessioninbox.InboxKey("acme", sid)
	}
	for _, sid := range []string{aliceSID, bobSID} {
		dlq, inbox := keys(sid)
		if err := rd.Client.ZAdd(ctx, dlq, redis.Z{Score: float64(time.Now().Add(time.Hour).Unix()), Member: `{"id":"m-dlq"}`}).Err(); err != nil {
			t.Fatalf("seed %s: %v", dlq, err)
		}
		if err := rd.Client.RPush(ctx, inbox, `{"id":"m-inbox"}`).Err(); err != nil {
			t.Fatalf("seed %s: %v", inbox, err)
		}
	}

	code, resp := erasureAdminDo(t, base, http.MethodPost, "/v1/admin/users/"+alice+"/erase", "platform-admin",
		map[string]any{"tenantId": "acme"})
	jobID, _ := resp["jobId"].(string)
	if code != http.StatusAccepted || jobID == "" {
		t.Fatalf("erase %s: status %d (%v)", alice, code, resp)
	}
	wait.For(t, 10*time.Second, "erasure job completes", func() (bool, error) {
		code, job := erasureAdminDo(t, base, http.MethodGet, "/v1/admin/erasure-jobs/"+jobID, "platform-admin", nil)
		if code != http.StatusOK {
			return false, nil
		}
		switch job["phase"] {
		case "completed":
			return true, nil
		case "failed":
			t.Fatalf("erasure job failed: %v", job)
		}
		return false, nil
	})

	aliceDLQ, aliceInbox := keys(aliceSID)
	bobDLQ, bobInbox := keys(bobSID)
	if n := rd.Client.Exists(ctx, aliceDLQ, aliceInbox).Val(); n != 0 {
		t.Errorf("%d of the erased user's session message queues survived the erasure, want 0", n)
	}
	if n := rd.Client.Exists(ctx, bobDLQ, bobInbox).Val(); n != 2 {
		t.Errorf("%d of another user's session message queues remain, want 2", n)
	}
}

// bootstrapErasureTenants bootstraps one tenant per id with the echo
// runtime and one tenant-admin user, alice@<tenant>.com, in each.
func bootstrapErasureTenants(t *testing.T, base string, tenants ...string) {
	t.Helper()
	var ts, users []map[string]any
	for _, id := range tenants {
		ts = append(ts, map[string]any{"id": id, "displayName": id})
		user := "alice@" + id + ".com"
		users = append(users, map[string]any{"subject": user, "tenantId": id, "email": user, "roles": []string{"tenant-admin"}})
	}
	code, _ := erasureAdminDo(t, base, http.MethodPost, "/v1/admin/bootstrap", "platform-admin", map[string]any{
		"tenants":  ts,
		"runtimes": []map[string]any{{"name": "echo", "image": "lenny/echo@sha256:abc", "labels": map[string]string{"tier": "test"}}},
		"users":    users,
	})
	if code != http.StatusOK {
		t.Fatalf("bootstrap: status %d", code)
	}
}

// eraseAlice starts an erasure of alice@<tenant>.com through base and
// returns the job id.
func eraseAlice(t *testing.T, base, tenant string) string {
	t.Helper()
	code, resp := erasureAdminDo(t, base, http.MethodPost, "/v1/admin/users/alice@"+tenant+".com/erase", "platform-admin",
		map[string]any{"tenantId": tenant})
	jobID, _ := resp["jobId"].(string)
	if code != http.StatusAccepted || jobID == "" {
		t.Fatalf("erase alice@%s.com: status %d (%v)", tenant, code, resp)
	}
	return jobID
}

// awaitErasureCompleted polls the job through base until it completes.
func awaitErasureCompleted(t *testing.T, base, jobID string, within time.Duration) {
	t.Helper()
	wait.For(t, within, "erasure job "+jobID+" completes", func() (bool, error) {
		code, job := erasureAdminDo(t, base, http.MethodGet, "/v1/admin/erasure-jobs/"+jobID, "platform-admin", nil)
		if code != http.StatusOK {
			return false, nil
		}
		if job["phase"] == "failed" {
			t.Fatalf("erasure job %s failed: %v", jobID, job)
		}
		return job["phase"] == "completed", nil
	})
}

// spec: 12.8 (Erasure job phase field (crash recovery)), 12.8 (Processing restriction during erasure (GDPR Article 18)), 12.3 (Postgres HA Requirements), 12.5 (Artifact Store)
// diagnosis: a job recorded on one gateway replica was not resumed by
// another replica after the first one died, so the job store is not shared
// or the leader sweep does not poll it; or the erasure job's all-tenants
// reads (the runner's, the sampler's, and the platform-admin status
// query's) leave no cross_tenant_read event on the platform chain, so the
// §12.3 audit of sentinel reads is unwired.
func TestErasureJobResumesOnAnotherReplica_spec_12_8(t *testing.T) {
	t.Parallel()
	gateway.SkipUnlessAvailable(t)
	pg := sharedDatabase(t)
	args := func(interval string) []string {
		return []string{
			"--no-environment-policy", "allow-all", "--dev-mode", "--postgres-dsn=" + pg.DSN,
			"--postgres-billing-audit-ddl-dsn=" + pg.DSN, "--tenant-deletion-interval=" + interval,
		}
	}
	// Replica A polls once at startup and then not for an hour, so a job it
	// records stays unexecuted until A dies.
	gwA := gateway.StartWith(t, args("1h")...)
	bootstrapErasureTenants(t, gwA.BaseURL(), "acme")
	jobID := eraseAlice(t, gwA.BaseURL(), "acme")
	if code, job := erasureAdminDo(t, gwA.BaseURL(), http.MethodGet, "/v1/admin/erasure-jobs/"+jobID, "platform-admin", nil); code != http.StatusOK || job["phase"] != "initiated" {
		t.Fatalf("job on replica A = %d %v, want initiated", code, job)
	}
	gwA.Kill(t)

	gwB := gateway.StartWith(t, args("1s")...)
	awaitErasureCompleted(t, gwB.BaseURL(), jobID, 30*time.Second)
	if n := dbCountErasure(t, pg, `SELECT COUNT(*) FROM users WHERE tenant_id = 'acme' AND subject = 'alice@acme.com' AND processing_restricted`); n != 0 {
		t.Errorf("restriction still set after the resumed completion")
	}

	for _, ep := range []struct{ endpoint, caller string }{
		{"erasure_job_runner", ""},
		{"erasure_job_sampler", ""},
		{"GET /v1/admin/erasure-jobs/{jobId}", "ops@acme.com"},
	} {
		wait.For(t, 40*time.Second, "cross_tenant_read for "+ep.endpoint, func() (bool, error) {
			n := dbCountErasure(t, pg, `SELECT COUNT(*) FROM audit_log
				WHERE tenant_id = 'platform' AND event_type = 'cross_tenant_read'
				  AND payload->>'category' = 'erasure_job' AND payload->>'endpoint' = $1
				  AND COALESCE(payload->>'caller_subject', '') = $2`, ep.endpoint, ep.caller)
			return n > 0, nil
		})
	}
}

// spec: 12.8 (erasure_salt key management), 12.8 (Erasure job phase field (crash recovery)), 12.3 (Postgres HA Requirements)
// diagnosis: with a four-connection pool and eight concurrent erasure
// runs, at least one job did not complete: the salt lock holds or waits on
// pooled connections, so pseudonymizations exhaust the pool and deadlock,
// or the runner's concurrency limit does not admit jobs of distinct
// tenants together.
func TestErasureJobsCompleteWithSmallPool_spec_12_8(t *testing.T) {
	t.Parallel()
	gateway.SkipUnlessAvailable(t)
	pg := sharedDatabase(t)
	small := pg.DSN + "&pool_max_conns=4"
	if !strings.Contains(pg.DSN, "?") {
		small = pg.DSN + "?pool_max_conns=4"
	}
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all", "--dev-mode", "--postgres-dsn="+small,
		"--postgres-billing-audit-ddl-dsn="+pg.DSN, "--tenant-deletion-interval=1s", "--erasure-max-concurrent-jobs=8")
	base := gw.BaseURL()
	tenants := []string{"acme", "globex", "initech", "umbrella", "hooli", "stark", "wayne", "wonka"}
	bootstrapErasureTenants(t, base, tenants...)
	jobs := make([]string, 0, len(tenants))
	for _, tn := range tenants {
		jobs = append(jobs, eraseAlice(t, base, tn))
	}
	for _, id := range jobs {
		awaitErasureCompleted(t, base, id, 60*time.Second)
	}
}

// eraseTwinTables names the rows the wired-store erasure test seeds for
// one user's session in each store the user path erases by session.
var eraseTwinTables = []struct{ name, insert, count string }{
	{
		name:   "session_checkpoints",
		insert: `INSERT INTO session_checkpoints (tenant_id, session_id, ref) VALUES ($1, $2, 'ref-1')`,
		count:  `SELECT COUNT(*) FROM session_checkpoints WHERE tenant_id = $1 AND session_id = $2`,
	},
	{
		name:   "session_checkpoint_meta",
		insert: `INSERT INTO session_checkpoint_meta (tenant_id, session_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		count:  `SELECT COUNT(*) FROM session_checkpoint_meta WHERE tenant_id = $1 AND session_id = $2`,
	},
	{
		name: "checkpoint_manifest",
		insert: `INSERT INTO checkpoint_manifest (tenant_id, checkpoint_id, session_id, chunk_object_key_prefix,
			chunk_size_bytes, checkpoint_started_at, checkpoint_timeout_at)
			VALUES ($1, gen_random_uuid(), $2, $1 || '/checkpoints/' || $2 || '/', 1048576, now(), now() + interval '1 hour')`,
		count: `SELECT COUNT(*) FROM checkpoint_manifest WHERE tenant_id = $1 AND session_id = $2`,
	},
	{
		name:   "coordination_lease",
		insert: `INSERT INTO coordination_lease (tenant_id, session_id, coordinator_replica) VALUES ($1, $2, 'replica-x') ON CONFLICT DO NOTHING`,
		count:  `SELECT COUNT(*) FROM coordination_lease WHERE tenant_id = $1 AND session_id = $2`,
	},
}

// eraseTwinUserTables names the rows the wired-store erasure test seeds
// for one user in the two TokenStore stores.
var eraseTwinUserTables = []struct{ name, insert, count string }{
	{
		name: "connector_credentials",
		insert: `INSERT INTO connector_credentials (tenant_id, connector_id, user_id, access_token_blob, access_token_hash, refresh_token_blob)
			VALUES ($1, 'github', $2, '\x01', '\x02', '\x03')`,
		count: `SELECT COUNT(*) FROM connector_credentials WHERE tenant_id = $1 AND user_id = $2`,
	},
	{
		name:   "credentials",
		insert: `INSERT INTO credentials (tenant_id, ref, user_id, provider) VALUES ($1, 'cred-' || $2, $2, 'anthropic_direct')`,
		count:  `SELECT COUNT(*) FROM credentials WHERE tenant_id = $1 AND user_id = $2`,
	},
}

// seedEraseTwin writes one row per store for the user and their session.
func seedEraseTwin(t *testing.T, pg *containers.Postgres, user, sid string) {
	t.Helper()
	err := pgtenant.InTx(t.Context(), pg.Pool, "acme", func(tx pgx.Tx) error {
		for _, tb := range eraseTwinTables {
			if _, err := tx.Exec(t.Context(), tb.insert, "acme", sid); err != nil {
				return fmt.Errorf("%s: %w", tb.name, err)
			}
		}
		for _, tb := range eraseTwinUserTables {
			if _, err := tx.Exec(t.Context(), tb.insert, "acme", user); err != nil {
				return fmt.Errorf("%s: %w", tb.name, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed store rows for %s: %v", user, err)
	}
}

// eraseTwinCounts returns each store's row count for the user and session.
func eraseTwinCounts(t *testing.T, pg *containers.Postgres, user, sid string) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, tb := range eraseTwinTables {
		out[tb.name] = dbCountErasure(t, pg, tb.count, "acme", sid)
	}
	for _, tb := range eraseTwinUserTables {
		out[tb.name] = dbCountErasure(t, pg, tb.count, "acme", user)
	}
	return out
}

// spec: 12.8 (Compliance Interfaces: DeleteByUser dependency-ordered deletion sequence), 12.8 (Compliance Interfaces: Storage backends in erasure scope), 10.1.8, 4.9
// diagnosis: user erasure through the gateway binary left a checkpoint
// row, a coordination-mirror row, a connector refresh token, or a
// registered credential of the erased user, or deleted one of another
// user's, or a replica restarted after the erasure did not come back up.
// Each store is one the user path wires, so a surviving row means
// its eraser is not wired, and a missing bob row means an eraser ignores
// its scope. The restarted replica rebuilds the credential deny list from
// the revoked registered credentials; once user erasure deletes those rows,
// a lease still live on an unreleased remote binding loses its user-shaped
// deny entry after the restart, bounded by the lease TTL or by the orphan
// claim timeout plus one GC sweep.
func TestErasureDeletesTheUsersRowsInEveryWiredStore_spec_12_8(t *testing.T) {
	t.Parallel()
	gateway.SkipUnlessAvailable(t)

	pg := sharedDatabase(t)
	rd := containers.StartRedis(t, containers.RedisOptions{})
	args := []string{
		"--no-environment-policy", "allow-all", "--dev-mode",
		"--postgres-dsn=" + pg.DSN, "--postgres-billing-audit-ddl-dsn=" + pg.DSN,
		"--redis-url=redis://" + rd.Addr + "/0", "--tenant-deletion-interval=1s",
	}
	gw := gateway.StartWith(t, args...)
	base := gw.BaseURL()

	const alice, bob = "alice@acme.com", "bob@acme.com"
	code, _ := erasureAdminDo(t, base, http.MethodPost, "/v1/admin/bootstrap", "platform-admin", map[string]any{
		"tenants": []map[string]any{{"id": "acme", "displayName": "Acme Corp"}},
		"runtimes": []map[string]any{{
			"name": "echo", "image": "lenny/echo@sha256:abc", "labels": map[string]string{"tier": "test"},
		}},
		"users": []map[string]any{
			{"subject": alice, "tenantId": "acme", "email": alice, "roles": []string{"tenant-admin"}},
			{"subject": bob, "tenantId": "acme", "email": bob, "roles": []string{"tenant-admin"}},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("bootstrap: status %d", code)
	}
	start := func(user string) string {
		t.Helper()
		code, created := erasureAdminDo(t, base, http.MethodPost, "/v1/sessions/start", "", map[string]any{
			"runtimeRef": "echo", "userId": user,
		})
		sid, _ := created["id"].(string)
		if code != http.StatusCreated || sid == "" {
			t.Fatalf("create session for %s: %d (%v)", user, code, created)
		}
		return sid
	}
	aliceSID, bobSID := start(alice), start(bob)
	seedEraseTwin(t, pg, alice, aliceSID)
	seedEraseTwin(t, pg, bob, bobSID)

	jobID := eraseUser(t, base, alice)
	awaitErasureCompleted(t, base, jobID, 15*time.Second)

	for store, n := range eraseTwinCounts(t, pg, alice, aliceSID) {
		if n != 0 {
			t.Errorf("%s: %d rows of the erased user survived, want 0", store, n)
		}
	}
	for store, n := range eraseTwinCounts(t, pg, bob, bobSID) {
		if n != 1 {
			t.Errorf("%s: %d rows of another user remain, want 1", store, n)
		}
	}

	// A replica started after the erasure rebuilds its deny list from the
	// store and reaches readiness.
	restarted := gateway.StartWith(t, args...)
	if code, job := erasureAdminDo(t, restarted.BaseURL(), http.MethodGet, "/v1/admin/erasure-jobs/"+jobID, "platform-admin", nil); code != http.StatusOK || job["phase"] != "completed" {
		t.Fatalf("restarted replica: erasure job status %d (%v), want the completed job", code, job)
	}
}

// eraseUser starts an erasure of user in acme and returns the job id.
func eraseUser(t *testing.T, base, user string) string {
	t.Helper()
	code, resp := erasureAdminDo(t, base, http.MethodPost, "/v1/admin/users/"+user+"/erase", "platform-admin",
		map[string]any{"tenantId": "acme"})
	jobID, _ := resp["jobId"].(string)
	if code != http.StatusAccepted || jobID == "" {
		t.Fatalf("erase %s: status %d (%v)", user, code, resp)
	}
	return jobID
}
