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
// tests/tier4_integration/concurrent_workspace_test.go exercises sessionId
// multiplexing in-process: a bufconn adapter driving a spawned
// echo-concurrent binary, with no real pod, no RuntimeClass, and no
// per-slot filesystem isolation enforced by a real container boundary.
// This file drives the same sequential-reuse and concurrent-slot
// behaviors through the live gateway against pods
// tests/testinfra/kind/install.sh warms in the lenny-agents namespace
// (task-mode-echo-pool and concurrent-echo-pool), closing the TESTING.md §14.9
// coverage gap for concurrent per-slot directories on a real sandbox.
// TestTaskModeRecycleScrubsWorkspaceBetweenSessions exercises the same
// real-pod path for the task-mode workspace-scrub half of the gap. The
// gateway-side checkpoint driver that opens the §10.1 Checkpoint stream
// against the pod and mints per-chunk presigned grants is now wired, so
// the §7.1 seal-and-export step on /terminate completes end to end and a
// completed session reaches the §6.2 occupancy-zero recycle branch.
//
// Both echo-runtime-task-mode and echo-runtime-concurrent are
// distroless (no shell), so workspace content is inspected through a
// short-lived busybox ephemeral debug container attached to the pod's
// "runtime" container with the shared "workspace" emptyDir mounted,
// mirroring the pattern gvisor_isolation_test.go uses for its
// /proc/version kernel-fingerprint probe. The adapter (not the debug
// container's unprivileged UID) owns each session's
// /workspace/slots/{sessionId}/current, so content is placed via a client
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
// acknowledgeProcessLevelIsolation, backed by the sessionId dispatch-loop
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
// procedure") — the adapter removes each session's workspace tree before
// materializing so no residual state from a prior task survives, and
// "Verify scrub by stat-checking the workspace path... if any path is
// non-empty after scrub... the scrub is marked failed."
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

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	tenant := uniqueName("task-mode-tenant")
	if err := d.BootstrapTenant(ctx, tenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}
	// spec: §10.6 — a bootstrapped tenant carries noEnvironmentPolicy
	// deny-all, and this test creates sessions that name no environment.
	ensureTenantAllowsSessionsWithNoEnvironment(t, d, tenant)

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

	// spec: §6.4 — the per-slot tree is the only workspace layout, so each
	// session materialized into its own /workspace/slots/{sessionId}/current
	// and the recycled pod must carry session B's tree and none of session
	// A's. The pod-global /workspace/current is retired on every pool class,
	// including this exclusive one, so the directory must be absent.
	listing := execDebugContainer(t, c, podA, []string{
		"sh", "-c", "ls -la /workspace/slots/ 2>&1; echo ---; " +
			"ls -la /workspace/slots/" + sessB.ID + "/current/ 2>&1; echo ---; " +
			"[ -e /workspace/current ] && echo present || echo absent",
	})
	parts := strings.Split(listing, "---")
	if len(parts) != 3 {
		t.Fatalf("pod %s: debug container output did not split into 3 sections, got %d:\n%s", podA, len(parts), listing)
	}
	slotsListing, currentListing, podGlobal := strings.TrimSpace(parts[0]),
		strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2])

	if strings.Contains(slotsListing, sessA.ID) {
		t.Errorf("pod %s: session A's slot tree /workspace/slots/%s survived into session B; "+
			"the §5.2 whole-pod scrub did not clear residual state between recycled sessions:\n%s",
			podA, sessA.ID, slotsListing)
	}
	if !strings.Contains(currentListing, "marker-b.txt") {
		t.Errorf("pod %s: session B's /workspace/slots/%s/current/marker-b.txt is missing; workspace "+
			"materialization did not run for the reused pod:\n%s", podA, sessB.ID, currentListing)
	}
	if podGlobal != "absent" {
		t.Errorf("pod %s: /workspace/current is %s; §6.4 retires the pod-global path on every pool class, "+
			"including a maxConcurrentSessions: 1 pool", podA, podGlobal)
	}
}

// spec: §6.4 (spec/06_warm-pod-model.md) — a session's cwd is
// `/workspace/slots/{sessionId}/current/` and no global
// `/workspace/current` path exists.
//
// diagnosis: an exclusive pool (maxConcurrentSessions: 1) still
// materializes into a pod-global /workspace/current on a real agent pod.
// This is the pool class that used the retired path before the layout
// became uniform, so a regression that reinstates it fails here and passes
// unnoticed on the concurrent pool that never used it.
func TestSessionModePoolMaterializesIntoTheSlotTree_spec_6_4(t *testing.T) {
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 30 * time.Second})
	c := d.Cluster()
	requirePoolReadyPods(t, c, taskModePoolName, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	tenant := uniqueName("session-mode-layout-tenant")
	if err := d.BootstrapTenant(ctx, tenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}
	// spec: §10.6 — a bootstrapped tenant carries noEnvironmentPolicy
	// deny-all, and this test creates sessions that name no environment.
	ensureTenantAllowsSessionsWithNoEnvironment(t, d, tenant)

	sess, err := d.CreateAndStartWithPlan(ctx, tenant, taskModeRuntimeRef,
		inlineWorkspacePlan("marker.txt", "workspace-of-session-mode"))
	if err != nil {
		t.Fatalf("create session on %s: %v", taskModeRuntimeRef, err)
	}
	t.Cleanup(func() { _ = d.Terminate(context.Background(), tenant, sess.ID) })
	if sess.PodAssignment == "" {
		t.Fatalf("session carries no podAssignment; the pool did not bind a pod")
	}
	pod := sess.PodAssignment

	listing := execDebugContainer(t, c, pod, []string{
		"sh", "-c", "cat /workspace/slots/" + sess.ID + "/current/marker.txt 2>&1; echo ---; " +
			"[ -e /workspace/current ] && echo present || echo absent",
	})
	parts := strings.Split(listing, "---")
	if len(parts) != 2 {
		t.Fatalf("pod %s: debug container output did not split into 2 sections, got %d:\n%s", pod, len(parts), listing)
	}
	slotContent, podGlobal := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])

	if slotContent != "workspace-of-session-mode" {
		t.Errorf("pod %s: /workspace/slots/%s/current/marker.txt = %q, want the session's own content; "+
			"an exclusive pool materializes into its session's slot tree like every other pool",
			pod, sess.ID, slotContent)
	}
	if podGlobal != "absent" {
		t.Errorf("pod %s: /workspace/current is %s; §6.4 retires the pod-global path", pod, podGlobal)
	}
}

