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

func TestRuntimeLabelsRoundTrip(t *testing.T) {
	// §5.1: the admin runtime API round-trips the label set on create,
	// read, and update.
	router, _, _ := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:   "scanner",
		Image:  "lenny/scanner@sha256:abc",
		Labels: map[string]string{"team": "security", "approved": "true"},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var created admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	if created.Labels["team"] != "security" || created.Labels["approved"] != "true" {
		t.Errorf("create response labels: %+v", created.Labels)
	}

	rr = runtimeRequest(t, router.Handler(), http.MethodGet, "/v1/admin/runtimes/scanner", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get: status %d", rr.Code)
	}
	var got admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got.Labels["team"] != "security" {
		t.Errorf("get response labels: %+v", got.Labels)
	}

	newLabels := map[string]string{"team": "platform"}
	rr = runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/scanner",
		admin.UpdateRuntimeRequest{Labels: &newLabels})
	if rr.Code != http.StatusOK {
		t.Fatalf("update: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var updated admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &updated)
	if updated.Labels["team"] != "platform" || len(updated.Labels) != 1 {
		t.Errorf("update did not replace labels: %+v", updated.Labels)
	}
}

func TestRuntimeAgentInterfaceRoundTrip(t *testing.T) {
	// §5.1: the admin runtime API round-trips the agentInterface
	// descriptor on create, read, and update.
	router, _, _ := newRuntimeAdmin(t)
	iface := &runtimestore.AgentInterface{
		Description:            "Analyzes codebases",
		InputModes:             []runtimestore.AgentInterfaceMode{{Type: "text/plain"}},
		SupportsWorkspaceFiles: true,
		Skills:                 []runtimestore.AgentInterfaceSkill{{ID: "review", Name: "Code Review"}},
	}
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:           "refactorer",
		Image:          "lenny/refactorer@sha256:abc",
		AgentInterface: iface,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var created admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	if created.AgentInterface == nil || created.AgentInterface.Description != "Analyzes codebases" ||
		!created.AgentInterface.SupportsWorkspaceFiles || len(created.AgentInterface.Skills) != 1 {
		t.Errorf("create response agentInterface: %+v", created.AgentInterface)
	}

	rr = runtimeRequest(t, router.Handler(), http.MethodGet, "/v1/admin/runtimes/refactorer", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get: status %d", rr.Code)
	}
	var got admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got.AgentInterface == nil || len(got.AgentInterface.Skills) != 1 ||
		got.AgentInterface.Skills[0].ID != "review" {
		t.Errorf("get response agentInterface: %+v", got.AgentInterface)
	}

	// A PUT carrying an agentInterface object replaces the descriptor.
	replacement, _ := json.Marshal(&runtimestore.AgentInterface{Description: "Refactors only"})
	rr = runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/refactorer",
		admin.UpdateRuntimeRequest{AgentInterface: replacement})
	if rr.Code != http.StatusOK {
		t.Fatalf("update: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var updated admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &updated)
	if updated.AgentInterface == nil || updated.AgentInterface.Description != "Refactors only" ||
		len(updated.AgentInterface.Skills) != 0 {
		t.Errorf("update did not replace agentInterface: %+v", updated.AgentInterface)
	}

	// A PUT carrying JSON null clears the descriptor.
	rr = runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/refactorer",
		admin.UpdateRuntimeRequest{AgentInterface: json.RawMessage("null")})
	if rr.Code != http.StatusOK {
		t.Fatalf("update-clear: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var cleared admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &cleared)
	if cleared.AgentInterface != nil {
		t.Errorf("PUT null did not clear agentInterface: %+v", cleared.AgentInterface)
	}
}

func TestRuntimeAgentInterfaceRejectedOnMCP(t *testing.T) {
	// §5.1: type:mcp runtimes do not carry an agentInterface.
	router, runtimes, _ := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:           "mcp-tool",
		Type:           "mcp",
		Image:          "lenny/mcp-tool@sha256:abc",
		AgentInterface: &runtimestore.AgentInterface{Description: "should be rejected"},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("create with agentInterface on type:mcp: status %d, want 400 (body=%s)", rr.Code, rr.Body.String())
	}

	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "mcp-live", Type: runtimestore.TypeMCP, Image: "lenny/mcp-live@sha256:abc",
	})
	raw, _ := json.Marshal(&runtimestore.AgentInterface{Description: "nope"})
	rr = runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/mcp-live",
		admin.UpdateRuntimeRequest{AgentInterface: raw})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("update with agentInterface on type:mcp: status %d, want 400 (body=%s)", rr.Code, rr.Body.String())
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
