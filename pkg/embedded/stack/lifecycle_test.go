// SPDX-License-Identifier: MIT

package stack

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunDownNoStack(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LENNY_HOME", root)
	var out bytes.Buffer
	err := RunDown(context.Background(), DownOptions{Out: &out})
	if err != nil {
		t.Fatalf("RunDown with no stack: %v", err)
	}
	if !strings.Contains(out.String(), "no embedded stack is running") {
		t.Errorf("RunDown output = %q, want a no-stack message", out.String())
	}
}

func TestRunDownPurgeRemovesRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "lenny-state")
	t.Setenv("LENNY_HOME", root)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("seed state dir: %v", err)
	}
	marker := filepath.Join(root, "marker")
	if err := os.WriteFile(marker, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed marker: %v", err)
	}
	var out bytes.Buffer
	if err := RunDown(context.Background(), DownOptions{Purge: true, Out: &out}); err != nil {
		t.Fatalf("RunDown --purge: %v", err)
	}
	// §17.4: lenny down --purge removes ~/.lenny entirely.
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Error("state directory still present after --purge")
	}
}

func TestRunDownStaleStateFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LENNY_HOME", root)
	paths := NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	// A state file whose recorded PIDs are dead: RunDown must clear it
	// without error.
	stale := State{SupervisorPID: 1 << 30, GatewayPID: 1 << 30, K3sEnabled: false}
	if err := writeState(paths.StateFile(), stale); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	var out bytes.Buffer
	if err := RunDown(context.Background(), DownOptions{Out: &out}); err != nil {
		t.Fatalf("RunDown with a stale state file: %v", err)
	}
	if _, err := os.Stat(paths.StateFile()); !os.IsNotExist(err) {
		t.Error("RunDown left the stale state file in place")
	}
}

func TestRunLogsNoLogs(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LENNY_HOME", root)
	var out bytes.Buffer
	if err := RunLogs(LogsOptions{Out: &out}); err != nil {
		t.Fatalf("RunLogs: %v", err)
	}
	if !strings.Contains(out.String(), "no log files found") {
		t.Errorf("RunLogs output = %q, want a no-logs message", out.String())
	}
}

func TestRunLogsUnknownComponent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LENNY_HOME", root)
	err := RunLogs(LogsOptions{Component: "nonsense", Out: &bytes.Buffer{}})
	if err == nil {
		t.Fatal("expected RunLogs to reject an unknown component")
	}
}

func TestRunLogsFiltersComponent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LENNY_HOME", root)
	paths := NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	if err := os.WriteFile(logFilePath(paths, "gateway"), []byte("gw-line\n"), 0o644); err != nil {
		t.Fatalf("seed gateway log: %v", err)
	}
	if err := os.WriteFile(logFilePath(paths, "controller"), []byte("ctl-line\n"), 0o644); err != nil {
		t.Fatalf("seed controller log: %v", err)
	}
	var out bytes.Buffer
	if err := RunLogs(LogsOptions{Component: "gateway", Out: &out}); err != nil {
		t.Fatalf("RunLogs: %v", err)
	}
	if !strings.Contains(out.String(), "gw-line") {
		t.Errorf("RunLogs output %q missing the gateway line", out.String())
	}
	if strings.Contains(out.String(), "ctl-line") {
		t.Errorf("RunLogs filtered to gateway leaked the controller line: %q", out.String())
	}
}

func TestRunLogsMergesAndPrefixes(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LENNY_HOME", root)
	paths := NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	if err := os.WriteFile(logFilePath(paths, "gateway"), []byte("gw-line\n"), 0o644); err != nil {
		t.Fatalf("seed gateway log: %v", err)
	}
	if err := os.WriteFile(logFilePath(paths, "controller"), []byte("ctl-line\n"), 0o644); err != nil {
		t.Fatalf("seed controller log: %v", err)
	}
	var out bytes.Buffer
	if err := RunLogs(LogsOptions{Out: &out}); err != nil {
		t.Fatalf("RunLogs: %v", err)
	}
	// A merged stream prefixes each line with its component name.
	if !strings.Contains(out.String(), "gateway | gw-line") {
		t.Errorf("merged output %q missing the prefixed gateway line", out.String())
	}
	if !strings.Contains(out.String(), "controller | ctl-line") {
		t.Errorf("merged output %q missing the prefixed controller line", out.String())
	}
}

func TestCollectStatusNoStack(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LENNY_HOME", root)
	st, err := CollectStatus(context.Background(), StatusOptions{})
	if err != nil {
		t.Fatalf("CollectStatus: %v", err)
	}
	if st.Running {
		t.Error("CollectStatus reported a running stack with no state file")
	}
	if st.ActiveSessions != -1 {
		t.Errorf("ActiveSessions = %d, want -1 when no stack runs", st.ActiveSessions)
	}
	var out bytes.Buffer
	WriteStatus(&out, st)
	if !strings.Contains(out.String(), "no embedded stack is running") {
		t.Errorf("WriteStatus output = %q", out.String())
	}
}

func TestCollectStatusRecordedStack(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LENNY_HOME", root)
	paths := NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	// Record a stack whose gateway PID is dead so the probe reports
	// the gateway as down without needing a real process.
	st := State{
		SupervisorPID: os.Getpid(),
		GatewayPID:    1 << 30,
		HTTPAddr:      "127.0.0.1:8080",
		HTTPSAddr:     "127.0.0.1:8443",
		K3sEnabled:    false,
	}
	if err := writeState(paths.StateFile(), st); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	status, err := CollectStatus(context.Background(), StatusOptions{})
	if err != nil {
		t.Fatalf("CollectStatus: %v", err)
	}
	if !status.Running {
		t.Fatal("CollectStatus reported the recorded stack as not running")
	}
	byName := map[string]ComponentStatus{}
	for _, c := range status.Components {
		byName[c.Name] = c
	}
	if c, ok := byName["supervisor"]; !ok || !c.Healthy {
		t.Errorf("supervisor component = %+v, want healthy (current process)", c)
	}
	if c, ok := byName["gateway"]; !ok || c.Healthy {
		t.Errorf("gateway component = %+v, want unhealthy (dead PID)", c)
	}
	if c, ok := byName["k3s"]; !ok || c.Healthy {
		t.Errorf("k3s component = %+v, want down when K3sEnabled is false", c)
	}
}
