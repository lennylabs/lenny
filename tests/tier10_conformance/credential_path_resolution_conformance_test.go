// SPDX-License-Identifier: MIT

//go:build conformance

// Tier-10 conformance for the credential path a runtime resolves and the
// path a rotation lands on.
//
// The adapter writes one credential file per session at
// /run/lenny/slots/{sessionId}/credentials.json and names it on the §4.7
// adapter manifest as `credentialsPath`. No fixed location names that
// file, so a runtime that reads credential material reads the manifest
// member, and a construction-time option is only the fallback for a
// manifest that carries none. The Full-level `credentials_rotated` event
// carries the path the adapter rewrote, so a rotation lands on the file
// the event names rather than on the path the runtime started with.
//
// The cases drive a Basic-level runtime built on each shipped runtime
// SDK and a Full-level Go-SDK runtime against a fake CH-RUNTIMEOPS.
//
// spec: 4.7 (manifest credentialsPath, Full-level rotation protocol),
// 6.1 (per-session credential file), 4.9 (credential lease)

package tier10_conformance_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/sdks/runtime/go/runtime"
)

// credProbeSessionID is the session the cases bind their slot tree to.
// The credential file sits under slots/{sessionId}/, so the path is only
// derivable from the identifier and the manifest is the only surface
// that carries it.
const credProbeSessionID = "sess_credpath"

// writeCredentialSlotTree writes a §6.1 credential file for sessionID
// under root and returns its path. The tree mirrors the pod layout
// (<root>/slots/{sessionId}/credentials.json) so the test exercises the
// same derivation the adapter performs.
func writeCredentialSlotTree(t *testing.T, root, sessionID, provider string) string {
	t.Helper()
	dir := filepath.Join(root, "slots", sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create slot credential dir: %v", err)
	}
	path := filepath.Join(dir, "credentials.json")
	writeCredentialBundle(t, path, provider)
	return path
}

// writeCredentialBundle writes a §6.1 credential bundle for provider at
// path, replacing whatever stood there.
func writeCredentialBundle(t *testing.T, path, provider string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"mode": "direct", "provider": provider,
		"leaseId": "lease_" + provider, "apiKey": "sk-" + provider,
	})
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write credential file: %v", err)
	}
}

// credLogSink collects the SDK's diagnostic lines so a test can assert
// what the runtime reported. The SDK logs from its own goroutines, so
// the sink is mutex-guarded.
type credLogSink struct {
	mu  sync.Mutex
	out []string
}

func (l *credLogSink) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.out = append(l.out, fmt.Sprintf(format, args...))
}

func (l *credLogSink) lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.out...)
}

