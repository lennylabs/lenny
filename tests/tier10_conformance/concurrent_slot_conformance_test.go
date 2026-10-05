// SPDX-License-Identifier: MIT

//go:build conformance

// Tier-10 conformance case for the per-session dispatch loop. A pool whose
// sessionPolicy.maxConcurrentSessions is above 1 multiplexes simultaneous
// sessions onto one pod, each in its own slot, over a single runtime
// process. Every session is bound to a slot on every pod, so the §28.5.3
// contract requires a runtime to implement a dispatch loop keyed on
// sessionId: every inbound session-scoped frame carries a sessionId, the
// runtime derives each session's cwd as
// /workspace/slots/{sessionId}/current/, and every outbound session-scoped
// frame echoes the identifier it was handed so the adapter routes the
// response back to the session that sent it.
// cmd/runtimes/echo-concurrent is the reference runtime that implements
// that loop.
//
// This case drives the freshly built echo-concurrent binary over its
// §15.4 stdin/stdout contract transport (the same transport the
// conformance harness and the Tier 3 contract tests use) with frames
// interleaved across two distinct sessions, and separately with one
// session-scoped frame that carries no identifier. It asserts the three
// properties the model rests on:
//
//   - Per-session response tagging: each session's response comes back
//     tagged with the session that sent it, and each session keeps an
//     independent sequence counter, so the adapter can demultiplex the
//     interleaved output by sessionId.
//   - Per-session cwd derivation: the runtime derives each session's cwd
//     as /workspace/slots/{sessionId}/current/. The reference echo runtime
//     performs no file operations, so it surfaces the derived cwd as a
//     per-session stderr diagnostic; this case reads that diagnostic to
//     confirm the derivation.
//   - The unaddressed frame is refused: a session-scoped frame carrying no
//     sessionId names no session the runtime may act for, so the loop
//     answers no response and exits with the §15.4 protocol-error code
//     rather than serving the frame on a pod-global default session.
//
// The cases at the end of the file drive each runtime-author SDK's echo
// example the same way at Basic level, through two sequential sessions and
// two concurrent sessions on one process, and require each reply to name
// its own session's identifier and experiment variant.
//
// spec: 5.2 (per-session multiplexing
//
//	over stdin), 28.5.3 (single stdin channel carrying sessionId on every
//	pod), 6.4 (per-slot cwd /workspace/slots/{sessionId}/current/), 4.7.10
//	(runtime process lifetime), 15.4.6 (session lifetime).

package tier10_conformance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// concurrentFrame is the subset of an outbound JSONL frame this
// conformance case reads: the discriminator, the sessionId the dispatch
// loop stamps, and the echoed text parts. The §28.5.3 outbound schema
// carries sessionId alone for multiplexing, so the case asserts on
// sessionId and never on a cwd wire field; the per-session cwd is an
// internal derivation read from the runtime's stderr diagnostic.
type concurrentFrame struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	Output    []struct {
		Inline string `json:"inline"`
	} `json:"output"`
}

// concurrentResult holds the decoded stdout response frames and the raw
// stderr the echo-concurrent runtime emitted while serving a batch of
// interleaved frames.
type concurrentResult struct {
	responses []concurrentFrame
	stderr    string
	exitCode  int
}

// runConcurrentRuntime execs the echo-concurrent binary over its §15.4
// stdin/stdout contract transport, feeds it the frames, and returns the
// decoded `response` frames, the captured stderr, and the exit code. A
// trailing shutdown is appended so the dispatch loop drains every session
// deterministically before the process exits.
func runConcurrentRuntime(t *testing.T, binary string, frames []string) concurrentResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	input := strings.Join(frames, "") +
		`{"type":"shutdown","reason":"session_complete","deadline_ms":1}` + "\n"

	cmd := exec.CommandContext(ctx, binary)
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	exitCode := 0
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("echo-concurrent could not be run (stderr: %s): %v", stderr.String(), err)
		}
		exitCode = ee.ExitCode()
	}

	var responses []concurrentFrame
	for _, line := range strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var f concurrentFrame
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			t.Fatalf("decode outbound frame %q: %v", line, err)
		}
		if f.Type == "response" {
			responses = append(responses, f)
		}
	}
	return concurrentResult{responses: responses, stderr: stderr.String(), exitCode: exitCode}
}

