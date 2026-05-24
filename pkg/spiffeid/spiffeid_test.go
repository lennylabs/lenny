// SPDX-License-Identifier: MIT

package spiffeid

import (
	"crypto/x509"
	"errors"
	"net/url"
	"testing"
)

// TestParseHappyPath exercises the §4.3 per-replica identity shape:
// spiffe://lenny.dev/gateway/replica-3 parses to trust-domain
// lenny.dev with path /gateway/replica-3.
// spec: §4.3 line 205.
func TestParseHappyPath(t *testing.T) {
	t.Parallel()
	got, err := Parse("spiffe://lenny.dev/gateway/replica-3")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.TrustDomain != "lenny.dev" {
		t.Errorf("TrustDomain = %q, want lenny.dev", got.TrustDomain)
	}
	if got.Path != "/gateway/replica-3" {
		t.Errorf("Path = %q, want /gateway/replica-3", got.Path)
	}
	if got.URI != "spiffe://lenny.dev/gateway/replica-3" {
		t.Errorf("URI = %q, want canonical form", got.URI)
	}
}

// TestParseLowercasesTrustDomain confirms the SPIFFE-ID
// case-insensitive trust-domain rule: a mixed-case host parses to a
// lower-case trust domain so downstream audit comparisons match the
// configured mtls.spiffeTrustDomain value.
// spec: §4.3 line 205.
func TestParseLowercasesTrustDomain(t *testing.T) {
	t.Parallel()
	got, err := Parse("spiffe://Lenny.Dev/gateway/replica-3")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.TrustDomain != "lenny.dev" {
		t.Errorf("TrustDomain = %q, want lenny.dev", got.TrustDomain)
	}
	if got.URI != "spiffe://lenny.dev/gateway/replica-3" {
		t.Errorf("URI = %q, want canonical form (lowercase host)", got.URI)
	}
}

// TestParseRejectsNonSpiffeScheme rejects URIs whose scheme is not
// spiffe://. The §4.3 admission contract is that only SPIFFE IDs
// carry workload identity; an https:// or urn:: SAN must be ignored
// by the auditor.
// spec: §4.3 line 205.
func TestParseRejectsNonSpiffeScheme(t *testing.T) {
	t.Parallel()
	cases := []string{
		"https://lenny.dev/gateway/replica-3",
		"urn:lenny:gateway:replica-3",
		"file:///etc/foo",
		"",
	}
	for _, c := range cases {
		if _, err := Parse(c); err == nil {
			t.Errorf("Parse(%q) = nil, want error", c)
		}
	}
}

// TestParseRejectsForbiddenComponents covers the SPIFFE-ID
// rejection of user info, port, query, fragment, and dot path
// segments. Each must produce an error so a malformed credential
// cannot smuggle metadata through the auditor.
// spec: §4.3 line 205.
func TestParseRejectsForbiddenComponents(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, uri string
	}{
		{"empty host", "spiffe:///gateway/replica-3"},
		{"user info", "spiffe://alice@lenny.dev/gateway/replica-3"},
		{"port", "spiffe://lenny.dev:443/gateway/replica-3"},
		{"query", "spiffe://lenny.dev/gateway/replica-3?x=1"},
		{"fragment", "spiffe://lenny.dev/gateway/replica-3#x"},
		{"dot segment", "spiffe://lenny.dev/gateway/./replica-3"},
		{"dot-dot segment", "spiffe://lenny.dev/gateway/../replica-3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Parse(c.uri); err == nil {
				t.Errorf("Parse(%q) = nil, want error", c.uri)
			}
		})
	}
}

// TestFromCertReturnsFirstSpiffeURISAN covers the path the Token
// Service auditor uses: a certificate carrying a spiffe:// URI SAN
// must yield the parsed ID. Multiple URIs are tolerated; the first
// SPIFFE-ID URI wins.
// spec: §4.3 line 205.
func TestFromCertReturnsFirstSpiffeURISAN(t *testing.T) {
	t.Parallel()
	spiffe, _ := url.Parse("spiffe://lenny.dev/gateway/replica-2")
	other, _ := url.Parse("https://lenny.dev/cluster-internal")
	cert := &x509.Certificate{URIs: []*url.URL{other, spiffe}}
	got, err := FromCert(cert)
	if err != nil {
		t.Fatalf("FromCert: %v", err)
	}
	if got.TrustDomain != "lenny.dev" || got.Path != "/gateway/replica-2" {
		t.Errorf("FromCert = %+v, want trust=lenny.dev path=/gateway/replica-2", got)
	}
}

// TestFromCertReturnsErrNoSPIFFEIDWhenAbsent covers the negative path
// — a non-SPIFFE certificate produces ErrNoSPIFFEID so the auditor
// can degrade cleanly when the per-replica path is not configured.
// spec: §4.3 line 205.
func TestFromCertReturnsErrNoSPIFFEIDWhenAbsent(t *testing.T) {
	t.Parallel()
	cert := &x509.Certificate{}
	if _, err := FromCert(cert); !errors.Is(err, ErrNoSPIFFEID) {
		t.Errorf("FromCert on bare cert: got %v, want ErrNoSPIFFEID", err)
	}
	other, _ := url.Parse("https://lenny.dev/cluster-internal")
	if _, err := FromCert(&x509.Certificate{URIs: []*url.URL{other}}); !errors.Is(err, ErrNoSPIFFEID) {
		t.Errorf("FromCert on https-only cert: got %v, want ErrNoSPIFFEID", err)
	}
}

// TestFromCertNilCertificateReturnsError guards against a nil pointer
// at the auditor edge.
// spec: §4.3 line 205.
func TestFromCertNilCertificateReturnsError(t *testing.T) {
	t.Parallel()
	if _, err := FromCert(nil); err == nil {
		t.Error("FromCert(nil) = nil, want error")
	}
}
