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

// TestRunUpDrivesBringUpAndPropagatesFailure covers the foreground lenny up
// wrapper: it threads its options into Up and propagates a bring-up failure.
// An unsupported substrate makes Up report the substrate failure (S1), which
// RunUp surfaces to the caller and writes to ErrOut.
//
// spec: §17.4 (lenny up brings the in-cluster control plane up in-process; an
// unavailable substrate makes lenny up report the failure).
func TestRunUpPropagatesSubstrateFailure_spec_17_4(t *testing.T) {
	root := t.TempDir()
	withSubstrateSeams(t, false, &fakeLauncher{}, nil)
	withBringUpSeams(t)
	var out, errOut bytes.Buffer
	err := RunUp(context.Background(), UpOptions{Root: root, HTTPSPort: freeLoopbackPort(t), Out: &out, ErrOut: &errOut})
	if err == nil {
		t.Fatal("RunUp with no substrate = nil, want the substrate-failure error")
	}
	if !strings.Contains(errOut.String(), "lenny up:") {
		t.Errorf("RunUp ErrOut = %q, want a lenny up error line", errOut.String())
	}
}

// TestRunUpSucceedsAndRecordsCLIVersion covers the foreground lenny up success
// path: it drives the full bring-up through the seams and records the CLI
// version it was given as the deployed image tag, so a later warm up
// reconciles against it (C4).
//
// spec: §17.4 (lenny up records the deployed image tag for the warm reconcile).
func TestRunUpSucceedsAndRecordsCLIVersion_spec_17_4(t *testing.T) {
	root := t.TempDir()
	l := &fakeLauncher{gatewayHost: "127.0.0.1", kubeconfig: filepath.Join(t.TempDir(), "kubeconfig")}
	withSubstrateSeams(t, true, l, nil)
	withActivationSeams(t, "ghcr.io/lennylabs/runtime-echo-embedded@sha256:"+
		"4444444444444444444444444444444444444444444444444444444444444444")
	withRuntimeClassSeam(t)
	withBringUpSeams(t)

	var out, errOut bytes.Buffer
	if err := RunUp(context.Background(), UpOptions{
		Root: root, HTTPSPort: freeLoopbackPort(t), CLIVersion: "v3.1.4", Out: &out, ErrOut: &errOut,
	}); err != nil {
		t.Fatalf("RunUp: %v", err)
	}
	st, ok, err := readState(NewPaths(root).StateFile())
	if err != nil || !ok {
		t.Fatalf("readState after RunUp: ok=%v err=%v", ok, err)
	}
	if st.DeployedImageTag != "v3.1.4" {
		t.Errorf("recorded DeployedImageTag = %q, want v3.1.4", st.DeployedImageTag)
	}
}

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
// stack: it stops the substrate, reports the stop, and rewrites the state
// file as a Stopped marker that preserves the deployed image tag rather than
// deleting it, so a later lenny status reads no running stack while the warm
// reconcile still sees the persisted tag. The §17.4 control plane runs as
// in-cluster pods, so the teardown is substrate-level rather than a
// host-process kill.
//
// spec: §17.4 (a non-`--purge` down preserves the deployed tag for the warm
// reconcile while reporting no running stack), §24.19 (lenny down tears the
// running stack down).
func TestRunDownStopsRecordedStack_spec_24_19(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LENNY_HOME", root)
	paths := NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	st := State{K3sEnabled: true, GatewayForwarderAddr: "127.0.0.1:8443", DeployedImageTag: "v7.0.0"}
	if err := writeState(paths.StateFile(), st); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	var out bytes.Buffer
	if err := RunDown(context.Background(), DownOptions{Out: &out}); err != nil {
		t.Fatalf("RunDown with a recorded stack: %v", err)
	}
	// The state file persists as a Stopped marker that preserves the deployed
	// tag for the warm reconcile, while readRunningState reads it as no
	// running stack.
	marker, ok, err := readState(paths.StateFile())
	if err != nil || !ok {
		t.Fatalf("RunDown removed the state file; want a preserved Stopped marker (ok=%v err=%v)", ok, err)
	}
	if !marker.Stopped {
		t.Error("RunDown did not mark the state file as Stopped")
	}
	if marker.DeployedImageTag != "v7.0.0" {
		t.Errorf("Stopped marker DeployedImageTag = %q, want the preserved v7.0.0", marker.DeployedImageTag)
	}
	if _, running, err := readRunningState(paths.StateFile()); err != nil || running {
		t.Errorf("readRunningState after down = running=%v err=%v, want not running", running, err)
	}
	if !strings.Contains(out.String(), "stopping the embedded stack") {
		t.Errorf("RunDown output = %q, want the stopping message", out.String())
	}
}

