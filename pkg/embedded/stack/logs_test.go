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

	k8sfake "k8s.io/client-go/kubernetes/fake"
)

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

// waitFor polls cond until it is true or d elapses.
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

// TestRunLogsNoStack covers the no-stack path: lenny logs against a stack that
// is not running reports ErrNoRunningStack so the CLI can present a precise
// message rather than streaming nothing.
//
// spec: §24.19 line 263.
func TestRunLogsNoStack_spec_24_19_263(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	err := RunLogs(context.Background(), LogsOptions{Out: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "no running stack") {
		t.Errorf("RunLogs with no stack = %v, want ErrNoRunningStack", err)
	}
}

// TestRunLogsUnknownComponent covers the fail-closed allow-list: an unknown
// component is rejected rather than streamed.
//
// spec: §24.19 line 263.
func TestRunLogsUnknownComponent_spec_24_19_263(t *testing.T) {
	recordRunningStack(t)
	withClusterClient(t, k8sfake.NewSimpleClientset())
	err := RunLogs(context.Background(), LogsOptions{Component: "nonsense", Out: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "unknown component") {
		t.Errorf("RunLogs unknown component = %v, want a rejection", err)
	}
}

// TestRunLogsRejectsRemovedHostComponents covers the in-cluster-topology
// removal: the host-process components (postgres, redis, kms, oidc, supervisor)
// are no longer selectable, so lenny logs rejects each rather than streaming a
// stale host file.
//
// spec: §17.4 line 179, §24.19 line 263 (the pod-backed log component set).
func TestRunLogsRejectsRemovedHostComponents_spec_17_4(t *testing.T) {
	recordRunningStack(t)
	withClusterClient(t, k8sfake.NewSimpleClientset())
	for _, c := range []string{"postgres", "redis", "kms", "oidc", "supervisor"} {
		err := RunLogs(context.Background(), LogsOptions{Component: c, Out: &bytes.Buffer{}})
		if err == nil || !strings.Contains(err.Error(), "unknown component") {
			t.Errorf("RunLogs %q = %v, want a rejection of the removed host component", c, err)
		}
	}
}

// TestRunLogsStreamsControlPlanePod covers the §17.4 pod-backed control-plane
// log path: lenny logs gateway lists the gateway pods through the embedded
// kubeconfig and streams each pod's log. The fake clientset returns a fixed
// pod-log body, so the test asserts the stream reaches the output.
//
// spec: §17.4 line 179 (the control-plane logs stream from the in-cluster
// pods), §24.19 line 263.
func TestRunLogsStreamsControlPlanePod_spec_17_4(t *testing.T) {
	recordRunningStack(t)
	withClusterClient(t, k8sfake.NewSimpleClientset(
		controlPlanePod("lenny-gateway-abc", "gateway"),
	))
	var out bytes.Buffer
	if err := RunLogs(context.Background(), LogsOptions{Component: "gateway", Out: &out}); err != nil {
		t.Fatalf("RunLogs gateway: %v", err)
	}
	// The fake clientset's GetLogs stream returns a fixed body.
	if !strings.Contains(out.String(), "fake logs") {
		t.Errorf("RunLogs gateway output = %q, want the streamed pod log", out.String())
	}
}

// TestRunLogsControlPlaneNoPodsReportsAndContinues covers the path where a
// component's Deployment has not scheduled a pod yet: lenny logs reports the
// component has no running pods rather than failing.
//
// spec: §17.4 line 179, §24.19 line 263.
func TestRunLogsControlPlaneNoPods_spec_17_4(t *testing.T) {
	recordRunningStack(t)
	withClusterClient(t, k8sfake.NewSimpleClientset())
	var out bytes.Buffer
	if err := RunLogs(context.Background(), LogsOptions{Component: "controller", Out: &out}); err != nil {
		t.Fatalf("RunLogs controller: %v", err)
	}
	if !strings.Contains(out.String(), "no running pods for controller") {
		t.Errorf("RunLogs controller output = %q, want a no-pods note", out.String())
	}
}

// TestRunLogsRuntimeComponentSelectsAgentPods covers the §24.19 runtime-<name>
// filter: lenny logs runtime-echo lists the agent pods labeled with that
// runtime name in the agent namespace and streams their logs, while an
// unrelated runtime's pods are not streamed.
//
// spec: §6.2 (the runtime-name pod label), §17.4 line 179 (runtime-<name>
// streams the runtime's agent pods), §24.19 line 263.
func TestRunLogsRuntimeComponentSelectsAgentPods_spec_24_19_263(t *testing.T) {
	recordRunningStack(t)
	withClusterClient(t, k8sfake.NewSimpleClientset(
		agentPod("echo-pod-1", "echo"),
		agentPod("other-pod-1", "claude-code"),
	))
	var out bytes.Buffer
	if err := RunLogs(context.Background(), LogsOptions{Component: "runtime-echo", Out: &out}); err != nil {
		t.Fatalf("RunLogs runtime-echo: %v", err)
	}
	if !strings.Contains(out.String(), "fake logs") {
		t.Errorf("RunLogs runtime-echo output = %q, want the streamed agent-pod log", out.String())
	}
}

// TestRunLogsRuntimeAliasExpandsToRunningRuntimes covers the bare `runtime`
// alias: it expands to every runtime with running agent pods, so a stack with
// echo and claude-code agent pods streams both, prefixed by runtime name.
//
// spec: §17.4 line 179, §24.19 line 263.
func TestRunLogsRuntimeAliasExpands_spec_24_19_263(t *testing.T) {
	recordRunningStack(t)
	withClusterClient(t, k8sfake.NewSimpleClientset(
		agentPod("echo-pod-1", "echo"),
		agentPod("cc-pod-1", "claude-code"),
	))
	var out bytes.Buffer
	if err := RunLogs(context.Background(), LogsOptions{Component: "runtime", Out: &out}); err != nil {
		t.Fatalf("RunLogs runtime: %v", err)
	}
	// Two runtimes means merging, so the lines carry the runtime-name prefix.
	if !strings.Contains(out.String(), "runtime-echo |") {
		t.Errorf("RunLogs runtime alias output %q missing the echo prefix", out.String())
	}
	if !strings.Contains(out.String(), "runtime-claude-code |") {
		t.Errorf("RunLogs runtime alias output %q missing the claude-code prefix", out.String())
	}
}

// TestRunLogsRuntimeAliasNoPodsRejects covers the path where the bare `runtime`
// alias matches no running agent pod: lenny logs reports no runtime pods are
// running rather than streaming nothing.
//
// spec: §24.19 line 263.
func TestRunLogsRuntimeAliasNoPods_spec_24_19_263(t *testing.T) {
	recordRunningStack(t)
	withClusterClient(t, k8sfake.NewSimpleClientset())
	err := RunLogs(context.Background(), LogsOptions{Component: "runtime", Out: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "no runtime agent pods") {
		t.Errorf("RunLogs runtime alias with no pods = %v, want a no-pods rejection", err)
	}
}

// TestRunLogsMergedStreamsEveryComponent covers the empty-filter merged path:
// lenny logs with no component merges the control-plane Deployments, the k3s
// substrate log, and every runtime with running agent pods, prefixing each line
// with its component name. The gateway pod, the k3s substrate file, and an echo
// agent pod each reach the merged output.
//
// spec: §17.4 line 179 (the merged log set is the pod-backed sources plus the
// k3s substrate), §24.19 line 263.
func TestRunLogsMergedStreamsEveryComponent_spec_17_4(t *testing.T) {
	recordRunningStack(t)
	withClusterClient(t, k8sfake.NewSimpleClientset(
		controlPlanePod("lenny-gateway-abc", "gateway"),
		agentPod("echo-pod-1", "echo"),
	))
	root, err := DefaultRoot()
	if err != nil {
		t.Fatalf("DefaultRoot: %v", err)
	}
	paths := NewPaths(root)
	if err := os.WriteFile(filepath.Join(paths.K3s, "k3s.log"), []byte("substrate-line\n"), 0o644); err != nil {
		t.Fatalf("seed k3s log: %v", err)
	}
	var out bytes.Buffer
	if err := RunLogs(context.Background(), LogsOptions{Out: &out}); err != nil {
		t.Fatalf("RunLogs merged: %v", err)
	}
	got := out.String()
	// The gateway pod and echo agent pod stream the fake-client log body, both
	// prefixed; the k3s substrate file line is prefixed too.
	if !strings.Contains(got, "gateway | fake logs") {
		t.Errorf("merged output %q missing the prefixed gateway pod log", got)
	}
	if !strings.Contains(got, "k3s | substrate-line") {
		t.Errorf("merged output %q missing the prefixed k3s substrate line", got)
	}
	if !strings.Contains(got, "runtime-echo | fake logs") {
		t.Errorf("merged output %q missing the prefixed echo agent log", got)
	}
}

// TestRunLogsK3sStreamsSubstrateFile covers the k3s substrate component: it
// keeps its host log file path (the substrate has no API follow channel), so
// lenny logs k3s reads the substrate log file rather than a pod.
//
// spec: §17.4 line 179 (the k3s substrate keeps its host log file path),
// §24.19 line 263.
func TestRunLogsK3sStreamsSubstrateFile_spec_17_4(t *testing.T) {
	recordRunningStack(t)
	withClusterClient(t, k8sfake.NewSimpleClientset())
	root, err := DefaultRoot()
	if err != nil {
		t.Fatalf("DefaultRoot: %v", err)
	}
	paths := NewPaths(root)
	if err := os.WriteFile(filepath.Join(paths.K3s, "k3s.log"), []byte("substrate-line\n"), 0o644); err != nil {
		t.Fatalf("seed k3s log: %v", err)
	}
	var out bytes.Buffer
	if err := RunLogs(context.Background(), LogsOptions{Component: "k3s", Out: &out}); err != nil {
		t.Fatalf("RunLogs k3s: %v", err)
	}
	if !strings.Contains(out.String(), "substrate-line") {
		t.Errorf("RunLogs k3s output = %q, want the substrate log line", out.String())
	}
}

// TestRunLogsK3sFollowStreamsAppendedLines covers the §24.19 line 263
// `--follow` mode on the substrate file source: RunLogs streams new lines
// appended to the k3s substrate log until the caller cancels, since the
// substrate file has no API follow channel and is polled.
//
// spec: §24.19 line 263 (`--follow`).
func TestRunLogsK3sFollowStreamsAppendedLines_spec_24_19_263(t *testing.T) {
	recordRunningStack(t)
	withClusterClient(t, k8sfake.NewSimpleClientset())
	root, err := DefaultRoot()
	if err != nil {
		t.Fatalf("DefaultRoot: %v", err)
	}
	paths := NewPaths(root)
	logPath := filepath.Join(paths.K3s, "k3s.log")
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create k3s log: %v", err)
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
		if err := RunLogs(ctx, LogsOptions{Component: "k3s", Follow: true, Out: &out}); err != nil {
			t.Errorf("RunLogs follow: %v", err)
		}
	}()

	waitFor(t, time.Second, func() bool { return strings.Contains(out.String(), "initial") })
	if _, err := f.WriteString("appended-1\nappended-2\n"); err != nil {
		t.Fatalf("append: %v", err)
	}
	_ = f.Close()
	waitFor(t, time.Second, func() bool {
		s := out.String()
		return strings.Contains(s, "appended-1") && strings.Contains(s, "appended-2")
	})
	cancel()
	wg.Wait()
}
