// SPDX-License-Identifier: MIT

package runtimekit_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter/mcp"
	"github.com/lennylabs/lenny/pkg/runtimekit"
	"github.com/lennylabs/lenny/tests/testinfra/runtimenonce"
)

// fakeListener stands in for the adapter's CH-MSGSOCK or CH-RUNTIMEOPS
// listener and hands each accepted connection to the test.
func fakeListener(t *testing.T) (string, <-chan net.Conn) {
	t.Helper()
	socket := transportSocketAddr(t)
	addr := socket
	if strings.HasPrefix(socket, "@") {
		addr = "\x00" + socket[1:]
	}
	ln, err := net.Listen("unix", addr)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	conns := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = c.Close() })
			conns <- c
		}
	}()
	return socket, conns
}

// nextConn waits for the fake listener's next accepted connection.
func nextConn(t *testing.T, conns <-chan net.Conn) net.Conn {
	t.Helper()
	select {
	case c := <-conns:
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("the runtime did not dial")
		return nil
	}
}

// readLine reads one line from r within five seconds.
func readLine(t *testing.T, conn net.Conn, r *bufio.Reader) string {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return strings.TrimSpace(line)
}

// readFirstFrame reads the runtime side's first line in the background, so
// the fake adapter can drive the handshake in the foreground.
func readFirstFrame(conn net.Conn) <-chan string {
	got := make(chan string, 1)
	go func() {
		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			got <- "error: " + err.Error()
			return
		}
		got <- strings.TrimSpace(line)
	}()
	return got
}

// awaitFrame waits for the line readFirstFrame delivers.
func awaitFrame(t *testing.T, got <-chan string) string {
	t.Helper()
	select {
	case line := <-got:
		return line
	case <-time.After(5 * time.Second):
		t.Fatal("the runtime side read no first frame")
		return ""
	}
}

