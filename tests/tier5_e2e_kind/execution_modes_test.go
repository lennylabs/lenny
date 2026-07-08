// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the §5.2 session-mode sessionPolicy presets
// against real agent pods under a real RuntimeClass: sequential pod
// reuse ("task mode" — sessionPolicy.recycle.enabled) and concurrent
// slot multiplexing ("concurrent workspace mode" —
// sessionPolicy.maxConcurrentSessions > 1). §5.2 collapses the former
// task and concurrent modes into presets of the sessionPolicy block
// (runtimestore.SessionPolicy's doc comment: "recycle.enabled is
// sequential pod reuse, and maxConcurrentSessions > 1 is the
// concurrent-slot configuration").
//
// tests/tier4_integration/concurrent_workspace_test.go exercises slotId
// multiplexing in-process: a bufconn adapter driving a spawned
// echo-concurrent binary, with no real pod, no RuntimeClass, and no
// per-slot filesystem isolation enforced by a real container boundary.
// This file drives the same sequential-reuse and concurrent-slot
// behaviors through the live gateway against pods
// tests/testinfra/kind/install.sh warms in the lenny-agents namespace
// (task-mode-echo-pool and concurrent-echo-pool), closing the §14.9
// coverage gap for concurrent per-slot directories on a real sandbox.
// TestTaskModeRecycleScrubsWorkspaceBetweenSessions is written against
// the same real-pod path for the task-mode workspace-scrub half of the
// gap but currently skips: see its doc comment for the blocking
// precondition.
//
// Both echo-runtime-task-mode and echo-runtime-concurrent are
// distroless (no shell), so workspace content is inspected through a
// short-lived busybox ephemeral debug container attached to the pod's
// "runtime" container with the shared "workspace" emptyDir mounted,
// mirroring the pattern gvisor_isolation_test.go uses for its
// /proc/version kernel-fingerprint probe. The adapter (not the debug
// container's unprivileged UID) owns /workspace/current and
// /workspace/slots/{slotId}/current, so content is placed via a client
// §14 WorkspacePlan at session start rather than by writing through the
// debug container, which the directory's group-readable, non-group-
// writable permissions would reject anyway.

package tier5_e2e_kind_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// taskModePoolName and taskModeRuntimeRef name the §5.2 sequential
// pod-reuse pool tests/testinfra/kind/install.sh's generated bootstrap
// overlay seeds: sessionPolicy.recycle.enabled with the default
// (standard) scrub profile, maxSessionsPerPod: 5, warmCount: 2. A
// distinct runtimeRef (rather than reusing echo-runtime-sidecar) keeps
// this pool the only match for its (runtimeRef, isolationProfile) pair —
// podsession.ResolvePool rejects a pair two pools both satisfy with
// ErrAmbiguousPool.
const (
	taskModePoolName   = "task-mode-echo-pool"
	taskModeRuntimeRef = "echo-runtime-task-mode"
)

// concurrentPoolName and concurrentRuntimeRef name the §5.2 concurrent-
// session pool: sessionPolicy.maxConcurrentSessions: 2 with
// acknowledgeProcessLevelIsolation, backed by the slotId dispatch-loop
// reference runtime (cmd/runtimes/echo-concurrent).
const (
	concurrentPoolName   = "concurrent-echo-pool"
	concurrentRuntimeRef = "echo-runtime-concurrent"
)

// executionModesNamespace is the namespace the agent-pod workload runs
// in (kind.RequireAgentWorkload's agentNamespace, unexported to that
// package).
const executionModesNamespace = "lenny-agents"

