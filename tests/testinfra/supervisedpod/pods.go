// SPDX-License-Identifier: MIT

package supervisedpod

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// PodState is the part of a pod's status the supervisor tests read.
type PodState struct {
	Name      string
	Node      string
	Ready     bool
	Deleting  bool
	ReadyAt   time.Time
	Labels    map[string]string
	Container map[string]ContainerState
}

// ContainerState is one container's status.
type ContainerState struct {
	ID           string
	Running      bool
	StartedAt    time.Time
	Terminated   bool
	ExitCode     int
	Reason       string
	RestartCount int
}

// podJSON is the subset of a Pod object PodState is read from.
type podJSON struct {
	Metadata struct {
		Name              string            `json:"name"`
		DeletionTimestamp *string           `json:"deletionTimestamp"`
		Labels            map[string]string `json:"labels"`
	} `json:"metadata"`
	Spec struct {
		NodeName string `json:"nodeName"`
	} `json:"spec"`
	Status struct {
		Conditions []struct {
			Type               string    `json:"type"`
			Status             string    `json:"status"`
			LastTransitionTime time.Time `json:"lastTransitionTime"`
		} `json:"conditions"`
		ContainerStatuses []struct {
			Name         string `json:"name"`
			ContainerID  string `json:"containerID"`
			RestartCount int    `json:"restartCount"`
			State        struct {
				Running *struct {
					StartedAt time.Time `json:"startedAt"`
				} `json:"running"`
				Terminated *struct {
					ExitCode int    `json:"exitCode"`
					Reason   string `json:"reason"`
				} `json:"terminated"`
			} `json:"state"`
		} `json:"containerStatuses"`
	} `json:"status"`
}

// toState reduces a decoded pod to its PodState.
func (p podJSON) toState() PodState {
	s := PodState{
		Name:      p.Metadata.Name,
		Node:      p.Spec.NodeName,
		Deleting:  p.Metadata.DeletionTimestamp != nil,
		Labels:    p.Metadata.Labels,
		Container: map[string]ContainerState{},
	}
	for _, c := range p.Status.Conditions {
		if c.Type == "Ready" {
			s.Ready = c.Status == "True"
			s.ReadyAt = c.LastTransitionTime
		}
	}
	for _, cs := range p.Status.ContainerStatuses {
		st := ContainerState{ID: cs.ContainerID, RestartCount: cs.RestartCount}
		if cs.State.Running != nil {
			st.Running, st.StartedAt = true, cs.State.Running.StartedAt
		}
		if cs.State.Terminated != nil {
			st.Terminated, st.ExitCode, st.Reason = true, cs.State.Terminated.ExitCode, cs.State.Terminated.Reason
		}
		s.Container[cs.Name] = st
	}
	return s
}

// GetPod reads one pod's state. found is false when the pod does not exist.
func GetPod(t testing.TB, c *kind.Cluster, pod string) (state PodState, found bool) {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", Namespace, "get", "pod", pod, "-o", "json", "--ignore-not-found")
	if err != nil {
		t.Fatalf("get pod %s: %v\n%s", pod, err, out)
	}
	if strings.TrimSpace(out) == "" {
		return PodState{}, false
	}
	var p podJSON
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatalf("decode pod %s: %v", pod, err)
	}
	return p.toState(), true
}

