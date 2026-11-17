//go:build component

// SPDX-License-Identifier: MIT

package auditstore_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/storage/deadletterredaction"
	"github.com/lennylabs/lenny/tests/testinfra/auditquery"
)

// spec: 25.9 (Audit Log Query API, Chain gap detection), 11.7 (Audit
// Logging), 12.8 (Compliance Interfaces, OCSF dead-letter PII redaction)
// diagnosis: a committed audit row removed directly after a receipted GDPR
// redaction was not reported broken by the §25.9 list over Postgres, or the
// redaction itself broke its successor's link. The test seals four rows
// through auditstore.Store, redacts row 2 through the §12.8 dead-letter
// redaction with a signed receipt, and reads the list through the
// production admin router: row 2 must be redacted_gdpr and row 3 verified,
// because the store restores row 2's pre-redaction hash, which row 3's
// prev_hash carries. After an erasure-mode DELETE of row 3, row 4 links to
// the absent row 3, so §25.9 requires it to be broken and the broken-chain
// event to fire. A gap_suspected verdict on row 4 is BUILD-GAPS F-25.9.22
// (the link check skipped after a redacted predecessor).
func TestAdminAuditListReportsDeletionAfterRedactionBrokenOverPostgres(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	store := auditstore.New(pg.Router(t))
	sink := &chainGapSink{}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return adminQueryClock },
		Audit: sink,
	}).WithAuditLog(store)
	ctx := context.Background()
	seedTenant(t, ctx, pg, "acme")

	at := adminQueryClock.Add(-10 * time.Minute)
	for _, payload := range []string{
		`{"actor_subject":"bob@acme.com"}`,
		`{"user_id":"alice@acme.com","note":"secret-a"}`,
		`{"actor_subject":"bob@acme.com"}`,
		`{"actor_subject":"bob@acme.com"}`,
	} {
		if _, err := store.Append(ctx, "acme", "admin.tenant.updated", json.RawMessage(payload), at); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', 'acme', true)"); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE audit_log SET ocsf_translation_state='dead_lettered'
		 WHERE tenant_id='acme' AND sequence_number = 2`); err != nil {
		t.Fatalf("mark dead_lettered: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	svc := deadletterredaction.New(deadletterredaction.Config{
		Store:    store,
		Emit:     store,
		Signer:   deadletterredaction.NewHMACReceiptSigner("boot", []byte("0123456789abcdef0123456789abcdef")),
		Classify: func(audit.Row) string { return "class_mapping_missing" },
		Clock:    func() time.Time { return at },
	})
	if n, err := svc.RedactForUser(ctx, "acme", "alice@acme.com"); err != nil || n != 1 {
		t.Fatalf("RedactForUser = %d, %v; want 1 redacted row", n, err)
	}

	read := func() auditquery.Chain {
		t.Helper()
		since := adminQueryClock.Add(-time.Hour).Format(time.RFC3339)
		until := adminQueryClock.Add(time.Hour).Format(time.RFC3339)
		chain, err := auditquery.ReadChain("acme", since, until, func(path string) (int, []byte, error) {
			rr := httptest.NewRecorder()
			router.Handler().ServeHTTP(rr, asPlatformAdmin(httptest.NewRequest(http.MethodGet, path, nil)))
			return rr.Code, rr.Body.Bytes(), nil
		})
		if err != nil {
			t.Fatalf("read the §25.9 list: %v", err)
		}
		return chain
	}

	redacted := read()
	for seq, want := range map[uint64]string{1: "verified", 2: "redacted_gdpr", 3: "verified", 4: "verified"} {
		if got, ok := redacted.Integrity(seq); !ok || got != want {
			t.Errorf("before the deletion, row %d = %q (listed %v), want %s", seq, got, ok, want)
		}
	}
	if redacted.Report.Broken != 0 {
		t.Fatalf("the redacted chain reports %d broken rows, want 0: %+v", redacted.Report.Broken, redacted.NotVerified())
	}
	if sink.has("audit.chain_integrity_broken_detected") {
		t.Fatalf("the broken-chain event fired on a lawfully redacted chain")
	}

	deleteAuditRowAt(t, ctx, pg, "acme", 3)

	gap := read()
	if got, ok := gap.Integrity(2); !ok || got != "redacted_gdpr" {
		t.Errorf("row 2 (the redaction) = %q (listed %v), want redacted_gdpr", got, ok)
	}
	if got, ok := gap.Integrity(4); !ok || got != "broken" {
		t.Errorf("row 4 (after the deleted row) = %q (listed %v), want broken: its prev_hash does not link to "+
			"row 2's preserved hash (BUILD-GAPS F-25.9.22)", got, ok)
	}
	if gap.Report.Broken != 1 || gap.Report.GapSuspected != 0 {
		t.Errorf("chainIntegrityReport = %+v, want broken 1 and gap_suspected 0", gap.Report)
	}
	if !sink.has("audit.chain_integrity_broken_detected") {
		t.Errorf("audit.chain_integrity_broken_detected not emitted after the deletion; events %v", sink.events)
	}
}
