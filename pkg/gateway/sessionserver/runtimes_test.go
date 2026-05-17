// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/environment"
	"github.com/lennylabs/lenny/pkg/gateway/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §9.1 GET /v1/runtimes runtime discovery.

type runtimeDiscoveryResponse struct {
	Runtimes []sessionserver.RuntimeDiscoveryEntry `json:"runtimes"`
}

func TestListRuntimesUnfiltered(t *testing.T) {
	runtimes := runtimestore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "claude-agent", Type: runtimestore.TypeAgent,
	})
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "gemini-agent", Type: runtimestore.TypeAgent,
	})
	srv := sessionserver.New(memstore.New(), sessionserver.Options{Runtimes: runtimes})

	req := httptest.NewRequest(http.MethodGet, "/v1/runtimes", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp runtimeDiscoveryResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	// Without an environment registry the discovery list is unfiltered.
	if len(resp.Runtimes) != 2 {
		t.Errorf("unfiltered discovery: got %d runtimes, want 2", len(resp.Runtimes))
	}
}

func TestListRuntimesEmptyWhenUnwired(t *testing.T) {
	srv := sessionserver.New(memstore.New(), sessionserver.Options{})
	req := httptest.NewRequest(http.MethodGet, "/v1/runtimes", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp runtimeDiscoveryResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Runtimes) != 0 {
		t.Errorf("discovery without a runtime registry must be empty: %+v", resp.Runtimes)
	}
}

func TestListModelsOpenAIFormat(t *testing.T) {
	runtimes := runtimestore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "claude-agent", Type: runtimestore.TypeAgent,
	})
	srv := sessionserver.New(memstore.New(), sessionserver.Options{Runtimes: runtimes})

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Object string                      `json:"object"`
		Data   []sessionserver.OpenAIModel `json:"data"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Object != "list" {
		t.Errorf("object: got %q, want list", resp.Object)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("models: got %d, want 1 (%+v)", len(resp.Data), resp.Data)
	}
	m := resp.Data[0]
	if m.ID != "claude-agent" || m.Object != "model" || m.OwnedBy != "lenny" {
		t.Errorf("model entry: %+v", m)
	}
}

func TestListModelsEmptyWhenUnwired(t *testing.T) {
	srv := sessionserver.New(memstore.New(), sessionserver.Options{})
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp struct {
		Data []sessionserver.OpenAIModel `json:"data"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Data) != 0 {
		t.Errorf("models without a runtime registry must be empty: %+v", resp.Data)
	}
}

func TestDiscoveryEmbedsAdapterCapabilities(t *testing.T) {
	runtimes := runtimestore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "claude-agent", Type: runtimestore.TypeAgent,
	})
	srv := sessionserver.New(memstore.New(), sessionserver.Options{Runtimes: runtimes})

	// §9.1: every discovery response embeds a top-level adapterCapabilities
	// block describing the adapter serving the request.
	for _, path := range []string{"/v1/runtimes", "/v1/models"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s status: %d", path, rr.Code)
		}
		var resp struct {
			AdapterCapabilities adapter.Capabilities `json:"adapterCapabilities"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("%s decode: %v", path, err)
		}
		caps := resp.AdapterCapabilities
		if caps.PathPrefix != "/v1" || caps.Protocol != "rest" {
			t.Errorf("%s: adapterCapabilities routing fields: %+v", path, caps)
		}
		// The REST adapter persists sessions and serves the resume,
		// interrupt, and elicitation-respond endpoints.
		if !caps.SupportsElicitation || !caps.SupportsInterrupt || !caps.SupportsSessionContinuity {
			t.Errorf("%s: REST adapter must report elicitation, interrupt, continuity: %+v", path, caps)
		}
		if caps.SupportsDelegation {
			t.Errorf("%s: REST adapter has no delegate route, SupportsDelegation must be false", path)
		}
	}
}

func TestDiscoveryAdapterCapabilitiesPresentWhenUnwired(t *testing.T) {
	srv := sessionserver.New(memstore.New(), sessionserver.Options{})
	req := httptest.NewRequest(http.MethodGet, "/v1/runtimes", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp struct {
		AdapterCapabilities adapter.Capabilities `json:"adapterCapabilities"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	// §9.1: the block is mandatory on every discovery response, including
	// one returned with no runtime registry wired.
	if resp.AdapterCapabilities.Protocol != "rest" {
		t.Errorf("adapterCapabilities must be present even when unwired: %+v", resp.AdapterCapabilities)
	}
}

func TestListRuntimesSurfacesAgentInterface(t *testing.T) {
	runtimes := runtimestore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "refactorer", Type: runtimestore.TypeAgent,
		AgentInterface: &runtimestore.AgentInterface{
			Description: "Analyzes codebases",
			Skills:      []runtimestore.AgentInterfaceSkill{{ID: "review"}},
		},
	})
	srv := sessionserver.New(memstore.New(), sessionserver.Options{Runtimes: runtimes})

	req := httptest.NewRequest(http.MethodGet, "/v1/runtimes", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp runtimeDiscoveryResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Runtimes) != 1 {
		t.Fatalf("runtimes: got %d, want 1", len(resp.Runtimes))
	}
	// §9.1: GET /v1/runtimes surfaces the per-runtime agentInterface.
	ai := resp.Runtimes[0].AgentInterface
	if ai == nil || ai.Description != "Analyzes codebases" || len(ai.Skills) != 1 {
		t.Errorf("discovery must surface the agentInterface descriptor: %+v", ai)
	}
}

