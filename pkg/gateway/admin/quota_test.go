// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
)

// fakeQuotaReconciler is a test double for the §12.4 reconciliation seam.
type fakeQuotaReconciler struct {
	gotScope admin.QuotaReconcileScope
	result   admin.QuotaReconcileResult
	err      error
}

func (f *fakeQuotaReconciler) Reconcile(_ context.Context, scope admin.QuotaReconcileScope) (admin.QuotaReconcileResult, error) {
	f.gotScope = scope
	return f.result, f.err
}

func newQuotaAdmin(t *testing.T, qr admin.QuotaReconciler) *admin.Router {
	t.Helper()
	r := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	})
	if qr != nil {
		r = r.WithQuotaReconciler(qr)
	}
	return r
}

// spec: §15.1 line 879 / §12.4 line 216 — the endpoint reaches the
// reconciler seam and returns the per-tenant MAX-rule summary.
func TestQuotaReconcileAllTenants(t *testing.T) {
	fake := &fakeQuotaReconciler{result: admin.QuotaReconcileResult{
		TenantsReconciled: 2,
		CountersWritten:   2,
		Tenants: []admin.QuotaTenantReconcileResult{
			{TenantID: "acme", CheckpointValue: 100, InMemoryValue: 140, WrittenValue: 140},
		},
	}}
	router := newQuotaAdmin(t, fake)
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/quota/reconcile",
		map[string]any{"allTenants": true})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if !fake.gotScope.AllTenants || fake.gotScope.TenantID != "" {
		t.Fatalf("scope = %+v, want AllTenants", fake.gotScope)
	}
	var out admin.QuotaReconcileResult
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.TenantsReconciled != 2 || out.Tenants[0].WrittenValue != 140 {
		t.Fatalf("unexpected result: %+v", out)
	}
}

// spec: §24.6 line 99 — the per-tenant `--tenant <id>` scope reaches the
// reconciler with the named tenant.
func TestQuotaReconcileSingleTenant(t *testing.T) {
	fake := &fakeQuotaReconciler{result: admin.QuotaReconcileResult{TenantsReconciled: 1}}
	router := newQuotaAdmin(t, fake)
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/quota/reconcile",
		map[string]any{"tenantId": "acme"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if fake.gotScope.AllTenants || fake.gotScope.TenantID != "acme" {
		t.Fatalf("scope = %+v, want TenantID=acme", fake.gotScope)
	}
}

// spec: §11.2 / F-11.2.4 — with no Postgres-checkpoint reconciler wired,
// the spec-registered route reports the dependency rather than no-op'ing.
func TestQuotaReconcileUnavailableWhenSeamUnwired(t *testing.T) {
	router := newQuotaAdmin(t, nil)
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/quota/reconcile",
		map[string]any{"allTenants": true})
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &env)
	if env.Error.Code != "QUOTA_RECONCILE_UNAVAILABLE" {
		t.Fatalf("code = %q, want QUOTA_RECONCILE_UNAVAILABLE; body=%s", env.Error.Code, rr.Body.String())
	}
}

// spec: §24.6 — exactly one scope. Neither and both are rejected before
// the reconciler is consulted.
func TestQuotaReconcileScopeValidation(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
	}{
		{"neither", map[string]any{}},
		{"both", map[string]any{"allTenants": true, "tenantId": "acme"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeQuotaReconciler{}
			router := newQuotaAdmin(t, fake)
			rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/quota/reconcile", tc.body)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
			}
			if (fake.gotScope != admin.QuotaReconcileScope{}) {
				t.Fatalf("reconciler should not be consulted on invalid scope; got %+v", fake.gotScope)
			}
		})
	}
}

// spec: §24.6 — a per-tenant reconcile naming an absent tenant maps the
// reconciler's ErrQuotaTenantNotFound onto 404.
func TestQuotaReconcileTenantNotFound(t *testing.T) {
	fake := &fakeQuotaReconciler{err: admin.ErrQuotaTenantNotFound}
	router := newQuotaAdmin(t, fake)
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/quota/reconcile",
		map[string]any{"tenantId": "ghost"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}
