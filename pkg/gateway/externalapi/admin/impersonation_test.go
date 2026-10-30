// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/impersonation"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/impersonation/impersonationtest"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
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

func (stubImpSigner) Sign(context.Context, jwt.Claims) (string, error) { return "minted-bearer", nil }

// impRegionUnresolvable models the auditstore CMP-058 fail-closed error.
type impRegionUnresolvable struct{}

func (impRegionUnresolvable) Error() string   { return "region unresolvable" }
func (impRegionUnresolvable) Code() string    { return "PLATFORM_AUDIT_REGION_UNRESOLVABLE" }
func (impRegionUnresolvable) HTTPStatus() int { return 422 }

// impersonationFixture is the admin router over the real impersonation
// service and the in-memory issued-token fakes.
type impersonationFixture struct {
	router *admin.Router
	users  *userstore.Memory
	tokens *impersonationtest.IssuedTokens
	cache  *impersonationtest.RevocationCache
	reads  *impersonationtest.CrossTenantReads
	clock  *time.Time
}

func newImpersonationFixture(t *testing.T, app impersonation.AuditAppender) *impersonationFixture {
	t.Helper()
	tenants := tenantstore.NewMemory()
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"})
	users := userstore.NewMemory()
	_ = users.Create(context.Background(), userstore.User{Subject: "alice@acme.com", TenantID: "acme", Roles: []pkgauth.Role{pkgauth.RoleUser}})
	now := time.Date(2026, 6, 9, 0, 0, 0, 0, time.UTC)
	f := &impersonationFixture{
		users:  users,
		tokens: &impersonationtest.IssuedTokens{},
		cache:  &impersonationtest.RevocationCache{},
		reads:  &impersonationtest.CrossTenantReads{},
		clock:  &now,
	}
	ids := 0
	svc := impersonation.New(f.tokens, f.cache, f.reads, app, stubImpSigner{}, impersonation.Config{
		PlatformTenantID: "platform",
		MaxDuration:      time.Hour,
		Clock:            func() time.Time { return *f.clock },
		NewID: func() string {
			ids++
			if ids == 1 {
				return "imp-test-1"
			}
			return "imp-test-" + string(rune('0'+ids))
		},
	})
	f.router = admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return *f.clock },
	}).WithUsers(users).WithImpersonation(svc)
	return f
}

func newImpersonationAdmin(t *testing.T, app impersonation.AuditAppender) (*admin.Router, *userstore.Memory) {
	t.Helper()
	f := newImpersonationFixture(t, app)
	return f.router, f.users
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

// serveImpersonation runs one request through h as the principal as sets.
func serveImpersonation(h http.Handler, method, path string, as func(*http.Request) *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, as(httptest.NewRequest(method, path, nil)))
	return rr
}

// decodeAdminError returns the code and details of the admin error envelope.
func decodeAdminError(t *testing.T, rr *httptest.ResponseRecorder) (string, map[string]any) {
	t.Helper()
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error body %s: %v", rr.Body.String(), err)
	}
	return env.Error.Code, env.Error.Details
}

