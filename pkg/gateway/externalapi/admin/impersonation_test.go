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
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/impersonation"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// stubImpAppender is a minimal impersonation.AuditAppender for the admin
// handler tests. It can inject a §11.7 CMP-058 fail-closed error.
type stubImpAppender struct {
	events []string
	err    error
}

func (a *stubImpAppender) Append(_ context.Context, _, eventType string, _ json.RawMessage, _ time.Time) (audit.Row, error) {
	if a.err != nil {
		return audit.Row{}, a.err
	}
	a.events = append(a.events, eventType)
	return audit.Row{ID: "row"}, nil
}

type stubImpSigner struct{}

func (stubImpSigner) Sign(jwt.Claims) (string, error) { return "minted-bearer", nil }

// impRegionUnresolvable models the auditstore CMP-058 fail-closed error.
type impRegionUnresolvable struct{}

func (impRegionUnresolvable) Error() string   { return "region unresolvable" }
func (impRegionUnresolvable) Code() string    { return "PLATFORM_AUDIT_REGION_UNRESOLVABLE" }
func (impRegionUnresolvable) HTTPStatus() int { return 422 }

func newImpersonationAdmin(t *testing.T, app impersonation.AuditAppender) (*admin.Router, *userstore.Memory) {
	t.Helper()
	tenants := tenantstore.NewMemory()
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"})
	users := userstore.NewMemory()
	_ = users.Create(context.Background(), userstore.User{Subject: "alice@acme.com", TenantID: "acme", Roles: []pkgauth.Role{pkgauth.RoleUser}})
	svc := impersonation.New(impersonation.NewMemStore(), app, stubImpSigner{}, impersonation.Config{
		PlatformTenantID: "platform",
		MaxDuration:      time.Hour,
		Clock:            func() time.Time { return time.Date(2026, 6, 9, 0, 0, 0, 0, time.UTC) },
		NewID:            func() string { return "imp-test-1" },
	})
	router := admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 6, 9, 0, 0, 0, 0, time.UTC) },
	}).WithUsers(users).WithImpersonation(svc)
	return router, users
}

