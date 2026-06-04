// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/agentcard"
	"github.com/lennylabs/lenny/pkg/gateway/capabilityinference"
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
	// spec: §15.1 lines 1207-1211 — runtime PUTs enforce If-Match. A test
	// that is not driving the precondition reaches the handler by carrying
	// the runtime's current ETag; one that exercises the precondition sets
	// If-Match explicitly and is left untouched.
	injectAdminIfMatch(t, h, req)
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
		Runtimes []admin.RuntimePayload `json:"items"`
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

func TestRuntimePublishedMetadataRoundTrip(t *testing.T) {
	// §5.1: the admin runtime API round-trips the publishedMetadata list
	// on create, read, and update.
	router, _, _ := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:  "carded",
		Image: "lenny/carded@sha256:abc",
		PublishedMetadata: []runtimestore.PublishedMetadataEntry{
			{Key: "agent-card", ContentType: "application/json", Visibility: runtimestore.VisibilityPublic, Content: `{"n":"x"}`},
		},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var created admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	if len(created.PublishedMetadata) != 1 || created.PublishedMetadata[0].Key != "agent-card" {
		t.Errorf("create response publishedMetadata: %+v", created.PublishedMetadata)
	}

	rr = runtimeRequest(t, router.Handler(), http.MethodGet, "/v1/admin/runtimes/carded", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get: status %d", rr.Code)
	}
	var got admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got.PublishedMetadata) != 1 || got.PublishedMetadata[0].Content != `{"n":"x"}` {
		t.Errorf("get response publishedMetadata: %+v", got.PublishedMetadata)
	}

	// A PUT replaces the list.
	replacement := []runtimestore.PublishedMetadataEntry{
		{Key: "spec", ContentType: "application/yaml", Visibility: runtimestore.VisibilityInternal},
	}
	rr = runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/carded",
		admin.UpdateRuntimeRequest{PublishedMetadata: &replacement})
	if rr.Code != http.StatusOK {
		t.Fatalf("update: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var updated admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &updated)
	if len(updated.PublishedMetadata) != 1 || updated.PublishedMetadata[0].Key != "spec" {
		t.Errorf("update did not replace publishedMetadata: %+v", updated.PublishedMetadata)
	}

	// An empty list clears the published entries.
	empty := []runtimestore.PublishedMetadataEntry{}
	rr = runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/carded",
		admin.UpdateRuntimeRequest{PublishedMetadata: &empty})
	if rr.Code != http.StatusOK {
		t.Fatalf("update-clear: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var cleared admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &cleared)
	if len(cleared.PublishedMetadata) != 0 {
		t.Errorf("empty list did not clear publishedMetadata: %+v", cleared.PublishedMetadata)
	}
}

func TestCreateRuntimeRejectsInvalidPublishedMetadata(t *testing.T) {
	// §5.1: publishedMetadata keys must be non-empty and unique, and
	// each entry needs a valid visibility class.
	router, _, _ := newRuntimeAdmin(t)
	cases := []struct {
		name    string
		entries []runtimestore.PublishedMetadataEntry
	}{
		{"dup-keys", []runtimestore.PublishedMetadataEntry{
			{Key: "dup", Visibility: runtimestore.VisibilityPublic},
			{Key: "dup", Visibility: runtimestore.VisibilityTenant},
		}},
		{"bad-visibility", []runtimestore.PublishedMetadataEntry{
			{Key: "k", Visibility: "world"},
		}},
		{"empty-key", []runtimestore.PublishedMetadataEntry{
			{Key: "", Visibility: runtimestore.VisibilityPublic},
		}},
	}
	for _, tc := range cases {
		rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
			Name:              tc.name,
			Image:             "lenny/x@sha256:abc",
			PublishedMetadata: tc.entries,
		})
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400 (body=%s)", tc.name, rr.Code, rr.Body.String())
		}
	}
}

func agentCardEntry(t *testing.T, entries []runtimestore.PublishedMetadataEntry) runtimestore.PublishedMetadataEntry {
	t.Helper()
	for _, e := range entries {
		if e.Key == agentcard.Key {
			return e
		}
	}
	t.Fatalf("no agent-card entry in %+v", entries)
	return runtimestore.PublishedMetadataEntry{}
}

func TestCreateRuntimeAutoGeneratesAgentCard(t *testing.T) {
	// §5.1: a runtime created with an agentInterface gets a write-time
	// auto-generated A2A agent card stored as a publishedMetadata entry.
	router, _, _ := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:  "refactorer",
		Image: "lenny/refactorer@sha256:abc",
		AgentInterface: &runtimestore.AgentInterface{
			Description: "Analyzes codebases",
			Skills:      []runtimestore.AgentInterfaceSkill{{ID: "review"}},
		},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var created admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &created)

	entry := agentCardEntry(t, created.PublishedMetadata)
	if entry.Visibility != runtimestore.VisibilityPublic || entry.ContentType != "application/json" {
		t.Errorf("agent-card entry: %+v", entry)
	}
	var card agentcard.Card
	if err := json.Unmarshal([]byte(entry.Content), &card); err != nil {
		t.Fatalf("agent-card content is not a card: %v", err)
	}
	if card.Name != "refactorer" || card.Description != "Analyzes codebases" || len(card.Skills) != 1 {
		t.Errorf("generated card: %+v", card)
	}
	if card.GeneratedAt == "" || card.GeneratorVersion == "" {
		t.Errorf("card missing the §5.1 envelope fields: %+v", card)
	}
}

func TestCreateRuntimeKeepsHandcraftedCardWithoutAgentInterface(t *testing.T) {
	// §5.1: a runtime with no agentInterface keeps its hand-crafted
	// agent-card entry; the gateway does not auto-generate one.
	router, _, _ := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:  "handcrafted",
		Image: "lenny/handcrafted@sha256:abc",
		PublishedMetadata: []runtimestore.PublishedMetadataEntry{
			{Key: "agent-card", ContentType: "application/json", Visibility: runtimestore.VisibilityPublic, Content: `{"hand":"crafted"}`},
		},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var created admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	if entry := agentCardEntry(t, created.PublishedMetadata); entry.Content != `{"hand":"crafted"}` {
		t.Errorf("hand-crafted agent-card must be left untouched: %q", entry.Content)
	}
}

