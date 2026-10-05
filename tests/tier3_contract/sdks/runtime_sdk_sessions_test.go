// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 per-session contract cases for the runtime-author SDKs. Each case
// drives an inline probe runtime built on one SDK over its stdin and stdout,
// and, for the session_started cases, a CH-RUNTIMEOPS listener the test
// owns. The Go probe runs in process on the SDK's Run; the Python and
// TypeScript probes are interpreted processes. Python and TypeScript have
// no in-tree tier-1 runner, so the independence cases here also carry the
// per-session behavior the Go SDK's tier-1 cases pin.
//
// The probe records each create and each terminate call in an events file
// under its control directory, marks each create call it enters, and blocks
// a create whose session_start names the experiment variant "block", or a
// message whose envelope id starts with "block", until the test writes a
// release file. A create whose variant is "fail" fails. Every reply names
// the create call that built the context serving the session.
//
// spec: §28.5.3 (CH-MSGSOCK, Outbound: session_started; Inbound:
// session_end; Session errors); §28.5.3 (CH-RUNTIMEOPS, Messages); §15.7
// (Runtime Author SDKs); §4.7.10 (Runtime process lifetime).

package sdks_test

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

// probeRuntime is one probe process or in-process Go runtime: the stdin
// it reads, the frames it writes, its control directory, and how to wait
// for it to exit.
type probeRuntime struct {
	in     io.WriteCloser
	frames chan map[string]any
	dir    string
	wait   func() (exitCode int)
}

// probeSDK names an SDK and starts its probe with the given manifest.
type probeSDK struct {
	name  string
	start func(t *testing.T, dir, manifest string) *probeRuntime
}

// probeSDKs lists every SDK's probe. A probe skips when its toolchain is
// absent.
var probeSDKs = []probeSDK{
	{"go", startGoProbe},
	{"python", startPythonProbe},
	{"typescript", startTypeScriptProbe},
}

// interpretedProbeSDKs lists the probes that have no in-tree tier-1 runner.
var interpretedProbeSDKs = probeSDKs[1:]

// readProbeFrames decodes each stdout line onto frames, closing it at end of
// stream.
func readProbeFrames(out io.Reader, frames chan map[string]any) {
	defer close(frames)
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var m map[string]any
		if json.Unmarshal(sc.Bytes(), &m) == nil {
			frames <- m
		}
	}
}

// startGoProbe runs goProbe under the Go SDK's Run over pipes.
func startGoProbe(t *testing.T, dir, manifest string) *probeRuntime {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan int, 1)
	go func() {
		err := runtime.Run(&goProbe{dir: dir, served: map[string]int{}},
			runtime.WithStreams(inR, outW), runtime.WithSocketTransport(false),
			runtime.WithLogger(nil), runtime.WithManifestPath(manifest),
			runtime.WithLifecycleHandlers())
		_ = outW.Close()
		switch {
		case err == nil:
			done <- 0
		case runtime.ErrIsProtocol(err):
			done <- 2
		default:
			done <- 1
		}
	}()
	p := &probeRuntime{in: inW, frames: make(chan map[string]any, 64), dir: dir}
	p.wait = func() int { return <-done }
	go readProbeFrames(outR, p.frames)
	t.Cleanup(func() { _ = inW.Close() })
	return p
}

// startProcessProbe runs argv as a probe process with the control directory
// and the manifest in its environment.
func startProcessProbe(t *testing.T, dir, manifest string, argv ...string) *probeRuntime {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "LENNY_ADAPTER_MANIFEST="+manifest, "PROBE_DIR="+dir)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start probe %v: %v", argv, err)
	}
	p := &probeRuntime{in: stdin, frames: make(chan map[string]any, 64), dir: dir}
	p.wait = func() int {
		if err := cmd.Wait(); err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				return ee.ExitCode()
			}
			return -1
		}
		return 0
	}
	go readProbeFrames(stdout, p.frames)
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Process.Kill() })
	return p
}

func startPythonProbe(t *testing.T, dir, manifest string) *probeRuntime {
	t.Helper()
	python := requireTool(t, "python3")
	return startProcessProbe(t, dir, manifest, buildPythonProbe(t, python, pythonSessionProbe))
}

