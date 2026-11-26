// SPDX-License-Identifier: MIT

//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit/auditstore"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/wait"
)

// adminCall issues one platform-admin request against base and decodes the
// JSON response.
func adminCall(t *testing.T, base, method, path string, body any) (int, map[string]any) {
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
	req.Header.Set("X-Lenny-Roles", "platform-admin")
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

// TestRedactionReceiptFromAStoppedReplicaVerifiesOnAnother drives a user
// erasure through one gateway process, which redacts a dead-lettered audit
// row under a receipt signed in the KMS, stops that process, and reads the
// chain through a second gateway process over the same Postgres and the
// same KMS master key. The second process reports the row redacted_gdpr
// with no broken row, and serves the key the receipt names on
// GET /v1/admin/audit-receipt-keys. Before the receipt key moved into the
// KMS, each process drew its own key, so no receipt verified after the
// process that signed it exited.
//
// spec: §12.8 (Compliance Interfaces), §15.1 (REST API)
// diagnosis: a receipt signed by one gateway process does not verify in
// another process of the same install: the processes do not load the same
// platform:audit-signing key, the receipt records a key identifier the
// second process does not trust, or the receipt key route does not serve
// the key the receipt names.
func TestRedactionReceiptFromAStoppedReplicaVerifiesOnAnother(t *testing.T) {
	t.Parallel()
	gateway.SkipUnlessAvailable(t)
	pg := sharedDatabase(t)
	args := []string{"--no-environment-policy", "allow-all", "--dev-mode", "--postgres-dsn=" + pg.DSN,
		"--postgres-billing-audit-ddl-dsn=" + pg.DSN, "--tenant-deletion-interval=1s"}
	first := gateway.StartWith(t, args...)
	base := first.BaseURL()

	const user = "alice@acme.com"
	if code, _ := adminCall(t, base, http.MethodPost, "/v1/admin/bootstrap", map[string]any{
		"tenants": []map[string]any{{"id": "acme", "displayName": "Acme Corp"}},
		"users":   []map[string]any{{"subject": user, "tenantId": "acme", "email": user, "roles": []string{"tenant-admin"}}},
	}); code != http.StatusOK {
		t.Fatalf("bootstrap: status %d", code)
	}
	ctx := context.Background()
	store := auditstore.New(pg.Router(t))
	row, err := store.Append(ctx, "acme", "session.created", json.RawMessage(`{"user_id":"alice@acme.com","note":"secret"}`), time.Now().UTC())
	if err != nil {
		t.Fatalf("seed audit row: %v", err)
	}
	markDeadLettered(t, ctx, pg, "acme", row.Seq)

	code, resp := adminCall(t, base, http.MethodPost, "/v1/admin/users/"+user+"/erase", map[string]any{"tenantId": "acme"})
	if code != http.StatusAccepted {
		t.Fatalf("erase user: status %d (%v)", code, resp)
	}
	jobID, _ := resp["jobId"].(string)
	wait.For(t, 15*time.Second, "erasure job reaches completed", func() (bool, error) {
		code, got := adminCall(t, base, http.MethodGet, "/v1/admin/erasure-jobs/"+jobID, nil)
		if code != http.StatusOK {
			return false, nil
		}
		phase, _ := got["phase"].(string)
		if phase == "failed" {
			t.Fatalf("erasure job failed: %v", got)
		}
		return phase == "completed", nil
	})
	receipts, err := store.Receipts(ctx, "acme")
	if err != nil {
		t.Fatalf("Receipts: %v", err)
	}
	rcpt, ok := receipts[row.Seq]
	if !ok || len(rcpt.Signature) != 64 || rcpt.KeyID == "" {
		t.Fatalf("receipt at seq %d = %+v, want a 64-byte signature and a key identifier", row.Seq, rcpt)
	}
	first.Stop(t)

	second := gateway.StartWith(t, args...)
	since := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	code, env := adminCall(t, second.BaseURL(), http.MethodGet, "/v1/admin/audit-events?tenantId=acme&since="+url.QueryEscape(since), nil)
	if code != http.StatusOK {
		t.Fatalf("audit query on the second process: status %d (%v)", code, env)
	}
	report, _ := env["chainIntegrityReport"].(map[string]any)
	if got := jsonNumber(report["redacted_gdpr"]); got < 1 {
		t.Errorf("chainIntegrityReport.redacted_gdpr = %d, want >= 1 (report %v)", got, report)
	}
	if got := jsonNumber(report["broken"]); got != 0 {
		t.Errorf("chainIntegrityReport.broken = %d, want 0 (report %v)", got, report)
	}

	code, keys := adminCall(t, second.BaseURL(), http.MethodGet, "/v1/admin/audit-receipt-keys", nil)
	if code != http.StatusOK {
		t.Fatalf("receipt keys: status %d (%v)", code, keys)
	}
	served := false
	list, _ := keys["keys"].([]any)
	for _, k := range list {
		entry, _ := k.(map[string]any)
		if id, _ := entry["keyId"].(string); id == rcpt.KeyID {
			served = true
		}
	}
	if !served {
		t.Errorf("receipt key %q is not among the served keys %v", rcpt.KeyID, list)
	}
}