func TestUpdateRuntimeRegeneratesAgentCard(t *testing.T) {
	// §5.1: the auto-generated card is refreshed at update write time.
	router, runtimes, _ := newRuntimeAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "refactorer", Type: runtimestore.TypeAgent,
		AgentInterface: &runtimestore.AgentInterface{Description: "old"},
	})
	newIface, _ := json.Marshal(&runtimestore.AgentInterface{Description: "new and improved"})
	rr := runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/refactorer",
		admin.UpdateRuntimeRequest{AgentInterface: newIface})
	if rr.Code != http.StatusOK {
		t.Fatalf("update: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var updated admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &updated)
	entry := agentCardEntry(t, updated.PublishedMetadata)
	var card agentcard.Card
	if err := json.Unmarshal([]byte(entry.Content), &card); err != nil {
		t.Fatalf("regenerated card is not a card: %v", err)
	}
	if card.Description != "new and improved" {
		t.Errorf("update did not regenerate the card: %+v", card)
	}
}

func TestRegenerateCardsRegeneratesAndSkips(t *testing.T) {
	// §5.1: regenerate-cards regenerates every runtime with an
	// agentInterface and skips the rest, leaving hand-crafted cards be.
	router, runtimes, _ := newRuntimeAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "withiface", Type: runtimestore.TypeAgent,
		AgentInterface: &runtimestore.AgentInterface{Description: "has interface"},
	})
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "noiface", Type: runtimestore.TypeAgent,
		PublishedMetadata: []runtimestore.PublishedMetadataEntry{
			{Key: "agent-card", ContentType: "application/json", Visibility: runtimestore.VisibilityPublic, Content: `{"hand":"crafted"}`},
		},
	})
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes/regenerate-cards",
		admin.RegenerateCardsRequest{})
	if rr.Code != http.StatusOK {
		t.Fatalf("regenerate: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp admin.RegenerateCardsResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Regenerated) != 1 || resp.Regenerated[0] != "withiface" {
		t.Errorf("regenerated: %+v, want [withiface]", resp.Regenerated)
	}
	if len(resp.Skipped) != 1 || resp.Skipped[0] != "noiface" {
		t.Errorf("skipped: %+v, want [noiface]", resp.Skipped)
	}
	got, _ := runtimes.Get(context.Background(), "withiface")
	var card agentcard.Card
	_ = json.Unmarshal([]byte(agentCardEntry(t, got.PublishedMetadata).Content), &card)
	if card.Name != "withiface" {
		t.Errorf("regenerated card: %+v", card)
	}
	handcrafted, _ := runtimes.Get(context.Background(), "noiface")
	if e := agentCardEntry(t, handcrafted.PublishedMetadata); e.Content != `{"hand":"crafted"}` {
		t.Errorf("hand-crafted card must be left untouched: %q", e.Content)
	}
}

func TestRegenerateCardsDryRun(t *testing.T) {
	// §5.1: dryRun reports the affected runtimes without writing.
	router, runtimes, _ := newRuntimeAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "withiface", Type: runtimestore.TypeAgent,
		AgentInterface: &runtimestore.AgentInterface{Description: "x"},
	})
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes/regenerate-cards",
		admin.RegenerateCardsRequest{DryRun: true})
	if rr.Code != http.StatusOK {
		t.Fatalf("dry run: status %d", rr.Code)
	}
	var resp admin.RegenerateCardsResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Regenerated) != 1 {
		t.Errorf("dry run regenerated: %+v", resp.Regenerated)
	}
	got, _ := runtimes.Get(context.Background(), "withiface")
	for _, e := range got.PublishedMetadata {
		if e.Key == agentcard.Key {
			t.Error("dry run must not write the card")
		}
	}
}

func TestRegenerateCardsVersionThreshold(t *testing.T) {
	// §5.1: generatorVersionBefore regenerates only runtimes whose
	// stored card is stamped by a strictly older generator.
	router, runtimes, _ := newRuntimeAdmin(t)
	stale := agentcard.Entry("stale", runtimestore.AgentInterface{Description: "stale"}, time.Now(), "1.0.0")
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "stale", Type: runtimestore.TypeAgent,
		AgentInterface:    &runtimestore.AgentInterface{Description: "stale"},
		PublishedMetadata: []runtimestore.PublishedMetadataEntry{stale},
	})
	fresh := agentcard.Entry("fresh", runtimestore.AgentInterface{Description: "fresh"}, time.Now(), "2.0.0")
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "fresh", Type: runtimestore.TypeAgent,
		AgentInterface:    &runtimestore.AgentInterface{Description: "fresh"},
		PublishedMetadata: []runtimestore.PublishedMetadataEntry{fresh},
	})
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes/regenerate-cards",
		admin.RegenerateCardsRequest{GeneratorVersionBefore: "2.0.0"})
	if rr.Code != http.StatusOK {
		t.Fatalf("regenerate: status %d", rr.Code)
	}
	var resp admin.RegenerateCardsResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Regenerated) != 1 || resp.Regenerated[0] != "stale" {
		t.Errorf("regenerated: %+v, want [stale]", resp.Regenerated)
	}
	if len(resp.Skipped) != 1 || resp.Skipped[0] != "fresh" {
		t.Errorf("skipped: %+v, want [fresh]", resp.Skipped)
	}
}