// TestRunDownStopsDockerContainer covers the default (non-purge) teardown on
// a Docker-backed substrate (macOS and Windows): lenny down stops the
// container while persisting it and its containerd image store so a warm
// lenny up restarts it. RunDown must stop the container by its recorded
// handle (not force-remove it) and must not discard the image store. The
// substrate-container stop/remove seams are injected so the test asserts the
// persist-stop without invoking a real docker.
//
// diagnosis: a failure means lenny down either fails to stop the embedded
// k3s container on macOS/Windows or force-removes it, discarding the
// containerd image store the §17.4 substrate-persistence model preserves.
//
// spec: §17.4 (lenny down persists the substrate and the imported-image
// store; --purge removes them), §24.19 (lenny up/down manage the substrate).
func TestRunDownStopsDockerContainer_spec_17_4(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LENNY_HOME", root)
	paths := NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	var stopped, removed []string
	prevStop, prevRemove := stopSubstrateContainer, removeSubstrateContainer
	t.Cleanup(func() { stopSubstrateContainer, removeSubstrateContainer = prevStop, prevRemove })
	stopSubstrateContainer = func(name string) { stopped = append(stopped, name) }
	removeSubstrateContainer = func(name string) { removed = append(removed, name) }

	const handle = "lenny-embedded-k3s-demo"
	st := State{K3sContainer: handle, K3sEnabled: true, DeployedImageTag: "v7.0.0"}
	if err := writeState(paths.StateFile(), st); err != nil {
		t.Fatalf("writeState: %v", err)
	}

	var out bytes.Buffer
	if err := RunDown(context.Background(), DownOptions{Out: &out}); err != nil {
		t.Fatalf("RunDown: %v", err)
	}
	if len(stopped) != 1 || stopped[0] != handle {
		t.Fatalf("RunDown stopped %v, want exactly the recorded container %q", stopped, handle)
	}
	// A non-purge down must persist (not force-remove) the container.
	if len(removed) != 0 {
		t.Errorf("RunDown force-removed the container on a non-purge down: %v", removed)
	}
	// The state file persists as a Stopped marker that keeps the container
	// handle and the deployed tag so a warm lenny up can stop/restart the same
	// container and skip the re-import when the CLI version is unchanged.
	marker, ok, err := readState(paths.StateFile())
	if err != nil || !ok {
		t.Fatalf("RunDown removed the state file; want a preserved Stopped marker (ok=%v err=%v)", ok, err)
	}
	if !marker.Stopped || marker.K3sContainer != handle || marker.DeployedImageTag != "v7.0.0" {
		t.Errorf("Stopped marker = %+v, want Stopped with the container handle and deployed tag preserved", marker)
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

// TestRunDownPurgeStopsLinuxProcessBeforeDiscardingRoot covers the
// lenny down --purge teardown on the Linux child-process substrate: RunDown
// must terminate the recorded k3s process group by PID before purgeRoot
// discards the data directory, so --purge does not leave k3s running while
// removing its data directory out from under it.
//
// diagnosis: a failure means lenny down --purge leaves the Linux k3s process
// group running while deleting its data directory, corrupting the substrate.
//
// spec: §17.4 (--purge removes the persisted substrate; the data-directory
// removal is purgeRoot's, after the process is stopped).
func TestRunDownPurgeStopsLinuxProcessBeforeDiscardingRoot_spec_17_4(t *testing.T) {
	root := filepath.Join(t.TempDir(), "lenny-state")
	t.Setenv("LENNY_HOME", root)
	paths := NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	var stoppedPIDs []int
	prev := stopSubstrateProcess
	t.Cleanup(func() { stopSubstrateProcess = prev })
	stopSubstrateProcess = func(pid int) {
		// The process must be stopped before purgeRoot discards the data dir.
		if _, err := os.Stat(paths.StateFile()); err != nil {
			t.Errorf("process stopped after the state directory was already gone: %v", err)
		}
		stoppedPIDs = append(stoppedPIDs, pid)
	}

	const k3sPID = 5151
	st := State{KubeconfigPath: "/state/k3s/kubeconfig.yaml", K3sPID: k3sPID, K3sEnabled: true}
	if err := writeState(paths.StateFile(), st); err != nil {
		t.Fatalf("writeState: %v", err)
	}

	var out bytes.Buffer
	if err := RunDown(context.Background(), DownOptions{Purge: true, Out: &out}); err != nil {
		t.Fatalf("RunDown --purge: %v", err)
	}
	if len(stoppedPIDs) != 1 || stoppedPIDs[0] != k3sPID {
		t.Fatalf("RunDown --purge stopped PIDs %v, want exactly the recorded k3s PID %d", stoppedPIDs, k3sPID)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Error("state directory still present after --purge")
	}
}

// TestRunDownLinuxSubstrateStopsProcessByPID confirms the default down on the
// Linux child-process substrate stops the recorded k3s process group by PID
// (so the in-cluster control plane stops) while persisting the data
// directory, and that the container-stop seam is a no-op on the empty
// container handle the Linux launcher records.
//
// diagnosis: a failure means lenny down either fails to stop the Linux k3s
// process group (leaking the substrate) or leaks the Docker-container
// teardown into the Linux path, which has no container.
//
// spec: §17.4 (the Linux substrate outlives the CLI; lenny down stops it and
// persists its data directory unless --purge removes it), §24.19.
func TestRunDownLinuxSubstrateStopsProcessByPID_spec_17_4(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LENNY_HOME", root)
	paths := NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	var stoppedContainers []string
	var stoppedPIDs []int
	prevStop, prevProc := stopSubstrateContainer, stopSubstrateProcess
	t.Cleanup(func() { stopSubstrateContainer, stopSubstrateProcess = prevStop, prevProc })
	stopSubstrateContainer = func(name string) { stoppedContainers = append(stoppedContainers, name) }
	stopSubstrateProcess = func(pid int) { stoppedPIDs = append(stoppedPIDs, pid) }

	// A Linux stack: a recorded kubeconfig and k3s PID, no container handle.
	const k3sPID = 4242
	st := State{KubeconfigPath: "/state/k3s/kubeconfig.yaml", K3sPID: k3sPID, K3sEnabled: true}
	if err := writeState(paths.StateFile(), st); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	var out bytes.Buffer
	if err := RunDown(context.Background(), DownOptions{Out: &out}); err != nil {
		t.Fatalf("RunDown: %v", err)
	}
	if len(stoppedPIDs) != 1 || stoppedPIDs[0] != k3sPID {
		t.Errorf("RunDown stopped PIDs %v, want exactly the recorded k3s PID %d", stoppedPIDs, k3sPID)
	}
	// The container-stop seam is still invoked, but with an empty handle: the
	// real StopContainer is a no-op on an empty name.
	if len(stoppedContainers) != 1 || stoppedContainers[0] != "" {
		t.Errorf("RunDown on a Linux substrate passed container handles %v, want a single empty handle", stoppedContainers)
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