// concurrentMessage builds a `message` JSONL frame carrying the session
// address the adapter stamps and a single inline text part. An empty
// sessionID omits the field, producing the unaddressed session-scoped
// frame §28.5.3 gives no session to serve.
func concurrentMessage(sessionID, text string) string {
	m := map[string]any{
		"type":  "message",
		"id":    "m_" + text,
		"input": []map[string]any{{"type": "text", "inline": text}},
	}
	if sessionID != "" {
		m["sessionId"] = sessionID
	}
	b, _ := json.Marshal(m)
	return string(b) + "\n"
}

// concurrentSessionStart builds the session_start that opens sessionID.
// The reference runtime creates a session's context only on session_start,
// so every session a case messages is opened first.
func concurrentSessionStart(sessionID, startID string) string {
	b, _ := json.Marshal(map[string]any{"type": "session_start", "sessionId": sessionID, "startId": startID})
	return string(b) + "\n"
}

// inlineText concatenates the inline text parts of a response frame.
func inlineText(f concurrentFrame) string {
	var b strings.Builder
	for _, p := range f.Output {
		b.WriteString(p.Inline)
	}
	return b.String()
}

// spec: 5.2 (per-session multiplexing
//
//	over stdin), 28.5.3 (single stdin channel carrying sessionId on every
//	pod), 6.4 (per-session cwd derivation).
//
// diagnosis: The reference echo-concurrent runtime no longer demultiplexes
//
//	interleaved frames for distinct sessions over a single stdin channel:
//	either a session's response is not tagged with its sessionId, or the
//	per-session cwd is no longer derived as
//	/workspace/slots/{sessionId}/current/. The §28.5.3 dispatch loop
//	regressed and a maxConcurrentSessions > 1 pod can no longer serve a
//	second session over one runtime process.
func TestConcurrentSessionDispatchConformance(t *testing.T) {
	a := buildArtifacts(t)

	const (
		sessionA = "sess-01"
		sessionB = "sess-02"
	)

	// Interleave two sessions on one stdin channel: sess-01 sends two
	// messages and sess-02 one. The single dispatch loop must demultiplex
	// both streams.
	frames := []string{
		concurrentSessionStart(sessionA, "st_a"),
		concurrentSessionStart(sessionB, "st_b"),
		concurrentMessage(sessionA, "a1"),
		concurrentMessage(sessionB, "b1"),
		concurrentMessage(sessionA, "a2"),
	}
	result := runConcurrentRuntime(t, a.echoConcurrent, frames)
	if result.exitCode != 0 {
		t.Fatalf("echo-concurrent exited %d serving addressed frames, want 0 (stderr: %s)", result.exitCode, result.stderr)
	}

	bySession := map[string][]concurrentFrame{}
	for _, f := range result.responses {
		bySession[f.SessionID] = append(bySession[f.SessionID], f)
	}

	// Per-session response tagging: each session's responses come back
	// tagged with the identifier the inbound frame carried.
	if got := len(bySession[sessionA]); got != 2 {
		t.Errorf("sess-01 got %d responses, want 2: %+v", got, bySession[sessionA])
	}
	if got := len(bySession[sessionB]); got != 1 {
		t.Errorf("sess-02 got %d responses, want 1: %+v", got, bySession[sessionB])
	}
	if got := len(bySession[""]); got != 0 {
		t.Errorf("%d response(s) carried no sessionId: %+v; every session-scoped frame is addressed on every pod", got, bySession[""])
	}

	// Independent per-session sequence counters: sess-01's two responses
	// are seq=1 then seq=2; sess-02's single response is an independent
	// seq=1, not seq=3, proving the dispatch loop does not share a counter
	// across sessions. A shared counter would let one session observe
	// another's ordering, breaking the per-session stream isolation
	// §28.5.3 promises.
	if len(bySession[sessionA]) == 2 {
		if got := inlineText(bySession[sessionA][0]); !strings.Contains(got, "[echo seq=1]") || !strings.Contains(got, "a1") {
			t.Errorf("sess-01 first response = %q, want a seq=1 echo of a1", got)
		}
		if got := inlineText(bySession[sessionA][1]); !strings.Contains(got, "[echo seq=2]") || !strings.Contains(got, "a2") {
			t.Errorf("sess-01 second response = %q, want a seq=2 echo of a2", got)
		}
	}
	if len(bySession[sessionB]) == 1 {
		if got := inlineText(bySession[sessionB][0]); !strings.Contains(got, "[echo seq=1]") || !strings.Contains(got, "b1") {
			t.Errorf("sess-02 response = %q, want an independent seq=1 echo of b1", got)
		}
	}

	// Per-session cwd derivation (§6.4): the runtime derives each active
	// session's cwd as /workspace/slots/{sessionId}/current/. The echo
	// runtime performs no file operations, so it surfaces the derived cwd
	// on stderr; the diagnostic confirms the derivation runs for each
	// session the dispatch loop opens.
	for _, sessionID := range []string{sessionA, sessionB} {
		wantCwd := "/workspace/slots/" + sessionID + "/current/"
		if !strings.Contains(result.stderr, wantCwd) {
			t.Errorf("stderr does not record per-session cwd %q for %q; got:\n%s", wantCwd, sessionID, result.stderr)
		}
	}
	// No session derives a tree under an empty identifier, which would
	// mean an unaddressed frame opened a session of its own.
	if strings.Contains(result.stderr, "/workspace/slots//") {
		t.Errorf("stderr records a per-session cwd under an empty identifier:\n%s", result.stderr)
	}
}

