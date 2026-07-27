// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind tests for the health of the cluster the rest of the
// tier-5, 7b, 8, and 9 suites run against. Every other test in those
// tiers assumes a serving control plane and a CNI that enforces
// NetworkPolicy; when either assumption fails, the suites report skips
// or, worse, policy assertions that pass because nothing is enforced.
// These tests name that condition directly so it surfaces as one
// actionable failure instead of dozens of silent skips.

package tier5_e2e_kind_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// spec: 13.2 (network isolation)
// diagnosis: the cluster CNI is not enforcing NetworkPolicy. §13.2
// requires a CNI that supports NetworkPolicy enforcement including
// egress rules; a CNI DaemonSet in a restart loop enforces nothing, so
// the default-deny policy the chart installs into every agent namespace
// is absent. Every NetworkPolicy assertion in tiers 5, 8, and 9 is
// invalid while this fails, including the ones that assert a connection
// is refused: on an unenforced cluster those can pass for the wrong
// reason. The failure message carries the cycling pods, their
// termination reason, and the cluster object counts. On a standing
// cluster an OOMKilled CNI pod is normally informer-cache pressure from
// accumulated test objects rather than a code defect.
func TestClusterCNIEnforcesNetworkPolicy(t *testing.T) {
	kind.SkipUnlessAvailable(t)
	c := kind.EnsureCluster(t)
	kind.RequireEnforcingCNI(t, c)
}

// spec: 6.2 (pod state machine — terminal Sandbox phases), 4.6.1 (warm
// pool controller)
// diagnosis: the per-run debris prune left objects it had selected behind.
// §6.2 makes `failed` and `terminated` terminal, so those objects serve no
// session and will never serve another, but they stay in etcd and in every
// controller's informer cache. Unbounded accumulation is what OOM-kills the
// WarmPoolController against its chart memory limit, which in turn stops the
// controller that would have released them. A failure here means the prune
// selected objects and could not remove them: either the API server rejected
// the deletes, or the §4.6.1 `lenny.dev/session-cleanup` finalizer is being
// re-added faster than the prune clears it. The assertion is on the prune's
// own selection rather than on a fresh listing, because a pool that is
// churning retires more pods while the prune runs and those objects age into
// the debris window moments later; treating them as prune failures would
// fail this test on every busy cluster. Set LENNY_KIND_SKIP_PRUNE=1 to
// inspect the objects before they are removed.
func TestTerminalSandboxDebrisIsPruned(t *testing.T) {
	c := kind.InstallLenny(t)

	// InstallLenny prunes once per test process. This pass re-runs it with
	// its own cutoff so the assertion reads the selection and the outcome
	// from the same instant.
	res, err := kind.PruneTerminalSandboxes(c, time.Now())
	if err != nil {
		t.Fatalf("prune aged terminal Sandboxes: %v", err)
	}
	t.Logf("prune selected %d aged terminal-phase Sandbox object(s)", len(res.Selected))
	if len(res.Survivors) > 0 {
		sample := res.Survivors
		if len(sample) > 10 {
			sample = sample[:10]
		}
		t.Errorf("%d of %d selected terminal-phase Sandbox object(s) survived the prune "+
			"(first %d: %v); accumulated Sandbox objects grow the controller informer "+
			"caches until the controller is OOMKilled against its chart memory limit",
			len(res.Survivors), len(res.Selected), len(sample), sample)
	}
}