func startTypeScriptProbe(t *testing.T, dir, manifest string) *probeRuntime {
	t.Helper()
	node := requireTool(t, "node")
	npm := requireTool(t, "npm")
	return startProcessProbe(t, dir, manifest, buildTypeScriptProbe(t, node, npm, typeScriptSessionProbe))
}

// send writes one frame line.
func (p *probeRuntime) send(t *testing.T, line string) {
	t.Helper()
	if _, err := io.WriteString(p.in, line+"\n"); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
}

// next returns the next frame within d.
func (p *probeRuntime) next(t *testing.T, d time.Duration) map[string]any {
	t.Helper()
	select {
	case f, ok := <-p.frames:
		if !ok {
			t.Fatal("the probe closed stdout")
		}
		return f
	case <-time.After(d):
		t.Fatalf("no frame from the probe within %s", d)
		return nil
	}
}

// expectNone fails the test when the probe writes a frame within d.
func (p *probeRuntime) expectNone(t *testing.T, d time.Duration, why string) {
	t.Helper()
	select {
	case f, ok := <-p.frames:
		if ok {
			t.Fatalf("probe wrote %v, want no frame: %s", f, why)
		}
	case <-time.After(d):
	}
}

// heartbeat writes a heartbeat and requires the heartbeat_ack, which shows
// the process is still serving.
func (p *probeRuntime) heartbeat(t *testing.T) {
	t.Helper()
	p.send(t, `{"type":"heartbeat","ts":1}`)
	if f := p.next(t, 10*time.Second); f["type"] != "heartbeat_ack" {
		t.Fatalf("frame = %v, want heartbeat_ack from a still-running process", f)
	}
}

// probeStart is a session_start for sessionID and startID naming variant.
func probeStart(sessionID, startID, variant string) string {
	return fmt.Sprintf(`{"type":"session_start","sessionId":%q,"startId":%q,`+
		`"experimentContext":{"experimentId":"exp_probe","variantId":%q,"inherited":false}}`,
		sessionID, startID, variant)
}

func probeMessage(sessionID, id string) string {
	return fmt.Sprintf(`{"type":"message","id":%q,"sessionId":%q,"input":[{"type":"text","inline":"x"}]}`, id, sessionID)
}

func probeEnd(sessionID string) string {
	return fmt.Sprintf(`{"type":"session_end","sessionId":%q}`, sessionID)
}

// release writes the control file name, which unblocks the probe call
// waiting on it.
func (p *probeRuntime) release(t *testing.T, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(p.dir, name), nil, 0o600); err != nil {
		t.Fatalf("write release file %s: %v", name, err)
	}
}

// awaitFile waits until the probe writes the control file name.
func (p *probeRuntime) awaitFile(t *testing.T, name string) {
	t.Helper()
	if !waitFor(10*time.Second, func() bool {
		_, err := os.Stat(filepath.Join(p.dir, name))
		return err == nil
	}) {
		t.Fatalf("the probe never wrote %s", name)
	}
}

// events returns the probe's recorded create and terminate calls.
func (p *probeRuntime) events() []string {
	raw, _ := os.ReadFile(filepath.Join(p.dir, "events"))
	return strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(raw)), " ", "_"))
}

// countEvents counts recorded events that start with prefix.
func (p *probeRuntime) countEvents(prefix string) int {
	n := 0
	for _, e := range p.events() {
		if strings.HasPrefix(e, prefix) {
			n++
		}
	}
	return n
}

// awaitEvents waits until n events start with prefix.
func (p *probeRuntime) awaitEvents(t *testing.T, prefix string, n int) {
	t.Helper()
	if !waitFor(10*time.Second, func() bool { return p.countEvents(prefix) >= n }) {
		t.Fatalf("probe recorded %d %q events, want %d: %v", p.countEvents(prefix), prefix, n, p.events())
	}
}

// nextOfType reads frames until one of type want, skipping session_started
// frames whose startId is in skip.
func (p *probeRuntime) nextOfType(t *testing.T, want string, skip ...string) map[string]any {
	t.Helper()
	for {
		f := p.next(t, 10*time.Second)
		if f["type"] == "session_started" && containsString(skip, fmt.Sprint(f["startId"])) {
			continue
		}
		if f["type"] != want {
			t.Fatalf("frame = %v, want %s", f, want)
		}
		return f
	}
}

