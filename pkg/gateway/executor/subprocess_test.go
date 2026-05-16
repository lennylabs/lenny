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

	"github.com/lennylabs/lenny/pkg/gateway/executor"
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
	if len(out) != 1 {
		t.Fatalf("output parts: got %d, want 1 (%+v)", len(out), out)
	}
	if out[0].Type != "text" {
		t.Errorf("part type: %q", out[0].Type)
	}
	if !strings.Contains(out[0].Text, "hello world") {
		t.Errorf("echo output does not contain input: %q", out[0].Text)
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
		if len(out) != 1 || !strings.Contains(out[0].Text, content) {
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
	if !strings.Contains(outA[0].Text, "from-a") {
		t.Errorf("session a: %q", outA[0].Text)
	}
	if !strings.Contains(outB[0].Text, "from-b") {
		t.Errorf("session b: %q", outB[0].Text)
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
	if len(out) != 1 || !strings.Contains(out[0].Text, "ping") {
		t.Errorf("Send after Start output: %+v", out)
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
