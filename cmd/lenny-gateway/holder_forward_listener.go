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

	"github.com/lennylabs/lenny/pkg/auth/authmw"
	"github.com/lennylabs/lenny/pkg/gateway/api/routetable"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
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
// forward listener's mux of the API front's route table, behind the REST
// listener's auth options. The session core registers the session paths
// there and the MCP server registers /mcp; this step registers the Open
// Responses warm turn, whose package belongs to no part. It runs after the
// HTTP surface is built.
//
// spec: §29.3 (Interactive message send) Forwarding rules 3 and 4, §10.3
// (mTLS PKI) NET-072, §15 (Open Responses conversation sessions)
func (w *gatewayWiring) buildHolderForwardServer() {
	if w.holderForwardTLS == nil {
		return
	}
	registerForwardResponses(w.apiRoutes, w.responsesHandler.Handler())
	w.holderForwardSrv = newHolderForwardServer(*w.f.holderForwardListenAddr, w.holderForwardTLS,
		w.apiRoutes.Handler(seam.ListenerForward), w.restAuthOpts, w.sessions)
}

// registerForwardResponses registers an Open Responses warm turn, POST
// /v1/responses, on the forward listener of routes. GET /v1/responses/{id}
// reads stores only and is never forwarded, so it is not registered.
//
// spec: §29.3 (Interactive message send) Forwarding, §15 (Open Responses
// conversation sessions)
func registerForwardResponses(routes seam.Routes, responses http.Handler) {
	routetable.HandlerMux{Routes: routes, Listener: seam.ListenerForward}.Handle("POST /v1/responses", responses)
}

// newHolderForwardServer builds the listener's http.Server.
func newHolderForwardServer(addr string, cfg *tls.Config, inner http.Handler, authOpts authmw.Options, sessions sessionserver.SessionResolver) *http.Server {
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