func startImpersonation(t *testing.T, h http.Handler, body admin.StartImpersonationRequest, as func(*http.Request) *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := as(httptest.NewRequest(http.MethodPost, "/v1/admin/impersonation", bytes.NewReader(b)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// TestImpersonation_StartHappyPath_spec_16_7 covers a platform-admin
// establishing an impersonation session: 200, a minted bearer, a session
// id, and a durable admin.impersonation_started row.
func TestImpersonation_StartHappyPath_spec_16_7(t *testing.T) {
	app := &stubImpAppender{}
	router, _ := newImpersonationAdmin(t, app)
	rr := startImpersonation(t, router.Handler(), admin.StartImpersonationRequest{
		TargetTenantID: "acme", TargetUserID: "alice@acme.com", Reason: "support", TicketRef: "SUP-9", DurationSeconds: 600,
	}, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["bearerToken"] != "minted-bearer" || resp["impersonationSessionId"] != "imp-test-1" {
		t.Fatalf("unexpected response: %v", resp)
	}
	if len(app.events) != 1 || app.events[0] != "admin.impersonation_started" {
		t.Fatalf("expected admin.impersonation_started, got %v", app.events)
	}
}

// TestImpersonation_TenantAdminForbidden_spec_13_3 covers that a
// tenant-admin cannot impersonate (cross-tenant by design) — 403.
func TestImpersonation_TenantAdminForbidden_spec_13_3(t *testing.T) {
	app := &stubImpAppender{}
	router, _ := newImpersonationAdmin(t, app)
	rr := startImpersonation(t, router.Handler(), admin.StartImpersonationRequest{
		TargetTenantID: "acme", TargetUserID: "alice@acme.com", Reason: "x", TicketRef: "T", DurationSeconds: 60,
	}, withTenantAdminPrincipal)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body %s", rr.Code, rr.Body.String())
	}
	if len(app.events) != 0 {
		t.Fatalf("no audit event must be written on a forbidden request, got %v", app.events)
	}
}

// TestImpersonation_MissingTarget_spec_13_3 covers a 404 for an unknown
// target tenant and an unknown target user, before any audit write.
func TestImpersonation_MissingTarget_spec_13_3(t *testing.T) {
	app := &stubImpAppender{}
	router, _ := newImpersonationAdmin(t, app)
	// Unknown tenant.
	rr := startImpersonation(t, router.Handler(), admin.StartImpersonationRequest{
		TargetTenantID: "globex", TargetUserID: "alice@acme.com", Reason: "x", TicketRef: "T", DurationSeconds: 60,
	}, withAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown tenant status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}
	// Unknown user under a known tenant.
	rr = startImpersonation(t, router.Handler(), admin.StartImpersonationRequest{
		TargetTenantID: "acme", TargetUserID: "ghost@acme.com", Reason: "x", TicketRef: "T", DurationSeconds: 60,
	}, withAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown user status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}
	if len(app.events) != 0 {
		t.Fatalf("no audit event before target resolution, got %v", app.events)
	}
}

// TestImpersonation_Validation_spec_16_7 covers the required-field guards.
func TestImpersonation_Validation_spec_16_7(t *testing.T) {
	app := &stubImpAppender{}
	router, _ := newImpersonationAdmin(t, app)
	cases := []admin.StartImpersonationRequest{
		{TargetUserID: "alice@acme.com", Reason: "x", TicketRef: "T", DurationSeconds: 60},            // no tenant
		{TargetTenantID: "acme", Reason: "x", TicketRef: "T", DurationSeconds: 60},                    // no user
		{TargetTenantID: "acme", TargetUserID: "alice@acme.com", TicketRef: "T", DurationSeconds: 60}, // no reason
		{TargetTenantID: "acme", TargetUserID: "alice@acme.com", Reason: "x", DurationSeconds: 60},    // no ticket
		{TargetTenantID: "acme", TargetUserID: "alice@acme.com", Reason: "x", TicketRef: "T"},         // no duration
	}
	for i, c := range cases {
		rr := startImpersonation(t, router.Handler(), c, withAdminPrincipal)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("case %d status = %d, want 400; body %s", i, rr.Code, rr.Body.String())
		}
	}
}

// TestImpersonation_RegionUnresolvable_spec_11_7 covers the CMP-058
// fail-closed mapping to 422 PLATFORM_AUDIT_REGION_UNRESOLVABLE.
func TestImpersonation_RegionUnresolvable_spec_11_7(t *testing.T) {
	app := &stubImpAppender{err: impRegionUnresolvable{}}
	router, _ := newImpersonationAdmin(t, app)
	rr := startImpersonation(t, router.Handler(), admin.StartImpersonationRequest{
		TargetTenantID: "acme", TargetUserID: "alice@acme.com", Reason: "x", TicketRef: "T", DurationSeconds: 60,
	}, withAdminPrincipal)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("PLATFORM_AUDIT_REGION_UNRESOLVABLE")) {
		t.Fatalf("body missing PLATFORM_AUDIT_REGION_UNRESOLVABLE: %s", rr.Body.String())
	}
}

// TestImpersonation_EndAndList_spec_16_7 covers ending an active session
// (emits admin.impersonation_ended) and the active-session listing.
func TestImpersonation_EndAndList_spec_16_7(t *testing.T) {
	app := &stubImpAppender{}
	router, _ := newImpersonationAdmin(t, app)
	h := router.Handler()

	// Establish a session.
	if rr := startImpersonation(t, h, admin.StartImpersonationRequest{
		TargetTenantID: "acme", TargetUserID: "alice@acme.com", Reason: "x", TicketRef: "T", DurationSeconds: 600,
	}, withAdminPrincipal); rr.Code != http.StatusOK {
		t.Fatalf("start status = %d; body %s", rr.Code, rr.Body.String())
	}

	// List active.
	listReq := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/impersonation", nil))
	listRR := httptest.NewRecorder()
	h.ServeHTTP(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Fatalf("list status = %d; body %s", listRR.Code, listRR.Body.String())
	}
	var list struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(listRR.Body.Bytes(), &list)
	if len(list.Items) != 1 || list.Items[0]["targetUserId"] != "alice@acme.com" {
		t.Fatalf("unexpected active list: %v", list.Items)
	}

	// End the session.
	endReq := withAdminPrincipal(httptest.NewRequest(http.MethodDelete, "/v1/admin/impersonation/imp-test-1", nil))
	endRR := httptest.NewRecorder()
	h.ServeHTTP(endRR, endReq)
	if endRR.Code != http.StatusOK {
		t.Fatalf("end status = %d; body %s", endRR.Code, endRR.Body.String())
	}
	if len(app.events) != 2 || app.events[1] != "admin.impersonation_ended" {
		t.Fatalf("expected started+ended, got %v", app.events)
	}

	// A second end is a 404 (the session is no longer active).
	endReq2 := withAdminPrincipal(httptest.NewRequest(http.MethodDelete, "/v1/admin/impersonation/imp-test-1", nil))
	endRR2 := httptest.NewRecorder()
	h.ServeHTTP(endRR2, endReq2)
	if endRR2.Code != http.StatusConflict {
		t.Fatalf("double-end status = %d, want 409; body %s", endRR2.Code, endRR2.Body.String())
	}
}
