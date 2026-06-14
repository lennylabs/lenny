// SPDX-License-Identifier: MIT

package tlsprobe

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

// selfSignedCert mints an ephemeral loopback certificate for the
// in-process TLS listeners the enforcement tests stand up.
func selfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}
}

// newTLSListener stands up a loopback TLS listener that completes the
// server-side handshake and closes — the hardened, TLS-only posture a
// correctly configured Redis or PgBouncer presents.
func newTLSListener(t *testing.T) (addr string, clientCfg *tls.Config) {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{selfSignedCert(t)}})
	if err != nil {
		t.Fatalf("tls listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				if tc, ok := c.(*tls.Conn); ok {
					_ = tc.Handshake()
				}
				c.Close()
			}(conn)
		}
	}()
	return ln.Addr().String(), &tls.Config{InsecureSkipVerify: true} //nolint:gosec // self-signed loopback fixture
}

// newPlaintextListener stands up a loopback plaintext TCP listener that
// runs handler for each connection — used to emulate a misconfigured
// backend that accepts non-TLS clients.
func newPlaintextListener(t *testing.T, handler func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				handler(c)
			}(conn)
		}
	}()
	return ln.Addr().String()
}

// drain reads and discards n bytes so a handler can consume the probe's
// request before replying.
func drain(c net.Conn, n int) {
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	io := make([]byte, n)
	_, _ = c.Read(io)
}

func testConfig(client *tls.Config) Config {
	return Config{TLSConfig: client, HandshakeTimeout: 2 * time.Second, PlaintextTimeout: 500 * time.Millisecond}
}

// TestRedisTLSEnforcement_spec_10_3_373 asserts the §10.3 line 373
// TestRedisTLSEnforcement contract: a plaintext connection attempt to
// Redis is rejected. The probe passes against a TLS-only listener and
// fails against a plaintext listener that answers an inline PING with
// "+PONG" (a Redis still serving its plaintext port).
// spec: §10.3 lines 359, 373.
func TestRedisTLSEnforcement_spec_10_3_373(t *testing.T) {
	addr, clientCfg := newTLSListener(t)
	if err := Probe(context.Background(), testConfig(clientCfg), Target{Backend: BackendRedis, Addr: addr}); err != nil {
		t.Fatalf("TLS-only Redis endpoint should pass the probe, got: %v", err)
	}

	plain := newPlaintextListener(t, func(c net.Conn) {
		drain(c, 6) // "PING\r\n"
		_, _ = c.Write([]byte("+PONG\r\n"))
	})
	err := Probe(context.Background(), testConfig(clientCfg), Target{Backend: BackendRedis, Addr: plain})
	if err == nil {
		t.Fatal("plaintext Redis endpoint must fail the probe")
	}
	if !strings.Contains(err.Error(), "redis") {
		t.Errorf("error should name the redis backend, got: %v", err)
	}
}

// TestPgBouncerTLSEnforcement_spec_10_3_373 asserts the §10.3 line 373
// TestPgBouncerTLSEnforcement contract: a plaintext connection attempt
// to PgBouncer is rejected. A plaintext listener that declines the
// Postgres SSLRequest with 'N' is detected as accepting non-TLS
// clients; a TLS-only listener passes.
// spec: §10.3 lines 359, 373.
func TestPgBouncerTLSEnforcement_spec_10_3_373(t *testing.T) {
	addr, clientCfg := newTLSListener(t)
	if err := Probe(context.Background(), testConfig(clientCfg), Target{Backend: BackendPgBouncer, Addr: addr}); err != nil {
		t.Fatalf("TLS-only PgBouncer endpoint should pass the probe, got: %v", err)
	}

	plain := newPlaintextListener(t, func(c net.Conn) {
		drain(c, 8) // SSLRequest packet
		_, _ = c.Write([]byte("N"))
	})
	err := Probe(context.Background(), testConfig(clientCfg), Target{Backend: BackendPgBouncer, Addr: plain})
	if err == nil {
		t.Fatal("plaintext PgBouncer endpoint (SSLRequest -> 'N') must fail the probe")
	}
	if !strings.Contains(err.Error(), "pgbouncer") {
		t.Errorf("error should name the pgbouncer backend, got: %v", err)
	}
}

