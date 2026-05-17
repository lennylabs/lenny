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
	"github.com/lennylabs/lenny/pkg/gateway/agentcard"
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
