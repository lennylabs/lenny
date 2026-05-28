// SPDX-License-Identifier: MIT

package stack

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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
	if err := RunLogs(context.Background(), LogsOptions{Out: &out}); err != nil {
		t.Fatalf("RunLogs: %v", err)
	}
	if !strings.Contains(out.String(), "no log files found") {
		t.Errorf("RunLogs output = %q, want a no-logs message", out.String())
	}
}

func TestRunLogsUnknownComponent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LENNY_HOME", root)
	err := RunLogs(context.Background(), LogsOptions{Component: "nonsense", Out: &bytes.Buffer{}})
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
	if err := RunLogs(context.Background(), LogsOptions{Component: "gateway", Out: &out}); err != nil {
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
	if err := RunLogs(context.Background(), LogsOptions{Out: &out}); err != nil {
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

// TestRunLogsAcceptsExpandedComponentList covers the §24.19 line 263
// component allow-list: ops, postgres, redis, kms, oidc, and
// runtime-<name>.
//
// spec: §24.19 line 263.
func TestRunLogsAcceptsExpandedComponentList_spec_24_19_263(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LENNY_HOME", root)
	paths := NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	for _, c := range []string{"ops", "postgres", "redis", "kms", "oidc", "supervisor"} {
		if err := os.WriteFile(logFilePath(paths, c), []byte(c+"-line\n"), 0o644); err != nil {
			t.Fatalf("seed %s log: %v", c, err)
		}
	}
	for _, c := range []string{"ops", "postgres", "redis", "kms", "oidc"} {
		var out bytes.Buffer
		if err := RunLogs(context.Background(), LogsOptions{Component: c, Out: &out}); err != nil {
			t.Fatalf("RunLogs %s: %v", c, err)
		}
		if !strings.Contains(out.String(), c+"-line") {
			t.Errorf("RunLogs %s output %q missing seed line", c, out.String())
		}
	}
}

// TestRunLogsAcceptsRuntimeNameComponent covers the §24.19 line 263
// `runtime-<name>` filter form.
//
// spec: §24.19 line 263.
func TestRunLogsAcceptsRuntimeNameComponent_spec_24_19_263(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LENNY_HOME", root)
	paths := NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	if err := os.WriteFile(logFilePath(paths, "runtime-claude-code"), []byte("rt-line\n"), 0o644); err != nil {
		t.Fatalf("seed runtime log: %v", err)
	}
	if err := os.WriteFile(logFilePath(paths, "runtime-codex"), []byte("codex-line\n"), 0o644); err != nil {
		t.Fatalf("seed runtime log: %v", err)
	}
	var out bytes.Buffer
	if err := RunLogs(context.Background(), LogsOptions{Component: "runtime-claude-code", Out: &out}); err != nil {
		t.Fatalf("RunLogs runtime-claude-code: %v", err)
	}
	if !strings.Contains(out.String(), "rt-line") {
		t.Errorf("RunLogs runtime-claude-code output %q missing seed line", out.String())
	}
	if strings.Contains(out.String(), "codex-line") {
		t.Errorf("RunLogs runtime-claude-code leaked codex line: %q", out.String())
	}
	// The bare `runtime` alias expands to every runtime-<name>.log.
	var all bytes.Buffer
	if err := RunLogs(context.Background(), LogsOptions{Component: "runtime", Out: &all}); err != nil {
		t.Fatalf("RunLogs runtime: %v", err)
	}
	if !strings.Contains(all.String(), "rt-line") || !strings.Contains(all.String(), "codex-line") {
		t.Errorf("RunLogs runtime alias output %q missing one of the runtime lines", all.String())
	}
}

// TestRunLogsFollowStreamsAppendedLines covers the §24.19 line 263
// `--follow` mode: RunLogs blocks, polling for new lines, until the
// caller cancels.
//
// spec: §24.19 line 263.
func TestRunLogsFollowStreamsAppendedLines_spec_24_19_263(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LENNY_HOME", root)
	paths := NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	logPath := logFilePath(paths, "gateway")
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create gateway log: %v", err)
	}
	if _, err := f.WriteString("initial\n"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out safeBuffer
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := RunLogs(ctx, LogsOptions{Component: "gateway", Follow: true, FollowInterval: 10 * time.Millisecond, Out: &out}); err != nil {
			t.Errorf("RunLogs follow: %v", err)
		}
	}()

	// Allow the follower to absorb the initial line.
	waitFor(t, time.Second, func() bool { return strings.Contains(out.String(), "initial") })

	// Append more lines; the follower should pick them up within a few
	// poll intervals.
	if _, err := f.WriteString("appended-1\n"); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	if _, err := f.WriteString("appended-2\n"); err != nil {
		t.Fatalf("append 2: %v", err)
	}
	_ = f.Close()
	waitFor(t, time.Second, func() bool {
		s := out.String()
		return strings.Contains(s, "appended-1") && strings.Contains(s, "appended-2")
	})

	cancel()
	wg.Wait()
}

func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("waitFor: condition did not become true within %s", d)
}

// safeBuffer is a goroutine-safe bytes.Buffer for follow-mode tests.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
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

// TestWriteStatusRendersResourceColumns covers the §24.19 line 262
// rendering: CPU% and RSS columns next to component health, with "—"
// for un-sampled rows.
//
// spec: §24.19 line 262.
func TestWriteStatusRendersResourceColumns_spec_24_19_262(t *testing.T) {
	s := Status{
		Running:        true,
		StartedAt:      time.Unix(0, 0).UTC(),
		ActiveSessions: 3,
		Components: []ComponentStatus{
			{Name: "gateway", Healthy: true, Detail: "pid 1", Resource: ResourceUsage{Sampled: true, CPUPercent: 12.5, RSSBytes: 87432 * 1024}},
			{Name: "postgres", Healthy: true, Detail: "embedded"},
		},
	}
	var out bytes.Buffer
	WriteStatus(&out, s)
	got := out.String()
	if !strings.Contains(got, "CPU%") || !strings.Contains(got, "RSS") {
		t.Errorf("WriteStatus header missing CPU%%/RSS columns: %q", got)
	}
	if !strings.Contains(got, "12.5") {
		t.Errorf("WriteStatus output %q missing CPU sample", got)
	}
	if !strings.Contains(got, "MiB") {
		t.Errorf("WriteStatus output %q missing RSS suffix", got)
	}
	if !strings.Contains(got, "—") {
		t.Errorf("WriteStatus output %q missing em-dash for un-sampled row", got)
	}
	if !strings.Contains(got, "active sessions: 3") {
		t.Errorf("WriteStatus output %q missing active-session count", got)
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
