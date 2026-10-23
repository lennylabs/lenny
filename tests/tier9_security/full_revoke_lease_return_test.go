// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 coverage for the §11.4 full_revoke lease step: every credential
// lease of every session the revoke lists is returned to its pool before
// any cancel write, the step writes no credential deny-list entry, and the
// reported lease count is the number of leases returned.
//
// The case drives the admin router's full_revoke handler and the gateway's
// lease step (leaserevoke.Revoker) over a real credential-assignment
// service, a real lease store, and an LLM proxy whose deny list sits
// behind the cross-replica revocation propagator with an observed revoke
// hook. alice's sessions hold a pool-backed and a user-backed proxy lease;
// bob's live session holds a proxy and a direct lease on the same pool
// credential. The returned tokens are rejected because their records are
// gone, and bob's leases keep working because nothing marks the shared
// pool credential revoked.
//
// spec: §11.4; §4.9.
package tier9_security_test

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
	credrenewalprop "github.com/lennylabs/lenny/pkg/gateway/credentials/credrenewal/propagator"
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

// observedAssigner is the credential assigner the production lease step
// drives. For each Release it records the lease and whether a listed row
// was already cancelled, which must be never.
type observedAssigner struct {
	sessions sessionstore.Store
	assign   *credassign.Service
	ids      []string
	released map[string]int
	// cancelledAtStep counts the Releases that ran after a listed row's
	// cancel write.
	cancelledAtStep int
}

func (o *observedAssigner) Release(leaseID string) error {
	o.released[leaseID]++
	for _, id := range o.ids {
		if row, err := o.sessions.Get(context.Background(), "acme", id); err == nil && row.State == session.StateCancelled {
			o.cancelledAtStep++
			break
		}
	}
	return o.assign.Release(leaseID)
}

