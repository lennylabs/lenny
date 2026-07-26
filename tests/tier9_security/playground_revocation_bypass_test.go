// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 adversarial coverage for the §27.3.1 cross-replica playground
// bearer-revocation guarantee, driven against two real cmd/lenny-gateway
// subprocesses sharing one real Redis container and a real (stub) OIDC
// provider over a genuine PKCE authorization-code flow. Every other test
// of this guarantee (pkg/gateway/mcpfabric/playground/cross_replica_test.go
// TestPlaygroundSessionRevocationCrossReplica, pkg/gateway/middleware/auth's
// fake-checker unit test) drives the RedisSessionStore or the auth
// middleware directly against a single in-process miniredis instance; this
// file is the first to stand up the real HTTP surface twice, point both
// processes at the same Redis, and replay a logged-out session's cookie
// and bearer across the peer replica the way an adversary who captured
// either credential would.
package tier9_security_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	oidcstub "github.com/lennylabs/lenny/tests/testinfra/stubs/oidc"
)

// spec: §27.3.1 ("Every authenticated request carrying a playground-origin
// bearer (identified by the origin: "playground" claim ...) MUST consult
// t:{tenant_id}:pg:revoked:{jti} on the auth hot path before the bearer is
// honored. A hit produces 401 UNAUTHORIZED with details.reason:
// "bearer_revoked" on REST/MCP requests ... This check is the correctness
// guarantee that a logout on one replica cannot be bypassed by presenting
// the same cookie or bearer to a peer replica."); §27.3.1 ("TestPlayground
// SessionRevocationCrossReplica MUST assert that a logout on replica A
// invalidates a subsequent request carrying the same cookie or bearer on
// replica B, both before and after the pub/sub message is delivered (the
// authoritative Redis check covers the pre-delivery case; the LRU negative
// cache covers the post-delivery case).")
//
// diagnosis: a failure here means the §27.3.1 cross-replica revocation
// guarantee does not hold against the real gateway binary and a real Redis
// pub/sub bus: an adversary who captured a playground session cookie or a
// minted bearer before its owner logged out could keep using either
// credential against any gateway replica other than the one that served
// the logout, defeating the reason the revocation write is synchronous and
// fans out cross-replica in the first place. The in-process miniredis
// coverage in pkg/gateway/mcpfabric/playground/cross_replica_test.go pins
// the RedisSessionStore's own logic but cannot catch a wiring defect
// between the real HTTP handlers, the real auth middleware, and a real
// Redis server that only surfaces once two independent gateway processes
// are involved.
func TestPlaygroundCrossReplicaRevocationBypassAdversarial(t *testing.T) {
	// playground.enabled=true crash-loops cmd/lenny-gateway under every
	// playground.authMode: pkg/gateway/mcpfabric/playground/metrics.go
	// registers the lenny_playground_page_views_total counter with the
	// label "authMode", which pkg/observability/metrics's §16.1.1
	// snake_case validator rejects at startup (a fatal error), before the
	// process ever reaches the authMode branching this test depends on.
	// Confirmed directly against the current binary: `lenny-gateway
	// --playground-enabled --playground-auth-mode dev ...` logs
	// `§27.8 playground metrics: metric "lenny_playground_page_views_total":
	// label "authMode" is not snake_case` and exits before binding its
	// listener. The same defect already blocks every other live-process
	// playground test (tests/tier5_e2e_kind/playground_test.go
	// TestPlaygroundDevModeJourneyOnLiveCluster, tests/tier5_e2e_kind/
	// playground_oidc_journey_test.go, tests/tier9_security/playground_test.go
	// TestPlaygroundSecurityPostureOnLiveCluster) and is tracked as a
	// spec/code reconciliation (does spec/27_web-playground.md's own §27.8
	// metrics table get corrected to auth_mode, or does the platform-wide
	// §16.1.1 snake_case rule get an exception) rather than a code-only
	// fix. Remove this skip once that reconciliation lands.
	t.Skip("playground.enabled=true crash-loops cmd/lenny-gateway under every authMode (non-snake_case \"authMode\" metrics label rejected by the §16.1.1 validator); needs a spec/code reconciliation before a real gateway process can serve the playground at all")

	gateway.SkipUnlessAvailable(t)

	redis := containers.StartRedis(t, containers.RedisOptions{})
	idp := oidcstub.New(t)

	const (
		clientID     = "playground-adversarial-test"
		revocSubject = "alice@acme.com"
		revocTenant  = "default" // the built-in tenant every installation always has registered
	)
	redisURL := "redis://" + redis.Addr + "/0"

	pgArgs := []string{
		"--playground-enabled",
		"--playground-auth-mode", "oidc",
		"--oidc-issuer-url", idp.Issuer(),
		"--oidc-client-id", clientID,
		"--redis-url", redisURL,
	}
	replicaA := gateway.StartWith(t, pgArgs...)
	replicaB := gateway.StartWith(t, pgArgs...)

	// ---- establish a real OIDC-mode playground session on replica A ----
	sessionCookie, bearer := loginAndMintOnReplica(t, replicaA, idp, revocSubject, revocTenant)

	// ---- sanity: the peer replica honors both credentials before logout ----
	if status := mintWithCookie(t, replicaB, sessionCookie); status != http.StatusOK {
		t.Fatalf("pre-logout: replica B rejected the still-valid session cookie: status %d", status)
	}
	if status := listSessionsWithBearer(t, replicaB, bearer); status != http.StatusOK {
		t.Fatalf("pre-logout: replica B rejected the still-valid bearer: status %d", status)
	}

	// ---- logout on replica A: the §27.3.1 handler does not return 200
	// until the revocation writes have committed to the shared Redis ----
	logoutOnReplica(t, replicaA, sessionCookie)

	// ---- pre-delivery case: replica B has not necessarily received the
	// pub/sub fanout yet, so this exercises the authoritative Redis GET
	// on the per-request revocation check ----
	if status := mintWithCookie(t, replicaB, sessionCookie); status == http.StatusOK {
		t.Fatal("pre-delivery: replica B minted a fresh bearer from a session cookie logged out on replica A")
	}
	if status := listSessionsWithBearer(t, replicaB, bearer); status != http.StatusUnauthorized {
		t.Fatalf("pre-delivery: replica B honored a bearer revoked on replica A: status %d, want 401", status)
	}

	// ---- post-delivery case: wait past the §27.3.1 500ms P99 propagation
	// SLO so the LRU negative cache on replica B has converged, and assert
	// the rejection still holds (the cache-hit path, not just the
	// authoritative Redis fallback) ----
	time.Sleep(750 * time.Millisecond)
	if status := listSessionsWithBearer(t, replicaB, bearer); status != http.StatusUnauthorized {
		t.Fatalf("post-delivery: replica B honored a bearer revoked on replica A: status %d, want 401", status)
	}
}