func TestListRuntimesSurfacesPublicMetadataRefs(t *testing.T) {
	runtimes := runtimestore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "carded", Type: runtimestore.TypeAgent,
		PublishedMetadata: []runtimestore.PublishedMetadataEntry{
			{Key: "agent-card", ContentType: "application/json", Visibility: runtimestore.VisibilityPublic, Content: `{"big":"payload"}`},
			{Key: "secret-spec", ContentType: "application/yaml", Visibility: runtimestore.VisibilityInternal, Content: "internalonly"},
		},
	})
	srv := sessionserver.New(memstore.New(), sessionserver.Options{Runtimes: runtimes})

	req := httptest.NewRequest(http.MethodGet, "/v1/runtimes", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp runtimeDiscoveryResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Runtimes) != 1 {
		t.Fatalf("runtimes: got %d, want 1", len(resp.Runtimes))
	}
	// §15: discovery carries only the public refs, and a ref never
	// carries entry content.
	refs := resp.Runtimes[0].PublishedMetadata
	if len(refs) != 1 || refs[0].Key != "agent-card" || refs[0].Visibility != runtimestore.VisibilityPublic {
		t.Errorf("discovery must surface only the public metadata ref: %+v", refs)
	}
	if strings.Contains(rr.Body.String(), "payload") || strings.Contains(rr.Body.String(), "internalonly") {
		t.Errorf("discovery refs must not carry entry content: %s", rr.Body.String())
	}
}

func TestRuntimeMetaServesPublicEntry(t *testing.T) {
	runtimes := runtimestore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "carded", Type: runtimestore.TypeAgent,
		PublishedMetadata: []runtimestore.PublishedMetadataEntry{
			{Key: "agent-card", ContentType: "application/json", Visibility: runtimestore.VisibilityPublic, Content: `{"name":"carded"}`},
		},
	})
	srv := sessionserver.New(memstore.New(), sessionserver.Options{Runtimes: runtimes})

	// §5.1: a public entry is served opaquely under its content type.
	req := httptest.NewRequest(http.MethodGet, "/v1/runtimes/carded/meta/agent-card", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("public meta: status %d, body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != `{"name":"carded"}` {
		t.Errorf("public meta body: %q", rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("public meta content-type: %q", ct)
	}

	// A soft-deleted runtime no longer serves its metadata.
	_ = runtimes.SoftDelete(context.Background(), "carded", time.Now())
	req = httptest.NewRequest(http.MethodGet, "/v1/runtimes/carded/meta/agent-card", nil)
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("soft-deleted runtime meta: status %d, want 404", rr.Code)
	}
}

func TestRuntimeMetaHidesNonPublicAndMissing(t *testing.T) {
	runtimes := runtimestore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "carded", Type: runtimestore.TypeAgent,
		PublishedMetadata: []runtimestore.PublishedMetadataEntry{
			{Key: "secret-spec", Visibility: runtimestore.VisibilityInternal, Content: "internal: true"},
		},
	})
	srv := sessionserver.New(memstore.New(), sessionserver.Options{Runtimes: runtimes})

	// §5.1: a non-public entry, a missing key, and a missing runtime
	// all return an identical 404, so the endpoint does not enumerate.
	for _, path := range []string{
		"/v1/runtimes/carded/meta/secret-spec",
		"/v1/runtimes/carded/meta/no-such-key",
		"/v1/runtimes/no-such-runtime/meta/agent-card",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, rr.Code)
		}
	}
}

func TestListRuntimesEnvironmentFiltered(t *testing.T) {
	runtimes := runtimestore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "sec-agent", Type: runtimestore.TypeAgent,
		Labels: map[string]string{"team": "security"},
	})
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "research-agent", Type: runtimestore.TypeAgent,
		Labels: map[string]string{"team": "research"},
	})
	envs := environmentstore.NewMemory()
	_ = envs.Create(context.Background(), environmentstore.Environment{
		Name: "security-team", TenantID: "acme",
		Members: []environmentstore.Member{{
			Identity: environmentstore.Identity{Type: "oidc-group", Value: "security-engineers"},
			Role:     environment.RoleCreator,
		}},
		RuntimeSelector: environment.Selector{MatchLabels: map[string]string{"team": "security"}},
	})
	tenants := tenantstore.NewMemory()
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"})
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		Runtimes: runtimes, Environments: envs, Tenants: tenants,
	})

	// §9.1: discovery is identity-filtered — a member of security-team
	// sees only the runtimes that environment's selector admits.
	req := httptest.NewRequest(http.MethodGet, "/v1/runtimes", nil)
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject: "alice", TenantID: "acme", Groups: []string{"security-engineers"},
	}))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp runtimeDiscoveryResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Runtimes) != 1 || resp.Runtimes[0].Name != "sec-agent" {
		t.Errorf("environment-filtered discovery: got %+v, want only sec-agent", resp.Runtimes)
	}
}
