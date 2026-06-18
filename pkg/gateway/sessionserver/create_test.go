// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/environment"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// spec: §7.1 uploadToken / sessionIsolationLevel, §14
// WorkspacePlan, §15.1 POST /v1/sessions response.

func createRequest(t *testing.T, h http.Handler, body sessionserver.CreateSessionRequest) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func createRequestRaw(t *testing.T, h http.Handler, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// TestCreateSessionServiceHappyPath_spec_15_2_1_1380 exercises the
// shared §15.1 service entry point the MCP lenny/create_session tool
// dispatches to: it runs the full create flow (validation, persist,
// uploadToken mint) and returns the same CreateSessionResponse the REST
// handler returns, in `created` state. spec: §15.2.1 rule 1 line 1380.
// F-15.2.4.
func TestCreateSessionServiceHappyPath_spec_15_2_1_1380(t *testing.T) {
	store := memstore.New()
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("test-secret")})
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	srv := sessionserver.New(store, sessionserver.Options{
		Clock:             clock,
		IDFunc:            func() string { return "sess_svc" },
		UploadTokenIssuer: uploadtoken.NewIssuer(ring, clock),
	})

	resp, svcErr := srv.CreateSessionService(context.Background(), "acme", sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
		UserID:     "alice@acme.com",
	})
	if svcErr != nil {
		t.Fatalf("unexpected service error: %+v", svcErr)
	}
	if resp.ID != "sess_svc" {
		t.Errorf("id: got %q, want sess_svc", resp.ID)
	}
	if resp.State != "created" {
		t.Errorf("state: got %q, want created", resp.State)
	}
	if resp.TenantID != "acme" {
		t.Errorf("tenant: got %q, want acme (from tenantID arg)", resp.TenantID)
	}
	if resp.UploadToken == "" {
		t.Error("service did not mint a §7.1 uploadToken")
	}
	if _, err := store.Get(context.Background(), "acme", "sess_svc"); err != nil {
		t.Fatalf("service did not persist the row: %v", err)
	}
}

// TestCreateSessionServiceValidationError_spec_15_2_1_1380 verifies the
// service surfaces a validation rejection as a typed ServiceError with the
// REST code, so the MCP surface can project the identical envelope. An
// empty runtimeRef is the canonical VALIDATION_ERROR. F-15.2.4.
func TestCreateSessionServiceValidationError_spec_15_2_1_1380(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		Clock:  func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc: func() string { return "sess_svc" },
	})

	_, svcErr := srv.CreateSessionService(context.Background(), "acme", sessionserver.CreateSessionRequest{})
	if svcErr == nil {
		t.Fatal("expected a ServiceError for an empty runtimeRef")
	}
	if svcErr.Code != "VALIDATION_ERROR" {
		t.Errorf("code: got %q, want VALIDATION_ERROR", svcErr.Code)
	}
	if svcErr.HTTPStatus != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", svcErr.HTTPStatus)
	}
}

func TestCreateMintsUploadTokenAndIsolationLevel(t *testing.T) {
	store := memstore.New()
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("test-secret")})
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	srv := sessionserver.New(store, sessionserver.Options{
		Clock:             clock,
		IDFunc:            func() string { return "sess_new" },
		UploadTokenIssuer: uploadtoken.NewIssuer(ring, clock),
	})

	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
		UserID:     "alice@acme.com",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.CreateSessionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID != "sess_new" {
		t.Errorf("id: got %q", resp.ID)
	}
	if resp.UploadToken == "" {
		t.Errorf("uploadToken is empty")
	}
	if !strings.HasPrefix(resp.UploadToken, "sess_new.") {
		t.Errorf("uploadToken prefix: %q does not start with sess_new.", resp.UploadToken)
	}
	// uploadToken must verify under the same key ring.
	v := uploadtoken.NewVerifier(ring, nil, clock)
	if _, err := v.Verify(resp.UploadToken, resp.ID); err != nil {
		t.Errorf("uploadToken does not verify: %v", err)
	}
	if resp.SessionIsolationLevel.IsolationProfile != string(isolation.Default()) {
		t.Errorf("default isolation profile: got %q, want %q",
			resp.SessionIsolationLevel.IsolationProfile, isolation.Default())
	}
	if resp.SessionIsolationLevel.ExecutionMode != "session" {
		t.Errorf("executionMode: got %q, want session", resp.SessionIsolationLevel.ExecutionMode)
	}
	if resp.SessionIsolationLevel.PodReuse {
		t.Error("session-mode pod must not have podReuse=true")
	}
	if resp.SessionIsolationLevel.ResidualStateWarning {
		t.Error("session-mode must not raise residualStateWarning")
	}
	// spec: §7.1 line 74 — a session-mode pool binds the session to one pod
	// and preserves conversation context for its lifetime.
	if resp.SessionIsolationLevel.ConversationContinuity != "platform" {
		t.Errorf("conversationContinuity: got %q, want platform",
			resp.SessionIsolationLevel.ConversationContinuity)
	}
	// Session row should be persisted with the resolved isolation profile.
	row, _ := store.Get(context.Background(), "acme", "sess_new")
	if row.IsolationProfile != isolation.Default() {
		t.Errorf("row.IsolationProfile: got %q, want %q", row.IsolationProfile, isolation.Default())
	}
}

func TestCreateRecordsEnvironment(t *testing.T) {
	store := memstore.New()
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("test-secret")})
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	srv := sessionserver.New(store, sessionserver.Options{
		Clock:             clock,
		IDFunc:            func() string { return "sess_env" },
		UploadTokenIssuer: uploadtoken.NewIssuer(ring, clock),
	})

	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef:  "claude-code",
		Environment: "security-team",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.CreateSessionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Environment != "security-team" {
		t.Errorf("create response environment: got %q, want security-team", resp.Environment)
	}
	// The §10.6 environment is recorded on the stored row.
	row, err := store.Get(context.Background(), "acme", "sess_env")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if row.Environment != "security-team" {
		t.Errorf("stored environment: got %q, want security-team", row.Environment)
	}
	// And it is echoed on GET /v1/sessions/{id}.
	getReq := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess_env", nil)
	getReq.Header.Set("X-Lenny-Tenant-ID", "acme")
	getRR := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("GET status: %d, body=%s", getRR.Code, getRR.Body.String())
	}
	var got sessionserver.SessionResponse
	_ = json.Unmarshal(getRR.Body.Bytes(), &got)
	if got.Environment != "security-team" {
		t.Errorf("GET response environment: got %q, want security-team", got.Environment)
	}
}

