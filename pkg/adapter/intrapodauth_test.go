// SPDX-License-Identifier: MIT

package adapter

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter/mcp"
	"github.com/lennylabs/lenny/tests/testinfra/runtimenonce"
)

// handshakeListener is one of the adapter's runtime listeners under test:
// CH-MSGSOCK or CH-RUNTIMEOPS, bound with a nonce provider and a posture.
type handshakeListener struct {
	name string
	// start binds the listener and starts accepting. It returns the socket
	// address and a function that asserts the listener serves conn: it
	// installed conn as the runtime's connection and frames flow on it.
	start func(t *testing.T, auth SocketPeerAuth, nonce func() string, logs *syncBuffer) (string, func(t *testing.T, conn net.Conn, r *bufio.Reader))
}

// handshakeListeners are the two runtime listeners the runtime connection
// handshake guards.
var handshakeListeners = []handshakeListener{
	{name: "CH-MSGSOCK", start: startHandshakeMsgsock},
	{name: "CH-RUNTIMEOPS", start: startHandshakeRuntimeOps},
}

// startHandshakeMsgsock binds a CH-MSGSOCK listener and runs one Start in
// the background, which returns once the accept loop installs a connection.
func startHandshakeMsgsock(t *testing.T, auth SocketPeerAuth, nonce func() string, logs *syncBuffer) (string, func(*testing.T, net.Conn, *bufio.Reader)) {
	t.Helper()
	sp, err := NewSocketRuntimeProcess(shortSocketName(t, "rt.sock"), auth, nonce)
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	sp.logger = slog.New(slog.NewJSONHandler(logs, nil))
	sp.AcceptTimeout = 30 * time.Second
	t.Cleanup(func() { _ = sp.CloseListener() })
	started := make(chan error, 1)
	go func() { started <- sp.Start(context.Background(), "alice") }()
	served := func(t *testing.T, conn net.Conn, r *bufio.Reader) {
		t.Helper()
		select {
		case err := <-started:
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("Start did not install the authenticated connection")
		}
		if err := sp.WriteEnvelope("alice", []byte(`{"type":"message","sessionId":"alice"}`)); err != nil {
			t.Fatalf("WriteEnvelope: %v", err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		line, err := r.ReadString('\n')
		if err != nil || !strings.Contains(line, `"sessionId":"alice"`) {
			t.Fatalf("runtime read = (%q, %v), want the alice envelope", line, err)
		}
	}
	return sp.SocketPath(), served
}

// startHandshakeRuntimeOps binds a CH-RUNTIMEOPS listener and runs it until
// the test ends.
func startHandshakeRuntimeOps(t *testing.T, auth SocketPeerAuth, nonce func() string, logs *syncBuffer) (string, func(*testing.T, net.Conn, *bufio.Reader)) {
	t.Helper()
	lc, err := NewRuntimeOps(shortSocketName(t, "ops.sock"), auth, nonce)
	if err != nil {
		t.Fatalf("NewRuntimeOps: %v", err)
	}
	lc.logger = slog.New(slog.NewJSONHandler(logs, nil))
	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- lc.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = lc.Close()
		<-runErr
	})
	served := func(t *testing.T, conn net.Conn, r *bufio.Reader) {
		t.Helper()
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		f, err := readLifecycleFrame(r)
		if err != nil || f.Type != "lifecycle_capabilities" {
			t.Fatalf("first frame = (%+v, %v), want lifecycle_capabilities", f, err)
		}
	}
	return lc.SocketPath(), served
}

// handshakePostures are the postures the handshake runs in: the default
// peer-checked posture, which writes no challenge, and nonce-only mode,
// which follows the nonce line with a challenge.
var handshakePostures = map[string]SocketPeerAuth{
	"peer-checked": {ExpectedUID: uint32(os.Getuid())},
	"nonce-only":   {ExpectedUID: uint32(os.Getuid()) + 1, NonceOnly: true},
}

// dialHandshake dials socket and writes payload as the connection's first
// bytes.
func dialHandshake(t *testing.T, socket string, payload []byte) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatalf("dial %s: %v", socket, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if len(payload) > 0 {
		if _, err := conn.Write(payload); err != nil {
			t.Fatalf("write first bytes: %v", err)
		}
	}
	return conn, bufio.NewReader(conn)
}

// requireHandshakeRefused asserts the adapter closed conn without writing a
// protocol frame. A nonce-only listener may have written its challenge
// before it refused the answer, so a challenge line is skipped.
func requireHandshakeRefused(t *testing.T, conn net.Conn, r *bufio.Reader, which string) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			if !isClosedByAdapter(err) {
				t.Fatalf("%s: read = %v, want the adapter to close the connection", which, err)
			}
			return
		}
		if _, cerr := handshakeMember(line, mcp.ChallengeParamKey); cerr != nil {
			t.Fatalf("%s: the adapter wrote %q on a connection it must refuse", which, line)
		}
	}
}