func TestRuntimeCapabilityInferenceModeRoundTrip(t *testing.T) {
	// §5.1: the admin runtime API round-trips capabilityInferenceMode
	// and defaults it to strict.
	router, _, _ := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:  "defaulted",
		Image: "lenny/defaulted@sha256:abc",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var created admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	if created.CapabilityInferenceMode != "strict" {
		t.Errorf("default capabilityInferenceMode: %q, want strict", created.CapabilityInferenceMode)
	}

	permissive := "permissive"
	rr = runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/defaulted",
		admin.UpdateRuntimeRequest{CapabilityInferenceMode: &permissive})
	if rr.Code != http.StatusOK {
		t.Fatalf("update: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var updated admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &updated)
	if updated.CapabilityInferenceMode != "permissive" {
		t.Errorf("update did not switch capabilityInferenceMode: %q", updated.CapabilityInferenceMode)
	}
}

func TestCreateRuntimeRejectsInvalidCapabilityInferenceMode(t *testing.T) {
	// §5.1: capabilityInferenceMode is the strict / permissive enum.
	router, _, _ := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:                    "bad",
		Image:                   "lenny/bad@sha256:abc",
		CapabilityInferenceMode: "loose",
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("invalid capabilityInferenceMode: status %d, want 400", rr.Code)
	}
}

func TestRuntimeToolCapabilityOverridesRoundTrip(t *testing.T) {
	// §5.1: the admin runtime API round-trips toolCapabilityOverrides.
	router, _, _ := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:  "rt",
		Image: "lenny/rt@sha256:abc",
		ToolCapabilityOverrides: map[string][]capabilityinference.Capability{
			"deploy": {capabilityinference.CapExecute, capabilityinference.CapAdmin},
		},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var created admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	if len(created.ToolCapabilityOverrides["deploy"]) != 2 {
		t.Errorf("create response toolCapabilityOverrides: %+v", created.ToolCapabilityOverrides)
	}

	rr = runtimeRequest(t, router.Handler(), http.MethodGet, "/v1/admin/runtimes/rt", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get: status %d", rr.Code)
	}
	var got admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got.ToolCapabilityOverrides["deploy"]) != 2 ||
		got.ToolCapabilityOverrides["deploy"][0] != capabilityinference.CapExecute {
		t.Errorf("get response toolCapabilityOverrides: %+v", got.ToolCapabilityOverrides)
	}

	replacement := map[string][]capabilityinference.Capability{
		"read_tool": {capabilityinference.CapRead},
	}
	rr = runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/rt",
		admin.UpdateRuntimeRequest{ToolCapabilityOverrides: &replacement})
	if rr.Code != http.StatusOK {
		t.Fatalf("update: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var updated admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &updated)
	if _, ok := updated.ToolCapabilityOverrides["read_tool"]; !ok || len(updated.ToolCapabilityOverrides) != 1 {
		t.Errorf("update did not replace toolCapabilityOverrides: %+v", updated.ToolCapabilityOverrides)
	}
}

func TestCreateRuntimeRejectsInvalidToolCapabilityOverrides(t *testing.T) {
	// §5.1: a toolCapabilityOverrides entry must name known §5.3
	// capabilities.
	router, _, _ := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:  "bad",
		Image: "lenny/bad@sha256:abc",
		ToolCapabilityOverrides: map[string][]capabilityinference.Capability{
			"tool": {"superuser"},
		},
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("invalid capability in overrides: status %d, want 400", rr.Code)
	}
}

func TestRuntimeSetupPolicyRoundTrip(t *testing.T) {
	// §5.1: the admin runtime API round-trips the setupPolicy block.
	router, _, _ := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:        "rt",
		Image:       "lenny/rt@sha256:abc",
		SetupPolicy: &runtimestore.SetupPolicy{TimeoutSeconds: 300, OnTimeout: runtimestore.SetupTimeoutFail},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var created admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	if created.SetupPolicy == nil || created.SetupPolicy.TimeoutSeconds != 300 {
		t.Errorf("create response setupPolicy: %+v", created.SetupPolicy)
	}

	replacement := &runtimestore.SetupPolicy{TimeoutSeconds: 600, OnTimeout: runtimestore.SetupTimeoutWarn}
	rr = runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/rt",
		admin.UpdateRuntimeRequest{SetupPolicy: replacement})
	if rr.Code != http.StatusOK {
		t.Fatalf("update: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var updated admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &updated)
	if updated.SetupPolicy == nil || updated.SetupPolicy.TimeoutSeconds != 600 ||
		updated.SetupPolicy.OnTimeout != runtimestore.SetupTimeoutWarn {
		t.Errorf("update did not replace setupPolicy: %+v", updated.SetupPolicy)
	}
}

func TestCreateRuntimeRejectsInvalidSetupPolicy(t *testing.T) {
	// §5.1: setupPolicy.onTimeout is the fail / warn enum and the
	// timeout must not be negative.
	router, _, _ := newRuntimeAdmin(t)
	cases := []*runtimestore.SetupPolicy{
		{TimeoutSeconds: 100, OnTimeout: "abort"},
		{TimeoutSeconds: -5, OnTimeout: runtimestore.SetupTimeoutFail},
	}
	for i, p := range cases {
		rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
			Name:        "bad",
			Image:       "lenny/bad@sha256:abc",
			SetupPolicy: p,
		})
		if rr.Code != http.StatusBadRequest {
			t.Errorf("case %d: status %d, want 400 (body=%s)", i, rr.Code, rr.Body.String())
		}
	}
}

