// SPDX-License-Identifier: MIT

package tlsgen

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"testing"
	"time"
)

func TestGenerateWritesMaterial(t *testing.T) {
	dir := t.TempDir()
	m, err := Generate(dir)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, p := range []string{m.CACertPath, m.CertPath, m.KeyPath} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s to exist: %v", p, err)
		}
	}
	if m.NotAfter.Before(time.Now()) {
		t.Errorf("leaf certificate already expired: NotAfter=%s", m.NotAfter)
	}
	// §17.4 fixes the certificate validity at 24 hours.
	want := time.Now().Add(Validity)
	if diff := m.NotAfter.Sub(want); diff > time.Minute || diff < -time.Minute {
		t.Errorf("NotAfter %s not within a minute of %s", m.NotAfter, want)
	}
}

func TestGeneratedLeafVerifiesAgainstCA(t *testing.T) {
	dir := t.TempDir()
	m, err := Generate(dir)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	caPEM, err := os.ReadFile(m.CACertPath)
	if err != nil {
		t.Fatalf("read CA: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("AppendCertsFromPEM: CA not added")
	}
	keyPair, err := tls.LoadX509KeyPair(m.CertPath, m.KeyPath)
	if err != nil {
		t.Fatalf("LoadX509KeyPair: %v", err)
	}
	leaf, err := x509.ParseCertificate(keyPair.Certificate[0])
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	// The leaf must verify for localhost against the generated CA.
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: "localhost", Roots: pool}); err != nil {
		t.Errorf("leaf does not verify for localhost: %v", err)
	}
}

func TestGenerateRotatesOnEachCall(t *testing.T) {
	dir := t.TempDir()
	first, err := Generate(dir)
	if err != nil {
		t.Fatalf("Generate first: %v", err)
	}
	firstCert, err := os.ReadFile(first.CertPath)
	if err != nil {
		t.Fatalf("read first cert: %v", err)
	}
	second, err := Generate(dir)
	if err != nil {
		t.Fatalf("Generate second: %v", err)
	}
	secondCert, err := os.ReadFile(second.CertPath)
	if err != nil {
		t.Fatalf("read second cert: %v", err)
	}
	// §17.4 rotates the TLS material per lenny up; a second Generate
	// must produce a different leaf certificate.
	if string(firstCert) == string(secondCert) {
		t.Error("expected rotated leaf certificate, got identical material")
	}
}
