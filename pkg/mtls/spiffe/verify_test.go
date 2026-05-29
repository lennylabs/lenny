// SPDX-License-Identifier: MIT

package spiffe_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net/url"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/mtls/spiffe"
)

const testTrustDomain = "lenny-acme-prod"

// certWithURIs builds a self-signed leaf certificate carrying the given
// URI SANs, returning the parsed *x509.Certificate. The §10.3 agent-pod
// certificate's identity lives in its spiffe:// URI SAN, so the
// verifier reads exactly this field.
func certWithURIs(t *testing.T, uris ...string) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	parsed := make([]*url.URL, 0, len(uris))
	for _, raw := range uris {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse SAN URI %q: %v", raw, err)
		}
		parsed = append(parsed, u)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "agent-pod"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		URIs:         parsed,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return cert
}

// verifiedChains wraps a leaf certificate in the [][]*x509.Certificate
// shape tls passes to VerifyPeerCertificate under
// RequireAndVerifyClientCert.
func verifiedChains(cert *x509.Certificate) [][]*x509.Certificate {
	return [][]*x509.Certificate{{cert}}
}

type denySet map[string]bool

func (d denySet) Contains(uri string) bool { return d[uri] }

func TestAgentPeerVerifierAcceptsMatchingTrustDomain_spec_10_3_321(t *testing.T) {
	cert := certWithURIs(t, "spiffe://"+testTrustDomain+"/agent/default-pool/pod-abc")
	v := spiffe.AgentPeerVerifier{TrustDomain: testTrustDomain}
	if err := v.VerifyPeerCertificate(nil, verifiedChains(cert)); err != nil {
		t.Fatalf("expected a matching agent identity to verify, got %v", err)
	}
}

func TestAgentPeerVerifierRejectsWrongTrustDomain_spec_10_3_321(t *testing.T) {
	cert := certWithURIs(t, "spiffe://lenny-other-deployment/agent/default-pool/pod-abc")
	var gotReason spiffe.MismatchReason
	v := spiffe.AgentPeerVerifier{
		TrustDomain: testTrustDomain,
		OnMismatch:  func(r spiffe.MismatchReason, _ string, _ error) { gotReason = r },
	}
	if err := v.VerifyPeerCertificate(nil, verifiedChains(cert)); err == nil {
		t.Fatal("expected a certificate from a foreign trust domain to be rejected (NET-060 cross-deployment isolation)")
	}
	if gotReason != spiffe.ReasonIdentityMismatch {
		t.Errorf("reason = %q, want %q", gotReason, spiffe.ReasonIdentityMismatch)
	}
}

func TestAgentPeerVerifierRejectsInterceptorKind_spec_10_3_321(t *testing.T) {
	cert := certWithURIs(t, "spiffe://"+testTrustDomain+"/interceptor/lenny-system/pod-abc")
	v := spiffe.AgentPeerVerifier{TrustDomain: testTrustDomain}
	if err := v.VerifyPeerCertificate(nil, verifiedChains(cert)); err == nil {
		t.Fatal("expected an interceptor identity to be rejected on the agent peer path")
	}
}

func TestAgentPeerVerifierRejectsNoSPIFFESAN_spec_10_3_321(t *testing.T) {
	cert := certWithURIs(t) // no URI SANs at all
	var gotReason spiffe.MismatchReason
	v := spiffe.AgentPeerVerifier{
		TrustDomain: testTrustDomain,
		OnMismatch:  func(r spiffe.MismatchReason, _ string, _ error) { gotReason = r },
	}
	if err := v.VerifyPeerCertificate(nil, verifiedChains(cert)); err == nil {
		t.Fatal("expected a certificate with no spiffe:// SAN to be rejected")
	}
	if gotReason != spiffe.ReasonNoSPIFFESAN {
		t.Errorf("reason = %q, want %q", gotReason, spiffe.ReasonNoSPIFFESAN)
	}
}

func TestAgentPeerVerifierRejectsMalformedURI_spec_10_3_321(t *testing.T) {
	cert := certWithURIs(t, "spiffe://"+testTrustDomain+"/agent/only-two-segments")
	var gotReason spiffe.MismatchReason
	v := spiffe.AgentPeerVerifier{
		TrustDomain: testTrustDomain,
		OnMismatch:  func(r spiffe.MismatchReason, _ string, _ error) { gotReason = r },
	}
	if err := v.VerifyPeerCertificate(nil, verifiedChains(cert)); err == nil {
		t.Fatal("expected a malformed SPIFFE URI to be rejected")
	}
	if gotReason != spiffe.ReasonMalformedURI {
		t.Errorf("reason = %q, want %q", gotReason, spiffe.ReasonMalformedURI)
	}
}

