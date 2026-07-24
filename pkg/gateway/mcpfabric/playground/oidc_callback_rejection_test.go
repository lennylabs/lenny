// SPDX-License-Identifier: MIT

package playground

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// noRedirectClient returns an *http.Client that does not follow
// redirects, so a 302 response is directly observable.
func noRedirectClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

// startOIDCLogin drives GET /playground/auth/login and returns the
// state cookie the gateway set together with the state value recovered
// from it, so a callback test can present a matching or mismatched
// state parameter.
func startOIDCLogin(t *testing.T, h *Handler, srv *httptest.Server, client *http.Client) (*http.Cookie, stateCookieValue) {
	t.Helper()
	resp, err := client.Get(srv.URL + "/playground/auth/login")
	if err != nil {
		t.Fatalf("GET login: %v", err)
	}
	_ = resp.Body.Close()
	var stateCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == oidcStateCookie {
			stateCookie = c
		}
	}
	if stateCookie == nil {
		t.Fatal("login set no state cookie")
	}
	cv, err := h.openState(stateCookie.Value)
	if err != nil {
		t.Fatalf("openState: %v", err)
	}
	return stateCookie, cv
}

// assertCallbackRejected drives GET /playground/auth/callback with the
// supplied query string and state cookie and asserts the §27.3.1
// callback-rejection contract: a 302 redirect to the playground error
// page carrying the wanted code, no lenny_playground_session cookie set
// on the response, and the OIDC state cookie cleared.
//
// spec: §27.3.1 ("when extraction fails the gateway does not
// establish a session record or set lenny_playground_session, and
// instead redirects the browser to the playground error page GET
// /playground/auth/error?error=<code>"; "No lenny_playground_session
// cookie is set for any row; the state cookie is cleared as part of
// the error redirect to prevent dangling PKCE state.")
func assertCallbackRejected(t *testing.T, client *http.Client, srv *httptest.Server, query string, stateCookie *http.Cookie, wantCode string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/playground/auth/callback?"+query, nil)
	if err != nil {
		t.Fatalf("build callback request: %v", err)
	}
	if stateCookie != nil {
		req.AddCookie(stateCookie)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("callback status = %d, want 302", resp.StatusCode)
	}
	wantLocation := "/playground/auth/error?error=" + wantCode
	if loc := resp.Header.Get("Location"); loc != wantLocation {
		t.Fatalf("Location = %q, want %q", loc, wantLocation)
	}

	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie {
			t.Fatalf("callback rejection set a %s cookie: %+v", sessionCookie, c)
		}
	}

	var sawClearedState bool
	for _, c := range resp.Cookies() {
		if c.Name == oidcStateCookie {
			sawClearedState = true
			if c.MaxAge > 0 {
				t.Fatalf("state cookie MaxAge = %d, want <= 0 (cleared)", c.MaxAge)
			}
		}
	}
	if !sawClearedState {
		t.Fatal("callback rejection did not clear the OIDC state cookie")
	}
}

// TestOIDCCallbackStateMismatchRedirectsToErrorPage exercises the
// state-mismatch branch of the OIDC callback: the state query
// parameter does not match the value sealed in the state cookie.
//
// spec: §27.3.1 (the callback "verifies state against the state
// cookie"; rejections redirect to the playground error page, set no
// session cookie, and clear the state cookie).
func TestOIDCCallbackStateMismatchRedirectsToErrorPage(t *testing.T) {
	oidc := &fakeOIDC{}
	h := New(Config{Enabled: true, AuthMode: AuthModeOIDC}, Options{
		Signer:   devSigner(),
		Sessions: NewMemorySessionStore(),
		OIDC:     oidc,
	})
	srv := httptest.NewServer(h.PlaygroundRoutes())
	defer srv.Close()
	client := noRedirectClient()

	stateCookie, _ := startOIDCLogin(t, h, srv, client)
	assertCallbackRejected(t, client, srv, "code=auth-code&state=not-the-real-state", stateCookie, "oidc_state_mismatch")
}

