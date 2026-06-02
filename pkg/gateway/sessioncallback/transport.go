// SPDX-License-Identifier: MIT

package sessioncallback

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"time"
)

// connectTimeout is the §14 line 111 callback connect timeout.
const connectTimeout = 5 * time.Second

// requestTimeout bounds one delivery attempt end to end. §14 line 111
// names a 5 s connect and a 10 s response-read timeout; the client
// Timeout is their sum so a slow receiver cannot pin a worker goroutine.
const requestTimeout = connectTimeout + 10*time.Second

// defaultResolver adapts net.DefaultResolver to the Resolver seam.
type defaultResolver struct{}

func (defaultResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, network, host)
}

// errNoRedirect is returned by the callback client's CheckRedirect:
// §14 line 111 requires that callback requests never follow redirects,
// so a 3xx is a delivery failure rather than a hop to a new host that
// would bypass the registration-time SSRF pin.
var errNoRedirect = errors.New("sessioncallback: callback redirects are not followed")

// pinnedHTTPClient returns the §14 line 110-111 isolated callback HTTP
// client: it dials the registration-time pinned IP regardless of how the
// hostname re-resolves (defeating DNS rebinding), bounds the attempt by
// the connect and request timeouts, and refuses redirects. The TLS
// ServerName and Host header keep the original hostname because the URL
// is unchanged; only the dial target is overridden.
func pinnedHTTPClient(pinned netip.Addr) *http.Client {
	dialer := &net.Dialer{Timeout: connectTimeout}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(pinned.String(), port))
		},
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        4,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: connectTimeout,
	}
	return &http.Client{
		Timeout:       requestTimeout,
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return errNoRedirect },
	}
}
