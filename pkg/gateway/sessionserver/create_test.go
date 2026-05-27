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

	"github.com/lennylabs/lenny/pkg/environment"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
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
		Sessions []sessionserver.SessionResponse `json:"sessions"`
	}
	if err := json.Unmarshal(lr.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode LIST: %v", err)
	}
	if len(listResp.Sessions) != 1 {
		t.Fatalf("LIST returned %d sessions, want 1", len(listResp.Sessions))
	}
	if listResp.Sessions[0].SessionIsolationLevel.IsolationProfile != string(isolation.ProfileMicrovm) {
		t.Errorf("LIST sessionIsolationLevel.isolationProfile = %q, want microvm",
			listResp.Sessions[0].SessionIsolationLevel.IsolationProfile)
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

// TestCreateWithNamedEnvironmentBypassesAdmissionGate_spec_11_1 — when
// the request names an environment the §11.1 line 13 gate does not
// apply: the explicit-environment path is governed by §10.6
// membership checks (out of scope for this gap).
func TestCreateWithNamedEnvironmentBypassesAdmissionGate_spec_11_1(t *testing.T) {
	srv, _ := seedNoEnvServer(t, "sess_named", tenantstore.NoEnvPolicyDenyAll, tenantstore.NoEnvPolicyDenyAll)

	rr := createRequestAs(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef:  "claude-code",
		Environment: "security-team",
	}, authmw.Principal{Subject: "alice@acme.com", TenantID: "acme"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (named environment bypasses the §11.1 line 13 gate); body=%s", rr.Code, rr.Body.String())
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
