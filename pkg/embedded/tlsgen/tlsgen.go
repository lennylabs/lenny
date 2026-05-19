// SPDX-License-Identifier: MIT

// Package tlsgen generates the self-signed TLS material for §17.4
// Embedded Mode. Each lenny up rotates a fresh CA and leaf certificate
// valid for 24 hours and writes them under the Embedded Mode state
// directory. The leaf certificate covers localhost and the loopback
// addresses so a client verifying against the CA accepts the gateway's
// HTTPS listener.
//
// This material is for local development only. The non-suppressible
// production warning banner printed by lenny up states that these
// credentials are insecure and must not be reused in production.
package tlsgen

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Validity is the lifetime of a generated leaf certificate. §17.4
// fixes this at 24 hours: lenny up rotates the material on every run.
const Validity = 24 * time.Hour

// Material is a generated CA plus leaf certificate and the on-disk
// paths they were written to.
type Material struct {
	// CACertPath is the PEM-encoded CA certificate. A client trusts
	// the gateway by adding this file to its trust store or passing
	// it as --cacert.
	CACertPath string
	// CertPath and KeyPath are the PEM-encoded leaf certificate and
	// its private key. The Embedded Mode TLS reverse proxy presents
	// them on the HTTPS listener.
	CertPath string
	KeyPath  string
	// NotAfter is the leaf certificate's expiry.
	NotAfter time.Time
}

// Generate writes a fresh CA and a localhost leaf certificate into
// dir. dir is created when absent. Any pre-existing ca.crt, tls.crt,
// and tls.key in dir are overwritten so each lenny up starts from
// rotated material.
func Generate(dir string) (Material, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Material{}, fmt.Errorf("tlsgen: create %s: %w", dir, err)
	}
	now := time.Now()
	notAfter := now.Add(Validity)

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Material{}, fmt.Errorf("tlsgen: generate CA key: %w", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "Lenny Embedded Mode Dev CA"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return Material{}, fmt.Errorf("tlsgen: sign CA: %w", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return Material{}, fmt.Errorf("tlsgen: parse CA: %w", err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Material{}, fmt.Errorf("tlsgen: generate leaf key: %w", err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return Material{}, fmt.Errorf("tlsgen: sign leaf: %w", err)
	}

	m := Material{
		CACertPath: filepath.Join(dir, "ca.crt"),
		CertPath:   filepath.Join(dir, "tls.crt"),
		KeyPath:    filepath.Join(dir, "tls.key"),
		NotAfter:   notAfter,
	}
	if err := writePEM(m.CACertPath, "CERTIFICATE", caDER, 0o644); err != nil {
		return Material{}, err
	}
	if err := writePEM(m.CertPath, "CERTIFICATE", leafDER, 0o644); err != nil {
		return Material{}, err
	}
	leafKeyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		return Material{}, fmt.Errorf("tlsgen: marshal leaf key: %w", err)
	}
	if err := writePEM(m.KeyPath, "EC PRIVATE KEY", leafKeyDER, 0o600); err != nil {
		return Material{}, err
	}
	return m, nil
}

// serial returns a random 128-bit certificate serial number.
func serial() *big.Int {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		// rand.Int only fails when the entropy source fails. Fall back
		// to a fixed serial so generation does not panic; the resulting
		// certificate is still self-signed dev material.
		return big.NewInt(1)
	}
	return n
}

// writePEM PEM-encodes der under the given block type and writes it to
// path with the given file mode.
func writePEM(path, blockType string, der []byte, mode os.FileMode) error {
	buf := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if err := os.WriteFile(path, buf, mode); err != nil {
		return fmt.Errorf("tlsgen: write %s: %w", path, err)
	}
	return nil
}
