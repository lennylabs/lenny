// SPDX-License-Identifier: MIT

// Package gatewaypki mints the test certificate material of the gateway
// replica certificate: a cluster CA and a gateway leaf carrying the
// gatewaycontrol.GatewayDNSName SAN, written as PEM files under the test's
// temporary directory. A gateway started with this material outside dev
// mode passes the §10.3 NET-072 startup check and serves the
// CH-HOLDERFORWARD listener. Leaf mints further leaves from the same CA, for
// a negative case such as a cluster-CA certificate with another DNS SAN or
// an agent pod's SPIFFE certificate.
package gatewaypki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
)

// Material is a minted cluster CA and gateway leaf.
type Material struct {
	// CAFile, CertFile, and KeyFile are the PEM paths of the CA bundle,
	// the gateway leaf certificate, and its private key, in the form the
	// --adapter-ca, --adapter-tls-cert, and --adapter-tls-key flags take.
	CAFile, CertFile, KeyFile string
	// CAPool holds the CA certificate.
	CAPool *x509.CertPool

	dir   string
	ca    *x509.Certificate
	caKey *ecdsa.PrivateKey
	seq   atomic.Int64
}

// LeafFiles are the PEM paths of a leaf certificate and its key.
type LeafFiles struct {
	CertFile, KeyFile string
}

// Mint mints a cluster CA and a gateway leaf certificate with the
// gatewaycontrol.GatewayDNSName DNS SAN and a 127.0.0.1 IP SAN, valid for
// client and server authentication, and writes them under t.TempDir().
func Mint(t testing.TB) *Material {
	t.Helper()
	caKey := newKey(t)
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "lenny-test-cluster-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("gatewaypki: create CA: %v", err)
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("gatewaypki: parse CA: %v", err)
	}
	m := &Material{dir: t.TempDir(), ca: ca, caKey: caKey, CAPool: x509.NewCertPool()}
	m.CAPool.AddCert(ca)
	m.CAFile = filepath.Join(m.dir, "ca.crt")
	writePEM(t, m.CAFile, "CERTIFICATE", der)
	leaf := m.Leaf(t, []string{gatewaycontrol.GatewayDNSName}, nil)
	m.CertFile, m.KeyFile = leaf.CertFile, leaf.KeyFile
	return m
}

// Leaf mints a leaf certificate from the material's CA with the given DNS
// and URI SANs and a 127.0.0.1 IP SAN, valid for client and server
// authentication, and writes it under the material's directory.
func (m *Material) Leaf(t testing.TB, dnsNames []string, uris []*url.URL) LeafFiles {
	t.Helper()
	n := m.seq.Add(1)
	key := newKey(t)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(n + 1),
		Subject:      pkix.Name{CommonName: "lenny-test-leaf"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:     dnsNames,
		URIs:         uris,
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, m.ca, &key.PublicKey, m.caKey)
	if err != nil {
		t.Fatalf("gatewaypki: create leaf: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("gatewaypki: marshal leaf key: %v", err)
	}
	files := LeafFiles{
		CertFile: filepath.Join(m.dir, "leaf-"+big.NewInt(n).String()+".crt"),
		KeyFile:  filepath.Join(m.dir, "leaf-"+big.NewInt(n).String()+".key"),
	}
	writePEM(t, files.CertFile, "CERTIFICATE", der)
	writePEM(t, files.KeyFile, "EC PRIVATE KEY", keyDER)
	return files
}

// newKey generates a P-256 key.
func newKey(t testing.TB) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gatewaypki: generate key: %v", err)
	}
	return key
}

// writePEM writes one PEM block to path.
func writePEM(t testing.TB, path, blockType string, der []byte) {
	t.Helper()
	data := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("gatewaypki: write %s: %v", path, err)
	}
}
