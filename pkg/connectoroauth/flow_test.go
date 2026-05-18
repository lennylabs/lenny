// SPDX-License-Identifier: MIT

package connectoroauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestBuildAuthorizationURL(t *testing.T) {
	got, err := BuildAuthorizationURL(AuthorizationRequest{
		AuthorizationEndpoint: "https://github.com/login/oauth/authorize",
		ClientID:              "client-123",
		RedirectURI:           "https://gw.acme.com/v1/admin/connectors/oauth/callback",
		Scopes:                []string{"repo", "read:org"},
		State:                 "signed-state-value",
		CodeChallenge:         "the-pkce-challenge",
	})
	if err != nil {
		t.Fatalf("BuildAuthorizationURL: %v", err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("result is not a URL: %v", err)
	}
	q := u.Query()
	checks := map[string]string{
		"response_type":         "code",
		"client_id":             "client-123",
		"redirect_uri":          "https://gw.acme.com/v1/admin/connectors/oauth/callback",
		"state":                 "signed-state-value",
		"code_challenge":        "the-pkce-challenge",
		"code_challenge_method": "S256",
		"scope":                 "repo read:org",
	}
	for k, want := range checks {
		if got := q.Get(k); got != want {
			t.Errorf("query %q = %q, want %q", k, got, want)
		}
	}
	// OAuth 2.1 removes the implicit grant: response_type is never
	// "token".
	if q.Get("response_type") == "token" {
		t.Errorf("response_type=token is the implicit grant, forbidden under OAuth 2.1")
	}
}

func TestBuildAuthorizationURLRejectsMissingFields(t *testing.T) {
	base := AuthorizationRequest{
		AuthorizationEndpoint: "https://github.com/login/oauth/authorize",
		ClientID:              "c",
		RedirectURI:           "https://gw.acme.com/cb",
		State:                 "s",
		CodeChallenge:         "ch",
	}
	mutate := []func(*AuthorizationRequest){
		func(r *AuthorizationRequest) { r.AuthorizationEndpoint = "" },
		func(r *AuthorizationRequest) { r.ClientID = "" },
		func(r *AuthorizationRequest) { r.RedirectURI = "" },
		func(r *AuthorizationRequest) { r.State = "" },
		func(r *AuthorizationRequest) { r.CodeChallenge = "" },
		func(r *AuthorizationRequest) { r.AuthorizationEndpoint = "not-a-url" },
	}
	for i, m := range mutate {
		r := base
		m(&r)
		if _, err := BuildAuthorizationURL(r); err == nil {
			t.Errorf("case %d: BuildAuthorizationURL accepted an invalid request", i)
		}
	}
}

// fakeTokenEndpoint serves a canned RFC 6749 token response. It
// records the form values it received so the test can assert the PKCE
// verifier and grant type were sent.
func fakeTokenEndpoint(t *testing.T, status int, body string) (*httptest.Server, *url.Values) {
	t.Helper()
	captured := &url.Values{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("token endpoint: parse form: %v", err)
		}
		*captured = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, captured
}

func TestExchangeCodeHappyPath(t *testing.T) {
	srv, captured := fakeTokenEndpoint(t, http.StatusOK,
		`{"access_token":"at-xyz","refresh_token":"rt-abc","token_type":"Bearer","expires_in":3600,"scope":"repo"}`)

	got, err := ExchangeCode(context.Background(), srv.Client(), TokenExchangeRequest{
		TokenEndpoint: srv.URL,
		ClientID:      "client-123",
		ClientSecret:  "shh-secret",
		Code:          "auth-code-1",
		CodeVerifier:  "the-pkce-verifier",
		RedirectURI:   "https://gw.acme.com/cb",
	})
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if got.AccessToken != "at-xyz" || got.RefreshToken != "rt-abc" || got.TokenType != "Bearer" {
		t.Fatalf("token response = %+v", got)
	}
	if got.ExpiresIn != 3600 {
		t.Fatalf("ExpiresIn = %d, want 3600", got.ExpiresIn)
	}
	// The PKCE verifier and the authorization-code grant must be in
	// the form body.
	if v := captured.Get("grant_type"); v != "authorization_code" {
		t.Errorf("grant_type = %q, want authorization_code", v)
	}
	if v := captured.Get("code_verifier"); v != "the-pkce-verifier" {
		t.Errorf("code_verifier = %q, want the-pkce-verifier", v)
	}
	if v := captured.Get("code"); v != "auth-code-1" {
		t.Errorf("code = %q, want auth-code-1", v)
	}
	if v := captured.Get("client_secret"); v != "shh-secret" {
		t.Errorf("client_secret = %q, want shh-secret", v)
	}
}

func TestExchangeCodePublicClientOmitsSecret(t *testing.T) {
	srv, captured := fakeTokenEndpoint(t, http.StatusOK,
		`{"access_token":"at-public","token_type":"Bearer"}`)

	_, err := ExchangeCode(context.Background(), srv.Client(), TokenExchangeRequest{
		TokenEndpoint: srv.URL,
		ClientID:      "public-client",
		Code:          "code-1",
		CodeVerifier:  "verifier-1",
		RedirectURI:   "https://gw.acme.com/cb",
	})
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	// A public client sends no client_secret; PKCE alone authenticates.
	if _, present := (*captured)["client_secret"]; present {
		t.Errorf("public-client exchange sent a client_secret")
	}
	if v := captured.Get("code_verifier"); v == "" {
		t.Errorf("public-client exchange omitted the PKCE code_verifier")
	}
}

// TestExchangeCodeProviderError covers a token-exchange failure: a
// non-2xx response from the provider must surface ErrTokenExchangeFailed.
func TestExchangeCodeProviderError(t *testing.T) {
	srv, _ := fakeTokenEndpoint(t, http.StatusBadRequest,
		`{"error":"invalid_grant","error_description":"code expired"}`)

	_, err := ExchangeCode(context.Background(), srv.Client(), TokenExchangeRequest{
		TokenEndpoint: srv.URL,
		ClientID:      "c",
		Code:          "stale-code",
		CodeVerifier:  "verifier-1",
		RedirectURI:   "https://gw.acme.com/cb",
	})
	if !errors.Is(err, ErrTokenExchangeFailed) {
		t.Fatalf("ExchangeCode against a 400: got %v, want ErrTokenExchangeFailed", err)
	}
}

func TestExchangeCodeNoAccessToken(t *testing.T) {
	srv, _ := fakeTokenEndpoint(t, http.StatusOK, `{"token_type":"Bearer"}`)

	_, err := ExchangeCode(context.Background(), srv.Client(), TokenExchangeRequest{
		TokenEndpoint: srv.URL,
		ClientID:      "c",
		Code:          "code-1",
		CodeVerifier:  "verifier-1",
		RedirectURI:   "https://gw.acme.com/cb",
	})
	if !errors.Is(err, ErrNoAccessToken) {
		t.Fatalf("ExchangeCode with no access_token: got %v, want ErrNoAccessToken", err)
	}
}

func TestExchangeCodeNonJSONBody(t *testing.T) {
	srv, _ := fakeTokenEndpoint(t, http.StatusOK, `not json at all`)

	_, err := ExchangeCode(context.Background(), srv.Client(), TokenExchangeRequest{
		TokenEndpoint: srv.URL,
		ClientID:      "c",
		Code:          "code-1",
		CodeVerifier:  "verifier-1",
		RedirectURI:   "https://gw.acme.com/cb",
	})
	if !errors.Is(err, ErrTokenExchangeFailed) {
		t.Fatalf("ExchangeCode with a non-JSON body: got %v, want ErrTokenExchangeFailed", err)
	}
}

func TestExchangeCodeRejectsMissingFields(t *testing.T) {
	for i, r := range []TokenExchangeRequest{
		{ClientID: "c", Code: "x", CodeVerifier: "v"},                              // no token endpoint
		{TokenEndpoint: "https://t.example.com", ClientID: "c", CodeVerifier: "v"}, // no code
		{TokenEndpoint: "https://t.example.com", ClientID: "c", Code: "x"},         // no verifier
	} {
		if _, err := ExchangeCode(context.Background(), http.DefaultClient, r); err == nil {
			t.Errorf("case %d: ExchangeCode accepted an invalid request", i)
		}
	}
}

func TestTokenResponseExpiresAt(t *testing.T) {
	received := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	if got := (TokenResponse{ExpiresIn: 3600}).ExpiresAt(received); !got.Equal(received.Add(time.Hour)) {
		t.Errorf("ExpiresAt = %v, want %v", got, received.Add(time.Hour))
	}
	if got := (TokenResponse{}).ExpiresAt(received); !got.IsZero() {
		t.Errorf("ExpiresAt with no expires_in = %v, want zero", got)
	}
}
