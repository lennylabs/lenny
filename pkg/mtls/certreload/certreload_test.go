// SPDX-License-Identifier: MIT

package certreload

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeKeypair generates a self-signed leaf for commonName and writes
// the cert and key PEM to certPath/keyPath. It returns the leaf's
// SerialNumber so a test can assert which generation the reloader
// currently serves.
func writeKeypair(t *testing.T, certPath, keyPath, commonName string, serial int64) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Unix(0, 0),
		NotAfter:     time.Unix(1<<31-1, 0),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
}

func leafSerial(t *testing.T, cert *tls.Certificate) int64 {
	t.Helper()
	if cert == nil || len(cert.Certificate) == 0 {
		t.Fatal("nil certificate")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	return leaf.SerialNumber.Int64()
}

// spec: §10.3 line 338 — a renewed keypair on disk is served on the next
// handshake without recreating the Reloader (no process restart).
func TestReloaderPicksUpRenewedCert_spec_10_3_338(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")
	writeKeypair(t, certPath, keyPath, "leaf-v1", 1)

	r, err := New(certPath, keyPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := r.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if s := leafSerial(t, got); s != 1 {
		t.Fatalf("initial serial = %d, want 1", s)
	}

	// Rewrite with a future modtime so the stat-based change detector
	// fires deterministically regardless of filesystem mtime resolution.
	writeKeypair(t, certPath, keyPath, "leaf-v2", 2)
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(certPath, future, future); err != nil {
		t.Fatalf("chtimes cert: %v", err)
	}
	if err := os.Chtimes(keyPath, future, future); err != nil {
		t.Fatalf("chtimes key: %v", err)
	}

	got, err = r.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate after renewal: %v", err)
	}
	if s := leafSerial(t, got); s != 2 {
		t.Fatalf("post-renewal serial = %d, want 2", s)
	}
}

// spec: §10.3 line 338 — an unchanged keypair is served from cache; the
// reloader does not re-parse on every handshake.
func TestReloaderServesCacheWhenUnchanged_spec_10_3_338(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")
	writeKeypair(t, certPath, keyPath, "leaf", 7)

	r, err := New(certPath, keyPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	first, _ := r.GetClientCertificate(nil)
	second, _ := r.GetClientCertificate(nil)
	if first != second {
		t.Fatal("expected the same cached *tls.Certificate pointer for an unchanged file")
	}
}

// A reload that fails to parse (mid-write keypair) keeps the last good
// certificate rather than serving an error. spec: §10.3 line 338.
func TestReloaderKeepsLastGoodOnReloadError_spec_10_3_338(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")
	writeKeypair(t, certPath, keyPath, "leaf", 5)

	r, err := New(certPath, keyPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Truncate the cert to invalid PEM with an advanced modtime.
	if err := os.WriteFile(certPath, []byte("not a pem"), 0o600); err != nil {
		t.Fatalf("corrupt cert: %v", err)
	}
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(certPath, future, future)

	got, err := r.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate must not error on a mid-write keypair: %v", err)
	}
	if s := leafSerial(t, got); s != 5 {
		t.Fatalf("serial after failed reload = %d, want last-good 5", s)
	}
}

func TestNewRejectsEmptyPaths(t *testing.T) {
	if _, err := New("", "k"); err == nil {
		t.Fatal("expected error for empty cert path")
	}
	if _, err := New("c", ""); err == nil {
		t.Fatal("expected error for empty key path")
	}
}
