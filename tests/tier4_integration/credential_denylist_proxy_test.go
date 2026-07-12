// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §4.9 credential deny-list rejection at the
// LLM reverse proxy for a user-backed lease. It wires the same production
// components cmd/lenny-gateway wires — the user-source credential
// materializer (pkg/gateway/credentials/usercreds), the /v1/credentials
// revoke endpoint (pkg/gateway/credentials/credentialserver), the
// per-replica credential deny list (pkg/gateway/credentials/denylist),
// and the llmproxy.Handler served over an upstream stub — so a test drives
// a live agent request through the proxy, revokes the backing user
// credential through the real POST /v1/credentials/{credentialRef}/revoke
// endpoint, and observes that the next in-flight request is rejected with
// CREDENTIAL_REVOKED before any upstream call is made.
//
// The §4.9 revoke terminates the lease on the replica that served the
// revoke (it drops that replica's lease and adds the credential to the
// deny list) and propagates the deny-list entry to peers. The
// CREDENTIAL_REVOKED rejection therefore fires on a peer replica that
// still holds the lease but has received the propagated deny-list entry.
// The test models that peer with a second lease store while the origin
// replica's materializer serves the revoke; cross-replica pub/sub
// propagation of the deny-list entry itself is covered by the deny-list
// propagator tests, so here the two replicas share the deny list that
// propagation would have converged.

package tier4_integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialserver"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/usercreds"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"

	"github.com/lennylabs/lenny/tests/testinfra/stubs/llmprovider"
)

