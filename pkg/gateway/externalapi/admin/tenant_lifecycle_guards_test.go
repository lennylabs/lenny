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

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// newDefaultDeletionRouter builds an admin Router over a store holding
// an active `default` tenant, under the given TenancyMode.
func newDefaultDeletionRouter(t *testing.T, tenancyMode string) (http.Handler, *tenantstore.Memory) {
	t.Helper()
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(context.Background(), tenantstore.Tenant{
		ID: pkgauth.DefaultTenantID, State: tenantstore.TenantStateActive,
	}); err != nil {
		t.Fatal(err)
	}
	router := admin.NewRouter(tenants, admin.Options{
		Clock:       func() time.Time { return time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC) },
		Audit:       &recordingAudit{},
		TenancyMode: tenancyMode,
	}).WithSessions(memstore.New())
	return router.Handler(), tenants
}

func deleteTenant(h http.Handler, id string) *httptest.ResponseRecorder {
	req := withAdminPrincipal(httptest.NewRequest(http.MethodDelete, "/v1/admin/tenants/"+id, nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func forceDeleteTenant(h http.Handler, id string) *httptest.ResponseRecorder {
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/"+id+"/force-delete",
		bytes.NewReader([]byte(`{}`))))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// TestDefaultTenantDeletionRefusedOutsideMultiTenantMode_spec_12_8 pins
// the single-tenant `default` deletion guard on both delete paths. On a
// Router whose TenancyMode is `single` or empty, DELETE and force-delete
// of `default` return 409 INVALID_STATE_TRANSITION and leave the row
// `active`. With `multi`, both start the lifecycle, the case that fails
// against a guard that refuses `default` unconditionally.
// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle), 15.1
func TestDefaultTenantDeletionRefusedOutsideMultiTenantMode_spec_12_8(t *testing.T) {
	paths := map[string]func(http.Handler, string) *httptest.ResponseRecorder{
		"delete":       deleteTenant,
		"force-delete": forceDeleteTenant,
	}
	for pathName, do := range paths {
		for _, mode := range []string{"single", ""} {
			t.Run(pathName+"/mode="+mode, func(t *testing.T) {
				h, tenants := newDefaultDeletionRouter(t, mode)
				rr := do(h, pkgauth.DefaultTenantID)
				if rr.Code != http.StatusConflict {
					t.Fatalf("status = %d, want 409; body %s", rr.Code, rr.Body.String())
				}
				var env struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				_ = json.Unmarshal(rr.Body.Bytes(), &env)
				if env.Error.Code != "INVALID_STATE_TRANSITION" {
					t.Errorf("code = %q, want INVALID_STATE_TRANSITION", env.Error.Code)
				}
				row, err := tenants.Get(context.Background(), pkgauth.DefaultTenantID)
				if err != nil {
					t.Fatal(err)
				}
				if row.State != tenantstore.TenantStateActive {
					t.Errorf("state = %q, want active", row.State)
				}
			})
		}
		t.Run(pathName+"/mode=multi", func(t *testing.T) {
			h, tenants := newDefaultDeletionRouter(t, "multi")
			rr := do(h, pkgauth.DefaultTenantID)
			if rr.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want 202; body %s", rr.Code, rr.Body.String())
			}
			row, _ := tenants.Get(context.Background(), pkgauth.DefaultTenantID)
			if row.State != tenantstore.TenantStateDisabling {
				t.Errorf("state = %q, want disabling", row.State)
			}
		})
	}

	// A non-default tenant is deleted in single-tenant mode.
	h, tenants := newDefaultDeletionRouter(t, "single")
	if err := tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"}); err != nil {
		t.Fatal(err)
	}
	if rr := deleteTenant(h, "acme"); rr.Code != http.StatusAccepted {
		t.Errorf("single-tenant delete of acme: status = %d, want 202; body %s", rr.Code, rr.Body.String())
	}
}

// TestCreateTenantRefusesReservedPlatformID_spec_10_2 pins the admin-API
// half of the platform reservation: POST /v1/admin/tenants with the ID
// `platform` returns 422 RESERVED_IDENTIFIER with details.field `id` and
// details.value `platform` and creates no row, and `platform-eu` is
// created.
// spec: 10.2 (Authentication: `tenant_id` extraction), 15.1
func TestCreateTenantRefusesReservedPlatformID_spec_10_2(t *testing.T) {
	tenants := tenantstore.NewMemory()
	h := admin.NewRouter(tenants, admin.Options{Audit: &recordingAudit{}}).Handler()
	create := func(id string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"id": id})
		req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	rr := create("platform")
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != "RESERVED_IDENTIFIER" || env.Error.Details["field"] != "id" || env.Error.Details["value"] != "platform" {
		t.Errorf("error = %+v, want RESERVED_IDENTIFIER with field id and value platform", env.Error)
	}
	if _, err := tenants.Get(context.Background(), "platform"); err == nil {
		t.Error("POST created a platform tenant row")
	}

	if rr := create("platform-eu"); rr.Code != http.StatusCreated {
		t.Errorf("platform-eu: status = %d, want 201; body %s", rr.Code, rr.Body.String())
	}
}