// leaseReturnProxyCode sends one request through the proxy with the lease
// token and returns the status and the error code.
func leaseReturnProxyCode(t *testing.T, url, token string) (int, string) {
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

// leaseReturnUserLease is a user-backed proxy lease for alice's session.
func leaseReturnUserLease(sessionID string) credential.Lease {
	return credential.Lease{
		LeaseID: "lease-user-" + sessionID, SessionID: sessionID,
		Provider: credential.ProviderAnthropicDirect, Source: credential.SourceUser,
		TenantID: "acme", CredentialRef: "cred-alice", DeliveryMode: credential.DeliveryProxy,
		IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
		Proxy: &credential.ProxyConfig{
			ProxyURL: "https://gateway-internal:8443/llm-proxy", ProxyDialect: "anthropic",
			LeaseToken: "lt-user-" + sessionID,
		},
	}
}

// leaseReturnDirectLease is a direct-delivery lease for bob's session on
// the same pool credential his proxy lease uses.
func leaseReturnDirectLease(sessionID, credentialID string) credential.Lease {
	return credential.Lease{
		LeaseID: "lease-direct-" + sessionID, SessionID: sessionID,
		Provider: credential.ProviderAnthropicDirect, Source: credential.SourcePool,
		PoolID: "claude-prod", CredentialID: credentialID, DeliveryMode: credential.DeliveryDirect,
		IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	}
}

// spec: 11.4 (User Invalidation: Full revoke propagation mechanism), 4.9 (Credential Leasing Service)
// diagnosis: a full_revoke left a revoked user's credential lease usable, or
// it revoked a credential another user shares. A returned lease token the
// proxy still serves means the lease step did not remove its record; a lease
// step that saw a cancelled row ran after a cancel write; a deny-list entry,
// a revoke-hook call, or a rejected or rotated lease of bob's means the step
// deny-listed the shared pool credential; a leasesRevoked that differs from
// the returned count means the response misreports the step.
func TestFullRevokeReturnsEveryListedLeaseAndDenyListsNone_spec_11_4(t *testing.T) {
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
	sessions := memstore.New()
	users := userstore.NewMemory()
	for _, u := range []string{"alice@acme.com", "bob@acme.com"} {
		if err := users.Create(ctx, userstore.User{Subject: u, TenantID: "acme", Roles: []pkgauth.Role{pkgauth.RoleUser}}); err != nil {
			t.Fatalf("seed user %s: %v", u, err)
		}
	}
	listed := map[string]session.State{
		"s_ready":     session.StateReady,
		"s_running":   session.StateRunning,
		"s_suspended": session.StateSuspended,
	}
	for id, st := range listed {
		if err := sessions.Create(ctx, sessionstore.Session{ID: id, TenantID: "acme", UserID: "alice@acme.com", State: st}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	if err := sessions.Create(ctx, sessionstore.Session{ID: "s_bob", TenantID: "acme", UserID: "bob@acme.com", State: session.StateRunning}); err != nil {
		t.Fatalf("seed s_bob: %v", err)
	}

	var aliceLeases []credential.Lease
	for _, id := range []string{"s_ready", "s_running", "s_suspended"} {
		l, err := svc.Assign("claude-prod", id, "", "acme", 1)
		if err != nil {
			t.Fatalf("Assign %s: %v", id, err)
		}
		aliceLeases = append(aliceLeases, l)
	}
	userL := leaseReturnUserLease("s_running")
	if err := leases.Put(userL); err != nil {
		t.Fatalf("seed alice's user lease: %v", err)
	}
	aliceLeases = append(aliceLeases, userL)
	bobProxy, err := svc.Assign("claude-prod", "s_bob", "", "acme", 1)
	if err != nil {
		t.Fatalf("Assign s_bob: %v", err)
	}
	bobDirect := leaseReturnDirectLease("s_bob", bobProxy.CredentialID)
	if err := leases.Put(bobDirect); err != nil {
		t.Fatalf("seed bob's direct lease: %v", err)
	}

	// The proxy's deny list is the cross-replica propagator the gateway
	// wires, with a revoke hook in place of the direct-mode rotator.
	deny := denylist.New()
	hookCalls := 0
	prop := credrenewalprop.New(deny, nil, nil, credrenewalprop.WithRevokeHook(func(credential.CredentialKey) { hookCalls++ }))
	proxy := httptest.NewServer(&llmproxy.Handler{
		Leases: leases, Credentials: cache, DenyList: prop,
		Translators: llmproxy.NewTranslatorRegistry(&llmproxy.AnthropicDirectTranslator{
			BaseURL: upstream.URL(), DefaultAnthropicVersion: "2023-06-01",
		}),
		Forwarder: &llmproxy.Forwarder{Breaker: &llmproxy.CircuitBreaker{}},
	})
	t.Cleanup(proxy.Close)
	url := proxy.URL + "/v1/messages"
	if code, _ := leaseReturnProxyCode(t, url, bobProxy.Proxy.LeaseToken); code != http.StatusOK {
		t.Fatalf("bob's pre-revoke proxy request = %d, want 200", code)
	}

	observed := &observedAssigner{sessions: sessions, assign: svc, ids: []string{"s_ready", "s_running", "s_suspended"}, released: map[string]int{}}
	step := leaserevoke.New(leases, observed, nil)
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{Clock: time.Now}).
		WithUsers(users).
		WithSessions(sessions).
		WithUserRevocation(nil, step, nil)

	b, _ := json.Marshal(admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateFullRevoke})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/users/alice@acme.com/invalidate", bytes.NewReader(b))
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject: "admin@acme.com", TenantID: "platform", Roles: []pkgauth.Role{pkgauth.RolePlatformAdmin},
	}))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("full_revoke: status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)

	if observed.cancelledAtStep != 0 {
		t.Errorf("%d Releases ran after a cancel write; the lease step must return every lease first", observed.cancelledAtStep)
	}
	for _, l := range aliceLeases {
		if _, ok := leases.GetByID(l.LeaseID); ok {
			t.Errorf("lease %s of %s is still recorded; it was not returned", l.LeaseID, l.SessionID)
		}
		if n := observed.released[l.LeaseID]; n != 1 {
			t.Errorf("lease %s of %s was released %d times, want once", l.LeaseID, l.SessionID, n)
		}
		if code, errCode := leaseReturnProxyCode(t, url, l.Proxy.LeaseToken); code != http.StatusUnauthorized || errCode != "LEASE_TOKEN_INVALID" {
			t.Errorf("returned %s lease %s: proxy answered %d %q, want 401 LEASE_TOKEN_INVALID", l.Source, l.LeaseID, code, errCode)
		}
	}
	if got := resp["leasesRevoked"]; got != float64(len(aliceLeases)) || len(observed.released) != len(aliceLeases) {
		t.Errorf("leasesRevoked = %v with %d leases released, want both %d", got, len(observed.released), len(aliceLeases))
	}
	if resp["sessionsTerminated"] != float64(len(listed)) {
		t.Errorf("sessionsTerminated = %v, want %d", resp["sessionsTerminated"], len(listed))
	}

	// The step deny-lists nothing, so the shared pool credential stays
	// usable for bob.
	if deny.Len() != 0 || hookCalls != 0 {
		t.Errorf("the deny list holds %d entries and the revoke hook ran %d times, want neither", deny.Len(), hookCalls)
	}
	if code, errCode := leaseReturnProxyCode(t, url, bobProxy.Proxy.LeaseToken); code != http.StatusOK {
		t.Errorf("bob's proxy lease on the shared credential: proxy answered %d %q, want 200", code, errCode)
	}
	for _, id := range []string{bobProxy.LeaseID, bobDirect.LeaseID} {
		got, ok := leases.GetByID(id)
		if !ok {
			t.Errorf("bob's lease %s was removed", id)
			continue
		}
		if got.CredentialID != bobProxy.CredentialID {
			t.Errorf("bob's lease %s moved to credential %s, want %s", id, got.CredentialID, bobProxy.CredentialID)
		}
	}
	// RevokeCredential was never called: the pool's only credential still
	// assigns.
	if l, err := svc.Assign("claude-prod", "s_bob_next", "", "acme", 1); err != nil || l.CredentialID != bobProxy.CredentialID {
		t.Errorf("a fresh assignment from the pool = %+v, %v; want %s still selectable", l, err, bobProxy.CredentialID)
	}
}
