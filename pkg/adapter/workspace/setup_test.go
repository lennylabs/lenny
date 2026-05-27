// SPDX-License-Identifier: MIT

package workspace_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

func cmd(c string, timeout int32) *adapterv1.SetupCommand {
	return &adapterv1.SetupCommand{Cmd: c, TimeoutSeconds: timeout}
}

func TestRunSetupExecutesCommandsInOrder(t *testing.T) {
	dir := t.TempDir()
	err := workspace.RunSetup(context.Background(), dir, []*adapterv1.SetupCommand{
		cmd("echo first > a.txt", 30),
		cmd("echo second > b.txt", 30),
	}, workspace.SetupOptions{})
	if err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("setup command did not produce %s: %v", name, err)
		}
	}
}

func TestRunSetupRunsInWorkdir(t *testing.T) {
	dir := t.TempDir()
	if err := workspace.RunSetup(context.Background(), dir, []*adapterv1.SetupCommand{
		cmd("touch in-workdir.marker", 30),
	}, workspace.SetupOptions{}); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	// A relative path resolves against the command's working directory;
	// finding the marker under dir confirms the command ran there.
	if _, err := os.Stat(filepath.Join(dir, "in-workdir.marker")); err != nil {
		t.Errorf("setup command did not run in the workspace directory: %v", err)
	}
}

func TestRunSetupStopsAtFirstFailure(t *testing.T) {
	dir := t.TempDir()
	err := workspace.RunSetup(context.Background(), dir, []*adapterv1.SetupCommand{
		cmd("exit 3", 30),
		cmd("echo unreached > reached.txt", 30),
	}, workspace.SetupOptions{})
	if err == nil {
		t.Fatal("RunSetup should return an error when a command exits non-zero")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "reached.txt")); statErr == nil {
		t.Error("a command after the failing one was executed")
	}
}

func TestRunSetupEnforcesTimeout(t *testing.T) {
	dir := t.TempDir()
	err := workspace.RunSetup(context.Background(), dir, []*adapterv1.SetupCommand{
		cmd("sleep 30", 1),
	}, workspace.SetupOptions{})
	if err == nil {
		t.Fatal("RunSetup should return an error when a command exceeds its timeout")
	}
}

func TestRunSetupRejectsEmptyCommand(t *testing.T) {
	if err := workspace.RunSetup(context.Background(), t.TempDir(), []*adapterv1.SetupCommand{
		cmd("", 30),
	}, workspace.SetupOptions{}); err == nil {
		t.Fatal("RunSetup should reject an empty command")
	}
}

func TestRunSetupAcceptsNoCommands(t *testing.T) {
	if err := workspace.RunSetup(context.Background(), t.TempDir(), nil, workspace.SetupOptions{}); err != nil {
		t.Errorf("RunSetup with no commands should succeed, got %v", err)
	}
}

func TestRunSetupWithinAggregateTimeout(t *testing.T) {
	// Fast commands complete well within a generous aggregate cap.
	dir := t.TempDir()
	err := workspace.RunSetup(context.Background(), dir, []*adapterv1.SetupCommand{
		cmd("echo a > a.txt", 30),
		cmd("echo b > b.txt", 30),
	}, workspace.SetupOptions{AggregateTimeout: 30 * time.Second, FailOnAggregateTimeout: true})
	if err != nil {
		t.Fatalf("RunSetup within the aggregate cap: %v", err)
	}
}

func TestRunSetupAggregateTimeoutFails(t *testing.T) {
	// §5.1 onTimeout `fail`: exceeding the aggregate cap aborts.
	err := workspace.RunSetup(context.Background(), t.TempDir(), []*adapterv1.SetupCommand{
		cmd("sleep 30", 60),
	}, workspace.SetupOptions{AggregateTimeout: 50 * time.Millisecond, FailOnAggregateTimeout: true})
	if err == nil {
		t.Fatal("RunSetup should fail when the setup phase exceeds the aggregate cap under `fail`")
	}
}

func TestRunSetupAggregateTimeoutWarnProceeds(t *testing.T) {
	// §5.1 onTimeout `warn`: exceeding the aggregate cap proceeds.
	err := workspace.RunSetup(context.Background(), t.TempDir(), []*adapterv1.SetupCommand{
		cmd("sleep 30", 60),
	}, workspace.SetupOptions{AggregateTimeout: 50 * time.Millisecond, FailOnAggregateTimeout: false})
	if err != nil {
		t.Fatalf("RunSetup under `warn` should proceed past the aggregate cap, got %v", err)
	}
}

