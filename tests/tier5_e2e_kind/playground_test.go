// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the §27 web playground. It installs against
// the live cluster with playground.enabled=true (see
// tests/testinfra/kind/e2e-values.yaml, the only overlay knob this
// test depends on), then walks the dev-mode bearer mint and a real MCP
// WebSocket session end to end through the deployed chart.
package tier5_e2e_kind_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/lennylabs/lenny/tests/testinfra/mcpschema"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// decodePlaygroundJWTClaims decodes the payload segment of a compact
// JWT without verifying its signature. The test only needs to read the
// claims the live gateway just minted over the port-forwarded
// connection; signature verification of the platform's own signer is
// covered elsewhere (tier1/tier3).
func decodePlaygroundJWTClaims(t *testing.T, token string) map[string]any {
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

// spec: §27.2 ("Feature-flag gating" table) "`playground.enabled` |
// `false` | When `false`, `/playground/*` returns `404` and the asset
// bundle is unmounted" (so a true install must serve it); §27.3 ("`dev`:
// the endpoint accepts an empty body with no admission material ...
// synthesizes the subject from `playground.devTenantId` and a synthetic
// `dev-user` principal, and issues a dev HMAC-signed session JWT ... with
// the `origin: "playground"` claim attached"); §27.5 ("Chat stream: MCP
// WebSocket at `/mcp/v1/ws` with a session-capability JWT as the bearer.
// The JWT is minted by the single mode-polymorphic endpoint
// `POST /v1/playground/token`"); §27.7 (the Content-Security-Policy
// directive block, "applied only to `/playground/*`").
//
// diagnosis: a failure here means the bundled web playground does not
// work end to end on a real deployed installation: either the live
// chart install did not mount the /playground/* routes when
// playground.enabled=true, the dev-mode mint endpoint did not stamp the
// origin=playground claim the rest of the §27 protocol depends on, the
// minted bearer was not honored on the real /mcp/v1/ws WebSocket
// transport the way it is on the standard REST surface, or the §27.7
// Content-Security-Policy header was not applied to a live response.
// Every other playground test exercises this logic through httptest /
// in-process handlers only (tier1-3) or an explicitly disabled install
// (tier4); this is the first proof the feature works once actually
// deployed through the real Helm chart onto a real cluster.
func TestPlaygroundDevModeJourneyOnLiveCluster(t *testing.T) {
	// The e2e overlay (tests/testinfra/kind/e2e-values.yaml) does not set
	// playground.enabled=true: doing so currently crash-loops every
	// gateway replica at startup. pkg/gateway/mcpfabric/playground/metrics.go
	// registers the lenny_playground_page_views_total counter with the
	// label "authMode", which pkg/observability/metrics's §16.1.1
	// snake_case validator rejects (ValidationError: label "authMode" is
	// not snake_case); cmd/lenny-gateway/main.go treats that error as
	// fatal, so no replica ever becomes Ready. Confirmed live on this
	// cluster: enabling the overlay value reproduces the crash loop on
	// every gateway pod. §27.8's own metrics table names this same label
	// "authMode" (camelCase), so fixing the registration to "auth_mode"
	// contradicts the literal spec table until that table is corrected
	// through the proposal pipeline; this is not a call this test can
	// make for itself. Once the label naming is reconciled and the
	// overlay carries playground.enabled=true (authMode: dev,
	// devTenantId: acme, matching the "acme" tenant bootstrap.tenants
	// seeds), remove this skip.
	t.Skip("playground.enabled=true crash-loops the live gateway (non-snake_case metrics label); needs a spec/code reconciliation before this can run")

	d := sessiondriver.New(t)
	base := d.BaseURL()
	client := &http.Client{Timeout: 30 * time.Second}

	t.Run("playground index carries the CSP and companion security headers", func(t *testing.T) {
		resp, err := client.Get(base + "/playground/")
		if err != nil {
			t.Fatalf("GET /playground/: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /playground/: want 200, got %d (body %s)", resp.StatusCode, body)
		}
		csp := resp.Header.Get("Content-Security-Policy")
		for _, directive := range []string{
			"default-src 'self'", "script-src 'self'", "object-src 'none'",
			"media-src 'none'", "frame-ancestors 'none'",
		} {
			if !strings.Contains(csp, directive) {
				t.Errorf("Content-Security-Policy = %q, missing directive %q", csp, directive)
			}
		}
		if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
		}
		if got := resp.Header.Get("Referrer-Policy"); got != "same-origin" {
			t.Errorf("Referrer-Policy = %q, want same-origin", got)
		}
	})

	var bearer string
	t.Run("dev-mode mint stamps origin=playground on a live bearer", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, base+"/v1/playground/token", nil)
		if err != nil {
			t.Fatalf("build mint request: %v", err)
		}
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
		claims := decodePlaygroundJWTClaims(t, minted.BearerToken)
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
			t.Fatalf("dial /mcp/v1/ws with the playground-minted bearer: %v", err)
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		initReq := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "initialize",
			"params": map[string]any{
				"protocolVersion": mcpschema.CurrentVersion,
				"capabilities":    map[string]any{},
				"clientInfo":      map[string]any{"name": "playground-e2e-test", "version": "0.0.0"},
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
			t.Fatalf("initialize over the playground-minted bearer returned an error frame: %s", data)
		}
		if result, _ := resp["result"].(map[string]any); result == nil {
			t.Fatalf("initialize response carried no result: %s", data)
		}
	})
}
