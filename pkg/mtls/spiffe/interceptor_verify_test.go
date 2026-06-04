// SPDX-License-Identifier: MIT

package spiffe_test

import (
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/mtls/spiffe"
)

// spec: §10.3 line 328 (NET-063) — the gateway accepts an interceptor
// certificate whose SPIFFE URI carries the configured trust domain and a
// namespace in the gateway.interceptorNamespaces allowlist.
func TestInterceptorPeerVerifierAcceptsAllowedNamespace_spec_10_3_328(t *testing.T) {
	cert := certWithURIs(t, "spiffe://"+testTrustDomain+"/interceptor/acme-interceptors/lenny-interceptor")
	v := spiffe.InterceptorPeerVerifier{
		TrustDomain: testTrustDomain,
		Namespaces:  []string{"globex-interceptors", "acme-interceptors"},
	}
	if err := v.VerifyPeerCertificate(nil, verifiedChains(cert)); err != nil {
		t.Fatalf("expected an interceptor in an allowed namespace to verify, got %v", err)
	}
}

// spec: §10.3 line 328 — an interceptor whose namespace segment is not in
// the allowlist is rejected. The impact text (a co-located non-interceptor
// pod in the interceptor namespace) is the same class of failure.
func TestInterceptorPeerVerifierRejectsNamespaceNotInAllowlist_spec_10_3_328(t *testing.T) {
	cert := certWithURIs(t, "spiffe://"+testTrustDomain+"/interceptor/evil-namespace/lenny-interceptor")
	var gotReason spiffe.MismatchReason
	v := spiffe.InterceptorPeerVerifier{
		TrustDomain: testTrustDomain,
		Namespaces:  []string{"acme-interceptors"},
		OnMismatch:  func(r spiffe.MismatchReason, _ string, _ error) { gotReason = r },
	}
	err := v.VerifyPeerCertificate(nil, verifiedChains(cert))
	if err == nil {
		t.Fatal("expected an interceptor outside the namespace allowlist to be rejected (NET-063)")
	}
	if gotReason != spiffe.ReasonIdentityMismatch {
		t.Errorf("reason = %q, want %q", gotReason, spiffe.ReasonIdentityMismatch)
	}
	var verr *spiffe.VerifyError
	if !errors.As(err, &verr) {
		t.Fatalf("error %v is not a *spiffe.VerifyError", err)
	}
	if verr.Reason != spiffe.ReasonIdentityMismatch {
		t.Errorf("VerifyError.Reason = %q, want %q", verr.Reason, spiffe.ReasonIdentityMismatch)
	}
}

// spec: §10.3 line 328 — a foreign trust domain is rejected (NET-064
// cross-deployment isolation extends to the interceptor link).
func TestInterceptorPeerVerifierRejectsForeignTrustDomain_spec_10_3_328(t *testing.T) {
	cert := certWithURIs(t, "spiffe://lenny-other-deployment/interceptor/acme-interceptors/lenny-interceptor")
	v := spiffe.InterceptorPeerVerifier{
		TrustDomain: testTrustDomain,
		Namespaces:  []string{"acme-interceptors"},
	}
	if err := v.VerifyPeerCertificate(nil, verifiedChains(cert)); err == nil {
		t.Fatal("expected a foreign trust domain to be rejected")
	}
}

// spec: §10.3 line 332 — possession of a valid cluster-CA certificate is
// necessary but never sufficient: an agent identity (or any non-
// interceptor kind) on the interceptor link is rejected.
func TestInterceptorPeerVerifierRejectsAgentKind_spec_10_3_332(t *testing.T) {
	cert := certWithURIs(t, "spiffe://"+testTrustDomain+"/agent/default-pool/pod-abc")
	v := spiffe.InterceptorPeerVerifier{TrustDomain: testTrustDomain, Namespaces: []string{"acme-interceptors"}}
	if err := v.VerifyPeerCertificate(nil, verifiedChains(cert)); err == nil {
		t.Fatal("expected an agent identity to be rejected on the interceptor peer path")
	}
}

// spec: §10.3 line 328 — a certificate with no spiffe:// SAN cannot be an
// interceptor identity.
func TestInterceptorPeerVerifierRejectsNoSPIFFESAN_spec_10_3_328(t *testing.T) {
	cert := certWithURIs(t) // no URI SANs
	var gotReason spiffe.MismatchReason
	v := spiffe.InterceptorPeerVerifier{
		TrustDomain: testTrustDomain,
		Namespaces:  []string{"acme-interceptors"},
		OnMismatch:  func(r spiffe.MismatchReason, _ string, _ error) { gotReason = r },
	}
	if err := v.VerifyPeerCertificate(nil, verifiedChains(cert)); err == nil {
		t.Fatal("expected a certificate with no spiffe:// SAN to be rejected")
	}
	if gotReason != spiffe.ReasonNoSPIFFESAN {
		t.Errorf("reason = %q, want %q", gotReason, spiffe.ReasonNoSPIFFESAN)
	}
}

// spec: §10.3 line 352 — a revoked interceptor certificate (on the deny
// list) is rejected at the handshake even though its identity is valid.
func TestInterceptorPeerVerifierRejectsRevoked_spec_10_3_352(t *testing.T) {
	uri := "spiffe://" + testTrustDomain + "/interceptor/acme-interceptors/lenny-interceptor"
	cert := certWithURIs(t, uri)
	var gotReason spiffe.MismatchReason
	v := spiffe.InterceptorPeerVerifier{
		TrustDomain: testTrustDomain,
		Namespaces:  []string{"acme-interceptors"},
		DenyList:    denySet{uri: true},
		OnMismatch:  func(r spiffe.MismatchReason, _ string, _ error) { gotReason = r },
	}
	if err := v.VerifyPeerCertificate(nil, verifiedChains(cert)); err == nil {
		t.Fatal("expected a revoked interceptor certificate to be rejected")
	}
	if gotReason != spiffe.ReasonRevoked {
		t.Errorf("reason = %q, want %q", gotReason, spiffe.ReasonRevoked)
	}
}

// An empty allowlist accepts any namespace in the trust domain
// (trust-domain-only validation), so a gateway that sets the trust
// domain but no namespaces still rejects foreign trust domains and
// non-interceptor kinds while admitting any interceptor in its domain.
func TestInterceptorPeerVerifierEmptyAllowlistAcceptsAnyNamespace_spec_10_3_328(t *testing.T) {
	cert := certWithURIs(t, "spiffe://"+testTrustDomain+"/interceptor/any-namespace/lenny-interceptor")
	v := spiffe.InterceptorPeerVerifier{TrustDomain: testTrustDomain}
	if err := v.VerifyPeerCertificate(nil, verifiedChains(cert)); err != nil {
		t.Fatalf("expected any interceptor namespace to verify with an empty allowlist, got %v", err)
	}
}

func TestVerifyErrorUnwraps(t *testing.T) {
	inner := errors.New("boom")
	verr := &spiffe.VerifyError{Reason: spiffe.ReasonIdentityMismatch, Err: inner}
	if !errors.Is(verr, inner) {
		t.Fatal("expected VerifyError to unwrap to its inner error")
	}
}
