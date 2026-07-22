// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for the §26.3 claude-code adapter bootstrap
// contract that no other test exercises: multi-turn reuse of the spawned
// `claude` child process, and the SIGTERM-then-5s-force-kill teardown
// sequence. The real claude-code adapter binary
// (github.com/lennylabs/runtime-claude-code) is an external repository
// unavailable here, so this suite drives
// tests/testinfra/runtimes/coding-agent-stub, an in-repo double that
// implements the same two observable behaviors §26.3 documents and speaks
// the standard §15.4.1 JSONL envelope, through the real
// pkg/gateway/session/executor.SubprocessExecutor the gateway's own
// developer-loop executor uses to drive a §15.4 Basic-level runtime binary.
//
// §26.2 output-under-/workspace/output/ sealing (the second half of §26.3
// line 222) is already covered end to end, independent of any specific
// runtime, by TestCodingAgentUploadArchiveMaterializesAndSealsToArtifacts
// in this package; this suite does not duplicate it.
//
// spec: §26.3 (claude-code bootstrap behavior, lines 220-222).
package tier4_integration_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: §26.3 line 220 — "On first `message`: adapter invokes `claude`
// ... and pipes the message to stdin. All subsequent messages for the
// session reuse the same `claude` process (multi-turn)."
//
// diagnosis: a failure here means the claude-code bootstrap no longer
// reuses one `claude` child process across a session's messages — either
// no child is being reused at all (a fresh child spawned per message,
// which would multiply LLM proxy credential-lease consumption and lose
// conversation state the CLI keeps in the child's own process memory) or
// the stub/executor plumbing that stands in for it regressed.
func TestClaudeCodeAdapterReusesChildProcessAcrossMessages(t *testing.T) {
	bin := buildCodingAgentStub(t)
	subExec := executor.NewSubprocessExecutor(executor.SubprocessOptions{BinPath: bin})
	ctx := context.Background()
	const sessionID = "sess_claude_code_multiturn"
	t.Cleanup(func() { _ = subExec.Close(ctx, sessionID) })

	var pids []float64
	for i, content := range []string{"one", "two", "three"} {
		resp, err := subExec.Send(ctx, sessionID, []executor.Message{{Content: content}})
		if err != nil {
			t.Fatalf("send message %d: %v", i+1, err)
		}
		if len(resp.Parts) != 1 {
			t.Fatalf("message %d: got %d response part(s), want 1", i+1, len(resp.Parts))
		}
		part := resp.Parts[0]
		if part.Text != "child-ack:"+content {
			t.Errorf("message %d: reply text = %q, want the child's echo of %q", i+1, part.Text, content)
		}
		pid, ok := part.Annotations["childPid"].(float64)
		if !ok {
			t.Fatalf("message %d: response carried no numeric childPid annotation (got %#v)", i+1, part.Annotations["childPid"])
		}
		pids = append(pids, pid)
	}

	for i := 1; i < len(pids); i++ {
		if pids[i] != pids[0] {
			t.Errorf("message %d was handled by child pid %v, want the same pid %v the first message spawned (process reuse broken)",
				i+1, pids[i], pids[0])
		}
	}
}

// spec: §26.3 line 222 — "On session termination: adapter sends SIGTERM
// to `claude`; waits up to 5s for graceful shutdown; force-kills on
// timeout."
//
// diagnosis: a failure here means the claude-code adapter's teardown of
// its `claude` child regressed at one of two failure shapes: it force-
// kills too early (starving a `claude` process that could have exited
// cleanly within the budget, for example losing an in-flight checkpoint
// write) or it never force-kills at all (a hung `claude` process blocks
// session teardown indefinitely instead of being bounded at 5s).
func TestClaudeCodeAdapterForceKillsUnresponsiveChildWithinFiveSeconds(t *testing.T) {
	bin := buildCodingAgentStub(t)
	t.Setenv("CODING_AGENT_STUB_CHILD_IGNORE_SIGTERM", "1")
	subExec := executor.NewSubprocessExecutor(executor.SubprocessOptions{BinPath: bin})
	ctx := context.Background()
	const sessionID = "sess_claude_code_unresponsive_child"

	if _, err := subExec.Send(ctx, sessionID, []executor.Message{{Content: "hello"}}); err != nil {
		t.Fatalf("send first message: %v", err)
	}

	outCh, err := subExec.Output(ctx, sessionID)
	if err != nil {
		t.Fatalf("open output stream: %v", err)
	}

	type report struct {
		line    string
		elapsed time.Duration
	}
	ackCh := make(chan report, 1)
	start := time.Now()
	go func() {
		for line := range outCh {
			s := string(line)
			if containsShutdownAck(s) {
				ackCh <- report{line: s, elapsed: time.Since(start)}
				return
			}
		}
		ackCh <- report{}
	}()

	closeErrCh := make(chan error, 1)
	go func() { closeErrCh <- subExec.Close(ctx, sessionID) }()

	var got report
	select {
	case got = <-ackCh:
	case <-time.After(8 * time.Second):
		t.Fatal("no shutdown_ack observed within 8s of closing the session (force-kill never fired)")
	}
	if err := <-closeErrCh; err != nil {
		t.Fatalf("executor Close: %v", err)
	}

	if got.line == "" {
		t.Fatal("stub exited without emitting a shutdown_ack line")
	}
	if !containsForceKilled(got.line) {
		t.Errorf("shutdown_ack = %q, want childForceKilled:true (the unresponsive child must be force-killed)", got.line)
	}
	// spec: §26.3 line 222 — "waits up to 5s ... force-kills on timeout."
	// The SIGTERM is sent essentially at t=0 (immediately after stdin
	// closes), so the force-kill, and the shutdown_ack that follows it,
	// must land at approximately the 5s mark: comfortably past a
	// near-instant kill (which would mean SIGTERM was never actually
	// tried) and comfortably before it would read as unbounded.
	if got.elapsed < 4*time.Second {
		t.Errorf("shutdown_ack observed after %s, want at least ~5s (SIGTERM must be given its graceful window before the force-kill)", got.elapsed)
	}
	if got.elapsed > 7*time.Second {
		t.Errorf("shutdown_ack observed after %s, want at most ~5s plus scheduling slack (force-kill must be bounded at the spec's 5s deadline)", got.elapsed)
	}
}

func containsShutdownAck(line string) bool {
	return strings.Contains(line, `"type":"shutdown_ack"`)
}

func containsForceKilled(line string) bool {
	return strings.Contains(line, `"childForceKilled":true`)
}

// buildCodingAgentStub compiles the coding-agent-stub test double into a
// temp path.
func buildCodingAgentStub(t *testing.T) string {
	t.Helper()
	root := schematest.RepoRoot(t)
	bin := filepath.Join(t.TempDir(), "coding-agent-stub")
	cmd := exec.Command("go", "build", "-o", bin, "./tests/testinfra/runtimes/coding-agent-stub")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build coding-agent-stub: %v\n%s", err, out)
	}
	return bin
}
