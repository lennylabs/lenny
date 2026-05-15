// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
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
