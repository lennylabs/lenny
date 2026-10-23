// SPDX-License-Identifier: MIT

//go:build chaos

package tier8_chaos_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/leaserevoke"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/tests/testinfra/stubs/llmprovider"
)

// orderObservingAssigner is the handling replica's credential assigner as
// the production lease step sees it. It records, for each Release, whether
// the session's row was already terminal, so the test asserts that every
// lease record is removed before the cancel write.
type orderObservingAssigner struct {
	svc      *credassign.Service
	sessions sessionstore.Store
	id       string
	releases int
	// afterCancel counts the Releases that ran after the session's cancel
	// write.
	afterCancel int
}

func (o *orderObservingAssigner) Release(leaseID string) error {
	o.releases++
	if row, err := o.sessions.Get(context.Background(), "acme", o.id); err == nil && session.IsTerminal(row.State) {
		o.afterCancel++
	}
	return o.svc.Release(leaseID)
}

// proxyCode sends one request through the proxy with the lease token and
// returns the status and the error code.
func proxyCode(t *testing.T, url, token string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url,
		strings.NewReader(`{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"ping"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxy request: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	return resp.StatusCode, env.Error.Code
}

// spec: 11.4 (User Invalidation: Full revoke propagation mechanism), 4.9 (Credential Leasing Service)
// diagnosis: a full_revoke handled on a replica that does not bind the
// session left the returned lease usable on the binding replica, whose deny
// list no revocation reaches: the lease step did not remove the record of a
// pool-backed or a user-backed lease before the cancel, so the peer's LLM
// proxy still served its token; or the step wrote a deny-list entry; or the
// session's later release returned a lease the revoke already returned.
func TestFullRevokeRejectsReturnedLeasesOnAPeerWithNoDenyListEntry_spec_11_4(t *testing.T) {
	ctx := context.Background()
	upstream := llmprovider.New(t)
	leases := credleasestore.New()
	cache := credcache.New()
	svc := credassign.New(leases, cache)
	svc.RegisterPool(credassign.Pool{
		Name: "claude-prod", Provider: credential.ProviderAnthropicDirect, DeliveryMode: credential.DeliveryProxy,
		Strategy: credential.StrategyLeastLoaded, ProxyURL: "https://gateway-internal:8443/llm-proxy", ProxyDialect: "anthropic",
		Credentials: []credassign.PoolCredential{{ID: "key-1", APIKey: "sk-ant-1", Healthy: true}},
	})
	const id = "s-revoked"
	poolL, err := svc.Assign("claude-prod", id, "", "acme", 1)
	if err != nil || poolL.Proxy == nil || poolL.Proxy.LeaseToken == "" {
		t.Fatalf("Assign = %+v, %v; want a proxy lease with a token", poolL, err)
	}
	userL := credential.Lease{
		LeaseID: "lease-user", SessionID: id, Provider: credential.ProviderAnthropicDirect,
		Source: credential.SourceUser, TenantID: "acme", CredentialRef: "cred-alice",
		DeliveryMode: credential.DeliveryProxy, IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
		Proxy: &credential.ProxyConfig{
			ProxyURL: "https://gateway-internal:8443/llm-proxy", ProxyDialect: "anthropic", LeaseToken: "lt-user",
		},
	}
	if err := leases.Put(userL); err != nil {
		t.Fatalf("seed the user-backed lease: %v", err)
	}
	// The binding replica's proxy reads the shared lease store and its own
	// deny list, which no revocation reaches.
	bindingDeny := denylist.New()
	bindingProxy := httptest.NewServer(&llmproxy.Handler{
		Leases: leases, Credentials: cache, DenyList: bindingDeny,
		Translators: llmproxy.NewTranslatorRegistry(&llmproxy.AnthropicDirectTranslator{
			BaseURL: upstream.URL(), DefaultAnthropicVersion: "2023-06-01",
		}),
		Forwarder: &llmproxy.Forwarder{Breaker: &llmproxy.CircuitBreaker{}},
	})
	t.Cleanup(bindingProxy.Close)
	url := bindingProxy.URL + "/v1/messages"
	if code, _ := proxyCode(t, url, poolL.Proxy.LeaseToken); code != http.StatusOK {
		t.Fatalf("pre-revoke proxy request = %d, want 200", code)
	}

	sessions := memstore.New()
	if err := sessions.Create(ctx, sessionstore.Session{ID: id, TenantID: "acme", UserID: "alice@acme.com", State: session.StateRunning}); err != nil {
		t.Fatal(err)
	}
	users := userstore.NewMemory()
	if err := users.Create(ctx, userstore.User{Subject: "alice@acme.com", TenantID: "acme", Roles: []pkgauth.Role{pkgauth.RoleUser}}); err != nil {
		t.Fatal(err)
	}
	assigner := &orderObservingAssigner{svc: svc, sessions: sessions, id: id}
	step := leaserevoke.New(leases, assigner, nil)
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{Clock: time.Now}).
		WithUsers(users).WithSessions(sessions).
		WithUserRevocation(nil, step, nil)
	body, _ := json.Marshal(admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateFullRevoke})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/users/alice@acme.com/invalidate", bytes.NewReader(body))
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject: "admin@acme.com", TenantID: "platform", Roles: []pkgauth.Role{pkgauth.RolePlatformAdmin},
	}))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("full_revoke = %d %s", rr.Code, rr.Body.String())
	}

	if assigner.releases != 2 || assigner.afterCancel != 0 {
		t.Errorf("lease step: %d Releases (%d after the cancel write); want two Releases before it",
			assigner.releases, assigner.afterCancel)
	}
	// The rejection below comes from the removed record alone: the binding
	// replica's deny list holds no entry for either credential.
	if bindingDeny.Len() != 0 {
		t.Fatalf("the binding replica's deny list holds %d entries, want none", bindingDeny.Len())
	}
	before := len(upstream.Requests())
	for _, l := range []credential.Lease{poolL, userL} {
		code, errCode := proxyCode(t, url, l.Proxy.LeaseToken)
		if code != http.StatusUnauthorized || errCode != "LEASE_TOKEN_INVALID" {
			t.Errorf("the binding replica's proxy answered %d %q for the returned %s lease's token, want 401 LEASE_TOKEN_INVALID", code, errCode, l.Source)
		}
	}
	if got := len(upstream.Requests()); got != before {
		t.Errorf("a returned token reached the provider (%d requests, want %d)", got, before)
	}
	if left := leases.LeasesBySession([]string{id}); len(left) != 0 {
		t.Fatalf("the session still holds %d leases, so its release would return a revoked lease", len(left))
	}
	if err := svc.ReleaseSession(id); err != nil {
		t.Errorf("the session's release after the revoke: %v", err)
	}
}