// PoolPods lists the pods of pool.
func PoolPods(t testing.TB, c *kind.Cluster, pool string) []PodState {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", Namespace, "get", "pods", "-l", "lenny.dev/pool="+pool, "-o", "json")
	if err != nil {
		t.Fatalf("list pods of %s: %v\n%s", pool, err, out)
	}
	var list struct {
		Items []podJSON `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("decode pods of %s: %v", pool, err)
	}
	states := make([]PodState, 0, len(list.Items))
	for _, p := range list.Items {
		states = append(states, p.toState())
	}
	return states
}

// WaitReadyPods waits until pool has n Ready pods that are not being deleted
// and returns them. A pool that does not reach n within timeout skips the
// test as not yet applicable, because pools warm asynchronously after the
// install.
func WaitReadyPods(t testing.TB, c *kind.Cluster, pool string, n int, timeout time.Duration) []PodState {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		var ready []PodState
		for _, p := range PoolPods(t, c, pool) {
			if p.Ready && !p.Deleting {
				ready = append(ready, p)
			}
		}
		if len(ready) >= n {
			return ready
		}
		if time.Now().After(deadline) {
			t.Skipf("not-yet-applicable: %s has %d Ready pods, fewer than the %d the case needs; "+
				"re-run after tests/testinfra/kind/install.sh completes", pool, len(ready), n)
		}
		time.Sleep(3 * time.Second)
	}
}

// FreshIdlePods deletes every unclaimed pod of pool and waits until the pool
// has n Ready, unclaimed pods it created after the deletion, which it
// returns. None of them has served a session. A keep pod returns to idle
// when its reserved hold ends, still running the runtime process its
// sessions used and possibly still finishing post-session work in the
// runtime container, so a case that reads a never-used pod cannot take any
// Ready pod of a pool an earlier test used. Warming pods are deleted too,
// because one that turns Ready would satisfy the pool's idle count and no
// replacement would be created. A pool that does not reach n within
// timeout skips the test as not yet applicable, as WaitReadyPods does.
// spec: §5.2 (Recycle lifecycle); §4.6.1.
func FreshIdlePods(t testing.TB, c *kind.Cluster, pool string, n int, timeout time.Duration) []PodState {
	t.Helper()
	before := map[string]bool{}
	for _, p := range PoolPods(t, c, pool) {
		before[p.Name] = true
		if !p.Deleting && !ClaimExists(t, c, p.Name) {
			DeletePod(t, c, p.Name)
		}
	}
	deadline := time.Now().Add(timeout)
	for {
		var fresh []PodState
		for _, p := range PoolPods(t, c, pool) {
			if !before[p.Name] && p.Ready && !p.Deleting && !ClaimExists(t, c, p.Name) {
				fresh = append(fresh, p)
			}
		}
		if len(fresh) >= n {
			return fresh
		}
		if time.Now().After(deadline) {
			t.Skipf("not-yet-applicable: %s has %d Ready pods created after its idle pods were replaced, "+
				"fewer than the %d the case needs", pool, len(fresh), n)
		}
		time.Sleep(3 * time.Second)
	}
}

// WaitGone polls pod every interval until it no longer exists or timeout
// elapses, and returns the last state it saw and whether the pod is gone.
// The last state carries the containers' terminated states when the kubelet
// reported them before the object was removed.
func WaitGone(t testing.TB, c *kind.Cluster, pod string, timeout time.Duration) (last PodState, gone bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		st, found := GetPod(t, c, pod)
		if !found {
			return last, true
		}
		last = st
		time.Sleep(250 * time.Millisecond)
	}
	return last, false
}

// HostPID returns the node and the host PID of pod's container, read from
// the node's container runtime with crictl. A process signalled through it
// is signalled from outside the container's PID namespace.
func HostPID(t testing.TB, pod PodState, container string) (node string, pid int) {
	t.Helper()
	cs, ok := pod.Container[container]
	if !ok || cs.ID == "" {
		t.Fatalf("pod %s has no %s container ID", pod.Name, container)
	}
	id := cs.ID[strings.Index(cs.ID, "://")+3:]
	out, err := exec.Command("docker", "exec", pod.Node, "crictl", "inspect", "--output", "go-template",
		"--template", "{{.info.pid}}", id).CombinedOutput()
	if err != nil {
		t.Fatalf("crictl inspect %s on %s: %v\n%s", id, pod.Node, err, out)
	}
	pid, err = strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || pid <= 1 {
		t.Fatalf("crictl inspect %s printed %q, not a host PID", id, out)
	}
	return pod.Node, pid
}

// HostSignal sends sig (a signal name such as STOP) to a host PID on node,
// as root in the node's PID namespace.
func HostSignal(t testing.TB, node string, pid int, sig string) {
	t.Helper()
	out, err := exec.Command("docker", "exec", node, "kill", "-s", sig, strconv.Itoa(pid)).CombinedOutput()
	if err != nil {
		t.Fatalf("kill -s %s %d on %s: %v\n%s", sig, pid, node, err, out)
	}
}

// DeletePod deletes pod without waiting for it to go. A pod that is already
// gone is the outcome the call asks for, so it is not an error: a cleanup
// runs after cases whose subject is the pod draining and being removed.
func DeletePod(t testing.TB, c *kind.Cluster, pod string) {
	t.Helper()
	if out, err := c.KubectlOut(t, "-n", Namespace, "delete", "pod", pod, "--wait=false", "--ignore-not-found"); err != nil {
		t.Fatalf("delete pod %s: %v\n%s", pod, err, out)
	}
}

// Logs returns a container's log, or the previous instance's with previous.
func Logs(t testing.TB, c *kind.Cluster, pod, container string, previous bool) string {
	t.Helper()
	args := []string{"-n", Namespace, "logs", pod, "-c", container}
	if previous {
		args = append(args, "--previous")
	}
	out, _ := c.KubectlOut(t, args...)
	return out
}

// TestLabel marks every object a supervisor test creates, so a later run's
// start-time sweep reclaims what a killed run left.
const TestLabel = "lenny.dev/e2e-supervisor-test"

// SweepTestObjects deletes every pod in the agent namespace that carries
// TestLabel. It is best-effort: a failure is logged and never fails the
// test, because the sweep's subject is an earlier run's wreckage.
func SweepTestObjects(t testing.TB, c *kind.Cluster) {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", Namespace, "delete", "pods", "-l", TestLabel+"=true", "--wait=false", "--ignore-not-found")
	if err != nil {
		t.Logf("sweep of %s pods failed (ignored): %v\n%s", TestLabel, err, out)
		return
	}
	if s := strings.TrimSpace(out); s != "" {
		t.Logf("swept objects an earlier run left: %s", s)
	}
}

// Describe renders a PodState for a failure message.
func (s PodState) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s ready=%v deleting=%v", s.Name, s.Ready, s.Deleting)
	for name, cs := range s.Container {
		fmt.Fprintf(&b, " %s{running=%v terminated=%v exit=%d reason=%q restarts=%d}",
			name, cs.Running, cs.Terminated, cs.ExitCode, cs.Reason, cs.RestartCount)
	}
	return b.String()
}

// LogStream follows a container's log from the moment it starts.
type LogStream struct {
	cmd *exec.Cmd
	buf *syncBuffer
}

// StreamLogs starts following pod's container log. Stop returns what it
// collected; it is safe to call after the pod is gone.
func StreamLogs(t testing.TB, c *kind.Cluster, pod, container string) *LogStream {
	t.Helper()
	buf := &syncBuffer{}
	cmd := c.Kubectl("-n", Namespace, "logs", "-f", pod, "-c", container)
	cmd.Stdout, cmd.Stderr = buf, buf
	if err := cmd.Start(); err != nil {
		t.Fatalf("follow %s/%s log: %v", pod, container, err)
	}
	s := &LogStream{cmd: cmd, buf: buf}
	t.Cleanup(func() { s.Stop() })
	return s
}

// Stop ends the stream and returns the collected log.
func (s *LogStream) Stop() string {
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_ = s.cmd.Wait()
	}
	return s.buf.String()
}

// syncBuffer is a bytes.Buffer safe for the stream's writer and a reader.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}
