// SPDX-License-Identifier: MIT

//go:build conformance

// Tier-10 conformance for the credential path a runtime resolves and the
// path a rotation lands on.
//
// The adapter writes one credential file per session at
// /run/lenny/slots/{sessionId}/credentials.json and names it in the
// `credentialsPath` member of that session's `session_start` frame on
// CH-MSGSOCK. No fixed location names the file, and one runtime process
// serves every session the pod holds, so a runtime that reads credential
// material reads each session's path from that session's frame. The
// Full-level `credentials_rotated` event names the session it rotates and
// the path the adapter rewrote, so a rotation lands on that session's
// bundle and on the file the event names.
//
// The Go cases drive in-process runtimes built on the Go SDK, at Basic
// level and at Full level against a fake CH-RUNTIMEOPS. The Python and
// TypeScript cases drive probe runtimes built on those SDKs.
//
// spec: 28.5.3 (CH-MSGSOCK Inbound: session_start, CH-RUNTIMEOPS
// credentials_rotated), 4.7.11 (adapter-agent security boundary, item 4),
// 4.7.10 (runtime process lifetime), 4.9 (credential lease)

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
// derivable from the identifier.
const credProbeSessionID = "sess_credpath"

// writeCredentialSlotTree writes a providers-layout credential file for
// sessionID under root and returns its path. The tree mirrors the pod
// layout (<root>/slots/{sessionId}/credentials.json).
func writeCredentialSlotTree(t *testing.T, root, sessionID, provider string) string {
	t.Helper()
	path := credentialSlotPath(t, root, sessionID)
	writeCredentialBundle(t, path, provider)
	return path
}

// credentialSlotPath creates the slot directory for sessionID under root
// and returns the credential file path inside it.
func credentialSlotPath(t *testing.T, root, sessionID string) string {
	t.Helper()
	dir := filepath.Join(root, "slots", sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create slot credential dir: %v", err)
	}
	return filepath.Join(dir, "credentials.json")
}

// writeCredentialBundle writes a credential file for provider at path in
// the providers layout of the runtime credential file contract, replacing
// whatever stood there.
func writeCredentialBundle(t *testing.T, path, provider string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"providers": []map[string]any{{
		"leaseId": "lease_" + provider, "provider": provider,
		"expiresAt": "2026-10-05T00:00:00Z", "deliveryMode": "direct",
		"materializedConfig": map[string]any{"apiKey": "sk-" + provider},
	}}})
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write credential file: %v", err)
	}
}

// writeFlatCredentialSlotTree writes the flat credential bundle the
// Python and TypeScript SDKs still decode for sessionID under root and
// returns its path.
func writeFlatCredentialSlotTree(t *testing.T, root, sessionID, provider string) string {
	t.Helper()
	path := credentialSlotPath(t, root, sessionID)
	body, _ := json.Marshal(map[string]any{
		"mode": "direct", "provider": provider,
		"leaseId": "lease_" + provider, "apiKey": "sk-" + provider,
	})
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write credential file: %v", err)
	}
	return path
}

