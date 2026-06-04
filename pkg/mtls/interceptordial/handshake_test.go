// SPDX-License-Identifier: MIT

package interceptordial_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc/credentials"

	"github.com/lennylabs/lenny/pkg/mtls/certreload"
	"github.com/lennylabs/lenny/pkg/mtls/interceptordial"
	"github.com/lennylabs/lenny/pkg/mtls/spiffe"
)

const handshakeTrustDomain = "lenny-acme-prod"

// ca bundles a self-signed CA and the helpers to mint leaf certificates
// chained to it.
type ca struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func newCA(t *testing.T) *ca {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ca key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "lenny-mtls-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("ca cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse ca: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &ca{cert: cert, key: key, pool: pool}
}

// leaf mints a leaf certificate signed by the CA carrying the given DNS
// and SPIFFE-URI SANs, returning a tls.Certificate the server presents.
func (c *ca) leaf(t *testing.T, dnsName, spiffeURI string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("leaf key: %v", err)
	}
	u, err := url.Parse(spiffeURI)
	if err != nil {
		t.Fatalf("parse spiffe uri: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: dnsName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{dnsName},
		URIs:         []*url.URL{u},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &key.PublicKey, c.key)
	if err != nil {
		t.Fatalf("leaf cert: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: nil}
}

// clientReloader writes a CA-signed client leaf to temp files and returns
// a certreload.Reloader serving it (the gateway leaf the dial presents).
func clientReloader(t *testing.T, c *ca) *certreload.Reloader {
	t.Helper()
	leaf := c.leaf(t, "lenny-gateway.lenny-system.svc", "spiffe://"+handshakeTrustDomain+"/agent/gateway/lenny-gateway")
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Certificate[0]})
	keyDER, err := x509.MarshalPKCS8PrivateKey(leaf.PrivateKey)
	if err != nil {
		t.Fatalf("marshal client key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("write client cert: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write client key: %v", err)
	}
	r, err := certreload.New(certPath, keyPath)
	if err != nil {
		t.Fatalf("certreload.New: %v", err)
	}
	return r
}

// spec: §10.3 line 328 (NET-063) — a correctly-issued in-cluster
// interceptor certificate (CA-chained, DNS SAN covering the pinned
// ServerName, SPIFFE URI in an allowed namespace) completes the
// handshake and records a `success` outcome on the §16.1 histogram.
func TestCredentialsHandshakeSuccessObservesMetric_spec_10_3_328(t *testing.T) {
	c := newCA(t)
	dnsName := "lenny-interceptor.acme-interceptors.svc"
	serverCert := c.leaf(t, dnsName, "spiffe://"+handshakeTrustDomain+"/interceptor/acme-interceptors/lenny-interceptor")

	var gotResult string
	creds := interceptordial.Credentials(interceptordial.Options{
		Reloader:   clientReloader(t, c),
		RootCAs:    c.pool,
		ServerName: dnsName,
		Verifier: &spiffe.InterceptorPeerVerifier{
			TrustDomain: handshakeTrustDomain,
			Namespaces:  []string{"acme-interceptors"},
		},
		Observe: func(result string, _ float64) { gotResult = result },
	})

	if err := doHandshake(t, c, serverCert, creds); err != nil {
		t.Fatalf("expected a valid interceptor handshake to succeed, got %v (result=%q)", err, gotResult)
	}
	if gotResult != interceptordial.ResultSuccess {
		t.Errorf("observed result = %q, want %q", gotResult, interceptordial.ResultSuccess)
	}
}

// spec: §10.3 line 328 — an interceptor whose SPIFFE namespace is outside
// the allowlist is rejected at the handshake (no gRPC frame) and recorded
// as `san_mismatch`, the impact the finding describes (a co-located pod
// in the interceptor namespace cannot impersonate the interceptor).
func TestCredentialsHandshakeRejectsWrongNamespace_spec_10_3_328(t *testing.T) {
	c := newCA(t)
	dnsName := "lenny-interceptor.evil-namespace.svc"
	serverCert := c.leaf(t, dnsName, "spiffe://"+handshakeTrustDomain+"/interceptor/evil-namespace/lenny-interceptor")

	var gotResult string
	creds := interceptordial.Credentials(interceptordial.Options{
		Reloader:   clientReloader(t, c),
		RootCAs:    c.pool,
		ServerName: dnsName,
		Verifier: &spiffe.InterceptorPeerVerifier{
			TrustDomain: handshakeTrustDomain,
			Namespaces:  []string{"acme-interceptors"},
		},
		Observe: func(result string, _ float64) { gotResult = result },
	})

	if err := doHandshake(t, c, serverCert, creds); err == nil {
		t.Fatal("expected a wrong-namespace interceptor certificate to be rejected")
	}
	if gotResult != interceptordial.ResultSANMismatch {
		t.Errorf("observed result = %q, want %q", gotResult, interceptordial.ResultSANMismatch)
	}
}

// spec: §10.3 line 328 — pinning ServerName makes Go's standard chain
// verification refuse a certificate whose DNS SAN does not cover the
// registered endpoint, recorded as `san_mismatch`.
func TestCredentialsHandshakeRejectsDNSSANMismatch_spec_10_3_328(t *testing.T) {
	c := newCA(t)
	// The certificate's DNS SAN is for a different service than the
	// pinned ServerName.
	serverCert := c.leaf(t, "other-svc.acme-interceptors.svc", "spiffe://"+handshakeTrustDomain+"/interceptor/acme-interceptors/lenny-interceptor")

	var gotResult string
	creds := interceptordial.Credentials(interceptordial.Options{
		Reloader:   clientReloader(t, c),
		RootCAs:    c.pool,
		ServerName: "lenny-interceptor.acme-interceptors.svc",
		Verifier: &spiffe.InterceptorPeerVerifier{
			TrustDomain: handshakeTrustDomain,
			Namespaces:  []string{"acme-interceptors"},
		},
		Observe: func(result string, _ float64) { gotResult = result },
	})

	if err := doHandshake(t, c, serverCert, creds); err == nil {
		t.Fatal("expected a DNS-SAN mismatch to be rejected by ServerName pinning")
	}
	if gotResult != interceptordial.ResultSANMismatch {
		t.Errorf("observed result = %q, want %q", gotResult, interceptordial.ResultSANMismatch)
	}
}

// doHandshake wires a tls.Server presenting serverCert to the
// interceptordial client credentials over net.Pipe and returns the
// client-side ClientHandshake error.
func doHandshake(t *testing.T, c *ca, serverCert tls.Certificate, creds credentials.TransportCredentials) error {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	go func() {
		srv := tls.Server(serverConn, &tls.Config{
			Certificates: []tls.Certificate{serverCert},
			ClientAuth:   tls.RequireAndVerifyClientCert,
			ClientCAs:    c.pool,
			MinVersion:   tls.VersionTLS13,
			// grpc's credentials.NewTLS enforces ALPN "h2" on the client;
			// the server must offer it or the handshake fails post-TLS.
			NextProtos: []string{"h2"},
		})
		_ = srv.HandshakeContext(context.Background())
		_ = srv.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := creds.ClientHandshake(ctx, "lenny-interceptor.acme-interceptors.svc", clientConn)
	return err
}
