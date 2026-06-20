// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// spec: §24.19 line 264 — only the supervised child processes (gateway,
// controller) are individually restartable; the in-process components
// are cycled with down/up.
func TestRestartableComponents_spec_24_19_264(t *testing.T) {
	if !Restartable("gateway") || !Restartable("controller") {
		t.Error("gateway and controller must be restartable")
	}
	for _, name := range []string{"redis", "postgres", "oidc", "k3s", "supervisor", ""} {
		if Restartable(name) {
			t.Errorf("Restartable(%q) = true, want false", name)
		}
	}
}

// spec: §24.19 line 264 — an unrecognised component is rejected with a
// message naming the restartable set rather than silently no-op'ing.
func TestRestartComponentRejectsUnknown_spec_24_19_264(t *testing.T) {
	s := &Stack{}
	err := s.RestartComponent(context.Background(), "redis")
	if err == nil {
		t.Fatal("RestartComponent(redis) should error")
	}
	if !strings.Contains(err.Error(), "gateway and controller") {
		t.Errorf("error = %v, want it to name the restartable components", err)
	}
}

// A gateway restart with no retained spec (e.g. a Stack reconstructed
// from disk rather than from Up) reports the limitation instead of
// spawning a process with an empty binary path.
func TestRestartComponentGatewayWithoutSpec(t *testing.T) {
	s := &Stack{}
	if err := s.RestartComponent(context.Background(), "gateway"); err == nil {
		t.Fatal("RestartComponent(gateway) without a spec should error")
	}
}

func TestRunRestartRequiresComponent(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	err := RunRestart(context.Background(), RestartOptions{Component: ""})
	if err == nil || !strings.Contains(err.Error(), "component") {
		t.Errorf("empty component error = %v, want a required-argument error", err)
	}
}

func TestRunRestartRejectsUnknownComponent(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	err := RunRestart(context.Background(), RestartOptions{Component: "redis"})
	if err == nil || !strings.Contains(err.Error(), "cannot be restarted individually") {
		t.Errorf("unknown-component error = %v, want a rejection", err)
	}
}

// spec: §24.19 line 264 — restart against a stack that is not running
// reports ErrNoRunningStack so the CLI can present a precise message.
func TestRunRestartNoStack_spec_24_19_264(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	err := RunRestart(context.Background(), RestartOptions{Component: "gateway"})
	if !errors.Is(err, ErrNoRunningStack) {
		t.Errorf("error = %v, want ErrNoRunningStack", err)
	}
}

// A recorded stack whose supervisor PID is dead cannot service a restart
// request; RunRestart fails fast rather than signalling a stale PID.
func TestRunRestartDeadSupervisor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LENNY_HOME", home)
	paths := NewPaths(home)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	// A PID above the kernel maximum is never alive.
	if err := writeState(paths.StateFile(), State{SupervisorPID: 1 << 30, HTTPAddr: "127.0.0.1:8080"}); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	err := RunRestart(context.Background(), RestartOptions{Component: "gateway"})
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Errorf("error = %v, want a dead-supervisor error", err)
	}
}

// handleRestartRequest is the supervisor's restart-wakeup body: it reads
// the component name, restarts it, and writes the outcome so the separate
// `lenny restart` process can report success or failure. The wakeup is an
// OS-specific signal (SIGHUP on unix, a named event on Windows).
func TestHandleRestartRequestWritesResult(t *testing.T) {
	home := t.TempDir()
	paths := NewPaths(home)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	if err := os.WriteFile(paths.RestartRequestFile(), []byte("redis\n"), 0o600); err != nil {
		t.Fatalf("write request: %v", err)
	}
	s := &Stack{}
	s.handleRestartRequest(context.Background(), paths)

	if _, err := os.Stat(paths.RestartRequestFile()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("request file should be cleared after handling, stat err = %v", err)
	}
	b, err := os.ReadFile(paths.RestartResultFile())
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	var res restartResult
	if err := json.Unmarshal(b, &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if res.Component != "redis" || res.OK {
		t.Errorf("result = %+v, want component=redis ok=false", res)
	}
	if res.Error == "" {
		t.Error("a failed restart should record an error message")
	}
}

// A spurious restart wakeup with no pending request is harmless: no
// result file is produced.
func TestHandleRestartRequestNoRequest(t *testing.T) {
	home := t.TempDir()
	paths := NewPaths(home)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	s := &Stack{}
	s.handleRestartRequest(context.Background(), paths)
	if _, err := os.Stat(paths.RestartResultFile()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("no result file should be written for a missing request, stat err = %v", err)
	}
}

func TestWaitRestartResultParses(t *testing.T) {
	home := t.TempDir()
	paths := NewPaths(home)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	want := restartResult{Component: "gateway", OK: true}
	data, _ := json.Marshal(want)
	if err := os.WriteFile(paths.RestartResultFile(), data, 0o600); err != nil {
		t.Fatalf("write result: %v", err)
	}
	got, err := waitRestartResult(context.Background(), paths, 2*time.Second)
	if err != nil {
		t.Fatalf("waitRestartResult: %v", err)
	}
	if got.Component != "gateway" || !got.OK {
		t.Errorf("result = %+v, want gateway ok=true", got)
	}
	// The result file is consumed so the next request starts clean.
	if _, err := os.Stat(paths.RestartResultFile()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("result file should be removed after reading, stat err = %v", err)
	}
}

func TestWaitRestartResultTimesOut(t *testing.T) {
	home := t.TempDir()
	paths := NewPaths(home)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	_, err := waitRestartResult(context.Background(), paths, 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "did not acknowledge") {
		t.Errorf("error = %v, want a timeout error", err)
	}
}