func TestRuntimeCapabilitiesRoundTrip(t *testing.T) {
	// §5.1: the admin runtime API round-trips the capabilities block.
	router, _, _ := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:  "rt",
		Image: "lenny/rt@sha256:abc",
		Capabilities: &runtimestore.RuntimeCapabilities{
			Interaction: runtimestore.InteractionMultiTurn,
			Injection: runtimestore.InjectionCapability{
				Supported: true,
				Modes:     []runtimestore.InjectionMode{runtimestore.InjectionImmediate},
			},
		},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var created admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	if created.Capabilities == nil || created.Capabilities.Interaction != runtimestore.InteractionMultiTurn ||
		!created.Capabilities.Injection.Supported {
		t.Errorf("create response capabilities: %+v", created.Capabilities)
	}

	replacement := &runtimestore.RuntimeCapabilities{Interaction: runtimestore.InteractionOneShot}
	rr = runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/rt",
		admin.UpdateRuntimeRequest{Capabilities: replacement})
	if rr.Code != http.StatusOK {
		t.Fatalf("update: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var updated admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &updated)
	if updated.Capabilities == nil || updated.Capabilities.Interaction != runtimestore.InteractionOneShot {
		t.Errorf("update did not replace capabilities: %+v", updated.Capabilities)
	}
}

func TestCreateRuntimeRejectsIncoherentCapabilities(t *testing.T) {
	// §5.1: a multi_turn runtime must declare injection.supported true.
	router, _, _ := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:  "incoherent",
		Image: "lenny/incoherent@sha256:abc",
		Capabilities: &runtimestore.RuntimeCapabilities{
			Interaction: runtimestore.InteractionMultiTurn,
			Injection:   runtimestore.InjectionCapability{Supported: false},
		},
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("multi_turn without injection.supported: status %d, want 400 (body=%s)", rr.Code, rr.Body.String())
	}

	bad := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:         "badenum",
		Image:        "lenny/badenum@sha256:abc",
		Capabilities: &runtimestore.RuntimeCapabilities{Interaction: "batch"},
	})
	if bad.Code != http.StatusBadRequest {
		t.Errorf("unknown interaction: status %d, want 400", bad.Code)
	}
}

func TestRuntimeMinPlatformVersionRoundTrip(t *testing.T) {
	// §5.1: the admin runtime API round-trips minPlatformVersion.
	router, _, _ := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:               "rt",
		Image:              "lenny/rt@sha256:abc",
		MinPlatformVersion: "1.0.0",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var created admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	if created.MinPlatformVersion != "1.0.0" {
		t.Errorf("create response minPlatformVersion: %q", created.MinPlatformVersion)
	}
	bumped := "1.1.0"
	rr = runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/rt",
		admin.UpdateRuntimeRequest{MinPlatformVersion: &bumped})
	if rr.Code != http.StatusOK {
		t.Fatalf("update: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var updated admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &updated)
	if updated.MinPlatformVersion != "1.1.0" {
		t.Errorf("update minPlatformVersion: %q", updated.MinPlatformVersion)
	}
}

func TestCreateRuntimeRejectsMinPlatformVersionAboveGateway(t *testing.T) {
	// §5.1: the gateway rejects registration when its own version is
	// below the runtime's minPlatformVersion.
	router, _, _ := newRuntimeAdmin(t)
	router = router.WithPlatformInfo(admin.PlatformInfo{Version: "1.2.0"}, nil)

	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:               "futuristic",
		Image:              "lenny/futuristic@sha256:abc",
		MinPlatformVersion: "2.0.0",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("minPlatformVersion above gateway: status %d, want 400 (body=%s)", rr.Code, rr.Body.String())
	}

	ok := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:               "compatible",
		Image:              "lenny/compatible@sha256:abc",
		MinPlatformVersion: "1.0.0",
	})
	if ok.Code != http.StatusCreated {
		t.Errorf("satisfied minPlatformVersion: status %d, want 201 (body=%s)", ok.Code, ok.Body.String())
	}
}

func TestCreateRuntimeRejectsMalformedMinPlatformVersion(t *testing.T) {
	// §5.1: minPlatformVersion must be a valid version string.
	router, _, _ := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:               "bad",
		Image:              "lenny/bad@sha256:abc",
		MinPlatformVersion: "not-a-version",
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("malformed minPlatformVersion: status %d, want 400", rr.Code)
	}
}

func TestRuntimeTaskPolicyRoundTrip(t *testing.T) {
	// §5.1: the admin runtime API round-trips the taskPolicy block.
	router, _, _ := newRuntimeAdmin(t)
	retries := 2
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:  "rt",
		Image: "lenny/rt@sha256:abc",
		TaskPolicy: &runtimestore.TaskPolicy{
			AcknowledgeBestEffortScrub: true,
			MicrovmScrubMode:           runtimestore.MicrovmScrubRestart,
			OnCleanupFailure:           runtimestore.CleanupFailureWarn,
			MaxTasksPerPod:             50,
			MaxTaskRetries:             &retries,
		},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var created admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	if created.TaskPolicy == nil || created.TaskPolicy.MaxTasksPerPod != 50 ||
		created.TaskPolicy.MaxTaskRetries == nil || *created.TaskPolicy.MaxTaskRetries != 2 {
		t.Errorf("create response taskPolicy: %+v", created.TaskPolicy)
	}

	replacement := &runtimestore.TaskPolicy{AcknowledgeBestEffortScrub: true, MaxTasksPerPod: 99}
	rr = runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/rt",
		admin.UpdateRuntimeRequest{TaskPolicy: replacement})
	if rr.Code != http.StatusOK {
		t.Fatalf("update: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var updated admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &updated)
	if updated.TaskPolicy == nil || updated.TaskPolicy.MaxTasksPerPod != 99 {
		t.Errorf("update did not replace taskPolicy: %+v", updated.TaskPolicy)
	}
}

func TestCreateRuntimeRejectsInvalidTaskPolicy(t *testing.T) {
	// §5.1: taskPolicy enums and numeric fields are validated.
	router, _, _ := newRuntimeAdmin(t)
	cases := []*runtimestore.TaskPolicy{
		{MicrovmScrubMode: "wipe"},
		{OnCleanupFailure: "retry"},
		{MaxTasksPerPod: -1},
	}
	for i, p := range cases {
		rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
			Name:       "bad",
			Image:      "lenny/bad@sha256:abc",
			TaskPolicy: p,
		})
		if rr.Code != http.StatusBadRequest {
			t.Errorf("case %d: status %d, want 400 (body=%s)", i, rr.Code, rr.Body.String())
		}
	}
}

