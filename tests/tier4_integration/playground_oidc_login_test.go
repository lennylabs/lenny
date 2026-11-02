// SPDX-License-Identifier: MIT

//go:build integration

package tier4_integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/gatewaypki"
	oidcstub "github.com/lennylabs/lenny/tests/testinfra/stubs/oidc"
)

// playgroundHop sends one request without following redirects and returns
// the response with its body read. A non-empty host replaces the request's
// Host, as an ingress in front of several replicas presents one host to all of
// them.
func playgroundHop(t *testing.T, method, target, host string, cookies []*http.Cookie, body string) (*http.Response, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, target, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request %s: %v", target, err)
	}
	if host != "" {
		req.Host = host
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, string(raw)
}

// cookieNamed returns the cookie of resp with name, or nil.
func cookieNamed(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name && c.Value != "" {
			return c
		}
	}
	return nil
}

// spec: 27.3 (Authentication) the `playground.authMode=oidc` bullet, 27.3.1
// (OIDC cookie-to-MCP-bearer exchange) `GET /playground/auth/login`,
// `GET /playground/auth/callback`, and `POST /v1/playground/token`
// diagnosis: a 503 LENNY_PLAYGROUND_OIDC_UNAVAILABLE from login or callback
// means the live cmd/lenny-gateway builds the playground with no
// authorization-code exchanger: check that buildHTTPSurface passes
// buildPlaygroundOIDC's exchanger as playground.Options.OIDC. A login
// redirect to anything but the stub's discovered authorization endpoint
// means the exchanger does not read the --oidc-issuer-url discovery
// document. A callback that redirects to the error page means the code
// exchange or the ID-token validation failed; the error query parameter
// names which. A cookie mint other than 200 means the session record the
// callback wrote does not admit the cookie.
func TestLivePlaygroundOIDCModeSignsInThroughTheIdP_spec_27_3_1(t *testing.T) {
	t.Parallel()
	gateway.SkipUnlessAvailable(t)
	idp := oidcstub.New(t)
	idp.SetLoginIdentity(oidcstub.MintOptions{Subject: "alice@acme.com", TenantID: "default"})
	gw := gateway.StartWith(
		t,
		"--no-environment-policy", "allow-all",
		"--dev-mode=false",
		"--oidc-issuer-url="+idp.Issuer(),
		"--oidc-client-id=lenny-gateway",
		"--playground-enabled",
		"--playground-auth-mode=oidc",
	)

	login, body := playgroundHop(t, http.MethodGet, gw.BaseURL()+"/playground/auth/login", "", nil, "")
	if login.StatusCode != http.StatusFound {
		t.Fatalf("login: status %d (%s)\ngateway logs:\n%s", login.StatusCode, body, gw.Logs())
	}
	authURL := login.Header.Get("Location")
	if !strings.HasPrefix(authURL, idp.Issuer()+"/authorize?") {
		t.Fatalf("login redirected to %s, want the IdP's discovered authorization endpoint", authURL)
	}
	state := cookieNamed(login, "lenny_playground_oidc_state")
	if state == nil {
		t.Fatal("login set no lenny_playground_oidc_state cookie")
	}

	// The IdP redirects to the callback URL the gateway derived from the
	// request host; the test sends it to the gateway's plain-HTTP listener,
	// which an ingress fronts in production.
	authz, _ := playgroundHop(t, http.MethodGet, authURL, "", nil, "")
	back, err := url.Parse(authz.Header.Get("Location"))
	if err != nil || back.Path != "/playground/auth/callback" {
		t.Fatalf("IdP redirected to %q, want the gateway callback", authz.Header.Get("Location"))
	}
	callback, body := playgroundHop(t, http.MethodGet, gw.BaseURL()+back.Path+"?"+back.RawQuery, "", []*http.Cookie{state}, "")
	session := cookieNamed(callback, "lenny_playground_session")
	if callback.StatusCode != http.StatusFound || session == nil {
		t.Fatalf("callback: status %d Location %q session cookie %v (%s)\ngateway logs:\n%s",
			callback.StatusCode, callback.Header.Get("Location"), session, body, gw.Logs())
	}

	mint, body := playgroundHop(t, http.MethodPost, gw.BaseURL()+"/v1/playground/token", "", []*http.Cookie{session}, "{}")
	if mint.StatusCode != http.StatusOK {
		t.Fatalf("cookie mint: status %d (%s)", mint.StatusCode, body)
	}
	var minted struct {
		BearerToken string `json:"bearerToken"`
	}
	if err := json.Unmarshal([]byte(body), &minted); err != nil || minted.BearerToken == "" {
		t.Errorf("cookie mint returned no bearerToken (decode err %v): %s", err, body)
	}
}