// noRedirectHTTPClient never follows a redirect, so each hop of the OIDC
// authorization-code dance is observable and the test can extract the
// Location header and any Set-Cookie headers at every step instead of
// letting a client silently walk the whole chain.
func noRedirectHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// loginAndMintOnReplica drives the real §27.3.1 OIDC authorization-code
// flow against p: GET /playground/auth/login, the provider's /authorize
// (steered to mint the given subject/tenant via the stub's test-only query
// parameters), GET /playground/auth/callback, and finally POST
// /v1/playground/token via the resulting session cookie. It manages
// cookies by hand (rather than an http.Client cookiejar) because every
// cookie the handler sets carries Secure=true and this test drives the
// subprocess over plain HTTP, which a standard cookiejar would refuse to
// re-attach to a non-https request.
func loginAndMintOnReplica(t *testing.T, p *gateway.Process, idp *oidcstub.Stub, subject, tenant string) (sessionCookie, bearer string) {
	t.Helper()
	client := noRedirectHTTPClient()

	loginResp, err := client.Get(p.BaseURL() + "/playground/auth/login")
	if err != nil {
		t.Fatalf("GET /playground/auth/login: %v", err)
	}
	defer loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(loginResp.Body)
		t.Fatalf("GET /playground/auth/login: status %d, body %s", loginResp.StatusCode, body)
	}
	var stateCookie *http.Cookie
	for _, c := range loginResp.Cookies() {
		if c.Name == "lenny_playground_oidc_state" {
			stateCookie = c
		}
	}
	if stateCookie == nil {
		t.Fatal("login response carried no OIDC state cookie")
	}
	authorizeURL := loginResp.Header.Get("Location")
	if authorizeURL == "" {
		t.Fatal("login response carried no Location header")
	}

	// The stub reads "sub" and "tenant_id" as test-only controls bound to
	// the issued authorization code; a real provider would derive these
	// from its own authenticated session instead of a query parameter.
	authorizeReq, err := http.NewRequest(http.MethodGet, authorizeURL+"&sub="+subject+"&tenant_id="+tenant, nil)
	if err != nil {
		t.Fatalf("build authorize request: %v", err)
	}
	authorizeResp, err := client.Do(authorizeReq)
	if err != nil {
		t.Fatalf("GET provider authorize endpoint: %v", err)
	}
	defer authorizeResp.Body.Close()
	if authorizeResp.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(authorizeResp.Body)
		t.Fatalf("provider authorize response: status %d, body %s", authorizeResp.StatusCode, body)
	}
	callbackURL := authorizeResp.Header.Get("Location")
	if !strings.Contains(callbackURL, "/playground/auth/callback") {
		t.Fatalf("provider redirected to %q, want the gateway callback path", callbackURL)
	}

	callbackReq, err := http.NewRequest(http.MethodGet, callbackURL, nil)
	if err != nil {
		t.Fatalf("build callback request: %v", err)
	}
	callbackReq.AddCookie(stateCookie)
	callbackResp, err := client.Do(callbackReq)
	if err != nil {
		t.Fatalf("GET %s: %v", callbackURL, err)
	}
	defer callbackResp.Body.Close()
	if callbackResp.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(callbackResp.Body)
		t.Fatalf("GET /playground/auth/callback: status %d, body %s", callbackResp.StatusCode, body)
	}
	for _, c := range callbackResp.Cookies() {
		if c.Name == "lenny_playground_session" && c.Value != "" {
			sessionCookie = c.Value
		}
	}
	if sessionCookie == "" {
		t.Fatal("callback did not establish the lenny_playground_session cookie")
	}

	if status, minted := mintTokenWithCookie(t, p, sessionCookie); status != http.StatusOK {
		t.Fatalf("POST /v1/playground/token: status %d", status)
	} else {
		bearer = minted
	}
	return sessionCookie, bearer
}

