// SPDX-License-Identifier: MIT

package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/gatewaycontrol/platformtools"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/mtls/certreload"
)

// buildHolderForwardTransport builds the CH-HOLDERFORWARD client and the
// listener's TLS configuration over one reloader of the gateway replica
// certificate, when the forward is enabled. It runs before the session
// server is built, which takes the client. Without the material, the dev-mode
// posture, it builds nothing and every forward is not delivered.
//
// spec: §10.3 (mTLS PKI) NET-072, §10.1.1 (Stateless Replicas and
// Per-Session Coordination) Replica address
func (w *gatewayWiring) buildHolderForwardTransport() {
	if !w.holderForwardEnabled {
		return
	}
	f := w.f
	client, serverTLS, err := holderForwardTransport(holderForwardMaterial{
		cert: *f.adapterTLSCert, key: *f.adapterTLSKey, ca: *f.adapterCA,
	})
	if err != nil {
		log.Fatalf("lenny-gateway: §10.3 NET-072: %v", err)
	}
	w.holderForwardClient = client
	w.holderForwardTLS = serverTLS
}

// holderForwardTransport loads m into one certificate reloader and the CA
// pool, and returns the forward client and the listener TLS configuration.
func holderForwardTransport(m holderForwardMaterial) (*http.Client, *tls.Config, error) {
	reloader, err := certreload.New(m.cert, m.key)
	if err != nil {
		return nil, nil, fmt.Errorf("load gateway replica certificate: %w", err)
	}
	pool, err := loadCAPool(m.ca)
	if err != nil {
		return nil, nil, err
	}
	return sessionserver.NewHolderForwardClient(reloader, pool), sessionserver.HolderForwardServerTLS(reloader, pool), nil
}

// loadCAPool reads a PEM CA bundle into a certificate pool.
func loadCAPool(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read CA bundle %s: %w", path, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("CA bundle %s parsed no certificates", path)
	}
	return pool, nil
}

// buildHolderForwardServer builds the CH-HOLDERFORWARD listener over the
// session handler mux, the /mcp handler, and the Open Responses handler the
// REST listener serves, behind the REST listener's auth options. It runs
// after the HTTP surface is built.
//
// spec: §29.3 (Interactive message send) Forwarding rules 3 and 4, §10.3
// (mTLS PKI) NET-072, §15 (Open Responses conversation sessions)
func (w *gatewayWiring) buildHolderForwardServer(sessionSrv *sessionserver.Server, mcpSrv *mcp.Server) {
	if w.holderForwardTLS == nil {
		return
	}
	w.holderForwardSrv = newHolderForwardServer(*w.f.holderForwardListenAddr, w.holderForwardTLS,
		holderForwardInner(sessionSrv.Handler(), mcpSrv.Handler(), w.responsesHandler.Handler()), w.restAuthOpts, w.sessions)
}

// holderForwardInner routes the session paths to the session handler, /mcp
// to the MCP handler, and an Open Responses warm turn to the responses
// handler, the surfaces a forward reaches. GET /v1/responses/{id} reads
// stores only and is never forwarded, so it is not mounted.
//
// spec: §29.3 (Interactive message send) Forwarding, §15 (Open Responses
// conversation sessions)
func holderForwardInner(sessions, mcpHandler, responses http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/v1/sessions", sessions)
	mux.Handle("/v1/sessions/", sessions)
	mux.Handle("/mcp", mcpHandler)
	mux.Handle("POST /v1/responses", responses)
	return mux
}

// newHolderForwardServer builds the listener's http.Server.
func newHolderForwardServer(addr string, cfg *tls.Config, inner http.Handler, authOpts authmw.Options, sessions platformtools.SessionResolver) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           sessionserver.NewHolderForwardHandler(inner, authOpts, sessions),
		TLSConfig:         cfg,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

// startHolderForwardListener binds the forward listener and serves it in the
// background. A bind failure stops the process, as the REST listener's does.
func (w *gatewayWiring) startHolderForwardListener() {
	if w.holderForwardSrv == nil {
		return
	}
	lis, err := net.Listen("tcp", w.holderForwardSrv.Addr)
	if err != nil {
		log.Fatalf("lenny-gateway: §10.3 NET-072 holder-forward listen: %v", err)
	}
	go func() {
		log.Printf("lenny-gateway: §28.5.4 CH-HOLDERFORWARD listening on %s", lis.Addr())
		if err := w.holderForwardSrv.ServeTLS(lis, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("lenny-gateway: holder-forward serve: %v", err)
		}
	}()
}