// spec: §5.2 (spec/05_runtime-registry-and-pool-model.md, "Recycle
// lifecycle (`recycle.enabled: true`)") — "When the pod's occupancy
// reaches zero, the gateway patches the pod's SandboxClaim to
// `recycling`... On a successful scrub on a `standard` or `in-place`
// pool the pod is held for its tenant through the claim's `reserved`
// state and serves the next session." And (§5.2, "Lenny scrub
// procedure") — "Before materializing, the adapter MUST remove all
// files from `/workspace/current` to prevent residual state from prior
// tasks" and "Verify scrub by stat-checking the workspace path... if
// any path is non-empty after scrub... the scrub is marked failed."
//
// diagnosis: a failure here means §5.2 sequential pod reuse ("task
// mode") is broken on a real agent pod: either the second session on a
// recycling pod did not land on the same pod as the first (pod reuse
// itself regressed — the pod was retired and replaced instead of
// scrubbed and recycled) or the first session's workspace content
// survived into the second session (the whole-pod scrub regressed or
// never ran). This is the residual-state boundary between successive
// tasks on one pod that only a real pod — not the tier-4 compose stack —
// can exercise, since it depends on the adapter's own scrub sequence
// running against a real filesystem.
func TestTaskModeRecycleScrubsWorkspaceBetweenSessions(t *testing.T) {
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 30 * time.Second})
	c := d.Cluster()
	requirePoolReadyPods(t, c, taskModePoolName, 1)
	t.Skip("precondition not met: cmd/lenny-adapter never wires a CheckpointSink (pkg/adapter/server.go " +
		"Server.Checkpoints is left nil in every deployment, not only this Kind overlay), so every adapter " +
		"Checkpoint RPC returns Unimplemented. The §7.1 seal-and-export step on /terminate calls Checkpoint " +
		"for any session that ran on a bound pod (checkpointer.Seal no-ops only when this replica holds no " +
		"live binding for the session at all) and, on exhausting its retry window, relabels the session " +
		"failed/workspace_seal_timeout. §6.2 requires a failed session to always retire its pod regardless of " +
		"sessionPolicy.recycle, so occupancy-zero recycling can never be observed end to end until a " +
		"checkpoint sink is wired into the adapter binary — the same precondition gap " +
		"tests/tier5_e2e_kind/checkpoint_resume_test.go already names for the eviction-checkpoint path. " +
		"Verified directly: with sessionPolicy.recycle folding fixed (podsession.PoolPolicyMirror.Recycle), " +
		"a session's own sessionIsolationLevel.podReuse correctly reports true, but Release still sends a " +
		"plain Shutdown (not ShutdownRecycle) because the session's disposition is \"failed\", not " +
		"\"completed\". The rest of this test is written against the spec and is ready to run once the " +
		"checkpoint sink lands.")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	tenant := uniqueName("task-mode-tenant")
	if err := d.BootstrapTenant(ctx, tenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}

	sessA, err := d.CreateAndStartWithPlan(ctx, tenant, taskModeRuntimeRef,
		inlineWorkspacePlan("marker-a.txt", "workspace-of-session-A"))
	if err != nil {
		t.Fatalf("create session A on %s: %v", taskModeRuntimeRef, err)
	}
	if sessA.PodAssignment == "" {
		t.Fatalf("session A carries no podAssignment; the pool did not bind a pod")
	}
	podA := sessA.PodAssignment
	t.Logf("session A %s landed on pod %s", sessA.ID, podA)

	if err := d.Terminate(ctx, tenant, sessA.ID); err != nil {
		t.Fatalf("terminate session A: %v", err)
	}

	// The whole-pod scrub runs asynchronously after Terminate releases the
	// pod (§5.2: "the gateway does not block the response on the scrub").
	// Wait for the recycled pod to report idle again before claiming
	// session B.
	waitPodLabel(t, c, podA, "lenny.dev/state", "idle", 90*time.Second)

	sessB, err := d.CreateAndStartWithPlan(ctx, tenant, taskModeRuntimeRef,
		inlineWorkspacePlan("marker-b.txt", "workspace-of-session-B"))
	if err != nil {
		t.Fatalf("create session B on %s: %v", taskModeRuntimeRef, err)
	}
	t.Cleanup(func() { _ = d.Terminate(context.Background(), tenant, sessB.ID) })

	if sessB.PodAssignment != podA {
		t.Fatalf("session B landed on pod %q, want the recycled pod %q; §5.2 recycle.enabled did not reuse "+
			"the pod (the pod was retired and replaced instead of scrubbed and recycled)", sessB.PodAssignment, podA)
	}
	t.Logf("session B %s reused pod %s", sessB.ID, sessB.PodAssignment)

	listing := execDebugContainer(t, c, podA, []string{
		"sh", "-c", "ls -la /workspace/current/ 2>&1",
	})
	if strings.Contains(listing, "marker-a.txt") {
		t.Errorf("pod %s: session A's /workspace/current/marker-a.txt survived into session B; "+
			"the §5.2 whole-pod scrub did not clear residual state between recycled sessions:\n%s", podA, listing)
	}
	if !strings.Contains(listing, "marker-b.txt") {
		t.Errorf("pod %s: session B's /workspace/current/marker-b.txt is missing; workspace materialization "+
			"did not run for the reused pod:\n%s", podA, listing)
	}
	if !strings.Contains(listing, "marker-a.txt") && strings.Contains(listing, "marker-b.txt") {
		t.Logf("pod %s: /workspace/current scrubbed clean between sessions A and B; only session B's "+
			"marker is present:\n%s", podA, listing)
	}
}

