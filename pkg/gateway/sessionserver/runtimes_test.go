// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/environment"
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
