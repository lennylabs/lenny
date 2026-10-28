// SPDX-License-Identifier: MIT

package gatewaypki

import (
	"crypto/tls"
	"crypto/x509"
	"net/url"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
)

// loadLeaf loads a minted leaf as a tls.Certificate and parses its leaf.
func loadLeaf(t *testing.T, f LeafFiles) *x509.Certificate {
	t.Helper()
	pair, err := tls.LoadX509KeyPair(f.CertFile, f.KeyFile)
	if err != nil {
		t.Fatalf("load key pair %s: %v", f.CertFile, err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf %s: %v", f.CertFile, err)
	}
	return leaf
}

// spec: §10.3 (mTLS PKI) NET-072, §29.3 (Interactive message send) Forwarding
//
// TestMintIssuesAGatewayLeafTheClusterCAVerifies_spec_10_3 asserts that the
// minted gateway leaf chains to the minted cluster CA for client and server
// authentication, carries the gateway DNS SAN and the loopback IP SAN, and
// loads as a key pair from the PEM files the gateway flags take; and that a
// further leaf from the same CA carries exactly the requested DNS and URI
// SANs under its own file names.
func TestMintIssuesAGatewayLeafTheClusterCAVerifies_spec_10_3(t *testing.T) {
	m := Mint(t)
	gw := loadLeaf(t, LeafFiles{CertFile: m.CertFile, KeyFile: m.KeyFile})
	for _, usage := range []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth} {
		if _, err := gw.Verify(x509.VerifyOptions{
			Roots: m.CAPool, DNSName: gatewaycontrol.GatewayDNSName, KeyUsages: []x509.ExtKeyUsage{usage},
		}); err != nil {
			t.Errorf("verify the gateway leaf for %v: %v", usage, err)
		}
	}
	if _, err := gw.Verify(x509.VerifyOptions{Roots: m.CAPool, DNSName: "127.0.0.1"}); err != nil {
		t.Errorf("verify the gateway leaf for the loopback IP SAN: %v", err)
	}
	if m.CAFile == "" {
		t.Error("CAFile is empty")
	}

	spiffe := &url.URL{Scheme: "spiffe", Host: "lenny.local", Path: "/ns/lenny-agents/sa/agent"}
	other := m.Leaf(t, []string{"other.lenny.svc"}, []*url.URL{spiffe})
	if other.CertFile == m.CertFile || other.KeyFile == m.KeyFile {
		t.Fatalf("second leaf reused the gateway leaf files: %+v", other)
	}
	leaf := loadLeaf(t, other)
	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != "other.lenny.svc" {
		t.Errorf("DNS SANs = %v, want [other.lenny.svc]", leaf.DNSNames)
	}
	if len(leaf.URIs) != 1 || leaf.URIs[0].String() != spiffe.String() {
		t.Errorf("URI SANs = %v, want [%s]", leaf.URIs, spiffe)
	}
	if leaf.SerialNumber.Cmp(gw.SerialNumber) == 0 {
		t.Errorf("second leaf serial %v repeats the gateway leaf serial", leaf.SerialNumber)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: m.CAPool, DNSName: gatewaycontrol.GatewayDNSName}); err == nil {
		t.Error("a leaf without the gateway DNS SAN verified as the gateway name")
	}
}