// createBaseRuntime registers a standalone runtime usable as a §5.1
// derived-runtime base.
func createBaseRuntime(t *testing.T, h http.Handler, name string) {
	t.Helper()
	rr := runtimeRequest(t, h, http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:  name,
		Image: "lenny/" + name + "@sha256:abc",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create base %q: status %d, body=%s", name, rr.Code, rr.Body.String())
	}
}

func TestRuntimeBaseRuntimeRoundTrip(t *testing.T) {
	// §5.1: the admin runtime API round-trips the baseRuntime reference.
	// A derived runtime omits the inherited fields and references an
	// existing standalone base.
	router, _, _ := newRuntimeAdmin(t)
	createBaseRuntime(t, router.Handler(), "langgraph-runtime")

	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:        "research-pipeline",
		BaseRuntime: "langgraph-runtime",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var created admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	if created.BaseRuntime != "langgraph-runtime" {
		t.Errorf("create response baseRuntime: %q", created.BaseRuntime)
	}

	rr = runtimeRequest(t, router.Handler(), http.MethodGet, "/v1/admin/runtimes/research-pipeline", nil)
	var got admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got.BaseRuntime != "langgraph-runtime" {
		t.Errorf("get response baseRuntime: %q", got.BaseRuntime)
	}
}

func TestCreateRuntimeRejectsInvalidDerivedRuntime(t *testing.T) {
	// §5.1: a derived runtime may not set the inherited / prohibited
	// fields, and its baseRuntime must reference an existing standalone
	// runtime.
	router, _, _ := newRuntimeAdmin(t)
	createBaseRuntime(t, router.Handler(), "base-rt")

	cases := []struct {
		name    string
		payload admin.RuntimePayload
	}{
		{"image set", admin.RuntimePayload{Name: "d1", BaseRuntime: "base-rt", Image: "lenny/x@sha256:abc"}},
		{"type set", admin.RuntimePayload{Name: "d2", BaseRuntime: "base-rt", Type: "agent"}},
		{"isolationProfile set", admin.RuntimePayload{Name: "d3", BaseRuntime: "base-rt", IsolationProfile: "standard"}},
		{"integrationLevel set", admin.RuntimePayload{Name: "d4", BaseRuntime: "base-rt", IntegrationLevel: "full"}},
		{"capabilities set", admin.RuntimePayload{
			Name: "d5", BaseRuntime: "base-rt",
			Capabilities: &runtimestore.RuntimeCapabilities{Interaction: runtimestore.InteractionOneShot},
		}},
		{"missing base", admin.RuntimePayload{Name: "d6", BaseRuntime: "no-such-runtime"}},
	}
	for _, tc := range cases {
		rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", tc.payload)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400 (body=%s)", tc.name, rr.Code, rr.Body.String())
			continue
		}
		if !strings.Contains(rr.Body.String(), "INVALID_DERIVED_RUNTIME") {
			t.Errorf("%s: body %s, want INVALID_DERIVED_RUNTIME", tc.name, rr.Body.String())
		}
	}
}

func TestCreateRuntimeRejectsChainedDerivation(t *testing.T) {
	// §5.1: the merge algorithm is single-level — a derived runtime's
	// base must itself be standalone.
	router, _, _ := newRuntimeAdmin(t)
	createBaseRuntime(t, router.Handler(), "root-rt")
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name: "mid-rt", BaseRuntime: "root-rt",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create mid runtime: status %d, body=%s", rr.Code, rr.Body.String())
	}
	chained := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name: "leaf-rt", BaseRuntime: "mid-rt",
	})
	if chained.Code != http.StatusBadRequest {
		t.Errorf("chained derivation: status %d, want 400 (body=%s)", chained.Code, chained.Body.String())
	}
}

func TestUpdateRuntimeRejectsInvalidDerivedRuntime(t *testing.T) {
	// §5.1: baseRuntime is fixed at registration, and a PUT against a
	// derived runtime may not set the inherited / prohibited fields.
	router, _, _ := newRuntimeAdmin(t)
	createBaseRuntime(t, router.Handler(), "base-rt")
	createBaseRuntime(t, router.Handler(), "other-base")
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name: "derived-rt", BaseRuntime: "base-rt",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create derived: status %d, body=%s", rr.Code, rr.Body.String())
	}

	rebased := "other-base"
	rr = runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/derived-rt",
		admin.UpdateRuntimeRequest{BaseRuntime: &rebased})
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "INVALID_DERIVED_RUNTIME") {
		t.Errorf("PUT changing baseRuntime: status %d body %s", rr.Code, rr.Body.String())
	}

	img := "lenny/x@sha256:abc"
	rr = runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/derived-rt",
		admin.UpdateRuntimeRequest{Image: &img})
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "INVALID_DERIVED_RUNTIME") {
		t.Errorf("PUT image on a derived runtime: status %d body %s", rr.Code, rr.Body.String())
	}

	rr = runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/derived-rt",
		admin.UpdateRuntimeRequest{Capabilities: &runtimestore.RuntimeCapabilities{Interaction: runtimestore.InteractionOneShot}})
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "INVALID_DERIVED_RUNTIME") {
		t.Errorf("PUT capabilities on a derived runtime: status %d body %s", rr.Code, rr.Body.String())
	}

	// A PUT cannot convert a standalone runtime into a derived one.
	rr = runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/base-rt",
		admin.UpdateRuntimeRequest{BaseRuntime: &rebased})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("PUT baseRuntime onto a standalone runtime: status %d, want 400", rr.Code)
	}

	// A benign field PUT against a derived runtime is accepted.
	desc := "updated description"
	rr = runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/derived-rt",
		admin.UpdateRuntimeRequest{Description: &desc})
	if rr.Code != http.StatusOK {
		t.Errorf("PUT description on a derived runtime: status %d, want 200 (body=%s)", rr.Code, rr.Body.String())
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

// TestRuntimeMutationAuthorization covers the runtime mutation
// endpoints against the §10.2 "Manage runtimes" matrix row. Runtime
// create and delete are platform-admin-only per §15.1 (they define or
// remove a platform-global record): a tenant-admin receives 403.
// Runtime update is granted to tenant-admin for "own tenant", which
// §15.1 scopes to access-table entries — a tenant-admin with no
// runtime_tenant_access grant for the target runtime receives 404 (the
// gate reports the out-of-scope resource as absent, matching the
// read-path scoping in handleGetRuntime). (Reconciliation note: the PUT
// previously used requireAdmin, which denied the tenant-admin
// entitlement the §10.2 matrix grants.)
func TestRuntimeMutationAuthorization(t *testing.T) {
	router, store, _ := newRuntimeAdmin(t)
	_ = store.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	for _, c := range []struct {
		method, path string
		body         []byte
		want         int
	}{
		// Create / delete: platform-admin only; tenant-admin is forbidden.
		{http.MethodPost, "/v1/admin/runtimes", []byte(`{"name":"x"}`), http.StatusForbidden},
		{http.MethodDelete, "/v1/admin/runtimes/echo", nil, http.StatusForbidden},
		// Update: granted to tenant-admin, scoped to the access table —
		// an ungranted runtime reads as absent (404), not forbidden.
		{http.MethodPut, "/v1/admin/runtimes/echo", []byte("{}"), http.StatusNotFound},
	} {
		req := withTenantAdminPrincipal(httptest.NewRequest(c.method, c.path, bytes.NewReader(c.body)))
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, req)
		if rr.Code != c.want {
			t.Errorf("%s %s as tenant-admin: got %d, want %d", c.method, c.path, rr.Code, c.want)
		}
	}
}