// startOne establishes a session through the handler and returns its id.
func startOne(t *testing.T, f *impersonationFixture) string {
	t.Helper()
	rr := startImpersonation(t, f.router.Handler(), admin.StartImpersonationRequest{
		TargetTenantID: "acme", TargetUserID: "alice@acme.com", Reason: "x", TicketRef: "T", DurationSeconds: 600,
	}, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("start status = %d; body %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	id, _ := resp["impersonationSessionId"].(string)
	return id
}

// TestImpersonationEnd_InactiveReturns409InvalidStateTransition_spec_15_1
// covers the catalogued DELETE conflict: a revoked or an expired bearer
// answers 409 INVALID_STATE_TRANSITION with details.currentState and
// details.allowedStates ["active"], where the handler used to answer the
// uncatalogued CONFLICT code.
//
// spec: 15.1 (REST API), 13.3 (Credential Flow: Platform-admin impersonation)
func TestImpersonationEnd_InactiveReturns409InvalidStateTransition_spec_15_1(t *testing.T) {
	f := newImpersonationFixture(t, &stubImpAppender{})
	h := f.router.Handler()
	revoked := startOne(t, f)
	if rr := serveImpersonation(h, http.MethodDelete, "/v1/admin/impersonation/"+revoked, withAdminPrincipal); rr.Code != http.StatusOK {
		t.Fatalf("first end status = %d; body %s", rr.Code, rr.Body.String())
	}
	expired := startOne(t, f)
	*f.clock = f.clock.Add(time.Hour)

	for _, tc := range []struct{ id, state string }{{revoked, "revoked"}, {expired, "expired"}} {
		rr := serveImpersonation(h, http.MethodDelete, "/v1/admin/impersonation/"+tc.id, withAdminPrincipal)
		if rr.Code != http.StatusConflict {
			t.Fatalf("%s: status = %d, want 409; body %s", tc.state, rr.Code, rr.Body.String())
		}
		code, details := decodeAdminError(t, rr)
		if code != "INVALID_STATE_TRANSITION" || details["currentState"] != tc.state {
			t.Fatalf("%s: code=%q details=%v", tc.state, code, details)
		}
		allowed, _ := details["allowedStates"].([]any)
		if len(allowed) != 1 || allowed[0] != "active" {
			t.Fatalf("%s: allowedStates = %v, want [active]", tc.state, details["allowedStates"])
		}
	}
}

// TestImpersonationEnd_UnknownReturns404_spec_15_1 covers DELETE of an id
// no impersonation row carries, including a joined cross_tenant_read emit
// failure, which still yields 404.
//
// spec: 15.1 (REST API), 13.3 (Credential Flow: Platform-admin impersonation)
func TestImpersonationEnd_UnknownReturns404_spec_15_1(t *testing.T) {
	f := newImpersonationFixture(t, &stubImpAppender{})
	f.reads.Err = errors.New("audit chain down")
	rr := serveImpersonation(f.router.Handler(), http.MethodDelete, "/v1/admin/impersonation/imp-unknown", withAdminPrincipal)
	if code, _ := decodeAdminError(t, rr); rr.Code != http.StatusNotFound || code != "RESOURCE_NOT_FOUND" {
		t.Fatalf("status = %d code = %q, want 404 RESOURCE_NOT_FOUND", rr.Code, code)
	}
}

// TestImpersonationEnd_BodyAndEndedAuditFailure_spec_13_3 covers the DELETE
// body {impersonationSessionId, endedAt} with no endReason, and the rule 3
// ended-append failure: a CMP-058 error joined after the revoke answers
// 422 while the bearer stays revoked and is pushed into the cache.
//
// spec: 13.3 (Credential Flow: Platform-admin impersonation), 15.1 (REST API), 11.7 (Audit Logging)
func TestImpersonationEnd_BodyAndEndedAuditFailure_spec_13_3(t *testing.T) {
	app := &stubImpAppender{}
	f := newImpersonationFixture(t, app)
	h := f.router.Handler()
	id := startOne(t, f)
	rr := serveImpersonation(h, http.MethodDelete, "/v1/admin/impersonation/"+id, withAdminPrincipal)
	var body map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if rr.Code != http.StatusOK || body["impersonationSessionId"] != id || body["endedAt"] == nil {
		t.Fatalf("status = %d body = %v", rr.Code, body)
	}
	if _, has := body["endReason"]; has || len(body) != 2 {
		t.Fatalf("DELETE body = %v, want only impersonationSessionId and endedAt", body)
	}

	id2 := startOne(t, f)
	app.err = impRegionUnresolvable{}
	rr = serveImpersonation(h, http.MethodDelete, "/v1/admin/impersonation/"+id2, withAdminPrincipal)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	if row, _ := f.tokens.Row(id2); row.RevokedAt.IsZero() {
		t.Fatalf("the bearer must stay revoked when the ended event fails")
	}
	if got := f.cache.Revoked(); len(got) != 2 || got[1] != id2 {
		t.Fatalf("cache pushes = %v, want both sessions", got)
	}
}

// TestImpersonationStart_RefusedCallerReturns403_spec_13_3 covers the rule
// 1 caller check through the handler: a platform-admin presenting an
// impersonation bearer gets 403 FORBIDDEN and no audit row is written,
// where the handler used to mint a nested impersonation bearer.
//
// spec: 13.3 (Credential Flow: Platform-admin impersonation), 15.1 (REST API)
func TestImpersonationStart_RefusedCallerReturns403_spec_13_3(t *testing.T) {
	app := &stubImpAppender{}
	f := newImpersonationFixture(t, app)
	f.tokens.Put(issuedtokenstore.IssuedToken{
		JTI: "outer-imp", TenantID: "platform", Subject: "admin@acme.com",
		ActSubject: "root@acme.com", ExpiresAt: f.clock.Add(time.Hour),
	})
	asImpersonationBearer := func(req *http.Request) *http.Request {
		return req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
			Subject: "admin@acme.com", TenantID: "platform", JTI: "outer-imp",
			Roles: []pkgauth.Role{pkgauth.RolePlatformAdmin},
		}))
	}
	rr := startImpersonation(t, f.router.Handler(), admin.StartImpersonationRequest{
		TargetTenantID: "acme", TargetUserID: "alice@acme.com", Reason: "x", TicketRef: "T", DurationSeconds: 60,
	}, asImpersonationBearer)
	if code, _ := decodeAdminError(t, rr); rr.Code != http.StatusForbidden || code != "FORBIDDEN" {
		t.Fatalf("status = %d code = %q, want 403 FORBIDDEN", rr.Code, code)
	}
	if len(app.events) != 0 || len(f.tokens.Records()) != 0 {
		t.Fatalf("a refused caller must write nothing: events=%v records=%d", app.events, len(f.tokens.Records()))
	}
}