// spec: 28.5.3 (a session-scoped frame carries the per-session identifier
//
//	on every pod, so absence is an error rather than a scope), 15.4
//	(protocol-error exit code 2), 28.5.3
//
// diagnosis: the reference runtime serves a session-scoped frame that
//
//	names no session. Under the retired rule that frame took a pod-global
//	default path, which is what made the absence of an identifier carry a
//	scope. A response emitted for it, or a clean exit, means the retired
//	path is back and a co-tenanted pod can answer one session's frame as
//	another's.
func TestUnaddressedSessionScopedFrameIsRefused_spec_28_5_3(t *testing.T) {
	a := buildArtifacts(t)

	result := runConcurrentRuntime(t, a.echoConcurrent, []string{concurrentMessage("", "solo")})
	if len(result.responses) != 0 {
		t.Errorf("an unaddressed session-scoped frame produced %d response(s): %+v; it must be served by no session",
			len(result.responses), result.responses)
	}
	if result.exitCode != 2 {
		t.Errorf("echo-concurrent exited %d on an unaddressed session-scoped frame, want the §15.4 protocol-error code 2 (stderr: %s)",
			result.exitCode, result.stderr)
	}
	if strings.Contains(result.stderr, "/workspace/slots//") {
		t.Errorf("the unaddressed frame opened a session under an empty identifier:\n%s", result.stderr)
	}
}

// spec: 15.4 (a runtime tolerates an inbound frame type it does not
//
//	recognise), 28.5.3 (the addressing rule governs the session-scoped
//	frame set and no other type)
//
// diagnosis: the reference runtime failed closed on an unaddressed frame
//
//	whose type sits outside the session-scoped set. The addressing rule
//	reaches the session-scoped frames alone, so a frame of any other type
//	keeps §15.4's unknown-type tolerance. A runtime that exits on one makes
//	every forward-compatible frame type fatal and stops serving the
//	sessions already bound to the pod.
func TestUnaddressedUnknownFrameTypeIsTolerated_spec_15_4(t *testing.T) {
	a := buildArtifacts(t)

	result := runConcurrentRuntime(t, a.echoConcurrent, []string{
		`{"type":"this_is_a_future_message_type","x":1}` + "\n",
		concurrentSessionStart("sess_alice", "st_alice"),
		concurrentMessage("sess_alice", "ping"),
	})
	if result.exitCode != 0 {
		t.Errorf("echo-concurrent exited %d on an unaddressed frame outside the session-scoped set, want 0 (stderr: %s)",
			result.exitCode, result.stderr)
	}
	if len(result.responses) != 1 {
		t.Fatalf("got %d response(s) after the unknown frame type, want the addressed message still served: %+v",
			len(result.responses), result.responses)
	}
	if got := result.responses[0].SessionID; got != "sess_alice" {
		t.Errorf("response carries sessionId %q, want sess_alice", got)
	}
	if got := inlineText(result.responses[0]); !strings.Contains(got, "ping") {
		t.Errorf("response echoed %q, want the addressed message's text", got)
	}
}

