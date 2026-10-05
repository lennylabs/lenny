// SPDX-License-Identifier: MIT

package executor_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
)

// echoBinary is the compiled cmd/runtimes/echo path, built once in
// TestMain.
var echoBinary string

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "executor-echo-*")
	if err != nil {
		panic("executor TestMain: mkdtemp: " + err.Error())
	}
	defer os.RemoveAll(tmp)

	echoBinary = filepath.Join(tmp, "echo")
	cmd := exec.Command("go", "build", "-o", echoBinary, "./cmd/runtimes/echo")
	cmd.Dir = repoRoot()
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		panic("executor TestMain: build echo: " + err.Error())
	}
	os.Exit(m.Run())
}

func repoRoot() string {
	wd, _ := os.Getwd()
	for d := wd; d != "/" && d != ""; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	panic("executor test: could not locate repo root")
}

func TestSubprocessExecutorEchoesMessage(t *testing.T) {
	exec := executor.NewSubprocessExecutor(executor.SubprocessOptions{
		BinPath:     echoBinary,
		SendTimeout: 10 * time.Second,
	})
	out, err := exec.Send(context.Background(), "sess_1", []executor.Message{
		{Role: "user", Content: "hello world"},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(out.Parts) != 1 {
		t.Fatalf("output parts: got %d, want 1 (%+v)", len(out.Parts), out.Parts)
	}
	if out.Parts[0].Type != "text" {
		t.Errorf("part type: %q", out.Parts[0].Type)
	}
	if !strings.Contains(out.Parts[0].Text, "hello world") {
		t.Errorf("echo output does not contain input: %q", out.Parts[0].Text)
	}
	if err := exec.Close(context.Background(), "sess_1"); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestSubprocessExecutorMultipleMessagesSameSession(t *testing.T) {
	exec := executor.NewSubprocessExecutor(executor.SubprocessOptions{
		BinPath:     echoBinary,
		SendTimeout: 10 * time.Second,
	})
	defer exec.Close(context.Background(), "sess_2")

	for i, content := range []string{"first", "second", "third"} {
		out, err := exec.Send(context.Background(), "sess_2", []executor.Message{
			{Role: "user", Content: content},
		})
		if err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
		if len(out.Parts) != 1 || !strings.Contains(out.Parts[0].Text, content) {
			t.Errorf("Send %d output: %+v", i, out)
		}
	}
}

func TestSubprocessExecutorIndependentSessions(t *testing.T) {
	exec := executor.NewSubprocessExecutor(executor.SubprocessOptions{
		BinPath:     echoBinary,
		SendTimeout: 10 * time.Second,
	})
	defer exec.Close(context.Background(), "a")
	defer exec.Close(context.Background(), "b")

	outA, err := exec.Send(context.Background(), "a", []executor.Message{{Content: "from-a"}})
	if err != nil {
		t.Fatalf("Send a: %v", err)
	}
	outB, err := exec.Send(context.Background(), "b", []executor.Message{{Content: "from-b"}})
	if err != nil {
		t.Fatalf("Send b: %v", err)
	}
	if !strings.Contains(outA.Parts[0].Text, "from-a") {
		t.Errorf("session a: %q", outA.Parts[0].Text)
	}
	if !strings.Contains(outB.Parts[0].Text, "from-b") {
		t.Errorf("session b: %q", outB.Parts[0].Text)
	}
}

func TestSubprocessExecutorCloseUnopenedIsNoOp(t *testing.T) {
	exec := executor.NewSubprocessExecutor(executor.SubprocessOptions{BinPath: echoBinary})
	if err := exec.Close(context.Background(), "never-spawned"); err != nil {
		t.Errorf("Close unopened: %v", err)
	}
}

func TestSubprocessExecutorStartSpawnsProcess(t *testing.T) {
	exec := executor.NewSubprocessExecutor(executor.SubprocessOptions{
		BinPath:     echoBinary,
		SendTimeout: 10 * time.Second,
	})
	defer exec.Close(context.Background(), "eager")

	if err := exec.Start(context.Background(), "eager"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// A second Start for the same session is a no-op, not a respawn.
	if err := exec.Start(context.Background(), "eager"); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	// The eagerly-started process handles a message without a respawn.
	out, err := exec.Send(context.Background(), "eager", []executor.Message{{Content: "ping"}})
	if err != nil {
		t.Fatalf("Send after Start: %v", err)
	}
	if len(out.Parts) != 1 || !strings.Contains(out.Parts[0].Text, "ping") {
		t.Errorf("Send after Start output: %+v", out)
	}
}

func TestSubprocessExecutorWriteEnvelope(t *testing.T) {
	exec := executor.NewSubprocessExecutor(executor.SubprocessOptions{
		BinPath:     echoBinary,
		SendTimeout: 10 * time.Second,
	})
	defer exec.Close(context.Background(), "enveloped")

	if err := exec.Start(context.Background(), "enveloped"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := exec.WriteEnvelope("enveloped", []byte(`{"type":"heartbeat"}`)); err != nil {
		t.Errorf("WriteEnvelope to a started session: %v", err)
	}
}

func TestSubprocessExecutorWriteEnvelopeUnstartedSessionErrors(t *testing.T) {
	exec := executor.NewSubprocessExecutor(executor.SubprocessOptions{BinPath: echoBinary})
	if err := exec.WriteEnvelope("never-started", []byte(`{}`)); err == nil {
		t.Error("WriteEnvelope to an unstarted session should error")
	}
}

func TestSubprocessExecutorStartBadBinaryErrors(t *testing.T) {
	exec := executor.NewSubprocessExecutor(executor.SubprocessOptions{
		BinPath: "/nonexistent/runtime/binary",
	})
	if err := exec.Start(context.Background(), "sess_bad"); err == nil {
		t.Error("Start against a missing binary should error")
	}
}

func TestSubprocessExecutorBadBinaryErrors(t *testing.T) {
	exec := executor.NewSubprocessExecutor(executor.SubprocessOptions{
		BinPath: "/nonexistent/runtime/binary",
	})
	_, err := exec.Send(context.Background(), "sess_x", []executor.Message{{Content: "x"}})
	if err == nil {
		t.Error("Send against a missing binary should error")
	}
}

func TestSubprocessExecutorCleanInterrupt(t *testing.T) {
	exec := executor.NewSubprocessExecutor(executor.SubprocessOptions{
		BinPath:     echoBinary,
		SendTimeout: 10 * time.Second,
	})
	if err := exec.Start(context.Background(), "sess_clean"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := exec.Interrupt(context.Background(), "sess_clean", false); err != nil {
		t.Errorf("clean Interrupt: %v", err)
	}
	_ = exec.Close(context.Background(), "sess_clean")
}

func TestSubprocessExecutorHardInterruptKillsRuntime(t *testing.T) {
	exec := executor.NewSubprocessExecutor(executor.SubprocessOptions{
		BinPath:     echoBinary,
		SendTimeout: 10 * time.Second,
	})
	if err := exec.Start(context.Background(), "sess_hard"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := exec.Interrupt(context.Background(), "sess_hard", true); err != nil {
		t.Fatalf("hard Interrupt: %v", err)
	}
	// SIGKILL terminated the runtime, so a subsequent Send must fail.
	if _, err := exec.Send(context.Background(), "sess_hard", []executor.Message{
		{Role: "user", Content: "after kill"},
	}); err == nil {
		t.Error("Send succeeded after a hard interrupt, want a failure")
	}
	_ = exec.Close(context.Background(), "sess_hard")
}

func TestSubprocessExecutorInterruptUnknownSession(t *testing.T) {
	exec := executor.NewSubprocessExecutor(executor.SubprocessOptions{
		BinPath: echoBinary,
	})
	if err := exec.Interrupt(context.Background(), "sess_missing", false); err == nil {
		t.Error("Interrupt of an unknown session succeeded, want a failure")
	}
}

func TestSubprocessExecutorOutputUnknownSession(t *testing.T) {
	exec := executor.NewSubprocessExecutor(executor.SubprocessOptions{
		BinPath: echoBinary,
	})
	if _, err := exec.Output(context.Background(), "sess_missing"); err == nil {
		t.Error("Output for an unknown session succeeded, want a failure")
	}
}

func TestSubprocessExecutorOutputClosesWhenRuntimeExits(t *testing.T) {
	exec := executor.NewSubprocessExecutor(executor.SubprocessOptions{
		BinPath: echoBinary,
	})
	if err := exec.Start(context.Background(), "sess_out"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	out, err := exec.Output(context.Background(), "sess_out")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	// Closing the runtime EOFs its stdout, which closes the channel.
	_ = exec.Close(context.Background(), "sess_out")
	deadline := time.After(10 * time.Second)
	for {
		select {
		case _, ok := <-out:
			if !ok {
				return // channel closed — the expected outcome
			}
		case <-deadline:
			t.Fatal("the Output channel did not close after the runtime exited")
		}
	}
}

// TestSubprocessExecutorServesNextSession_spec_5_2 pins that the subprocess
// executor reports itself able to serve the pod's next session: it spawns a
// fresh runtime process per session, so the adapter's whole-pod scrub report
// carries runtime_live true and the gateway does not retire the pod with
// runtime_not_live.
// spec: 5.2 (Pod retirement policy), 4.7 (ReportPodScrub)
func TestSubprocessExecutorServesNextSession_spec_5_2(t *testing.T) {
	e := executor.NewSubprocessExecutor(executor.SubprocessOptions{BinPath: "/bin/true"})
	if !e.ServesNextSession() {
		t.Error("ServesNextSession() = false, want true for a per-session subprocess")
	}
}

// spec: 28.5.3 (CH-MSGSOCK Outbound: session_started), 17.4 (Local Development Mode)
// The subprocess transport's Output fans the child's stdout out to every
// live subscriber. A start's session_started wait subscribes beside the
// Attach stream, reads the acknowledgement, and cancels; the cancelled
// subscription consumes nothing afterwards, so the first response the child
// writes after the wait reaches the Attach subscription. Before the
// fan-out, each Output scanned the shared stdout itself, and the cancelled
// wait's reader went on consuming frames meant for Attach.
func TestSubprocessExecutorOutputFansOutPastACancelledWait_spec_28_5_3(t *testing.T) {
	cat, err := exec.LookPath("cat")
	if err != nil {
		t.Skipf("cat not on PATH: %v", err)
	}
	e := executor.NewSubprocessExecutor(executor.SubprocessOptions{BinPath: cat})
	defer e.Close(context.Background(), "sess_fan")
	if err := e.Start(context.Background(), "sess_fan"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	attach, err := e.Output(context.Background(), "sess_fan")
	if err != nil {
		t.Fatalf("Attach Output: %v", err)
	}
	waitCtx, cancelWait := context.WithCancel(context.Background())
	wait, err := e.Output(waitCtx, "sess_fan")
	if err != nil {
		t.Fatalf("wait Output: %v", err)
	}
	if err := e.WriteEnvelope("sess_fan", []byte(`{"type":"session_started"}`)); err != nil {
		t.Fatalf("write acknowledgement: %v", err)
	}
	if got := recvFrame(t, wait); !strings.Contains(got, "session_started") {
		t.Fatalf("wait read %q, want the acknowledgement", got)
	}
	cancelWait()
	if err := e.WriteEnvelope("sess_fan", []byte(`{"type":"response"}`)); err != nil {
		t.Fatalf("write response: %v", err)
	}
	if got := recvFrame(t, attach); !strings.Contains(got, "session_started") {
		t.Fatalf("Attach read %q first, want the acknowledgement", got)
	}
	if got := recvFrame(t, attach); !strings.Contains(got, `"response"`) {
		t.Fatalf("Attach read %q, want the response written after the wait", got)
	}
}

// recvFrame receives one frame or fails the test after five seconds.
func recvFrame(t *testing.T, ch <-chan []byte) string {
	t.Helper()
	select {
	case b, ok := <-ch:
		if !ok {
			t.Fatal("the subscription closed before the frame")
		}
		return string(b)
	case <-time.After(5 * time.Second):
		t.Fatal("no frame within 5s")
	}
	return ""
}
