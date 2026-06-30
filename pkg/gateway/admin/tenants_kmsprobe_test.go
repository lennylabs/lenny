// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
)

// fakeKMSProbe is the admin-test wiring for §12.5 — a tunable probe
// that lets each test drive the success path, the failure path, and
// the last-success-timestamp surfacing in isolation.
type fakeKMSProbe struct {
	mu          sync.Mutex
	failNext    error
	probes      []string
	lastSuccess map[string]time.Time
}

func newFakeKMSProbe() *fakeKMSProbe {
	return &fakeKMSProbe{lastSuccess: map[string]time.Time{}}
}

func (p *fakeKMSProbe) ProbeAvailability(_ context.Context, tenantID, workspaceTier string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.probes = append(p.probes, tenantID+":"+workspaceTier)
	if workspaceTier != "T4" {
		return nil
	}
	if p.failNext != nil {
		err := p.failNext
		p.failNext = nil
		return err
	}
	p.lastSuccess[tenantID] = time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	return nil
}

func (p *fakeKMSProbe) LastProbeSuccess(tenantID string) (time.Time, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	t, ok := p.lastSuccess[tenantID]
	return t, ok
}

func newProbeAdminServer(t *testing.T, probe admin.KMSProbe) (*admin.Router, *tenantstore.Memory) {
	t.Helper()
	store := tenantstore.NewMemory()
	router := admin.NewRouter(store, admin.Options{
		Clock: func() time.Time {
			return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		},
	}).WithKMSProbe(probe)
	return router, store
}

// spec: §12.5 / §15.1 — `PUT /v1/admin/tenants/{id}` with
// workspaceTier T4 runs the KMS availability probe before persisting.
func TestUpdateTenantT4PromotionProbeSuccess(t *testing.T) {
	probe := newFakeKMSProbe()
	router, store := newProbeAdminServer(t, probe)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme", WorkspaceTier: "T3"})

	body, _ := json.Marshal(map[string]any{"workspaceTier": "T4"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut, "/v1/admin/tenants/acme", bytes.NewReader(body)))
	injectAdminIfMatch(t, router.Handler(), req)
	rec := httptest.NewRecorder()
	router.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp admin.TenantPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.WorkspaceTier != "T4" {
		t.Errorf("workspaceTier = %q, want T4", resp.WorkspaceTier)
	}
	if resp.T4KmsLastProbeSuccessAt == "" {
		t.Error("response missing t4KmsLastProbeSuccessAt for T4 tenant")
	}
	if len(probe.probes) != 1 || probe.probes[0] != "acme:T4" {
		t.Errorf("probes = %v, want [acme:T4]", probe.probes)
	}
}

func TestUpdateTenantT4PromotionProbeFailureRejects(t *testing.T) {
	probe := newFakeKMSProbe()
	probe.failNext = errors.New("simulated KMS probe failure")
	router, store := newProbeAdminServer(t, probe)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme", WorkspaceTier: "T3"})

	body, _ := json.Marshal(map[string]any{"workspaceTier": "T4"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut, "/v1/admin/tenants/acme", bytes.NewReader(body)))
	injectAdminIfMatch(t, router.Handler(), req)
	rec := httptest.NewRecorder()
	router.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Error.Code != "CLASSIFICATION_CONTROL_VIOLATION" {
		t.Errorf("code = %q, want CLASSIFICATION_CONTROL_VIOLATION", env.Error.Code)
	}
	if env.Error.Details["reason"] != "kms_probe_failed" {
		t.Errorf("details.reason = %v, want kms_probe_failed", env.Error.Details["reason"])
	}
	// Tenant must remain at the prior tier — the failed probe runs
	// before persistence.
	got, _ := store.Get(nil, "acme")
	if got.WorkspaceTier != "T3" {
		t.Errorf("post-rejection workspaceTier = %q, want T3", got.WorkspaceTier)
	}
}

func TestUpdateTenantT4IdempotentReassertProbe(t *testing.T) {
	probe := newFakeKMSProbe()
	router, store := newProbeAdminServer(t, probe)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme", WorkspaceTier: "T4"})

	body, _ := json.Marshal(map[string]any{"workspaceTier": "T4"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut, "/v1/admin/tenants/acme", bytes.NewReader(body)))
	injectAdminIfMatch(t, router.Handler(), req)
	rec := httptest.NewRecorder()
	router.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(probe.probes) != 1 || probe.probes[0] != "acme:T4" {
		t.Errorf("idempotent re-assert must probe once, got %v", probe.probes)
	}
}

func TestUpdateTenantNonT4DoesNotProbe(t *testing.T) {
	probe := newFakeKMSProbe()
	router, store := newProbeAdminServer(t, probe)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme", WorkspaceTier: "T2"})

	body, _ := json.Marshal(map[string]any{"workspaceTier": "T3"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut, "/v1/admin/tenants/acme", bytes.NewReader(body)))
	injectAdminIfMatch(t, router.Handler(), req)
	rec := httptest.NewRecorder()
	router.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(probe.probes) != 0 {
		t.Errorf("T3 promotion must not probe, got %v", probe.probes)
	}
}

func TestGetTenantSurfacesT4ProbeTimestamp(t *testing.T) {
	probe := newFakeKMSProbe()
	probe.lastSuccess["acme"] = time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	router, store := newProbeAdminServer(t, probe)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme", WorkspaceTier: "T4"})

	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/tenants/acme", nil))
	rec := httptest.NewRecorder()
	router.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp admin.TenantPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.T4KmsLastProbeSuccessAt == "" {
		t.Error("response missing t4KmsLastProbeSuccessAt for T4 tenant")
	}
}

func TestGetTenantOmitsT4ProbeTimestampForNonT4(t *testing.T) {
	probe := newFakeKMSProbe()
	probe.lastSuccess["acme"] = time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	router, store := newProbeAdminServer(t, probe)
	_ = store.Create(nil, tenantstore.Tenant{ID: "acme", WorkspaceTier: "T3"})

	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/tenants/acme", nil))
	rec := httptest.NewRecorder()
	router.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp admin.TenantPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.T4KmsLastProbeSuccessAt != "" {
		t.Errorf("T3 tenant must not surface t4KmsLastProbeSuccessAt; got %q", resp.T4KmsLastProbeSuccessAt)
	}
}