// TestImpersonationStart_RecordFailureAndInvalidatedUser_spec_13_3 covers
// the record step: a store failure answers 500 INTERNAL_ERROR, and the
// user-row fence refusing an invalidated user answers 403 FORBIDDEN.
//
// spec: 13.3 (Credential Flow: Platform-admin impersonation), 15.1 (REST API)
func TestImpersonationStart_RecordFailureAndInvalidatedUser_spec_13_3(t *testing.T) {
	for _, tc := range []struct {
		err      error
		status   int
		wantCode string
	}{
		{errors.New("postgres down"), http.StatusInternalServerError, "INTERNAL_ERROR"},
		{&issuedtokenstore.UserInvalidatedError{TenantID: "acme", Subject: "alice@acme.com"}, http.StatusForbidden, "FORBIDDEN"},
	} {
		f := newImpersonationFixture(t, &stubImpAppender{})
		f.tokens.RecordErr = tc.err
		rr := startImpersonation(t, f.router.Handler(), admin.StartImpersonationRequest{
			TargetTenantID: "acme", TargetUserID: "alice@acme.com", Reason: "x", TicketRef: "T", DurationSeconds: 60,
		}, withAdminPrincipal)
		if code, _ := decodeAdminError(t, rr); rr.Code != tc.status || code != tc.wantCode {
			t.Fatalf("record error %v: status = %d code = %q, want %d %s", tc.err, rr.Code, code, tc.status, tc.wantCode)
		}
		if bytes.Contains(rr.Body.Bytes(), []byte("minted-bearer")) {
			t.Fatalf("no bearer may be returned when the record fails")
		}
	}
}

