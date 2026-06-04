// SPDX-License-Identifier: MIT

package adapter

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/lennylabs/lenny/pkg/mtls/certreload"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// NewGRPCServer builds a gRPC server with the Adapter service and the
// standard grpc.health.v1 service registered. The health service is
// set to SERVING; the gateway probes adapter liveness through it
// (§4.7). Pass TLSServerOption to serve the §4.7 mTLS link.
func NewGRPCServer(s *Server, opts ...grpc.ServerOption) *grpc.Server {
	// spec: §16.3 line 327 ("Gateway → Pod (gRPC metadata)") — the OTel
	// stats handler extracts the inbound traceparent from gRPC metadata so
	// the adapter's spans continue the gateway's trace, and propagates it
	// onward. Prepended so a caller-supplied handler in opts cannot shadow
	// the propagation seam. F-16.3.3.
	//
	// spec: §16.4 line 376 — the credential-redaction interceptor is the
	// per-method access-log/trace surface for AssignCredentials and
	// RotateCredentials: it records only the RPC name, lease ID, provider
	// type, and outcome, never the secret payload. Prepended so it owns the
	// credential access-log surface before any caller-supplied interceptor.
	// F-16.4.8.
	// spec: §10.1 line 49 — the hold-state interceptors reject every
	// non-allowlisted RPC with coordinator_hold while the adapter is
	// awaiting a new coordinator, so the enforcement is uniform across
	// every operational method without each handler re-checking.
	opts = append([]grpc.ServerOption{
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(credentialRedactionInterceptor(nil), s.holdStateUnaryInterceptor),
		grpc.ChainStreamInterceptor(s.holdStateStreamInterceptor),
	}, opts...)
	gs := grpc.NewServer(opts...)
	adapterv1.RegisterAdapterServer(gs, s)

	hs := health.NewServer()
	hs.SetServingStatus("", healthv1.HealthCheckResponse_SERVING)
	healthv1.RegisterHealthServer(gs, hs)

	return gs
}

// TLSConfigMod mutates the tls.Config a transport option assembles
// before it is wrapped in gRPC credentials. It lets a caller install a
// peer-validation callback (the §10.3 NET-060 SPIFFE VerifyPeerCertificate
// hook) or pin a ServerName without this package taking a dependency on
// the validator. Mods run after the base config (certificates, CA pool,
// client-auth mode) is built, so they can override or extend it.
type TLSConfigMod func(*tls.Config)

// WithServerName returns a TLSConfigMod that pins tls.Config.ServerName.
// A client with a pinned ServerName makes Go's standard crypto/tls
// verification chain reject any certificate whose SAN does not cover
// that exact name, regardless of the dial target host. The §10.3
// NET-060 adapter→gateway dial pins the gateway's DNS SAN this way
// (spec line 322) so a cluster-CA-signed certificate issued to the
// Token Service, controller, or any other lenny-system workload cannot
// impersonate the gateway.
// spec: §10.3 line 322 (NET-060)
func WithServerName(name string) TLSConfigMod {
	return func(c *tls.Config) { c.ServerName = name }
}

// TLSServerOption builds the gRPC server option that serves the
// adapter over TLS. The §4.7 gateway↔adapter link is mTLS in
// production: when clientCAFile is supplied, the adapter requires and
// verifies the gateway's client certificate against it. When certFile
// and keyFile are both empty the adapter serves plaintext, which is
// intended only for local development; the returned option is nil in
// that case.
//
// Optional mods run after the base config is assembled. The gateway's
// §8.6 GatewayControl listener passes a mod that installs the §10.3
// NET-060 SPIFFE VerifyPeerCertificate callback so each inbound pod
// certificate's identity is validated at handshake.
func TLSServerOption(certFile, keyFile, clientCAFile string, mods ...TLSConfigMod) (grpc.ServerOption, error) {
	if certFile == "" && keyFile == "" {
		return nil, nil
	}
	// spec: §10.3 line 338 — serve the leaf via a filesystem-watching
	// GetCertificate callback so a cert-manager renewal of the projected
	// volume is picked up without restarting the pod or dropping the
	// gRPC connection.
	reloader, err := certreload.New(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load adapter TLS keypair: %w", err)
	}
	cfg := &tls.Config{
		GetCertificate: reloader.GetCertificate,
		MinVersion:     tls.VersionTLS12,
	}
	if clientCAFile != "" {
		caPEM, err := os.ReadFile(clientCAFile)
		if err != nil {
			return nil, fmt.Errorf("read adapter client CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, errors.New("adapter client CA file contains no usable certificate")
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	for _, m := range mods {
		m(cfg)
	}
	return grpc.Creds(credentials.NewTLS(cfg)), nil
}

// TLSClientOption builds the gRPC dial option used to reach a gRPC peer
// over the §4.7/§10.3 mTLS link. When certFile and keyFile are supplied
// the dialer presents that client certificate; caFile verifies the
// peer's server certificate. When all three are empty the dial is
// plaintext, which is intended only for local development.
//
// Optional mods run after the base config is assembled. The §10.3
// NET-060 adapter→gateway dial passes WithServerName(gatewaycontrol.GatewayDNSName)
// so the gateway's DNS SAN is pinned (spec line 322); the gateway→adapter
// dial passes no mod because a pod's serving certificate carries a
// SPIFFE URI SAN rather than a DNS name.
func TLSClientOption(certFile, keyFile, caFile string, mods ...TLSConfigMod) (grpc.DialOption, error) {
	if certFile == "" && keyFile == "" && caFile == "" {
		return grpc.WithTransportCredentials(insecure.NewCredentials()), nil
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if certFile != "" || keyFile != "" {
		// spec: §10.3 line 338 — present the client leaf via a
		// filesystem-watching GetClientCertificate callback so a renewed
		// adapter certificate is used on the next dial without a restart.
		reloader, err := certreload.New(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("load adapter client TLS keypair: %w", err)
		}
		cfg.GetClientCertificate = reloader.GetClientCertificate
	}
	if caFile != "" {
		caPEM, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read adapter server CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, errors.New("adapter server CA file contains no usable certificate")
		}
		cfg.RootCAs = pool
	}
	for _, m := range mods {
		m(cfg)
	}
	return grpc.WithTransportCredentials(credentials.NewTLS(cfg)), nil
}