// spec: §5.1 — allowedResourceClasses is Prohibited on derived runtimes;
// supportedProviders and allowSelfRecursion are restrict-only on derived.
func TestCreateDerivedRuntimeRestrictsClassesProvidersRecursion(t *testing.T) {
	router, _, _ := newRuntimeAdmin(t)
	// A standalone base declaring providers and self-recursion.
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:               "base-rt",
		Image:              "lenny/base-rt@sha256:abc",
		SupportedProviders: []string{"anthropic_direct", "aws_bedrock"},
		AllowSelfRecursion: true,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create base: status %d body %s", rr.Code, rr.Body.String())
	}

	cases := []struct {
		name    string
		payload admin.RuntimePayload
	}{
		{"allowedResourceClasses prohibited", admin.RuntimePayload{
			Name: "d-classes", BaseRuntime: "base-rt", AllowedResourceClasses: []string{"small"}}},
		{"supportedProviders expand", admin.RuntimePayload{
			Name: "d-prov", BaseRuntime: "base-rt", SupportedProviders: []string{"anthropic_direct", "vault_transit"}}},
	}
	for _, tc := range cases {
		rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", tc.payload)
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "INVALID_DERIVED_RUNTIME") {
			t.Errorf("%s: status %d body %s, want 400 INVALID_DERIVED_RUNTIME", tc.name, rr.Code, rr.Body.String())
		}
	}

	// A restricting derived runtime (subset providers, recursion off) is accepted.
	ok := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name: "d-ok", BaseRuntime: "base-rt",
		SupportedProviders: []string{"anthropic_direct"},
		AllowSelfRecursion: false,
	})
	if ok.Code != http.StatusCreated {
		t.Errorf("restricting derived runtime: status %d body %s, want 201", ok.Code, ok.Body.String())
	}
}

// spec: §5.1 — a derived runtime declaring allowSelfRecursion:true when
// its base is false is rejected (security boundary, restrict-only).
func TestCreateDerivedRuntimeRejectsRecursionWiden(t *testing.T) {
	router, _, _ := newRuntimeAdmin(t)
	createBaseRuntime(t, router.Handler(), "base-noselfrec") // AllowSelfRecursion defaults false
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name: "d-widen", BaseRuntime: "base-noselfrec", AllowSelfRecursion: true,
	})
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "INVALID_DERIVED_RUNTIME") {
		t.Errorf("widening allowSelfRecursion: status %d body %s, want 400 INVALID_DERIVED_RUNTIME", rr.Code, rr.Body.String())
	}
}

// spec: §5.1 — the admin API round-trips the three new registry fields
// on a standalone runtime.
func TestStandaloneRuntimeRoundTripsNewFields(t *testing.T) {
	router, _, _ := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:                   "full-rt",
		Image:                  "lenny/full-rt@sha256:abc",
		AllowedResourceClasses: []string{"small", "medium", "large"},
		SupportedProviders:     []string{"anthropic_direct"},
		AllowSelfRecursion:     true,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d body %s", rr.Code, rr.Body.String())
	}
	var got admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got.AllowedResourceClasses) != 3 || len(got.SupportedProviders) != 1 || !got.AllowSelfRecursion {
		t.Errorf("round-trip lost new fields: %+v", got)
	}
}

// spec: §5.1 credentialCapabilities (lines 73-75) — the admin API
// round-trips the hotRotation flag and proxyDialect set on a standalone
// runtime.
func TestRuntimeCredentialCapabilitiesRoundTrip(t *testing.T) {
	router, store, _ := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:  "cc-rt",
		Image: "lenny/cc-rt@sha256:abc",
		CredentialCapabilities: &runtimestore.CredentialCapabilities{
			HotRotation:  true,
			ProxyDialect: []string{"openai", "anthropic"},
		},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d body %s", rr.Code, rr.Body.String())
	}
	var got admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got.CredentialCapabilities == nil || !got.CredentialCapabilities.HotRotation ||
		len(got.CredentialCapabilities.ProxyDialect) != 2 {
		t.Errorf("round-trip lost credentialCapabilities: %+v", got.CredentialCapabilities)
	}
	stored, err := store.Get(context.Background(), "cc-rt")
	if err != nil {
		t.Fatalf("store missing runtime: %v", err)
	}
	if !stored.CredentialCapabilities.AllowsProxyDialect("anthropic") {
		t.Errorf("stored credentialCapabilities lost proxyDialect: %+v", stored.CredentialCapabilities)
	}
}