func TestAgentPeerVerifierRejectsNoPeerCertificate_spec_10_3_321(t *testing.T) {
	var gotReason spiffe.MismatchReason
	v := spiffe.AgentPeerVerifier{
		TrustDomain: testTrustDomain,
		OnMismatch:  func(r spiffe.MismatchReason, _ string, _ error) { gotReason = r },
	}
	if err := v.VerifyPeerCertificate(nil, nil); err == nil {
		t.Fatal("expected an empty handshake to be rejected")
	}
	if gotReason != spiffe.ReasonNoCertificate {
		t.Errorf("reason = %q, want %q", gotReason, spiffe.ReasonNoCertificate)
	}
}

func TestAgentPeerVerifierRejectsRevokedCertificate_spec_10_3_352(t *testing.T) {
	uri := "spiffe://" + testTrustDomain + "/agent/default-pool/pod-revoked"
	cert := certWithURIs(t, uri)
	var gotReason spiffe.MismatchReason
	v := spiffe.AgentPeerVerifier{
		TrustDomain: testTrustDomain,
		DenyList:    denySet{uri: true},
		OnMismatch:  func(r spiffe.MismatchReason, _ string, _ error) { gotReason = r },
	}
	if err := v.VerifyPeerCertificate(nil, verifiedChains(cert)); err == nil {
		t.Fatal("expected a deny-listed certificate to be rejected at the handshake (NET-060 revocation)")
	}
	if gotReason != spiffe.ReasonRevoked {
		t.Errorf("reason = %q, want %q", gotReason, spiffe.ReasonRevoked)
	}
}

func TestAgentPeerVerifierAcceptsWhenNotRevoked_spec_10_3_352(t *testing.T) {
	uri := "spiffe://" + testTrustDomain + "/agent/default-pool/pod-live"
	cert := certWithURIs(t, uri)
	v := spiffe.AgentPeerVerifier{
		TrustDomain: testTrustDomain,
		DenyList:    denySet{"spiffe://" + testTrustDomain + "/agent/default-pool/some-other-pod": true},
	}
	if err := v.VerifyPeerCertificate(nil, verifiedChains(cert)); err != nil {
		t.Fatalf("a live certificate not on the deny list must verify, got %v", err)
	}
}

func TestAgentPeerVerifierEnforcesPoolAndPodExpectation_spec_10_3_321(t *testing.T) {
	cert := certWithURIs(t, "spiffe://"+testTrustDomain+"/agent/pool-a/pod-1")
	// Expect a different pool: the §10.3 line 321 {pool}/{pod} narrowing
	// must reject a certificate whose pool segment disagrees.
	v := spiffe.AgentPeerVerifier{
		TrustDomain: testTrustDomain,
		Expect:      spiffe.AgentExpectation{Pool: "pool-b"},
	}
	if err := v.VerifyPeerCertificate(nil, verifiedChains(cert)); err == nil {
		t.Fatal("expected a pool mismatch to be rejected when Expect.Pool is set")
	}

	// The matching pool/pod is accepted.
	match := spiffe.AgentPeerVerifier{
		TrustDomain: testTrustDomain,
		Expect:      spiffe.AgentExpectation{Pool: "pool-a", PodName: "pod-1"},
	}
	if err := match.VerifyPeerCertificate(nil, verifiedChains(cert)); err != nil {
		t.Fatalf("a certificate matching the expected pool/pod must verify, got %v", err)
	}
}

// TestAgentPeerVerifierFailsClosedWithoutTrustDomain confirms that a
// verifier with no trust domain rejects every peer rather than passing
// CA-only trust through — spec line 324 ("never falls back to CA-only
// trust").
func TestAgentPeerVerifierFailsClosedWithoutTrustDomain_spec_10_3_324(t *testing.T) {
	cert := certWithURIs(t, "spiffe://"+testTrustDomain+"/agent/default-pool/pod-abc")
	v := spiffe.AgentPeerVerifier{} // TrustDomain unset
	if err := v.VerifyPeerCertificate(nil, verifiedChains(cert)); err == nil {
		t.Fatal("expected a verifier with no trust domain to fail closed")
	}
}

// TestAgentPeerVerifierReadsRawCertsFallback confirms the verifier reads
// the raw certificate when no verified chain is supplied.
func TestAgentPeerVerifierReadsRawCertsFallback_spec_10_3_321(t *testing.T) {
	cert := certWithURIs(t, "spiffe://"+testTrustDomain+"/agent/default-pool/pod-abc")
	v := spiffe.AgentPeerVerifier{TrustDomain: testTrustDomain}
	if err := v.VerifyPeerCertificate([][]byte{cert.Raw}, nil); err != nil {
		t.Fatalf("expected the raw-certificate fallback to verify, got %v", err)
	}
}
