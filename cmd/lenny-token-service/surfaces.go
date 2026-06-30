// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
	"github.com/lennylabs/lenny/pkg/tokenservice"
	"github.com/lennylabs/lenny/pkg/tokenservice/secretprobe"
)

// buildHTTPSurface constructs the §13.3 HTTP token-exchange server.
// spec: §13.3.
func (w *tokenServiceWiring) buildHTTPSurface() {
	w.httpSrv = &http.Server{
		Addr:              *w.f.addr,
		Handler:           w.srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
}

// buildMetricsSurface constructs the §16.1 metrics server when --metrics-addr
// is set. The chart's allow-token-service-metrics-scrape NetworkPolicy admits
// the monitoring namespace on tokenService.metricsPort.
//
// spec: §16.1.
func (w *tokenServiceWiring) buildMetricsSurface() {
	if *w.f.metricsAddr == "" {
		return
	}
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", w.metricsEmitter.Handler())
	w.metricsSrv = &http.Server{
		Addr:              *w.f.metricsAddr,
		Handler:           metricsMux,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

// buildGRPCSurface constructs the §4.3 / §12.2.4 gRPC TokenService surface when
// --grpc-addr is set. The credential-assignment service is the same in-process
// credassign.Service pool-selection + lease-minting logic the gateway runs
// today; the binary makes it reachable over gRPC so the gateway can switch its
// call site from the in-process MintLease to a gRPC client without
// re-implementing the lease semantics. Pool registration runs through the same
// RegisterPool entry point; no pools are registered at startup so
// AssignCredentials fails fast until an operator configures pools. The
// in-memory lease store and credential cache are appropriate for the
// development path; a production deployment swaps in Postgres-backed
// credleasestore/pgstore and a shared credential cache.
//
// spec: §4.3 / §12.2.4, §4.9 line 1212, §4.3 line 195, §4.3 line 205.
func (w *tokenServiceWiring) buildGRPCSurface() {
	leases := credleasestore.New()
	cache := credcache.New()
	assignSvc := credassign.New(leases, cache)
	tsGRPC := tokenservice.NewGRPCServer(assignSvc, leases)
	tsGRPC.SetAuditor(w.auditor)
	tsGRPC.SetMetrics(w.metricsEmitter)
	// §4.9 line 1212 admin-time RBAC live-probe. Wire the
	// Kubernetes-backed prober when the Token Service runs in-cluster.
	// Out of cluster (local dev) the prober is left unset; the
	// ProbeSecretAccess RPC then returns codes.Unavailable so the gateway
	// admin handler maps it to 503 CREDENTIAL_PROBE_UNAVAILABLE and never
	// fails open.
	if clientset, kerr := inClusterClientset(); kerr != nil {
		log.Printf("lenny-token-service: §4.9 RBAC live-probe disabled (no in-cluster Kubernetes client: %v)", kerr)
	} else {
		tsGRPC.SetSecretAccessProber(secretprobe.New(clientset, *w.f.secretNamespace))
		log.Printf("lenny-token-service: §4.9 RBAC live-probe active in namespace %q", *w.f.secretNamespace)
	}

	if *w.f.grpcAddr == "" {
		return
	}
	// §4.3 / §10.3 mTLS server credentials. The Token Service
	// must verify the gateway's client certificate; the §4.3
	// trust boundary rests on the Token Service authenticating
	// the caller. When the TLS flags are unset the gRPC surface
	// runs in plaintext, which is the dev-mode path only.
	// spec: §4.3 line 195
	creds, err := tokenServiceCreds(*w.f.tlsCert, *w.f.tlsKey, *w.f.tlsCA)
	if err != nil {
		fatalf("gRPC mTLS: %v", err)
	}
	var opts []grpc.ServerOption
	if creds != nil {
		opts = append(opts, grpc.Creds(creds))
		log.Printf("lenny-token-service: §4.3 gRPC mTLS active; client certs verified against --tls-ca")
	} else {
		log.Printf("lenny-token-service: §4.3 gRPC running plaintext (dev mode: --tls-cert/--tls-key/--tls-ca unset)")
	}
	// §4.3 line 205 per-replica identity. When the gateway client
	// cert carries a SPIFFE URI SAN (chart value
	// gateway.spiffe.enabled wires cert-manager-csi-driver-spiffe
	// per-replica identities into the gateway pods), extract the
	// SPIFFE ID and log it for audit. The unary interceptor
	// surfaces the value through the context so handler-level
	// audit emits can include it.
	opts = append(opts, grpc.UnaryInterceptor(spiffeAuditInterceptor()))
	w.grpcSrv = grpc.NewServer(opts...)
	tokensv1.RegisterTokenServiceServer(w.grpcSrv, tsGRPC)
}

// runServer starts the §13.3 HTTP, §16.1 metrics, and §4.3 gRPC listeners,
// blocks until SIGTERM/SIGINT, then drains each surface within a 5-second
// shutdown window. The metrics and gRPC listeners only run when their build
// step constructed a server (their flags were set).
//
// spec: §13.3, §16.1, §4.3.
func (w *tokenServiceWiring) runServer() {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		log.Printf("lenny-token-service: HTTP token-exchange listening on %s", *w.f.addr)
		if err := w.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen http: %v", err)
		}
	}()
	if w.metricsSrv != nil {
		go func() {
			log.Printf("lenny-token-service: §16.1 metrics listening on %s", *w.f.metricsAddr)
			if err := w.metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("listen metrics: %v", err)
			}
		}()
	}
	if w.grpcSrv != nil {
		lis, err := net.Listen("tcp", *w.f.grpcAddr)
		if err != nil {
			log.Fatalf("listen grpc %s: %v", *w.f.grpcAddr, err)
		}
		go func() {
			log.Printf("lenny-token-service: gRPC TokenService listening on %s", *w.f.grpcAddr)
			if err := w.grpcSrv.Serve(lis); err != nil {
				log.Fatalf("serve grpc: %v", err)
			}
		}()
	}
	<-stop
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = w.httpSrv.Shutdown(shutdownCtx)
	if w.metricsSrv != nil {
		_ = w.metricsSrv.Shutdown(shutdownCtx)
	}
	if w.grpcSrv != nil {
		w.grpcSrv.GracefulStop()
	}
}
