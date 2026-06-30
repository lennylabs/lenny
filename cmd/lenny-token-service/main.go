// SPDX-License-Identifier: MIT

// Command lenny-token-service is the §13.3 Token Service. It serves
// two surfaces:
//
//   - HTTP `POST /v1/oauth/token` (RFC 8693 token exchange) for the
//     external dialect-issuing path. The gateway reverse-proxies
//     `/v1/oauth/*` here so the Token Service is the actual minter
//     for every Lenny bearer token.
//   - gRPC `lenny.tokenservice.v1.TokenService` for the §4.3 / §12.2.4
//     credential-assignment trust boundary the gateway calls over mTLS
//     to materialize, rotate, and revoke credential leases.
//
// Both surfaces sign with the §4 KMS-envelope-backed signer: the HMAC-
// SHA256 signing key is sealed under a KMS key-encryption-key rather
// than being a plaintext per-process secret. The KMS provider is
// pluggable: --kms-provider selects local | aws | gcp | azure; local
// is the no-cloud development KEK and is rejected when
// --environment=prod.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/observability/logging"
	"github.com/lennylabs/lenny/pkg/spiffeid"
)

// main is the §13.3 Token Service composition root reduced to its ordered
// sequence: install the §16.4 structured logger, parse the flag surface once,
// run the finalize hook the KMS provider flags expose, then hand the parsed
// flags to runTokenService, which constructs and starts every subsystem in
// dependency order and serves until shutdown (proposal 0020 §4 Part A R11).
//
// spec: §16.4 lines 370-372 — structured JSON logs (component=token-service),
// F-16.4.1; §4.1 — the composition root parses its inputs once and threads
// them to each subsystem builder.
func main() {
	// spec: §16.4 lines 370-372 — structured JSON logs from the token
	// service; routes the stdlib log package through the §16.4 handler
	// (component=token-service). F-16.4.1.
	logging.Setup(os.Stderr, "token-service")

	f := parseFlags()
	if err := f.kmsFinalize(); err != nil {
		log.Fatalf("lenny-token-service: %v", err)
	}
	runTokenService(f)
}

// envOr resolves a string from the named env var, falling back to def
// when unset.
func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// inClusterClientset builds a Kubernetes clientset from the in-cluster
// ServiceAccount config for the §4.9 RBAC live-probe. It returns an
// error when the Token Service is not running inside a cluster (local
// dev), in which case the probe is disabled.
func inClusterClientset() (kubernetes.Interface, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(cfg)
}

// envInt resolves an int from the named env var, falling back to def
// when unset or unparseable.
func envInt(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	var out int
	_, err := fmt.Sscan(v, &out)
	if err != nil {
		return def
	}
	return out
}

// envDuration resolves a time.Duration from the named env var,
// falling back to def when unset or unparseable.
func envDuration(name string, def time.Duration) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

// spiffeAuditInterceptor returns a unary server interceptor that
// extracts the SPIFFE-ID URI SAN from the gateway client's mTLS
// certificate and logs it on every Token Service gRPC call. When the
// caller presents a non-SPIFFE certificate (the shared per-Service
// cert path), the interceptor logs once at INFO and proceeds. When
// the caller presents a SPIFFE URI SAN (the per-replica path enabled
// by gateway.spiffe.enabled), the URI lands in the log line and
// future audit emits can read it from the context.
//
// spec: §4.3 line 205 — "Each gateway replica has a distinct mTLS
// identity so compromise of one is attributable and revocable
// independently."
func spiffeAuditInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		pr, ok := peer.FromContext(ctx)
		if !ok || pr == nil {
			return handler(ctx, req)
		}
		tlsInfo, ok := pr.AuthInfo.(credentials.TLSInfo)
		if !ok || len(tlsInfo.State.PeerCertificates) == 0 {
			// Plaintext dev path or no client cert: nothing to extract.
			return handler(ctx, req)
		}
		cert := tlsInfo.State.PeerCertificates[0]
		id, err := spiffeid.FromCert(cert)
		if err != nil {
			// Non-SPIFFE cert: shared per-Service path. Log once per
			// session so an operator can correlate the audit trail
			// with the chart configuration (spiffe.enabled=false).
			log.Printf("lenny-token-service: §4.3 gRPC %s: peer cert has no SPIFFE URI SAN (per-Service identity path)", info.FullMethod)
			return handler(ctx, req)
		}
		log.Printf("lenny-token-service: §4.3 gRPC %s: peer SPIFFE ID = %s (per-replica identity)", info.FullMethod, id)
		return handler(WithSPIFFEID(ctx, id), req)
	}
}

// spiffeIDCtxKey carries a parsed §4.3 SPIFFE ID through the request
// context so audit emits and future per-replica policy checks read a
// single canonical value.
type spiffeIDCtxKey struct{}

// WithSPIFFEID returns a copy of ctx carrying id. Exposed so future
// in-handler audit emits can record the caller's SPIFFE identity
// without re-parsing the certificate.
// spec: §4.3 line 205.
func WithSPIFFEID(ctx context.Context, id spiffeid.ID) context.Context {
	return context.WithValue(ctx, spiffeIDCtxKey{}, id)
}

// SPIFFEIDFromContext returns the SPIFFE ID the interceptor attached
// to ctx, or the zero value and false when none is set.
// spec: §4.3 line 205.
func SPIFFEIDFromContext(ctx context.Context) (spiffeid.ID, bool) {
	id, ok := ctx.Value(spiffeIDCtxKey{}).(spiffeid.ID)
	return id, ok
}

// tokenServiceCreds builds the §4.3 / §10.3 mTLS server credentials.
// All three of certPath, keyPath, and caPath empty selects plaintext
// (dev mode); any one set requires all three and returns mTLS server
// credentials that require and verify the gateway's client cert.
// spec: §4.3 line 195 "Gateway replicas call the Token Service over mTLS"
func tokenServiceCreds(certPath, keyPath, caPath string) (credentials.TransportCredentials, error) {
	switch {
	case certPath == "" && keyPath == "" && caPath == "":
		return nil, nil
	case certPath == "" || keyPath == "" || caPath == "":
		return nil, fmt.Errorf("token service mTLS requires --tls-cert, --tls-key, and --tls-ca to all be set")
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("load token-service server cert: %w", err)
	}
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read token-service client CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("token-service client CA bundle parsed no certificates")
	}
	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
	}), nil
}

// kms.Provider is implicitly referenced through the providerflags
// package; keep the import live for code-search clarity.
var _ kms.Provider = (*kms.Local)(nil)
