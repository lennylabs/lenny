// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests for the §27.3.1 OIDC login, callback, and error-page
// responses over the shared `state` store. They pin what a browser receives:
// the opaque state cookie, the JSON 503 a login store fault answers, the
// redirect a callback store fault answers, and the 503 the error page renders
// for it. The harness composes the real playground handler with a stub
// exchanger and a store whose Put and Consume can fail.

package playground_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/connectoroauth"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"
	"github.com/lennylabs/lenny/tests/testinfra/jwtsign"
)

const oidcStateCookieName = "lenny_playground_oidc_state"

// stubExchanger is a playground.OIDCExchanger that needs no provider.
type stubExchanger struct{}

func (stubExchanger) AuthorizationURL(_ context.Context, state, challenge, _ string) (string, error) {
	return "https://idp.acme.com/authorize?state=" + url.QueryEscape(state) + "&code_challenge=" + url.QueryEscape(challenge), nil
}

func (stubExchanger) Exchange(context.Context, string, string, string) (playground.OIDCSubject, error) {
	return playground.OIDCSubject{UserID: "alice", TenantID: "acme", Scope: "tools:sessions:read"}, nil
}

// faultableStateStore fails Put and Consume on demand over a memory store.
type faultableStateStore struct {
	*connectoroauth.MemoryStateStore
	putErr, consumeErr error
}

func (s *faultableStateStore) Put(state string, fc connectoroauth.FlowContext, ttl time.Duration) error {
	if s.putErr != nil {
		return s.putErr
	}
	return s.MemoryStateStore.Put(state, fc, ttl)
}

func (s *faultableStateStore) Consume(state string, now time.Time) (connectoroauth.FlowContext, error) {
	if s.consumeErr != nil {
		return connectoroauth.FlowContext{}, s.consumeErr
	}
	return s.MemoryStateStore.Consume(state, now)
}

// oidcContractServer serves an oidc-mode handler over a faultable store.
func oidcContractServer(t *testing.T) (*httptest.Server, *faultableStateStore) {
	t.Helper()
	store := &faultableStateStore{MemoryStateStore: connectoroauth.NewMemoryStateStore()}
	h := playground.New(playground.Config{
		Enabled:            true,
		AuthMode:           playground.AuthModeOIDC,
		CapabilityTokenTTL: 900 * time.Second,
		OIDCSessionTTL:     time.Hour,
	}, playground.Options{
		Signer:     jwtsign.HMAC(devSigner()),
		Verifier:   devSigner(),
		Tenants:    fakeTenants{registered: map[string]bool{"acme": true}},
		Sessions:   playground.NewMemorySessionStore(),
		OIDC:       stubExchanger{},
		OAuthState: store,
	})
	srv := httptest.NewServer(h.PlaygroundRoutes())
	t.Cleanup(srv.Close)
	return srv, store
}

// oidcContractGet sends one GET without following redirects.
func oidcContractGet(t *testing.T, target string, cookies ...*http.Cookie) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

func oidcStateCookie(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == oidcStateCookieName {
			return c
		}
	}
	return nil
}

// spec: 27.3.1 (OIDC cookie-to-MCP-bearer exchange) `GET /playground/auth/login`
// diagnosis: The login response does not carry the §27.3.1 cookie contract.
// A cookie value that differs from the redirect's state parameter means the
// cookie still carries more than the opaque state, or the callback comparison
// cannot match. A changed attribute widens the cookie's reach: inspect
// handleLogin in pkg/gateway/mcpfabric/playground/auth.go.
func TestOIDCLoginCookieIsTheOpaqueState_spec_27_3_1(t *testing.T) {
	srv, _ := oidcContractServer(t)
	resp, raw := oidcContractGet(t, srv.URL+"/playground/auth/login")
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("login status = %d, want 302; body=%s", resp.StatusCode, raw)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	cookie := oidcStateCookie(resp)
	if cookie == nil {
		t.Fatal("login set no state cookie")
	}
	if state := loc.Query().Get("state"); state == "" || cookie.Value != state {
		t.Fatalf("cookie value = %q, want the redirect state %q", cookie.Value, state)
	}
	if cookie.Path != "/playground/auth/" || !cookie.HttpOnly || !cookie.Secure ||
		cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge != 600 {
		t.Fatalf("cookie attributes = %+v, want Path=/playground/auth/, HttpOnly, Secure, SameSite=Lax, and Max-Age=600", cookie)
	}
}

