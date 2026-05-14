// SPDX-License-Identifier: MIT

// Package kind is the testinfra harness for the tier-5 e2e suite.
// Every tier-5 test calls SkipUnlessAvailable to short-circuit when
// the host has no Docker daemon or `kind` binary; the e2e tests run
// only on workstations and CI runners with those tools installed.
//
// The harness shape will evolve to bring up a real Kind cluster and
// install the chart once the chart + admission webhook binaries
// land. Today it provides the skip plumbing and a stable place for
// the cluster bring-up to land.
package kind

import (
	"os/exec"
	"testing"
)

// SkipUnlessAvailable skips the test when the host cannot run Kind
// e2e suites (docker missing, kind missing, or docker daemon
// unreachable). Returns a Cluster handle when the prerequisites are
// in place; today this is a placeholder type until the cluster
// bring-up logic lands.
func SkipUnlessAvailable(t testing.TB) *Cluster {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker not on PATH: %v", err)
	}
	if _, err := exec.LookPath("kind"); err != nil {
		t.Skipf("kind not on PATH: %v", err)
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker daemon not reachable: %v", err)
	}
	// Placeholder: future revisions bring up a Kind cluster, install
	// cert-manager + the Lenny chart, and return a populated Cluster.
	t.Skip("kind cluster bring-up not yet implemented: ships with the Helm chart + admission webhook binaries")
	return &Cluster{}
}

// Cluster is the handle the bring-up logic will return. Empty for now.
type Cluster struct{}