// sdkEchoRuntimes lists each runtime-author SDK's echo example as a
// runtime the case starts over stdin and stdout. A builder skips its case
// when its toolchain is absent.
var sdkEchoRuntimes = []struct {
	name  string
	build func(t *testing.T) interpretedProbe
}{
	{"go", goEchoExample},
	{"python", pythonEchoExample},
	{"typescript", typeScriptEchoExample},
}

// goEchoExample builds the Go SDK's echo example.
func goEchoExample(t *testing.T) interpretedProbe {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "go-echo")
	cmd := exec.Command("go", "build", "-o", bin, "./sdks/runtime/go/example/echo")
	cmd.Dir = repoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build the Go SDK echo example: %v\n%s", err, out)
	}
	return interpretedProbe{argv: []string{bin}}
}

// pythonEchoExample runs the Python SDK's echo example module.
func pythonEchoExample(t *testing.T) interpretedProbe {
	t.Helper()
	python := requireCredProbeTool(t, "python3")
	root := filepath.Join(repoRoot(t), "sdks", "runtime", "python")
	return interpretedProbe{
		argv: []string{python, "-m", "lenny_runtime.examples.echo"}, workdir: root,
		env: []string{"PYTHONPATH=" + root},
	}
}

// typeScriptEchoExample builds the TypeScript SDK and runs its echo example.
func typeScriptEchoExample(t *testing.T) interpretedProbe {
	t.Helper()
	node := requireCredProbeTool(t, "node")
	npm := requireCredProbeTool(t, "npm")
	root := filepath.Join(repoRoot(t), "sdks", "runtime", "typescript")
	for _, args := range [][]string{{"install", "--no-audit", "--no-fund"}, {"run", "build"}} {
		cmd := exec.Command(npm, args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("npm %v: %v\n%s", args, err, out)
		}
	}
	return interpretedProbe{argv: []string{node, filepath.Join(root, "dist", "examples", "echo", "main.js")}, workdir: root}
}

// openWithVariant writes a session_start enrolling sessionID in variant
// and requires the session_started that names the session and the start.
func openWithVariant(t *testing.T, rt *sessionRuntime, sessionID, variant string) {
	t.Helper()
	rt.send(t, fmt.Sprintf(`{"type":"session_start","sessionId":%q,"startId":"st_%s",`+
		`"experimentContext":{"experimentId":"exp_lifetime","variantId":%q,"inherited":false}}`,
		sessionID, sessionID, variant))
	f := rt.next(t, 10*time.Second)
	if f["type"] != "session_started" || f["sessionId"] != sessionID || f["startId"] != "st_"+sessionID || f["error"] != nil {
		t.Fatalf("frame after session_start(%s) = %v, want its session_started without error", sessionID, f)
	}
}

// writeMessage writes one message for sessionID.
func writeMessage(t *testing.T, rt *sessionRuntime, sessionID string) {
	t.Helper()
	rt.send(t, fmt.Sprintf(`{"type":"message","id":"msg_%s","sessionId":%q,"input":[{"type":"text","inline":"ping"}]}`,
		sessionID, sessionID))
}

// readReplies reads n responses and returns their reply text by session.
func readReplies(t *testing.T, rt *sessionRuntime, n int) map[string]string {
	t.Helper()
	out := map[string]string{}
	for len(out) < n {
		f := rt.next(t, 10*time.Second)
		if f["type"] != "response" {
			t.Fatalf("frame = %v, want a response", f)
		}
		id, _ := f["sessionId"].(string)
		parts, _ := f["output"].([]any)
		if len(parts) == 0 {
			t.Fatalf("response for %s carries no output: %v", id, f)
		}
		p, _ := parts[0].(map[string]any)
		out[id], _ = p["inline"].(string)
	}
	return out
}