// close closes stdin and returns the exit code.
func (p *probeRuntime) close(t *testing.T) int {
	t.Helper()
	_ = p.in.Close()
	exited := make(chan int, 1)
	go func() { exited <- p.wait() }()
	select {
	case code := <-exited:
		return code
	case <-time.After(20 * time.Second):
		t.Fatal("the probe did not exit after stdin closed")
		return -1
	}
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

// opsListener is the adapter side of CH-RUNTIMEOPS for the session_started
// cases: it accepts the probe's connection, announces the checkpoint
// capability, and lets the case write frames and read replies.
type opsListener struct {
	ln   net.Listener
	mu   sync.Mutex
	conn net.Conn
	r    *bufio.Reader
}

// startOpsListener listens on a socket under a short directory, because a
// test temp directory can overrun the Unix socket path limit.
func startOpsListener(t *testing.T) *opsListener {
	t.Helper()
	dir, err := os.MkdirTemp("", "lenny-ops-")
	if err != nil {
		t.Fatalf("socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	ln, err := net.Listen("unix", filepath.Join(dir, "ops.sock"))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	l := &opsListener{ln: ln}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		_ = json.NewEncoder(conn).Encode(map[string]any{
			"type": "lifecycle_capabilities", "capabilities": []string{"checkpoint"},
		})
		l.mu.Lock()
		l.conn, l.r = conn, bufio.NewReader(conn)
		l.mu.Unlock()
	}()
	return l
}

