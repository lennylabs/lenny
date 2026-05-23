// SPDX-License-Identifier: MIT

package connectoroauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Token exchange errors. The connector OAuth callback handler maps each
// to its HTTP status.
var (
	// ErrTokenExchangeFailed — the provider's token endpoint returned a
	// non-2xx response or a body the gateway could not parse. The
	// callback handler returns 502 Bad Gateway: the failure is upstream,
	// not the caller's.
	ErrTokenExchangeFailed = errors.New("connectoroauth: token exchange with the provider failed")

	// ErrNoAccessToken — the token endpoint returned 2xx but the body
	// carried no access_token.
	ErrNoAccessToken = errors.New("connectoroauth: token response carried no access_token")
)

// AuthorizationRequest carries the inputs for one §9.3 authorization
// request. BuildAuthorizationURL renders these into the provider's
// authorization URL.
type AuthorizationRequest struct {
	// AuthorizationEndpoint is the connector's
	// auth.authorizationEndpoint.
	AuthorizationEndpoint string

	// ClientID is the connector's auth.clientId.
	ClientID string

	// RedirectURI is the gateway's connector OAuth callback URL.
	RedirectURI string

	// Scopes is the connector's requested auth.scopes.
	Scopes []string

	// State is the signed anti-CSRF value from StateSigner.Mint.
	State string

	// CodeChallenge is the PKCE S256 challenge. Always set per §9.3
	// (mandatory for public clients, recommended for confidential).
	CodeChallenge string
}

