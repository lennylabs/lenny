//go:build chaos

// SPDX-License-Identifier: MIT

// Tier-8 chaos coverage for the §27.3.1 playground OIDC state under a Redis
// outage, against a real cmd/lenny-gateway process. The gateway dials Redis
// through a tcpfault proxy, because stopping a Redis container loses its data
// and its address. With the proxy cut, a login answers 503 REDIS_UNAVAILABLE
// and a callback redirects to oidc_state_unavailable, whose page renders 503.
// After the proxy returns, with no gateway restart, a new login completes, and
// so does the callback of a login begun before the cut.
package tier8_chaos_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	oidcstub "github.com/lennylabs/lenny/tests/testinfra/stubs/oidc"
	"github.com/lennylabs/lenny/tests/testinfra/tcpfault"
)

const (
	pgRedisStateCookie   = "lenny_playground_oidc_state"
	pgRedisSessionCookie = "lenny_playground_session"
	// pgRedisHopTimeout bounds one request, including the Redis client's
	// dial and retry budget during the cut.
	pgRedisHopTimeout = 30 * time.Second
	// pgRedisRecoveryBound bounds the Redis client's reconnect after Restore.
	pgRedisRecoveryBound = 30 * time.Second
)

// pgRedisHop sends one GET without following redirects and returns the response
// with its body read.
func pgRedisHop(t *testing.T, target string, cookies ...*http.Cookie) (*http.Response, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), pgRedisHopTimeout)
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

// pgRedisCookie returns the non-empty cookie of resp with name, or nil.
func pgRedisCookie(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name && c.Value != "" {
			return c
		}
	}
	return nil
}

// pgRedisLogin is one login with the authorization step behind it: the state
// cookie and the callback target the IdP redirected to.
type pgRedisLogin struct {
	state    *http.Cookie
	callback string
}

// beginPgRedisLogin runs a login and its IdP authorization step.
func beginPgRedisLogin(t *testing.T, gw *gateway.Process) pgRedisLogin {
	t.Helper()
	login, body := pgRedisHop(t, gw.BaseURL()+"/playground/auth/login")
	if login.StatusCode != http.StatusFound {
		t.Fatalf("login: status %d (%s)\ngateway logs:\n%s", login.StatusCode, body, gw.Logs())
	}
	state := pgRedisCookie(login, pgRedisStateCookie)
	if state == nil {
		t.Fatal("login set no state cookie")
	}
	authz, _ := pgRedisHop(t, login.Header.Get("Location"))
	back, err := url.Parse(authz.Header.Get("Location"))
	if err != nil || back.Path != "/playground/auth/callback" {
		t.Fatalf("IdP redirected to %q, want the gateway callback", authz.Header.Get("Location"))
	}
	return pgRedisLogin{state: state, callback: gw.BaseURL() + back.Path + "?" + back.RawQuery}
}

// waitPgRedisLogin retries a login until Redis answers it again.
func waitPgRedisLogin(t *testing.T, gw *gateway.Process) pgRedisLogin {
	t.Helper()
	deadline := time.Now().Add(pgRedisRecoveryBound)
	for {
		login, body := pgRedisHop(t, gw.BaseURL()+"/playground/auth/login")
		if login.StatusCode == http.StatusFound {
			return beginPgRedisLogin(t, gw)
		}
		if time.Now().After(deadline) {
			t.Fatalf("login after Restore: status %d (%s)\ngateway logs:\n%s", login.StatusCode, body, gw.Logs())
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// completePgRedisLogin delivers the callback and fails unless it establishes a
// session.
func completePgRedisLogin(t *testing.T, what string, l pgRedisLogin) {
	t.Helper()
	callback, body := pgRedisHop(t, l.callback, l.state)
	if callback.StatusCode != http.StatusFound || pgRedisCookie(callback, pgRedisSessionCookie) == nil {
		t.Errorf("%s: status %d Location %q (%s), want a session", what, callback.StatusCode, callback.Header.Get("Location"), body)
	}
}

// spec: 27.3.1 (OIDC cookie-to-MCP-bearer exchange) "Redis unavailability." and Failure modes summary, 12.4 (Redis HA and Failure Modes)
// diagnosis: a login that answers anything but 503 REDIS_UNAVAILABLE during the
// cut means the store fault is reported as a provider fault, or the login does
// not write the shared store. A callback that does not redirect to
// oidc_state_unavailable means a store fault is reported as a bad state, and a
// page that renders 200 means handleAuthError lost its 503. A login that does
// not recover after Restore means the gateway keeps a broken Redis client. A
// pre-cut callback that fails after Restore means the entry did not survive
// the outage or the cut callback consumed it.
func TestPlaygroundOIDCStateFailsClosedAcrossRedisOutage_spec_27_3_1(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	idp := oidcstub.New(t)
	idp.SetLoginIdentity(oidcstub.MintOptions{Subject: "alice@acme.com", TenantID: "default"})
	rd := containers.StartRedis(t, containers.RedisOptions{})
	proxy := tcpfault.Start(t, rd.Addr)
	gw := gateway.StartWith(
		t,
		"--no-environment-policy", "allow-all",
		"--dev-mode=false",
		"--oidc-issuer-url="+idp.Issuer(),
		"--oidc-client-id=lenny-gateway",
		"--playground-enabled",
		"--playground-auth-mode=oidc",
		"--redis-url=redis://"+proxy.Addr()+"/0",
	)

	cutCallback := beginPgRedisLogin(t, gw)
	preCut := beginPgRedisLogin(t, gw)

	proxy.Cut()
	login, body := pgRedisHop(t, gw.BaseURL()+"/playground/auth/login")
	if login.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, "REDIS_UNAVAILABLE") {
		t.Errorf("login during the cut: status %d (%s), want 503 REDIS_UNAVAILABLE", login.StatusCode, body)
	}
	if pgRedisCookie(login, pgRedisStateCookie) != nil {
		t.Error("login during the cut set a state cookie")
	}

	callback, _ := pgRedisHop(t, cutCallback.callback, cutCallback.state)
	location := callback.Header.Get("Location")
	if callback.StatusCode != http.StatusFound || !strings.Contains(location, "oidc_state_unavailable") {
		t.Errorf("callback during the cut: status %d Location %q, want a redirect to oidc_state_unavailable", callback.StatusCode, location)
	}
	if strings.HasPrefix(location, "/") {
		page, _ := pgRedisHop(t, gw.BaseURL()+location)
		if page.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("error page during the cut: status %d, want 503", page.StatusCode)
		}
	}

	proxy.Restore(t)
	completePgRedisLogin(t, "login begun after Restore", waitPgRedisLogin(t, gw))
	completePgRedisLogin(t, "login begun before the cut", preCut)
}