// TestOIDCCallbackExpiredStateRedirectsToErrorPage exercises the
// expired-state branch: the state cookie's IssuedAt predates the
// stateCookieTTL window by the time the callback runs.
//
// spec: §27.3.1 (rejections redirect to the playground error page,
// set no session cookie, and clear the state cookie).
func TestOIDCCallbackExpiredStateRedirectsToErrorPage(t *testing.T) {
	clockAt := time.Now()
	clock := func() time.Time { return clockAt }
	oidc := &fakeOIDC{}
	h := New(Config{Enabled: true, AuthMode: AuthModeOIDC}, Options{
		Signer:   devSigner(),
		Sessions: NewMemorySessionStore(),
		OIDC:     oidc,
		Now:      clock,
	})
	srv := httptest.NewServer(h.PlaygroundRoutes())
	defer srv.Close()
	client := noRedirectClient()

	stateCookie, cv := startOIDCLogin(t, h, srv, client)
	// Advance the injected clock past the state cookie TTL before the
	// callback runs, without needing a wall-clock sleep.
	clockAt = clockAt.Add(stateCookieTTL + time.Minute)
	assertCallbackRejected(t, client, srv, "code=auth-code&state="+cv.State, stateCookie, "oidc_state_expired")
}

// TestOIDCCallbackExchangeFailureRedirectsToErrorPage exercises a
// non-tenant-claim exchange failure (provider token-endpoint error):
// the generic oidc_exchange_failed code is used.
//
// spec: §27.3.1 (rejections redirect to the playground error page,
// set no session cookie, and clear the state cookie).
func TestOIDCCallbackExchangeFailureRedirectsToErrorPage(t *testing.T) {
	oidc := &fakeOIDC{exchangeErr: errors.New("token endpoint unreachable")}
	h := New(Config{Enabled: true, AuthMode: AuthModeOIDC}, Options{
		Signer:   devSigner(),
		Sessions: NewMemorySessionStore(),
		OIDC:     oidc,
	})
	srv := httptest.NewServer(h.PlaygroundRoutes())
	defer srv.Close()
	client := noRedirectClient()

	stateCookie, cv := startOIDCLogin(t, h, srv, client)
	assertCallbackRejected(t, client, srv, "code=auth-code&state="+cv.State, stateCookie, "oidc_exchange_failed")
}

// TestOIDCCallbackTenantClaimRejectionsRedirectToErrorPage exercises
// the three §10.2 tenant-claim rejection codes as surfaced through the
// OIDC callback: a missing tenant_id claim, a malformed tenant_id
// claim, and a well-formed tenant_id that names no provisioned Tenant
// CR.
//
// spec: §27.3.1 ("tenant_id extraction reuses the canonical rejection
// semantics pinned in §10.2 ... when extraction fails the gateway does
// not establish a session record or set lenny_playground_session, and
// instead redirects the browser to the playground error page GET
// /playground/auth/error?error=<code> with the code assigned in the
// Tenant-claim rejection codes (OIDC callback) table"); the table maps
// TENANT_CLAIM_MISSING -> tenant_claim_missing, TENANT_NOT_FOUND ->
// tenant_not_found, TENANT_CLAIM_INVALID_FORMAT ->
// tenant_claim_invalid_format.
func TestOIDCCallbackTenantClaimRejectionsRedirectToErrorPage(t *testing.T) {
	cases := []struct {
		name     string
		oidc     *fakeOIDC
		tenants  TenantRegistry
		wantCode string
	}{
		{
			name:     "empty tenant_id claim",
			oidc:     &fakeOIDC{exchangeErr: &OIDCError{Code: errTenantClaimMissing, Detail: "ID token lacks the tenant_id claim"}},
			tenants:  fakeTenants{registered: map[string]bool{"acme": true}},
			wantCode: "tenant_claim_missing",
		},
		{
			name:     "malformed tenant_id claim",
			oidc:     &fakeOIDC{exchangeErr: &OIDCError{Code: errTenantClaimInvalidFormat, Detail: "tenant_id claim fails the format regex"}},
			tenants:  fakeTenants{registered: map[string]bool{"acme": true}},
			wantCode: "tenant_claim_invalid_format",
		},
		{
			name:     "well-formed but unregistered tenant_id",
			oidc:     &fakeOIDC{subject: OIDCSubject{UserID: "alice", TenantID: "ghost"}},
			tenants:  fakeTenants{registered: map[string]bool{"acme": true}},
			wantCode: "tenant_not_found",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := New(Config{Enabled: true, AuthMode: AuthModeOIDC}, Options{
				Signer:   devSigner(),
				Sessions: NewMemorySessionStore(),
				OIDC:     tc.oidc,
				Tenants:  tc.tenants,
			})
			srv := httptest.NewServer(h.PlaygroundRoutes())
			defer srv.Close()
			client := noRedirectClient()

			stateCookie, cv := startOIDCLogin(t, h, srv, client)
			assertCallbackRejected(t, client, srv, "code=auth-code&state="+cv.State, stateCookie, tc.wantCode)
		})
	}
}