// BuildAuthorizationURL renders an OAuth 2.1 authorization-code request
// URL. It always emits `response_type=code`, the signed `state`, and
// the PKCE `code_challenge` with `code_challenge_method=S256`. OAuth
// 2.1 removes the implicit grant, so `response_type` is never `token`.
//
// It returns an error when a required field is empty or the
// authorization endpoint does not parse as an absolute URL.
func BuildAuthorizationURL(r AuthorizationRequest) (string, error) {
	switch {
	case r.AuthorizationEndpoint == "":
		return "", errors.New("connectoroauth: authorization endpoint is empty")
	case r.ClientID == "":
		return "", errors.New("connectoroauth: client id is empty")
	case r.RedirectURI == "":
		return "", errors.New("connectoroauth: redirect URI is empty")
	case r.State == "":
		return "", errors.New("connectoroauth: state is empty")
	case r.CodeChallenge == "":
		return "", errors.New("connectoroauth: PKCE code_challenge is empty")
	}
	u, err := url.Parse(r.AuthorizationEndpoint)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return "", fmt.Errorf("connectoroauth: authorization endpoint %q is not an absolute URL", r.AuthorizationEndpoint)
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", r.ClientID)
	q.Set("redirect_uri", r.RedirectURI)
	q.Set("state", r.State)
	q.Set("code_challenge", r.CodeChallenge)
	q.Set("code_challenge_method", PKCEMethodS256)
	if len(r.Scopes) > 0 {
		q.Set("scope", strings.Join(r.Scopes, " "))
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// TokenExchangeRequest carries the inputs for the §9.3 authorization-
// code-grant token exchange at the provider's token endpoint.
type TokenExchangeRequest struct {
	// TokenEndpoint is the connector's auth.tokenEndpoint.
	TokenEndpoint string

	// ClientID is the connector's auth.clientId.
	ClientID string

	// ClientSecret is the resolved confidential-client secret. Empty
	// for a public client (PKCE-only); when present it is sent in the
	// form body per RFC 6749 section 2.3.1.
	ClientSecret string

	// Code is the authorization code from the callback query.
	Code string

	// CodeVerifier is the PKCE verifier recovered from the StateStore.
	// Always sent: §9.3 mandates PKCE for public clients and recommends
	// it for confidential ones.
	CodeVerifier string

	// RedirectURI must equal the redirect_uri sent in the authorization
	// request (the OAuth redirect_uri match).
	RedirectURI string
}

// TokenResponse is the parsed RFC 6749 token-endpoint response.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

// ExpiresAt returns the absolute expiry of the access token given the
// instant the response was received, or the zero time when the
// response carried no expires_in.
func (t TokenResponse) ExpiresAt(received time.Time) time.Time {
	if t.ExpiresIn <= 0 {
		return time.Time{}
	}
	return received.Add(time.Duration(t.ExpiresIn) * time.Second)
}

// HTTPDoer is the subset of *http.Client the token exchange uses.
// Accepting an interface keeps ExchangeCode testable against a fake
// provider token endpoint. *http.Client satisfies it.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ExchangeCode performs the §9.3 authorization-code-grant token
// exchange: it POSTs `grant_type=authorization_code` with the code,
// the PKCE code_verifier, and the redirect_uri to the provider's token
// endpoint and parses the JSON response.
//
// httpClient is the client used for the exchange; pass a client with a
// timeout. ExchangeCode returns ErrTokenExchangeFailed when the
// endpoint is non-2xx or the body does not parse, and ErrNoAccessToken
// when a 2xx response carries no access_token. The provider's error
// body is not echoed back to the caller — it may carry provider
// internals — but it is wrapped into the returned error for the
// gateway log.
func ExchangeCode(ctx context.Context, httpClient HTTPDoer, r TokenExchangeRequest) (TokenResponse, error) {
	switch {
	case r.TokenEndpoint == "":
		return TokenResponse{}, errors.New("connectoroauth: token endpoint is empty")
	case r.Code == "":
		return TokenResponse{}, errors.New("connectoroauth: authorization code is empty")
	case r.CodeVerifier == "":
		return TokenResponse{}, errors.New("connectoroauth: PKCE code_verifier is empty")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", r.Code)
	form.Set("redirect_uri", r.RedirectURI)
	form.Set("client_id", r.ClientID)
	form.Set("code_verifier", r.CodeVerifier)
	if r.ClientSecret != "" {
		form.Set("client_secret", r.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.TokenEndpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return TokenResponse{}, fmt.Errorf("connectoroauth: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return TokenResponse{}, fmt.Errorf("%w: %v", ErrTokenExchangeFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Cap the body read so a hostile or misbehaving token endpoint
	// cannot exhaust gateway memory.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return TokenResponse{}, fmt.Errorf("%w: read body: %v", ErrTokenExchangeFailed, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return TokenResponse{}, fmt.Errorf("%w: provider returned HTTP %d: %s",
			ErrTokenExchangeFailed, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var tr TokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return TokenResponse{}, fmt.Errorf("%w: response body is not JSON: %v",
			ErrTokenExchangeFailed, err)
	}
	if tr.AccessToken == "" {
		return TokenResponse{}, ErrNoAccessToken
	}
	return tr, nil
}

// RefreshTokenRequest carries the inputs for the §9.3 / RFC 6749 §6
// refresh-token grant against a provider's token endpoint.
//
// spec: §4.3 line 200 "Refresh tokens stored encrypted at rest";
// §9.3 lines 152–155 (connector token lifecycle).
type RefreshTokenRequest struct {
	// TokenEndpoint is the connector's auth.tokenEndpoint.
	TokenEndpoint string

	// ClientID is the connector's auth.clientId.
	ClientID string

	// ClientSecret is the resolved confidential-client secret. Empty
	// for a public client.
	ClientSecret string

	// RefreshToken is the previously stored refresh token. Required.
	RefreshToken string

	// Scopes optionally narrows the scopes on the refreshed access
	// token. Empty leaves the granted scope set unchanged.
	Scopes []string
}

// RefreshAccessToken drives the RFC 6749 §6 refresh-token grant:
// `POST {token_endpoint}` with `grant_type=refresh_token` plus the
// stored refresh_token. Returns the new TokenResponse (which may
// include a rotated refresh_token per the provider's rotation policy).
//
// Returns ErrTokenExchangeFailed when the endpoint is non-2xx or the
// body does not parse, and ErrNoAccessToken when a 2xx response carries
// no access_token. Providers that rotate the refresh token return the
// new value on the response's `refresh_token` field; callers MUST persist
// it and the access token under one transaction.
//
// spec: §4.3 line 200, §9.3 connector OAuth flow.
func RefreshAccessToken(ctx context.Context, httpClient HTTPDoer, r RefreshTokenRequest) (TokenResponse, error) {
	switch {
	case r.TokenEndpoint == "":
		return TokenResponse{}, errors.New("connectoroauth: token endpoint is empty")
	case r.RefreshToken == "":
		return TokenResponse{}, errors.New("connectoroauth: refresh_token is empty")
	case r.ClientID == "":
		return TokenResponse{}, errors.New("connectoroauth: client id is empty")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", r.RefreshToken)
	form.Set("client_id", r.ClientID)
	if r.ClientSecret != "" {
		form.Set("client_secret", r.ClientSecret)
	}
	if len(r.Scopes) > 0 {
		form.Set("scope", strings.Join(r.Scopes, " "))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.TokenEndpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return TokenResponse{}, fmt.Errorf("connectoroauth: build refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return TokenResponse{}, fmt.Errorf("%w: %v", ErrTokenExchangeFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return TokenResponse{}, fmt.Errorf("%w: read body: %v", ErrTokenExchangeFailed, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return TokenResponse{}, fmt.Errorf("%w: provider returned HTTP %d: %s",
			ErrTokenExchangeFailed, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var tr TokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return TokenResponse{}, fmt.Errorf("%w: response body is not JSON: %v",
			ErrTokenExchangeFailed, err)
	}
	if tr.AccessToken == "" {
		return TokenResponse{}, ErrNoAccessToken
	}
	return tr, nil
}
