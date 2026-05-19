// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

// tlsProxy terminates HTTPS for §17.4 Embedded Mode and forwards plain
// HTTP to the gateway. The production gateway serves plaintext HTTP;
// in a cluster an ingress terminates TLS. Embedded Mode has no
// ingress, so lenny runs this reverse proxy to present
// https://localhost:8443 with the self-signed leaf certificate.
//
// The listener binds loopback only. §17.4 requires that any attempt to
// expose the gateway outside localhost fails closed; binding 127.0.0.1
// enforces that for the HTTPS listener.
type tlsProxy struct {
	srv *http.Server
}

// startTLSProxy builds and starts the HTTPS reverse proxy. listenAddr
// is the loopback host:port to serve HTTPS on, upstream is the
// gateway's plaintext base URL, and certPath and keyPath are the
// self-signed leaf certificate and key. It returns once the listener
// is bound.
func startTLSProxy(listenAddr, upstream, certPath, keyPath string) (*tlsProxy, error) {
	host, _, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return nil, fmt.Errorf("embedded tls-proxy: parse listen address %q: %w", listenAddr, err)
	}
	if !isLoopbackHost(host) {
		// §17.4 EMBEDDED_MODE_LOCAL_ONLY: the gateway must not be
		// reachable outside localhost.
		return nil, fmt.Errorf("EMBEDDED_MODE_LOCAL_ONLY: HTTPS listener host %q is not loopback", host)
	}
	target, err := url.Parse(upstream)
	if err != nil {
		return nil, fmt.Errorf("embedded tls-proxy: parse upstream %q: %w", upstream, err)
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("embedded tls-proxy: load certificate: %w", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           proxy,
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		},
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("embedded tls-proxy: listen on %s: %w", listenAddr, err)
	}
	p := &tlsProxy{srv: srv}
	go func() {
		// ServeTLS with empty cert/key paths uses TLSConfig.Certificates.
		_ = srv.ServeTLS(ln, "", "")
	}()
	return p, nil
}

// Shutdown gracefully stops the HTTPS proxy.
func (p *tlsProxy) Shutdown(ctx context.Context) error {
	if p == nil || p.srv == nil {
		return nil
	}
	return p.srv.Shutdown(ctx)
}

// isLoopbackHost reports whether host names the loopback interface. It
// accepts the literal localhost name and any loopback IP literal.
func isLoopbackHost(host string) bool {
	if host == "localhost" || host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
