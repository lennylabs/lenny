// SPDX-License-Identifier: MIT

// Package tokensvcproxy is the gateway-side HTTP reverse-proxy for
// the §13.3 canonical token endpoint. The gateway no longer mints
// tokens in-process; /v1/oauth/* requests are forwarded to
// lenny-token-service so the Token Service is the only component
// holding the signing key, the only component reaching the
// issued_tokens table, and the only component writing
// `token.exchanged` audit rows.
//
// spec: §4.3 line 193 ("Canonical token endpoint — POST
// /v1/oauth/token ... All Lenny bearer tokens are minted here") and
// F-4.3.12 (the in-process gateway mount is removed; the gateway
// forwards to the Token Service HTTP listener).
package tokensvcproxy

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
)

// Proxy is the §4.3 canonical-surface reverse proxy. Construct with
// New(upstreamURL). The proxy preserves the path so /v1/oauth/token
// on the gateway lands on /v1/oauth/token at the Token Service.
type Proxy struct {
	upstream *url.URL
	rp       *httputil.ReverseProxy
}

// New returns a Proxy that forwards every request to upstreamURL,
// preserving the path and the body. The host header is rewritten to
// the upstream's host so the Token Service does not see a
// Host: lenny-gateway header for a request the gateway forwarded.
// Returns an error when upstreamURL fails to parse or is missing a
// scheme.
func New(upstreamURL string) (*Proxy, error) {
	u, err := url.Parse(upstreamURL)
	if err != nil {
		return nil, fmt.Errorf("tokensvcproxy: parse upstream URL %q: %w", upstreamURL, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("tokensvcproxy: upstream URL %q must include scheme://host", upstreamURL)
	}
	rp := httputil.NewSingleHostReverseProxy(u)
	// Preserve the original Director's URL-rewrite while also
	// stamping the upstream's Host header so the Token Service can
	// reject requests with a wrong Host should a future check
	// require it.
	defaultDirector := rp.Director
	rp.Director = func(req *http.Request) {
		defaultDirector(req)
		req.Host = u.Host
	}
	return &Proxy{upstream: u, rp: rp}, nil
}

// Handler returns the http.Handler that forwards every request to the
// upstream Token Service.
func (p *Proxy) Handler() http.Handler { return p.rp }

// UpstreamURL returns the resolved upstream URL.
func (p *Proxy) UpstreamURL() *url.URL { return p.upstream }
