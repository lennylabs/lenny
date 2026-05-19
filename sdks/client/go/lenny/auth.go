// SPDX-License-Identifier: MIT

package lenny

import "net/http"

// Authenticator attaches an authentication credential to an outbound
// request. The Client invokes Apply on every request before sending
// it. Implementations are expected to be safe for concurrent use.
type Authenticator interface {
	// Apply mutates req to carry the credential. A non-nil error
	// aborts the request.
	Apply(req *http.Request) error
}

// AuthFunc adapts a plain function to the Authenticator interface.
type AuthFunc func(req *http.Request) error

// Apply implements Authenticator.
func (f AuthFunc) Apply(req *http.Request) error { return f(req) }

// bearerToken attaches a static OIDC or Lenny-issued access token as
// an Authorization: Bearer header.
type bearerToken struct {
	token string
}

// BearerToken returns an Authenticator that sends token in the
// Authorization header. Use it for an OIDC ID token, a Lenny-issued
// access token, or a service-account token, per the §13 client
// authentication model.
func BearerToken(token string) Authenticator {
	return &bearerToken{token: token}
}

// Apply implements Authenticator.
func (b *bearerToken) Apply(req *http.Request) error {
	req.Header.Set("Authorization", "Bearer "+b.token)
	return nil
}

// apiKey attaches a static API key as an X-Lenny-API-Key header.
type apiKey struct {
	key string
}

// APIKey returns an Authenticator that sends key in the
// X-Lenny-API-Key header.
func APIKey(key string) Authenticator {
	return &apiKey{key: key}
}

// Apply implements Authenticator.
func (a *apiKey) Apply(req *http.Request) error {
	req.Header.Set("X-Lenny-API-Key", a.key)
	return nil
}

// TokenSource supplies a bearer token that may change over time. A
// RefreshingAuth calls Token before each request, so an
// implementation can refresh a token before it expires.
type TokenSource interface {
	// Token returns the current bearer token, refreshing it when
	// necessary. A non-nil error aborts the request.
	Token() (string, error)
}

// refreshingAuth attaches a bearer token sourced fresh from a
// TokenSource on every request.
type refreshingAuth struct {
	source TokenSource
}

// RefreshingToken returns an Authenticator that fetches the bearer
// token from source before each request. It is the §15.6
// token-refresh-before-expiry hook: a TokenSource implementation
// refreshes the token when it is close to expiry and returns the
// current value, and the SDK attaches whatever the source returns.
func RefreshingToken(source TokenSource) Authenticator {
	return &refreshingAuth{source: source}
}

// Apply implements Authenticator.
func (r *refreshingAuth) Apply(req *http.Request) error {
	tok, err := r.source.Token()
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	return nil
}
