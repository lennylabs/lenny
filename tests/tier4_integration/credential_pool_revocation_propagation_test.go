// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §4.9 Emergency Credential Revocation
// credential_revocation suite (TESTING.md §12.4). It drives a pool-backed
// proxy lease through the real admin emergency-revocation endpoint
// (POST /v1/admin/credential-pools/{name}/credentials/{credId}/revoke),
// propagates the revocation to a second gateway replica over a real Redis
// pub/sub bus (the production credential deny-list propagator over a
// miniredis-backed EventBus), and asserts that the peer replica's LLM
// reverse proxy rejects the still-held lease with CREDENTIAL_REVOKED
// before any upstream call — all within the documented propagation SLO.
//
// The two modeled replicas each own a private in-memory deny list wrapped
// by pkg/gateway/credentials/denylist/propagator.Propagator over its own
// pubsub.Bus client against one miniredis server, so a Revoke published on
// the origin replica reaches the peer over the same Redis PUBLISH/SUBSCRIBE
// path the gateway wires in production. The origin replica serves the admin
// revoke endpoint and terminates its own lease; the peer replica holds the
// same lease (as a replica a pod's proxy request load-balances to still
// would) and enforces the propagated deny-list entry at its LLM proxy.

package tier4_integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/denylist/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pubsub"

	"github.com/lennylabs/lenny/tests/testinfra/stubs/llmprovider"
)

// poolRevoker mirrors cmd/lenny-gateway's poolCredentialRevoker: the
// admin.PoolCredentialRevoker the emergency-revocation endpoint calls.
// For each revoked credential it adds the source-aware pool identity to
// the deny list (a Propagator, so the entry fans out to peer replicas over
// Redis) and retains the leases this replica holds against it, denied in
// place by the deny-list entry, returning the count of leases affected. It
// is reconstructed here rather than imported because the production glue
// lives in package main. spec: §4.9 lines 1640-1652, 1671.
type poolRevoker struct {
	denyList *propagator.Propagator
	leases   *credleasestore.Store
}

func (p *poolRevoker) RevokePoolCredentials(_ context.Context, poolID string, credentialIDs []string) int {
	total := 0
	for _, credID := range credentialIDs {
		key := credential.CredentialKey{
			Source:       credential.SourcePool,
			PoolID:       poolID,
			CredentialID: credID,
		}
		p.denyList.Revoke(key)
		// spec: §4.9 — the lease is retained and denied in place; the count
		// is leases-affected, not leases-removed.
		for range p.leases.LeasesByCredential(key) {
			total++
		}
	}
	return total
}

// replica is one modeled gateway replica: a private deny list wrapped by
// the credential deny-list propagator over its own Redis client, and its
// own credential-lease store and upstream-credential cache. Both replicas
// point their pubsub.Bus at the same miniredis server.
type replica struct {
	prop   *propagator.Propagator
	leases *credleasestore.Store
	creds  *credcache.Cache
}

func newReplica(mrAddr string) (*replica, *redis.Client) {
	client := redis.NewClient(&redis.Options{Addr: mrAddr})
	return &replica{
		prop:   propagator.New(denylist.New(), pubsub.New(client)),
		leases: credleasestore.New(),
		creds:  credcache.New(),
	}, client
}

const (
	revPool     = "claude-direct-prod"
	revCredID   = "key-2"
	revTenant   = "acme"
	revSession  = "s-emergency-revoke"
	revUpstream = "sk-upstream-key2-real-secret-do-not-leak"
	// revProxyBody is a minimal Anthropic Messages request; the default
	// stub echoes it with 200 before revocation.
	revProxyBody = `{"model":"claude-3-5-sonnet","max_tokens":16,"messages":[{"role":"user","content":"ping"}]}`
)