// spec: §5.2 (spec/05_runtime-registry-and-pool-model.md, "Concurrent
// sessions (`maxConcurrentSessions > 1`)") — "Each slot gets its own
// workspace under `/workspace/slots/{slotId}/`... Cross-slot isolation
// is process-level and filesystem-level." And §6.4 (spec/
// 06_warm-pod-model.md) — "The runtime MUST NOT assume a global
// `/workspace/current` path when `maxConcurrentSessions > 1`."
//
// diagnosis: a failure here means the §5.2 concurrent-slot filesystem
// isolation is broken on a real agent pod: either two sessions
// multiplexed onto one pod's slots did not each get their own
// /workspace/slots/{slotId}/current/ tree, one slot's file leaked into
// a sibling slot's tree, or a slot's content leaked into the whole-pod
// /workspace/current (which a concurrent-workspace pod must never use).
// The gateway derives slotId from the session id (podsession/
// slotbinder.go), so each session's own id names its slot directory.
func TestConcurrentSlotsIsolateWorkspaceDirectories(t *testing.T) {
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 30 * time.Second})
	c := d.Cluster()
	requirePoolReadyPods(t, c, concurrentPoolName, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	tenant := uniqueName("concurrent-tenant")
	if err := d.BootstrapTenant(ctx, tenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}

	sessA, err := d.CreateAndStartWithPlan(ctx, tenant, concurrentRuntimeRef,
		inlineWorkspacePlan("marker.txt", "workspace-of-slot-A"))
	if err != nil {
		t.Fatalf("create session A (slot 1) on %s: %v", concurrentRuntimeRef, err)
	}
	t.Cleanup(func() { _ = d.Terminate(context.Background(), tenant, sessA.ID) })
	if sessA.PodAssignment == "" {
		t.Fatalf("session A carries no podAssignment; the pool did not bind a pod")
	}
	pod := sessA.PodAssignment
	t.Logf("session A %s (slot %s) landed on pod %s", sessA.ID, sessA.ID, pod)

	sessB, err := d.CreateAndStartWithPlan(ctx, tenant, concurrentRuntimeRef,
		inlineWorkspacePlan("marker.txt", "workspace-of-slot-B"))
	if err != nil {
		t.Fatalf("create session B (slot 2) on %s: %v", concurrentRuntimeRef, err)
	}
	t.Cleanup(func() { _ = d.Terminate(context.Background(), tenant, sessB.ID) })
	t.Logf("session B %s (slot %s) landed on pod %s", sessB.ID, sessB.ID, sessB.PodAssignment)

	// §5.2's whole point is multiplexing simultaneous sessions onto one
	// pod (podclaim/slotclaimer.go prefers a partially-occupied pod over
	// claiming a fresh idle one), so both sessions should share the same
	// pod. If they did not, the isolation assertions below would pass
	// trivially without exercising slotId multiplexing at all, so this is
	// a hard precondition rather than a soft log.
	if sessB.PodAssignment != pod {
		t.Fatalf("session B landed on pod %q, want session A's pod %q; the concurrent pool did not "+
			"multiplex both sessions onto one pod's slots", sessB.PodAssignment, pod)
	}

	listing := execDebugContainer(t, c, pod, []string{
		"sh", "-c", "cat /workspace/slots/" + sessA.ID + "/current/marker.txt 2>&1; echo ---; " +
			"cat /workspace/slots/" + sessB.ID + "/current/marker.txt 2>&1; echo ---; " +
			"ls /workspace/current/ 2>&1",
	})
	parts := strings.Split(listing, "---")
	if len(parts) != 3 {
		t.Fatalf("pod %s: debug container output did not split into 3 sections, got %d:\n%s", pod, len(parts), listing)
	}
	slotAContent, slotBContent, wholePodListing := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2])

	if slotAContent != "workspace-of-slot-A" {
		t.Errorf("pod %s: /workspace/slots/%s/current/marker.txt = %q, want \"workspace-of-slot-A\"; "+
			"slot A's per-slot workspace is missing or was overwritten by a sibling slot", pod, sessA.ID, slotAContent)
	}
	if slotBContent != "workspace-of-slot-B" {
		t.Errorf("pod %s: /workspace/slots/%s/current/marker.txt = %q, want \"workspace-of-slot-B\"; "+
			"slot B's per-slot workspace is missing or was overwritten by a sibling slot", pod, sessB.ID, slotBContent)
	}
	// §6.4: a concurrent-workspace pod has no shared /workspace/current;
	// the whole-pod path must never have been materialized into.
	if wholePodListing != "" {
		t.Errorf("pod %s: /workspace/current is non-empty (%q); a concurrent-workspace pod must never "+
			"materialize into the whole-pod path, only into /workspace/slots/{slotId}/current/", pod, wholePodListing)
	}
	t.Logf("pod %s: slot %s and slot %s each hold only their own workspace content, whole-pod "+
		"/workspace/current is empty", pod, sessA.ID, sessB.ID)
}