// bundleProvider returns the first providers entry's provider, or "none"
// when the runtime holds no bundle.
func bundleProvider(c *runtime.CredentialBundle) string {
	if c == nil || len(c.Providers) == 0 {
		return "none"
	}
	return c.Providers[0].Provider
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
// and, when runtimeOpsSocket is non-empty, a CH-RUNTIMEOPS socket. The
// Python and TypeScript probes still read the path from the manifest; a
// Go case passes a decoy path the Go SDK must ignore, or none.
func writeCredPathManifest(t *testing.T, dir, credentialsPath, runtimeOpsSocket string) string {
	t.Helper()
	m := map[string]any{
		"version":  1,
		"mcpNonce": "nonce_credpath",
	}
	if credentialsPath != "" {
		m["sessionId"] = credProbeSessionID
		m["taskId"] = credProbeSessionID
		m["credentialsPath"] = credentialsPath
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
// each session's OnCreate and answers every message with the provider of
// the bundle the session's handler context carries.
type credProbeHandler struct {
	mu    sync.Mutex
	creds map[string]*runtime.CredentialBundle
}

func (h *credProbeHandler) OnCreate(_ context.Context, req runtime.CreateRequest) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.creds == nil {
		h.creds = map[string]*runtime.CredentialBundle{}
	}
	h.creds[req.SessionID] = req.Credentials
	return nil
}

func (h *credProbeHandler) OnMessage(ctx context.Context, _ runtime.Message) (runtime.Reply, error) {
	return runtime.TextReply(bundleProvider(runtime.CredentialsFrom(ctx))), nil
}

func (h *credProbeHandler) OnTerminate(context.Context, string, runtime.TerminationReason) error {
	return nil
}

// bundle returns the bundle OnCreate received for sessionID, and whether
// OnCreate ran for it.
func (h *credProbeHandler) bundle(sessionID string) (*runtime.CredentialBundle, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.creds[sessionID]
	return c, ok
}

// goSessionRuntime is an in-process Go-SDK runtime driven over pipes:
// the case writes CH-MSGSOCK frames and reads each frame the runtime
// writes.
type goSessionRuntime struct {
	in     *io.PipeWriter
	frames chan map[string]any
	done   chan struct{}
	err    error
}

// startGoSessionRuntime runs h under runtime.Run with opts over pipes.
func startGoSessionRuntime(t *testing.T, h runtime.Handler, opts ...runtime.Option) *goSessionRuntime {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	r := &goSessionRuntime{in: inW, frames: make(chan map[string]any, 64), done: make(chan struct{})}
	all := append([]runtime.Option{runtime.WithStreams(inR, outW), runtime.WithSocketTransport(false)}, opts...)
	go func() {
		r.err = runtime.Run(h, all...)
		_ = outW.Close()
		close(r.done)
	}()
	go func() {
		defer close(r.frames)
		sc := bufio.NewScanner(outR)
		for sc.Scan() {
			var m map[string]any
			if json.Unmarshal(sc.Bytes(), &m) == nil {
				r.frames <- m
			}
		}
	}()
	t.Cleanup(func() { _ = inW.Close() })
	return r
}

// send writes one frame line on the runtime's stdin.
func (r *goSessionRuntime) send(t *testing.T, line string) {
	t.Helper()
	if _, err := io.WriteString(r.in, line+"\n"); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
}

// next returns the next frame the runtime wrote.
func (r *goSessionRuntime) next(t *testing.T, d time.Duration) map[string]any {
	t.Helper()
	select {
	case f, ok := <-r.frames:
		if !ok {
			t.Fatal("the runtime closed stdout")
		}
		return f
	case <-time.After(d):
		t.Fatalf("no frame from the runtime within %s", d)
		return nil
	}
}

// open writes a session_start for sessionID, naming credentialsPath when
// it is non-empty, and reads the session_started that answers it.
func (r *goSessionRuntime) open(t *testing.T, sessionID, credentialsPath string) map[string]any {
	t.Helper()
	frame := map[string]any{"type": "session_start", "sessionId": sessionID, "startId": "st_" + sessionID}
	if credentialsPath != "" {
		frame["credentialsPath"] = credentialsPath
	}
	line, _ := json.Marshal(frame)
	r.send(t, string(line))
	f := r.next(t, 5*time.Second)
	if f["type"] != "session_started" || f["sessionId"] != sessionID {
		t.Fatalf("frame after session_start(%s) = %v, want its session_started", sessionID, f)
	}
	return f
}

// ask writes a message for sessionID and returns the response text.
func (r *goSessionRuntime) ask(t *testing.T, sessionID string) string {
	t.Helper()
	r.send(t, fmt.Sprintf(`{"type":"message","id":"msg_%s","sessionId":%q,"input":[{"type":"text","inline":"ping"}]}`, sessionID, sessionID))
	f := r.next(t, 5*time.Second)
	if f["type"] != "response" || f["sessionId"] != sessionID {
		t.Fatalf("frame = %v, want the response for %s", f, sessionID)
	}
	out, _ := f["output"].([]any)
	if len(out) == 0 {
		return ""
	}
	p, _ := out[0].(map[string]any)
	s, _ := p["inline"].(string)
	return s
}

// close closes stdin and returns Run's error.
func (r *goSessionRuntime) close(t *testing.T) error {
	t.Helper()
	_ = r.in.Close()
	for range r.frames {
	}
	select {
	case <-r.done:
		return r.err
	case <-time.After(10 * time.Second):
		t.Fatal("the runtime did not exit after stdin closed")
		return nil
	}
}

// spec: 28.5.3 (CH-MSGSOCK Inbound: session_start), 4.7.11 (item 4)
// diagnosis: a runtime built on the Go SDK did not load the credential
//
//	bundle the session_start's credentialsPath named. The credential file
//	is written per session under /run/lenny/slots/{sessionId}/, and one
//	process serves every session, so a runtime that reads a path from the
//	pod-scoped manifest reads another session's file or none. The manifest
//	here names a readable decoy, and a failure means the SDK read it.
func TestGoRuntimeSDKResolvesCredentialPathFromSessionStart_spec_4_7(t *testing.T) {
	dir := t.TempDir()
	credRoot := filepath.Join(dir, "run", "lenny")
	real := writeCredentialSlotTree(t, credRoot, credProbeSessionID, "anthropic")
	decoy := writeCredentialSlotTree(t, credRoot, "sess_decoy", "decoy")
	manifest := writeCredPathManifest(t, dir, decoy, "")

	h := &credProbeHandler{}
	rt := startGoSessionRuntime(t, h, runtime.WithLogger(nil), runtime.WithManifestPath(manifest))
	if f := rt.open(t, credProbeSessionID, real); f["error"] != nil {
		t.Fatalf("session_started = %v, want no error", f)
	}
	got, _ := h.bundle(credProbeSessionID)
	if p := bundleProvider(got); p != "anthropic" {
		t.Fatalf("credential bundle provider = %q, want %q (the SDK read a path other than the session_start's)", p, "anthropic")
	}
	if p := rt.ask(t, credProbeSessionID); p != "anthropic" {
		t.Fatalf("handler context provider = %q, want %q", p, "anthropic")
	}
	if err := rt.close(t); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// spec: 28.5.3 (CH-MSGSOCK Inbound: session_start), 4.7.11 (item 4)
// diagnosis: a session whose session_start names no credentialsPath has
//
//	no provisioned credential file, and the runtime must open it without
//	credentials rather than fail it or borrow a bundle from elsewhere. A
//	failure means the SDK failed the session or loaded a bundle the frame
//	did not name, such as the pod-scoped manifest's path.
func TestGoRuntimeSDKStartsWithNoCredentialFileAtTheManifestPath_spec_4_7(t *testing.T) {
	dir := t.TempDir()
	credRoot := filepath.Join(dir, "run", "lenny")
	decoy := writeCredentialSlotTree(t, credRoot, credProbeSessionID, "decoy")
	manifest := writeCredPathManifest(t, dir, decoy, "")

	h := &credProbeHandler{}
	rt := startGoSessionRuntime(t, h, runtime.WithLogger(nil), runtime.WithManifestPath(manifest))
	if f := rt.open(t, credProbeSessionID, ""); f["error"] != nil {
		t.Fatalf("session_started = %v, want no error for a session with no credential file", f)
	}
	got, ran := h.bundle(credProbeSessionID)
	if !ran {
		t.Fatal("OnCreate did not run for the session")
	}
	if got != nil {
		t.Fatalf("OnCreate received %+v, want no bundle when the session_start names no credentialsPath", got)
	}
	if err := rt.close(t); err != nil {
		t.Fatalf("Run with no credential file returned %v, want a clean exit", err)
	}
}

// spec: 28.5.3 (CH-MSGSOCK Inbound: session_start), 4.7.10 (runtime
// process lifetime), 4.7.11 (item 4)
// diagnosis: two sessions on one Go-SDK runtime process did not each hold
//
//	their own credentials. Each session's handler context must carry the
//	bundle its own session_start named; a runtime that loads one bundle
//	per process serves session B with session A's lease, which crosses
//	users inside a tenant.
func TestGoRuntimeSDKTwoSessionsHoldSeparateCredentials_spec_4_7_10(t *testing.T) {
	dir := t.TempDir()
	credRoot := filepath.Join(dir, "run", "lenny")
	pathA := writeCredentialSlotTree(t, credRoot, "sess_a", "anthropic")
	pathB := writeCredentialSlotTree(t, credRoot, "sess_b", "openai")

	rt := startGoSessionRuntime(t, &credProbeHandler{}, runtime.WithLogger(nil),
		runtime.WithManifestPath(filepath.Join(dir, "absent.json")))
	rt.open(t, "sess_a", pathA)
	rt.open(t, "sess_b", pathB)
	for _, c := range []struct{ session, want string }{
		{"sess_a", "anthropic"}, {"sess_b", "openai"}, {"sess_a", "anthropic"},
	} {
		if got := rt.ask(t, c.session); got != c.want {
			t.Fatalf("%s answered with provider %q, want %q", c.session, got, c.want)
		}
	}
	if err := rt.close(t); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// credRotationAdapter is the adapter side of CH-RUNTIMEOPS for the
// rotation cases: it announces credential_rotation support on connect and
// lets the case drive a credentials_rotated event.
type credRotationAdapter struct {
	ln   net.Listener
	mu   sync.Mutex
	conn net.Conn
	r    *bufio.Reader
}

func startCredRotationAdapter(t *testing.T) *credRotationAdapter {
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

// handshake waits for the runtime to dial the channel and reads its
// lifecycle_support reply.
func (fa *credRotationAdapter) handshake(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !fa.connected() {
		if time.Now().After(deadline) {
			t.Fatal("the runtime did not dial CH-RUNTIMEOPS")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if support := fa.recv(t, 5*time.Second); support["type"] != "lifecycle_support" {
		t.Fatalf("handshake reply = %v, want lifecycle_support", support)
	}
}

// rotation is one OnCredentialsRotated callback.
type rotation struct {
	session  string
	provider string
}

// startRotationRuntime starts a Full-level Go-SDK runtime against a fake
// CH-RUNTIMEOPS and returns it once the handshake completed.
func startRotationRuntime(t *testing.T, logs *credLogSink) (*goSessionRuntime, *credRotationAdapter, chan rotation) {
	t.Helper()
	fa := startCredRotationAdapter(t)
	manifest := writeCredPathManifest(t, t.TempDir(), "", fa.socket())
	rotated := make(chan rotation, 4)
	rt := startGoSessionRuntime(
		t, &credProbeHandler{},
		runtime.WithLogger(logs.logf),
		runtime.WithManifestPath(manifest),
		runtime.WithLifecycleHandlers(
			runtime.OnCredentialsRotated(func(sessionID string, c *runtime.CredentialBundle) {
				rotated <- rotation{session: sessionID, provider: bundleProvider(c)}
			}),
		),
	)
	fa.handshake(t)
	return rt, fa, rotated
}

// rotate writes credentials_rotated for sessionID and requires the
// credentials_acknowledged for leaseID.
func rotate(t *testing.T, fa *credRotationAdapter, sessionID, provider, path, leaseID string) {
	t.Helper()
	frame := map[string]any{
		"type": "credentials_rotated", "sessionId": sessionID,
		"provider": provider, "leaseId": leaseID,
	}
	if path != "" {
		frame["credentialsPath"] = path
	}
	fa.send(t, frame)
	if ack := fa.recv(t, 5*time.Second); ack["type"] != "credentials_acknowledged" || ack["leaseId"] != leaseID {
		t.Fatalf("rotation reply = %v, want credentials_acknowledged for %s", ack, leaseID)
	}
}

// awaitRotation reads the next OnCredentialsRotated callback.
func awaitRotation(t *testing.T, rotated chan rotation) rotation {
	t.Helper()
	select {
	case r := <-rotated:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("OnCredentialsRotated did not run")
		return rotation{}
	}
}

// spec: 28.5.3 (CH-RUNTIMEOPS credentials_rotated), 4.7.11 (item 4), 4.9
// diagnosis: a Full-level rotation did not land on the file the
//
//	credentials_rotated event named for the session it named. The adapter
//	rewrites the rotating session's own credential file and names it on the
//	event, after it reads the session's session_started. A runtime that
//	re-reads the path it started with acknowledges the rotation while still
//	holding the pre-rotation credential, which releases the old credential
//	the runtime is still using.
func TestGoRuntimeSDKRotationReadsTheEventCredentialPath_spec_4_7(t *testing.T) {
	credRoot := filepath.Join(t.TempDir(), "run", "lenny")
	startPath := writeCredentialSlotTree(t, credRoot, credProbeSessionID, "anthropic")
	rotatedPath := writeCredentialSlotTree(t, credRoot, "sess_credpath_rotated", "openai")
	logs := &credLogSink{}
	rt, fa, rotated := startRotationRuntime(t, logs)
	rt.open(t, credProbeSessionID, startPath)

	rotate(t, fa, credProbeSessionID, "openai", rotatedPath, "lease_openai")
	if got := awaitRotation(t, rotated); got != (rotation{credProbeSessionID, "openai"}) {
		t.Fatalf("rotation callback = %+v, want %s rotated to openai: the SDK re-read its startup path rather than the event's credentialsPath",
			got, credProbeSessionID)
	}
	if p := rt.ask(t, credProbeSessionID); p != "openai" {
		t.Fatalf("session provider after rotation = %q, want openai", p)
	}

	// Non-happy path: an event naming a file the runtime cannot read
	// leaves the session's bundle in place, is still acknowledged, and is
	// reported through the SDK's diagnostic sink.
	absentPath := filepath.Join(credRoot, "slots", "sess_absent", "credentials.json")
	rotate(t, fa, credProbeSessionID, "openai", absentPath, "lease_absent")
	if got := awaitRotation(t, rotated); got.provider != "openai" {
		t.Fatalf("bundle after an unreadable rotation path = %+v, want the bundle the session already held", got)
	}
	if !logs.waitFor(absentPath, 5*time.Second) {
		t.Fatalf("no diagnostic named the unreadable rotation path %s; the logged lines were %v", absentPath, logs.lines())
	}

	// Non-happy path: an event carrying no credentialsPath keeps the
	// session's bundle. The startup file is rewritten first, so a runtime
	// that fell back to reading it would report "unexpected".
	writeCredentialBundle(t, startPath, "unexpected")
	rotate(t, fa, credProbeSessionID, "unexpected", "", "lease_pathless")
	if got := awaitRotation(t, rotated); got.provider != "openai" {
		t.Fatalf("bundle after a pathless rotation = %+v, want the bundle the session already held (openai)", got)
	}
	if !logs.waitFor("no credentialsPath", 5*time.Second) {
		t.Fatalf("no diagnostic reported the event carrying no credentialsPath; the logged lines were %v", logs.lines())
	}

	fa.send(t, map[string]any{"type": "terminate", "reason": "done"})
	if err := rt.close(t); err != nil {
		t.Fatalf("Run returned %v, want a clean exit", err)
	}
}

// spec: 28.5.3 (CH-RUNTIMEOPS credentials_rotated), 4.7.10 (runtime
// process lifetime), 4.7.11 (item 4)
// diagnosis: a credentials_rotated event naming session A changed another
//
//	session's credentials, or did not reach A. The event names the session
//	it rotates in sessionId; a runtime that applies it to every session, or
//	to the session it started with, hands B's requests A's new lease.
func TestGoRuntimeSDKRotationReloadsOnlyTheNamedSession_spec_4_7_10(t *testing.T) {
	credRoot := filepath.Join(t.TempDir(), "run", "lenny")
	pathA := writeCredentialSlotTree(t, credRoot, "sess_a", "anthropic")
	pathB := writeCredentialSlotTree(t, credRoot, "sess_b", "openai")
	rotatedA := writeCredentialSlotTree(t, credRoot, "sess_a_rotated", "rotated")
	rt, fa, rotated := startRotationRuntime(t, &credLogSink{})
	rt.open(t, "sess_a", pathA)
	rt.open(t, "sess_b", pathB)

	rotate(t, fa, "sess_a", "rotated", rotatedA, "lease_rotated")
	if got := awaitRotation(t, rotated); got != (rotation{"sess_a", "rotated"}) {
		t.Fatalf("rotation callback = %+v, want sess_a rotated", got)
	}
	if p := rt.ask(t, "sess_a"); p != "rotated" {
		t.Fatalf("sess_a provider after its rotation = %q, want rotated", p)
	}
	if p := rt.ask(t, "sess_b"); p != "openai" {
		t.Fatalf("sess_b provider after sess_a's rotation = %q, want openai (unchanged)", p)
	}
	fa.send(t, map[string]any{"type": "terminate", "reason": "done"})
	if err := rt.close(t); err != nil {
		t.Fatalf("Run returned %v, want a clean exit", err)
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
	writeFlatCredentialSlotTree(t, credRoot, credProbeSessionID, "anthropic")
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
	writeFlatCredentialSlotTree(t, credRoot, credProbeSessionID, "anthropic")
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