func TestCreateWithoutEnvironmentLeavesItEmpty(t *testing.T) {
	store := memstore.New()
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("test-secret")})
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	srv := sessionserver.New(store, sessionserver.Options{
		Clock:             clock,
		IDFunc:            func() string { return "sess_noenv" },
		UploadTokenIssuer: uploadtoken.NewIssuer(ring, clock),
	})
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "acme", "sess_noenv")
	if row.Environment != "" {
		t.Errorf("an unscoped session must have an empty environment: got %q", row.Environment)
	}
}

func TestEnvironmentSessionsEndpointSetsEnvironment(t *testing.T) {
	store := memstore.New()
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("test-secret")})
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	srv := sessionserver.New(store, sessionserver.Options{
		Clock:             clock,
		IDFunc:            func() string { return "sess_envpath" },
		UploadTokenIssuer: uploadtoken.NewIssuer(ring, clock),
	})

	body, _ := json.Marshal(sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"})
	req := httptest.NewRequest(http.MethodPost, "/v1/environments/security-team/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.CreateSessionResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Environment != "security-team" {
		t.Errorf("environment from path: got %q, want security-team", resp.Environment)
	}
	row, err := store.Get(context.Background(), "acme", "sess_envpath")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if row.Environment != "security-team" {
		t.Errorf("stored environment: got %q, want security-team", row.Environment)
	}
}

func TestEnvironmentSessionsEndpointPathOverridesBody(t *testing.T) {
	store := memstore.New()
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("test-secret")})
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	srv := sessionserver.New(store, sessionserver.Options{
		Clock:             clock,
		IDFunc:            func() string { return "sess_override" },
		UploadTokenIssuer: uploadtoken.NewIssuer(ring, clock),
	})

	// An environment in the request body is overridden by the URL path.
	body, _ := json.Marshal(sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code", Environment: "from-body",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/environments/from-path/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	row, err := store.Get(context.Background(), "acme", "sess_override")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if row.Environment != "from-path" {
		t.Errorf("the URL path must override the body environment: got %q, want from-path", row.Environment)
	}
}

// TestCreateDevModeDefaultIsolation_spec_5_3 verifies the §5.3 line 677
// dev-mode fallback at the session-creation default: with DevMode set
// and no configured DefaultIsolationProfile, a session that omits a
// profile resolves to `standard` (runc) rather than the production
// `sandboxed`.
//
// spec: §5.3 line 677.
func TestCreateDevModeDefaultIsolation_spec_5_3(t *testing.T) {
	store := memstore.New()
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	srv := sessionserver.New(store, sessionserver.Options{
		Clock:   clock,
		IDFunc:  func() string { return "sess_dev" },
		DevMode: true,
	})

	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
		UserID:     "alice@acme.com",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.CreateSessionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.SessionIsolationLevel.IsolationProfile != string(isolation.ProfileStandard) {
		t.Errorf("dev-mode default isolation = %q, want standard",
			resp.SessionIsolationLevel.IsolationProfile)
	}
	row, _ := store.Get(context.Background(), "acme", "sess_dev")
	if row.IsolationProfile != isolation.ProfileStandard {
		t.Errorf("row.IsolationProfile = %q, want standard under dev mode", row.IsolationProfile)
	}
}

func TestCreateRespectsExplicitIsolationProfile(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{IDFunc: func() string { return "sess_iso" }})
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef:       "claude-code",
		IsolationProfile: isolation.ProfileMicrovm,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.CreateSessionResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.SessionIsolationLevel.IsolationProfile != string(isolation.ProfileMicrovm) {
		t.Errorf("isolation: got %q, want microvm", resp.SessionIsolationLevel.IsolationProfile)
	}
}

// TestGetReturnsSessionIsolationLevel_spec_7_1 asserts the §7.1 line 75
// requirement that GET /v1/sessions/{id} and the list both return the
// sessionIsolationLevel metadata so a client that lost the create
// response can still inspect the session's isolation posture. The
// isolation profile is the one persisted at creation (microvm here) and
// is stable for the session's lifetime.
func TestGetReturnsSessionIsolationLevel_spec_7_1(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{IDFunc: func() string { return "sess_getiso" }})

	if rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef:       "claude-code",
		IsolationProfile: isolation.ProfileMicrovm,
	}); rr.Code != http.StatusCreated {
		t.Fatalf("create status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	// GET /v1/sessions/{id} must carry sessionIsolationLevel.
	get := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess_getiso", nil)
	get.Header.Set("X-Lenny-Tenant-ID", "acme")
	gr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(gr, get)
	if gr.Code != http.StatusOK {
		t.Fatalf("GET status: got %d, want 200; body=%s", gr.Code, gr.Body.String())
	}
	var got sessionserver.SessionResponse
	if err := json.Unmarshal(gr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	if got.SessionIsolationLevel.IsolationProfile != string(isolation.ProfileMicrovm) {
		t.Errorf("GET sessionIsolationLevel.isolationProfile = %q, want microvm",
			got.SessionIsolationLevel.IsolationProfile)
	}
	if got.SessionIsolationLevel.ExecutionMode != "session" {
		t.Errorf("GET executionMode = %q, want session", got.SessionIsolationLevel.ExecutionMode)
	}
	// The field must be present in the raw JSON, not just the zero value.
	if !strings.Contains(gr.Body.String(), `"sessionIsolationLevel"`) {
		t.Errorf("GET body missing sessionIsolationLevel key: %s", gr.Body.String())
	}

	// GET /v1/sessions (list) must carry it for each row too.
	list := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	list.Header.Set("X-Lenny-Tenant-ID", "acme")
	lr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(lr, list)
	if lr.Code != http.StatusOK {
		t.Fatalf("LIST status: got %d, want 200", lr.Code)
	}
	var listResp struct {
		Items []sessionserver.SessionResponse `json:"items"`
	}
	if err := json.Unmarshal(lr.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode LIST: %v", err)
	}
	if len(listResp.Items) != 1 {
		t.Fatalf("LIST returned %d sessions, want 1", len(listResp.Items))
	}
	if listResp.Items[0].SessionIsolationLevel.IsolationProfile != string(isolation.ProfileMicrovm) {
		t.Errorf("LIST sessionIsolationLevel.isolationProfile = %q, want microvm",
			listResp.Items[0].SessionIsolationLevel.IsolationProfile)
	}
}

func TestCreateRejectsUnknownIsolationProfile(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{})
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef:       "claude-code",
		IsolationProfile: isolation.Profile("ferrous"),
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rr.Code)
	}
}

func TestCreateValidatesWorkspacePlan(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{IDFunc: func() string { return "sess_wp" }})

	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
		WorkspacePlan: json.RawMessage(`{
			"schemaVersion": 1,
			"sources": [
				{"type": "inlineFile", "path": "main.go", "content": "package main"}
			]
		}`),
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.CreateSessionResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.WorkspacePlanWarnings) != 0 {
		t.Errorf("unexpected warnings: %+v", resp.WorkspacePlanWarnings)
	}
}