// waitFor polls until a logged line names substr, because the SDK logs
// the failure on the channel goroutine that also writes the
// acknowledgement.
func (l *credLogSink) waitFor(substr string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		for _, line := range l.lines() {
			if strings.Contains(line, substr) {
				return true
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// writeCredPathManifest writes a §4.7 manifest naming credentialsPath
// and, when runtimeOpsSocket is non-empty, a CH-RUNTIMEOPS socket.
func writeCredPathManifest(t *testing.T, dir, credentialsPath, runtimeOpsSocket string) string {
	t.Helper()
	m := map[string]any{
		"version":         1,
		"sessionId":       credProbeSessionID,
		"taskId":          credProbeSessionID,
		"mcpNonce":        "nonce_credpath",
		"credentialsPath": credentialsPath,
	}
	if runtimeOpsSocket != "" {
		m["runtimeOps"] = map[string]any{"socket": runtimeOpsSocket}
	}
	path := filepath.Join(dir, "adapter-manifest.json")
	body, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

// credProbeHandler records the credential bundle the SDK delivered on
// OnCreate and echoes every message so the runtime completes a turn.
type credProbeHandler struct {
	mu    sync.Mutex
	creds *runtime.CredentialBundle
}

func (h *credProbeHandler) OnCreate(_ context.Context, req runtime.CreateRequest) error {
	h.mu.Lock()
	h.creds = req.Credentials
	h.mu.Unlock()
	return nil
}

func (h *credProbeHandler) OnMessage(_ context.Context, m runtime.Message) (runtime.Reply, error) {
	return runtime.Reply{Parts: m.Envelope.Input, Final: true}, nil
}

func (h *credProbeHandler) OnTerminate(context.Context, runtime.TerminationReason) error { return nil }

func (h *credProbeHandler) bundle() *runtime.CredentialBundle {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.creds
}

// credProbeSink is a concurrency-safe stdout for an in-process runtime.
type credProbeSink struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (s *credProbeSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

// credProbeStdin is a held-open stdin: Read blocks until Close, which
// mirrors the adapter holding the runtime's stdin open.
type credProbeStdin struct {
	mu     sync.Mutex
	cond   *sync.Cond
	closed bool
}

func newCredProbeStdin() *credProbeStdin {
	p := &credProbeStdin{}
	p.cond = sync.NewCond(&p.mu)
	return p
}

func (p *credProbeStdin) Read([]byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for !p.closed {
		p.cond.Wait()
	}
	return 0, io.EOF
}

func (p *credProbeStdin) Close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	p.cond.Broadcast()
}

// spec: 4.7, 6.1
// diagnosis: a runtime built on the Go SDK did not load the credential
//
//	bundle the manifest's credentialsPath named. The credential file is
//	written per session under /run/lenny/slots/{sessionId}/, so a runtime
//	that keeps its construction-time path reads a file that exists on no
//	pod and runs without credentials, or worse reads a co-tenant's file.
//	A failure means the SDK's startup resolution regressed to a fixed
//	location.
func TestGoRuntimeSDKResolvesCredentialPathFromTheManifest_spec_4_7(t *testing.T) {
	dir := t.TempDir()
	credRoot := filepath.Join(dir, "run", "lenny")
	writeCredentialSlotTree(t, credRoot, credProbeSessionID, "anthropic")
	manifest := writeCredPathManifest(t, dir,
		filepath.Join(credRoot, "slots", credProbeSessionID, "credentials.json"), "")

	// A construction-time option naming a different, readable file must
	// lose to the manifest: it is the fallback rather than the default.
	decoy := filepath.Join(dir, "decoy-credentials.json")
	body, _ := json.Marshal(map[string]any{"mode": "direct", "provider": "decoy"})
	if err := os.WriteFile(decoy, body, 0o600); err != nil {
		t.Fatalf("write decoy credential file: %v", err)
	}

	h := &credProbeHandler{}
	if err := runCredProbeRuntime(t, h, manifest, decoy); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := h.bundle()
	if got == nil {
		t.Fatal("OnCreate received no credential bundle; the manifest's credentialsPath was not read")
	}
	if got.Provider != "anthropic" {
		t.Fatalf("credential bundle provider = %q, want %q (the construction-time option won over the manifest)",
			got.Provider, "anthropic")
	}
}

// spec: 4.7, 6.1
// diagnosis: a session with no active lease has no credential file, and
//
//	the runtime must start without credentials rather than fail. A
//	failure here means the manifest-resolved path turned an absent file
//	into a startup error.
func TestGoRuntimeSDKStartsWithNoCredentialFileAtTheManifestPath_spec_4_7(t *testing.T) {
	dir := t.TempDir()
	credRoot := filepath.Join(dir, "run", "lenny")
	manifest := writeCredPathManifest(t, dir,
		filepath.Join(credRoot, "slots", credProbeSessionID, "credentials.json"), "")

	h := &credProbeHandler{}
	if err := runCredProbeRuntime(t, h, manifest, ""); err != nil {
		t.Fatalf("Run with no credential file at the manifest path returned %v, want a clean exit", err)
	}
	if got := h.bundle(); got != nil {
		t.Fatalf("OnCreate received %+v, want no bundle when the session holds no lease", got)
	}
}

// runCredProbeRuntime runs a Basic-level in-process runtime against the
// manifest and returns Run's error once stdin closes.
func runCredProbeRuntime(t *testing.T, h runtime.Handler, manifest, fallbackCredentials string) error {
	t.Helper()
	stdin := newCredProbeStdin()
	opts := []runtime.Option{
		runtime.WithStreams(stdin, &credProbeSink{}),
		runtime.WithLogger(nil),
		runtime.WithSocketTransport(false),
		runtime.WithManifestPath(manifest),
	}
	if fallbackCredentials != "" {
		opts = append(opts, runtime.WithCredentialsPath(fallbackCredentials))
	}
	done := make(chan error, 1)
	go func() { done <- runtime.Run(h, opts...) }()
	// The bundle is loaded before the frame loop starts, so closing
	// stdin immediately still exercises the resolution.
	stdin.Close()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("the runtime did not exit after stdin closed")
		return nil
	}
}

// credRotationAdapter is the adapter side of CH-RUNTIMEOPS for the
// rotation case: it announces credential_rotation support on connect and
// lets the case drive a credentials_rotated event.
type credRotationAdapter struct {
	ln   net.Listener
	mu   sync.Mutex
	conn net.Conn
	r    *bufio.Reader
}

func startCredRotationAdapter(t *testing.T, _ string) *credRotationAdapter {
	t.Helper()
	// The Unix socket path is capped at 108 bytes, which a test temp
	// directory under a long TMPDIR overruns, so the socket lives in its
	// own short directory.
	sockDir, err := os.MkdirTemp("/tmp", "lenny-credrot-")
	if err != nil {
		t.Fatalf("create socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "runtimeops.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen %s: %v", sock, err)
	}
	fa := &credRotationAdapter{ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		fa.mu.Lock()
		fa.conn = conn
		fa.r = bufio.NewReader(conn)
		fa.mu.Unlock()
		_ = json.NewEncoder(conn).Encode(map[string]any{
			"type":         "lifecycle_capabilities",
			"capabilities": []string{"credential_rotation"},
		})
	}()
	return fa
}

func (fa *credRotationAdapter) socket() string { return fa.ln.Addr().String() }

func (fa *credRotationAdapter) connected() bool {
	fa.mu.Lock()
	defer fa.mu.Unlock()
	return fa.conn != nil
}

func (fa *credRotationAdapter) send(t *testing.T, v any) {
	t.Helper()
	fa.mu.Lock()
	conn := fa.conn
	fa.mu.Unlock()
	if conn == nil {
		t.Fatal("the runtime has not dialed CH-RUNTIMEOPS")
	}
	if err := json.NewEncoder(conn).Encode(v); err != nil {
		t.Fatalf("CH-RUNTIMEOPS send: %v", err)
	}
}

func (fa *credRotationAdapter) recv(t *testing.T, d time.Duration) map[string]any {
	t.Helper()
	fa.mu.Lock()
	conn, r := fa.conn, fa.r
	fa.mu.Unlock()
	if conn == nil {
		t.Fatal("the runtime has not dialed CH-RUNTIMEOPS")
	}
	_ = conn.SetReadDeadline(time.Now().Add(d))
	line, err := r.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		t.Fatalf("CH-RUNTIMEOPS recv: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		t.Fatalf("CH-RUNTIMEOPS frame is not JSON: %v (line %q)", err, line)
	}
	return m
}

// waitConnected polls until the runtime dials the channel.
func waitConnected(t *testing.T, fa *credRotationAdapter, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if fa.connected() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the runtime did not dial CH-RUNTIMEOPS")
}

// spec: 4.7, 4.9
// diagnosis: a Full-level rotation did not land on the file the
//
//	credentials_rotated event named. The adapter rewrites the rotating
//	session's own /run/lenny/slots/{sessionId}/credentials.json and names
//	it on the event, so a runtime that re-reads the path it started with
//	acknowledges the rotation while continuing to hold the pre-rotation
//	credential. That is a silent staleness failure: the acknowledgement
//	releases the old credential the runtime is still using.
func TestGoRuntimeSDKRotationReadsTheEventCredentialPath_spec_4_7(t *testing.T) {
	dir := t.TempDir()
	credRoot := filepath.Join(dir, "run", "lenny")
	startPath := writeCredentialSlotTree(t, credRoot, credProbeSessionID, "anthropic")
	fa := startCredRotationAdapter(t, dir)
	manifest := writeCredPathManifest(t, dir, startPath, fa.socket())

	// The rotated file is a second path, as it is on a pod where the
	// gateway re-places the session's credential directory.
	rotatedPath := writeCredentialSlotTree(t, credRoot, "sess_credpath_rotated", "openai")

	rotated := make(chan *runtime.CredentialBundle, 4)
	logs := &credLogSink{}
	stdin := newCredProbeStdin()
	done := make(chan error, 1)
	go func() {
		done <- runtime.Run(
			&credProbeHandler{},
			runtime.WithStreams(stdin, &credProbeSink{}),
			runtime.WithLogger(logs.logf),
			runtime.WithSocketTransport(false),
			runtime.WithFullLevel(),
			runtime.WithManifestPath(manifest),
			runtime.WithLifecycleHandlers(
				runtime.OnCredentialsRotated(func(c *runtime.CredentialBundle) { rotated <- c }),
			),
		)
	}()
	waitConnected(t, fa, 5*time.Second)
	if support := fa.recv(t, 5*time.Second); support["type"] != "lifecycle_support" {
		t.Fatalf("handshake reply = %v, want lifecycle_support", support)
	}

	fa.send(t, map[string]any{
		"type":            "credentials_rotated",
		"provider":        "openai",
		"credentialsPath": rotatedPath,
		"leaseId":         "lease_openai",
	})
	ack := fa.recv(t, 5*time.Second)
	if ack["type"] != "credentials_acknowledged" || ack["leaseId"] != "lease_openai" || ack["provider"] != "openai" {
		t.Fatalf("rotation reply = %v, want credentials_acknowledged for lease_openai / openai", ack)
	}
	select {
	case got := <-rotated:
		if got == nil {
			t.Fatal("OnCredentialsRotated received no bundle after a rotation naming a readable path")
		}
		if got.Provider != "openai" {
			t.Fatalf("rotated bundle provider = %q, want %q: the SDK re-read its startup path rather than the event's credentialsPath",
				got.Provider, "openai")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnCredentialsRotated did not run")
	}

	// Non-happy path: an event naming a file the runtime cannot read
	// leaves the bundle the runtime holds in place, is still
	// acknowledged, and is reported through the SDK's diagnostic sink so
	// the failure is not silent.
	absentPath := filepath.Join(credRoot, "slots", "sess_absent", "credentials.json")
	fa.send(t, map[string]any{
		"type":            "credentials_rotated",
		"provider":        "openai",
		"credentialsPath": absentPath,
		"leaseId":         "lease_absent",
	})
	ack = fa.recv(t, 5*time.Second)
	if ack["type"] != "credentials_acknowledged" || ack["leaseId"] != "lease_absent" {
		t.Fatalf("unreadable-path rotation reply = %v, want credentials_acknowledged for lease_absent", ack)
	}
	select {
	case got := <-rotated:
		if got == nil || got.Provider != "openai" {
			t.Fatalf("bundle after an unreadable rotation path = %+v, want the bundle the runtime already held", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnCredentialsRotated did not run for the unreadable-path event")
	}
	if !logs.waitFor(absentPath, 5*time.Second) {
		t.Fatalf("no diagnostic named the unreadable rotation path %s; the logged lines were %v",
			absentPath, logs.lines())
	}

	// Non-happy path: an event carrying no credentialsPath breaks the
	// wire contract, which makes the path required. The runtime keeps
	// the bundle it holds, acknowledges the event, and reports the
	// violation. The startup file is rewritten first, so a runtime that
	// fell back to reading it would hand the callback "unexpected"
	// instead of the bundle it already held.
	writeCredentialBundle(t, startPath, "unexpected")
	fa.send(t, map[string]any{
		"type":     "credentials_rotated",
		"provider": "unexpected",
		"leaseId":  "lease_pathless",
	})
	ack = fa.recv(t, 5*time.Second)
	if ack["type"] != "credentials_acknowledged" || ack["leaseId"] != "lease_pathless" {
		t.Fatalf("pathless rotation reply = %v, want credentials_acknowledged for lease_pathless", ack)
	}
	select {
	case got := <-rotated:
		if got == nil || got.Provider != "openai" {
			t.Fatalf("bundle after a pathless rotation = %+v, want the bundle the runtime already held (provider %q); the runtime read a file the event did not name",
				got, "openai")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnCredentialsRotated did not run for the pathless event")
	}
	if !logs.waitFor("no credentialsPath", 5*time.Second) {
		t.Fatalf("no diagnostic reported the rotation event carrying no credentialsPath; the logged lines were %v", logs.lines())
	}

	fa.send(t, map[string]any{"type": "terminate", "reason": "done"})
	stdin.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want a clean exit", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the runtime did not exit after terminate")
	}
}

// credProbeMessage is the inbound §28.5.3 message frame the interpreted
// probes answer. The probe's response output carries the provider of the
// bundle the SDK loaded, so the case reads the resolution off the wire.
const credProbeMessage = `{"type":"message","id":"msg_credpath","from":{"kind":"client","id":"client_alice"},"input":[{"type":"text","inline":"ping"}]}`

// runCredProbeBinary runs a probe runtime with the manifest env var set,
// feeds it one message frame, and returns the first response frame's
// concatenated text output.
func runCredProbeBinary(t *testing.T, argv []string, manifest, workdir string, extraEnv ...string) string {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = workdir
	cmd.Env = append(append(os.Environ(), "LENNY_ADAPTER_MANIFEST="+manifest), extraEnv...)
	cmd.Stdin = strings.NewReader(credProbeMessage + "\n")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("probe runtime %v: %v\nstderr: %s", argv, err, stderr.String())
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var frame struct {
			Type   string `json:"type"`
			Output []struct {
				Inline string `json:"inline"`
			} `json:"output"`
		}
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			continue
		}
		if frame.Type != "response" {
			continue
		}
		var b strings.Builder
		for _, p := range frame.Output {
			b.WriteString(p.Inline)
		}
		return b.String()
	}
	t.Fatalf("probe runtime %v emitted no response frame\nstdout: %s\nstderr: %s", argv, out, stderr.String())
	return ""
}

// requireCredProbeTool resolves a toolchain binary or skips: an
// interpreted SDK cannot be driven without its interpreter.
func requireCredProbeTool(t *testing.T, tool string) string {
	t.Helper()
	path, err := exec.LookPath(tool)
	if err != nil {
		t.Skipf("blocked: %s is not on PATH; the %s runtime SDK credential-path case needs it", tool, tool)
	}
	return path
}

// spec: 4.7, 6.1
// diagnosis: a runtime built on the Python SDK did not load the bundle
//
//	the manifest's credentialsPath named. The Python SDK carries no
//	compiler, so a missed resolution is invisible until a session reads
//	the wrong file or none at all.
func TestPythonRuntimeSDKResolvesCredentialPathFromTheManifest_spec_4_7(t *testing.T) {
	python := requireCredProbeTool(t, "python3")
	root := filepath.Join(repoRoot(t), "sdks", "runtime", "python")

	dir := t.TempDir()
	credRoot := filepath.Join(dir, "run", "lenny")
	writeCredentialSlotTree(t, credRoot, credProbeSessionID, "anthropic")
	manifest := writeCredPathManifest(t, dir,
		filepath.Join(credRoot, "slots", credProbeSessionID, "credentials.json"), "")

	probe := filepath.Join(dir, "probe.py")
	if err := os.WriteFile(probe, []byte(pythonCredProbe), 0o600); err != nil {
		t.Fatalf("write python probe: %v", err)
	}
	got := runCredProbeBinary(t, []string{python, probe}, manifest, root, "PYTHONPATH="+root)
	if got != "anthropic" {
		t.Fatalf("python probe reported provider %q, want %q: the manifest's credentialsPath was not resolved", got, "anthropic")
	}
}

// pythonCredProbe is a Basic-level Python runtime that answers each
// message with the provider of the credential bundle the SDK loaded, or
// "none" when it loaded none.
const pythonCredProbe = `
import sys
from lenny_runtime import Reply, run, text

class Probe:
    def on_create(self, req):
        pass

    def on_message(self, msg, tools):
        creds = tools.credentials
        return Reply(parts=[text(creds.provider if creds and creds.provider else "none")], final=True)

    def on_terminate(self, reason):
        pass

sys.exit(run(Probe()) or 0)
`

// spec: 4.7, 6.1
// diagnosis: a runtime built on the TypeScript SDK did not load the
//
//	bundle the manifest's credentialsPath named. Like the Python SDK it
//	has no compile-time tie to the adapter's manifest, so the resolution
//	is only held by this case.
func TestTypeScriptRuntimeSDKResolvesCredentialPathFromTheManifest_spec_4_7(t *testing.T) {
	node := requireCredProbeTool(t, "node")
	npm := requireCredProbeTool(t, "npm")
	root := filepath.Join(repoRoot(t), "sdks", "runtime", "typescript")

	build := exec.Command(npm, "run", "build")
	build.Dir = root
	if combined, err := build.CombinedOutput(); err != nil {
		t.Fatalf("npm run build: %v\n%s", err, combined)
	}

	dir := t.TempDir()
	credRoot := filepath.Join(dir, "run", "lenny")
	writeCredentialSlotTree(t, credRoot, credProbeSessionID, "anthropic")
	manifest := writeCredPathManifest(t, dir,
		filepath.Join(credRoot, "slots", credProbeSessionID, "credentials.json"), "")

	probe := filepath.Join(dir, "probe.mjs")
	entry := filepath.Join(root, "dist", "src", "index.js")
	body := fmt.Sprintf(typeScriptCredProbe, entry)
	if err := os.WriteFile(probe, []byte(body), 0o600); err != nil {
		t.Fatalf("write typescript probe: %v", err)
	}
	got := runCredProbeBinary(t, []string{node, probe}, manifest, root)
	if got != "anthropic" {
		t.Fatalf("typescript probe reported provider %q, want %q: the manifest's credentialsPath was not resolved", got, "anthropic")
	}
}

// typeScriptCredProbe is a Basic-level TypeScript-SDK runtime, in the
// built JavaScript the package publishes. The single format verb is the
// absolute path of the built entrypoint.
const typeScriptCredProbe = `
import { run, text } from %q;

await run({
  onCreate: async () => {},
  onMessage: async (_msg, tools) => ({
    parts: [text(tools.credentials?.provider ?? "none")],
    final: true,
  }),
  onTerminate: async () => {},
});
`
