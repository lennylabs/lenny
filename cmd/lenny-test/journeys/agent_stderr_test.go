// SPDX-License-Identifier: MIT

package journeys

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests exercise contributor tooling and carry no spec annotation.

// TestAgentErrorCarriesTheExitCodeAndTheLastStderrLine reproduces the
// first real probe's failure: Claude Code refused to start and wrote only
// a line to standard error, so the record must keep that line and the
// exit code.
func TestAgentErrorCarriesTheExitCodeAndTheLastStderrLine(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "docker")
	body := `#!/bin/sh
case "$1" in
  rm) exit 0 ;;
  network) [ "$2" = inspect ] && echo true; exit 0 ;;
  exec) exit 0 ;;
esac
case "$*" in *--detach*) echo id; exit 0 ;; esac
echo "starting" >&2
echo "Claude Code could not start: bubblewrap is required for subprocess env scrubbing" >&2
exit 1
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := AgentConfig{Docker: script, Image: "sha256:img", Model: DefaultModel, RunCapUSD: 1, Rates: SonnetRates}
	var stderr bytes.Buffer
	run, _ := agentRun(t, time.Minute)
	run.Stderr = &stderr
	res, err := cfg.Run(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stop != StopAgentError || res.ExitCode != 1 || !strings.Contains(res.StopDetail, "stderr: Claude Code could not start") {
		t.Fatalf("result = %+v", res)
	}
	if !strings.Contains(stderr.String(), "starting") {
		t.Errorf("the caller's stderr writer lost output: %q", stderr.String())
	}

	tw := &tailWriter{max: 8}
	_, _ = tw.Write([]byte("first line\nsecond"))
	if got := tw.lastLine(); got != "second" {
		t.Errorf("tail = %q", got)
	}
}