// spec: 4.9 (credential deny list: "the LLM Proxy subsystem dispatches on
//
//	the lease record's credential source ... user-backed leases are
//	matched against {source: user, tenantId, credentialRef} entries ... A
//	hit ... rejects the request with CREDENTIAL_REVOKED (category:
//	SECURITY) before any upstream call is made." The integration test
//	asserts "an in-flight proxy request presenting a user-backed lease
//	whose credential_ref was just revoked via POST
//	/v1/credentials/{credential_ref}/revoke is rejected with
//	CREDENTIAL_REVOKED before any upstream call is made".)
//
// diagnosis: the §4.9 user-source credential deny-list rejection path
//
//	diverged. A user-backed proxy lease whose credential_ref was revoked
//	via the /v1/credentials revoke endpoint was NOT rejected at the LLM
//	proxy: either the revoke endpoint did not add the user-shaped
//	{source: user, tenantId, credentialRef} deny-list entry, or the proxy
//	did not consult the deny list for a user-backed lease, or it consulted
//	it only after issuing the upstream call. Any of these leaves a window
//	in which a revoked user credential still reaches the upstream provider,
//	defeating the reason revocation is synchronous.
func TestUserCredentialRevocationDenyListProxy(t *testing.T) {
	const (
		tenant  = "acme"
		user    = "alice"
		session = "s-usercred-deny"
		realKey = "sk-ant-user-real-secret-do-not-leak"
	)
	ctx := context.Background()

	upstream := llmprovider.New(t)

	// Shared §4.9 credential registry and deny list. The deny list is both
	// the proxy's revocation oracle (llmproxy.DenyList) and the origin
	// materializer's cross-replica revoker (usercreds.Revoker); sharing the
	// one instance across the two modeled replicas represents the state
	// after the deny-list entry has propagated.
	store := credentialstore.NewMemory(nil)
	dl := denylist.New()

	// Origin replica: the lease store the materializer mints into and the
	// revoke endpoint drops from.
	leasesOrigin := credleasestore.New()
	credsOrigin := credcache.New()
	mat := usercreds.New(usercreds.Config{
		Store:    store,
		Leases:   leasesOrigin,
		Creds:    credsOrigin,
		ProxyURL: "https://lenny-llm-proxy.internal/llm-proxy",
	})
	mat.SetRevoker(dl)

	// Register a user credential and mint a proxy-mode user lease from it,
	// the same objects the §4.9 session-creation path produces for a
	// user-source session.
	cred, err := store.Register(ctx, tenant, user, credential.ProviderAnthropicDirect, "", realKey)
	if err != nil {
		t.Fatalf("register user credential: %v", err)
	}
	if _, err := mat.MintProto(ctx, tenant, user, session, "", string(credential.ProviderAnthropicDirect)); err != nil {
		t.Fatalf("mint user proxy lease: %v", err)
	}
	credKey := credential.CredentialKey{Source: credential.SourceUser, TenantID: tenant, CredentialRef: cred.Ref}
	minted := leasesOrigin.LeasesByCredential(credKey)
	if len(minted) != 1 || minted[0].Proxy == nil || minted[0].Proxy.LeaseToken == "" {
		t.Fatalf("expected exactly one minted proxy lease with a token, got %+v", minted)
	}
	lease := minted[0]
	leaseToken := lease.Proxy.LeaseToken

	// Peer replica: a second lease store and credential cache holding the
	// same lease (as a replica the pod's proxy request load-balances to
	// still would), serving the §4.9 LLM proxy over the upstream stub. It
	// reads the shared deny list.
	leasesPeer := credleasestore.New()
	credsPeer := credcache.New()
	if err := leasesPeer.Put(lease); err != nil {
		t.Fatalf("seed peer lease store: %v", err)
	}
	credsPeer.Put(credKey, realKey)

	registry := llmproxy.NewTranslatorRegistry(&llmproxy.AnthropicDirectTranslator{
		BaseURL:                 upstream.URL(),
		DefaultAnthropicVersion: "2023-06-01",
	})
	handler := &llmproxy.Handler{
		Leases:      leasesPeer,
		Translators: registry,
		Forwarder:   &llmproxy.Forwarder{Breaker: &llmproxy.CircuitBreaker{}},
		Credentials: credsPeer,
		DenyList:    dl,
	}
	proxySrv := httptest.NewServer(handler)
	t.Cleanup(proxySrv.Close)
	proxyMessagesURL := proxySrv.URL + "/v1/messages"

	// The real §15.1 /v1/credentials revoke endpoint over the shared store,
	// wired to the origin materializer as its lease propagator so a revoke
	// drives the deny-list entry. The caller principal is injected the way
	// the gateway's auth middleware injects it.
	credHandler := authInject(credentialserver.New(store).WithLeasePropagator(mat).Handler(), tenant, user)
	credSrv := httptest.NewServer(credHandler)
	t.Cleanup(credSrv.Close)

	// ---- before revocation: the lease works and reaches upstream ----
	if code := doProxy(t, proxyMessagesURL, leaseToken); code != http.StatusOK {
		t.Fatalf("pre-revocation proxy request: status %d, want 200", code)
	}
	if got := len(upstream.Requests()); got != 1 {
		t.Fatalf("pre-revocation: upstream received %d requests, want 1", got)
	}

	// ---- revoke the user credential through the real revoke endpoint ----
	revokeURL := credSrv.URL + "/v1/credentials/" + cred.Ref + "/revoke"
	rreq, _ := http.NewRequest(http.MethodPost, revokeURL, strings.NewReader(`{"reason":"suspected_exfiltration"}`))
	rreq.Header.Set("Content-Type", "application/json")
	rresp, err := http.DefaultClient.Do(rreq)
	if err != nil {
		t.Fatalf("issue revoke request: %v", err)
	}
	rbody, _ := io.ReadAll(rresp.Body)
	rresp.Body.Close()
	if rresp.StatusCode != http.StatusOK {
		t.Fatalf("revoke: status %d, body %s", rresp.StatusCode, rbody)
	}

	// ---- after revocation: the in-flight request the peer serves is
	// rejected with CREDENTIAL_REVOKED before any upstream call ----
	req, _ := http.NewRequest(http.MethodPost, proxyMessagesURL,
		strings.NewReader(`{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"post-revoke"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", leaseToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("issue post-revocation proxy request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("post-revocation proxy request: status %d, want 403; body %s", resp.StatusCode, body)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode proxy error envelope: %v; body %s", err, body)
	}
	if env.Error.Code != "CREDENTIAL_REVOKED" {
		t.Fatalf("post-revocation error code = %q, want CREDENTIAL_REVOKED; body %s", env.Error.Code, body)
	}
	// The rejection must precede the upstream call: the stub's request count
	// must not have advanced past the single pre-revocation request.
	if got := len(upstream.Requests()); got != 1 {
		t.Fatalf("post-revocation: upstream received %d requests, want 1 — the revoked lease reached the provider", got)
	}

	// ---- startup rebuild arm (restarted replica) ----
	// spec §4.9: "a gateway replica restart immediately after either a
	// pool-credential or user-credential revocation rebuilds a complete
	// deny list ... exercising ... the startup rebuild path (on a replica
	// restarted immediately after the revoke)."
	t.Run("startup rebuild on a restarted replica", func(t *testing.T) {
		t.Skip("the §4.9 user-credential startup deny-list rebuild (the 'From TokenStore' union term) is unimplemented: cmd/lenny-gateway/workers.go seeds only pool-credential deny-list entries and the user credential store exposes no revoked-listing query, so a replica restarted immediately after a user-credential revoke does not rebuild the user-shaped {source: user, tenantId, credentialRef} entry from Postgres and would accept the revoked lease; unskip once the rebuild covers the user source")
		// Once implemented, this arm builds a fresh deny list and llmproxy
		// handler sharing the same credential and lease stores (a restarted
		// replica that missed the pub/sub revocation), runs the startup
		// rebuild over both stores, and asserts the same user-backed lease is
		// rejected with CREDENTIAL_REVOKED before any upstream call.
	})
}

// authInject wraps a handler so the credential endpoints see an
// authenticated (tenant, user) caller, standing in for the gateway auth
// middleware that populates the request principal.
func authInject(h http.Handler, tenant, user string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := authmw.WithPrincipal(r.Context(), authmw.Principal{Subject: user, TenantID: tenant})
		h.ServeHTTP(w, r.WithContext(ctx))
	})
}

// doProxy issues one Anthropic Messages request through the proxy with the
// given lease token and returns the response status.
func doProxy(t *testing.T, url, leaseToken string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url,
		strings.NewReader(`{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"ping"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", leaseToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("issue proxy request: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}
