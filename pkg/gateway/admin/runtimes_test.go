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

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

func newRuntimeAdmin(t *testing.T) (*admin.Router, runtimestore.Store, *recordingAudit) {
	t.Helper()
	tenants := tenantstore.NewMemory()
	runtimes := runtimestore.NewMemory()
	audit := &recordingAudit{}
	router := admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithRuntimes(runtimes)
	return router, runtimes, audit
}

func runtimeRequest(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewReader(b)
	} else {
		buf = bytes.NewReader(nil)
	}
	req := withAdminPrincipal(httptest.NewRequest(method, path, buf))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestCreateRuntimeWithDelegationPolicyRef(t *testing.T) {
	router, store, _ := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:                "claude-code",
		Type:                "agent",
		Image:               "ghcr.io/anthropic/claude-code@sha256:abcdef",
		ExecutionMode:       "session",
		IsolationProfile:    "sandboxed",
		IntegrationLevel:    "full",
		DelegationPolicyRef: "orchestrator-policy",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var resp admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.DelegationPolicyRef != "orchestrator-policy" {
		t.Errorf("response delegationPolicyRef = %q, want orchestrator-policy", resp.DelegationPolicyRef)
	}
	row, err := store.Get(context.Background(), "claude-code")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if row.DelegationPolicyRef != "orchestrator-policy" {
		t.Errorf("stored delegationPolicyRef = %q, want orchestrator-policy", row.DelegationPolicyRef)
	}
}

func TestUpdateRuntimeDelegationPolicyRef(t *testing.T) {
	router, store, _ := newRuntimeAdmin(t)
	if err := store.Create(context.Background(), runtimestore.Runtime{
		Name: "claude-code", DelegationPolicyRef: "old-policy",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ref := "new-policy"
	rr := runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/claude-code",
		admin.UpdateRuntimeRequest{DelegationPolicyRef: &ref})
	if rr.Code != http.StatusOK {
		t.Fatalf("update: status %d, body %s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "claude-code")
	if row.DelegationPolicyRef != "new-policy" {
		t.Errorf("stored delegationPolicyRef = %q, want new-policy", row.DelegationPolicyRef)
	}
}

func TestCreateRuntimeHappyPath(t *testing.T) {
	router, store, audit := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:             "claude-code",
		Type:             "agent",
		Image:            "ghcr.io/anthropic/claude-code@sha256:abcdef",
		ExecutionMode:    "session",
		IsolationProfile: "sandboxed",
		IntegrationLevel: "full",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var resp admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Name != "claude-code" {
		t.Errorf("Name: got %q", resp.Name)
	}
	row, err := store.Get(context.Background(), "claude-code")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if row.IsolationProfile != isolation.ProfileSandboxed {
		t.Errorf("Stored IsolationProfile: got %q", row.IsolationProfile)
	}
	if len(audit.snapshot()) != 1 || audit.snapshot()[0].Type != "admin.runtime.created" {
		t.Errorf("audit: got %+v", audit.snapshot())
	}
}

func TestCreateRuntimeDefaultsTypeAndIsolation(t *testing.T) {
	router, store, _ := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:  "echo",
		Image: "lenny/echo@sha256:def",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "echo")
	if row.Type != runtimestore.TypeAgent {
		t.Errorf("default Type: got %q, want agent", row.Type)
	}
	if row.IsolationProfile != isolation.Default() {
		t.Errorf("default IsolationProfile: got %q", row.IsolationProfile)
	}
}

func TestCreateRuntimeRejectsNonDigestImage(t *testing.T) {
	router, _, _ := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:  "echo",
		Image: "lenny/echo:v1", // tag only, no digest
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("non-digest image should be rejected: got %d", rr.Code)
	}
}

func TestCreateRuntimeRejectsInvalidEnum(t *testing.T) {
	router, _, _ := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:             "echo",
		Image:            "lenny/echo@sha256:def",
		ExecutionMode:    "supernatural", // invalid
		IsolationProfile: "sandboxed",
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("invalid enum should be rejected: got %d", rr.Code)
	}
}

func TestCreateRuntimeRejectsInvalidName(t *testing.T) {
	router, _, _ := newRuntimeAdmin(t)
	for _, name := range []string{"With-Caps", "", "/slash"} {
		rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{Name: name})
		if rr.Code != http.StatusBadRequest {
			t.Errorf("name %q: got %d, want 400", name, rr.Code)
		}
	}
}