// spec: §5.2 (spec/05_runtime-registry-and-pool-model.md, "Concurrent
// sessions (`maxConcurrentSessions > 1`)") — "Each slot gets its own
// workspace under `/workspace/slots/{slotId}/`... Cross-slot isolation
// is process-level and filesystem-level." And §6.4 (spec/
// 06_warm-pod-model.md) — "No global `/workspace/current` path exists,
// and the runtime MUST NOT assume one."
//
// diagnosis: a failure here means the §5.2 concurrent-slot filesystem
// isolation is broken on a real agent pod: either two sessions
// multiplexed onto one pod's slots did not each get their own
// /workspace/slots/{sessionId}/current/ tree, one slot's file leaked into
// a sibling slot's tree, or a pod-global /workspace/current appeared on a
// pod that must carry none. A session-mode slot's identifier is its
// session's identifier, so each session's own id names its slot directory.
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
	// spec: §10.6 — a bootstrapped tenant carries noEnvironmentPolicy
	// deny-all, and this test creates sessions that name no environment.
	ensureTenantAllowsSessionsWithNoEnvironment(t, d, tenant)

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
	// trivially without exercising sessionId multiplexing at all, so this is
	// a hard precondition rather than a soft log.
	if sessB.PodAssignment != pod {
		t.Fatalf("session B landed on pod %q, want session A's pod %q; the concurrent pool did not "+
			"multiplex both sessions onto one pod's slots", sessB.PodAssignment, pod)
	}

	listing := execDebugContainer(t, c, pod, []string{
		"sh", "-c", "cat /workspace/slots/" + sessA.ID + "/current/marker.txt 2>&1; echo ---; " +
			"cat /workspace/slots/" + sessB.ID + "/current/marker.txt 2>&1; echo ---; " +
			"[ -e /workspace/current ] && echo present || echo absent",
	})
	parts := strings.Split(listing, "---")
	if len(parts) != 3 {
		t.Fatalf("pod %s: debug container output did not split into 3 sections, got %d:\n%s", pod, len(parts), listing)
	}
	slotAContent, slotBContent, podGlobal := strings.TrimSpace(parts[0]),
		strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2])

	if slotAContent != "workspace-of-slot-A" {
		t.Errorf("pod %s: /workspace/slots/%s/current/marker.txt = %q, want \"workspace-of-slot-A\"; "+
			"slot A's per-slot workspace is missing or was overwritten by a sibling slot", pod, sessA.ID, slotAContent)
	}
	if slotBContent != "workspace-of-slot-B" {
		t.Errorf("pod %s: /workspace/slots/%s/current/marker.txt = %q, want \"workspace-of-slot-B\"; "+
			"slot B's per-slot workspace is missing or was overwritten by a sibling slot", pod, sessB.ID, slotBContent)
	}
	// §6.4: the pod-global /workspace/current is retired rather than kept as
	// an empty directory, so the path must not exist at all.
	if podGlobal != "absent" {
		t.Errorf("pod %s: /workspace/current is %s; §6.4 retires the pod-global path, and every session "+
			"materializes into /workspace/slots/{sessionId}/current/", pod, podGlobal)
	}
	t.Logf("pod %s: session %s and session %s each hold only their own workspace content, and no "+
		"pod-global /workspace/current exists", pod, sessA.ID, sessB.ID)
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
// skip instead of a failure. It counts the pods that report condition
// Ready and waits for the count to reach n, rather than requiring every
// pod carrying the pool label to be Ready: a wedged pod left behind by
// an earlier run keeps a label-wide readiness wait failing forever, and
// that hid the Ready pods beside it, skipping the §6.4 slot-tree cases
// on a cluster that had the capacity to run them.
func requirePoolReadyPods(t *testing.T, c *kind.Cluster, pool string, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		if readyPoolPods(t, c, pool) >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Skipf("%s has fewer than %d Ready warm pods (pools warm asynchronously); "+
				"re-run after tests/testinfra/kind/install.sh completes", pool, n)
		}
		time.Sleep(3 * time.Second)
	}
}

// readyPoolPods returns how many pods carrying the pool's label report
// condition Ready true. A kubectl failure counts as zero so the caller
// keeps polling until its deadline.
func readyPoolPods(t *testing.T, c *kind.Cluster, pool string) int {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", executionModesNamespace, "get", "pods",
		"-l", "lenny.dev/pool="+pool, "-o",
		`jsonpath={range .items[*]}{.status.conditions[?(@.type=="Ready")].status}{"\n"}{end}`)
	if err != nil {
		return 0
	}
	ready := 0
	for _, status := range strings.Fields(out) {
		if status == "True" {
			ready++
		}
	}
	return ready
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
// root filesystem, all capabilities dropped) and TESTING.md §12.9.3's
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
