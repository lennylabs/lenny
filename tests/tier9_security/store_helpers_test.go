// SPDX-License-Identifier: MIT

//go:build security

// Shared helpers for the tier-9 security tests that read or seed the
// e2e Kind cluster's real in-cluster data stores. The e2e cluster runs
// single-replica Postgres, Redis, and MinIO Deployments in
// lenny-system; the gateway persists to them. These helpers wrap a
// one-shot psql pod (connected straight to the Postgres pod IP, since
// the lenny-system default-deny NetworkPolicy denies a throwaway pod's
// egress to CoreDNS) and the Deployment-readiness reads the tests gate
// their preconditions on. They mirror the equivalent tier-8 chaos
// helpers without taking a cross-build-tag dependency on that package.

package tier9_security_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// deploymentReadyT9 reports whether the named lenny-system Deployment
// has its full desired replica count Ready. A read error or an
// unparseable status yields false so callers skip rather than fail.
func deploymentReadyT9(t *testing.T, c *kind.Cluster, deployment string) bool {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNS, "get", "deployment", deployment,
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

// dataStorePodIPT9 returns the pod IP of the named e2e data store
// (postgres, redis, or minio). The test process runs kubectl from
// outside the cluster, so it resolves the pod IP directly without
// needing in-cluster DNS. An empty result means the store pod is
// absent or not yet assigned an IP.
func dataStorePodIPT9(t *testing.T, c *kind.Cluster, store string) string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNS, "get", "pod",
		"-l", "lenny.dev/e2e-datastore="+store,
		"-o", "jsonpath={.items[0].status.podIP}",
	)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// runPsqlExec runs a SQL script against the e2e Postgres via a one-shot
// postgres:16.4 pod connected to pgIP. The pod connects to the pod IP
// rather than the Service DNS name because the lenny-system
// default-deny NetworkPolicy denies a throwaway pod's egress to
// CoreDNS. ON_ERROR_STOP makes any failed statement a non-zero exit,
// which fails the test — a seed or cleanup that did not apply is not a
// recoverable condition.
func runPsqlExec(t *testing.T, c *kind.Cluster, pgIP, podName, sql string) {
	t.Helper()
	_, _ = c.KubectlOut(t, "-n", lennySystemNS, "delete", "pod", podName,
		"--ignore-not-found", "--wait=true")
	if out, err := c.KubectlOut(
		t,
		"-n", lennySystemNS, "run", podName,
		"--image=postgres:16.4", "--image-pull-policy=IfNotPresent",
		"--restart=Never", "--attach", "--rm", "--quiet",
		"--env=PGPASSWORD=lenny",
		"--command", "--",
		"psql", "-h", pgIP, "-U", "lenny", "-d", "lenny",
		"-v", "ON_ERROR_STOP=1", "-c", sql,
	); err != nil {
		t.Fatalf("psql script %q failed: %v\n%s", podName, err, out)
	}
}

// runPsqlQuery runs a read-only SQL query against the e2e Postgres via
// a one-shot psql pod and returns the rows as raw lines (psql -tA:
// tuples-only, unaligned — one value per line). A query failure fails
// the test.
func runPsqlQuery(t *testing.T, c *kind.Cluster, pgIP, podName, sql string) string {
	t.Helper()
	_, _ = c.KubectlOut(t, "-n", lennySystemNS, "delete", "pod", podName,
		"--ignore-not-found", "--wait=true")
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNS, "run", podName,
		"--image=postgres:16.4", "--image-pull-policy=IfNotPresent",
		"--restart=Never", "--attach", "--rm", "--quiet",
		"--env=PGPASSWORD=lenny",
		"--command", "--",
		"psql", "-h", pgIP, "-U", "lenny", "-d", "lenny",
		"-tA", "-v", "ON_ERROR_STOP=1", "-c", sql,
	)
	if err != nil {
		t.Fatalf("psql query %q failed: %v\n%s", podName, err, out)
	}
	return out
}