// spec: §5.1 lines 76-101 / merge table — limits, setupCommandPolicy, and
// defaultPoolConfig are modeled, persisted, and round-trip through the
// admin create/get path.
func TestCreateRuntimeModelsLimitsSetupCommandPolicyAndPoolConfig(t *testing.T) {
	router, store, _ := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:  "langgraph",
		Type:  "agent",
		Image: "ghcr.io/acme/langgraph@sha256:abcdef",
		Limits: &runtimestore.Limits{
			MaxSessionAgeSeconds: 7200, MaxUploadSizeBytes: 524288000, MaxRequestInputWaitSeconds: 600,
		},
		SetupCommandPolicy: &runtimestore.SetupCommandPolicy{
			Mode: runtimestore.SetupCommandModeAllowlist, Allowlist: []string{"npm ci", "make"}, MaxCommands: 10,
		},
		DefaultPoolConfig: &runtimestore.DefaultPoolConfig{WarmCount: 5, ResourceClass: "medium", EgressProfile: "restricted"},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	row, err := store.Get(context.Background(), "langgraph")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if row.Limits == nil || row.Limits.MaxRequestInputWaitSeconds != 600 || row.Limits.MaxUploadSizeBytes != 524288000 {
		t.Errorf("stored limits = %+v", row.Limits)
	}
	if row.SetupCommandPolicy == nil || row.SetupCommandPolicy.Mode != runtimestore.SetupCommandModeAllowlist ||
		len(row.SetupCommandPolicy.Allowlist) != 2 {
		t.Errorf("stored setupCommandPolicy = %+v", row.SetupCommandPolicy)
	}
	if row.DefaultPoolConfig == nil || row.DefaultPoolConfig.WarmCount != 5 || row.DefaultPoolConfig.ResourceClass != "medium" {
		t.Errorf("stored defaultPoolConfig = %+v", row.DefaultPoolConfig)
	}
}

