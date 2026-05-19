// SPDX-License-Identifier: MIT

package playground

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

// OIDCSubject is the validated set of standard Lenny claims §27.3.1
// extracts from an OIDC ID token at /playground/auth/callback.
type OIDCSubject struct {
	// UserID is the resolved subject identity (the sub claim, or the
	// configured user_id claim).
	UserID string

	// TenantID is the value of the configured auth.tenantIdClaim.
	TenantID string

	// CallerType is the §25.1 caller_type claim.
	CallerType string

	// Scope is the space-delimited OIDC scope claim.
	Scope string

	// RefreshToken is the OIDC refresh token, when the provider
	// granted one. §27.3.1 holds it on the session record; v1 does not
	// perform server-side refresh-token rotation.
	RefreshToken string
}

// OIDCError categorizes an authorization-code-exchange failure so the
// callback handler can decide between a tenant-claim error redirect
// and a generic provider-error redirect.
type OIDCError struct {
	// Code is surfaced to the playground error page as the error
	// query parameter.
	Code string
	// Detail is logged; it is not shown to the browser.
	Detail string
}

func (e *OIDCError) Error() string {
	if e.Detail != "" {
		return "playground oidc: " + e.Code + ": " + e.Detail
	}
	return "playground oidc: " + e.Code
}

// OIDCExchanger performs the §27.3.1 OIDC authorization-code flow
// against the configured provider. It is an interface so the gateway
// wires an HTTP-backed implementation while the package tests inject
// a deterministic fake; neither the mint tests nor the cross-replica
// revocation test needs a live provider.
type OIDCExchanger interface {
	// AuthorizationURL returns the provider authorization-endpoint URL
	// the browser is redirected to. state is the anti-CSRF token and
	// challenge is the PKCE S256 code_challenge.
	AuthorizationURL(state, challenge, redirectURI string) string

	// Exchange performs the PKCE-protected token exchange and
	// validates the returned ID token (signature, iss, aud, exp, nbf).
	// On success it returns the extracted subject claims. A failure
	// returns an *OIDCError carrying the code the callback surfaces to
	// the user.
	Exchange(ctx context.Context, code, verifier, redirectURI string) (OIDCSubject, error)
}

// HTTPOIDCConfig configures the HTTP-backed OIDCExchanger the gateway
// wires in oidc mode.
type HTTPOIDCConfig struct {
	// AuthorizationEndpoint and TokenEndpoint are the provider OIDC
	// endpoints (discovered from the provider's well-known document by
	// the gateway, or configured directly).
	AuthorizationEndpoint string
	TokenEndpoint         string

	// ClientID and ClientSecret identify the playground to the
	// provider. ClientSecret is empty for a public client; the flow is
	// PKCE-protected regardless (§9.3 / §27.3.1).
	ClientID     string
	ClientSecret string

	// Scopes is the OIDC scope set requested in the authorization
	// request. The default is "openid profile email" when empty.
	Scopes []string

	// TenantIDClaim is the auth.tenantIdClaim the gateway extracts the
	// tenant from. The default is "tenant_id".
	TenantIDClaim string

	// UserIDClaim names the claim the subject identity is read from.
	// The default is "sub".
	UserIDClaim string

	// CallerTypeClaim names the §25.1 caller_type claim. The default
	// is "caller_type".
	CallerTypeClaim string

	// IDTokenValidator validates the returned ID token (signature,
	// iss, aud, exp, nbf) and returns its claims as a map. It is
	// injected so the gateway supplies the OIDC JWKS verifier without
	// this package depending on a JWKS library.
	IDTokenValidator func(ctx context.Context, idToken string) (map[string]any, error)

	// HTTPClient performs the token-endpoint request. A nil client
	// uses http.DefaultClient.
	HTTPClient *http.Client
}

// httpOIDCExchanger is the HTTP-backed OIDCExchanger.
type httpOIDCExchanger struct {
	cfg HTTPOIDCConfig
}

// NewHTTPOIDCExchanger returns an OIDCExchanger that drives the
// authorization-code flow over HTTP against the configured provider.
func NewHTTPOIDCExchanger(cfg HTTPOIDCConfig) OIDCExchanger {
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"openid", "profile", "email"}
	}
	if cfg.TenantIDClaim == "" {
		cfg.TenantIDClaim = "tenant_id"
	}
	if cfg.UserIDClaim == "" {
		cfg.UserIDClaim = "sub"
	}
	if cfg.CallerTypeClaim == "" {
		cfg.CallerTypeClaim = "caller_type"
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &httpOIDCExchanger{cfg: cfg}
}