// spec: 27.3.1 (OIDC cookie-to-MCP-bearer exchange) Failure modes summary "Redis unavailable at `GET /playground/auth/login`"
// diagnosis: A state store write failure at login did not answer the JSON 503
// REDIS_UNAVAILABLE envelope with no cookie. A 302 means the handler redirected
// with no stored verifier, which fails open toward a callback that can never
// complete. Check the order of the Put and SetCookie calls in handleLogin.
func TestOIDCLoginStoreFaultAnswersRedisUnavailable_spec_27_3_1(t *testing.T) {
	srv, store := oidcContractServer(t)
	store.putErr = errors.New("store unreachable")
	resp, raw := oidcContractGet(t, srv.URL+"/playground/auth/login")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("login status = %d, want 503; body=%s", resp.StatusCode, raw)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || body.Error.Code != "REDIS_UNAVAILABLE" {
		t.Fatalf("login body = %s (decode err %v), want code REDIS_UNAVAILABLE", raw, err)
	}
	if c := oidcStateCookie(resp); c != nil {
		t.Fatalf("a failed login set the state cookie: %+v", c)
	}
}

// spec: 27.3.1 (OIDC cookie-to-MCP-bearer exchange) `GET /playground/auth/callback` "A `state` store fault does the same with `error=oidc_state_unavailable`"
// diagnosis: A store fault at the callback did not redirect to the error page
// with oidc_state_unavailable. A JSON body here reaches a browser that cannot
// render it, and oidc_state_invalid names an outage as a bad state. Check the
// default arm of consumeLoginState in pkg/gateway/mcpfabric/playground/auth.go.
func TestOIDCCallbackStoreFaultRedirectsToUnavailable_spec_27_3_1(t *testing.T) {
	srv, store := oidcContractServer(t)
	login, _ := oidcContractGet(t, srv.URL+"/playground/auth/login")
	cookie := oidcStateCookie(login)
	if cookie == nil {
		t.Fatal("login set no state cookie")
	}
	store.consumeErr = errors.New("store unreachable")
	resp, raw := oidcContractGet(t, srv.URL+"/playground/auth/callback?code=c&state="+url.QueryEscape(cookie.Value), cookie)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("callback status = %d, want 302; body=%s", resp.StatusCode, raw)
	}
	if got, want := resp.Header.Get("Location"), "/playground/auth/error?error=oidc_state_unavailable"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}

// spec: 27.3.1 (OIDC cookie-to-MCP-bearer exchange) `GET /playground/auth/callback` "the HTTP status on the final response to the browser is 503"
// diagnosis: The error page status does not follow the §27.3.1 contract. A 200
// for oidc_state_unavailable reports an outage as a rendered success to any
// probe. A 503 for oidc_state_invalid reports a bad state as an outage. Check
// handleAuthError in pkg/gateway/mcpfabric/playground/auth.go.
func TestOIDCErrorPageStatus_spec_27_3_1(t *testing.T) {
	srv, _ := oidcContractServer(t)
	for code, want := range map[string]int{
		"oidc_state_unavailable": http.StatusServiceUnavailable,
		"oidc_state_invalid":     http.StatusOK,
	} {
		resp, _ := oidcContractGet(t, srv.URL+"/playground/auth/error?error="+code)
		if resp.StatusCode != want {
			t.Errorf("error=%s status = %d, want %d", code, resp.StatusCode, want)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("error=%s Content-Type = %q, want the HTML page", code, ct)
		}
	}
}