// spec: §5.1 — invalid limits / setupCommandPolicy values are rejected at
// registration.
func TestCreateRuntimeRejectsInvalidNewBlocks(t *testing.T) {
	cases := []struct {
		name    string
		payload admin.RuntimePayload
	}{
		{"negative-limit", admin.RuntimePayload{
			Name: "r1", Image: "x@sha256:a", Limits: &runtimestore.Limits{MaxSessionAgeSeconds: -1},
		}},
		{"bad-setup-mode", admin.RuntimePayload{
			Name: "r2", Image: "x@sha256:a", SetupCommandPolicy: &runtimestore.SetupCommandPolicy{Mode: "bogus"},
		}},
		{"negative-warm", admin.RuntimePayload{
			Name: "r3", Image: "x@sha256:a", DefaultPoolConfig: &runtimestore.DefaultPoolConfig{WarmCount: -2},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router, _, _ := newRuntimeAdmin(t)
			rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", tc.payload)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400; body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

// spec: §5.1 line 36 — integrationLevel is only valid on type:agent
// runtimes; a type:mcp payload that sets it is rejected with INVALID_RUNTIME.
func TestCreateRuntimeRejectsIntegrationLevelOnMCP(t *testing.T) {
	router, _, _ := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name: "mcp-tool", Type: "mcp", Image: "lenny/mcp-tool@sha256:abc", IntegrationLevel: "full",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("INVALID_RUNTIME")) {
		t.Errorf("error code: body=%s, want INVALID_RUNTIME", rr.Body.String())
	}
}

// spec: §5.1 line 36 — a PUT setting integrationLevel on an existing
// type:mcp runtime is rejected.
func TestUpdateRuntimeRejectsIntegrationLevelOnMCP(t *testing.T) {
	router, store, _ := newRuntimeAdmin(t)
	if err := store.Create(context.Background(), runtimestore.Runtime{
		Name: "mcp-live", Type: runtimestore.TypeMCP, Image: "lenny/mcp-live@sha256:abc",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	level := "standard"
	rr := runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/mcp-live",
		admin.UpdateRuntimeRequest{IntegrationLevel: &level})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

// spec: §5.1 line 36 — a type:agent runtime accepts integrationLevel.
func TestCreateRuntimeAcceptsIntegrationLevelOnAgent(t *testing.T) {
	router, _, _ := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name: "agent-rt", Type: "agent", Image: "lenny/agent@sha256:abc", IntegrationLevel: "full",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
}

// TestRuntimeWorkspaceTierRoundTrip_spec_12_9 verifies the §12.9 / §5.2
// runtime workspaceTier field round-trips through POST and GET and is
// updatable via PUT.
//
// spec: §5.2 line 396 / §12.9.
func TestRuntimeWorkspaceTierRoundTrip_spec_12_9(t *testing.T) {
	router, store, _ := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:          "phi-agent",
		Type:          "agent",
		Image:         "ghcr.io/acme/phi-agent@sha256:abcdef",
		WorkspaceTier: "T4",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var resp admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.WorkspaceTier != "T4" {
		t.Errorf("response workspaceTier = %q, want T4", resp.WorkspaceTier)
	}
	row, err := store.Get(context.Background(), "phi-agent")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if row.WorkspaceTier != runtimestore.WorkspaceTierT4 {
		t.Errorf("stored workspaceTier = %q, want T4", row.WorkspaceTier)
	}

	// PUT back down to T3.
	t3 := "T3"
	rr = runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/phi-agent",
		admin.UpdateRuntimeRequest{WorkspaceTier: &t3})
	if rr.Code != http.StatusOK {
		t.Fatalf("update status: got %d; body=%s", rr.Code, rr.Body.String())
	}
	row, _ = store.Get(context.Background(), "phi-agent")
	if row.WorkspaceTier != runtimestore.WorkspaceTierT3 {
		t.Errorf("after PUT workspaceTier = %q, want T3", row.WorkspaceTier)
	}
}

// TestCreateRuntimeRejectsUnknownWorkspaceTier_spec_12_9 verifies a
// non-empty, unrecognised workspaceTier fails closed at admission.
//
// spec: §12.9.
func TestCreateRuntimeRejectsUnknownWorkspaceTier_spec_12_9(t *testing.T) {
	router, _, _ := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:          "bad-tier",
		Image:         "ghcr.io/acme/x@sha256:abcdef",
		WorkspaceTier: "T9",
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("unknown workspaceTier: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

// TestCreateRuntimeRejectsWorkspaceTierOnDerived_spec_5_1 verifies
// workspaceTier is Inherited from the base runtime, so a derived runtime
// may not declare it.
//
// spec: §5.1 merge table / §5.2 line 396.
func TestCreateRuntimeRejectsWorkspaceTierOnDerived_spec_5_1(t *testing.T) {
	router, store, _ := newRuntimeAdmin(t)
	if err := store.Create(context.Background(), runtimestore.Runtime{
		Name: "base", Type: runtimestore.TypeAgent, Image: "ghcr.io/acme/base@sha256:abc",
		WorkspaceTier: runtimestore.WorkspaceTierT4,
	}); err != nil {
		t.Fatalf("seed base: %v", err)
	}
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:          "derived",
		BaseRuntime:   "base",
		WorkspaceTier: "T4",
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("workspaceTier on derived: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

// spec: §6.2 line 260 — `maxFinalizingTimeoutSeconds ≥ setupTimeoutSeconds`;
// §11.3 line 219 — the gateway-side outer bound.
func TestCreateRuntimeRejectsSetupTimeoutAboveFinalizingCap_spec_6_2(t *testing.T) {
	router, _, _ := newRuntimeAdmin(t)
	router = router.WithMaxFinalizingTimeoutSeconds(600)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:             "echo",
		Type:             "agent",
		Image:            "lenny/echo@sha256:abc",
		ExecutionMode:    "session",
		IsolationProfile: "sandboxed",
		IntegrationLevel: "full",
		SetupPolicy:      &runtimestore.SetupPolicy{TimeoutSeconds: 900},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("setupTimeout 900 with cap 600: got %d, want 400; body=%s",
			rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "maxFinalizingTimeoutSeconds") {
		t.Errorf("error body should cite the gateway cap: %s", rr.Body.String())
	}
}

// spec: §6.2 line 260 — the inner bound is enforced inclusively at the
// boundary; setupTimeoutSeconds == maxFinalizingTimeoutSeconds is accepted.
func TestCreateRuntimeAcceptsSetupTimeoutAtFinalizingCap_spec_6_2(t *testing.T) {
	router, _, _ := newRuntimeAdmin(t)
	router = router.WithMaxFinalizingTimeoutSeconds(600)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:             "echo",
		Type:             "agent",
		Image:            "lenny/echo@sha256:abc",
		ExecutionMode:    "session",
		IsolationProfile: "sandboxed",
		IntegrationLevel: "full",
		SetupPolicy:      &runtimestore.SetupPolicy{TimeoutSeconds: 600},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("setupTimeout 600 == cap 600: got %d, want 201; body=%s",
			rr.Code, rr.Body.String())
	}
}

// spec: §6.2 line 260 — the cap applies only when the runtime declares a
// finite aggregate cap; zero (no-cap) remains acceptable because the
// per-command timeout still bounds work.
func TestCreateRuntimeAcceptsZeroSetupTimeoutUnderCap_spec_6_2(t *testing.T) {
	router, _, _ := newRuntimeAdmin(t)
	router = router.WithMaxFinalizingTimeoutSeconds(600)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:             "echo",
		Type:             "agent",
		Image:            "lenny/echo@sha256:abc",
		ExecutionMode:    "session",
		IsolationProfile: "sandboxed",
		IntegrationLevel: "full",
		SetupPolicy:      &runtimestore.SetupPolicy{TimeoutSeconds: 0},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("zero setupTimeout (no cap): got %d, want 201; body=%s",
			rr.Code, rr.Body.String())
	}
}

// spec: §6.2 line 260 — the bound applies on PUT updates too, since a PUT
// can shrink the gateway cap or grow the runtime setupPolicy.
func TestUpdateRuntimeRejectsSetupTimeoutAboveFinalizingCap_spec_6_2(t *testing.T) {
	router, store, _ := newRuntimeAdmin(t)
	router = router.WithMaxFinalizingTimeoutSeconds(600)
	if err := store.Create(context.Background(), runtimestore.Runtime{
		Name:             "echo",
		Image:            "lenny/echo@sha256:abc",
		Type:             runtimestore.TypeAgent,
		ExecutionMode:    runtimestore.ExecutionModeSession,
		IsolationProfile: isolation.ProfileSandboxed,
		IntegrationLevel: runtimestore.IntegrationLevelFull,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rr := runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/echo",
		admin.UpdateRuntimeRequest{
			SetupPolicy: &runtimestore.SetupPolicy{TimeoutSeconds: 1200},
		})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("PUT setupTimeout 1200 with cap 600: got %d, want 400; body=%s",
			rr.Code, rr.Body.String())
	}
}

// spec: §6.2 line 260 — when no gateway cap is wired (Router constructed
// without WithMaxFinalizingTimeoutSeconds), the check no-ops so test
// scaffolding does not need the wiring.
func TestCreateRuntimeAcceptsArbitrarySetupTimeoutWithoutCap_spec_6_2(t *testing.T) {
	router, _, _ := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:             "echo",
		Type:             "agent",
		Image:            "lenny/echo@sha256:abc",
		ExecutionMode:    "session",
		IsolationProfile: "sandboxed",
		IntegrationLevel: "full",
		SetupPolicy:      &runtimestore.SetupPolicy{TimeoutSeconds: 36000},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("setupTimeout 36000 without cap: got %d, want 201; body=%s",
			rr.Code, rr.Body.String())
	}
}
