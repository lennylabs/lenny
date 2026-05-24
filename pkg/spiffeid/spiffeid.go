// SPDX-License-Identifier: MIT

// Package spiffeid is the minimal SPIFFE-ID parser the Token Service
// uses to extract a per-replica caller identity from an mTLS client
// certificate's SAN field. Per-replica identity backs the §4.3 line
// 205 attributability rule: each gateway replica has a distinct
// SPIFFE identity so compromise of one replica is attributable and
// revocable independently.
//
// A SPIFFE ID has the form spiffe://<trust-domain>/<path>. This
// package intentionally implements only the parsing the §4.3
// auditor needs; it does not depend on github.com/spiffe/go-spiffe so
// the gateway and Token Service stay free of cgo and external trust
// domain libraries. spec: §4.3 line 205.
package spiffeid

import (
	"crypto/x509"
	"errors"
	"net/url"
	"strings"
)

// ErrNoSPIFFEID is returned when no URI SAN in the certificate parses
// as a SPIFFE ID. spec: §4.3 line 205.
var ErrNoSPIFFEID = errors.New("spiffeid: no spiffe:// URI SAN on certificate")

// ID is a parsed SPIFFE ID. Both fields are non-empty when Parse
// returns nil.
type ID struct {
	// TrustDomain is the SPIFFE trust-domain authority component
	// (the host portion of the spiffe:// URI). It matches the
	// gateway's mtls.spiffeTrustDomain Helm value when the §4.3
	// per-replica path is configured.
	TrustDomain string

	// Path is the SPIFFE workload path (e.g. /gateway/replica-3 for
	// a per-replica gateway identity). It begins with "/" when set.
	Path string

	// URI is the raw spiffe:// URI for audit logging.
	URI string
}

// String returns the canonical spiffe:// URI form.
func (id ID) String() string { return id.URI }

// Parse returns the parsed SPIFFE ID for the given URI string, or an
// error when the URI does not satisfy the SPIFFE-ID format. SPIFFE-ID
// requirements applied here:
//
//   - Scheme MUST be "spiffe".
//   - Host (trust domain) MUST be non-empty.
//   - No port, user info, query, or fragment.
//   - Path MUST NOT contain "." or ".." segments.
//
// Parse is forgiving on cases the SPIFFE spec normalizes to lowercase
// (trust domains are case-insensitive) — both inputs and outputs are
// lower-cased on the trust domain.
// spec: §4.3 line 205.
func Parse(uri string) (ID, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return ID{}, err
	}
	if u.Scheme != "spiffe" {
		return ID{}, errors.New("spiffeid: scheme is not spiffe://")
	}
	if u.Host == "" {
		return ID{}, errors.New("spiffeid: empty trust domain")
	}
	if u.User != nil {
		return ID{}, errors.New("spiffeid: user info is not permitted")
	}
	if u.RawQuery != "" {
		return ID{}, errors.New("spiffeid: query string is not permitted")
	}
	if u.Fragment != "" {
		return ID{}, errors.New("spiffeid: fragment is not permitted")
	}
	if u.Port() != "" {
		return ID{}, errors.New("spiffeid: port is not permitted")
	}
	// SPIFFE-ID path normalization: reject "." and ".." segments.
	for _, seg := range strings.Split(u.Path, "/") {
		if seg == "." || seg == ".." {
			return ID{}, errors.New("spiffeid: path contains dot segment")
		}
	}
	td := strings.ToLower(u.Host)
	// Reassemble the canonical URI form so audit consumers compare
	// stable values.
	canonical := "spiffe://" + td + u.Path
	return ID{TrustDomain: td, Path: u.Path, URI: canonical}, nil
}

// FromCert returns the first SPIFFE-ID URI SAN on cert, or
// ErrNoSPIFFEID when the certificate carries none. A certificate with
// multiple SPIFFE-ID URI SANs is unusual; the first is returned and
// the rest are ignored by this parser. spec: §4.3 line 205.
func FromCert(cert *x509.Certificate) (ID, error) {
	if cert == nil {
		return ID{}, errors.New("spiffeid: nil certificate")
	}
	for _, uri := range cert.URIs {
		if uri == nil || uri.Scheme != "spiffe" {
			continue
		}
		return Parse(uri.String())
	}
	return ID{}, ErrNoSPIFFEID
}