func TestGetReturnsStoredWorkspacePlan(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{IDFunc: func() string { return "sess_wp_get" }})
	h := srv.Handler()

	const plan = `{"schemaVersion":1,"sources":[{"type":"mkdir","path":"out/"}]}`
	if rr := createRequest(t, h, sessionserver.CreateSessionRequest{
		RuntimeRef:    "echo",
		WorkspacePlan: json.RawMessage(plan),
	}); rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess_wp_get", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("get: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.SessionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.WorkspacePlan) == 0 {
		t.Fatal("GET /v1/sessions/{id} did not return the stored workspacePlan (§15.1)")
	}
	var got, want any
	_ = json.Unmarshal(resp.WorkspacePlan, &got)
	_ = json.Unmarshal([]byte(plan), &want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("workspacePlan = %s, want %s", resp.WorkspacePlan, plan)
	}
}

func TestGetOmitsWorkspacePlanWhenAbsent(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{IDFunc: func() string { return "sess_noplan" }})
	h := srv.Handler()

	if rr := createRequest(t, h, sessionserver.CreateSessionRequest{RuntimeRef: "echo"}); rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d", rr.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess_noplan", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("get: status %d", rr.Code)
	}
	// omitempty drops the field from the envelope for a planless session.
	if bytes.Contains(rr.Body.Bytes(), []byte("workspacePlan")) {
		t.Errorf("response carries workspacePlan for a planless session: %s", rr.Body.String())
	}
}

func TestCreateRejectsMalformedWorkspacePlan(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{})

	// Setuid mode → WORKSPACE_PLAN_INVALID with setuid_setgid_prohibited.
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
		WorkspacePlan: json.RawMessage(`{
			"schemaVersion": 1,
			"sources": [
				{"type": "inlineFile", "path": "x", "content": "y", "mode": "04755"}
			]
		}`),
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "WORKSPACE_PLAN_INVALID" {
		t.Errorf("error code: got %q", env.Error.Code)
	}
}

// spec: §14.1 line 326 — a plan whose schemaVersion exceeds what the
// gateway understands is not a "bad plan" (400 WORKSPACE_PLAN_INVALID)
// but a "gateway too old" condition: HTTP 422
// WORKSPACE_PLAN_SCHEMA_UNSUPPORTED carrying details.knownVersion and
// details.encounteredVersion so a rollback can be diagnosed. F-14.1.1.
func TestCreateRejectsUnsupportedSchemaVersion_spec_14_1_326(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{})

	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef:    "claude-code",
		WorkspacePlan: json.RawMessage(`{"schemaVersion": 2, "sources": []}`),
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status: got %d, want 422; body=%s", rr.Code, rr.Body.String())
	}
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "WORKSPACE_PLAN_SCHEMA_UNSUPPORTED" {
		t.Errorf("error code: got %q, want WORKSPACE_PLAN_SCHEMA_UNSUPPORTED", env.Error.Code)
	}
	if got, _ := env.Error.Details["knownVersion"].(float64); got != 1 {
		t.Errorf("details.knownVersion: got %v, want 1", env.Error.Details["knownVersion"])
	}
	if got, _ := env.Error.Details["encounteredVersion"].(float64); got != 2 {
		t.Errorf("details.encounteredVersion: got %v, want 2", env.Error.Details["encounteredVersion"])
	}
}

// TestCreateMultiViolationReportsDetailsFields covers F-14.1.19 / §15.1
// line 979: when multiple WorkspacePlan sub-errors are aggregated the
// envelope rides under `details.fields` (plural — the JSON Schema
// validation report), not `details.subErrors`. `details.field`
// (singular) carries the offending plan path.
func TestCreateMultiViolationReportsDetailsFields_spec_15_1_979(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{})

	// Two bad sources — parser aggregates both into SubErrs and the
	// gateway maps them to details.fields per the spec.
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
		WorkspacePlan: json.RawMessage(`{
			"schemaVersion": 1,
			"sources": [
				{"type":"inlineFile","path":"/abs","content":""},
				{"type":"gitClone","url":"ftp://x","ref":"main"}
			]
		}`),
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "WORKSPACE_PLAN_INVALID" {
		t.Fatalf("error code: got %q, want WORKSPACE_PLAN_INVALID", env.Error.Code)
	}
	fields, ok := env.Error.Details["fields"].([]any)
	if !ok || len(fields) != 2 {
		t.Fatalf("details.fields: got %v, want a slice of length 2; full details=%v", env.Error.Details["fields"], env.Error.Details)
	}
	if _, has := env.Error.Details["subErrors"]; has {
		t.Errorf("details.subErrors is the pre-F-14.1.19 wire name; spec uses details.fields (plural)")
	}
}

func TestCreatePropagatesUnknownSourceWarning(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{IDFunc: func() string { return "sess_warn" }})
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
		WorkspacePlan: json.RawMessage(`{
			"schemaVersion": 1,
			"sources": [
				{"type": "ferrousVariant", "magicNumber": 7}
			]
		}`),
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.CreateSessionResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.WorkspacePlanWarnings) != 1 {
		t.Fatalf("warnings: got %d, want 1; resp=%+v", len(resp.WorkspacePlanWarnings), resp)
	}
}

func TestCreateAcceptsNullWorkspacePlan(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{IDFunc: func() string { return "sess_null" }})
	rr := createRequestRaw(t, srv.Handler(), []byte(`{"runtimeRef": "x", "workspacePlan": null}`))
	if rr.Code != http.StatusCreated {
		t.Errorf("status: got %d, want 201", rr.Code)
	}
}

// spec: §7.1 line 28 — the atomic-creation contract requires that
// when the upload-token mint fails, no session row is persisted and
// the client receives 503 SESSION_CREATION_FAILED with Retry-After.
// A nil UploadTokenIssuer is not the exercised failure mode (the
// constructor synthesizes one); fabricate a failure-mode store that
// rejects Create to verify the persistence-failure half of the
// rollback contract.
func TestCreateReturnsSessionCreationFailedOnPersistFailure_spec_7_1_4(t *testing.T) {
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("test")})
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	store := &createRejectingStore{Store: memstore.New()}
	srv := sessionserver.New(store, sessionserver.Options{
		Clock:             clock,
		IDFunc:            func() string { return "sess_persist_fail" },
		UploadTokenIssuer: uploadtoken.NewIssuer(ring, clock),
	})

	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
	})

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	if ra := rr.Header().Get("Retry-After"); ra == "" {
		t.Errorf("Retry-After header missing on the SESSION_CREATION_FAILED reply")
	}
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &env)
	if env.Error.Code != "SESSION_CREATION_FAILED" {
		t.Errorf("code = %q, want SESSION_CREATION_FAILED", env.Error.Code)
	}
	if env.Error.Details["reason"] != "row_persistence_failed" {
		t.Errorf("details.reason = %v, want row_persistence_failed", env.Error.Details["reason"])
	}

	// §7.1 line 28: "does NOT persist the session row". Even though the
	// store's Create returned an error, the row must not exist.
	if _, err := store.Store.Get(context.Background(), "acme", "sess_persist_fail"); err == nil {
		t.Errorf("session row exists after persistence failure; §7.1 forbids the create row")
	}
}

