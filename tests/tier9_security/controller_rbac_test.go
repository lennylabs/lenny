// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 live RBAC assertion for the §4.6.3 controller grant that backs
// the §4.7 nonce-only carrier write. The WarmPoolController records the
// §4.7 nonce-only render decision on the WarmPoolController-owned
// Sandbox.spec.requireSoPeercred carrier through a Server-Side Apply
// (recordNonceOnlyCarrier / clearNonceOnlyCarrier in
// pkg/controller/sandbox/controller.go). SSA issues an HTTP PATCH, so
// the write requires the `patch` verb on the `sandboxes` main resource.
// The §4.6.3 grant historically omitted `patch`; proposal 0008 adds it.
//
// This file asserts the verb is allowed against the chart-installed
// RBAC, not only the rendered chart template (the tier-1 guard in
// tests/tier1_unit/rbac/rbac_test.go covers the rendered template). It
// runs a live SubjectAccessReview through `kubectl auth can-i ... --as`
// for the controller ServiceAccount in an agent namespace and asserts
// the verb is allowed; a denial means the §4.7 carrier write is
// forbidden and the §16.5 PoolSecurityDegraded alert can never fire for
// a pool running with SO_PEERCRED disabled (a fail-open
// security-surfacing defect).
//
// The check shells out through the kind.Cluster handle's kubectl, the
// same live-cluster access path every other tier-9 file uses; there is
// no client-go RESTConfig wired into the harness, and `kubectl auth
// can-i --as` issues a SubjectAccessReview to the apiserver, so this is
// a live authorization decision against the installed RBAC rather than a
// chart-template assertion.

package tier9_security_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// controllerDeploymentName is the lenny-system Deployment whose
// ServiceAccount (system:serviceaccount:lenny-system:lenny-controller)
// holds the §4.6.3 controller grants. The test gates on this Deployment
// being Ready so the chart-installed RBAC (including the S1 `patch`
// grant) is present before it asserts the verb.
const controllerDeploymentName = "lenny-controller"

// controllerServiceAccountUser is the fully-qualified ServiceAccount
// username the SubjectAccessReview impersonates. It is the subject the
// §4.6.3 ClusterRoleBinding binds the controller grants to.
const controllerServiceAccountUser = "system:serviceaccount:lenny-system:lenny-controller"

// controllerSandboxAccess is one `kubectl auth can-i <verb>
// sandboxes.lenny.dev` assertion for the controller ServiceAccount in an
// agent namespace: the verb under test and whether the §4.6.3 grant must
// allow it. The positive `patch` case is the §4.7 carrier-write gate
// proposal 0008 adds; the `update` and `delete` controls confirm the
// rest of the main-resource grant is intact, so a regression that
// narrowed the rule to drop `patch` alone is distinguishable from a rule
// that lost every write verb.
type controllerSandboxAccess struct {
	name    string
	verb    string
	allowed bool
}

// spec: 4.6.3 (controller patch grant on Sandbox), 4.7 (nonce-only carrier SSA write)
// diagnosis: the chart-installed §4.6.3 controller RBAC does not allow
// the WarmPoolController ServiceAccount to `patch sandboxes.lenny.dev`
// in an agent namespace. The §4.7 nonce-only render decision is written
// to the WarmPoolController-owned Sandbox.spec.requireSoPeercred carrier
// through a Server-Side Apply, which issues an HTTP PATCH; without the
// `patch` verb that carrier write is forbidden, so the WarmPoolController
// never surfaces SecurityDegradedMode and the §16.5 PoolSecurityDegraded
// alert never has a live series. A pool then runs with the SO_PEERCRED
// boundary disabled and operators receive no signal. A failure here
// means the S1 chart grant was not deployed or a later edit narrowed the
// controller `sandboxes` rule.
func TestControllerCanPatchSandboxesLiveRBAC(t *testing.T) {
	c := kind.InstallLenny(t)
	if !deploymentReadyT9(t, c, controllerDeploymentName) {
		t.Skipf("precondition not met: %s is not Ready; the §4.6.3 controller RBAC ships with that Deployment",
			controllerDeploymentName)
	}

	cases := []controllerSandboxAccess{
		{
			// §4.6.3 / §4.7: the carrier write proposal 0008 unblocks. SSA
			// issues an HTTP PATCH, so the write requires `patch` on the
			// `sandboxes` main resource; `update` is insufficient for an SSA
			// Apply request.
			name:    "patch-sandboxes-allowed",
			verb:    "patch",
			allowed: true,
		},
		{
			// §4.6.3 positive control: the controller creates member
			// Sandboxes from templates, so `update` on the main resource is
			// granted independent of the §4.7 `patch` addition.
			name:    "update-sandboxes-allowed",
			verb:    "update",
			allowed: true,
		},
		{
			// §4.6.3 positive control: the controller deletes member
			// Sandboxes on pool drain; `delete` is part of the main-resource
			// grant.
			name:    "delete-sandboxes-allowed",
			verb:    "delete",
			allowed: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			allowed, raw := controllerCanIPatchSandbox(t, c, tc.verb)
			if allowed != tc.allowed {
				t.Fatalf("§4.6.3/§4.7 violation: `kubectl auth can-i %s sandboxes.lenny.dev -n %s "+
					"--as=%s` returned %q (allowed=%t), want allowed=%t. A denied `patch` means the "+
					"§4.7 nonce-only Sandbox.spec.requireSoPeercred carrier SSA write is forbidden and the "+
					"PoolSecurityDegraded alert cannot fire; deploy the proposal 0008 chart grant (S1) and "+
					"re-run.",
					tc.verb, agentNamespace, controllerServiceAccountUser, raw, allowed, tc.allowed)
			}
			t.Logf("§4.6.3/§4.7: controller ServiceAccount %s can-i %s sandboxes.lenny.dev in %s = %q",
				controllerServiceAccountUser, tc.verb, agentNamespace, raw)
		})
	}
}

// controllerCanIPatchSandbox runs `kubectl auth can-i <verb>
// sandboxes.lenny.dev -n <agent-ns> --as <controller-sa>` against the
// live cluster and reports whether the apiserver allowed the access.
// `kubectl auth can-i --as` issues a SubjectAccessReview, so the verdict
// reflects the installed RBAC rather than a rendered chart template.
//
// The command exits 0 and prints "yes" when allowed, exits non-zero and
// prints "no" when denied. Because a denial is a non-zero exit (not a
// harness error), the function reads the trimmed stdout marker rather
// than the exit code: "yes" maps to allowed, anything else (including
// "no") maps to denied. The raw output is returned for the failure
// message.
func controllerCanIPatchSandbox(t *testing.T, c *kind.Cluster, verb string) (allowed bool, raw string) {
	t.Helper()
	out, _ := c.KubectlOut(
		t,
		"auth", "can-i", verb, "sandboxes.lenny.dev",
		"-n", agentNamespace,
		"--as", controllerServiceAccountUser,
	)
	raw = strings.TrimSpace(out)
	return raw == "yes", raw
}
