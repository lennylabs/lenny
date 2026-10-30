// SPDX-License-Identifier: MIT

package adapter

import (
	"bufio"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter/mcp"
)

// Sentinel errors for the runtime connection handshake on the CH-MSGSOCK and
// CH-RUNTIMEOPS listeners. A connection that produces any of them, or a read
// error or timeout, is closed with no protocol response, and the listener
// accepts the next connection. spec: §4.7.11 (Runtime connection handshake).
var (
	// errNoManifestPublished reports a connection accepted while no manifest
	// is published, so there is no mcpNonce to compare the nonce line with.
	errNoManifestPublished = errors.New("adapter: no adapter manifest is published")
	// errNonceLineMissing reports a first line that carries no _lennyNonce
	// string member.
	errNonceLineMissing = errors.New("adapter: first line carries no _lennyNonce")
	// errNonceMismatch reports a nonce line whose value differs from the
	// published mcpNonce.
	errNonceMismatch = errors.New("adapter: _lennyNonce does not match the published mcpNonce")
)

// authenticateRuntimeConn runs the adapter half of the runtime connection
// handshake on a freshly accepted CH-MSGSOCK or CH-RUNTIMEOPS connection.
// It reads the runtime's first line through br within mcp.ChallengeTimeout
// and compares its _lennyNonce, in constant time, with the mcpNonce that
// nonce reports for the manifest published at that moment. An empty value
// means no manifest is published, and the connection is refused. In
// nonce-only mode it then writes a fresh _lennyChallenge and validates the
// runtime's HMAC-SHA256 answer, read within mcp.ChallengeTimeout, through the
// MCP servers' challenge code.
//
// br must be the reader the caller keeps for the connection: the runtime may
// send its first protocol frame in the same write as the nonce line, and
// those bytes stay buffered in br for the channel's frame reader. The read
// deadline is cleared on return. A nil error admits the connection; the
// caller closes it on any error.
//
// spec: §4.7.11 (Runtime connection handshake, Nonce-only fallback), §4.7.6
// (mcpNonce row).
func authenticateRuntimeConn(conn net.Conn, br *bufio.Reader, nonce func() string, nonceOnly bool) error {
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	line, err := readHandshakeLine(conn, br)
	if err != nil {
		return fmt.Errorf("adapter: read nonce line: %w", err)
	}
	want := nonce()
	if want == "" {
		return errNoManifestPublished
	}
	got, err := handshakeMember(line, mcp.NonceParamKey)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		return errNonceMismatch
	}
	if !nonceOnly {
		return nil
	}
	return answerChallenge(conn, br, want)
}

// answerChallenge runs the nonce-only challenge-response: it writes a fresh
// _lennyChallenge and validates the runtime's _lennyChallengeResponse
// against HMAC-SHA256(key=nonce, data=challenge). spec: §4.7.11 (Nonce-only
// fallback).
func answerChallenge(conn net.Conn, br *bufio.Reader, nonce string) error {
	challenge, err := mcp.NewChallenge()
	if err != nil {
		return err
	}
	frame, err := json.Marshal(map[string]string{mcp.ChallengeParamKey: challenge})
	if err != nil {
		return fmt.Errorf("adapter: encode challenge: %w", err)
	}
	if _, err := conn.Write(append(frame, '\n')); err != nil {
		return fmt.Errorf("adapter: write challenge: %w", err)
	}
	response, err := readHandshakeLine(conn, br)
	if err != nil {
		return fmt.Errorf("adapter: read challenge response: %w", err)
	}
	return mcp.ValidateChallengeResponse(response, nonce, challenge)
}

// readHandshakeLine reads one handshake line under a fresh
// mcp.ChallengeTimeout read deadline. The line is bounded by br's buffer, so
// a peer that never sends a newline cannot grow it without limit.
func readHandshakeLine(conn net.Conn, br *bufio.Reader) ([]byte, error) {
	if err := conn.SetReadDeadline(time.Now().Add(mcp.ChallengeTimeout)); err != nil {
		return nil, fmt.Errorf("set handshake deadline: %w", err)
	}
	line, err := br.ReadSlice('\n')
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), line...), nil
}

// handshakeMember decodes line as a JSON object and returns its string
// member key. A line that is not such an object reports errNonceLineMissing.
func handshakeMember(line []byte, key string) (string, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(line, &obj); err != nil {
		return "", errNonceLineMissing
	}
	raw, ok := obj[key]
	if !ok {
		return "", errNonceLineMissing
	}
	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", errNonceLineMissing
	}
	return v, nil
}

// PublishedManifestNonce returns the nonce provider the CH-MSGSOCK and
// CH-RUNTIMEOPS listeners take: a function that reports the mcpNonce of the
// manifest currently published in dir, or the empty string when no manifest
// is published there or it cannot be read. Both listeners are bound at
// adapter boot, before any manifest exists, so they read the nonce at accept
// time rather than at construction. Reading the published file, rather than
// a copy the writer keeps, compares against exactly the document a runtime
// can read, including when two starts publish at once.
// spec: §4.7.11 (Runtime connection handshake), §4.7.6 (mcpNonce row).
func PublishedManifestNonce(dir string) func() string {
	return func() string {
		if dir == "" {
			return ""
		}
		m, err := ReadManifest(dir)
		if err != nil {
			return ""
		}
		return m.MCPNonce
	}
}