// requireContext fails the test unless reply names sessionID and variant.
func requireContext(t *testing.T, reply, sessionID, variant string) {
	t.Helper()
	if want := "session=" + sessionID + " variant=" + variant; !strings.Contains(reply, want) {
		t.Fatalf("reply %q does not carry %q: the session was served under another session's context", reply, want)
	}
}

// heartbeatAlive requires a heartbeat_ack, which shows the process still
// runs after a session ended.
func heartbeatAlive(t *testing.T, rt *sessionRuntime) {
	t.Helper()
	rt.send(t, `{"type":"heartbeat","ts":1}`)
	if f := rt.next(t, 10*time.Second); f["type"] != "heartbeat_ack" {
		t.Fatalf("frame = %v, want heartbeat_ack from a runtime still running after session_end", f)
	}
}

// spec: 4.7.10 (Runtime process lifetime), 15.4.6 (session lifetime)
// diagnosis: an SDK-built Basic runtime did not serve two sessions in
//
//	sequence on one process, each under its own context: the second
//	session's reply named the first session or its experiment variant, a
//	session_start was not answered with a session_started naming its
//	session, or the process exited at the first session's session_end. A
//	recycling pool keeps the runtime process across sessions, so such a
//	runtime serves every later session under a stale context.
func TestRuntimeSDKEchoServesSequentialSessionsOnOneProcess_spec_4_7_10(t *testing.T) {
	for _, sdk := range sdkEchoRuntimes {
		t.Run(sdk.name, func(t *testing.T) {
			dir := t.TempDir()
			rt := startProbeRuntime(t, sdk.build(t), filepath.Join(dir, "absent.json"))
			for _, s := range []struct{ id, variant string }{{"sess_a", "control"}, {"sess_b", "treatment"}} {
				openWithVariant(t, rt, s.id, s.variant)
				writeMessage(t, rt, s.id)
				requireContext(t, readReplies(t, rt, 1)[s.id], s.id, s.variant)
				rt.send(t, fmt.Sprintf(`{"type":"session_end","sessionId":%q}`, s.id))
				heartbeatAlive(t, rt)
			}
			if err := rt.close(t); err != nil {
				t.Fatalf("runtime exit: %v", err)
			}
		})
	}
}

// spec: 4.7.10 (Runtime process lifetime), 15.4.6 (session lifetime), 5.2 (per-session multiplexing over stdin)
// diagnosis: an SDK-built Basic runtime did not serve two concurrent
//
//	sessions on one process, each under its own context: an interleaved
//	reply named the other session or its experiment variant, or one
//	session's session_end ended the other or the process. A concurrent pool
//	multiplexes sessions onto one runtime process, so such a runtime
//	answers one tenant's session under another's context.
func TestRuntimeSDKEchoServesConcurrentSessionsOnOneProcess_spec_4_7_10(t *testing.T) {
	for _, sdk := range sdkEchoRuntimes {
		t.Run(sdk.name, func(t *testing.T) {
			dir := t.TempDir()
			rt := startProbeRuntime(t, sdk.build(t), filepath.Join(dir, "absent.json"))
			openWithVariant(t, rt, "sess_a", "control")
			openWithVariant(t, rt, "sess_b", "treatment")
			writeMessage(t, rt, "sess_a")
			writeMessage(t, rt, "sess_b")
			replies := readReplies(t, rt, 2)
			requireContext(t, replies["sess_a"], "sess_a", "control")
			requireContext(t, replies["sess_b"], "sess_b", "treatment")
			rt.send(t, `{"type":"session_end","sessionId":"sess_a"}`)
			heartbeatAlive(t, rt)
			writeMessage(t, rt, "sess_b")
			requireContext(t, readReplies(t, rt, 1)["sess_b"], "sess_b", "treatment")
			if err := rt.close(t); err != nil {
				t.Fatalf("runtime exit: %v", err)
			}
		})
	}
}
