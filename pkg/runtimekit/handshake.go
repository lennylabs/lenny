// SPDX-License-Identifier: MIT

package runtimekit

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// ManifestEnvVar names the environment variable that overrides the adapter
// manifest path. A runtime that has no other source for the path reads
// DefaultManifestPath when it is unset or empty.
const ManifestEnvVar = "LENNY_ADAPTER_MANIFEST"

// DefaultManifestPath is the adapter manifest's location in the runtime
// container. spec: §4.7.6 (Adapter Manifest Field Reference).
const DefaultManifestPath = "/run/lenny/adapter-manifest.json"

// Handshake line keys. The nonce line, the challenge, and the challenge
// response are single-member JSON objects that precede the channel's framed
// protocol and are outside its frame schema. spec: §4.7.11 (Runtime
// connection handshake).
const (
	nonceKey             = "_lennyNonce"
	challengeKey         = "_lennyChallenge"
	challengeResponseKey = "_lennyChallengeResponse"
)

// handshakePollInterval is the pause between two reads of a manifest that is
// not yet published, and before each redial after the adapter closes a
// connection before its first protocol frame. The runtime keeps polling with
// no overall deadline, so the interval stays short.
const handshakePollInterval = 100 * time.Millisecond

// ManifestPath resolves the adapter manifest path from the environment:
// ManifestEnvVar when it is set, otherwise DefaultManifestPath.
func ManifestPath() string {
	if p := strings.TrimSpace(os.Getenv(ManifestEnvVar)); p != "" {
		return p
	}
	return DefaultManifestPath
}

// ChallengeResponse computes the answer to a nonce-only challenge:
// HMAC-SHA256 keyed by the manifest nonce over the challenge, lowercase hex.
// The CH-MSGSOCK and CH-RUNTIMEOPS listeners and the intra-pod MCP servers
// issue the same challenge, so every client answers it with this function.
// spec: §4.7.11 (Nonce-only fallback).
func ChallengeResponse(nonce, challenge string) string {
	mac := hmac.New(sha256.New, []byte(nonce))
	mac.Write([]byte(challenge))
	return hex.EncodeToString(mac.Sum(nil))
}

// ChallengeOf reports the challenge a handshake line carries. ok is false for
// any line that is not a JSON object with a string _lennyChallenge member,
// which includes every protocol frame.
func ChallengeOf(line []byte) (challenge string, ok bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(line, &obj); err != nil {
		return "", false
	}
	raw, present := obj[challengeKey]
	if !present {
		return "", false
	}
	if err := json.Unmarshal(raw, &challenge); err != nil {
		return "", false
	}
	return challenge, true
}

// ChallengeResponseLine returns the newline-terminated line that answers
// challenge for nonce.
func ChallengeResponseLine(nonce, challenge string) []byte {
	return handshakeLine(challengeResponseKey, ChallengeResponse(nonce, challenge))
}

// NonceLine returns the newline-terminated nonce line a runtime writes first
// on a CH-MSGSOCK or CH-RUNTIMEOPS connection.
func NonceLine(nonce string) []byte {
	return handshakeLine(nonceKey, nonce)
}

// handshakeLine encodes one single-member handshake object as a line.
func handshakeLine(key, value string) []byte {
	b, _ := json.Marshal(map[string]string{key: value})
	return append(b, '\n')
}

