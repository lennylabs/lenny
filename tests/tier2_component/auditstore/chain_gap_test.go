//go:build component

// SPDX-License-Identifier: MIT

package auditstore_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
	"github.com/lennylabs/lenny/tests/testinfra/auditquery"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// chainGapSink records the admin events the §25.9 list emits.
type chainGapSink struct {
	mu     sync.Mutex
	events []string
}

func (s *chainGapSink) EmitAdminEvent(_ context.Context, e admin.AuditEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e.Type)
}

func (s *chainGapSink) has(eventType string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.events {
		if e == eventType {
			return true
		}
	}
	return false
}

// deleteAuditRowAt removes one committed audit row in erasure mode, the
// direct-write path the §11.7 lenny_audit_immutability trigger admits.
// It is the private-database counterpart of the tier-8
// TestAuditChainGapDetection fault injector.
func deleteAuditRowAt(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant string, seq uint64) {
	t.Helper()
	err := pgtenant.InTx(ctx, pg.Pool, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL lenny.erasure_mode = 'true'"); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, "DELETE FROM audit_log WHERE tenant_id = $1 AND sequence_number = $2", tenant, seq)
		if err == nil && tag.RowsAffected() != 1 {
			t.Errorf("erasure-mode DELETE of seq %d removed %d rows, want 1", seq, tag.RowsAffected())
		}
		return err
	})
	if err != nil {
		t.Fatalf("delete audit row %d for %q: %v", seq, tenant, err)
	}
}

// spec: 25.9 (Audit Log Query API, Chain gap detection), 11.7 (Audit
// Logging, Authoritative total order)
// diagnosis: a committed audit row removed from the Postgres chain was not
// reported broken by the §25.9 audit-events list. The test seals a
// three-row chain through auditstore.Store on a private Postgres, deletes
// the middle row in erasure mode, and reads the list through the
// production admin router. The row after the gap carries a prev_hash that
// does not link to its in-order predecessor, so §25.9 requires it to be
// broken, chainIntegrityReport.broken to be 1, and
// audit.chain_integrity_broken_detected to be emitted. A gap_suspected
// verdict on that row is BUILD-GAPS F-25.9.21 (sequence checked before
// the link). The same scenario runs on Kind as tier-8
// TestAuditChainGapDetection.
func TestAdminAuditListReportsDeletedRowBrokenOverPostgres(t *testing.T) {
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
	for i := 3; i >= 1; i-- {
		appendAuditRow(t, ctx, store, "acme", "admin.tenant.updated", "alice@acme.com", "tenant/acme",
			adminQueryClock.Add(-time.Duration(i)*time.Minute))
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

	sealed := read()
	if len(sealed.Events) != 3 || sealed.Report.Verified != 3 || len(sealed.NotVerified()) != 0 {
		t.Fatalf("sealed chain: %d rows, report %+v, unverified %+v; want 3 verified rows",
			len(sealed.Events), sealed.Report, sealed.NotVerified())
	}
	if sink.has("audit.chain_integrity_broken_detected") {
		t.Fatalf("the broken-chain event fired on an intact chain")
	}

	deleteAuditRowAt(t, ctx, pg, "acme", 2)

	gap := read()
	if got, ok := gap.Integrity(1); !ok || got != "verified" {
		t.Errorf("row 1 (before the gap) = %q (listed %v), want verified", got, ok)
	}
	if got, ok := gap.Integrity(3); !ok || got != "broken" {
		t.Errorf("row 3 (after the gap) = %q (listed %v), want broken: its prev_hash does not link to row 1 "+
			"(BUILD-GAPS F-25.9.21)", got, ok)
	}
	if gap.Report.Broken != 1 || gap.Report.GapSuspected != 0 {
		t.Errorf("chainIntegrityReport = %+v, want broken 1 and gap_suspected 0", gap.Report)
	}
	if !sink.has("audit.chain_integrity_broken_detected") {
		t.Errorf("audit.chain_integrity_broken_detected not emitted after the deletion; events %v", sink.events)
	}
}
