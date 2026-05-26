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

	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
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
