// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
)

// spec: §11.7 audit hash chain — admin mutations are hash-chained.

func TestChainAuditSinkCommitsAdminMutations(t *testing.T) {
	chains := audit.NewChainSet()
	sink := admin.NewChainAuditSink(chains, func() time.Time {
		return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	})
	store := tenantstore.NewMemory()
	router := admin.NewRouter(store, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: sink,
	})

	// Create a tenant — should commit one chained audit row.
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme", DisplayName: "Acme"})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)),
	))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d, body=%s", rr.Code, rr.Body.String())
	}

	// The actor is platform-admin (tenant "platform").
	chain := chains.Chain("platform")
	if chain == nil {
		t.Fatal("platform chain not created")
	}
	if chain.Len() != 1 {
		t.Fatalf("chain length: got %d, want 1", chain.Len())
	}
	rows := chain.Rows()
	if rows[0].EventType != "admin.tenant.created" {
		t.Errorf("event type: %q", rows[0].EventType)
	}
	if res := chain.Verify(); res.Integrity != audit.ChainVerified {
		t.Errorf("chain should verify: got %q (%s)", res.Integrity, res.Detail)
	}
}

func TestChainAuditSinkChainsMultipleEvents(t *testing.T) {
	chains := audit.NewChainSet()
	sink := admin.NewChainAuditSink(chains, func() time.Time {
		return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	})
	store := tenantstore.NewMemory()
	router := admin.NewRouter(store, admin.Options{Audit: sink})

	for _, id := range []string{"acme", "globex", "initech"} {
		body, _ := json.Marshal(admin.TenantPayload{ID: id})
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, withAdminPrincipal(
			httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)),
		))
		if rr.Code != http.StatusCreated {
			t.Fatalf("create %s: %d", id, rr.Code)
		}
	}
	chain := chains.Chain("platform")
	if chain.Len() != 3 {
		t.Fatalf("chain length: got %d, want 3", chain.Len())
	}
	if res := chain.Verify(); res.Integrity != audit.ChainVerified {
		t.Errorf("3-row chain should verify: %q", res.Integrity)
	}
	// Seq numbers must be 1, 2, 3.
	for i, row := range chain.Rows() {
		if row.Seq != uint64(i+1) {
			t.Errorf("row %d seq: got %d", i, row.Seq)
		}
	}
}

func TestChainAuditSinkEmitDirect(t *testing.T) {
	chains := audit.NewChainSet()
	sink := admin.NewChainAuditSink(chains, nil)
	sink.EmitAdminEvent(context.Background(), admin.AuditEvent{
		Type:           "admin.tenant.created",
		ActorSubject:   "admin@acme.com",
		ActorTenantID:  "acme",
		TargetResource: "acme",
		At:             time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	chain := chains.Chain("acme")
	if chain == nil || chain.Len() != 1 {
		t.Fatalf("acme chain: %v", chain)
	}
}