// AuthorizationURL implements OIDCExchanger.
func (x *httpOIDCExchanger) AuthorizationURL(state, challenge, redirectURI string) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", x.cfg.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", strings.Join(x.cfg.Scopes, " "))
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	sep := "?"
	if strings.Contains(x.cfg.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	return x.cfg.AuthorizationEndpoint + sep + q.Encode()
}

// Exchange implements OIDCExchanger.
func (x *httpOIDCExchanger) Exchange(ctx context.Context, code, verifier, redirectURI string) (OIDCSubject, error) {
	if x.cfg.IDTokenValidator == nil {
		return OIDCSubject{}, &OIDCError{Code: "oidc_misconfigured", Detail: "no ID-token validator wired"}
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", x.cfg.ClientID)
	form.Set("code_verifier", verifier)
	if x.cfg.ClientSecret != "" {
		form.Set("client_secret", x.cfg.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, x.cfg.TokenEndpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return OIDCSubject{}, &OIDCError{Code: "oidc_exchange_failed", Detail: err.Error()}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := x.cfg.HTTPClient.Do(req)
	if err != nil {
		return OIDCSubject{}, &OIDCError{Code: "oidc_exchange_failed", Detail: err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return OIDCSubject{}, &OIDCError{
			Code:   "oidc_exchange_failed",
			Detail: fmt.Sprintf("token endpoint returned %d: %s", resp.StatusCode, string(body)),
		}
	}
	var tok struct {
		IDToken      string `json:"id_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return OIDCSubject{}, &OIDCError{Code: "oidc_exchange_failed", Detail: "token response is not JSON"}
	}
	if tok.IDToken == "" {
		return OIDCSubject{}, &OIDCError{Code: "oidc_exchange_failed", Detail: "token response carried no id_token"}
	}
	claims, err := x.cfg.IDTokenValidator(ctx, tok.IDToken)
	if err != nil {
		return OIDCSubject{}, &OIDCError{Code: "oidc_id_token_invalid", Detail: err.Error()}
	}
	return x.extractSubject(claims, tok.RefreshToken)
}

// extractSubject maps validated ID-token claims to an OIDCSubject. A
// missing or malformed tenant claim is returned as an *OIDCError with
// the §27.3.1 tenant-claim-rejection code so the callback handler
// redirects to the canonical error page.
func (x *httpOIDCExchanger) extractSubject(claims map[string]any, refreshToken string) (OIDCSubject, error) {
	tenant := stringClaim(claims, x.cfg.TenantIDClaim)
	if tenant == "" {
		return OIDCSubject{}, &OIDCError{Code: errTenantClaimMissing, Detail: "ID token lacks the tenant_id claim"}
	}
	if !tenantIDPattern.MatchString(tenant) {
		return OIDCSubject{}, &OIDCError{Code: errTenantClaimInvalidFormat, Detail: "tenant_id claim fails the format regex"}
	}
	user := stringClaim(claims, x.cfg.UserIDClaim)
	if user == "" {
		user = stringClaim(claims, "sub")
	}
	return OIDCSubject{
		UserID:       user,
		TenantID:     tenant,
		CallerType:   stringClaim(claims, x.cfg.CallerTypeClaim),
		Scope:        stringClaim(claims, "scope"),
		RefreshToken: refreshToken,
	}, nil
}

// stringClaim reads a string-valued claim from a decoded claim map.
// A non-string value yields the empty string.
func stringClaim(claims map[string]any, key string) string {
	v, ok := claims[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

// errIsTenantClaim reports whether err is an *OIDCError carrying one
// of the §27.3.1 tenant-claim-rejection codes.
func errIsTenantClaim(err error) (*OIDCError, bool) {
	var oe *OIDCError
	if !errors.As(err, &oe) {
		return nil, false
	}
	switch oe.Code {
	case errTenantClaimMissing, errTenantNotFound, errTenantClaimInvalidFormat:
		return oe, true
	}
	return nil, false
}