// uniqueName returns a base name suffixed with a per-call nanosecond
// timestamp so repeated local runs against the long-lived Kind cluster
// never collide on a stale tenant from a prior run.
func uniqueName(base string) string {
	return base + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

// inlineWorkspacePlan renders a minimal §14 WorkspacePlan carrying one
// inline file, the JSON body CreateAndStartWithPlan needs.
func inlineWorkspacePlan(path, content string) json.RawMessage {
	plan := struct {
		SchemaVersion int `json:"schemaVersion"`
		Sources       []struct {
			Type    string `json:"type"`
			Path    string `json:"path"`
			Content string `json:"content"`
			Mode    string `json:"mode"`
		} `json:"sources"`
	}{SchemaVersion: 1}
	plan.Sources = append(plan.Sources, struct {
		Type    string `json:"type"`
		Path    string `json:"path"`
		Content string `json:"content"`
		Mode    string `json:"mode"`
	}{Type: "inlineFile", Path: path, Content: content, Mode: "0644"})
	b, err := json.Marshal(plan)
	if err != nil {
		panic(fmt.Sprintf("inlineWorkspacePlan: %v", err)) // encoding a static struct never fails
	}
	return b
}

// requirePoolReadyPods skips the calling test when the named pool has
// fewer than n Ready warm pods, so a cluster whose install.sh predates
// this pool (or that has not finished warming it) degrades to a clean
// skip instead of a failure. Mirrors gvisor_isolation_test.go's
// requireGvisorPoolPod.
func requirePoolReadyPods(t *testing.T, c *kind.Cluster, pool string, n int) {
	t.Helper()
	selector := "lenny.dev/pool=" + pool
	if err := c.Kubectl(
		"-n", executionModesNamespace, "wait", "--for=condition=Ready",
		"pod", "-l", selector, "--timeout=120s",
	).Run(); err != nil {
		t.Skip(pool + " has no Ready warm pod yet (pools warm asynchronously); " +
			"re-run after tests/testinfra/kind/install.sh completes")
	}
	out, err := c.KubectlOut(t, "-n", executionModesNamespace, "get", "pods",
		"-l", selector, "-o", "jsonpath={.items[*].metadata.name}")
	if err != nil {
		t.Fatalf("listing %s pods: %v\n%s", pool, err, out)
	}
	got := len(strings.Fields(strings.TrimSpace(out)))
	if got < n {
		t.Skip(pool + " has fewer than the expected warm pods; re-run after tests/testinfra/kind/install.sh completes")
	}
}

// waitPodLabel polls pod's named label until it equals want or timeout
// elapses, failing the test on timeout. Used to observe the §5.2
// recycle boundary: the WarmPoolController's lenny.dev/state label
// transitions active -> idle once the whole-pod scrub reports and the
// pod is returned to the pool.
func waitPodLabel(t *testing.T, c *kind.Cluster, pod, label, want string, timeout time.Duration) {
	t.Helper()
	jsonpath := fmt.Sprintf("{.metadata.labels.%s}", strings.ReplaceAll(label, ".", `\.`))
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		last = podField(t, c, pod, jsonpath)
		if last == want {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("%s: label %s did not reach %q within %s (last seen %q)", pod, label, want, timeout, last)
}

// execDebugContainer attaches a short-lived busybox ephemeral debug
// container to pod's "runtime" container with the shared "workspace"
// emptyDir mounted at /workspace (the same mount every real container
// in the pod carries), runs cmd, waits for it to terminate, and returns
// its combined log output. Both echo-runtime-task-mode and
// echo-runtime-concurrent are distroless (no shell), so this is the
// only way to inspect their shared workspace volume from outside the
// adapter. Mirrors gvisor_isolation_test.go's
// assertGvisorKernelFingerprint, generalized to an arbitrary shell
// command and a workspace volume mount.
//
// The debug container's securityContext satisfies both admission layers
// a real attach goes through: §13.1 pod-security (non-root, read-only
// root filesystem, all capabilities dropped) and §12.9.3's
// lenny-ephemeral-container-cred-guard (runAsUser/runAsGroup distinct
// from the pod's adapter/agent UIDs and the lenny-cred-readers GID). The
// chosen UID/GID (25252) has no other significance; it is the same value
// gvisor_isolation_test.go uses for the same reason.
func execDebugContainer(t *testing.T, c *kind.Cluster, pod string, cmd []string) string {
	t.Helper()
	containerName := "wsprobe-" + strconv.FormatInt(time.Now().UnixNano(), 36)

	existing := podField(t, c, pod, "{.spec.ephemeralContainers}")
	if existing == "" {
		existing = "[]"
	}

	bodyPath := filepath.Join(t.TempDir(), "wsprobe-attach.json")
	body, err := debugContainerAttachBody(pod, containerName, existing, cmd)
	if err != nil {
		t.Fatalf("build ephemeral-container attach body: %v", err)
	}
	if err := os.WriteFile(bodyPath, body, 0o600); err != nil {
		t.Fatalf("write ephemeral-container attach body: %v", err)
	}
	raw := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/ephemeralcontainers", executionModesNamespace, pod)
	if out, err := c.KubectlOut(t, "-n", executionModesNamespace, "replace", "--raw", raw, "-f", bodyPath); err != nil {
		t.Fatalf("attach %s debug container to %s: %v\n%s", containerName, pod, err, out)
	}

	deadline := time.Now().Add(60 * time.Second)
	var terminated bool
	for time.Now().Before(deadline) {
		reason := podField(t, c, pod, fmt.Sprintf(
			`{.status.ephemeralContainerStatuses[?(@.name=="%s")].state.terminated.reason}`, containerName,
		))
		if reason != "" {
			terminated = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !terminated {
		t.Fatalf("%s: debug container %q did not terminate within 60s", pod, containerName)
	}

	logs, err := c.KubectlOut(t, "-n", executionModesNamespace, "logs", pod, "-c", containerName)
	if err != nil {
		t.Fatalf("read logs of debug container %q on %s: %v\n%s", containerName, pod, err, logs)
	}
	return logs
}

// debugContainerAttachBody renders the JSON pod object posted to the
// pods/ephemeralcontainers subresource: existingEphemeralContainersJSON
// carried forward verbatim (the subresource replaces the whole list
// rather than appending) plus the new debug container, which mounts the
// pod's "workspace" emptyDir volume and runs cmd.
func debugContainerAttachBody(podName, containerName, existingEphemeralContainersJSON string, cmd []string) ([]byte, error) {
	var existing []json.RawMessage
	if err := json.Unmarshal([]byte(existingEphemeralContainersJSON), &existing); err != nil {
		return nil, fmt.Errorf("parse existing ephemeralContainers %q: %w", existingEphemeralContainersJSON, err)
	}
	cmdJSON, err := json.Marshal(cmd)
	if err != nil {
		return nil, fmt.Errorf("encode debug container command %v: %w", cmd, err)
	}
	newContainer := json.RawMessage(fmt.Sprintf(`{
    "name": %q,
    "image": "busybox:1.36",
    "command": %s,
    "targetContainerName": "runtime",
    "volumeMounts": [{"name": "workspace", "mountPath": "/workspace"}],
    "securityContext": {
      "allowPrivilegeEscalation": false,
      "readOnlyRootFilesystem": true,
      "runAsNonRoot": true,
      "runAsUser": 25252,
      "runAsGroup": 25252,
      "capabilities": {"drop": ["ALL"]},
      "seccompProfile": {"type": "RuntimeDefault"}
    }
  }`, containerName, cmdJSON))
	body := struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Metadata   struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Spec struct {
			EphemeralContainers []json.RawMessage `json:"ephemeralContainers"`
		} `json:"spec"`
	}{
		APIVersion: "v1",
		Kind:       "Pod",
	}
	body.Metadata.Name = podName
	body.Metadata.Namespace = executionModesNamespace
	body.Spec.EphemeralContainers = append(existing, newContainer)
	return json.Marshal(body)
}