// spec: 4.9 (Emergency Credential Revocation, spec/04_system-components.md
//
//	lines 1626-1652: "The credential ID is added to an in-memory credential
//	deny list on every gateway replica, propagated via Redis pub/sub ...
//	Any request presenting a lease backed by a denied credential is
//	immediately rejected with CREDENTIAL_REVOKED ... there is no window
//	where the compromised key continues to reach the provider."), 11.4
//	(spec/11_policy-and-controls.md line 263: "peer replicas apply them on
//	their subscribers within seconds"), 16 (spec/16_observability.md line
//	60: a revoked credential still holding active leases beyond 30s fires
//	the CredentialCompromised critical alert — the propagation SLO ceiling),
//	18 (spec/18_build-sequence.md line 480: "Emergency revocation
//	propagating through Redis pub/sub; active leases terminate within the
//	documented SLO").
//
// diagnosis: the §4.9 pool-credential emergency-revocation propagation path
//
//	diverged across gateway replicas. A pool credential revoked through the
//	real POST /v1/admin/credential-pools/{name}/credentials/{credId}/revoke
//	endpoint on one replica did not reach a peer replica's deny list over
//	Redis pub/sub within the propagation SLO, or the peer's LLM reverse
//	proxy did not reject a lease backed by the revoked credential with
//	CREDENTIAL_REVOKED before issuing the upstream call. Either failure
//	leaves a window in which a compromised pool key still reaches the
//	provider on the replicas that hold its lease, defeating the reason
//	emergency revocation is synchronous and cross-replica.
func TestPoolCredentialEmergencyRevocationPropagatesAcrossReplicas(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// One Redis server, two replica clients: the shared EventBus the
	// credential deny-list propagator fans revocations out over.
	mr := miniredis.RunT(t)
	origin, originClient := newReplica(mr.Addr())
	peer, peerClient := newReplica(mr.Addr())
	t.Cleanup(func() { _ = originClient.Close(); _ = peerClient.Close() })

	// Each replica subscribes to the credential deny-list channel; a Revoke
	// on either replica publishes there and the peer applies it locally.
	go origin.prop.Run(ctx)
	go peer.prop.Run(ctx)

	// Redis pub/sub is at-most-once with no server-side buffering: a Revoke
	// published before the peer's SUBSCRIBE lands is lost. Wait until both
	// replicas are subscribed before driving the revocation, so the test
	// exercises propagation rather than a subscribe race.
	waitSubscribers(t, ctx, originClient, propagator.Channel, 2)

	// Mint a pool-backed proxy lease on the origin replica: the §4.9
	// session-creation path assigns a proxy lease from a direct-prod pool.
	assign := credassign.New(origin.leases, origin.creds)
	assign.RegisterPool(credassign.Pool{
		Name:         revPool,
		Provider:     credential.ProviderAnthropicDirect,
		DeliveryMode: credential.DeliveryProxy,
		Strategy:     credential.StrategyLeastLoaded,
		ProxyURL:     "https://lenny-llm-proxy.internal/llm-proxy",
		ProxyDialect: string(credential.ProxyDialectAnthropic),
		Credentials: []credassign.PoolCredential{
			{ID: revCredID, APIKey: revUpstream, Healthy: true},
		},
	})
	lease, err := assign.Assign(revPool, revSession, "", revTenant)
	if err != nil {
		t.Fatalf("assign pool lease: %v", err)
	}
	if lease.Proxy == nil || lease.Proxy.LeaseToken == "" {
		t.Fatalf("assigned pool lease carries no proxy token: %+v", lease)
	}
	leaseToken := lease.Proxy.LeaseToken
	credKey := credential.CredentialKey{
		Source:       credential.SourcePool,
		PoolID:       revPool,
		CredentialID: revCredID,
	}

	// The peer replica holds the same lease and its upstream credential, as
	// a replica the pod's proxy request load-balances to still would after
	// the origin replica minted it.
	if err := peer.leases.Put(lease); err != nil {
		t.Fatalf("seed peer lease store: %v", err)
	}
	peer.creds.Put(credKey, revUpstream)

	// The peer replica's §4.9 LLM reverse proxy, reading its own lease store,
	// credential cache, and (propagated) deny list, served over an upstream
	// stub that echoes 200 by default.
	upstream := llmprovider.New(t)
	registry := llmproxy.NewTranslatorRegistry(&llmproxy.AnthropicDirectTranslator{
		BaseURL:                 upstream.URL(),
		DefaultAnthropicVersion: "2023-06-01",
	})
	peerProxy := httptest.NewServer(&llmproxy.Handler{
		Leases:      peer.leases,
		Translators: registry,
		Forwarder:   &llmproxy.Forwarder{Breaker: &llmproxy.CircuitBreaker{}},
		Credentials: peer.creds,
		DenyList:    peer.prop,
	})
	t.Cleanup(peerProxy.Close)
	peerMessagesURL := peerProxy.URL + "/v1/messages"

	// The real §4.9 admin emergency-revocation endpoint on the origin
	// replica, wired to the origin's deny-list propagator and lease store
	// exactly as cmd/lenny-gateway wires poolCredentialRevoker.
	poolStore := credentialpoolstore.NewMemory()
	if err := poolStore.Create(ctx, credentialpoolstore.CredentialPool{
		TenantID: revTenant,
		Name:     revPool,
		Provider: string(credential.ProviderAnthropicDirect),
		Credentials: []credentialpoolstore.Credential{
			{ID: "key-1", SecretRef: "lenny-system/k1"},
			{ID: revCredID, SecretRef: "lenny-system/k2"},
		},
	}); err != nil {
		t.Fatalf("seed credential pool: %v", err)
	}
	adminRouter := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithCredentialPools(poolStore).
		WithPoolCredentialRevocation(&poolRevoker{denyList: origin.prop, leases: origin.leases})

	// ---- before revocation: the peer's lease reaches upstream ----
	if code := doProxy(t, peerMessagesURL, leaseToken); code != http.StatusOK {
		t.Fatalf("pre-revocation proxy request: status %d, want 200", code)
	}
	if got := len(upstream.Requests()); got != 1 {
		t.Fatalf("pre-revocation: upstream received %d requests, want 1", got)
	}

	// ---- emergency-revoke key-2 through the real admin endpoint ----
	revokeStart := time.Now()
	revReq := adminRevokeRequest(t, adminRouter.Handler(),
		"/v1/admin/credential-pools/"+revPool+"/credentials/"+revCredID+"/revoke")
	if revReq.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(revReq.Body)
		revReq.Body.Close()
		t.Fatalf("admin revoke: status %d, body %s", revReq.StatusCode, body)
	}
	var summary struct {
		RevokedCredential string `json:"revokedCredential"`
		LeasesTerminated  int    `json:"leasesTerminated"`
		PropagatedAt      string `json:"propagatedAt"`
	}
	if err := json.NewDecoder(revReq.Body).Decode(&summary); err != nil {
		t.Fatalf("decode revoke summary: %v", err)
	}
	revReq.Body.Close()
	if summary.RevokedCredential != revCredID {
		t.Errorf("revokedCredential = %q, want %q", summary.RevokedCredential, revCredID)
	}
	// The origin replica held exactly one lease against key-2; the §4.9
	// terminator dropped it, so leasesTerminated reflects that.
	if summary.LeasesTerminated != 1 {
		t.Errorf("leasesTerminated = %d, want 1", summary.LeasesTerminated)
	}
	if summary.PropagatedAt == "" {
		t.Error("revoke summary missing propagatedAt")
	}

	// ---- propagation SLO: the peer replica observes the deny-list entry
	// within seconds (§11.4), well inside the 30s CredentialCompromised
	// alert ceiling (§16) ----
	const propagationSLO = 5 * time.Second
	deadline := time.Now().Add(propagationSLO)
	for !peer.prop.Revoked(credKey) {
		if time.Now().After(deadline) {
			t.Fatalf("peer replica did not observe the revoked pool credential within the %s propagation SLO", propagationSLO)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if elapsed := time.Since(revokeStart); elapsed > propagationSLO {
		t.Errorf("cross-replica revocation propagation took %s, over the %s SLO", elapsed, propagationSLO)
	}

	// ---- after revocation: the peer rejects the still-held lease with
	// CREDENTIAL_REVOKED before any upstream call ----
	req, _ := http.NewRequest(http.MethodPost, peerMessagesURL, strings.NewReader(revProxyBody))
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
	// The rejection preceded the upstream call: the stub's request count did
	// not advance past the single pre-revocation request.
	if got := len(upstream.Requests()); got != 1 {
		t.Fatalf("post-revocation: upstream received %d requests, want 1 — the revoked lease reached the provider", got)
	}
}

// TestPoolCredentialEmergencyRevocationDeniesRetainedLeaseSharedStore drives
// a pool-backed proxy lease through the real admin emergency-revocation
// endpoint under the multi-replica shared-Postgres topology, where the revoke
// endpoint and the LLM proxy read one shared credential-lease store. The §4.9
// revoke retains the lease and adds the credential to the deny list, so a
// post-revocation proxy request resolves the retained lease and is rejected
// CREDENTIAL_REVOKED before any upstream call. Deleting the lease on revoke
// (the pre-fix behavior) would remove the row the proxy reads, so the request
// would resolve no lease and degrade to 401 LEASE_TOKEN_INVALID, never
// reaching the deny-list check.
//
// spec: §4.9 (CREDENTIAL_REVOKED reachable for a pool-backed lease under the
// shared store).
func TestPoolCredentialEmergencyRevocationDeniesRetainedLeaseSharedStore(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// One shared credential-lease store, upstream-credential cache, and deny
	// list, as every replica reads under the shared-Postgres topology.
	leases := credleasestore.New()
	creds := credcache.New()
	dl := denylist.New()

	assign := credassign.New(leases, creds)
	assign.RegisterPool(credassign.Pool{
		Name:         revPool,
		Provider:     credential.ProviderAnthropicDirect,
		DeliveryMode: credential.DeliveryProxy,
		Strategy:     credential.StrategyLeastLoaded,
		ProxyURL:     "https://lenny-llm-proxy.internal/llm-proxy",
		ProxyDialect: string(credential.ProxyDialectAnthropic),
		Credentials: []credassign.PoolCredential{
			{ID: revCredID, APIKey: revUpstream, Healthy: true},
		},
	})
	lease, err := assign.Assign(revPool, revSession, "", revTenant)
	if err != nil {
		t.Fatalf("assign pool lease: %v", err)
	}
	if lease.Proxy == nil || lease.Proxy.LeaseToken == "" {
		t.Fatalf("assigned pool lease carries no proxy token: %+v", lease)
	}
	leaseToken := lease.Proxy.LeaseToken

	// The §4.9 LLM proxy reads the same shared lease store, credential cache,
	// and deny list.
	upstream := llmprovider.New(t)
	registry := llmproxy.NewTranslatorRegistry(&llmproxy.AnthropicDirectTranslator{
		BaseURL:                 upstream.URL(),
		DefaultAnthropicVersion: "2023-06-01",
	})
	proxy := httptest.NewServer(&llmproxy.Handler{
		Leases:      leases,
		Translators: registry,
		Forwarder:   &llmproxy.Forwarder{Breaker: &llmproxy.CircuitBreaker{}},
		Credentials: creds,
		DenyList:    dl,
	})
	t.Cleanup(proxy.Close)
	messagesURL := proxy.URL + "/v1/messages"

	// The real §4.9 admin emergency-revocation endpoint, wired to the shared
	// deny list and lease store exactly as cmd/lenny-gateway wires
	// poolCredentialRevoker (reusing the reconstructed lifecycleRevoker).
	poolStore := credentialpoolstore.NewMemory()
	if err := poolStore.Create(ctx, credentialpoolstore.CredentialPool{
		TenantID: revTenant,
		Name:     revPool,
		Provider: string(credential.ProviderAnthropicDirect),
		Credentials: []credentialpoolstore.Credential{
			{ID: revCredID, SecretRef: "lenny-system/k2"},
		},
	}); err != nil {
		t.Fatalf("seed credential pool: %v", err)
	}
	adminRouter := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithCredentialPools(poolStore).
		WithPoolCredentialRevocation(&lifecycleRevoker{denyList: dl, leases: leases})

	// ---- before revocation: the shared lease reaches upstream ----
	if code := doProxy(t, messagesURL, leaseToken); code != http.StatusOK {
		t.Fatalf("pre-revocation proxy request: status %d, want 200", code)
	}
	if got := len(upstream.Requests()); got != 1 {
		t.Fatalf("pre-revocation: upstream received %d requests, want 1", got)
	}

	// ---- emergency-revoke through the real admin endpoint ----
	revResp := adminRevokeRequest(t, adminRouter.Handler(),
		"/v1/admin/credential-pools/"+revPool+"/credentials/"+revCredID+"/revoke")
	if revResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(revResp.Body)
		revResp.Body.Close()
		t.Fatalf("admin revoke: status %d, body %s", revResp.StatusCode, body)
	}
	revResp.Body.Close()

	// ---- after revocation: the retained shared lease is denied in place ----
	req, _ := http.NewRequest(http.MethodPost, messagesURL, strings.NewReader(revProxyBody))
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
	// The rejection preceded the upstream call: the stub's request count did
	// not advance past the single pre-revocation request.
	if got := len(upstream.Requests()); got != 1 {
		t.Fatalf("post-revocation: upstream received %d requests, want 1 — the revoked lease reached the provider", got)
	}
}

// waitSubscribers blocks until at least want subscribers are registered on
// channel, so a subsequent PUBLISH is delivered rather than dropped by
// Redis's at-most-once pub/sub. It fails the test if the count is not
// reached within a short deadline.
func waitSubscribers(t *testing.T, ctx context.Context, client *redis.Client, channel string, want int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		counts, err := client.PubSubNumSub(ctx, channel).Result()
		if err == nil && counts[channel] >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d/%d subscribers registered on %q before the deadline", counts[channel], want, channel)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// adminRevokeRequest issues a POST to the admin revoke path as a tenant
// admin of acme and returns the raw response.
func adminRevokeRequest(t *testing.T, h http.Handler, path string) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"reason":"suspected_exfiltration"}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject:  "admin@acme.com",
		TenantID: revTenant,
		Roles:    []pkgauth.Role{pkgauth.RoleTenantAdmin},
	}))
	h.ServeHTTP(rec, req)
	return rec.Result()
}
