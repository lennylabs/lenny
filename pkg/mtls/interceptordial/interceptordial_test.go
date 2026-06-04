// SPDX-License-Identifier: MIT

package interceptordial_test

import (
	"crypto/x509"
	"errors"
	"fmt"
	"testing"

	"github.com/lennylabs/lenny/pkg/mtls/interceptordial"
	"github.com/lennylabs/lenny/pkg/mtls/spiffe"
)

// spec: 16_observability.md line 50 — every handshake outcome maps to a
// distinct §16.1 result label. The classifier is the single point that
// decides the bucket, so the matrix pins each path.
func TestClassifyResult_spec_16_1_50(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"success", nil, interceptordial.ResultSuccess},
		{
			"spiffe identity mismatch -> san_mismatch",
			&spiffe.VerifyError{Reason: spiffe.ReasonIdentityMismatch, Err: errors.New("ns mismatch")},
			interceptordial.ResultSANMismatch,
		},
		{
			"spiffe no-SAN -> san_mismatch",
			&spiffe.VerifyError{Reason: spiffe.ReasonNoSPIFFESAN, Err: errors.New("no san")},
			interceptordial.ResultSANMismatch,
		},
		{
			"spiffe malformed -> san_mismatch",
			&spiffe.VerifyError{Reason: spiffe.ReasonMalformedURI, Err: errors.New("bad uri")},
			interceptordial.ResultSANMismatch,
		},
		{
			"spiffe revoked -> san_mismatch",
			&spiffe.VerifyError{Reason: spiffe.ReasonRevoked, Err: errors.New("revoked")},
			interceptordial.ResultSANMismatch,
		},
		{
			"spiffe no-certificate -> cert_missing",
			&spiffe.VerifyError{Reason: spiffe.ReasonNoCertificate, Err: errors.New("no cert")},
			interceptordial.ResultCertMissing,
		},
		{
			"wrapped spiffe verify error -> san_mismatch",
			fmt.Errorf("tls handshake: %w", &spiffe.VerifyError{Reason: spiffe.ReasonIdentityMismatch, Err: errors.New("x")}),
			interceptordial.ResultSANMismatch,
		},
		{
			"x509 hostname (DNS SAN) mismatch -> san_mismatch",
			x509.HostnameError{Host: "lenny-interceptor.acme.svc"},
			interceptordial.ResultSANMismatch,
		},
		{
			"x509 expired -> cert_expired",
			x509.CertificateInvalidError{Reason: x509.Expired},
			interceptordial.ResultCertExpired,
		},
		{
			"tls certificate required -> cert_missing",
			errors.New("remote error: tls: certificate required"),
			interceptordial.ResultCertMissing,
		},
		{
			"generic tls error -> tls_error",
			errors.New("remote error: tls: handshake failure"),
			interceptordial.ResultTLSError,
		},
		{
			"x509 unknown authority -> tls_error",
			x509.UnknownAuthorityError{},
			interceptordial.ResultTLSError,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := interceptordial.ClassifyResult(tc.err); got != tc.want {
				t.Errorf("ClassifyResult(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

// spec: §10.3 line 322/327 — only an in-cluster (.svc) endpoint carries a
// SPIFFE identity; external FQDNs and raw IPs are out of NET-063 scope.
func TestInCluster_spec_10_3_322(t *testing.T) {
	cases := map[string]bool{
		"lenny-interceptor.acme.svc":               true,
		"lenny-interceptor.acme.svc.cluster.local": true,
		"lenny-interceptor.acme.svc.":              true,
		"svc":                                      true,
		"interceptor.example.com":                  false,
		"10.0.0.5":                                 false,
		"localhost":                                false,
		"my-svc-host.example.com":                  false,
	}
	for host, want := range cases {
		if got := interceptordial.InCluster(host); got != want {
			t.Errorf("InCluster(%q) = %v, want %v", host, got, want)
		}
	}
}
