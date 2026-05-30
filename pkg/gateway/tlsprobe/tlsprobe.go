// SPDX-License-Identifier: MIT

// Package tlsprobe implements the §10.3 line 359 gateway startup TLS
// probe. Before a gateway replica is marked ready it verifies that a
// TLS handshake to Redis and PgBouncer succeeds and that a plaintext
// connection to the same endpoint is refused. A misconfigured backend
// (wrong port, missing certificate, or a plaintext listener left open)
// becomes a deployment failure caught before traffic is served rather
// than a silent runtime failure.
//
// The probe runs two legs per target:
//
//  1. A TLS handshake to the endpoint, which must succeed. This catches
//     the "wrong port / missing cert" class the spec calls out.
//  2. A plaintext (non-TLS) protocol exchange, which must be refused.
//     Redis and PgBouncer speak different wire protocols, so "is
//     plaintext accepted?" is answered per backend: a Redis listener is
//     PINGed inline; a PgBouncer listener is asked to negotiate SSL via
//     the Postgres SSLRequest packet. Only a positive confirmation that
//     the server completed a plaintext exchange fails the probe; every
//     dial/IO error is read as a refusal (the desired TLS-only posture).
package tlsprobe

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"
)

// Backend selects which plaintext-rejection protocol probe to run
// against a target.
type Backend string

const (
	// BackendRedis probes a Redis endpoint with an inline RESP PING.
	BackendRedis Backend = "redis"
	// BackendPgBouncer probes a PgBouncer/Postgres endpoint with the
	// Postgres SSLRequest negotiation packet.
	BackendPgBouncer Backend = "pgbouncer"
)

// Target is one endpoint the startup probe checks.
type Target struct {
	Backend Backend
	// Addr is the host:port of the TLS listener (the same endpoint the
	// gateway uses for its data-plane connection).
	Addr string
}

// Config holds the probe's TLS client material and per-leg timeouts.
type Config struct {
	// TLSConfig is the client config used for the handshake leg. A nil
	// value uses an empty *tls.Config (system roots, SNI from Addr).
	TLSConfig *tls.Config
	// HandshakeTimeout bounds the TLS handshake leg. Non-positive uses
	// DefaultHandshakeTimeout.
	HandshakeTimeout time.Duration
	// PlaintextTimeout bounds the plaintext-rejection leg. Non-positive
	// uses DefaultPlaintextTimeout.
	PlaintextTimeout time.Duration
}

// DefaultHandshakeTimeout and DefaultPlaintextTimeout bound each probe
// leg when Config leaves them unset.
const (
	DefaultHandshakeTimeout = 5 * time.Second
	DefaultPlaintextTimeout = 3 * time.Second
)

func (c Config) handshakeTimeout() time.Duration {
	if c.HandshakeTimeout > 0 {
		return c.HandshakeTimeout
	}
	return DefaultHandshakeTimeout
}

func (c Config) plaintextTimeout() time.Duration {
	if c.PlaintextTimeout > 0 {
		return c.PlaintextTimeout
	}
	return DefaultPlaintextTimeout
}

// Probe runs the §10.3 startup probe against every target in order and
// returns the first failure. A nil return means every target completed
// a TLS handshake and refused a plaintext connection. A target with an
// empty Addr is skipped (the backend is not configured on this
// deployment), so a dev-mode / in-memory gateway passes trivially.
func Probe(ctx context.Context, cfg Config, targets ...Target) error {
	for _, t := range targets {
		if t.Addr == "" {
			continue
		}
		if err := probeOne(ctx, cfg, t); err != nil {
			return fmt.Errorf("tlsprobe: %s endpoint %s: %w", t.Backend, t.Addr, err)
		}
	}
	return nil
}

func probeOne(ctx context.Context, cfg Config, t Target) error {
	if err := tlsHandshake(ctx, cfg, t.Addr); err != nil {
		return fmt.Errorf("TLS handshake failed (wrong port or missing/invalid certificate?): %w", err)
	}
	if accepted := plaintextAccepted(ctx, cfg, t); accepted {
		return errors.New("a plaintext connection was accepted; the backend must reject non-TLS clients (Redis tls-port with port 0; PgBouncer client_tls_sslmode=require)")
	}
	return nil
}

// tlsHandshake dials addr and completes a TLS handshake, which must
// succeed.
func tlsHandshake(ctx context.Context, cfg Config, addr string) error {
	ctx, cancel := context.WithTimeout(ctx, cfg.handshakeTimeout())
	defer cancel()
	d := tls.Dialer{NetDialer: &net.Dialer{}, Config: cfg.TLSConfig}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return conn.Close()
}

// plaintextAccepted reports whether the target's endpoint completes a
// plaintext (non-TLS) protocol exchange. It returns true only on a
// positive confirmation; every dial or IO error is read as a refusal so
// a hardened TLS-only listener (which fails the plaintext exchange)
// passes.
func plaintextAccepted(ctx context.Context, cfg Config, t Target) bool {
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", t.Addr)
	if err != nil {
		return false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(cfg.plaintextTimeout()))
	switch t.Backend {
	case BackendRedis:
		return redisPlaintextPong(conn)
	case BackendPgBouncer:
		return pgPlaintextNoSSL(conn)
	default:
		return false
	}
}

// redisPlaintextPong writes an inline RESP PING and reports whether the
// server answered "+PONG". A TLS-only Redis listener treats the inline
// command as a malformed ClientHello and never replies "+PONG".
func redisPlaintextPong(conn net.Conn) bool {
	if _, err := conn.Write([]byte("PING\r\n")); err != nil {
		return false
	}
	buf := make([]byte, 7)
	n, err := readFull(conn, buf)
	if err != nil {
		return false
	}
	return string(buf[:n]) == "+PONG\r\n"
}

// pgPlaintextNoSSL sends the Postgres SSLRequest packet and reports
// whether the server declined SSL with a single 'N' byte — the signal
// that the listener accepts plaintext sessions. A TLS-enforcing
// PgBouncer (client_tls_sslmode=require) replies 'S' instead, and a raw
// TLS listener replies neither.
func pgPlaintextNoSSL(conn net.Conn) bool {
	// SSLRequest: int32 length (8) followed by the int32 request code
	// 80877103 (0x04d2162f), per the Postgres frontend/backend protocol.
	var packet [8]byte
	binary.BigEndian.PutUint32(packet[0:4], 8)
	binary.BigEndian.PutUint32(packet[4:8], 80877103)
	if _, err := conn.Write(packet[:]); err != nil {
		return false
	}
	reply := make([]byte, 1)
	if _, err := readFull(conn, reply); err != nil {
		return false
	}
	return reply[0] == 'N'
}

// readFull reads up to len(buf) bytes, returning the count actually
// read. Unlike io.ReadFull it tolerates a short read followed by EOF so
// a server that closes after a few bytes still yields what it sent.
func readFull(conn net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := conn.Read(buf[total:])
		total += n
		if err != nil {
			if total > 0 {
				return total, nil
			}
			return 0, err
		}
	}
	return total, nil
}
