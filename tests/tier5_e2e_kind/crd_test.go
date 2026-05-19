// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind tests for the CRD plane the §13.6 warm-pool and
// sandbox-claim behaviours rest on. Each test installs the Lenny
// control plane on a Kind cluster via the install.sh-backed
// kind.InstallLenny harness.
//
// The full §13.6 critical path — a pool scaling to its warm floor,
// the Warm Pool Controller assigning a real pod to a claim — needs a
// running agent-pod workload, which the control-plane-only dev-mode
// install does not provide. Where a test cannot drive that workload it
// asserts the CRD and admission contract the behaviour depends on and
// records a precise skip for the workload-dependent half.

package tier5_e2e_kind_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// assertCRDEstablished fails when the named CustomResourceDefinition
// is absent or its Established condition is not "True". An
// un-Established CRD cannot serve its custom resources.
func assertCRDEstablished(t *testing.T, c *kind.Cluster, crd string) {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"get", "crd", crd,
		"-o", "jsonpath={range .status.conditions[?(@.type==\"Established\")]}{.status}{end}",
	)
	if err != nil {
		t.Fatalf("the %s CRD is not installed: %v\n%s", crd, err, out)
	}
	if strings.TrimSpace(out) != "True" {
		t.Fatalf("the %s CRD is not Established (condition status %q)", crd, strings.TrimSpace(out))
	}
}

// spec: 13.6
// diagnosis: the §13.6 warm-pool CRD plane is not installed. The test
// asserts the sandboxwarmpools.lenny.dev and sandboxtemplates.lenny.dev
// CRDs are Established and that the lenny-pool-config-validator
// admission webhook gating warm-pool writes is scoped to both
// resources. Pool scaling to a warm floor is exercised by the Warm
// Pool Controller against live agent pods; that workload is not part
// of the control-plane-only dev install, so the running-pool half is
// covered by the tier-2/3 component suites.
func TestWarmPool(t *testing.T) {
	c := kind.InstallLenny(t)

	assertCRDEstablished(t, c, "sandboxwarmpools.lenny.dev")
	assertCRDEstablished(t, c, "sandboxtemplates.lenny.dev")

	// The pool-config-validator webhook is the §4.6.3 admission gate
	// for warm-pool writes; it must cover both warm pools and the
	// templates they reference.
	rule := webhookRule(t, c, "lenny-pool-config-validator")
	for _, res := range []string{"sandboxwarmpools", "sandboxtemplates"} {
		if !rule.coversResource(res) {
			t.Errorf("lenny-pool-config-validator does not gate the %q resource (scope: %v)", res, rule.resources)
		}
	}
	t.Logf("warm-pool CRD plane established; pool-config-validator scope: %v", rule.resources)
}

// spec: 13.6
// diagnosis: the §13.6 sandbox-claim CRD plane is not installed. The
// test asserts the sandboxclaims.lenny.dev CRD is Established and that
// the lenny-sandboxclaim-guard admission webhook — the §4.6.1 ADR-007
// gate that rejects duplicate claims on CREATE and stale writes on
// UPDATE — is installed, fail-closed, and scoped to the sandboxclaims
// resource. The concurrent-claim no-double-assignment behaviour runs
// against the live Warm Pool Controller in the tier-2/8 suites; that
// workload is not part of the dev-mode control-plane install.
func TestSandboxClaim(t *testing.T) {
	c := kind.InstallLenny(t)

	assertCRDEstablished(t, c, "sandboxclaims.lenny.dev")

	out, err := c.KubectlOut(
		t,
		"get", "validatingwebhookconfiguration", "lenny-sandboxclaim-guard",
		"-o", "jsonpath={.webhooks[0].failurePolicy}|{.webhooks[0].rules[0].resources[0]}",
	)
	if err != nil {
		t.Fatalf("the §4.6.1 lenny-sandboxclaim-guard webhook is not installed: %v\n%s", err, out)
	}
	failurePolicy, resource, ok := strings.Cut(out, "|")
	if !ok {
		t.Fatalf("unexpected jsonpath output for lenny-sandboxclaim-guard: %q", out)
	}
	if failurePolicy != "Fail" {
		t.Errorf("lenny-sandboxclaim-guard has failurePolicy %q; ADR-007 requires fail-closed (Fail)", failurePolicy)
	}
	if resource != "sandboxclaims" {
		t.Errorf("lenny-sandboxclaim-guard is scoped to resource %q; it must gate sandboxclaims", resource)
	}
	t.Logf("sandbox-claim CRD plane established; sandboxclaim-guard failurePolicy=%s", failurePolicy)
}

// spec: 13.6
// diagnosis: the §13.6 pool-upgrade path cannot be admitted. A pool
// upgrade is an UPDATE to a SandboxWarmPool or SandboxTemplate spec;
// the lenny-pool-config-validator webhook must gate that UPDATE so an
// upgrade cannot move the pool into an invalid budget. The test
// confirms the webhook intercepts the UPDATE operation on both
// resources. Rolling the warm pods through a template change is
// performed by the live Warm Pool Controller, which the dev-mode
// control-plane install does not run; that rollout is covered by the
// tier-2/3 component suites.
func TestPoolUpgrade(t *testing.T) {
	c := kind.InstallLenny(t)

	rule := webhookRule(t, c, "lenny-pool-config-validator")
	if !rule.coversOperation("UPDATE") {
		t.Fatalf("lenny-pool-config-validator does not intercept UPDATE; a pool upgrade could move "+
			"the pool into an invalid §4.6.3 budget unchecked (operations: %v)", rule.operations)
	}
	t.Logf("pool-upgrade admission gate present; pool-config-validator operations: %v", rule.operations)
}