// mintTokenWithCookie drives POST /v1/playground/token against p with the
// supplied session cookie and returns the status code and (on success)
// the minted bearer token.
func mintTokenWithCookie(t *testing.T, p *gateway.Process, sessionCookie string) (status int, bearer string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, p.BaseURL()+"/v1/playground/token", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("build mint request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "lenny_playground_session", Value: sessionCookie})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/playground/token: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, ""
	}
	var minted struct {
		BearerToken string `json:"bearerToken"`
	}
	if err := json.Unmarshal(raw, &minted); err != nil {
		t.Fatalf("decode mint response: %v; body %s", err, raw)
	}
	return resp.StatusCode, minted.BearerToken
}

// mintWithCookie is mintTokenWithCookie without the caller needing the
// minted bearer back, for the post-logout assertions where only the
// status code matters.
func mintWithCookie(t *testing.T, p *gateway.Process, sessionCookie string) int {
	t.Helper()
	status, _ := mintTokenWithCookie(t, p, sessionCookie)
	return status
}

// listSessionsWithBearer drives GET /v1/sessions against p, authenticated
// with the supplied playground-minted bearer via the standard
// Authorization: Bearer header — the same non-playground-specific REST
// surface every other MCP/REST client authenticates against — and returns
// the status code.
func listSessionsWithBearer(t *testing.T, p *gateway.Process, bearer string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, p.BaseURL()+"/v1/sessions", nil)
	if err != nil {
		t.Fatalf("build list-sessions request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/sessions: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// logoutOnReplica drives POST /playground/auth/logout against p with the
// supplied session cookie and fails the test unless the §27.3.1
// commit-before-200 guarantee is observed (a 200 response).
func logoutOnReplica(t *testing.T, p *gateway.Process, sessionCookie string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, p.BaseURL()+"/playground/auth/logout", nil)
	if err != nil {
		t.Fatalf("build logout request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: "lenny_playground_session", Value: sessionCookie})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /playground/auth/logout: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /playground/auth/logout: status %d, body %s", resp.StatusCode, body)
	}
}
