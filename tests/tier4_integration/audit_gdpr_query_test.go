// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §25.9 Audit Log Query API surfacing of the
// §12.8 step-14 GDPR dead-letter redaction, driven through the real
// cmd/lenny-gateway binary against a live Postgres container. It seeds a
// dead-lettered audit row that names the erasure subject, runs
// DeleteByUser via the admin erasure API, and then queries the audit
// trail through GET /v1/admin/audit-events to confirm two §25.9
// contracts against the real Postgres chain and RedactionReceipt store:
//   - the gdpr.erasure_deadletter_downstream_notified event Lenny emits
//     for the notified OCSF sink is queryable by eventType, so compliance
//     teams can produce an auditor-ready record of every downstream
//     notification, and it carries the OCSF Entity-Management/Delete class.
//   - the row Lenny rewrote in place under a signed RedactionReceipt is
//     returned with per-row chainIntegrity redacted_gdpr (the authorized
//     discontinuity), rather than broken, and the envelope's
//     chainIntegrityReport tallies it.
package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	"github.com/lennylabs/lenny/tests/testinfra/wait"
)

// spec: 25.9 (audit log query api), 12.8 (deadletter redaction)
// diagnosis: a failure means the §25.9 query API, wired to the real
// Postgres audit chain and RedactionReceipt store through the gateway
// binary, either does not surface the gdpr.erasure_deadletter_downstream_notified
// event Lenny emitted for a redacted dead-letter row, or misclassifies
// the lawfully redacted row as broken instead of redacted_gdpr. Either
// defect leaves a compliance team unable to produce the auditor-ready
// record §25.9 promises. The in-memory-chain unit tests in pkg/audit
// cannot catch this: the redacted_gdpr verdict here depends on the
// signed receipt persisted by the real DeleteByUser run being loaded
// back through the auditstore receipt reader.
func TestAuditGDPRDownstreamNotificationQueryable(t *testing.T) {
	gateway.SkipUnlessAvailable(t)

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	// The single test Postgres plays both the app DSN and the
	// CREATE-privileged DDL DSN the gateway uses to provision a tenant's
	// audit_seq_<40hex> sequence.
	gw := gateway.StartWith(t, "--dev-mode", "--postgres-dsn="+pg.DSN, "--postgres-billing-audit-ddl-dsn="+pg.DSN)
	base := gw.BaseURL()
	client := http.DefaultClient

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

	const user = "alice@acme.com"

	// ---- bootstrap: the tenant and the erasure subject ----
	code, _ := do(t, http.MethodPost, "/v1/admin/bootstrap", "platform-admin", map[string]any{
		"tenants": []map[string]any{{"id": "acme", "displayName": "Acme Corp"}},
		"users": []map[string]any{{
			"subject": user, "tenantId": "acme", "email": user, "roles": []string{"tenant-admin"},
		}},
	})
	if code != http.StatusOK {
		t.Fatalf("bootstrap: status %d", code)
	}

	// ---- seed a dead-lettered audit row that names the subject ----
	// The §12.8 step-14 scan finds a dead-lettered row whose payload names
	// the target user; the row's raw canonical payload is the pre-redaction
	// PII the OCSF sink already ingested. Append it through the same
	// Postgres-backed chain the gateway writes so it links onto the real
	// tail, then flip its ocsf_translation_state to dead_lettered (a
	// permitted bookkeeping column) so DeleteByUser redacts it. session.created
	// has an OCSF class mapping, so the query can translate the redacted row.
	ctx := context.Background()
	seedStore := auditstore.New(pg.Router(t))
	seedPayload, _ := json.Marshal(map[string]any{
		"user_id":           user,
		"raw_canonical_b64": "c2VjcmV0LXBpaS1mb3ItYWxpY2U=", // base64("secret-pii-for-alice")
	})
	seededRow, err := seedStore.Append(ctx, "acme", "session.created", json.RawMessage(seedPayload), time.Now().UTC())
	if err != nil {
		t.Fatalf("seed dead-lettered audit row: %v", err)
	}
	markDeadLettered(t, ctx, pg, "acme", seededRow.Seq)

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

	// Wait for the erasure job to reach a terminal phase; step-14 redaction
	// and its paired events are emitted before the job completes.
	wait.For(t, 15*time.Second, "erasure job reaches completed", func() (bool, error) {
		code, got := do(t, http.MethodGet, "/v1/admin/erasure-jobs/"+jobID, "platform-admin", nil)
		if code != http.StatusOK {
			return false, nil
		}
		phase, _ := got["phase"].(string)
		if phase == "failed" {
			t.Fatalf("erasure job failed: %v", got)
		}
		return phase == "completed", nil
	})

	// ---- assertion 1: the downstream-notification event is queryable by
	// eventType and carries the OCSF Entity-Management / Delete class ----
	// spec: §25.9 — "queryable via GET /v1/admin/audit-events?eventType=
	// gdpr.erasure_deadletter_downstream_notified so compliance teams can
	// produce an auditor-ready record of every downstream notification
	// Lenny emitted."
	const notifyType = "gdpr.erasure_deadletter_downstream_notified"
	var notifyItems []map[string]any
	wait.For(t, 10*time.Second, "downstream-notification event queryable", func() (bool, error) {
		code, env := do(t, http.MethodGet,
			"/v1/admin/audit-events?tenantId=acme&eventType="+url.QueryEscape(notifyType), "platform-admin", nil)
		if code != http.StatusOK {
			return false, nil
		}
		notifyItems = itemMaps(env)
		return len(notifyItems) > 0, nil
	})
	if len(notifyItems) == 0 {
		t.Fatalf("no %s event returned by the eventType query", notifyType)
	}
	// spec: §25.9 — the event is OCSF "class 5001 Entity Management,
	// activity_id: 4 Delete".
	for _, rec := range notifyItems {
		if got := jsonNumber(rec["class_uid"]); got != 5001 {
			t.Errorf("%s class_uid = %d, want 5001 (Entity Management)", notifyType, got)
		}
		if got := jsonNumber(rec["activity_id"]); got != 4 {
			t.Errorf("%s activity_id = %d, want 4 (Delete)", notifyType, got)
		}
	}

	// ---- assertion 2: the row Lenny redacted in place under a signed
	// RedactionReceipt is returned with per-row chainIntegrity redacted_gdpr,
	// and the envelope tallies it ----
	// spec: §25.9 — the per-row `chainIntegrity` field and its
	// `chainIntegrityReport` tally; a §12.8-redacted row carrying a valid
	// receipt is the redacted_gdpr authorized-discontinuity bucket, distinct
	// from broken. Query a window covering the whole chain (an eventType
	// filter would exclude the redacted session.created row).
	since := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	code, env := do(t, http.MethodGet, "/v1/admin/audit-events?tenantId=acme&since="+url.QueryEscape(since), "platform-admin", nil)
	if code != http.StatusOK {
		t.Fatalf("wide audit query: status %d (%v)", code, env)
	}
	report, _ := env["chainIntegrityReport"].(map[string]any)
	if report == nil {
		t.Fatalf("audit envelope carried no chainIntegrityReport: %v", env)
	}
	if got := jsonNumber(report["redacted_gdpr"]); got < 1 {
		t.Errorf("chainIntegrityReport.redacted_gdpr = %d, want >= 1 (the receipted §12.8 redaction)", got)
	}
	if got := jsonNumber(report["broken"]); got != 0 {
		t.Errorf("chainIntegrityReport.broken = %d, want 0 (the redaction is receipt-authorized, not tamper)", got)
	}
	// The redacted row itself must surface its redacted_gdpr verdict on the
	// per-record chain extension, not merely in the aggregate tally.
	var sawRedactedRow bool
	for _, rec := range itemMaps(env) {
		unmapped, _ := rec["unmapped"].(map[string]any)
		chain, _ := unmapped["lenny_chain"].(map[string]any)
		if chain == nil {
			continue
		}
		if integrity, _ := chain["integrity"].(string); integrity == "redacted_gdpr" {
			sawRedactedRow = true
		}
	}
	if !sawRedactedRow {
		t.Error("no returned record carried unmapped.lenny_chain.integrity = redacted_gdpr")
	}
}

// markDeadLettered flips one audit_log row's ocsf_translation_state to
// dead_lettered under the tenant RLS context, so the §12.8 step-14 scan
// treats it as an untranslatable row to redact. ocsf_translation_state is
// a permitted bookkeeping column, so the UPDATE needs only the RLS tenant
// context rather than erasure mode.
func markDeadLettered(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant string, seq uint64) {
	t.Helper()
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenant); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE audit_log SET ocsf_translation_state='dead_lettered'
		 WHERE tenant_id=$1 AND sequence_number=$2`, tenant, int64(seq)); err != nil {
		t.Fatalf("mark dead_lettered: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// itemMaps decodes the §25.9 audit envelope's items[] into a slice of
// generic OCSF record maps.
func itemMaps(env map[string]any) []map[string]any {
	raw, _ := env["items"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		if m, ok := r.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// jsonNumber coerces a JSON-decoded number (float64) to int.
func jsonNumber(v any) int {
	f, _ := v.(float64)
	return int(f)
}
