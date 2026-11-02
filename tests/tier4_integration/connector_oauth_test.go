// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §9.3 connector OAuth 2.1
// authorization-code flow end-to-end through the real
// cmd/lenny-gateway binary. It registers an oauth2 connector, drives
// POST /v1/admin/connectors/{id}/oauth/authorize to mint the
// authorization URL and random state, then drives
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
	"sync/atomic"
	"testing"

	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// spec: 9.3 (connector OAuth 2.1 authorization-code flow)
// diagnosis: the §9.3 connector OAuth authorize→callback flow failed
//
//	through the real cmd/lenny-gateway binary. The
//	POST /v1/admin/connectors/{id}/oauth/authorize endpoint, the
//	GET /v1/admin/connectors/oauth/callback handler, the
//	state store, the PKCE S256 challenge, or the provider
//	token exchange diverged from §9.3 when driven through one
//	process.
func TestOAuthConnector(t *testing.T) {
	t.Parallel()
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

	caPath := writeConnectorProviderCA(t, provider)

	// The §9.3 flow requires an absolute callback URL: it is the
	// redirect_uri the provider would redirect the browser back to.
	// The callback value only needs to be a well-formed absolute URL;
	// the test invokes the callback endpoint directly.
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all", "--dev-mode",
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
	// the state, and the PKCE S256 challenge.
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
	//      with the authorization code and the state ----
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

	// §9.3 anti-CSRF: a callback whose state has no stored flow is
	// refused before any code exchange.
	resp3, err := client.Get(base + "/v1/admin/connectors/oauth/callback?code=x&state=forged-state")
	if err != nil {
		t.Fatalf("forged-state callback: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusBadRequest {
		t.Errorf("forged-state callback: want 400, got %d", resp3.StatusCode)
	}
	raw3, _ := io.ReadAll(resp3.Body)
	if !strings.Contains(string(raw3), "CONNECTOR_OAUTH_STATE_UNKNOWN") {
		t.Errorf("forged-state body = %s, want CONNECTOR_OAUTH_STATE_UNKNOWN", raw3)
	}
}

// writeConnectorProviderCA writes the provider's self-signed certificate
// to a PEM file so the gateway's connector-OAuth HTTP client trusts the
// TLS token endpoint. httptest's leaf certificate is self-signed, so it
// is its own CA.
func writeConnectorProviderCA(t *testing.T, provider *httptest.Server) string {
	t.Helper()
	caPath := filepath.Join(t.TempDir(), "provider-ca.pem")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: provider.Certificate().Raw})
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		t.Fatalf("write provider CA: %v", err)
	}
	return caPath
}

// connectorOAuthAuthorize starts a flow for connector id on the gateway at
// base and returns the flow's state.
func connectorOAuthAuthorize(t *testing.T, base, id string) string {
	t.Helper()
	code, raw := deletionAdminDo(t, base, http.MethodPost, "/v1/admin/connectors/"+id+"/oauth/authorize", nil)
	if code != http.StatusOK {
		t.Fatalf("authorize on %s: status %d: %s", base, code, raw)
	}
	var out struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.State == "" {
		t.Fatalf("authorize on %s: no state in %s (%v)", base, raw, err)
	}
	return out.State
}

// connectorOAuthCallback delivers the provider redirect to the gateway at
// base, with no credentials, as the browser delivers it.
func connectorOAuthCallback(t *testing.T, base, state string) (int, string) {
	t.Helper()
	resp, err := http.Get(base + "/v1/admin/connectors/oauth/callback?code=auth-code-xyz&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatalf("callback on %s: %v", base, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// spec: 9.3 (Connector Definition and OAuth/OIDC) "`state` parameter (anti-CSRF)", 10.1.1 (Stateless Replicas and Per-Session Coordination), 12.4 (Redis HA and Failure Modes)
// diagnosis: a callback refused on replica B means the flow still depends
// on a value held in the process that ran authorize, such as a per-process
// state signature, or the state store is not the shared Redis store. A
// refusal on the replacement gateway means the same for a restart. A
// replay that is not CONNECTOR_OAUTH_STATE_REPLAYED means the Redis
// tombstone does not reach the other replica.
func TestConnectorOAuthCompletesAcrossGatewayProcesses_spec_9_3(t *testing.T) {
	t.Parallel()
	gateway.SkipUnlessAvailable(t)

	var exchanges atomic.Int64
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		exchanges.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "provider-access-token", "token_type": "Bearer"})
	}))
	defer provider.Close()

	pg := sharedDatabase(t)
	rd := containers.StartRedis(t, containers.RedisOptions{})
	keyFile := filepath.Join(t.TempDir(), "master.key")
	// Create the master key before any replica starts, so concurrent first
	// use cannot write two different keys to one path.
	if _, err := kms.LoadOrCreateMasterKey(keyFile); err != nil {
		t.Fatalf("create master key: %v", err)
	}
	// The connector registry and the state store are per process without
	// --postgres-dsn and --redis-url.
	args := append(keySetReplicaArgs(pg.DSN, keyFile),
		"--redis-url=redis://"+rd.Addr+"/0",
		"--connector-oauth-callback-url=http://callback.acme.test/v1/admin/connectors/oauth/callback",
		"--connector-oauth-ca="+writeConnectorProviderCA(t, provider))
	gwA := gateway.StartWith(t, args...)
	gwB := gateway.StartWith(t, args...)

	code, raw := deletionAdminDo(t, gwA.BaseURL(), http.MethodPost, "/v1/admin/connectors", map[string]any{
		"id":           "github",
		"displayName":  "GitHub",
		"mcpServerUrl": "https://mcp.github.test",
		"transport":    "streamable_http",
		"auth": map[string]any{
			"type":                  "oauth2",
			"authorizationEndpoint": "https://github.test/login/oauth/authorize",
			"tokenEndpoint":         provider.URL + "/token",
			"clientId":              "lenny-client-id",
		},
	})
	if code != http.StatusCreated {
		t.Fatalf("create connector: status %d: %s\n%s", code, raw, gwA.Logs())
	}

	state := connectorOAuthAuthorize(t, gwA.BaseURL(), "github")
	if code, body := connectorOAuthCallback(t, gwB.BaseURL(), state); code != http.StatusOK {
		t.Fatalf("callback on replica B: status %d: %s\n%s", code, body, gwB.Logs())
	}
	if got := exchanges.Load(); got != 1 {
		t.Fatalf("provider token exchanges after the peer callback = %d, want 1", got)
	}
	code, body := connectorOAuthCallback(t, gwA.BaseURL(), state)
	if code != http.StatusBadRequest || !strings.Contains(body, "CONNECTOR_OAUTH_STATE_REPLAYED") {
		t.Errorf("replay on replica A: status %d: %s, want 400 CONNECTOR_OAUTH_STATE_REPLAYED", code, body)
	}
	if got := exchanges.Load(); got != 1 {
		t.Errorf("provider token exchanges after the replay = %d, want 1", got)
	}

	// A gateway started after A stops completes a flow A began.
	pending := connectorOAuthAuthorize(t, gwA.BaseURL(), "github")
	gwA.Stop(t)
	gwC := gateway.StartWith(t, args...)
	if code, body := connectorOAuthCallback(t, gwC.BaseURL(), pending); code != http.StatusOK {
		t.Fatalf("callback on the replacement gateway: status %d: %s\n%s", code, body, gwC.Logs())
	}
}