// TestImpersonationList_PaginatedEnvelopeWithRowFields_spec_15_1 covers the
// GET listing: the §15.1 cursor-paginated envelope carrying hasMore, items
// with the issued_tokens fields and no reason or ticketId, created_at
// ordering on issuedAt, and a rejected sort field.
//
// spec: 15.1 (REST API), 13.3 (Credential Flow: Platform-admin impersonation)
func TestImpersonationList_PaginatedEnvelopeWithRowFields_spec_15_1(t *testing.T) {
	f := newImpersonationFixture(t, &stubImpAppender{})
	h := f.router.Handler()
	first := startOne(t, f)
	*f.clock = f.clock.Add(time.Minute)
	second := startOne(t, f)

	rr := serveImpersonation(h, http.MethodGet, "/v1/admin/impersonation?limit=1", withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", rr.Code, rr.Body.String())
	}
	var page struct {
		Items   []map[string]any `json:"items"`
		HasMore bool             `json:"hasMore"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &page)
	if len(page.Items) != 1 || !page.HasMore || page.Items[0]["impersonationSessionId"] != second {
		t.Fatalf("page = %+v, want the newest session first with hasMore", page)
	}
	item := page.Items[0]
	for _, k := range []string{"impersonationSessionId", "adminSub", "targetTenantId", "targetUserId", "issuedAt", "expiresAt"} {
		if _, ok := item[k]; !ok {
			t.Fatalf("item missing %q: %v", k, item)
		}
	}
	if _, ok := item["reason"]; ok || len(item) != 6 {
		t.Fatalf("item = %v, want exactly the six issued_tokens fields", item)
	}
	if _, ok := item["ticketId"]; ok {
		t.Fatalf("item carries ticketId: %v", item)
	}

	rr = serveImpersonation(h, http.MethodGet, "/v1/admin/impersonation?sort=created_at:asc", withAdminPrincipal)
	_ = json.Unmarshal(rr.Body.Bytes(), &page)
	if len(page.Items) != 2 || page.Items[0]["impersonationSessionId"] != first {
		t.Fatalf("ascending page = %+v, want the oldest first", page.Items)
	}
	if len(f.reads.Events()) != 2 {
		t.Fatalf("each listing emits one cross_tenant_read, got %d", len(f.reads.Events()))
	}

	rr = serveImpersonation(h, http.MethodGet, "/v1/admin/impersonation?sort=name", withAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid sort status = %d, want 400", rr.Code)
	}
}

// TestImpersonationList_EmitFailureReturns500_spec_13_3 covers rule 7: when
// the cross_tenant_read event cannot be written, GET returns 500 and no
// list.
//
// spec: 13.3 (Credential Flow: Platform-admin impersonation), 15.1 (REST API)
func TestImpersonationList_EmitFailureReturns500_spec_13_3(t *testing.T) {
	f := newImpersonationFixture(t, &stubImpAppender{})
	_ = startOne(t, f)
	f.reads.Err = errors.New("audit chain down")
	rr := serveImpersonation(f.router.Handler(), http.MethodGet, "/v1/admin/impersonation", withAdminPrincipal)
	if rr.Code != http.StatusInternalServerError || bytes.Contains(rr.Body.Bytes(), []byte("items")) {
		t.Fatalf("status = %d body = %s, want 500 with no list", rr.Code, rr.Body.String())
	}
}

// recordingImpersonation is an admin.ImpersonationService that counts the
// End and ListActive calls it receives.
type recordingImpersonation struct {
	failingImpersonation
	ends, lists int
}

func (r *recordingImpersonation) End(context.Context, string, string) (impersonation.EndResult, error) {
	r.ends++
	return impersonation.EndResult{}, nil
}

func (r *recordingImpersonation) ListActive(context.Context, string) ([]impersonation.Session, error) {
	r.lists++
	return nil, nil
}

// TestImpersonation_TenantAdminCannotReachCrossTenantReads_spec_13_3 covers
// rule 7: a tenant-admin caller of GET and DELETE receives 403 FORBIDDEN
// and the service, which reads under __all__, is never called.
//
// spec: 13.3 (Credential Flow: Platform-admin impersonation), 12.3 (Postgres HA Requirements)
func TestImpersonation_TenantAdminCannotReachCrossTenantReads_spec_13_3(t *testing.T) {
	svc := &recordingImpersonation{}
	h := admin.NewRouter(tenantstore.NewMemory(), admin.Options{}).WithImpersonation(svc).Handler()
	for _, rq := range []struct{ method, path string }{
		{http.MethodGet, "/v1/admin/impersonation"},
		{http.MethodDelete, "/v1/admin/impersonation/imp-1"},
	} {
		rr := serveImpersonation(h, rq.method, rq.path, withTenantAdminPrincipal)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("%s %s: status = %d, want 403", rq.method, rq.path, rr.Code)
		}
	}
	if svc.ends != 0 || svc.lists != 0 {
		t.Fatalf("service reached by a tenant-admin: ends=%d lists=%d", svc.ends, svc.lists)
	}
}