// answerChallengeWith reads the adapter's challenge from r and answers it
// with the HMAC keyed by key.
func answerChallengeWith(t *testing.T, conn net.Conn, r *bufio.Reader, key string) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	line, err := r.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read challenge: %v", err)
	}
	challenge, err := handshakeMember(line, mcp.ChallengeParamKey)
	if err != nil {
		t.Fatalf("first adapter line %q is not a challenge", line)
	}
	if _, err := conn.Write(challengeAnswerLine(key, challenge)); err != nil {
		t.Fatalf("write challenge answer: %v", err)
	}
}

// spec: 4.7.11 (Runtime connection handshake, Nonce-only fallback), 4.7.6
// (mcpNonce row)
//
// On both runtime listeners and in both postures, a connection whose first
// line carries no nonce, carries a nonce other than the published mcpNonce,
// or is silent past 500 ms is refused with no protocol frame, and in
// nonce-only mode so is a connection whose challenge answer is not the HMAC
// of the published nonce. Each refusal is followed by acceptance of the next
// good connection, which carries the published nonce and, in nonce-only
// mode, answers the challenge.
func TestRuntimeListenersRefuseAFailedHandshakeAndAcceptTheNext_spec_4_7_11(t *testing.T) {
	type badConn struct {
		name     string
		connect  func(t *testing.T, socket, nonce string) (net.Conn, *bufio.Reader)
		postures []string
	}
	cases := []badConn{
		{name: "missing nonce", postures: []string{"peer-checked", "nonce-only"}, connect: func(t *testing.T, socket, _ string) (net.Conn, *bufio.Reader) {
			return dialHandshake(t, socket, []byte(`{"type":"session_started","sessionId":"alice","startId":"st_1"}`+"\n"))
		}},
		{name: "wrong nonce", postures: []string{"peer-checked", "nonce-only"}, connect: func(t *testing.T, socket, _ string) (net.Conn, *bufio.Reader) {
			return dialHandshake(t, socket, runtimenonce.Line(runtimenonce.NewNonce(t)))
		}},
		{name: "silence past 500ms", postures: []string{"peer-checked", "nonce-only"}, connect: func(t *testing.T, socket, _ string) (net.Conn, *bufio.Reader) {
			return dialHandshake(t, socket, nil)
		}},
		{name: "bad HMAC", postures: []string{"nonce-only"}, connect: func(t *testing.T, socket, nonce string) (net.Conn, *bufio.Reader) {
			conn, r := dialHandshake(t, socket, runtimenonce.Line(nonce))
			answerChallengeWith(t, conn, r, runtimenonce.NewNonce(t))
			return conn, r
		}},
	}
	for _, l := range handshakeListeners {
		for posture, auth := range handshakePostures {
			for _, c := range cases {
				if !containsString(c.postures, posture) {
					continue
				}
				t.Run(l.name+"/"+posture+"/"+c.name, func(t *testing.T) {
					m := runtimenonce.Publish(t, nil)
					logs := &syncBuffer{}
					socket, served := l.start(t, auth, PublishedManifestNonce(m.Dir), logs)
					start := time.Now()
					bad, badR := c.connect(t, socket, m.Nonce)
					requireHandshakeRefused(t, bad, badR, c.name)
					if c.name == "silence past 500ms" && time.Since(start) < mcp.ChallengeTimeout {
						t.Fatalf("a silent connection was refused after %s, before the 500 ms bound", time.Since(start))
					}
					good, goodR := dialHandshake(t, socket, runtimenonce.Line(m.Nonce))
					if auth.NonceOnly {
						answerChallengeWith(t, good, goodR, m.Nonce)
					}
					served(t, good, goodR)
					if !strings.Contains(logs.String(), "_peer_refused") {
						t.Errorf("refusal log %q records no refusal", logs.String())
					}
					if strings.Contains(logs.String(), m.Nonce) {
						t.Error("the refusal log carries the published nonce")
					}
				})
			}
		}
	}
}

// spec: 4.7.11 (Runtime connection handshake)
//
// A connection accepted while no manifest is published is refused even
// when it sends a nonce line, and once a manifest is published the next
// connection carrying its nonce is served.
func TestRuntimeListenersRefuseAConnectionWhileNoManifestIsPublished_spec_4_7_11(t *testing.T) {
	for _, l := range handshakeListeners {
		t.Run(l.name, func(t *testing.T) {
			dir := t.TempDir()
			logs := &syncBuffer{}
			socket, served := l.start(t, handshakePostures["peer-checked"], PublishedManifestNonce(dir), logs)
			bad, badR := dialHandshake(t, socket, runtimenonce.Line(runtimenonce.NewNonce(t)))
			requireHandshakeRefused(t, bad, badR, "no manifest published")
			if !strings.Contains(logs.String(), errNoManifestPublished.Error()) {
				t.Errorf("refusal log %q does not name the missing manifest", logs.String())
			}
			m := runtimenonce.PublishIn(t, dir, nil)
			good, goodR := dialHandshake(t, socket, runtimenonce.Line(m.Nonce))
			served(t, good, goodR)
		})
	}
}