// playgroundIngressHost is the one host a browser addresses, whichever replica
// the ingress routes a request to. callbackURL derives redirect_uri from the
// request host, and the OIDC stub refuses a redemption whose redirect_uri
// differs from the authorization request, so a login and its callback on two
// replicas carry the same host.
const playgroundIngressHost = "playground.acme.test"

// spec: 27.3.1 (OIDC cookie-to-MCP-bearer exchange) `GET /playground/auth/login` and `GET /playground/auth/callback`, 10.1.1 (Stateless Replicas and Per-Session Coordination)
// diagnosis: a callback on replica B that redirects to oidc_state_invalid
// means the login's entry is not in the shared Redis store, so the playground
// still depends on a value held in the process that ran the login. A redirect
// to oidc_exchange_failed means the stored PKCE verifier or the redirect_uri
// differs between the two replicas. A replay that establishes a session means
// the entry is not single use across replicas.
func TestPlaygroundOIDCLoginCompletesAcrossGatewayProcesses_spec_27_3_1(t *testing.T) {
	t.Parallel()
	gateway.SkipUnlessAvailable(t)
	idp := oidcstub.New(t)
	idp.SetLoginIdentity(oidcstub.MintOptions{Subject: "alice@acme.com", TenantID: "default"})
	rd := containers.StartRedis(t, containers.RedisOptions{})
	args := append([]string{
		"--no-environment-policy", "allow-all",
		"--dev-mode=false",
		"--oidc-issuer-url=" + idp.Issuer(),
		"--oidc-client-id=lenny-gateway",
		"--playground-enabled",
		"--playground-auth-mode=oidc",
		"--redis-url=redis://" + rd.Addr + "/0",
	}, gatewaypki.Mint(t).Flags()...)
	gwA := gateway.StartWith(t, args...)
	gwB := gateway.StartWith(t, args...)

	login, body := playgroundHop(t, http.MethodGet, gwA.BaseURL()+"/playground/auth/login", playgroundIngressHost, nil, "")
	if login.StatusCode != http.StatusFound {
		t.Fatalf("login on replica A: status %d (%s)\ngateway logs:\n%s", login.StatusCode, body, gwA.Logs())
	}
	state := cookieNamed(login, "lenny_playground_oidc_state")
	if state == nil {
		t.Fatal("login set no lenny_playground_oidc_state cookie")
	}
	authz, _ := playgroundHop(t, http.MethodGet, login.Header.Get("Location"), "", nil, "")
	back, err := url.Parse(authz.Header.Get("Location"))
	if err != nil || back.Path != "/playground/auth/callback" {
		t.Fatalf("IdP redirected to %q, want the gateway callback", authz.Header.Get("Location"))
	}
	callbackTarget := func(gw *gateway.Process) string { return gw.BaseURL() + back.Path + "?" + back.RawQuery }

	callback, body := playgroundHop(t, http.MethodGet, callbackTarget(gwB), playgroundIngressHost, []*http.Cookie{state}, "")
	session := cookieNamed(callback, "lenny_playground_session")
	if callback.StatusCode != http.StatusFound || session == nil {
		t.Fatalf("callback on replica B: status %d Location %q session cookie %v (%s)\ngateway logs:\n%s",
			callback.StatusCode, callback.Header.Get("Location"), session, body, gwB.Logs())
	}
	mint, body := playgroundHop(t, http.MethodPost, gwA.BaseURL()+"/v1/playground/token", playgroundIngressHost, []*http.Cookie{session}, "{}")
	if mint.StatusCode != http.StatusOK {
		t.Errorf("cookie mint on replica A: status %d (%s)", mint.StatusCode, body)
	}

	replay, _ := playgroundHop(t, http.MethodGet, callbackTarget(gwA), playgroundIngressHost, []*http.Cookie{state}, "")
	if cookieNamed(replay, "lenny_playground_session") != nil {
		t.Error("a replayed state established a session on replica A")
	}
	if got := replay.Header.Get("Location"); !strings.Contains(got, "oidc_state_invalid") {
		t.Errorf("replay on replica A redirected to %q, want oidc_state_invalid", got)
	}
}