func TestRunSetupAggregateTimeoutStopsLaterCommands(t *testing.T) {
	dir := t.TempDir()
	err := workspace.RunSetup(context.Background(), dir, []*adapterv1.SetupCommand{
		cmd("sleep 30", 60),
		cmd("touch reached.marker", 60),
	}, workspace.SetupOptions{AggregateTimeout: 50 * time.Millisecond, FailOnAggregateTimeout: false})
	if err != nil {
		t.Fatalf("RunSetup under `warn`: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "reached.marker")); statErr == nil {
		t.Error("a command after the aggregate cap was reached should not have run")
	}
}

// spec: §14 line 99 — an omitted per-command timeoutSeconds carries no
// independent time limit; only the aggregate cap (or parent ctx) can
// terminate the command. F-7.5.6.
func TestRunSetupOmittedPerCommandTimeoutBoundsByAggregate(t *testing.T) {
	// With an aggregate cap of 100ms and no per-command timeout, the
	// long sleep is terminated by the aggregate, not by a (removed) 5m
	// per-command default.
	start := time.Now()
	err := workspace.RunSetup(context.Background(), t.TempDir(), []*adapterv1.SetupCommand{
		cmd("sleep 30", 0),
	}, workspace.SetupOptions{AggregateTimeout: 100 * time.Millisecond, FailOnAggregateTimeout: true})
	if err == nil {
		t.Fatal("RunSetup should fail when the setup phase exceeds the aggregate cap")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("aggregate cap did not bound the command: took %s", d)
	}
}

// spec: §14 line 99 — an omitted per-command timeout under no aggregate
// cap inherits only the parent ctx; cancelling ctx kills the command.
func TestRunSetupOmittedPerCommandTimeoutCancelsOnContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := workspace.RunSetup(ctx, t.TempDir(), []*adapterv1.SetupCommand{
		cmd("sleep 30", 0),
	}, workspace.SetupOptions{})
	if err == nil {
		t.Fatal("RunSetup should fail when the parent ctx is cancelled")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("parent ctx did not bound the command: took %s", d)
	}
}

// spec: §7.5 line 477 — a per-command timeout (or aggregate-cap kill)
// must reach descendants, not just the shell. F-7.5.7.
func TestRunSetupTimeoutKillsDescendants(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	// The shell backgrounds a long sleep and records its pid, then
	// itself sleeps long enough to exceed the per-command timeout. With
	// process-group kill the backgrounded sleep is also terminated;
	// without it, the sleep keeps running for the full 30s.
	start := time.Now()
	err := workspace.RunSetup(context.Background(), dir, []*adapterv1.SetupCommand{
		cmd("sh -c 'sleep 30 & echo $! > "+pidFile+"; sleep 30'", 1),
	}, workspace.SetupOptions{})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("per-command timeout did not return promptly: took %s", d)
	}
	// Give the kill a moment to land before reading the pid file.
	time.Sleep(200 * time.Millisecond)
	raw, readErr := os.ReadFile(pidFile)
	if readErr != nil {
		t.Skipf("setup never recorded a child pid (%v); skipping descendant-kill check", readErr)
	}
	pidStr := strings.TrimSpace(string(raw))
	if pidStr == "" {
		t.Skip("child pid file empty; skipping descendant-kill check")
	}
	pid, _ := strconv.Atoi(pidStr)
	if pid <= 0 {
		t.Skipf("could not parse child pid %q", pidStr)
	}
	// Probe the child pid; a zero-signal Kill on a still-running
	// process succeeds, on a dead one returns ESRCH or "process already
	// finished".
	if proc, err := os.FindProcess(pid); err == nil {
		if err := proc.Signal(syscall.Signal(0)); err == nil {
			// Some kernels also report success for a defunct (zombie)
			// child; treat that case as killed too.
			t.Errorf("descendant pid %d still alive after process-group kill", pid)
			_ = proc.Kill()
		}
	}
}

// spec: §7.5 line 479 — setup commands must not inherit arbitrary
// adapter-process state. With opts.Env set, the command sees only the
// configured environment. F-7.5.8.
func TestRunSetupAppliesEnvWhitelist(t *testing.T) {
	dir := t.TempDir()
	// Seed a sentinel in the parent process the setup command must NOT
	// see when opts.Env is set.
	t.Setenv("LENNY_SECRET_SENTINEL", "leaked")
	env := append(workspace.DefaultSetupEnv(dir), "EXTRA=ok")
	err := workspace.RunSetup(context.Background(), dir, []*adapterv1.SetupCommand{
		cmd("env > env.txt", 30),
	}, workspace.SetupOptions{Env: env})
	if err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "env.txt"))
	if err != nil {
		t.Fatalf("read env.txt: %v", err)
	}
	got := string(raw)
	if strings.Contains(got, "LENNY_SECRET_SENTINEL") {
		t.Errorf("env whitelist leaked LENNY_SECRET_SENTINEL: %s", got)
	}
	if !strings.Contains(got, "EXTRA=ok") {
		t.Errorf("env whitelist dropped configured EXTRA: %s", got)
	}
	if !strings.Contains(got, "PATH=") {
		t.Errorf("env whitelist dropped PATH: %s", got)
	}
}

// DefaultSetupEnv exposes the §7.5 line 479 minimal whitelist; the
// returned list seeds only PATH/HOME/USER/LANG/LC_ALL/PWD/TMPDIR.
func TestDefaultSetupEnv(t *testing.T) {
	env := workspace.DefaultSetupEnv("/workspace/current")
	required := []string{"PATH=", "HOME=", "USER=", "LANG=", "LC_ALL=", "PWD=/workspace/current", "TMPDIR=/tmp"}
	for _, prefix := range required {
		var found bool
		for _, e := range env {
			if strings.HasPrefix(e, prefix) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("DefaultSetupEnv missing %q in %v", prefix, env)
		}
	}
	// A nil workdir omits the path-scoped keys.
	noWorkdir := workspace.DefaultSetupEnv("")
	for _, e := range noWorkdir {
		if strings.HasPrefix(e, "PWD=") || strings.HasPrefix(e, "TMPDIR=") {
			t.Errorf("DefaultSetupEnv(\"\") should omit PWD/TMPDIR, got %q", e)
		}
	}
}
