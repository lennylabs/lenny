// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/erasurejob"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// fakeSaltRotator records the tenant ids it was asked to rotate and can
// return a canned error (e.g. the exempt sentinel).
type fakeSaltRotator struct {
	rotated []string
	err     error
}

func (f *fakeSaltRotator) RotateErasureSalt(_ context.Context, tenantID string) error {
	f.rotated = append(f.rotated, tenantID)
	return f.err
}

func newSaltRotateAdmin(t *testing.T, rotator admin.ErasureSaltRotator) (*admin.Router, *tenantstore.Memory, *recordingAudit) {
	t.Helper()
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	aud := &recordingAudit{}
	router := admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: aud,
	})
	if rotator != nil {
		router = router.WithErasureSaltRotation(rotator)
	}
	return router, tenants, aud
}

// spec: §12.8 line 857 — POST /v1/admin/tenants/{id}/rotate-erasure-salt
// rotates the salt and emits the security audit event. F-12.8.5.
func TestRotateErasureSaltOK_spec_12_8_857(t *testing.T) {
	rotator := &fakeSaltRotator{}
	router, _, aud := newSaltRotateAdmin(t, rotator)

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/acme/rotate-erasure-salt", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if len(rotator.rotated) != 1 || rotator.rotated[0] != "acme" {
		t.Fatalf("rotated = %v, want [acme]", rotator.rotated)
	}
	var sawAudit bool
	for _, e := range aud.snapshot() {
		if e.Type == "tenant.erasure_salt_rotated" && e.TargetResource == "acme" {
			sawAudit = true
		}
	}
	if !sawAudit {
		t.Error("§12.8 line 857: the security audit event tenant.erasure_salt_rotated was not emitted")
	}
}

// spec: §12.8 line 855 — an exempt tenant has no salt; the handler maps the
// sentinel to 409 BILLING_ERASURE_EXEMPT. F-12.8.5.
func TestRotateErasureSaltExempt_spec_12_8_855(t *testing.T) {
	rotator := &fakeSaltRotator{err: erasurejob.ErrBillingErasureExempt}
	router, _, _ := newSaltRotateAdmin(t, rotator)

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/acme/rotate-erasure-salt", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
}

// An unknown tenant is rejected with 404 before the rotator is called.
func TestRotateErasureSaltUnknownTenant(t *testing.T) {
	rotator := &fakeSaltRotator{}
	router, _, _ := newSaltRotateAdmin(t, rotator)

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/ghost/rotate-erasure-salt", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
	if len(rotator.rotated) != 0 {
		t.Errorf("rotator was called for an unknown tenant: %v", rotator.rotated)
	}
}

// spec: §12.8 line 857 — without a rotator wired, the route is absent (404
// from the mux), so a deployment that cannot rotate never advertises the
// endpoint. F-12.8.5.
func TestRotateErasureSaltRouteAbsentWhenUnwired(t *testing.T) {
	router, _, _ := newSaltRotateAdmin(t, nil)

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/acme/rotate-erasure-salt", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (route unregistered); body=%s", rr.Code, rr.Body.String())
	}
}
