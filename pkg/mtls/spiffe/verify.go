// SPDX-License-Identifier: MIT

package spiffe

import (
	"crypto/x509"
	"errors"
	"fmt"
)

// DenyChecker reports whether a SPIFFE URI is on the §10.3 certificate
// revocation deny list (spec line 352, "keyed by SPIFFE URI"). The
// gateway's *denylist.DenyList satisfies it; the interface keeps this
// package free of a denylist import so the verifier stays a leaf
// dependency.
type DenyChecker interface {
	Contains(uri string) bool
}

// MismatchReason classifies why an inbound peer certificate failed the
// §10.3 NET-060 agent-identity check. The caller maps it onto the
// spec's pod_identity_mismatch log so an operator can distinguish a
// malformed SAN from a wrong trust domain from a revoked certificate.
type MismatchReason string

const (
	// ReasonNoCertificate — the handshake presented no peer leaf
	// certificate (or it failed to parse).
	ReasonNoCertificate MismatchReason = "no_peer_certificate"
	// ReasonNoSPIFFESAN — the leaf certificate carries no spiffe:// URI
	// SAN, so it cannot be an agent-pod identity.
	ReasonNoSPIFFESAN MismatchReason = "no_spiffe_san"
	// ReasonMalformedURI — the spiffe:// URI does not parse into one of
	// the §10.3 identity shapes.
	ReasonMalformedURI MismatchReason = "spiffe_uri_malformed"
	// ReasonIdentityMismatch — the URI parsed but the kind, trust
	// domain, pool, or pod-name disagrees with the expectation.
	ReasonIdentityMismatch MismatchReason = "identity_mismatch"
	// ReasonRevoked — the certificate's SPIFFE URI is on the deny list.
	ReasonRevoked MismatchReason = "certificate_revoked"
)

// AgentPeerVerifier validates an inbound agent-pod mTLS peer per §10.3
// NET-060 (spec line 321): the peer certificate's SPIFFE URI SAN MUST
// parse as spiffe://<trust-domain>/agent/{pool}/{pod-name} with the
// trust domain equal to the configured global.spiffeTrustDomain, and
// the certificate MUST NOT be on the §10.3 revocation deny list (spec
// line 352). Either failure rejects the handshake before any gRPC frame
// is exchanged. Possession of a valid cluster-CA certificate is
// necessary but never sufficient (spec line 324): chain verification
// alone does not establish that the peer is an agent pod in this
// deployment's trust domain.
//
// The zero value is not usable: TrustDomain must be set. The gateway
// installs VerifyPeerCertificate on the §8.6 GatewayControl listener's
// tls.Config so the check runs at TLS handshake time.
type AgentPeerVerifier struct {
	// TrustDomain is the single trust-domain anchor (spec line 324) the
	// peer's SPIFFE URI must match. Required.
	TrustDomain string

	// DenyList, when non-nil, is consulted for every parsed identity so
	// a certificate revoked between rotations is rejected at handshake
	// (spec line 352). A nil deny list disables the revocation check.
	DenyList DenyChecker

	// Expect optionally narrows the accepted identity to a specific pool
	// and pod-name (spec line 321 "matched against the expected {pool}
	// and {pod-name}"). Its TrustDomain is overridden by TrustDomain.
	// Leave Pool/PodName empty to accept any agent in the trust domain.
	Expect AgentExpectation

	// OnMismatch, when non-nil, is invoked on every rejection so the
	// caller can emit the spec's pod_identity_mismatch log. It is never
	// called on success.
	OnMismatch func(reason MismatchReason, uri string, err error)
}

// VerifyPeerCertificate matches the tls.Config.VerifyPeerCertificate
// signature. It runs after the standard chain verification (the gateway
// listener is built with tls.RequireAndVerifyClientCert, so
// verifiedChains is populated) and applies the §10.3 NET-060 SPIFFE
// identity check on top of CA trust. Returning a non-nil error aborts
// the handshake with no gRPC response, exactly as the spec requires.
// spec: §10.3 line 321 (NET-060); §10.3 line 352 (deny list)
func (v AgentPeerVerifier) VerifyPeerCertificate(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
	leaf, err := leafCertificate(rawCerts, verifiedChains)
	if err != nil {
		v.report(ReasonNoCertificate, "", err)
		return err
	}
	uri, ok := spiffeURI(leaf)
	if !ok {
		err := errors.New("spiffe: peer certificate has no spiffe:// URI SAN (§10.3 NET-060)")
		v.report(ReasonNoSPIFFESAN, "", err)
		return err
	}
	id, err := Parse(uri)
	if err != nil {
		v.report(ReasonMalformedURI, uri, err)
		return err
	}
	want := v.Expect
	want.TrustDomain = v.TrustDomain
	if err := ValidateAgent(id, want); err != nil {
		v.report(ReasonIdentityMismatch, uri, err)
		return err
	}
	if v.DenyList != nil && v.DenyList.Contains(uri) {
		err := fmt.Errorf("spiffe: peer certificate %q is on the §10.3 revocation deny list", uri)
		v.report(ReasonRevoked, uri, err)
		return err
	}
	return nil
}

func (v AgentPeerVerifier) report(reason MismatchReason, uri string, err error) {
	if v.OnMismatch != nil {
		v.OnMismatch(reason, uri, err)
	}
}

// leafCertificate returns the peer's leaf certificate. It prefers the
// verified chain (populated under RequireAndVerifyClientCert) so the
// returned certificate is the one the CA trust path accepted, and falls
// back to parsing the first raw certificate when no verified chain is
// present (e.g. a VerifyClientCertIfGiven listener).
func leafCertificate(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) (*x509.Certificate, error) {
	if len(verifiedChains) > 0 && len(verifiedChains[0]) > 0 {
		return verifiedChains[0][0], nil
	}
	if len(rawCerts) > 0 {
		leaf, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			return nil, fmt.Errorf("spiffe: parse peer leaf certificate: %w", err)
		}
		return leaf, nil
	}
	return nil, errors.New("spiffe: no peer certificate presented on the mTLS handshake")
}

// spiffeURI returns the first spiffe:// URI SAN on the certificate.
func spiffeURI(cert *x509.Certificate) (string, bool) {
	for _, u := range cert.URIs {
		if u != nil && u.Scheme == Scheme {
			return u.String(), true
		}
	}
	return "", false
}
