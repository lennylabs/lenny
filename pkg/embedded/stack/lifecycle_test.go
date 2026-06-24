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

// TestRunDownStopsRecordedStack covers the RunDown teardown of a recorded
// stack: it removes the recorded substrate container, clears the state
// file, and reports the stop. The §17.4 control plane runs as in-cluster
// pods, so the teardown is substrate-level rather than a host-process kill.
//
// spec: §24.19 (lenny down tears the running stack down).
func TestRunDownStopsRecordedStack_spec_24_19(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LENNY_HOME", root)
	paths := NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	st := State{K3sEnabled: true, GatewayForwarderAddr: "127.0.0.1:8443"}
	if err := writeState(paths.StateFile(), st); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	var out bytes.Buffer
	if err := RunDown(context.Background(), DownOptions{Out: &out}); err != nil {
		t.Fatalf("RunDown with a recorded stack: %v", err)
	}
	if _, err := os.Stat(paths.StateFile()); !os.IsNotExist(err) {
		t.Error("RunDown left the state file in place after stopping the stack")
	}
	if !strings.Contains(out.String(), "stopping the embedded stack") {
		t.Errorf("RunDown output = %q, want the stopping message", out.String())
	}
}

// TestRunDownRemovesDockerContainer covers the teardown on a Docker-backed
// substrate (macOS and Windows): the Docker-backed k3s runs inside the
// Docker VM, so RunDown must remove the container by its recorded handle
// before removeState discards the handle, or a teardown leaks the container
// with nothing to find it by. The substrate-container removal seam is
// injected so the test asserts the removal without invoking a real docker.
//
// diagnosis: a failure means lenny down orphans the embedded k3s container
// on macOS/Windows — the named no-leak invariant the substrate-lifecycle
// scope requires holds only on the Linux path.
//
// spec: §24.19 (lenny up/down manage the substrate; a teardown must not
// leak the Docker-backed k3s container), §17.4 (the embedded substrate is a
// Docker-backed container on macOS and Windows).
func TestRunDownRemovesDockerContainer_spec_24_19(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LENNY_HOME", root)
	paths := NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	var removed []string
	prev := removeSubstrateContainer
	t.Cleanup(func() { removeSubstrateContainer = prev })
	removeSubstrateContainer = func(name string) { removed = append(removed, name) }

	const handle = "lenny-embedded-k3s-demo"
	st := State{K3sContainer: handle, K3sEnabled: true}
	if err := writeState(paths.StateFile(), st); err != nil {
		t.Fatalf("writeState: %v", err)
	}

	var out bytes.Buffer
	if err := RunDown(context.Background(), DownOptions{Out: &out}); err != nil {
		t.Fatalf("RunDown: %v", err)
	}
	if len(removed) != 1 || removed[0] != handle {
		t.Fatalf("RunDown removed %v, want exactly the recorded container %q", removed, handle)
	}
	if _, err := os.Stat(paths.StateFile()); !os.IsNotExist(err) {
		t.Error("RunDown left the state file (and its container handle) in place")
	}
}

// TestRunDownPurgeRemovesDockerContainerBeforeDiscardingRoot covers the
// lenny down --purge gap on a Docker-backed substrate: purgeRoot only does
// os.RemoveAll(root), which never reaches the container inside the Docker
// VM. RunDown must remove the container by its recorded handle before
// purgeRoot discards the state directory that held the handle, or --purge
// orphans the container while throwing away its name.
//
// diagnosis: a failure means lenny down --purge on macOS/Windows leaves the
// embedded k3s container running while deleting the only record of its name.
//
// spec: §24.19 (lenny up/down manage the substrate; --purge must not leak
// the Docker-backed k3s container), §17.4.
func TestRunDownPurgeRemovesDockerContainerBeforeDiscardingRoot_spec_24_19(t *testing.T) {
	root := filepath.Join(t.TempDir(), "lenny-state")
	t.Setenv("LENNY_HOME", root)
	paths := NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	var removed []string
	prev := removeSubstrateContainer
	t.Cleanup(func() { removeSubstrateContainer = prev })
	removeSubstrateContainer = func(name string) {
		// The handle must be removed before purgeRoot discards the state
		// directory that records it.
		if _, err := os.Stat(paths.StateFile()); err != nil {
			t.Errorf("container removed after the state file was already gone: %v", err)
		}
		removed = append(removed, name)
	}

	const handle = "lenny-embedded-k3s-demo"
	st := State{K3sContainer: handle, K3sEnabled: true}
	if err := writeState(paths.StateFile(), st); err != nil {
		t.Fatalf("writeState: %v", err)
	}

	var out bytes.Buffer
	if err := RunDown(context.Background(), DownOptions{Purge: true, Out: &out}); err != nil {
		t.Fatalf("RunDown --purge: %v", err)
	}
	if len(removed) != 1 || removed[0] != handle {
		t.Fatalf("RunDown --purge removed %v, want exactly the recorded container %q", removed, handle)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Error("state directory still present after --purge")
	}
}

// TestRunDownLinuxSubstrateRemovesNoContainer confirms the removal is a
// no-op on the Linux child-process substrate, which records no container
// handle: RemoveContainer is called with an empty handle and removes
// nothing, so the Linux teardown is unchanged.
//
// diagnosis: a failure means the Docker-container teardown leaked into the
// Linux path, which has no container to remove.
//
// spec: §24.19, §17.4 (the Linux substrate is a managed child process, not a
// container).
func TestRunDownLinuxSubstrateRemovesNoContainer_spec_24_19(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LENNY_HOME", root)
	paths := NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	var removedHandles []string
	prev := removeSubstrateContainer
	t.Cleanup(func() { removeSubstrateContainer = prev })
	removeSubstrateContainer = func(name string) { removedHandles = append(removedHandles, name) }

	// A Linux stack: a recorded kubeconfig, no container handle.
	st := State{KubeconfigPath: "/state/k3s/kubeconfig.yaml", K3sEnabled: true}
	if err := writeState(paths.StateFile(), st); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	var out bytes.Buffer
	if err := RunDown(context.Background(), DownOptions{Out: &out}); err != nil {
		t.Fatalf("RunDown: %v", err)
	}
	// The seam is still invoked, but with an empty handle: the real
	// RemoveContainer is a no-op on an empty name, so nothing is removed.
	if len(removedHandles) != 1 || removedHandles[0] != "" {
		t.Errorf("RunDown on a Linux substrate passed handles %v, want a single empty handle", removedHandles)
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

func TestCollectStatusRecordedStack(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LENNY_HOME", root)
	paths := NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	// Record a stack whose forwarder address points at an unbound loopback
	// port so the gateway probe reports it down without a real process, and
	// no substrate handle so k3s reports down.
	st := State{
		GatewayForwarderAddr: "127.0.0.1:1",
		K3sEnabled:           false,
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
	if c, ok := byName["gateway"]; !ok || c.Healthy {
		t.Errorf("gateway component = %+v, want unhealthy (unbound forwarder port)", c)
	}
	if c, ok := byName["k3s"]; !ok || c.Healthy {
		t.Errorf("k3s component = %+v, want down when no substrate handle is recorded", c)
	}
}
