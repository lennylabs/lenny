// SPDX-License-Identifier: MIT

package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter/mcp"
	"github.com/lennylabs/lenny/tests/testinfra/runtimenonce"
)

// handshakeSocketAddr returns a short filesystem socket path, removed at
// test end.
func handshakeSocketAddr(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "sdkhs")
	if err != nil {
		t.Fatalf("socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

// fakeListener stands in for the adapter's CH-MSGSOCK or CH-RUNTIMEOPS
// listener and hands each accepted connection to the test.
func fakeListener(t *testing.T) (string, <-chan net.Conn) {
	t.Helper()
	socket := handshakeSocketAddr(t)
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
func readFirstFrame(r io.Reader) <-chan string {
	got := make(chan string, 1)
	go func() {
		line, err := bufio.NewReader(r).ReadString('\n')
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

// The runtime half of the handshake lives once, in pkg/runtimekit, whose
// tier-1 cases pin the challenge answer, the redial with the re-read nonce,
// the wait for an unpublished manifest, and cancellation. The cases below pin
// that both Go SDK dials and the MCP client reach it.

// handshakeHelperNames are the identifiers of the runtime half of the
// handshake that pkg/runtimekit owns. The SDK package declares none of them.
var handshakeHelperNames = map[string]bool{
	"dialAuthenticated":     true,
	"authConn":              true,
	"waitManifestNonce":     true,
	"readManifestNonce":     true,
	"challengeOf":           true,
	"challengeResponse":     true,
	"challengeResponseLine": true,
	"nonceLine":             true,
	"encodeHandshakeLine":   true,
}

// spec: 4.7.11 (Runtime connection handshake)
//
// The Go SDK carries no copy of the runtime half of the handshake: it
// declares none of the helpers pkg/runtimekit owns, the CH-MSGSOCK and
// CH-RUNTIMEOPS dials call runtimekit.DialAuthenticated, and the MCP
// initialize answers the challenge through runtimekit.ChallengeResponseLine.
func TestSDKHandshakeIsRuntimekitsSingleImplementation_spec_4_7_11(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list the SDK package: %v", err)
	}
	fset := token.NewFileSet()
	callers := map[string]map[string]bool{}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			checkNoHandshakeCopy(t, decl)
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			callers[fn.Name.Name] = runtimekitCalls(fn.Body)
		}
	}
	for fn, want := range map[string]string{
		"openTransport": "DialAuthenticated",
		"dialLifecycle": "DialAuthenticated",
		"initialize":    "ChallengeResponseLine",
	} {
		if !callers[fn][want] {
			t.Errorf("%s does not call runtimekit.%s", fn, want)
		}
	}
}

// checkNoHandshakeCopy fails the test when decl declares a handshake helper
// pkg/runtimekit owns.
func checkNoHandshakeCopy(t *testing.T, decl ast.Decl) {
	t.Helper()
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if handshakeHelperNames[d.Name.Name] {
			t.Errorf("the SDK declares %s, a copy of the pkg/runtimekit handshake", d.Name.Name)
		}
	case *ast.GenDecl:
		for _, spec := range d.Specs {
			if ts, ok := spec.(*ast.TypeSpec); ok && handshakeHelperNames[ts.Name.Name] {
				t.Errorf("the SDK declares type %s, a copy of the pkg/runtimekit handshake", ts.Name.Name)
			}
		}
	}
}

// runtimekitCalls returns the runtimekit selectors body references.
func runtimekitCalls(body ast.Node) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == "runtimekit" {
			out[sel.Sel.Name] = true
		}
		return true
	})
	return out
}

// answerChallengeAndSend drives the fake adapter's half of the handshake: it
// checks the nonce line, writes a challenge, validates the answer, and writes
// frame as the first protocol frame. It returns the connection's reader.
func answerChallengeAndSend(t *testing.T, adapter net.Conn, nonce, frame string) *bufio.Reader {
	t.Helper()
	r := bufio.NewReader(adapter)
	if err := runtimenonce.Check(adapter, r, nonce); err != nil {
		t.Fatalf("nonce line: %v", err)
	}
	const challenge = "00112233445566778899aabbccddeeff"
	if _, err := adapter.Write([]byte(`{"_lennyChallenge":"` + challenge + `"}` + "\n")); err != nil {
		t.Fatalf("write challenge: %v", err)
	}
	answer := readLine(t, adapter, r)
	if err := mcp.ValidateChallengeResponse([]byte(answer), nonce, challenge); err != nil {
		t.Fatalf("challenge answer %s: %v", answer, err)
	}
	if _, err := adapter.Write([]byte(frame + "\n")); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	return r
}

