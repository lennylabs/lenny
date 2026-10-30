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
	"fmt"
	"os/exec"
	"strings"
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
	namespace, app, err := parseChaosMeshTarget(target)
	if err != nil {
		t.Fatalf("chaos.LatencyFault chaos-mesh target %q: %v", target, err)
	}
	name := chaosResourceName("lenny-latency", app)
	manifest := networkChaosLatencyManifest(name, namespace, app, latency)
	if err := kubectlApply(manifest); err != nil {
		t.Fatalf("chaos.LatencyFault apply NetworkChaos: %v", err)
	}
	return func() { _ = kubectlDelete("networkchaos", namespace, name) }
}

// parseChaosMeshTarget parses the "<namespace>/<app-label>" target
// shape callers pass to LatencyFault and PartitionService. The
// chaos-mesh selector picks pods by `app=<app-label>` inside the
// supplied namespace, matching the convention the §12.8 chaos
// scenarios use.
func parseChaosMeshTarget(target string) (namespace, app string, err error) {
	parts := strings.SplitN(target, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("target must be <namespace>/<app-label>, got %q", target)
	}
	return parts[0], parts[1], nil
}

// chaosResourceName builds a chaos-resource name that is stable per
// app label so retries against the same target overwrite rather than
// accumulating. A timestamp suffix would be a better differentiator
// when two faults overlap, but the §12.8 scenarios inject one fault
// at a time so the stable name is sufficient and simpler to clean
// up after a test crash.
func chaosResourceName(prefix, app string) string {
	return prefix + "-" + app
}

// networkChaosLatencyManifest renders the §12.8 latency-injection
// CR. The manifest applies to every pod under `app=<app>` in the
// supplied namespace and adds the documented latency to every TCP
// packet. The duration field is intentionally omitted so the fault
// stays in place until the cleanup deletes the CR; the test owns
// the lifetime via the returned cleanup func.
func networkChaosLatencyManifest(name, namespace, app string, latency time.Duration) string {
	return fmt.Sprintf(`apiVersion: chaos-mesh.org/v1alpha1
kind: NetworkChaos
metadata:
  name: %s
  namespace: %s
spec:
  action: delay
  mode: all
  selector:
    namespaces:
      - %s
    labelSelectors:
      app: %s
  delay:
    latency: "%s"
`, name, namespace, namespace, app, latency)
}

// networkChaosPartitionManifest renders the §12.8 network-partition
// CR. The manifest drops bidirectional traffic to every pod under
// `app=<app>` in the supplied namespace. The duration is honored by
// the controller; the cleanup deletes the CR early in case the test
// finishes before the duration expires.
func networkChaosPartitionManifest(name, namespace, app string, duration time.Duration) string {
	return fmt.Sprintf(`apiVersion: chaos-mesh.org/v1alpha1
kind: NetworkChaos
metadata:
  name: %s
  namespace: %s
spec:
  action: partition
  mode: all
  direction: both
  selector:
    namespaces:
      - %s
    labelSelectors:
      app: %s
  duration: "%s"
`, name, namespace, namespace, app, duration)
}

// kubectlApply pipes manifest to `kubectl apply -f -` and returns
// the combined output on failure.
func kubectlApply(manifest string) error {
	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl apply: %v\n%s", err, out)
	}
	return nil
}

// kubectlDelete deletes a chaos-mesh resource. Errors are swallowed
// (best-effort cleanup) because cleanup runs in t.Cleanup and the
// test should not fail on a cleanup race with the controller.
func kubectlDelete(kind, namespace, name string) error {
	cmd := exec.Command("kubectl", "-n", namespace, "delete", kind, name, "--ignore-not-found")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl delete %s/%s: %v\n%s", namespace, name, err, out)
	}
	return nil
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
	namespace, app, err := parseChaosMeshTarget(target)
	if err != nil {
		t.Fatalf("chaos.PartitionService chaos-mesh target %q: %v", target, err)
	}
	name := chaosResourceName("lenny-partition", app)
	manifest := networkChaosPartitionManifest(name, namespace, app, duration)
	if err := kubectlApply(manifest); err != nil {
		t.Fatalf("chaos.PartitionService apply NetworkChaos: %v", err)
	}
	return func() { _ = kubectlDelete("networkchaos", namespace, name) }
}