// spec: 4.7.11 (Runtime connection handshake, Nonce-only fallback)
//
// A challenge that arrives before the adapter's first protocol frame is
// answered with HMAC-SHA256 keyed by the nonce the connection presented,
// and the runtime side reads the protocol frame that follows, with the
// challenge consumed.
func TestDialAuthenticatedAnswersAChallengeBeforeTheFirstFrame_spec_4_7_11(t *testing.T) {
	socket, conns := fakeListener(t)
	manifest := runtimenonce.Publish(t, nil)
	conn, err := runtimekit.DialAuthenticated(context.Background(), socket, manifest.Path)
	if err != nil {
		t.Fatalf("DialAuthenticated: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	got := readFirstFrame(conn)

	adapter := nextConn(t, conns)
	r := bufio.NewReader(adapter)
	if err := runtimenonce.Check(adapter, r, manifest.Nonce); err != nil {
		t.Fatalf("nonce line: %v", err)
	}
	const challenge = "00112233445566778899aabbccddeeff"
	if _, err := adapter.Write([]byte(`{"_lennyChallenge":"` + challenge + `"}` + "\n")); err != nil {
		t.Fatalf("write challenge: %v", err)
	}
	answer := readLine(t, adapter, r)
	if err := mcp.ValidateChallengeResponse([]byte(answer), manifest.Nonce, challenge); err != nil {
		t.Fatalf("challenge answer %s: %v", answer, err)
	}
	if _, err := adapter.Write([]byte(`{"type":"session_start","sessionId":"s1"}` + "\n")); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	if line := awaitFrame(t, got); line != `{"type":"session_start","sessionId":"s1"}` {
		t.Fatalf("first frame the runtime side read = %s, want the session_start", line)
	}
}

// spec: 4.7.11 (Runtime connection handshake)
//
// When the adapter closes the connection before its first protocol frame,
// because the manifest's nonce has been replaced, the runtime side reads the
// manifest again, dials again, presents the new nonce, and reads the first
// frame on the new connection.
func TestDialAuthenticatedRedialsWithTheRereadNonce_spec_4_7_11(t *testing.T) {
	socket, conns := fakeListener(t)
	manifest := runtimenonce.Publish(t, nil)
	conn, err := runtimekit.DialAuthenticated(context.Background(), socket, manifest.Path)
	if err != nil {
		t.Fatalf("DialAuthenticated: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	got := readFirstFrame(conn)

	first := nextConn(t, conns)
	if err := runtimenonce.Check(first, bufio.NewReader(first), manifest.Nonce); err != nil {
		t.Fatalf("first nonce line: %v", err)
	}
	replaced := runtimenonce.NewNonce(t)
	runtimenonce.Rewrite(t, manifest.Path, replaced, nil)
	_ = first.Close()

	second := nextConn(t, conns)
	r := bufio.NewReader(second)
	if err := runtimenonce.Check(second, r, replaced); err != nil {
		t.Fatalf("redial nonce line: %v", err)
	}
	if _, err := second.Write([]byte(`{"type":"lifecycle_capabilities"}` + "\n")); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	if line := awaitFrame(t, got); line != `{"type":"lifecycle_capabilities"}` {
		t.Fatalf("first frame after the redial = %s, want lifecycle_capabilities", line)
	}
	// Writes after the handshake reach the redialed connection.
	if _, err := conn.Write([]byte(`{"type":"lifecycle_support"}` + "\n")); err != nil {
		t.Fatalf("write after redial: %v", err)
	}
	if line := readLine(t, second, r); line != `{"type":"lifecycle_support"}` {
		t.Fatalf("adapter read %s after the redial, want lifecycle_support", line)
	}
}

// spec: 4.7.11 (Runtime connection handshake)
//
// The runtime polls for a manifest that is not yet published and dials once
// it carries a nonce, presenting that nonce.
func TestDialAuthenticatedWaitsForTheManifest_spec_4_7_11(t *testing.T) {
	socket, conns := fakeListener(t)
	path := filepath.Join(t.TempDir(), "adapter-manifest.json")
	nonce := runtimenonce.NewNonce(t)
	go func() {
		time.Sleep(250 * time.Millisecond)
		b, _ := json.Marshal(map[string]any{"version": 1, "mcpNonce": nonce})
		_ = os.WriteFile(path, b, 0o600)
	}()
	conn, err := runtimekit.DialAuthenticated(context.Background(), socket, path)
	if err != nil {
		t.Fatalf("DialAuthenticated: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	adapter := nextConn(t, conns)
	if err := runtimenonce.Check(adapter, bufio.NewReader(adapter), nonce); err != nil {
		t.Fatalf("nonce line: %v", err)
	}
}

// spec: 4.7.11 (Runtime connection handshake)
//
// A cancelled context ends the wait for an unpublished manifest, and Close
// ends a connection whose handshake has not completed: a blocked Read
// returns rather than redialing.
func TestDialAuthenticatedStopsOnCancelAndClose_spec_4_7_11(t *testing.T) {
	socket, conns := fakeListener(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := runtimekit.DialAuthenticated(ctx, socket, filepath.Join(t.TempDir(), "absent.json")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("DialAuthenticated with no manifest = %v, want the context's deadline", err)
	}

	manifest := runtimenonce.Publish(t, nil)
	conn, err := runtimekit.DialAuthenticated(context.Background(), socket, manifest.Path)
	if err != nil {
		t.Fatalf("DialAuthenticated: %v", err)
	}
	_ = nextConn(t, conns)
	readErr := make(chan error, 1)
	go func() {
		_, err := conn.Read(make([]byte, 16))
		readErr <- err
	}()
	time.Sleep(50 * time.Millisecond)
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-readErr:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Read after Close = %v, want net.ErrClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Read did not return after Close")
	}
	if conn.LocalAddr() == nil || conn.RemoteAddr() == nil {
		t.Fatal("the connection reports no addresses")
	}
}

// spec: 4.7.11 (Runtime connection handshake)
//
// Only a single-member _lennyChallenge object is a challenge; a protocol
// frame, malformed JSON, and a non-string value are not.
func TestChallengeOfRecognizesOnlyAChallengeLine_spec_4_7_11(t *testing.T) {
	if c, ok := runtimekit.ChallengeOf([]byte(`{"_lennyChallenge":"ab"}`)); !ok || c != "ab" {
		t.Fatalf("ChallengeOf(challenge) = %q, %v", c, ok)
	}
	for _, line := range []string{`{"type":"message"}`, `not json`, `{"_lennyChallenge":1}`, `[]`} {
		if _, ok := runtimekit.ChallengeOf([]byte(line)); ok {
			t.Fatalf("ChallengeOf(%s) reported a challenge", line)
		}
	}
	if got, want := runtimekit.ChallengeResponse("n", "c"), mcp.ExpectedChallengeResponse("n", "c"); got != want {
		t.Fatalf("ChallengeResponse = %s, want the adapter's %s", got, want)
	}
	t.Setenv(runtimekit.ManifestEnvVar, "")
	if runtimekit.ManifestPath() != runtimekit.DefaultManifestPath {
		t.Fatalf("ManifestPath with the variable unset = %s", runtimekit.ManifestPath())
	}
}
