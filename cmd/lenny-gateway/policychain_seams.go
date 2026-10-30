// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"net"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/mtls/certreload"
	"github.com/lennylabs/lenny/pkg/mtls/interceptordial"
	"github.com/lennylabs/lenny/pkg/mtls/spiffe"
)

// sessionRetryLookup adapts the §4.2 session store to the §4.8
// RetryPolicyEvaluator's RetryStateLookup: a missing session reads as
// not-found (ok == false, the request is admitted), and any other store
// fault surfaces as an error so the fail-closed evaluator rejects.
// maxInputSizeResolverHolder lets the §4.8 DelegationPolicyEvaluator be
// registered into the policy chain before delegationSvc is constructed:
// the inner resolver is filled in once the service exists. Until then
// (and whenever inner is nil) it reports "no policy", so the evaluator
// falls back to the operator-configured default maxInputSize. The holder
// is read on the request path after wiring completes, so the deferred
// assignment is safe. spec: §4.8; §8.3. F-13.5.1 / F-8.2.9.
type maxInputSizeResolverHolder struct {
	inner policy.MaxInputSizeResolver
}

func (h *maxInputSizeResolverHolder) ResolveMaxInputSize(ctx context.Context, tenantID, parentSessionID string) (int, bool) {
	if h.inner == nil {
		return 0, false
	}
	return h.inner.ResolveMaxInputSize(ctx, tenantID, parentSessionID)
}

type sessionRetryLookup struct{ sessions sessionstore.Store }

func (l sessionRetryLookup) LookupRetryState(ctx context.Context, tenantID, sessionID string) (policy.RetryState, bool, error) {
	sess, err := l.sessions.Get(ctx, tenantID, sessionID)
	if errors.Is(err, sessionstore.ErrNotFound) {
		return policy.RetryState{}, false, nil
	}
	if err != nil {
		return policy.RetryState{}, false, err
	}
	return policy.RetryState{RetryCount: sess.RetryCount}, true, nil
}

// interceptorIdentity carries the §10.3 NET-063 peer-validation inputs a
// dialInterceptor call needs: the SPIFFE trust domain, the
// interceptor-namespace allowlist, the shared revocation deny list, and
// the §16.1 handshake-metric observer. The zero value disables SPIFFE
// validation (trust domain empty), leaving the existing CA-only dial.
type interceptorIdentity struct {
	trustDomain string
	namespaces  []string
	denyList    spiffe.DenyChecker
	observe     interceptordial.Observer
}

// dialInterceptor dials a §4.8 external RequestInterceptor service. mTLS
// is used when cert/key/ca are all set; with all three empty the dial
// falls through to plaintext for dev mode. The §13.2 NET-058
// NetworkPolicy that scopes egress to the interceptor namespace is
// templated by the Helm chart; this dial assumes that egress is
// permitted.
//
// For an in-cluster interceptor (a .svc endpoint host) with a configured
// SPIFFE trust domain, the dial pins tls.Config.ServerName to the
// endpoint host (DNS-SAN validation, spec §10.3) and installs a
// spiffe.InterceptorPeerVerifier that validates the SPIFFE-URI SAN
// against the trust domain and namespace allowlist and rejects revoked
// certificates (NET-063). Every mTLS handshake outcome is timed into the
// §16.1 lenny_interceptor_mtls_handshake_duration_seconds histogram.
func dialInterceptor(addr, certPath, keyPath, caPath string, id interceptorIdentity) (*grpc.ClientConn, error) {
	if addr == "" {
		return nil, fmt.Errorf("interceptor endpoint is empty")
	}
	var transport grpc.DialOption
	switch {
	case certPath == "" && keyPath == "" && caPath == "":
		transport = grpc.WithTransportCredentials(insecure.NewCredentials())
	case certPath == "" || keyPath == "" || caPath == "":
		return nil, fmt.Errorf("external interceptor mTLS requires --external-interceptor-tls-cert, --external-interceptor-tls-key, and --external-interceptor-ca to all be set")
	default:
		// spec: §10.3 — present the gateway leaf via a
		// filesystem-watching GetClientCertificate callback so a
		// cert-manager renewal is picked up on the next dial without a
		// gateway restart.
		reloader, err := certreload.New(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("load external-interceptor client cert: %w", err)
		}
		caPEM, err := os.ReadFile(caPath)
		if err != nil {
			return nil, fmt.Errorf("read external-interceptor CA bundle: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("external-interceptor CA bundle %q parsed no certificates", caPath)
		}
		host := addr
		if h, _, splitErr := net.SplitHostPort(addr); splitErr == nil {
			host = h
		}
		// spec: §10.3 (NET-063) — only an in-cluster
		// interceptor presents a SPIFFE identity; an external endpoint
		// (public FQDN or raw IP) is out of NET-063 scope (spec line 322)
		// and keeps CA + DNS-SAN validation only.
		var verifier *spiffe.InterceptorPeerVerifier
		if id.trustDomain != "" && interceptordial.InCluster(host) {
			verifier = &spiffe.InterceptorPeerVerifier{
				TrustDomain: id.trustDomain,
				Namespaces:  id.namespaces,
				DenyList:    id.denyList,
				OnMismatch: func(reason spiffe.MismatchReason, uri string, err error) {
					log.Printf("lenny-gateway: §10.3 NET-063 interceptor_identity_mismatch endpoint=%s reason=%s uri=%q: %v", addr, reason, uri, err)
				},
			}
		}
		transport = grpc.WithTransportCredentials(interceptordial.Credentials(interceptordial.Options{
			Reloader:   reloader,
			RootCAs:    pool,
			ServerName: host,
			Verifier:   verifier,
			Observe:    id.observe,
		}))
	}
	return grpc.NewClient(addr, transport)
}
