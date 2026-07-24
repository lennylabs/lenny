// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the §27.3/§27.3.1 web-playground OIDC
// journey. It drives the browser-facing redirect-to-login,
// authorization-code callback, cookie-to-bearer exchange, and MCP
// WebSocket upgrade against a chart installed with
// playground.enabled=true and playground.authMode=oidc, mirroring
// TestPlaygroundDevModeJourneyOnLiveCluster (playground_test.go) for
// the oidc mode instead of dev.
package tier5_e2e_kind_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/lennylabs/lenny/tests/testinfra/mcpschema"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// spec: §27.3 ("The playground never bypasses gateway auth. A user
// hitting `/playground` is routed through the gateway's auth chain
// identical to API traffic."); §27.3 ("`playground.authMode=oidc`: the
// gateway redirects unauthenticated users to the configured OIDC
// provider. On successful OIDC token exchange the gateway establishes
// an opaque server-side session record ... and sets a single session
// cookie with an explicit, exact path boundary: `Set-Cookie:
// lenny_playground_session=<opaque-session-id>; Path=/playground/;
// HttpOnly; Secure; SameSite=Strict; ...`"); §27.3.1 ("`GET
// /playground/auth/login` — Initiates the OIDC authorization-code
// flow. The gateway generates a per-login `state` and PKCE
// `code_verifier` ... and redirects the browser to the configured
// OIDC provider's authorization endpoint."); §27.3.1 ("`GET
// /playground/auth/callback?code=…&state=…` — OIDC provider redirects
// here. The gateway verifies `state` against the state cookie,
// performs the PKCE-protected token exchange with the provider,
// validates the returned ID token ... and establishes a playground
// session record ... On success, the gateway sets Set-Cookie:
// lenny_playground_session=<opaque-id>; ... and redirects the browser
// to the playground index."); §27.3 ("`oidc`: the endpoint reads the
// `lenny_playground_session` cookie, resolves the subject to the OIDC
// principal backing the server-side session record, and mints a
// bearer with the [origin: "playground"] claim attached."); §27.3.1
// ("**3. WebSocket upgrade.** The client sends the bearer via the
// standard `Authorization: Bearer <bearerToken>` header on the
// `wss://<gateway-host>/mcp/v1/ws` upgrade. The gateway validates the
// bearer exactly as it would for any non-playground MCP client.").
//
// diagnosis: a failure here means the browser-facing OIDC leg of the
// web playground does not work end to end on a real deployed
// installation: either the live chart install did not redirect an
// unauthenticated /playground/ request through the real
// /playground/auth/login → provider → /playground/auth/callback
// round trip, the callback did not establish the
// lenny_playground_session cookie the spec requires, the cookie-based
// mint at POST /v1/playground/token did not stamp the
// origin=playground claim, or the minted bearer was not honored on
// the real /mcp/v1/ws WebSocket transport. Every other OIDC-mode test
// (pkg/gateway/mcpfabric/playground/playground_test.go:725
// TestOIDCLoginRedirectsAndSetsStateCookie,
// pkg/gateway/mcpfabric/playground/sessionrecord_test.go:236
// completeOIDCLogin) drives this flow in-process against an httptest
// server with a fakeOIDC exchanger that never performs a real network
// round trip; this is meant to be the first proof the oidc mode works
// once actually deployed through the real Helm chart onto a real
// cluster, talking to a real (stub) OIDC provider over the network.
func TestPlaygroundOIDCLoginToWebSocketJourneyOnLiveCluster(t *testing.T) {
	// Blocked on two independent, unresolved dependencies; neither is
	// a test-infrastructure design choice this test can make for
	// itself.
	//
	// 1. playground.enabled=true crash-loops every gateway replica
	//    regardless of playground.authMode. cmd/lenny-gateway/httpsurface.go:385
	//    wraps the entire §27 wiring block — including the
	//    unconditional playground.NewMetrics(gwMetrics.Registerer())
	//    call at httpsurface.go:415 — in a single `if *playgroundEnabled`
	//    gate that runs before any authMode branching
	//    (httpsurface.go:388 sets AuthMode from the flag but the
	//    metrics registration at :415 does not depend on it).
	//    pkg/gateway/mcpfabric/playground/metrics.go registers the
	//    lenny_playground_page_views_total counter with the label
	//    "authMode", which pkg/observability/metrics's §16.1.1
	//    snake_case validator rejects, and cmd/lenny-gateway/main.go
	//    treats that error as fatal. This was confirmed live on the
	//    shared e2e cluster for the dev-mode case
	//    (TestPlaygroundDevModeJourneyOnLiveCluster in
	//    playground_test.go) and reproduces identically for oidc mode
	//    since the fatal path is reached before AuthMode is ever
	//    consulted. The spec's own §27.8 metrics table also spells the
	//    label "authMode" (camelCase), so the fix requires reconciling
	//    the spec table and the code together rather than a code-only
	//    change — not a call this test can make for itself.
	//
	// 2. No tests/testinfra building block deploys an OIDC provider
	//    reachable from inside the Kind cluster network.
	//    tests/testinfra/stubs/oidc is an in-process httptest.Server
	//    bound to the test process's own loopback address; a gateway
	//    pod's server-side PKCE token-exchange call (§27.3.1 step 2,
	//    "performs the PKCE-protected token exchange with the
	//    provider") cannot reach it. Exercising this journey needs the
	//    stub (or an equivalent) deployed as a cluster Service with a
	//    DNS name plumbed into --oidc-issuer-url on a
	//    playground.authMode=oidc overlay, which does not exist yet.
	//
	// Once both land, un-skip this test and point the overlay's
	// --oidc-issuer-url at the deployed stub's cluster-reachable
	// issuer URL.
	t.Skip("playground.enabled=true crash-loops the live gateway regardless of authMode (non-snake_case metrics label, shared with the dev-mode case); no in-cluster-reachable OIDC provider stub exists yet either — needs both a spec/code reconciliation and new test infrastructure before this can run")

	d := sessiondriver.New(t)
	base := d.BaseURL()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("build cookie jar: %v", err)
	}
	// A browser-like client: it stores Set-Cookie headers in the jar
	// and lets the test explicitly step through each redirect so the
	// intermediate Location headers and cookies are observable at
	// every hop.
	client := &http.Client{
		Timeout: 30 * time.Second,
		Jar:     jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	t.Run("unauthenticated GET /playground/ is routed through the gateway auth chain to the OIDC login endpoint", func(t *testing.T) {
		resp, err := client.Get(base + "/playground/")
		if err != nil {
			t.Fatalf("GET /playground/: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("GET /playground/: want 302, got %d", resp.StatusCode)
		}
		loc := resp.Header.Get("Location")
		if !strings.Contains(loc, "/playground/auth/login") {
			t.Errorf("Location = %q, want a redirect to /playground/auth/login", loc)
		}
	})

	stateCookieName := "lenny_playground_oidc_state"
	var loginRedirect string
	t.Run("GET /playground/auth/login redirects to the OIDC provider and sets the HttpOnly state cookie", func(t *testing.T) {
		resp, err := client.Get(base + "/playground/auth/login")
		if err != nil {
			t.Fatalf("GET /playground/auth/login: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("GET /playground/auth/login: want 302, got %d", resp.StatusCode)
		}
		loginRedirect = resp.Header.Get("Location")
		if loginRedirect == "" {
			t.Fatal("login response carried no Location header")
		}
		var sawState bool
		for _, c := range resp.Cookies() {
			if c.Name == stateCookieName {
				sawState = true
				if !c.HttpOnly {
					t.Error("state cookie is not HttpOnly")
				}
			}
		}
		if !sawState {
			t.Fatal("login did not set the OIDC state cookie")
		}
	})
	if loginRedirect == "" {
		t.Fatal("no provider redirect to follow; cannot continue to the callback leg")
	}

	var sessionCookieValue string
	t.Run("following the provider redirect completes the callback and establishes the session cookie", func(t *testing.T) {
		// A real browser would present a login form here; the
		// deployed OIDC provider stub auto-approves and redirects
		// straight back to the gateway's callback URL with a code
		// and the echoed state, so the test follows the chain the
		// same way a browser's automatic redirect-following would.
		resp, err := client.Get(loginRedirect)
		if err != nil {
			t.Fatalf("GET provider authorize endpoint: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("provider authorize response: want 302, got %d", resp.StatusCode)
		}
		callbackURL := resp.Header.Get("Location")
		if !strings.Contains(callbackURL, "/playground/auth/callback") {
			t.Fatalf("provider redirected to %q, want the gateway callback path", callbackURL)
		}
		cbResp, err := client.Get(callbackURL)
		if err != nil {
			t.Fatalf("GET %s: %v", callbackURL, err)
		}
		defer cbResp.Body.Close()
		if cbResp.StatusCode != http.StatusFound {
			body, _ := io.ReadAll(cbResp.Body)
			t.Fatalf("GET /playground/auth/callback: want 302, got %d (body %s)", cbResp.StatusCode, body)
		}
		for _, c := range cbResp.Cookies() {
			if c.Name == "lenny_playground_session" && c.Value != "" {
				sessionCookieValue = c.Value
				if !c.HttpOnly || !c.Secure {
					t.Errorf("session cookie HttpOnly=%v Secure=%v, want both true", c.HttpOnly, c.Secure)
				}
			}
		}
		if sessionCookieValue == "" {
			t.Fatal("callback did not set the lenny_playground_session cookie")
		}
	})
	if sessionCookieValue == "" {
		t.Fatal("no session cookie established; cannot continue to the mint leg")
	}

	var bearer string
	t.Run("mint via the session cookie stamps origin=playground on a live bearer", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, base+"/v1/playground/token", strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("build mint request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST /v1/playground/token: %v", err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /v1/playground/token: want 200, got %d (body %s)", resp.StatusCode, raw)
		}
		var minted struct {
			BearerToken string `json:"bearerToken"`
		}
		if err := json.Unmarshal(raw, &minted); err != nil {
			t.Fatalf("decode mint response: %v; body %s", err, raw)
		}
		if minted.BearerToken == "" {
			t.Fatalf("mint response carried no bearerToken: %s", raw)
		}
		claims := decodeOIDCJourneyJWTClaims(t, minted.BearerToken)
		if claims["origin"] != "playground" {
			t.Errorf("minted bearer origin claim = %v, want %q", claims["origin"], "playground")
		}
		bearer = minted.BearerToken
	})
	if bearer == "" {
		t.Fatal("no bearer minted; cannot continue to the WebSocket leg")
	}

	t.Run("the minted bearer opens a real MCP WebSocket session", func(t *testing.T) {
		wsURL := "ws" + strings.TrimPrefix(base, "http") + "/mcp/v1/ws"
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
			HTTPHeader: http.Header{"Authorization": []string{"Bearer " + bearer}},
		})
		if err != nil {
			t.Fatalf("dial /mcp/v1/ws with the OIDC-minted bearer: %v", err)
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		initReq := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "initialize",
			"params": map[string]any{
				"protocolVersion": mcpschema.CurrentVersion,
				"capabilities":    map[string]any{},
				"clientInfo":      map[string]any{"name": "playground-oidc-e2e-test", "version": "0.0.0"},
			},
		}
		body, err := json.Marshal(initReq)
		if err != nil {
			t.Fatalf("marshal initialize frame: %v", err)
		}
		if err := conn.Write(ctx, websocket.MessageText, body); err != nil {
			t.Fatalf("write initialize frame: %v", err)
		}
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read initialize response: %v", err)
		}
		var resp map[string]any
		if err := json.Unmarshal(data, &resp); err != nil {
			t.Fatalf("unmarshal initialize response: %v; frame %s", err, data)
		}
		if _, isErr := resp["error"]; isErr {
			t.Fatalf("initialize over the OIDC-minted bearer returned an error frame: %s", data)
		}
		if result, _ := resp["result"].(map[string]any); result == nil {
			t.Fatalf("initialize response carried no result: %s", data)
		}
	})
}

// decodeOIDCJourneyJWTClaims decodes the payload segment of a compact
// JWT without verifying its signature. The test only needs to read
// the claims the live gateway just minted over the port-forwarded
// connection; signature verification of the platform's own signer is
// covered elsewhere (tier1/tier3).
func decodeOIDCJourneyJWTClaims(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("bearer token has %d segments, want 3", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode JWT payload: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatalf("unmarshal JWT claims: %v", err)
	}
	return claims
}