// stubDualStore is a §10.1 DualStoreGate test double.
type stubDualStore struct{ unavailable bool }

func (s stubDualStore) Unavailable() bool { return s.unavailable }

// spec: §10.1 item 2 — while both coordination stores are unreachable
// `session.create` is rejected with 503 + Retry-After: 10 and the
// PLATFORM_DEGRADED code, before any quota / persistence work runs.
func TestCreateReturnsPlatformDegradedWhenDualStoreDown_spec_10_1(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:    func() string { return "sess_should_not_persist" },
		DualStore: stubDualStore{unavailable: true},
	})

	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
	})

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	if ra := rr.Header().Get("Retry-After"); ra != "10" {
		t.Errorf("Retry-After = %q, want 10", ra)
	}
	var env struct {
		Error struct {
			Code      string         `json:"code"`
			Retryable bool           `json:"retryable"`
			Details   map[string]any `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &env)
	if env.Error.Code != "PLATFORM_DEGRADED" {
		t.Errorf("code = %q, want PLATFORM_DEGRADED", env.Error.Code)
	}
	if !env.Error.Retryable {
		t.Error("a dual-store 503 must classify as retryable")
	}
	if env.Error.Details["reason"] != "dual_store_unavailable" {
		t.Errorf("details.reason = %v, want dual_store_unavailable", env.Error.Details["reason"])
	}
	// The gate runs before persistence: no row was created.
	if _, err := store.Get(context.Background(), "acme", "sess_should_not_persist"); err == nil {
		t.Error("no session row may be persisted while the dual-store gate is closed")
	}
}

// spec: §10.1 item 1 — when the gate reports available the create flow
// proceeds normally (the gate does not block healthy creates).
func TestCreateProceedsWhenDualStoreAvailable_spec_10_1(t *testing.T) {
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		Clock:     clock,
		IDFunc:    func() string { return "sess_ok" },
		DualStore: stubDualStore{unavailable: false},
	})
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
}

// spec: §7.1 line 28 — when the upload-token mint itself fails, the
// row was never built (no IDFunc was even called for persistence).
// The handler returns SESSION_CREATION_FAILED with reason
// `upload_token_issuance_failed` and no row.
func TestCreateReturnsSessionCreationFailedOnMintFailure_spec_7_1_4(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc: func() string {
			// Return a sessionID containing a forbidden dot so
			// uploadtoken.IssueDetailed rejects it; this is the canonical
			// path-deterministic mint failure.
			return "sess.bad-id"
		},
	})

	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
	})

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &env)
	if env.Error.Code != "SESSION_CREATION_FAILED" {
		t.Errorf("code = %q, want SESSION_CREATION_FAILED", env.Error.Code)
	}
	if env.Error.Details["reason"] != "upload_token_issuance_failed" {
		t.Errorf("details.reason = %v, want upload_token_issuance_failed", env.Error.Details["reason"])
	}
	if _, err := store.Get(context.Background(), "acme", "sess.bad-id"); err == nil {
		t.Errorf("session row exists after upload-token mint failure; §7.1 forbids the create row")
	}
}

// createRejectingStore wraps memstore so Create returns an error,
// exercising the §7.1 line 28 persistence-failure roll-back branch.
type createRejectingStore struct {
	*memstore.Store
}

func (s *createRejectingStore) Create(ctx context.Context, row sessionstore.Session) error {
	return errCreateRejected
}

var errCreateRejected = errors.New("test: persistence failure injected")

// --- §11.1 line 13 noEnvironmentPolicy admission gate -----------------

// seedNoEnvServer builds a sessionserver wired with the §10.6
// environment + tenant registries, the rest of the options stays
// minimal so the test exercises only the §11.1 line 13 admission gate.
func seedNoEnvServer(t *testing.T, sessionID string, tenantPolicy, platformDefault string, envs ...environmentstore.Environment) (*sessionserver.Server, *memstore.Store) {
	t.Helper()
	store := memstore.New()
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("test-secret")})
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	es := environmentstore.NewMemory()
	for _, e := range envs {
		if err := es.Create(context.Background(), e); err != nil {
			t.Fatalf("seed environment %q: %v", e.Name, err)
		}
	}
	ts := tenantstore.NewMemory()
	if err := ts.Create(context.Background(), tenantstore.Tenant{ID: "acme", NoEnvironmentPolicy: tenantPolicy}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	srv := sessionserver.New(store, sessionserver.Options{
		Clock:                      clock,
		IDFunc:                     func() string { return sessionID },
		UploadTokenIssuer:          uploadtoken.NewIssuer(ring, clock),
		Environments:               es,
		Tenants:                    ts,
		DefaultNoEnvironmentPolicy: platformDefault,
	})
	return srv, store
}

// createRequestAs drives a session create request whose context carries
// an authenticated Principal (the §10.2 path the §11.1 line 13 gate
// resolves environment membership against).
func createRequestAs(t *testing.T, h http.Handler, body sessionserver.CreateSessionRequest, principal authmw.Principal) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(authmw.WithPrincipal(req.Context(), principal))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// TestCreateWithoutEnvironmentRejectedUnderDenyAll_spec_11_1 — the
// §11.1 line 13 platform default `deny-all` rejects a session create
// that names no environment when the caller has no environment
// membership. §10.6 line 646 treats an empty per-tenant policy as
// deny-all.
func TestCreateWithoutEnvironmentRejectedUnderDenyAll_spec_11_1(t *testing.T) {
	srv, store := seedNoEnvServer(t, "sess_deny", tenantstore.NoEnvPolicyDenyAll, tenantstore.NoEnvPolicyDenyAll)

	rr := createRequestAs(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
	}, authmw.Principal{Subject: "alice@acme.com", TenantID: "acme"})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "FORBIDDEN") {
		t.Errorf("rejection body must mention FORBIDDEN, got %q", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "no_environment_policy_deny_all") {
		t.Errorf("rejection body must carry the policy reason, got %q", rr.Body.String())
	}
	// No row created — §7.1 atomicity holds because the gate runs
	// before the upload-token mint.
	if _, err := store.Get(context.Background(), "acme", "sess_deny"); err == nil {
		t.Error("a rejected create must not persist a session row")
	}
}

// TestCreateWithoutEnvironmentAdmittedUnderAllowAll_spec_11_1 — when
// the tenant's noEnvironmentPolicy resolves to allow-all, the same
// request passes through to the normal create path. §10.6 line 657.
func TestCreateWithoutEnvironmentAdmittedUnderAllowAll_spec_11_1(t *testing.T) {
	srv, _ := seedNoEnvServer(t, "sess_allow", tenantstore.NoEnvPolicyAllowAll, tenantstore.NoEnvPolicyDenyAll)

	rr := createRequestAs(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
	}, authmw.Principal{Subject: "alice@acme.com", TenantID: "acme"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 under allow-all; body=%s", rr.Code, rr.Body.String())
	}
}

// TestCreateWithoutEnvironmentAdmittedWhenCallerIsMember_spec_11_1 —
// a caller who belongs to at least one environment is admitted under
// deny-all because §10.6 line 657 scopes the policy to "no environment
// membership"; the transparent filter governs runtime access through
// the runtimeRef path even when no environment is named in the body.
func TestCreateWithoutEnvironmentAdmittedWhenCallerIsMember_spec_11_1(t *testing.T) {
	// envaccess matches any identity Type other than "oidc-group"
	// against the caller's Subject; "user" therefore matches a
	// principal with Subject="alice@acme.com".
	env := environmentstore.Environment{
		Name:     "security-team",
		TenantID: "acme",
		Members: []environmentstore.Member{{
			Identity: environmentstore.Identity{Type: "user", Value: "alice@acme.com"},
			Role:     environment.RoleCreator,
		}},
		RuntimeSelector: environment.Selector{MatchLabels: map[string]string{"team": "security"}},
	}
	srv, _ := seedNoEnvServer(t, "sess_member", tenantstore.NoEnvPolicyDenyAll, tenantstore.NoEnvPolicyDenyAll, env)

	rr := createRequestAs(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
		UserID:     "alice@acme.com",
	}, authmw.Principal{Subject: "alice@acme.com", TenantID: "acme"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 for an env-member caller; body=%s", rr.Code, rr.Body.String())
	}
}

// TestCreateWithoutEnvironmentEmptyTenantPolicyDefaultsToDenyAll_spec_10_6
// — an empty per-tenant noEnvironmentPolicy is treated as deny-all
// when the platform default is also deny-all. §10.6 line 646.
func TestCreateWithoutEnvironmentEmptyTenantPolicyDefaultsToDenyAll_spec_10_6(t *testing.T) {
	srv, _ := seedNoEnvServer(t, "sess_empty", "", tenantstore.NoEnvPolicyDenyAll)

	rr := createRequestAs(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
	}, authmw.Principal{Subject: "alice@acme.com", TenantID: "acme"})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (empty tenant policy → platform deny-all); body=%s", rr.Code, rr.Body.String())
	}
}

// --- §10.6 explicit-environment access path (F-10.6.1 / F-10.6.5) -----

// seedExplicitEnvServer wires the §10.6 environment + tenant + runtime
// registries so the explicit-environment admission path can resolve
// membership, role, and runtime scope. A zero-Name runtime is left
// unseeded so the runtimeSelector sub-check (c) is skipped.
func seedExplicitEnvServer(t *testing.T, sessionID string, rt runtimestore.Runtime, envs ...environmentstore.Environment) (*sessionserver.Server, *memstore.Store) {
	t.Helper()
	store := memstore.New()
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("test-secret")})
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	es := environmentstore.NewMemory()
	for _, e := range envs {
		if err := es.Create(context.Background(), e); err != nil {
			t.Fatalf("seed environment %q: %v", e.Name, err)
		}
	}
	ts := tenantstore.NewMemory()
	if err := ts.Create(context.Background(), tenantstore.Tenant{ID: "acme", NoEnvironmentPolicy: tenantstore.NoEnvPolicyDenyAll}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	rts := runtimestore.NewMemory()
	if rt.Name != "" {
		if err := rts.Create(context.Background(), rt); err != nil {
			t.Fatalf("seed runtime: %v", err)
		}
	}
	srv := sessionserver.New(store, sessionserver.Options{
		Clock:                      clock,
		IDFunc:                     func() string { return sessionID },
		UploadTokenIssuer:          uploadtoken.NewIssuer(ring, clock),
		Environments:               es,
		Tenants:                    ts,
		Runtimes:                   rts,
		DefaultNoEnvironmentPolicy: tenantstore.NoEnvPolicyDenyAll,
	})
	return srv, store
}

// createEnvRequestAs drives a POST to the §10.6 explicit-environment
// endpoint /v1/environments/{name}/sessions with an authenticated
// principal.
func createEnvRequestAs(t *testing.T, h http.Handler, envName string, body sessionserver.CreateSessionRequest, principal authmw.Principal) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/environments/"+envName+"/sessions", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(authmw.WithPrincipal(req.Context(), principal))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// envWithMembers builds a §10.6 environment whose runtimeSelector admits
// runtimes labelled team=security.
func envWithMembers(name string, members ...environmentstore.Member) environmentstore.Environment {
	return environmentstore.Environment{
		Name:            name,
		TenantID:        "acme",
		Members:         members,
		RuntimeSelector: environment.Selector{MatchLabels: map[string]string{"team": "security"}},
	}
}

func member(value string, role environment.Role) environmentstore.Member {
	return environmentstore.Member{Identity: environmentstore.Identity{Type: "user", Value: value}, Role: role}
}

// TestExplicitEnvironmentUnknownRejected_spec_10_6_557 — naming an
// environment that is not defined in the caller's tenant is rejected
// with 403, closing the confused-deputy hole where any authenticated
// caller could create a session "in" any environment name. F-10.6.1.
func TestExplicitEnvironmentUnknownRejected_spec_10_6_557(t *testing.T) {
	srv, store := seedExplicitEnvServer(t, "sess_unknown", runtimestore.Runtime{})

	rr := createEnvRequestAs(t, srv.Handler(), "security-team", sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
	}, authmw.Principal{Subject: "alice@acme.com", TenantID: "acme"})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for an undefined environment; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "environment_not_found") {
		t.Errorf("body must carry environment_not_found reason: %s", rr.Body.String())
	}
	if _, err := store.Get(context.Background(), "acme", "sess_unknown"); err == nil {
		t.Error("a rejected create must not persist a session row")
	}
}

// TestExplicitEnvironmentNonMemberRejected_spec_10_6_557 — a caller with
// no role in the named environment is rejected. F-10.6.1.
func TestExplicitEnvironmentNonMemberRejected_spec_10_6_557(t *testing.T) {
	env := envWithMembers("security-team", member("alice@acme.com", environment.RoleCreator))
	srv, _ := seedExplicitEnvServer(t, "sess_nonmember", runtimestore.Runtime{}, env)

	rr := createEnvRequestAs(t, srv.Handler(), "security-team", sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
	}, authmw.Principal{Subject: "carol@acme.com", TenantID: "acme"})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a non-member; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "not_environment_member") {
		t.Errorf("body must carry not_environment_member reason: %s", rr.Body.String())
	}
}

// TestExplicitEnvironmentViewerRejected_spec_10_6_605 — §10.6 line 605:
// a `viewer` reads; creating a session requires at least `creator`. A
// viewer-only member is rejected. F-10.6.5.
func TestExplicitEnvironmentViewerRejected_spec_10_6_605(t *testing.T) {
	env := envWithMembers("security-team", member("bob@acme.com", environment.RoleViewer))
	srv, _ := seedExplicitEnvServer(t, "sess_viewer", runtimestore.Runtime{}, env)

	rr := createEnvRequestAs(t, srv.Handler(), "security-team", sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
	}, authmw.Principal{Subject: "bob@acme.com", TenantID: "acme"})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a viewer; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "insufficient_environment_role") {
		t.Errorf("body must carry insufficient_environment_role reason: %s", rr.Body.String())
	}
}

// TestExplicitEnvironmentCreatorAdmitted_spec_10_6_605 — a `creator`
// member whose requested runtime is in the environment's runtimeSelector
// is admitted, and the session is recorded in the environment. F-10.6.5.
func TestExplicitEnvironmentCreatorAdmitted_spec_10_6_605(t *testing.T) {
	env := envWithMembers("security-team", member("alice@acme.com", environment.RoleCreator))
	rt := runtimestore.Runtime{Name: "claude-code", Type: runtimestore.TypeAgent, Labels: map[string]string{"team": "security"}}
	srv, store := seedExplicitEnvServer(t, "sess_creator", rt, env)

	rr := createEnvRequestAs(t, srv.Handler(), "security-team", sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
	}, authmw.Principal{Subject: "alice@acme.com", TenantID: "acme"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 for a creator member; body=%s", rr.Code, rr.Body.String())
	}
	row, err := store.Get(context.Background(), "acme", "sess_creator")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if row.Environment != "security-team" {
		t.Errorf("stored environment = %q, want security-team", row.Environment)
	}
}

// TestExplicitEnvironmentRuntimeNotInScopeRejected_spec_10_6_629 — §10.6
// line 629: the session's runtime must be inside the environment
// definition. A creator member requesting a runtime the environment's
// runtimeSelector does not admit is rejected. F-10.6.1.
func TestExplicitEnvironmentRuntimeNotInScopeRejected_spec_10_6_629(t *testing.T) {
	env := envWithMembers("security-team", member("alice@acme.com", environment.RoleCreator))
	rogue := runtimestore.Runtime{Name: "rogue", Type: runtimestore.TypeAgent, Labels: map[string]string{"team": "platform"}}
	srv, _ := seedExplicitEnvServer(t, "sess_rogue", rogue, env)

	rr := createEnvRequestAs(t, srv.Handler(), "security-team", sessionserver.CreateSessionRequest{
		RuntimeRef: "rogue",
	}, authmw.Principal{Subject: "alice@acme.com", TenantID: "acme"})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for an out-of-scope runtime; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "runtime_not_in_environment") {
		t.Errorf("body must carry runtime_not_in_environment reason: %s", rr.Body.String())
	}
}

// TestExplicitEnvironmentNoPrincipalPassesThrough_spec_10_6 — the
// explicit-environment gate is nil-safe for an unauthenticated request:
// with no principal there is no identity to resolve membership against,
// so the dev-header / single-tenant posture passes through. F-10.6.1.
func TestExplicitEnvironmentNoPrincipalPassesThrough_spec_10_6(t *testing.T) {
	env := envWithMembers("security-team", member("alice@acme.com", environment.RoleCreator))
	srv, _ := seedExplicitEnvServer(t, "sess_noprincipal", runtimestore.Runtime{}, env)

	body, _ := json.Marshal(sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"})
	req := httptest.NewRequest(http.MethodPost, "/v1/environments/security-team/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 when no principal is present; body=%s", rr.Code, rr.Body.String())
	}
}

// TestCreateAdmissionGateDisabledWhenRegistriesUnwired_spec_11_1 — the
// gate is a no-op when the gateway runs without the §10.6 registries
// (a minimal deployment posture). The existing
// TestCreateWithoutEnvironmentLeavesItEmpty case covers admission;
// this case adds the explicit assertion that the deny-all default
// does not engage without environments/tenants wired.
func TestCreateAdmissionGateDisabledWhenRegistriesUnwired_spec_11_1(t *testing.T) {
	store := memstore.New()
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("test-secret")})
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	srv := sessionserver.New(store, sessionserver.Options{
		Clock:                      clock,
		IDFunc:                     func() string { return "sess_unwired" },
		UploadTokenIssuer:          uploadtoken.NewIssuer(ring, clock),
		DefaultNoEnvironmentPolicy: tenantstore.NoEnvPolicyDenyAll,
	})
	rr := createRequestAs(t, srv.Handler(), sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"},
		authmw.Principal{Subject: "alice@acme.com", TenantID: "acme"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 without env registries wired; body=%s", rr.Code, rr.Body.String())
	}
}

// TestCreateRejectsSetupCommandsOverMaxCommands covers F-7.5.5 / §7.5
// line 477 / §5.1 line 76: the gateway rejects a session whose
// workspacePlan.setupCommands exceeds the runtime
// setupCommandPolicy.maxCommands cap before any row is persisted or any
// pod is claimed. The §15.1 envelope is WORKSPACE_PLAN_INVALID with a
// structured details payload (`field`, `reason`, `maxCommands`, `count`)
// so an operator can distinguish this rejection from setuid / path
// rejections without parsing the human message.
func TestCreateRejectsSetupCommandsOverMaxCommands(t *testing.T) {
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "capped",
		Type: runtimestore.TypeAgent,
		SetupCommandPolicy: &runtimestore.SetupCommandPolicy{
			Mode:        runtimestore.SetupCommandModeAllowlist,
			MaxCommands: 2,
		},
	})
	srv := sessionserver.New(store, sessionserver.Options{Runtimes: runtimes})

	plan := json.RawMessage(`{
		"schemaVersion": 1,
		"sources": [],
		"setupCommands": [
			{"cmd": "echo a"},
			{"cmd": "echo b"},
			{"cmd": "echo c"}
		]
	}`)
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "capped", WorkspacePlan: plan,
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "WORKSPACE_PLAN_INVALID" {
		t.Errorf("error code: got %q, want WORKSPACE_PLAN_INVALID", env.Error.Code)
	}
	if env.Error.Details["reason"] != "setup_commands_max_exceeded" {
		t.Errorf("details.reason = %v, want setup_commands_max_exceeded", env.Error.Details["reason"])
	}
	if got, _ := env.Error.Details["maxCommands"].(float64); int(got) != 2 {
		t.Errorf("details.maxCommands = %v, want 2", env.Error.Details["maxCommands"])
	}
	if got, _ := env.Error.Details["count"].(float64); int(got) != 3 {
		t.Errorf("details.count = %v, want 3", env.Error.Details["count"])
	}
}

// TestCreateAdmitsSetupCommandsWithinMaxCommands is the F-7.5.5
// regression guard: a request whose setupCommands count is at the cap
// must succeed so the cap is a ceiling, not a strict less-than gate.
// The policy declares an allowlist containing the test commands so the
// F-7.5.1 prefix gate also admits them.
func TestCreateAdmitsSetupCommandsWithinMaxCommands(t *testing.T) {
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "capped",
		Type: runtimestore.TypeAgent,
		SetupCommandPolicy: &runtimestore.SetupCommandPolicy{
			Mode:        runtimestore.SetupCommandModeAllowlist,
			Allowlist:   []string{"echo"},
			MaxCommands: 2,
		},
	})
	srv := sessionserver.New(store, sessionserver.Options{Runtimes: runtimes})

	plan := json.RawMessage(`{
		"schemaVersion": 1,
		"sources": [],
		"setupCommands": [{"cmd": "echo a"}, {"cmd": "echo b"}]
	}`)
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "capped", WorkspacePlan: plan,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201 (at-cap); body=%s", rr.Code, rr.Body.String())
	}
}

// TestCreateAdmitsSetupCommandsWhenPolicyUnset is the F-7.5.5 regression
// guard for the no-policy / no-cap case: a runtime that declares no
// setupCommandPolicy (or a policy with maxCommands == 0) admits any
// number of setup commands, preserving the pre-F-7.5.5 behaviour.
func TestCreateAdmitsSetupCommandsWhenPolicyUnset(t *testing.T) {
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "uncapped", Type: runtimestore.TypeAgent,
	})
	srv := sessionserver.New(store, sessionserver.Options{Runtimes: runtimes})

	plan := json.RawMessage(`{
		"schemaVersion": 1,
		"sources": [],
		"setupCommands": [
			{"cmd": "echo a"}, {"cmd": "echo b"}, {"cmd": "echo c"}, {"cmd": "echo d"}
		]
	}`)
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "uncapped", WorkspacePlan: plan,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201 with no policy; body=%s", rr.Code, rr.Body.String())
	}
}

// TestCreateRejectsSetupCommandOutsideAllowlist covers F-7.5.1 / §7.5 line
// 488: the gateway rejects a session whose workspacePlan.setupCommands
// includes a command that does not match any allowlist prefix. The §15.1
// envelope is WORKSPACE_PLAN_INVALID with a structured details payload
// (`field`, `reason`, `mode`, `index`, `command`) so the rejection reason
// is machine-readable as well as human-readable.
func TestCreateRejectsSetupCommandOutsideAllowlist(t *testing.T) {
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "locked",
		Type: runtimestore.TypeAgent,
		SetupCommandPolicy: &runtimestore.SetupCommandPolicy{
			Mode:      runtimestore.SetupCommandModeAllowlist,
			Allowlist: []string{"npm", "make"},
		},
	})
	srv := sessionserver.New(store, sessionserver.Options{Runtimes: runtimes})

	plan := json.RawMessage(`{
		"schemaVersion": 1,
		"sources": [],
		"setupCommands": [{"cmd": "npm ci"}, {"cmd": "curl http://evil"}]
	}`)
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "locked", WorkspacePlan: plan,
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "WORKSPACE_PLAN_INVALID" {
		t.Errorf("error code: got %q, want WORKSPACE_PLAN_INVALID", env.Error.Code)
	}
	if env.Error.Details["reason"] != "setup_command_policy_violation" {
		t.Errorf("details.reason = %v, want setup_command_policy_violation", env.Error.Details["reason"])
	}
	if env.Error.Details["mode"] != "allowlist" {
		t.Errorf("details.mode = %v, want allowlist", env.Error.Details["mode"])
	}
	if got, _ := env.Error.Details["index"].(float64); int(got) != 1 {
		t.Errorf("details.index = %v, want 1 (the second command)", env.Error.Details["index"])
	}
	if env.Error.Details["command"] != "curl http://evil" {
		t.Errorf("details.command = %v, want %q", env.Error.Details["command"], "curl http://evil")
	}
}

// TestCreateRejectsSetupCommandOnBlocklist covers F-7.5.1 / §7.5 line 488:
// the gateway rejects a session when a setup command matches a blocklist
// prefix.
func TestCreateRejectsSetupCommandOnBlocklist(t *testing.T) {
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "blocked",
		Type: runtimestore.TypeAgent,
		SetupCommandPolicy: &runtimestore.SetupCommandPolicy{
			Mode:      runtimestore.SetupCommandModeBlocklist,
			Blocklist: []string{"rm -rf"},
		},
	})
	srv := sessionserver.New(store, sessionserver.Options{Runtimes: runtimes})

	plan := json.RawMessage(`{
		"schemaVersion": 1,
		"sources": [],
		"setupCommands": [{"cmd": "rm -rf /"}]
	}`)
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "blocked", WorkspacePlan: plan,
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Details["mode"] != "blocklist" {
		t.Errorf("details.mode = %v, want blocklist", env.Error.Details["mode"])
	}
	if env.Error.Details["command"] != "rm -rf /" {
		t.Errorf("details.command = %v, want %q", env.Error.Details["command"], "rm -rf /")
	}
}

// TestCreateAdmitsSetupCommandWithinAllowlist is the F-7.5.1 happy path:
// a command that prefix-matches the allowlist is admitted.
func TestCreateAdmitsSetupCommandWithinAllowlist(t *testing.T) {
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "locked",
		Type: runtimestore.TypeAgent,
		SetupCommandPolicy: &runtimestore.SetupCommandPolicy{
			Mode:      runtimestore.SetupCommandModeAllowlist,
			Allowlist: []string{"npm", "make"},
		},
	})
	srv := sessionserver.New(store, sessionserver.Options{Runtimes: runtimes})

	plan := json.RawMessage(`{
		"schemaVersion": 1,
		"sources": [],
		"setupCommands": [{"cmd": "npm ci"}, {"cmd": "make test"}]
	}`)
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "locked", WorkspacePlan: plan,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201 (allowlist match); body=%s", rr.Code, rr.Body.String())
	}
}

// spec: §7.1 steps 4-5 (claim at create, persist binding), §4.6 (durable
// binding), §15.1 (created state: a warm pod has been claimed).
// diagnosis: a create that does not claim a pod or does not persist the
// PodAssignment + PoolRef binding breaks the §7.1 claim-at-create model —
// a later /finalize and /start on a fresh replica could not reconnect to
// the bound pod. A failure here means createSession skipped the §7.1 step-4
// claim or did not stamp the §4.6 binding on the row.
func TestCreateClaimsPodAndPersistsBinding_spec_7_1_4(t *testing.T) {
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.Runtime = &podBindRuntime{}

	cluster := podBindClient(
		t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
	)
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "sess-create-claim" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv)),
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          podTestNS,
	})

	rr := postSessionStep(t, srv.Handler(), "/v1/sessions", mustJSON(sessionserver.CreateSessionRequest{
		RuntimeRef: "echo",
		UserID:     "alice@acme.com",
	}))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	var resp sessionserver.CreateSessionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	// spec: §15.1 — create returns the session in `created` state; the heavy
	// preparation runs at /finalize and the launch at /start.
	if resp.State != "created" {
		t.Errorf("state = %q, want created", resp.State)
	}

	// spec: §4.6 — the durable binding is persisted on the row at create so a
	// later /finalize and /start on any replica reconnect to the bound pod.
	row, err := store.Get(context.Background(), "acme", "sess-create-claim")
	if err != nil {
		t.Fatalf("get created session: %v", err)
	}
	if row.PodAssignment != "sbx-1" {
		t.Errorf("row.PodAssignment = %q, want sbx-1 (claimed at create)", row.PodAssignment)
	}
	if row.PoolRef != "echo-pool" {
		t.Errorf("row.PoolRef = %q, want echo-pool (resolved pool persisted)", row.PoolRef)
	}

	// spec: §4.6.1 — the per-pod SandboxClaim exists in `bound` after the
	// create-time claim, so the pod is reserved through the upload window.
	var claim lennyv1.SandboxClaim
	if err := cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "claim-sbx-1"}, &claim); err != nil {
		t.Fatalf("get per-pod claim after create: %v", err)
	}
	if claim.Status.Phase != string(claimstate.Bound) {
		t.Errorf("claim binding state = %q, want bound after the create-time claim", claim.Status.Phase)
	}
}

// spec: §7.1 line 28 (atomicity: no row on a failed create-step), §15.1
// line 1138 (Retry-After).
// diagnosis: a create against an exhausted pool that persists a row, or
// omits the Retry-After hint, breaks the §7.1 fail-fast-at-create
// guarantee — the client would discover exhaustion only after wasting an
// upload. A failure here means createSession did not surface pool
// exhaustion as a 503 SESSION_CREATION_FAILED before the row persist.
func TestCreateLeavesNoRowOnPoolExhaustion_spec_7_1_28(t *testing.T) {
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.Runtime = &podBindRuntime{}

	// A pool with no idle Sandbox exhausts the claim path (and, absent
	// Postgres, the fallback), so the create-time claim returns ErrNoIdlePod.
	cluster := podBindClient(
		t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
	)
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "sess-create-exhausted" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv)),
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          podTestNS,
	})

	rr := postSessionStep(t, srv.Handler(), "/v1/sessions", mustJSON(sessionserver.CreateSessionRequest{RuntimeRef: "echo"}))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("create on empty pool: status %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	if ra := rr.Header().Get("Retry-After"); ra == "" {
		t.Error("Retry-After header missing on the SESSION_CREATION_FAILED reply")
	}
	if _, err := store.Get(context.Background(), "acme", "sess-create-exhausted"); err == nil {
		t.Error("session row was persisted; §7.1 atomicity requires no row when the create-time claim fails")
	}
}

// spec: §5.2 (service mode is claimless), §7.1 (created state).
// diagnosis: a service-mode create that claims a pod breaks the §5.2
// claimless contract — every service-mode session would burn a warm pod.
// A failure here means claimAtCreate did not take the service-mode branch.
func TestCreateServiceModeIsClaimless_spec_5_2(t *testing.T) {
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.Runtime = &podBindRuntime{}

	cluster := podBindClient(
		t,
		podBindWarmPool("svc-pool", "svc-tmpl"),
		podBindServiceTemplate("svc-tmpl", "svc-runtime", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("svc-sbx-1", "svc-pool", "10.244.3.9"),
	)
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "sess-create-svc" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv)),
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          podTestNS,
		Pools:                   podBindServicePool(t, "svc-pool", "svc-runtime", 8),
	})

	rr := postSessionStep(t, srv.Handler(), "/v1/sessions", mustJSON(sessionserver.CreateSessionRequest{RuntimeRef: "svc-runtime"}))
	if rr.Code != http.StatusCreated {
		t.Fatalf("service-mode create: status %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	row, err := store.Get(context.Background(), "acme", "sess-create-svc")
	if err != nil {
		t.Fatalf("get service-mode created row: %v", err)
	}
	if row.PodAssignment != "" {
		t.Errorf("row.PodAssignment = %q, want empty (service mode is claimless)", row.PodAssignment)
	}
	if row.ExecutionMode != "service" {
		t.Errorf("row.ExecutionMode = %q, want service (level taken from the resolved pool)", row.ExecutionMode)
	}

	// The idle Sandbox is untouched: no per-pod claim was created at create.
	var claim lennyv1.SandboxClaim
	if err := cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "claim-svc-sbx-1"}, &claim); !apierrors.IsNotFound(err) {
		t.Errorf("service-mode create created/looked up claim-svc-sbx-1 (err=%v); want NotFound (claimless)", err)
	}
}

// spec: §7.1 line 28 (rollback releases the pod claim on a create-step
// failure), §4.6 (durable binding).
// diagnosis: a create whose row persist fails after the §7.1 step-4 claim
// must release the claimed pod, or every persist failure leaks a warm pod.
// A failure here means createSession did not roll back the create-time
// claim when store.Create failed.
func TestCreateRollsBackClaimOnPersistFailure_spec_7_1_28(t *testing.T) {
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.Runtime = &podBindRuntime{}

	cluster := podBindClient(
		t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("sbx-rb", "echo-pool", "10.244.2.6"),
	)
	store := &createRejectingStore{Store: memstore.New()}
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "sess-create-rb" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv)),
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          podTestNS,
	})

	rr := postSessionStep(t, srv.Handler(), "/v1/sessions", mustJSON(sessionserver.CreateSessionRequest{RuntimeRef: "echo"}))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("create with rejecting store: status %d, want 503; body=%s", rr.Code, rr.Body.String())
	}

	// spec: §7.1 line 28 — the create-time claim is rolled back: the per-pod
	// SandboxClaim is deleted, returning the pod to the pool.
	var claim lennyv1.SandboxClaim
	if err := cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "claim-sbx-rb"}, &claim); !apierrors.IsNotFound(err) {
		t.Errorf("per-pod claim after a rolled-back create (err=%v); want NotFound (claim released)", err)
	}
}
