// SPDX-License-Identifier: MIT

// Package interceptordial builds the gateway's outbound mTLS transport
// credentials for the §10.3 NET-063 gateway→in-cluster-interceptor gRPC
// link (spec lines 326-332). The link carries every request and
// response envelope the policy engine mediates, so the gateway pins
// tls.Config.ServerName to the registered interceptor endpoint (DNS-SAN
// validation), installs a SPIFFE InterceptorPeerVerifier (SPIFFE-URI
// validation against the trust domain and interceptor-namespace
// allowlist), and refuses plaintext or one-way TLS. Every handshake is
// timed and classified into the §16.1
// lenny_interceptor_mtls_handshake_duration_seconds{result} histogram so
// the §16.5 InterceptorMTLSHandshakeFailure alert evaluates on a live
// series rather than an always-empty counter.
package interceptordial

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"strings"
	"time"

	"google.golang.org/grpc/credentials"

	"github.com/lennylabs/lenny/pkg/mtls/certreload"
	"github.com/lennylabs/lenny/pkg/mtls/spiffe"
)

// Handshake result labels for the §16.1 histogram (spec
// 16_observability.md line 50): each non-success bucket maps to a
// distinct rejection path.
const (
	ResultSuccess     = "success"
	ResultSANMismatch = "san_mismatch"
	ResultCertExpired = "cert_expired"
	ResultCertMissing = "cert_missing"
	ResultTLSError    = "tls_error"
)

// Observer records a completed handshake outcome. The gateway passes
// gatewaymetrics.Metrics.ObserveInterceptorMTLSHandshake.
type Observer func(result string, seconds float64)

// Options configures Credentials.
type Options struct {
	// Reloader serves the gateway leaf via a filesystem-watching
	// GetClientCertificate callback (§10.3 line 338). Required.
	Reloader *certreload.Reloader

	// RootCAs is the cluster-internal CA bundle the interceptor
	// certificate must chain to. Required.
	RootCAs *x509.CertPool

	// ServerName pins the TLS ServerName to the registered interceptor
	// endpoint host so Go's chain verification refuses any cluster-CA
	// certificate whose SAN does not cover it (spec line 328). Required.
	ServerName string

	// Verifier, when non-nil, applies the §10.3 NET-063 SPIFFE identity
	// check on top of CA trust. Nil leaves CA + DNS-SAN validation only
	// (an external out-of-cluster interceptor with no SPIFFE identity).
	Verifier *spiffe.InterceptorPeerVerifier

	// Observe records the timed handshake outcome. Optional.
	Observe Observer

	// Now is the clock; defaults to time.Now.
	Now func() time.Time
}

// Credentials builds the timed, identity-validating transport
// credentials for an interceptor dial.
func Credentials(opts Options) credentials.TransportCredentials {
	cfg := &tls.Config{
		GetClientCertificate: opts.Reloader.GetClientCertificate,
		RootCAs:              opts.RootCAs,
		ServerName:           opts.ServerName,
		MinVersion:           tls.VersionTLS13,
	}
	if opts.Verifier != nil {
		v := *opts.Verifier
		cfg.VerifyPeerCertificate = v.VerifyPeerCertificate
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &timedCreds{inner: credentials.NewTLS(cfg), observe: opts.Observe, now: now}
}

// timedCreds wraps a TransportCredentials to time and classify the TLS
// handshake into the §16.1 result labels. The SPIFFE check runs inside
// the wrapped handshake via tls.Config.VerifyPeerCertificate, so a
// SPIFFE rejection surfaces as the ClientHandshake error this wrapper
// classifies.
type timedCreds struct {
	inner   credentials.TransportCredentials
	observe Observer
	now     func() time.Time
}

func (c *timedCreds) ClientHandshake(ctx context.Context, authority string, conn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	start := c.now()
	nc, ai, err := c.inner.ClientHandshake(ctx, authority, conn)
	if c.observe != nil {
		c.observe(ClassifyResult(err), c.now().Sub(start).Seconds())
	}
	return nc, ai, err
}

func (c *timedCreds) ServerHandshake(conn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	return c.inner.ServerHandshake(conn)
}

func (c *timedCreds) Info() credentials.ProtocolInfo { return c.inner.Info() }

func (c *timedCreds) Clone() credentials.TransportCredentials {
	return &timedCreds{inner: c.inner.Clone(), observe: c.observe, now: c.now}
}

func (c *timedCreds) OverrideServerName(name string) error {
	return c.inner.OverrideServerName(name)
}

// ClassifyResult maps a ClientHandshake error onto the §16.1 result
// label. A nil error is a successful handshake. SPIFFE rejections carry
// a *spiffe.VerifyError whose MismatchReason determines the bucket; chain
// failures (expired certificate, DNS-SAN/hostname mismatch) are read off
// the crypto/x509 error types; anything else is a generic TLS failure.
// spec: 16_observability.md line 50.
func ClassifyResult(err error) string {
	if err == nil {
		return ResultSuccess
	}
	var verr *spiffe.VerifyError
	if errors.As(err, &verr) {
		switch verr.Reason {
		case spiffe.ReasonNoCertificate:
			return ResultCertMissing
		default:
			// no-SPIFFE-SAN, malformed-URI, identity-mismatch, revoked —
			// the peer presented a certificate that is not the expected
			// interceptor identity.
			return ResultSANMismatch
		}
	}
	var hostErr x509.HostnameError
	if errors.As(err, &hostErr) {
		return ResultSANMismatch
	}
	var invErr x509.CertificateInvalidError
	if errors.As(err, &invErr) && invErr.Reason == x509.Expired {
		return ResultCertExpired
	}
	// crypto/tls reports a server that requested but received no client
	// certificate, and a peer that sent no certificate at all, with
	// these phrases. Either is a missing-certificate condition.
	msg := err.Error()
	if strings.Contains(msg, "certificate required") || strings.Contains(msg, "no certificate") {
		return ResultCertMissing
	}
	return ResultTLSError
}

// InCluster reports whether endpointHost is an in-cluster Kubernetes
// Service DNS name (contains a ".svc" label), which the gateway uses to
// decide whether the §10.3 NET-063 SPIFFE check applies. External
// interceptors reachable by a public FQDN or raw IP are out of NET-063
// scope (spec line 322) and present no SPIFFE identity.
func InCluster(endpointHost string) bool {
	h := strings.TrimSuffix(endpointHost, ".")
	return h == "svc" || strings.HasSuffix(h, ".svc") || strings.Contains(h, ".svc.")
}
