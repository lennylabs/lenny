// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §9.3 connector OAuth 2.1
// authorization-code flow end-to-end through the real
// cmd/lenny-gateway binary. It registers an oauth2 connector, drives
// POST /v1/admin/connectors/{id}/oauth/authorize to mint the
// authorization URL and signed state, then drives
// GET /v1/admin/connectors/oauth/callback against a fake provider
// token endpoint and asserts the gateway completes the code exchange.

package tier4_integration_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// spec: 9.3 (connector OAuth 2.1 authorization-code flow)
// diagnosis: the §9.3 connector OAuth authorize→callback flow failed
//
//	through the real cmd/lenny-gateway binary. The
//	POST /v1/admin/connectors/{id}/oauth/authorize endpoint, the
//	GET /v1/admin/connectors/oauth/callback handler, the signed
//	state mint/verify, the PKCE S256 challenge, or the
//	provider token exchange diverged from §9.3 when driven
//	through one process.
func TestOAuthConnector(t *testing.T) {
	gateway.SkipUnlessAvailable(t)

	// A fake OAuth provider token endpoint. The §9.3 callback handler
	// POSTs grant_type=authorization_code here; the fake returns a
	// token response so the flow can complete without a real provider.
	// §9.3 requires the connector token endpoint to be HTTPS, so the
	// provider is a TLS server; its self-signed certificate is handed
	// to the gateway via --connector-oauth-ca.
	var exchanged struct {
		code         string
		codeVerifier string
		grantType    string
		hit          bool
	}
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		exchanged.hit = true
		exchanged.code = r.Form.Get("code")
		exchanged.codeVerifier = r.Form.Get("code_verifier")
		exchanged.grantType = r.Form.Get("grant_type")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "provider-access-token",
			"refresh_token": "provider-refresh-token",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"scope":         "repo read:org",
		})
	}))
	defer provider.Close()

	// Write the provider's self-signed certificate to a PEM file so the
	// gateway's connector-OAuth HTTP client trusts the TLS token
	// endpoint. httptest's leaf certificate is self-signed, so it is
	// its own CA.
	caPath := filepath.Join(t.TempDir(), "provider-ca.pem")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: provider.Certificate().Raw})
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		t.Fatalf("write provider CA: %v", err)
	}

	// The §9.3 flow requires an absolute callback URL: it is the
	// redirect_uri the provider would redirect the browser back to.
	// The callback value only needs to be a well-formed absolute URL;
	// the test invokes the callback endpoint directly.
	gw := gateway.StartWith(t, "--dev-mode",
		"--connector-oauth-callback-url", "http://callback.acme.test/v1/admin/connectors/oauth/callback",
		"--connector-oauth-ca", caPath)
	base := gw.BaseURL()
	client := http.DefaultClient

	do := func(method, path, roles string, body any) (int, map[string]any) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reader = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, base+path, reader)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
		if roles != "" {
			req.Header.Set("X-Lenny-Roles", roles)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &out)
		}
		return resp.StatusCode, out
	}

	// ---- register an oauth2 connector (public client: no
	//      clientSecretRef, so the flow is PKCE-only) ----
	code, _ := do(http.MethodPost, "/v1/admin/connectors", "platform-admin", map[string]any{
		"id":           "github",
		"displayName":  "GitHub",
		"mcpServerUrl": "https://mcp.github.test",
		"transport":    "streamable_http",
		"auth": map[string]any{
			"type":                  "oauth2",
			"authorizationEndpoint": "https://github.test/login/oauth/authorize",
			"tokenEndpoint":         provider.URL + "/token",
			"clientId":              "lenny-client-id",
			"scopes":                []string{"repo", "read:org"},
		},
	})
	if code != http.StatusCreated {
		t.Fatalf("create connector: status %d", code)
	}

	// ---- initiate the authorization flow ----
	code, authz := do(http.MethodPost, "/v1/admin/connectors/github/oauth/authorize", "platform-admin", nil)
	if code != http.StatusOK {
		t.Fatalf("authorize: status %d (%v)", code, authz)
	}
	authURL, _ := authz["authorizationUrl"].(string)
	state, _ := authz["state"].(string)
	if authURL == "" || state == "" {
		t.Fatalf("authorize response missing authorizationUrl/state: %v", authz)
	}
	// §9.3: the authorization URL carries the OAuth 2.1 grant params,
	// the signed state, and the PKCE S256 challenge.
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("authorizationUrl is not a URL: %v", err)
	}
	q := parsed.Query()
	if q.Get("response_type") != "code" {
		t.Errorf("authorization URL response_type = %q, want code", q.Get("response_type"))
	}
	if q.Get("state") != state {
		t.Errorf("authorization URL state = %q, want %q", q.Get("state"), state)
	}
	codeChallenge := q.Get("code_challenge")
	if codeChallenge == "" || q.Get("code_challenge_method") != "S256" {
		t.Errorf("authorization URL missing PKCE S256 challenge: %v", q)
	}

	// ---- the provider redirects the browser back to the callback
	//      with the authorization code and the signed state ----
	callbackPath := "/v1/admin/connectors/oauth/callback?code=auth-code-xyz&state=" + url.QueryEscape(state)
	resp, err := client.Get(base + callbackPath)
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("callback: want 200, got %d (body %s)", resp.StatusCode, raw)
	}
	var cb map[string]any
	_ = json.Unmarshal(raw, &cb)
	if cb["status"] != "connector_authorized" {
		t.Errorf("callback status = %v, want connector_authorized", cb["status"])
	}
	if cb["connectorId"] != "github" {
		t.Errorf("callback connectorId = %v, want github", cb["connectorId"])
	}

	// §9.3: the callback drove the authorization-code-grant token
	// exchange at the provider, replaying the code and PKCE verifier.
	if !exchanged.hit {
		t.Fatal("the callback did not exchange the code at the provider token endpoint")
	}
	if exchanged.grantType != "authorization_code" {
		t.Errorf("token exchange grant_type = %q, want authorization_code", exchanged.grantType)
	}
	if exchanged.code != "auth-code-xyz" {
		t.Errorf("token exchange code = %q, want the callback code", exchanged.code)
	}
	if exchanged.codeVerifier == "" {
		t.Error("token exchange carried no PKCE code_verifier")
	}

	// §9.3 / RFC 7636: the code_challenge sent in the authorization
	// request MUST be BASE64URL(SHA256(ASCII(code_verifier))) for the
	// verifier submitted at token exchange. Pin the transformation
	// end-to-end so a regression that mints a code_challenge not
	// derived from the verifier, or that silently switches to the
	// `plain` method while still labelling the request S256, fails
	// here even though the two presence checks above would pass.
	sum := sha256.Sum256([]byte(exchanged.codeVerifier))
	wantChallenge := base64.RawURLEncoding.EncodeToString(sum[:])
	if codeChallenge != wantChallenge {
		t.Errorf("code_challenge = %q, want BASE64URL(SHA256(code_verifier)) = %q (verifier %q)",
			codeChallenge, wantChallenge, exchanged.codeVerifier)
	}

	// §9.3 anti-CSRF: a replayed state is single-use — the second
	// callback with the same state is rejected.
	resp2, err := client.Get(base + callbackPath)
	if err != nil {
		t.Fatalf("replay callback: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("replayed state callback: want 400, got %d", resp2.StatusCode)
	}
	raw2, _ := io.ReadAll(resp2.Body)
	var rep map[string]any
	_ = json.Unmarshal(raw2, &rep)
	envelope, _ := rep["error"].(map[string]any)
	if errCode, _ := envelope["code"].(string); !strings.HasPrefix(errCode, "CONNECTOR_OAUTH_STATE") {
		t.Errorf("replayed state error code = %q, want a CONNECTOR_OAUTH_STATE_* code", errCode)
	}

	// §9.3 anti-CSRF: a callback whose state was not minted by this
	// gateway fails signature verification before any code exchange.
	resp3, err := client.Get(base + "/v1/admin/connectors/oauth/callback?code=x&state=forged-state")
	if err != nil {
		t.Fatalf("forged-state callback: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusBadRequest {
		t.Errorf("forged-state callback: want 400, got %d", resp3.StatusCode)
	}
}
