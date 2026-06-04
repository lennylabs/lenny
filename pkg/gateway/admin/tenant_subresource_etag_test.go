// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §15.1 lines 1207-1224 — the §10.6 rbac-config and §9.2
// elicitation-content-integrity admin sub-resources are stored on the
// tenant row, so their ETag is the tenant's version. These cases verify
// the contract end to end and that a write through any of the three
// surfaces advances the shared version.

func putSubresourceRaw(t *testing.T, h http.Handler, path, ifMatch string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut, path, bytes.NewReader(b)))
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func getETagHeader(t *testing.T, h http.Handler, path string) (int, string) {
	t.Helper()
	g := withAdminPrincipal(httptest.NewRequest(http.MethodGet, path, nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, g)
	return rr.Code, rr.Header().Get("ETag")
}

// TestRBACConfigETag_spec_15_1_1207 covers the §15.1 contract for the
// tenant rbac-config sub-resource.
func TestRBACConfigETag_spec_15_1_1207(t *testing.T) {
	const path = "/v1/admin/tenants/acme/rbac-config"

	t.Run("GetCarriesTenantETag", func(t *testing.T) {
		router, _ := seedEtagTenant(t, "acme")
		code, etag := getETagHeader(t, router.Handler(), path)
		if code != http.StatusOK || etag != `"1"` {
			t.Fatalf("get rbac-config: code=%d etag=%q, want 200 / \"1\"", code, etag)
		}
	})

	t.Run("PutMissingIfMatchIs428", func(t *testing.T) {
		router, _ := seedEtagTenant(t, "acme")
		rr := putSubresourceRaw(t, router.Handler(), path, "",
			admin.RBACConfigPayload{NoEnvironmentPolicy: "deny-all"})
		if rr.Code != http.StatusPreconditionRequired {
			t.Fatalf("status: got %d, want 428; body=%s", rr.Code, rr.Body.String())
		}
		assertErrorCode(t, rr, "ETAG_REQUIRED")
	})

	t.Run("PutMatchingBumpsETag", func(t *testing.T) {
		router, store := seedEtagTenant(t, "acme")
		rr := putSubresourceRaw(t, router.Handler(), path, `"1"`,
			admin.RBACConfigPayload{NoEnvironmentPolicy: "allow-all"})
		if rr.Code != http.StatusOK {
			t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
		if got := rr.Header().Get("ETag"); got != `"2"` {
			t.Errorf("new ETag: got %q, want \"2\"", got)
		}
		row, _ := store.Get(context.Background(), "acme")
		if row.Version != 2 || row.NoEnvironmentPolicy != "allow-all" {
			t.Errorf("stored: version=%d policy=%q", row.Version, row.NoEnvironmentPolicy)
		}
	})

	// A write through the tenant resource advances the version the
	// rbac-config GET reports, since both read the same row.
	t.Run("SharedVersionWithTenantResource", func(t *testing.T) {
		router, _ := seedEtagTenant(t, "acme")
		dn := "Renamed"
		put := putTenantRaw(t, router.Handler(), "acme", `"1"`, admin.UpdateTenantRequest{DisplayName: &dn})
		if put.Code != http.StatusOK {
			t.Fatalf("tenant PUT: %d body=%s", put.Code, put.Body.String())
		}
		_, etag := getETagHeader(t, router.Handler(), path)
		if etag != `"2"` {
			t.Errorf("rbac-config ETag after tenant write: got %q, want \"2\"", etag)
		}
	})
}

// TestElicitationIntegrityETag_spec_15_1_1207 covers the §15.1 contract for
// the tenant elicitation-content-integrity sub-resource.
func TestElicitationIntegrityETag_spec_15_1_1207(t *testing.T) {
	const path = "/v1/admin/tenants/acme/elicitation-content-integrity"

	t.Run("GetCarriesTenantETag", func(t *testing.T) {
		router, _ := seedEtagTenant(t, "acme")
		code, etag := getETagHeader(t, router.Handler(), path)
		if code != http.StatusOK || etag != `"1"` {
			t.Fatalf("get elicitation: code=%d etag=%q, want 200 / \"1\"", code, etag)
		}
	})

	t.Run("PutMissingIfMatchIs428", func(t *testing.T) {
		router, _ := seedEtagTenant(t, "acme")
		rr := putSubresourceRaw(t, router.Handler(), path, "",
			map[string]any{"mode": "enforce"})
		if rr.Code != http.StatusPreconditionRequired {
			t.Fatalf("status: got %d, want 428; body=%s", rr.Code, rr.Body.String())
		}
		assertErrorCode(t, rr, "ETAG_REQUIRED")
	})

	t.Run("PutStaleIfMatchIs412", func(t *testing.T) {
		router, _ := seedEtagTenant(t, "acme")
		rr := putSubresourceRaw(t, router.Handler(), path, `"999"`,
			map[string]any{"mode": "enforce"})
		if rr.Code != http.StatusPreconditionFailed {
			t.Fatalf("status: got %d, want 412; body=%s", rr.Code, rr.Body.String())
		}
		assertErrorCode(t, rr, "ETAG_MISMATCH")
	})

	t.Run("PutMatchingBumpsETag", func(t *testing.T) {
		router, store := seedEtagTenant(t, "acme")
		// detect-only is weaker than enforce, so a justification is required.
		rr := putSubresourceRaw(t, router.Handler(), path, `"1"`,
			map[string]any{"mode": "detect-only", "justification": "lowering for triage"})
		if rr.Code != http.StatusOK {
			t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
		if got := rr.Header().Get("ETag"); got != `"2"` {
			t.Errorf("new ETag: got %q, want \"2\"", got)
		}
		row, _ := store.Get(context.Background(), "acme")
		if row.Version != 2 || row.ElicitationContentIntegrity != "detect-only" {
			t.Errorf("stored: version=%d mode=%q", row.Version, row.ElicitationContentIntegrity)
		}
	})
}

// assert the §12.8 tombstone path also bumps the version, so a stale tag
// referencing a pre-deletion read cannot resurrect the row.
func TestTenantSoftDeleteBumpsVersion_spec_15_1_1207(t *testing.T) {
	store := tenantstore.NewMemory()
	if err := store.Create(context.Background(), tenantstore.Tenant{ID: "acme"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	before, _ := store.Get(context.Background(), "acme")
	if err := store.SoftDelete(context.Background(), "acme", before.CreatedAt.Add(1)); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	after, _ := store.Get(context.Background(), "acme")
	if after.Version != before.Version+1 {
		t.Errorf("version after soft delete: got %d, want %d", after.Version, before.Version+1)
	}
}