// spec: 4.7.11 (Runtime connection handshake), 28.5.3 (CH-MSGSOCK)
//
// The SDK's CH-MSGSOCK transport presents the manifest nonce, answers a
// challenge before the adapter's first frame, and yields that frame to the
// frame loop with the challenge consumed.
func TestSDKMessageSocketTransportCompletesTheHandshake_spec_4_7_11(t *testing.T) {
	socket, conns := fakeListener(t)
	manifest := runtimenonce.Publish(t, nil)
	t.Setenv(socketEnvVar, socket)
	cfg := defaultConfig()
	cfg.manifestPath = manifest.Path
	tr, err := cfg.openTransport(context.Background())
	if err != nil {
		t.Fatalf("openTransport: %v", err)
	}
	t.Cleanup(func() { _ = tr.Close() })
	got := readFirstFrame(tr.Reader)
	const frame = `{"type":"session_start","sessionId":"s1"}`
	answerChallengeAndSend(t, nextConn(t, conns), manifest.Nonce, frame)
	if line := awaitFrame(t, got); line != frame {
		t.Fatalf("first frame the transport yielded = %s, want the session_start", line)
	}
}

// spec: 4.7.11 (Runtime connection handshake), 15.4.3 (CH-RUNTIMEOPS)
//
// The SDK's CH-RUNTIMEOPS dial presents the manifest nonce, answers a
// challenge, and then completes the lifecycle_capabilities and
// lifecycle_support exchange on the authenticated connection.
func TestSDKLifecycleDialCompletesTheHandshake_spec_4_7_11(t *testing.T) {
	socket, conns := fakeListener(t)
	manifest := runtimenonce.Publish(t, nil)
	cfg := defaultConfig()
	cfg.manifestPath = manifest.Path
	p := &process{cfg: cfg, manifest: &AdapterManifest{RuntimeOps: &SocketRef{Socket: socket}}}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	type result struct {
		lc  *Lifecycle
		err error
	}
	done := make(chan result, 1)
	go func() {
		lc, err := p.dialLifecycle(ctx)
		done <- result{lc, err}
	}()
	adapter := nextConn(t, conns)
	r := answerChallengeAndSend(t, adapter, manifest.Nonce, `{"type":"lifecycle_capabilities"}`)
	var support struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(readLine(t, adapter, r)), &support); err != nil || support.Type != "lifecycle_support" {
		t.Fatalf("the runtime answered lifecycle_capabilities with %q (%v), want lifecycle_support", support.Type, err)
	}
	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("dialLifecycle: %v", res.err)
		}
		t.Cleanup(func() { _ = res.lc.conn.Close() })
	case <-time.After(5 * time.Second):
		t.Fatal("dialLifecycle did not return")
	}
}

// spec: 4.7.11 (Nonce-only fallback), 15.4.3 (intra-pod MCP)
//
// A nonce-only MCP server writes a _lennyChallenge in place of the
// initialize response. connectMCP answers it with the HMAC keyed by the
// manifest nonce, reads the initialize response, and completes tools/list.
func TestConnectMCPAnswersTheNonceOnlyChallenge_spec_4_7_11(t *testing.T) {
	sock := handshakeSocketAddr(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := mcp.NewServer()
	srv.RequireChallenge = true
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	const nonce = "nonce_mcp_challenge"
	go func() { _ = srv.Serve(ctx, ln, nonce) }()
	c, err := connectMCP(ctx, sock, nonce, "sdk-test", 5*time.Second)
	if err != nil {
		t.Fatalf("connectMCP against a nonce-only server: %v", err)
	}
	c.close()
	if _, err := connectMCP(ctx, sock, "the-wrong-nonce", "sdk-test", 5*time.Second); err == nil {
		t.Fatal("connectMCP with a wrong nonce succeeded")
	}
}
