//go:build security

// SPDX-License-Identifier: MIT

// Tier-9 coverage for the OAuth `state` binding of the §27.3.1 playground login
// and the §9.3 connector flow, which share one Redis store. A playground
// gateway and a connector gateway run as real cmd/lenny-gateway processes on
// one Redis, so an entry one flow wrote is visible to the other flow's
// callback. Each case drives the real routes and asserts on the session cookie
// or on the provider token endpoint, because a refusal that ran after the code
// exchange or that burned the entry is the failure these bindings prevent.
package tier9_security_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	oidcstub "github.com/lennylabs/lenny/tests/testinfra/stubs/oidc"
)

const (
	bindingStateCookie   = "lenny_playground_oidc_state"
	bindingSessionCookie = "lenny_playground_session"
	bindingConnector     = "github"
)

// bindingStack is a playground gateway and a connector gateway on one Redis,
// and a provider token endpoint that counts its exchanges.
type bindingStack struct {
	playground *gateway.Process
	connector  *gateway.Process
	exchanges  *atomic.Int64
}

func startBindingStack(t *testing.T) *bindingStack {
	t.Helper()
	gateway.SkipUnlessAvailable(t)
	idp := oidcstub.New(t)
	idp.SetLoginIdentity(oidcstub.MintOptions{Subject: "alice@acme.com", TenantID: "default"})
	rd := containers.StartRedis(t, containers.RedisOptions{})
	redisFlag := "--redis-url=redis://" + rd.Addr + "/0"

	exchanges := &atomic.Int64{}
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		exchanges.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "provider-access-token", "token_type": "Bearer"})
	}))
	t.Cleanup(provider.Close)
	caPath := filepath.Join(t.TempDir(), "provider-ca.pem")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: provider.Certificate().Raw})
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		t.Fatalf("write provider CA: %v", err)
	}

	s := &bindingStack{exchanges: exchanges}
	s.playground = gateway.StartWith(
		t,
		"--no-environment-policy", "allow-all",
		"--dev-mode=false",
		"--oidc-issuer-url="+idp.Issuer(),
		"--oidc-client-id=lenny-gateway",
		"--playground-enabled",
		"--playground-auth-mode=oidc",
		redisFlag,
	)
	s.connector = gateway.StartWith(
		t,
		"--no-environment-policy", "allow-all", "--dev-mode",
		"--connector-oauth-callback-url", "http://callback.acme.test/v1/admin/connectors/oauth/callback",
		"--connector-oauth-ca", caPath,
		redisFlag,
	)
	code, raw := s.connectorAdmin(t, http.MethodPost, "/v1/admin/connectors", map[string]any{
		"id":           bindingConnector,
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
		t.Fatalf("create connector: status %d: %s\n%s", code, raw, s.connector.Logs())
	}
	return s
}

// bindingHop sends one GET without following redirects and returns the response
// with its body read.
func bindingHop(t *testing.T, target string, cookies ...*http.Cookie) (*http.Response, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("new request %s: %v", target, err)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, string(raw)
}

// bindingCookie returns the non-empty cookie of resp with name, or nil.
func bindingCookie(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name && c.Value != "" {
			return c
		}
	}
	return nil
}

// playgroundLogin is one login with its IdP authorization step behind it.
type playgroundLogin struct {
	cookie   *http.Cookie
	callback string
}

func (s *bindingStack) login(t *testing.T) playgroundLogin {
	t.Helper()
	login, body := bindingHop(t, s.playground.BaseURL()+"/playground/auth/login")
	if login.StatusCode != http.StatusFound {
		t.Fatalf("login: status %d (%s)\n%s", login.StatusCode, body, s.playground.Logs())
	}
	cookie := bindingCookie(login, bindingStateCookie)
	if cookie == nil {
		t.Fatal("login set no state cookie")
	}
	authz, _ := bindingHop(t, login.Header.Get("Location"))
	back, err := url.Parse(authz.Header.Get("Location"))
	if err != nil || back.Path != "/playground/auth/callback" {
		t.Fatalf("IdP redirected to %q, want the gateway callback", authz.Header.Get("Location"))
	}
	return playgroundLogin{cookie: cookie, callback: s.playground.BaseURL() + back.Path + "?" + back.RawQuery}
}

// wantNoSession fails unless the callback established no session and redirected
// to the error page with code.
func wantNoSession(t *testing.T, what string, resp *http.Response, code string) {
	t.Helper()
	if bindingCookie(resp, bindingSessionCookie) != nil {
		t.Errorf("%s established a session", what)
	}
	if got := resp.Header.Get("Location"); !strings.Contains(got, code) {
		t.Errorf("%s: status %d Location %q, want a redirect to %s", what, resp.StatusCode, got, code)
	}
}

