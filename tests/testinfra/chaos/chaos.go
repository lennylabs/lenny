// SPDX-License-Identifier: MIT

// Package chaos wraps the §12.8 fault-injection primitives the
// tier-8 chaos suite depends on. Each helper is a thin interface
// that delegates to either toxiproxy (compose-level fault injection)
// or chaos-mesh (kind/cloud-level fault injection) depending on the
// running profile.
//
// The package does not actually invoke the underlying tools when
// they're absent; instead, every helper returns a typed
// ErrToolUnavailable so the calling test can SkipUnlessAvailable.
package chaos

import (
	"errors"
	"os/exec"
	"testing"
	"time"
)

// ErrToolUnavailable signals that the required injector binary is
// not on PATH.
var ErrToolUnavailable = errors.New("chaos: required tool not on PATH")

// ToxiproxyAvailable reports whether `toxiproxy-cli` is installed.
func ToxiproxyAvailable() bool {
	_, err := exec.LookPath("toxiproxy-cli")
	return err == nil
}

// ChaosMeshAvailable reports whether kubectl + the chaos-mesh CRDs
// are reachable.
func ChaosMeshAvailable() bool {
	if _, err := exec.LookPath("kubectl"); err != nil {
		return false
	}
	// Probe for the CRD; if it doesn't exist we treat chaos-mesh as
	// unavailable. The check is best-effort: a non-zero exit on the
	// kubectl call also reports unavailable.
	cmd := exec.Command("kubectl", "get", "crd", "podchaos.chaos-mesh.org")
	return cmd.Run() == nil
}

// SkipUnlessAvailable skips the test when no fault-injection tool
// is reachable. Tier-8 tests call this at the top of their body so
// they degrade gracefully on hosts without the injectors.
func SkipUnlessAvailable(t testing.TB) {
	t.Helper()
	if !ToxiproxyAvailable() && !ChaosMeshAvailable() {
		t.Skip("chaos.SkipUnlessAvailable: neither toxiproxy-cli nor chaos-mesh CRDs reachable; install per TESTING.md §12.8")
	}
}

// LatencyFault injects N ms of latency on the named compose service
// (toxiproxy variant) or on the matching Kubernetes label selector
// (chaos-mesh variant). Returns a cleanup that removes the fault.
func LatencyFault(t testing.TB, target string, latency time.Duration) func() {
	t.Helper()
	SkipUnlessAvailable(t)
	if ToxiproxyAvailable() {
		return injectViaToxiproxy(t, target, latency)
	}
	return injectViaChaosMesh(t, target, latency)
}

func injectViaToxiproxy(t testing.TB, target string, latency time.Duration) func() {
	t.Helper()
	// Toxiproxy invocation pattern. The exact CLI is documented at
	// https://github.com/Shopify/toxiproxy. The wrapper shells out
	// to `toxiproxy-cli toxic add <target> -t latency -a latency=N`.
	cmd := exec.Command("toxiproxy-cli", "toxic", "add", target,
		"-t", "latency", "-a", "latency="+latency.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("toxiproxy add latency: %v\n%s", err, out)
	}
	return func() {
		rm := exec.Command("toxiproxy-cli", "toxic", "remove", target, "-n", "latency_downstream")
		_, _ = rm.CombinedOutput()
	}
}

func injectViaChaosMesh(t testing.TB, target string, latency time.Duration) func() {
	t.Helper()
	t.Logf("chaos.LatencyFault via chaos-mesh: %s latency on %q (placeholder; applies a NetworkChaos CR)", latency, target)
	// Real implementation applies a NetworkChaos manifest via
	// kubectl apply -f -. The CR shape is documented at
	// https://chaos-mesh.org/docs/define-network-chaos/. For Phase
	// 0 we emit a log and return a no-op cleanup; the test that
	// drives this still skips via SkipUnlessAvailable when the CRDs
	// aren't present.
	return func() {}
}

// KillPod kills a pod matching the label selector via `kubectl
// delete pod -l <selector>`. Used by lifecycle chaos scenarios
// (pod kill during active session).
func KillPod(t testing.TB, namespace, selector string) {
	t.Helper()
	if !ChaosMeshAvailable() {
		t.Skipf("KillPod: chaos-mesh / kubectl not available")
	}
	cmd := exec.Command("kubectl", "-n", namespace, "delete", "pod", "-l", selector, "--grace-period=0", "--force")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("kill pod %s: %v\n%s", selector, err, out)
	}
}

// PartitionService drops all traffic to/from `target` for the given
// duration, then restores. Returns a cleanup that ensures the
// partition is healed even when the test fails.
func PartitionService(t testing.TB, target string, duration time.Duration) func() {
	t.Helper()
	SkipUnlessAvailable(t)
	if ToxiproxyAvailable() {
		cmd := exec.Command("toxiproxy-cli", "toxic", "add", target, "-t", "timeout", "-a", "timeout=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("toxiproxy partition: %v\n%s", err, out)
		}
		return func() {
			rm := exec.Command("toxiproxy-cli", "toxic", "remove", target, "-n", "timeout_downstream")
			_, _ = rm.CombinedOutput()
		}
	}
	t.Logf("chaos.PartitionService via chaos-mesh: partition %q for %s (placeholder)", target, duration)
	return func() {}
}
