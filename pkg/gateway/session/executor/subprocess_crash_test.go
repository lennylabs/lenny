// SPDX-License-Identifier: MIT

package executor_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/sessionrecord"
)

// writeRuntimeScript drops an executable POSIX sh runtime that consumes
// the inbound message line, then runs `body`.
func writeRuntimeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runtime.sh")
	script := "#!/bin/sh\nread line\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write runtime script: %v", err)
	}
	return path
}

// spec: §15.4.1 line 1889 — a runtime that exits non-zero without
// emitting a `response` is reported as a synthesized RUNTIME_CRASH
// carrying the exit code and stderr (MED-016).
func TestSubprocessExecutorSynthesizesRuntimeCrash_spec_15_4_1_1889(t *testing.T) {
	bin := writeRuntimeScript(t, "echo 'boom-detail' >&2\nexit 7")
	exec := executor.NewSubprocessExecutor(executor.SubprocessOptions{
		BinPath:     bin,
		SendTimeout: 10 * time.Second,
	})
	defer exec.Close(context.Background(), "crash")

	_, err := exec.Send(context.Background(), "crash", []executor.Message{{Content: "hi"}})
	if err == nil {
		t.Fatal("Send against a crashing runtime returned no error")
	}
	var te *sessionrecord.Error
	if !errors.As(err, &te) {
		t.Fatalf("error is not a *sessionrecord.Error RUNTIME_CRASH: %v", err)
	}
	if te.Code != "RUNTIME_CRASH" {
		t.Errorf("code = %q, want RUNTIME_CRASH", te.Code)
	}
	if te.Category != "TRANSIENT" {
		t.Errorf("category = %q, want TRANSIENT", te.Category)
	}
	if !strings.Contains(te.Message, "code 7") {
		t.Errorf("message omits exit code: %q", te.Message)
	}
	if !strings.Contains(te.Message, "boom-detail") {
		t.Errorf("message omits captured stderr: %q", te.Message)
	}
}

// spec: §15.4.1 line 1889 — a CLEAN (code 0) exit without a response is a
// protocol error, not a crash: the gateway must not mislabel it
// RUNTIME_CRASH.
func TestSubprocessExecutorCleanExitNoResponseIsNotCrash_spec_15_4_1_1889(t *testing.T) {
	bin := writeRuntimeScript(t, "exit 0")
	exec := executor.NewSubprocessExecutor(executor.SubprocessOptions{
		BinPath:     bin,
		SendTimeout: 10 * time.Second,
	})
	defer exec.Close(context.Background(), "clean")

	_, err := exec.Send(context.Background(), "clean", []executor.Message{{Content: "hi"}})
	if err == nil {
		t.Fatal("Send against a runtime that exits without responding returned no error")
	}
	var te *sessionrecord.Error
	if errors.As(err, &te) {
		t.Errorf("a clean exit was mislabeled a RUNTIME_CRASH: %v", te)
	}
}