// connectorAdmin sends one admin request to the connector gateway.
func (s *bindingStack) connectorAdmin(t *testing.T, method, path string, body any) (int, string) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, s.connector.BaseURL()+path, reader)
	if err != nil {
		t.Fatalf("new request %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// authorize starts a connector flow and returns its state.
func (s *bindingStack) authorize(t *testing.T) string {
	t.Helper()
	code, raw := s.connectorAdmin(t, http.MethodPost, "/v1/admin/connectors/"+bindingConnector+"/oauth/authorize", nil)
	if code != http.StatusOK {
		t.Fatalf("authorize: status %d: %s", code, raw)
	}
	var out struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out.State == "" {
		t.Fatalf("authorize: no state in %s (%v)", raw, err)
	}
	return out.State
}

// connectorCallback delivers the provider redirect with no credentials, as the
// browser delivers it.
func (s *bindingStack) connectorCallback(t *testing.T, state string) (int, string) {
	t.Helper()
	resp, body := bindingHop(t, s.connector.BaseURL()+"/v1/admin/connectors/oauth/callback?code=auth-code-xyz&state="+url.QueryEscape(state))
	return resp.StatusCode, body
}

// spec: 27.3.1 (OIDC cookie-to-MCP-bearer exchange) "The cookie binds the" login to the browser and "so each `state` is single use"
// diagnosis: a session on a callback with no cookie or with another login's
// cookie means the cookie comparison no longer guards the callback, which opens
// login CSRF. A matching callback that then fails means a refused callback
// consumed the entry or redeemed the code, so the comparison runs after the
// consume or the exchange: inspect the order in handleCallback in
// pkg/gateway/mcpfabric/playground/auth.go. A session on the replay means the
// store entry is not single use.
func TestPlaygroundCallbackBindsStateToTheBrowser_spec_27_3_1(t *testing.T) {
	s := startBindingStack(t)
	victim := s.login(t)
	other := s.login(t)

	noCookie, _ := bindingHop(t, victim.callback)
	wantNoSession(t, "callback with no cookie", noCookie, "oidc_state_missing")
	foreign, _ := bindingHop(t, victim.callback, other.cookie)
	wantNoSession(t, "callback with another login's cookie", foreign, "oidc_state_mismatch")

	match, body := bindingHop(t, victim.callback, victim.cookie)
	if match.StatusCode != http.StatusFound || bindingCookie(match, bindingSessionCookie) == nil {
		t.Fatalf("matching callback: status %d Location %q (%s), want a session\n%s",
			match.StatusCode, match.Header.Get("Location"), body, s.playground.Logs())
	}
	replay, _ := bindingHop(t, victim.callback, victim.cookie)
	wantNoSession(t, "replayed state", replay, "oidc_state_invalid")
}

// spec: 27.3.1 (OIDC cookie-to-MCP-bearer exchange) "or an entry a connector", 9.3 (Connector Definition and OAuth/OIDC) "`state` parameter (anti-CSRF)", 12.4 (Redis HA and Failure Modes)
// diagnosis: oidc_exchange_failed at the playground callback or a token
// exchange count above zero at the connector callback means an entry of the
// other flow reached a provider exchange, so the flow-kind check on the
// consumed entry is missing. An exchange on a forged state means the connector
// callback no longer treats StateStore.Consume as its single authority. A
// pending flow that fails after the forged callback means the forged request
// disturbed a stored entry.
func TestOAuthStateRefusedOutsideItsFlow_spec_9_3(t *testing.T) {
	s := startBindingStack(t)

	t.Run("a connector entry at the playground callback", func(t *testing.T) {
		state := s.authorize(t)
		// The code was never issued, so a callback that reached the exchange
		// answers oidc_exchange_failed rather than the refusal below.
		target := s.playground.BaseURL() + "/playground/auth/callback?code=unissued&state=" + url.QueryEscape(state)
		resp, _ := bindingHop(t, target, &http.Cookie{Name: bindingStateCookie, Value: state})
		wantNoSession(t, "connector entry", resp, "oidc_state_invalid")
	})

	t.Run("a playground entry at the connector callback", func(t *testing.T) {
		login := s.login(t)
		code, body := s.connectorCallback(t, login.cookie.Value)
		if code != http.StatusBadRequest || !strings.Contains(body, "CONNECTOR_OAUTH_INVALID_CALLBACK") {
			t.Errorf("playground entry: status %d: %s, want 400 CONNECTOR_OAUTH_INVALID_CALLBACK", code, body)
		}
		if n := s.exchanges.Load(); n != 0 {
			t.Errorf("provider token exchanges = %d, want 0", n)
		}
	})

	t.Run("a forged state while a flow is pending", func(t *testing.T) {
		pending := s.authorize(t)
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			t.Fatalf("draw forged state: %v", err)
		}
		code, body := s.connectorCallback(t, hex.EncodeToString(raw))
		if code != http.StatusBadRequest || !strings.Contains(body, "CONNECTOR_OAUTH_STATE_UNKNOWN") {
			t.Errorf("forged state: status %d: %s, want 400 CONNECTOR_OAUTH_STATE_UNKNOWN", code, body)
		}
		if n := s.exchanges.Load(); n != 0 {
			t.Errorf("provider token exchanges after the forged state = %d, want 0", n)
		}
		if code, body := s.connectorCallback(t, pending); code != http.StatusOK {
			t.Fatalf("pending flow: status %d: %s\n%s", code, body, s.connector.Logs())
		}
		if n := s.exchanges.Load(); n != 1 {
			t.Errorf("provider token exchanges after the pending flow = %d, want 1", n)
		}
	})
}