func TestCreateRuntimeRejectsDuplicate(t *testing.T) {
	router, store, _ := newRuntimeAdmin(t)
	_ = store.Create(context.Background(), runtimestore.Runtime{Name: "echo"})
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{Name: "echo"})
	if rr.Code != http.StatusConflict {
		t.Errorf("duplicate: got %d, want 409", rr.Code)
	}
}

func TestListRuntimesFilterByType(t *testing.T) {
	router, store, _ := newRuntimeAdmin(t)
	_ = store.Create(context.Background(), runtimestore.Runtime{Name: "a-agent", Type: runtimestore.TypeAgent})
	_ = store.Create(context.Background(), runtimestore.Runtime{Name: "b-mcp", Type: runtimestore.TypeMCP})

	rr := runtimeRequest(t, router.Handler(), http.MethodGet, "/v1/admin/runtimes?type=mcp", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp struct {
		Runtimes []admin.RuntimePayload `json:"runtimes"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Runtimes) != 1 || resp.Runtimes[0].Name != "b-mcp" {
		t.Errorf("filter by type=mcp: got %+v", resp.Runtimes)
	}
}

func TestGetRuntimeMissing(t *testing.T) {
	router, _, _ := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodGet, "/v1/admin/runtimes/missing", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rr.Code)
	}
}

func TestUpdateRuntimeMergesFields(t *testing.T) {
	router, store, audit := newRuntimeAdmin(t)
	_ = store.Create(context.Background(), runtimestore.Runtime{
		Name:             "echo",
		Image:            "lenny/echo@sha256:abc",
		ExecutionMode:    runtimestore.ExecutionModeSession,
		IsolationProfile: isolation.ProfileSandboxed,
	})

	desc := "echo reference runtime"
	body := admin.UpdateRuntimeRequest{Description: &desc}
	rr := runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/echo", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Description != desc {
		t.Errorf("Description: got %q, want %q", resp.Description, desc)
	}
	if resp.ExecutionMode != "session" {
		t.Errorf("ExecutionMode preserved: got %q", resp.ExecutionMode)
	}
	if len(audit.snapshot()) != 1 || audit.snapshot()[0].Type != "admin.runtime.updated" {
		t.Errorf("audit: got %+v", audit.snapshot())
	}
}

func TestUpdateRuntimeRejectsInvalidEnum(t *testing.T) {
	router, store, _ := newRuntimeAdmin(t)
	_ = store.Create(context.Background(), runtimestore.Runtime{Name: "echo"})
	mode := "supernatural"
	rr := runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/echo",
		admin.UpdateRuntimeRequest{ExecutionMode: &mode})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("invalid mode in update: got %d", rr.Code)
	}
}

func TestDeleteRuntimeSoftDeletes(t *testing.T) {
	router, store, audit := newRuntimeAdmin(t)
	_ = store.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	rr := runtimeRequest(t, router.Handler(), http.MethodDelete, "/v1/admin/runtimes/echo", nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status: %d", rr.Code)
	}
	row, err := store.Get(context.Background(), "echo")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if row.DeletedAt.IsZero() {
		t.Errorf("DeletedAt should be set")
	}
	if len(audit.snapshot()) != 1 || audit.snapshot()[0].Type != "admin.runtime.soft_deleted" {
		t.Errorf("audit: got %+v", audit.snapshot())
	}
}

// TestRuntimeEndpointsRequirePlatformAdmin covers the runtime mutation
// endpoints. The GET endpoints are §4 tenant-scoped (a tenant-admin may
// call them, filtered) and are covered by the tenant-scoping tests.
func TestRuntimeEndpointsRequirePlatformAdmin(t *testing.T) {
	router, store, _ := newRuntimeAdmin(t)
	_ = store.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	for _, c := range []struct {
		method, path string
		body         []byte
	}{
		{http.MethodPost, "/v1/admin/runtimes", []byte(`{"name":"x"}`)},
		{http.MethodPut, "/v1/admin/runtimes/echo", []byte("{}")},
		{http.MethodDelete, "/v1/admin/runtimes/echo", nil},
	} {
		req := withTenantAdminPrincipal(httptest.NewRequest(c.method, c.path, bytes.NewReader(c.body)))
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Errorf("%s %s: got %d, want 403", c.method, c.path, rr.Code)
		}
	}
}