// writeOpsManifest writes a pod-scoped manifest naming the listener's
// socket under dir.
func writeOpsManifest(t *testing.T, dir string, l *opsListener) string {
	t.Helper()
	m := map[string]any{"version": 1, "mcpNonce": "nonce_probe"}
	if l != nil {
		m["runtimeOps"] = map[string]any{"socket": l.ln.Addr().String()}
	}
	body, _ := json.Marshal(m)
	path := filepath.Join(dir, "adapter-manifest.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

// handshake waits for the probe to dial and reads its lifecycle_support.
func (l *opsListener) handshake(t *testing.T) {
	t.Helper()
	if !waitFor(10*time.Second, func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		return l.conn != nil
	}) {
		t.Fatal("the probe did not dial CH-RUNTIMEOPS")
	}
	if f, ok := l.recv(10 * time.Second); !ok || f["type"] != "lifecycle_support" {
		t.Fatalf("handshake reply = (%v, %v), want lifecycle_support", f, ok)
	}
}

func (l *opsListener) send(t *testing.T, v any) {
	t.Helper()
	l.mu.Lock()
	conn := l.conn
	l.mu.Unlock()
	if err := json.NewEncoder(conn).Encode(v); err != nil {
		t.Fatalf("CH-RUNTIMEOPS send: %v", err)
	}
}

// recv reads one frame within d, reporting false when none arrives.
func (l *opsListener) recv(d time.Duration) (map[string]any, bool) {
	l.mu.Lock()
	conn, r := l.conn, l.r
	l.mu.Unlock()
	_ = conn.SetReadDeadline(time.Now().Add(d))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	line, err := r.ReadBytes('\n')
	if err != nil {
		return nil, false
	}
	var m map[string]any
	if json.Unmarshal(line, &m) != nil {
		return nil, false
	}
	return m, true
}

// spec: 28.5.3 (CH-MSGSOCK Outbound: session_started), 28.5.3 (CH-MSGSOCK Session errors), 28.5.3 (CH-RUNTIMEOPS Messages), 15.7 (Runtime Author SDKs)
// diagnosis: an SDK wrote session_started before the session's context was
//
//	ready, wrote none after it, did not route a CH-RUNTIMEOPS frame the
//	adapter wrote once it read session_started to the session it names,
//	answered a frame for a session it does not hold, or did not report a
//	failed create. The adapter writes a session's CH-RUNTIMEOPS frames only
//	after it reads that session's session_started, so an early
//	acknowledgement lets a checkpoint quiesce a context that does not exist
//	yet, and a missing one fails every start on a Full-level pod.
func TestRuntimeSDKSessionStartedFollowsContextCreation_spec_28_5_3(t *testing.T) {
	for _, sdk := range probeSDKs {
		t.Run(sdk.name, func(t *testing.T) {
			dir := t.TempDir()
			ops := startOpsListener(t)
			p := sdk.start(t, dir, writeOpsManifest(t, dir, ops))
			ops.handshake(t)

			p.send(t, probeStart("sess_a", "st_a", "block"))
			p.awaitFile(t, "created-1")
			p.expectNone(t, 300*time.Millisecond, "the create of sess_a is still blocked")
			p.release(t, "release-1")
			if f := p.next(t, 10*time.Second); f["type"] != "session_started" || f["sessionId"] != "sess_a" ||
				f["startId"] != "st_a" || f["error"] != nil {
				t.Fatalf("frame after the create returned = %v, want session_started for st_a without error", f)
			}
			ops.send(t, map[string]any{"type": "checkpoint_request", "sessionId": "sess_a", "checkpointId": "ckpt_a", "deadlineMs": 5000})
			if f, ok := ops.recv(10 * time.Second); !ok || f["type"] != "checkpoint_ready" || f["checkpointId"] != "ckpt_a" {
				t.Fatalf("CH-RUNTIMEOPS reply = (%v, %v), want checkpoint_ready for ckpt_a", f, ok)
			}

			p.send(t, probeStart("sess_f", "st_f", "fail"))
			if f := p.next(t, 10*time.Second); f["type"] != "session_started" || f["startId"] != "st_f" || f["error"] == nil {
				t.Fatalf("frame after a failed create = %v, want session_started for st_f carrying error", f)
			}
			p.send(t, probeMessage("sess_f", "m_f"))
			f := p.next(t, 10*time.Second)
			if errObj, _ := f["error"].(map[string]any); f["type"] != "response" || f["sessionId"] != "sess_f" || errObj["code"] != "RUNTIME_ERROR" {
				t.Fatalf("frame after a message for failed sess_f = %v, want a RUNTIME_ERROR response", f)
			}

			ops.send(t, map[string]any{"type": "checkpoint_request", "sessionId": "sess_b", "checkpointId": "ckpt_b", "deadlineMs": 5000})
			if f, ok := ops.recv(300 * time.Millisecond); ok {
				t.Fatalf("CH-RUNTIMEOPS reply %v to a checkpoint for unopened sess_b, want none", f)
			}
			p.heartbeat(t)
			if code := p.close(t); code != 0 {
				t.Fatalf("probe exit = %d, want 0", code)
			}
		})
	}
}

// spec: 28.5.3 (CH-MSGSOCK Inbound: session_end), 15.7 (Runtime Author SDKs)
// diagnosis: two overlapping context creations for one session were not
//
//	each released exactly once, or the earlier one served a message. A
//	session_end that arrives while the session's create is still running
//	ends that creation, which the SDK releases when it completes; a later
//	session_start for the same session builds a new context. An SDK keyed
//	by sessionId alone leaks the first context or serves the session from
//	it.
func TestRuntimeSDKOverlappingCreationsAreEachReleasedOnce_spec_28_5_3(t *testing.T) {
	for _, sdk := range probeSDKs {
		t.Run(sdk.name, func(t *testing.T) {
			dir := t.TempDir()
			p := sdk.start(t, dir, writeOpsManifest(t, dir, nil))

			p.send(t, probeStart("sess_a", "st_1", "block"))
			p.awaitFile(t, "created-1")
			p.send(t, probeEnd("sess_a"))
			p.send(t, probeStart("sess_a", "st_2", "block"))
			p.release(t, "release-1")
			p.awaitEvents(t, "terminate_sess_a", 1)
			p.awaitFile(t, "created-2")
			p.release(t, "release-2")
			p.nextOfType(t, "session_started", "st_1")
			p.send(t, probeMessage("sess_a", "m_a"))
			if f := p.nextOfType(t, "response", "st_1"); f["sessionId"] != "sess_a" || !strings.Contains(replyText(f), "creation 2") {
				t.Fatalf("response = %v, want it served by creation 2", f)
			}
			p.send(t, probeEnd("sess_a"))
			p.awaitEvents(t, "terminate_sess_a", 2)
			p.heartbeat(t)
			if code := p.close(t); code != 0 {
				t.Fatalf("probe exit = %d, want 0", code)
			}
			if n := p.countEvents("terminate_sess_a"); n != 2 {
				t.Fatalf("on_terminate ran %d times for sess_a, want once per creation: %v", n, p.events())
			}
		})
	}
}

// spec: 15.7 (Runtime Author SDKs), 28.5.3 (CH-MSGSOCK Inbound: session_end), 4.7.10 (Runtime process lifetime)
// diagnosis: the Python or TypeScript SDK let one session's work block
//
//	another's, did not discard a session's queued messages and wait for
//	its in-flight handler before on_terminate on session_end, opened a
//	restarted session before the previous context's release, did not
//	drain every session on end of input or shutdown, or accepted a
//	malformed session_start. These are the per-session behaviors the Go
//	SDK's tier-1 cases pin; one runtime process serves every session the
//	pod holds, so a regression here serves or ends one session under
//	another's context.
func TestInterpretedRuntimeSDKsKeepSessionsIndependent_spec_15_7(t *testing.T) {
	for _, sdk := range interpretedProbeSDKs {
		t.Run(sdk.name, func(t *testing.T) {
			t.Run("session end ordering", func(t *testing.T) { assertSessionEndOrdering(t, sdk) })
			t.Run("a failed creation ends once on session_end", func(t *testing.T) { assertFailedCreationsEndOnce(t, sdk) })
			t.Run("end of input drains every session", func(t *testing.T) { assertDrainOnExit(t, sdk, "") })
			t.Run("shutdown drains every session", func(t *testing.T) { assertDrainOnExit(t, sdk, "drain") })
			t.Run("malformed session_start is a protocol error", func(t *testing.T) {
				dir := t.TempDir()
				p := sdk.start(t, dir, writeOpsManifest(t, dir, nil))
				p.send(t, `{"type":"session_start","startId":"st_1"}`)
				if code := p.close(t); code != 2 {
					t.Fatalf("probe exit on a session_start without sessionId = %d, want the protocol-error code 2", code)
				}
			})
		})
	}
}

// assertSessionEndOrdering opens two sessions, blocks A's handler, and
// checks that B is served meanwhile, that A's session_end discards A's
// queued message and runs on_terminate only after the in-flight handler
// returns, and that a session_start for A read before that release opens
// a new context only after it.
func assertSessionEndOrdering(t *testing.T, sdk probeSDK) {
	t.Helper()
	dir := t.TempDir()
	p := sdk.start(t, dir, writeOpsManifest(t, dir, nil))
	p.send(t, probeStart("sess_a", "st_a", "control"))
	p.send(t, probeStart("sess_b", "st_b", "control"))
	p.nextOfType(t, "session_started")
	p.nextOfType(t, "session_started")

	p.send(t, probeMessage("sess_a", "block_a"))
	p.awaitFile(t, "handling-sess_a")
	p.send(t, probeMessage("sess_a", "queued_a"))
	p.send(t, probeMessage("sess_b", "m_b"))
	if f := p.nextOfType(t, "response"); f["sessionId"] != "sess_b" {
		t.Fatalf("response = %v, want sess_b served while sess_a's handler is blocked", f)
	}

	p.send(t, probeEnd("sess_a"))
	p.send(t, probeStart("sess_a", "st_a2", "control"))
	p.send(t, probeMessage("sess_a", "after"))
	p.expectNone(t, 300*time.Millisecond, "sess_a's handler is still in flight")
	if n := p.countEvents("terminate_sess_a"); n != 0 {
		t.Fatalf("on_terminate ran for sess_a while its handler was in flight: %v", p.events())
	}
	p.release(t, "release-msg-sess_a")
	if f := p.nextOfType(t, "session_started"); f["startId"] != "st_a2" {
		t.Fatalf("frame = %v, want session_started for st_a2", f)
	}
	f := p.nextOfType(t, "response")
	if f["sessionId"] != "sess_a" || f["error"] != nil || !strings.Contains(replyText(f), "creation 3 after") {
		t.Fatalf("response = %v, want the new context (creation 3) answering after, and no answer for the discarded or in-flight messages", f)
	}
	ev := p.events()
	if term, second := indexOf(ev, "terminate_sess_a_session_end"), indexOf(ev, "create_sess_a_3"); term < 0 || second < term {
		t.Fatalf("events = %v, want sess_a's on_terminate before its second create", ev)
	}
	p.heartbeat(t)
	if code := p.close(t); code != 0 {
		t.Fatalf("probe exit = %d, want 0", code)
	}
}

// assertDrainOnExit blocks a session's handler with a message queued
// behind it, ends the input (or writes shutdown with reason when it is
// not empty), and checks that both messages are answered before
// on_terminate runs with the end's reason and the process exits cleanly.
func assertDrainOnExit(t *testing.T, sdk probeSDK, reason string) {
	t.Helper()
	dir := t.TempDir()
	p := sdk.start(t, dir, writeOpsManifest(t, dir, nil))
	p.send(t, probeStart("sess_a", "st_a", "control"))
	p.nextOfType(t, "session_started")
	p.send(t, probeMessage("sess_a", "block_a"))
	p.awaitFile(t, "handling-sess_a")
	p.send(t, probeMessage("sess_a", "queued_a"))
	want := "stdin_closed"
	if reason != "" {
		p.send(t, fmt.Sprintf(`{"type":"shutdown","reason":%q,"deadline_ms":5000}`, reason))
		want = reason
	} else {
		_ = p.in.Close()
	}
	p.release(t, "release-msg-sess_a")
	for _, id := range []string{"block_a", "queued_a"} {
		if f := p.nextOfType(t, "response"); f["sessionId"] != "sess_a" || !strings.Contains(replyText(f), id) {
			t.Fatalf("response = %v, want the answer to %s", f, id)
		}
	}
	if code := p.close(t); code != 0 {
		t.Fatalf("probe exit = %d, want 0", code)
	}
	if ev := p.events(); indexOf(ev, "terminate_sess_a_"+want) < 0 {
		t.Fatalf("events = %v, want sess_a terminated with reason %s", ev, want)
	}
}

// assertFailedCreationsEndOnce opens one session whose create fails and
// one whose credential file cannot be read, and checks that the credential
// failure never reaches the handler's create and that each session ends on
// its session_end with exactly one on_terminate.
func assertFailedCreationsEndOnce(t *testing.T, sdk probeSDK) {
	t.Helper()
	dir := t.TempDir()
	p := sdk.start(t, dir, writeOpsManifest(t, dir, nil))
	missing := filepath.Join(dir, "slots", "sess_c", "credentials.json")
	p.send(t, probeStart("sess_f", "st_f", "fail"))
	p.send(t, fmt.Sprintf(`{"type":"session_start","sessionId":"sess_c","startId":"st_c","credentialsPath":%q}`, missing))
	for i := 0; i < 2; i++ {
		if f := p.nextOfType(t, "session_started"); f["error"] == nil {
			t.Fatalf("session_started = %v, want it to carry the creation error", f)
		}
	}
	if n := p.countEvents("create_sess_c"); n != 0 {
		t.Fatalf("the handler's create ran for a session whose credential file could not be read: %v", p.events())
	}
	p.send(t, probeEnd("sess_f"))
	p.send(t, probeEnd("sess_c"))
	p.awaitEvents(t, "terminate_sess_f_session_end", 1)
	p.awaitEvents(t, "terminate_sess_c_session_end", 1)
	if code := p.close(t); code != 0 {
		t.Fatalf("probe exit = %d, want 0", code)
	}
	for _, id := range []string{"sess_f", "sess_c"} {
		if n := p.countEvents("terminate_" + id); n != 1 {
			t.Fatalf("on_terminate ran %d times for %s, want once: %v", n, id, p.events())
		}
	}
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

// goProbe is the Go-SDK probe handler.
type goProbe struct {
	dir    string
	mu     sync.Mutex
	calls  int
	served map[string]int
}

func (h *goProbe) event(line string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	f, err := os.OpenFile(filepath.Join(h.dir, "events"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintln(f, line)
}

func (h *goProbe) touch(name string) {
	_ = os.WriteFile(filepath.Join(h.dir, name), nil, 0o600)
}

func (h *goProbe) await(name string) {
	for {
		if _, err := os.Stat(filepath.Join(h.dir, name)); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (h *goProbe) OnCreate(_ context.Context, req runtime.CreateRequest) error {
	h.mu.Lock()
	h.calls++
	n := h.calls
	h.served[req.SessionID] = n
	h.mu.Unlock()
	h.event(fmt.Sprintf("create %s %d", req.SessionID, n))
	h.touch(fmt.Sprintf("created-%d", n))
	variant := ""
	if req.ExperimentContext != nil {
		variant = req.ExperimentContext.VariantID
	}
	switch variant {
	case "fail":
		return fmt.Errorf("probe create failed")
	case "block":
		h.await(fmt.Sprintf("release-%d", n))
	}
	return nil
}

func (h *goProbe) OnMessage(_ context.Context, m runtime.Message) (runtime.Reply, error) {
	id := m.Envelope.ID
	if strings.HasPrefix(id, "block") {
		h.touch("handling-" + m.SessionID)
		h.await("release-msg-" + m.SessionID)
	}
	h.mu.Lock()
	n := h.served[m.SessionID]
	h.mu.Unlock()
	return runtime.TextReply(fmt.Sprintf("creation %d %s", n, id)), nil
}

func (h *goProbe) OnTerminate(_ context.Context, sessionID string, reason runtime.TerminationReason) error {
	h.event(fmt.Sprintf("terminate %s %s", sessionID, reason.Reason))
	return nil
}

// pythonSessionProbe is the Python-SDK probe; see the file comment.
const pythonSessionProbe = `
import os, sys, threading, time
from lenny_runtime import LifecycleHooks, ProtocolError, Reply, RunOptions, run, text

DIR = os.environ["PROBE_DIR"]
_lock = threading.Lock()
_calls = 0
_served = {}

def _event(line):
    with _lock:
        with open(os.path.join(DIR, "events"), "a") as f:
            f.write(line + "\n")

def _touch(name):
    open(os.path.join(DIR, name), "w").close()

def _await(name):
    while not os.path.exists(os.path.join(DIR, name)):
        time.sleep(0.005)

class Probe:
    def on_create(self, req):
        global _calls
        with _lock:
            _calls += 1
            n = _calls
            _served[req.session_id] = n
        _event("create %s %d" % (req.session_id, n))
        _touch("created-%d" % n)
        variant = req.experiment_context.variant_id if req.experiment_context else ""
        if variant == "fail":
            raise RuntimeError("probe create failed")
        if variant == "block":
            _await("release-%d" % n)

    def on_message(self, msg, tools):
        mid = msg.envelope.id
        if mid.startswith("block"):
            _touch("handling-" + msg.session_id)
            _await("release-msg-" + msg.session_id)
        with _lock:
            n = _served.get(msg.session_id, 0)
        return Reply(parts=[text("creation %d %s" % (n, mid))], final=True)

    def on_terminate(self, session_id, reason):
        _event("terminate %s %s" % (session_id, reason.reason))

try:
    run(Probe(), RunOptions(level="full", lifecycle=LifecycleHooks()))
except ProtocolError:
    sys.exit(2)
`

// typeScriptSessionProbe is the TypeScript-SDK probe, in the built
// JavaScript the package publishes. The single format verb is the absolute
// path of the built entrypoint.
const typeScriptSessionProbe = `
import { appendFileSync, existsSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { isProtocolError, run, text } from %q;

const dir = process.env.PROBE_DIR;
let calls = 0;
const served = new Map();
const event = (line) => appendFileSync(join(dir, "events"), line + "\n");
const touch = (name) => writeFileSync(join(dir, name), "");
const wait = async (name) => {
  while (!existsSync(join(dir, name))) {
    await new Promise((r) => setTimeout(r, 5));
  }
};

try {
  await run({
    onCreate: async (req) => {
      const n = ++calls;
      served.set(req.sessionId, n);
      event("create " + req.sessionId + " " + n);
      touch("created-" + n);
      const variant = req.experimentContext?.variantId ?? "";
      if (variant === "fail") {
        throw new Error("probe create failed");
      }
      if (variant === "block") {
        await wait("release-" + n);
      }
    },
    onMessage: async (msg) => {
      const id = msg.envelope.id;
      if (id.startsWith("block")) {
        touch("handling-" + msg.sessionId);
        await wait("release-msg-" + msg.sessionId);
      }
      return { parts: [text("creation " + (served.get(msg.sessionId) ?? 0) + " " + id)], final: true };
    },
    onTerminate: async (sessionId, reason) => {
      event("terminate " + sessionId + " " + reason.reason);
    },
  }, { level: "full", lifecycle: {} });
} catch (err) {
  process.exitCode = isProtocolError(err) ? 2 : 1;
}
`
