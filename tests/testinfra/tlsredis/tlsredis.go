// SPDX-License-Identifier: MIT

// Package tlsredis starts an in-process Redis that requires AUTH and
// serves TLS on a self-signed certificate, the §12.4 posture a process
// without a --redis-allow-insecure opt-out (the Token Service) insists
// on. A test hands the URL to every process of its stack and reads the
// pub/sub traffic through Client.
package tlsredis

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// Server is a running AUTH-and-TLS Redis.
type Server struct {
	// URL is a rediss:// URL carrying the password, which skips
	// certificate verification because the certificate is self-signed.
	URL string
	// Client is a go-redis client on the server, closed at cleanup.
	Client *redis.Client
	// Mini is the underlying in-process server.
	Mini *miniredis.Miniredis
}

// Start starts the server with password and stops it at test cleanup.
//
// spec: §12.4 (Redis AUTH and TLS are required).
func Start(t testing.TB, password string) *Server {
	t.Helper()
	cert := selfSigned(t)
	mr := miniredis.NewMiniRedis()
	mr.RequireAuth(password)
	if err := mr.StartTLS(&tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}); err != nil {
		t.Fatalf("tlsredis: start: %v", err)
	}
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{
		Addr:     mr.Addr(),
		Password: password,
		// The certificate is self-signed for this test.
		TLSConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}, //nolint:gosec // self-signed test server
	})
	t.Cleanup(func() { _ = client.Close() })
	return &Server{
		URL:    "rediss://:" + password + "@" + mr.Addr() + "/0?skip_verify=true",
		Client: client,
		Mini:   mr,
	}
}

// selfSigned returns a P-256 certificate for 127.0.0.1 valid for an hour.
func selfSigned(t testing.TB) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("tlsredis: generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("tlsredis: create certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
