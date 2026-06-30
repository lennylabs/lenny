// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// newCredentialPoolAdminWithRuntimes builds a pool admin router wired
// with both a credential-pool store and a runtime registry seeded with
// the given runtimes, so the §4.9 line 1476 proxy-dialect admission
// boundary has runtimes to resolve against.
func newCredentialPoolAdminWithRuntimes(t *testing.T, rts ...runtimestore.Runtime) (*admin.Router, *credentialpoolstore.Memory) {
	t.Helper()
	runtimes := runtimestore.NewMemory()
	for _, rt := range rts {
		if err := runtimes.Create(context.Background(), rt); err != nil {
			t.Fatalf("seed runtime %q: %v", rt.Name, err)
		}
	}
	store := credentialpoolstore.NewMemory()
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithCredentialPools(store).WithRuntimes(runtimes)
	return router, store
}

func anthropicRuntime(name string, dialects ...string) runtimestore.Runtime {
	return runtimestore.Runtime{
		Name:                   name,
		Type:                   runtimestore.TypeAgent,
		SupportedProviders:     []string{"anthropic_direct"},
		CredentialCapabilities: &runtimestore.CredentialCapabilities{ProxyDialect: dialects},
	}
}

func adminErrorCode(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	return env.Error.Code
}

// spec: §4.9 line 1476 — a proxy-mode pool whose proxyDialect no agent
// runtime serving its provider declares is rejected at registration with
// 422 INVALID_POOL_PROXY_DIALECT.
func TestCreateCredentialPoolRejectsUnspeakableProxyDialect_spec_4_9_1476(t *testing.T) {
	router, _ := newCredentialPoolAdminWithRuntimes(t, anthropicRuntime("claude-code", "anthropic"))
	body := validCredentialPool("acme", "claude-proxy")
	body.DeliveryMode = "proxy"
	body.ProxyDialect = "openai" // no anthropic_direct runtime declares openai
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/credential-pools", body, withAdminPrincipal)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	if code := adminErrorCode(t, rr.Body.Bytes()); code != "INVALID_POOL_PROXY_DIALECT" {
		t.Errorf("code = %q, want INVALID_POOL_PROXY_DIALECT", code)
	}
}

// spec: §4.9 line 1476 — a proxy-mode pool whose proxyDialect at least one
// agent runtime serving its provider declares is accepted.
func TestCreateCredentialPoolAcceptsDeclaredProxyDialect_spec_4_9_1476(t *testing.T) {
	router, _ := newCredentialPoolAdminWithRuntimes(t, anthropicRuntime("claude-code", "anthropic", "openai"))
	body := validCredentialPool("acme", "claude-proxy")
	body.DeliveryMode = "proxy"
	body.ProxyDialect = "openai"
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/credential-pools", body, withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", rr.Code, rr.Body.String())
	}
}

// spec: §4.9 line 1476 — when no agent runtime references the pool's
// provider, the admission check defers to the session-creation join and
// the pool is accepted.
func TestCreateCredentialPoolDefersWhenNoRuntimeSupportsProvider_spec_4_9_1476(t *testing.T) {
	// Runtime supports a different provider, so anthropic_direct is
	// unreferenced; the proxy-dialect boundary has nothing to enforce here.
	other := runtimestore.Runtime{
		Name:                   "vertex-agent",
		Type:                   runtimestore.TypeAgent,
		SupportedProviders:     []string{"vertex_ai"},
		CredentialCapabilities: &runtimestore.CredentialCapabilities{ProxyDialect: []string{"openai"}},
	}
	router, _ := newCredentialPoolAdminWithRuntimes(t, other)
	body := validCredentialPool("acme", "claude-proxy")
	body.DeliveryMode = "proxy"
	body.ProxyDialect = "openai"
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/credential-pools", body, withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", rr.Code, rr.Body.String())
	}
}

// spec: §4.9 line 1476 — a direct-mode pool declares no proxyDialect, so
// the dialect boundary does not apply at registration.
func TestCreateCredentialPoolDirectModeSkipsProxyDialect_spec_4_9_1476(t *testing.T) {
	router, _ := newCredentialPoolAdminWithRuntimes(t, anthropicRuntime("claude-code", "anthropic"))
	body := validCredentialPool("acme", "claude-direct")
	body.DeliveryMode = "direct"
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/credential-pools", body, withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", rr.Code, rr.Body.String())
	}
}

// spec: §4.9 line 1476 — a PUT that changes a pool's proxyDialect to a
// value no agent runtime serving its provider declares is rejected.
func TestUpdateCredentialPoolRejectsUnspeakableProxyDialect_spec_4_9_1476(t *testing.T) {
	router, _ := newCredentialPoolAdminWithRuntimes(t, anthropicRuntime("claude-code", "anthropic"))
	// Seed a valid proxy pool (anthropic dialect, which the runtime speaks).
	create := validCredentialPool("acme", "claude-proxy")
	create.DeliveryMode = "proxy"
	create.ProxyDialect = "anthropic"
	if rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/credential-pools", create, withAdminPrincipal); rr.Code != http.StatusCreated {
		t.Fatalf("seed pool: status %d, body %s", rr.Code, rr.Body.String())
	}
	// PUT flips the dialect to openai, which no anthropic_direct runtime declares.
	update := create
	update.ProxyDialect = "openai"
	rr := doAdminReq(t, router.Handler(), http.MethodPut, "/v1/admin/credential-pools/claude-proxy", update, withAdminPrincipal)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	if code := adminErrorCode(t, rr.Body.Bytes()); code != "INVALID_POOL_PROXY_DIALECT" {
		t.Errorf("code = %q, want INVALID_POOL_PROXY_DIALECT", code)
	}
}