// ReadManifestNonce reads the mcpNonce member of the manifest at path. It
// returns an error when the file cannot be read or decoded, and the empty
// string when the manifest carries no nonce.
func ReadManifestNonce(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var m struct {
		MCPNonce string `json:"mcpNonce"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return "", fmt.Errorf("runtimekit: decode manifest %s: %w", path, err)
	}
	return m.MCPNonce, nil
}

// waitManifestNonce polls the manifest at path until it carries a nonce, with
// no deadline other than ctx.
func waitManifestNonce(ctx context.Context, path string) (string, error) {
	for {
		if nonce, err := ReadManifestNonce(path); err == nil && nonce != "" {
			return nonce, nil
		}
		if err := pause(ctx, handshakePollInterval); err != nil {
			return "", err
		}
	}
}

// pause waits for d or until ctx ends.
func pause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// DialAuthenticated dials the adapter's CH-MSGSOCK or CH-RUNTIMEOPS socket
// and performs the runtime half of the runtime connection handshake. Before
// the dial it reads the manifest at manifestPath, polling until the file
// carries a nonce, and its first write on the connection is the nonce line.
// The caller resolves manifestPath once and passes it; DialAuthenticated
// resolves no path itself.
//
// The adapter writes the first protocol frame on both channels, so the
// handshake completes inside the returned connection's Read: until the
// first protocol frame arrives, Read answers each _lennyChallenge with the
// HMAC of the nonce this connection presented, and when the adapter closes
// the connection, which it does for a nonce the manifest has since
// replaced, Read reads the manifest again, redials, and writes the new nonce
// line, with no overall deadline. The first protocol frame and everything
// after it pass through unchanged. A runtime therefore writes nothing on the
// connection before it has read the adapter's first frame.
//
// ctx bounds the first dial only. Close ends the connection and any redial.
// spec: §4.7.11 (Runtime connection handshake), §4.7.6 (mcpNonce row).
func DialAuthenticated(ctx context.Context, socket, manifestPath string) (net.Conn, error) {
	life, stop := context.WithCancel(context.Background())
	c := &authConn{socket: socket, manifestPath: manifestPath, life: life, stop: stop}
	if err := c.dial(ctx); err != nil {
		stop()
		return nil, err
	}
	return c, nil
}

// authConn is a runtime-side connection whose Read completes the runtime
// connection handshake before it yields the first protocol frame.
type authConn struct {
	socket       string
	manifestPath string
	// life ends at Close, which stops any redial in progress.
	life context.Context
	stop context.CancelFunc

	// mu guards conn, br, and nonce, which a redial replaces.
	mu    sync.Mutex
	conn  net.Conn
	br    *bufio.Reader
	nonce string

	// readMu serializes Read, which owns established and pending.
	readMu sync.Mutex
	// established records that the first protocol frame has arrived.
	established bool
	// pending holds the unread rest of the first protocol frame.
	pending []byte
}

// dial reads the published nonce, dials the socket, and writes the nonce
// line. A write failure means the adapter closed the connection at once, so
// dial reads the manifest again and redials.
func (c *authConn) dial(ctx context.Context) error {
	for {
		nonce, err := waitManifestNonce(ctx, c.manifestPath)
		if err != nil {
			return fmt.Errorf("runtimekit: read manifest nonce from %s: %w", c.manifestPath, err)
		}
		conn, err := DialSocket(ctx, c.socket)
		if err != nil {
			return fmt.Errorf("runtimekit: dial %q: %w", c.socket, err)
		}
		if _, err := conn.Write(NonceLine(nonce)); err != nil {
			_ = conn.Close()
			if perr := pause(ctx, handshakePollInterval); perr != nil {
				return perr
			}
			continue
		}
		c.mu.Lock()
		if c.life.Err() != nil {
			c.mu.Unlock()
			_ = conn.Close()
			return net.ErrClosed
		}
		c.conn, c.br, c.nonce = conn, bufio.NewReader(conn), nonce
		c.mu.Unlock()
		return nil
	}
}

// current returns the live connection, its reader, and the nonce it
// presented.
func (c *authConn) current() (net.Conn, *bufio.Reader, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn, c.br, c.nonce
}

// Read returns the adapter's bytes once the handshake has completed. See
// DialAuthenticated.
func (c *authConn) Read(p []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	if !c.established {
		if err := c.awaitFirstFrame(); err != nil {
			return 0, err
		}
	}
	if len(c.pending) > 0 {
		n := copy(p, c.pending)
		c.pending = c.pending[n:]
		return n, nil
	}
	_, br, _ := c.current()
	return br.Read(p)
}

// awaitFirstFrame reads handshake lines until the first protocol frame,
// answering each challenge and redialing whenever the adapter closes the
// connection first.
func (c *authConn) awaitFirstFrame() error {
	for {
		conn, br, nonce := c.current()
		line, err := br.ReadBytes('\n')
		if err != nil {
			if c.life.Err() != nil {
				return net.ErrClosed
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return err
			}
			_ = conn.Close()
			if perr := pause(c.life, handshakePollInterval); perr != nil {
				return net.ErrClosed
			}
			if derr := c.dial(c.life); derr != nil {
				return derr
			}
			continue
		}
		if challenge, ok := ChallengeOf(line); ok {
			// A failed write surfaces as a read error on the next pass,
			// which redials.
			_, _ = conn.Write(ChallengeResponseLine(nonce, challenge))
			continue
		}
		c.established = true
		c.pending = line
		return nil
	}
}

// Write writes to the live connection.
func (c *authConn) Write(p []byte) (int, error) {
	conn, _, _ := c.current()
	return conn.Write(p)
}

// Close ends any redial and closes the live connection.
func (c *authConn) Close() error {
	c.stop()
	conn, _, _ := c.current()
	return conn.Close()
}

// LocalAddr implements net.Conn on the live connection.
func (c *authConn) LocalAddr() net.Addr { conn, _, _ := c.current(); return conn.LocalAddr() }

// RemoteAddr implements net.Conn on the live connection.
func (c *authConn) RemoteAddr() net.Addr { conn, _, _ := c.current(); return conn.RemoteAddr() }

// SetDeadline implements net.Conn on the live connection.
func (c *authConn) SetDeadline(t time.Time) error {
	conn, _, _ := c.current()
	return conn.SetDeadline(t)
}

// SetReadDeadline implements net.Conn on the live connection.
func (c *authConn) SetReadDeadline(t time.Time) error {
	conn, _, _ := c.current()
	return conn.SetReadDeadline(t)
}

// SetWriteDeadline implements net.Conn on the live connection.
func (c *authConn) SetWriteDeadline(t time.Time) error {
	conn, _, _ := c.current()
	return conn.SetWriteDeadline(t)
}
