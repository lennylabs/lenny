// SPDX-License-Identifier: MIT

//go:build chaos

// Shared helpers for the tier-8 chaos tests that drive the live Lenny
// control plane on the Kind cluster. The helpers wrap kubectl reads
// (Lease holder identity, Lease renew timestamps, Deployment readiness,
// Service endpoint counts) and the poll loop the chaos assertions use
// to wait for recovery within a documented bound.

package tier8_chaos_test

import (
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// lennySystemNamespace is the namespace the Lenny control plane runs in.
const lennySystemNamespace = "lenny-system"

// pollUntil polls cond every interval until it returns true or timeout
// elapses. It reports whether cond became true within the bound. The
// chaos tests use it to wait for failover or reschedule recovery.
func pollUntil(timeout, interval time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(interval)
	}
}

// leaseHolderIdentity returns the .spec.holderIdentity of the named
// coordination.k8s.io/v1 Lease in lenny-system. An empty string means
// the Lease is absent or currently unheld.
func leaseHolderIdentity(t *testing.T, c *kind.Cluster, lease string) string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "get", "lease", lease,
		"-o", "jsonpath={.spec.holderIdentity}",
	)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// leaseRenewTime returns the .spec.renewTime of the named Lease parsed
// as a time.Time. A zero time means the Lease is absent or carries no
// renew timestamp.
func leaseRenewTime(t *testing.T, c *kind.Cluster, lease string) time.Time {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "get", "lease", lease,
		"-o", "jsonpath={.spec.renewTime}",
	)
	if err != nil {
		return time.Time{}
	}
	raw := strings.TrimSpace(out)
	if raw == "" {
		return time.Time{}
	}
	// The Lease renewTime is a microsecond-precision RFC 3339 timestamp
	// (for example 2026-05-19T05:23:27.459709Z). RFC3339Nano parses the
	// fractional seconds without a fixed-width assumption.
	ts, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return ts
}

// leaseHolderPod extracts the pod name from a leader-election Lease
// holderIdentity. client-go formats the identity as
// "<pod-name>_<uuid>"; callers need the pod name to delete the leader.
// When the identity carries no underscore the whole string is returned.
func leaseHolderPod(holderIdentity string) string {
	if i := strings.LastIndex(holderIdentity, "_"); i >= 0 {
		return holderIdentity[:i]
	}
	return holderIdentity
}

// deploymentReady reports whether the named Deployment in lenny-system
// has its full desired replica count Ready. A read error or an
// unparseable status yields false so callers keep polling.
func deploymentReady(t *testing.T, c *kind.Cluster, deployment string) bool {
	t.Helper()
	// .status.readyReplicas is absent until at least one replica is
	// ready; the spec/status pair with a literal fallback keeps the
	// jsonpath total well-formed in that window.
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "get", "deployment", deployment,
		"-o", "jsonpath={.spec.replicas}/{.status.readyReplicas}",
	)
	if err != nil {
		return false
	}
	desired, ready, ok := strings.Cut(strings.TrimSpace(out), "/")
	if !ok || desired == "" {
		return false
	}
	if ready == "" {
		ready = "0"
	}
	return desired == ready
}

// deploymentReadyState returns the "<ready>/<desired>" replica string
// for the named Deployment, for diagnostic logging.
func deploymentReadyState(t *testing.T, c *kind.Cluster, deployment string) string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "get", "deployment", deployment,
		"-o", "jsonpath={.status.readyReplicas}/{.spec.replicas}",
	)
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(out)
}

// podNames returns the names of every pod in lenny-system matching the
// label selector. The chaos tests use it to count Deployment pods and
// to confirm a deleted pod was replaced by a new one.
func podNames(t *testing.T, c *kind.Cluster, selector string) []string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "get", "pods",
		"-l", selector,
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\n\"}{end}",
	)
	if err != nil {
		return nil
	}
	return strings.Fields(out)
}

// endpointCount returns the number of ready endpoint addresses backing
// the named Service in lenny-system. The chaos tests assert a Service
// keeps a non-zero endpoint set while one of its pods is rescheduled.
func endpointCount(t *testing.T, c *kind.Cluster, service string) int {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "get", "endpoints", service,
		"-o", "jsonpath={range .subsets[*].addresses[*]}{.ip}{\"\\n\"}{end}",
	)
	if err != nil {
		return 0
	}
	return len(strings.Fields(out))
}