// spec: 4.7.11 (Runtime connection handshake), 28.5.3 (CH-MSGSOCK)
//
// The runtime may send its first protocol frame in the same write as the
// nonce line. The bytes the handshake read buffered past the nonce line
// reach an Output subscriber as the first frame.
func TestMsgsockFrameSentWithTheNonceLineReachesOutput_spec_4_7_11(t *testing.T) {
	m := runtimenonce.Publish(t, nil)
	sp, err := NewSocketRuntimeProcess(shortSocketName(t, "rt.sock"), handshakePostures["peer-checked"], PublishedManifestNonce(m.Dir))
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })
	// A first Start that times out starts the accept loop and leaves the
	// fan-out reader unstarted, so the subscriber below registers before
	// the reader scans the buffered frame.
	sp.AcceptTimeout = 50 * time.Millisecond
	if err := sp.Start(context.Background(), "alice"); err == nil {
		t.Fatal("the first Start returned with no runtime connected")
	}
	frame := `{"type":"session_started","sessionId":"alice","startId":"st_1"}`
	payload := append(runtimenonce.Line(m.Nonce), []byte(frame+"\n")...)
	_, _ = dialHandshake(t, sp.SocketPath(), payload)
	deadline := time.Now().Add(5 * time.Second)
	for !sp.ServesNextSession() {
		if time.Now().After(deadline) {
			t.Fatal("the accept loop did not install the authenticated connection")
		}
		time.Sleep(5 * time.Millisecond)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out, err := sp.Output(ctx, "alice")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	sp.AcceptTimeout = 5 * time.Second
	if err := sp.Start(context.Background(), "alice"); err != nil {
		t.Fatalf("Start on the installed connection: %v", err)
	}
	select {
	case got := <-out:
		if !bytes.Equal(got, []byte(frame)) {
			t.Fatalf("first frame on Output = %s, want %s", got, frame)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the frame sent in the nonce line's write never reached Output")
	}
}

// spec: 4.7.11 (Runtime connection handshake), 28.5.3 (CH-RUNTIMEOPS)
//
// CH-RUNTIMEOPS accepts a connection at any time from boot, before any
// Runtime.Start, and checks it against the manifest published at that
// moment: after a start replaces the published manifest, the earlier nonce
// is refused and the replacement is served.
func TestRuntimeOpsConnectionBeforeAnyStartIsCheckedAgainstThePublishedNonce_spec_4_7_11(t *testing.T) {
	m := runtimenonce.Publish(t, nil)
	stale := m.Nonce
	logs := &syncBuffer{}
	socket, served := startHandshakeRuntimeOps(t, handshakePostures["peer-checked"], PublishedManifestNonce(m.Dir), logs)
	replaced := runtimenonce.NewNonce(t)
	runtimenonce.Rewrite(t, m.Path, replaced, nil)
	bad, badR := dialHandshake(t, socket, runtimenonce.Line(stale))
	requireHandshakeRefused(t, bad, badR, "the replaced nonce")
	if !strings.Contains(logs.String(), errNonceMismatch.Error()) {
		t.Errorf("refusal log %q does not name the nonce mismatch", logs.String())
	}
	good, goodR := dialHandshake(t, socket, runtimenonce.Line(replaced))
	served(t, good, goodR)
}

// spec: 4.7.11 (Runtime connection handshake)
//
// The provider reports the published manifest's nonce, and the empty
// string when the directory is unset, holds no manifest, or holds one that
// does not decode, so the listeners refuse rather than compare against
// nothing.
func TestPublishedManifestNonceReadsThePublishedFile_spec_4_7_11(t *testing.T) {
	if got := PublishedManifestNonce("")(); got != "" {
		t.Fatalf("provider with no directory = %q, want empty", got)
	}
	dir := t.TempDir()
	provider := PublishedManifestNonce(dir)
	if got := provider(); got != "" {
		t.Fatalf("provider with no manifest = %q, want empty", got)
	}
	m := runtimenonce.PublishIn(t, dir, nil)
	if got := provider(); got != m.Nonce {
		t.Fatalf("provider = %q, want the published %q", got, m.Nonce)
	}
	if err := os.WriteFile(m.Path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := provider(); got != "" {
		t.Fatalf("provider over an undecodable manifest = %q, want empty", got)
	}
	if _, err := handshakeMember([]byte(`{"_lennyNonce":1}`), mcp.NonceParamKey); !errors.Is(err, errNonceLineMissing) {
		t.Fatalf("a non-string nonce member = %v, want errNonceLineMissing", err)
	}
}

// containsString reports whether list holds s.
func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