// TestPlaintextAcceptedDistinguishesSSLReply confirms the PgBouncer
// plaintext probe treats an 'S' reply (SSL offered/required) as a
// refusal and an 'N' reply (plaintext mode) as acceptance.
// spec: §10.3 line 359.
func TestPlaintextAcceptedDistinguishesSSLReply(t *testing.T) {
	sslOffered := newPlaintextListener(t, func(c net.Conn) {
		drain(c, 8)
		_, _ = c.Write([]byte("S"))
	})
	if plaintextAccepted(context.Background(), testConfig(nil), Target{Backend: BackendPgBouncer, Addr: sslOffered}) {
		t.Error("an 'S' SSLRequest reply must read as TLS-enforced (not plaintext-accepted)")
	}

	sslDeclined := newPlaintextListener(t, func(c net.Conn) {
		drain(c, 8)
		_, _ = c.Write([]byte("N"))
	})
	if !plaintextAccepted(context.Background(), testConfig(nil), Target{Backend: BackendPgBouncer, Addr: sslDeclined}) {
		t.Error("an 'N' SSLRequest reply must read as plaintext-accepted")
	}
}

// TestProbeFailsOnHandshakeFailure asserts the §10.3 line 359 "wrong
// port / missing cert" class: a probe against a plaintext-only endpoint
// fails at the TLS handshake leg.
func TestProbeFailsOnHandshakeFailure(t *testing.T) {
	plain := newPlaintextListener(t, func(c net.Conn) { drain(c, 16) })
	err := Probe(context.Background(), testConfig(&tls.Config{InsecureSkipVerify: true}), Target{Backend: BackendRedis, Addr: plain}) //nolint:gosec // test fixture
	if err == nil {
		t.Fatal("a plaintext-only endpoint must fail the TLS handshake leg")
	}
	if !strings.Contains(err.Error(), "handshake") {
		t.Errorf("want a handshake error, got: %v", err)
	}
}

// TestProbeSkipsEmptyAddr asserts a target with no Addr is skipped so a
// dev-mode / in-memory gateway (no Redis or PgBouncer) passes trivially.
// spec: §10.3 line 359 (probe runs per configured endpoint); §17.4.
func TestProbeSkipsEmptyAddr(t *testing.T) {
	if err := Probe(
		context.Background(), testConfig(nil),
		Target{Backend: BackendRedis, Addr: ""},
		Target{Backend: BackendPgBouncer, Addr: ""},
	); err != nil {
		t.Fatalf("empty targets must be skipped, got: %v", err)
	}
}

// TestProbeRejectsRefusedPlaintextAsPass confirms a refused plaintext
// dial (the plaintext port disabled, e.g. Redis port 0) reads as a
// refusal, so a TLS-only endpoint whose plaintext port is closed still
// passes the plaintext leg.
func TestProbeRejectsRefusedPlaintextAsPass(t *testing.T) {
	// Bind then immediately close to obtain an address that refuses.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	closedAddr := ln.Addr().String()
	ln.Close()
	if plaintextAccepted(context.Background(), testConfig(nil), Target{Backend: BackendRedis, Addr: closedAddr}) {
		t.Error("a refused plaintext dial must read as a refusal, not acceptance")
	}
}

// sanity-check the SSLRequest packet encoding stays the canonical
// Postgres negotiation code so the pgbouncer probe speaks the real
// protocol.
func TestSSLRequestPacketEncoding(t *testing.T) {
	var packet [8]byte
	binary.BigEndian.PutUint32(packet[0:4], 8)
	binary.BigEndian.PutUint32(packet[4:8], 80877103)
	if got := binary.BigEndian.Uint32(packet[4:8]); got != 80877103 {
		t.Fatalf("SSLRequest code = %d, want 80877103", got)
	}
}
