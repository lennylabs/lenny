// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind tests for the §13.7 admission plane. Each test
// installs the Lenny control plane on a Kind cluster (via the
// install.sh-backed kind.InstallLenny harness) and asserts the named
// §13.x admission behaviour against the live API server.
//
// The pod-rejection tests apply manifests with `kubectl apply
// --dry-run=server`. A server-side dry-run still runs every admission
// webhook, so a rejecting webhook denies the request, but nothing is
// persisted, which keeps the agent namespaces clean across runs. The
// pool-config tests apply for real and clean up with t.Cleanup,
// because a rejected apply never creates an object and an admitted
// one is removed by the cleanup.

package tier5_e2e_kind_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// The admission webhooks are scoped by a namespaceSelector matching
// lenny.dev/agent-namespace=true; the chart applies that label to the
// lenny-agents namespace, so the manifests below target lenny-agents
// to bring each webhook into scope.

// expectedAgentWebhooks names the ValidatingWebhookConfigurations the
// chart installs unconditionally in the Phase 3.5 baseline (the
// feature-gated cosign, llm-proxy, drain-readiness, and compliance
// webhooks are not in this set because the e2e values overlay leaves
// those features off). Every webhook here must exist and be
// failurePolicy: Fail.
var expectedAgentWebhooks = []string{
	"lenny-label-immutability",
	"lenny-sandboxclaim-guard",
	"lenny-pool-config-validator",
	"lenny-ephemeral-container-cred-guard",
	"lenny-pod-security",
}

// spec: 13.7
// diagnosis: §13.7 admission policy is not enforced. The test applies
// a SandboxWarmPool whose spec.minWarm exceeds spec.maxWarm — a §4.6.3
// semantic budget violation — and expects the API server to reject it
// via the lenny-pool-config-validator webhook with the
// INVALID_POOL_CONFIGURATION reason. A failure means the webhook
// admitted an invalid pool configuration, the webhook Deployment is
// down, or the ValidatingWebhookConfiguration is not installed.
func TestAdmissionPolicy(t *testing.T) {
	c := kind.InstallLenny(t)

	// A SandboxWarmPool with minWarm > maxWarm. Both fields satisfy the
	// CRD's `minimum: 0` OpenAPI bound, so the request reaches the
	// webhook rather than being rejected by schema validation first.
	const badPool = `apiVersion: lenny.dev/v1alpha1
kind: SandboxWarmPool
metadata:
  name: e2e-admission-badpool
  namespace: lenny-agents
spec:
  templateRef: e2e-nonexistent-template
  minWarm: 5
  maxWarm: 2
`
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, badPool) })

	out, err := c.ApplyStdin(t, badPool)
	if err == nil {
		t.Fatalf("API server admitted a SandboxWarmPool with minWarm=5 > maxWarm=2; "+
			"the lenny-pool-config-validator webhook did not reject it.\noutput:\n%s", out)
	}
	if !strings.Contains(out, "pool-config-validator.lenny.dev") {
		t.Fatalf("rejection did not come from the pool-config-validator webhook.\noutput:\n%s", out)
	}
	if !strings.Contains(out, "INVALID_POOL_CONFIGURATION") {
		t.Fatalf("rejection lacks the §4.6.3 INVALID_POOL_CONFIGURATION reason code.\noutput:\n%s", out)
	}
	t.Logf("pool-config-validator rejected the invalid warm pool: %s", strings.TrimSpace(out))
}

// spec: 13.1
// diagnosis: §13.1 pod-security admission is not enforced. The test
// applies an agent-namespace pod whose spec omits the §13.1 hardened
// securityContext (no fsGroup, no runAsNonRoot, a container with no
// capability drop and no seccomp profile) and expects the
// lenny-pod-security webhook to reject it. The apply is a server-side
// dry-run so the webhook still runs but nothing persists. A failure
// means the webhook admitted an unhardened pod or is not installed.
func TestAdmissionPodSecurity(t *testing.T) {
	c := kind.InstallLenny(t)

	// A pod missing every §13.1 securityContext field. The
	// lenny-pod-security webhook is scoped to Pod CREATE in agent
	// namespaces, so this is in scope.
	const badPod = `apiVersion: v1
kind: Pod
metadata:
  name: e2e-admission-badpod
  namespace: lenny-agents
spec:
  containers:
    - name: agent
      image: busybox:1.36
`
	out, err := dryRunApply(t, c, badPod)
	if err == nil {
		t.Fatalf("API server admitted an agent pod with no §13.1 securityContext; "+
			"the lenny-pod-security webhook did not reject it.\noutput:\n%s", out)
	}
	if !strings.Contains(out, "pod-security.lenny.dev") {
		t.Fatalf("rejection did not come from the pod-security webhook.\noutput:\n%s", out)
	}
	// The webhook surfaces the §13.1 validator's chained violations.
	for _, want := range []string{
		"POD_SPEC_CRED_FSGROUP_MISSING",
		"runAsNonRoot must be true",
		"capabilities.drop must contain ALL",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("pod-security rejection is missing the §13.1 violation %q.\noutput:\n%s", want, out)
		}
	}
	t.Logf("pod-security rejected the unhardened pod: %s", strings.TrimSpace(out))
}

// spec: 13.1
// diagnosis: the §13.1 pod-security webhook is over-broad and rejects
// a compliant pod. The test applies a pod that sets every §13.1
// securityContext field correctly (runAsNonRoot, the lenny-cred-readers
// fsGroup 65534, RuntimeDefault seccomp, a container that drops ALL
// capabilities and sets a read-only root filesystem) and expects the
// API server to admit it. This is the positive control for
// TestAdmissionPodSecurity: a failure means the webhook rejects a
// spec that satisfies §13.1.
func TestAdmissionPodSecurityAdmitsCompliantPod(t *testing.T) {
	c := kind.InstallLenny(t)

	// A pod that satisfies every §13.1 invariant. fsGroup 65534 is the
	// lenny-cred-readers GID from pkg/controller/sandbox/podspec.
	const goodPod = `apiVersion: v1
kind: Pod
metadata:
  name: e2e-admission-goodpod
  namespace: lenny-agents
spec:
  securityContext:
    runAsNonRoot: true
    fsGroup: 65534
    seccompProfile:
      type: RuntimeDefault
  containers:
    - name: agent
      image: busybox:1.36
      securityContext:
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        capabilities:
          drop: ["ALL"]
`
	out, err := dryRunApply(t, c, goodPod)
	if err != nil {
		t.Fatalf("API server rejected a §13.1-compliant agent pod; "+
			"the lenny-pod-security webhook is over-broad.\noutput:\n%s", out)
	}
	t.Logf("pod-security admitted the compliant pod: %s", strings.TrimSpace(out))
}

// spec: 13.7
// diagnosis: the §13.7 admission inventory is incomplete. The test
// lists every ValidatingWebhookConfiguration on the cluster and
// asserts that each Phase 3.5 baseline webhook is present and
// configured fail-closed (failurePolicy: Fail). A missing webhook
// leaves an admission gate open; a webhook with failurePolicy: Ignore
// would admit writes during a webhook outage.
func TestAdmissionInventory(t *testing.T) {
	c := kind.InstallLenny(t)

	out, err := c.KubectlOut(
		t,
		"get", "validatingwebhookconfigurations",
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\"=\"}{.webhooks[0].failurePolicy}{\"\\n\"}{end}",
	)
	if err != nil {
		t.Fatalf("list ValidatingWebhookConfigurations: %v\n%s", err, out)
	}

	policy := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		name, fp, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		policy[name] = fp
	}

	for _, name := range expectedAgentWebhooks {
		fp, present := policy[name]
		if !present {
			t.Errorf("ValidatingWebhookConfiguration %q is not installed; a §13.7 admission gate is missing", name)
			continue
		}
		if fp != "Fail" {
			t.Errorf("webhook %q has failurePolicy %q; §13.7 requires fail-closed (Fail)", name, fp)
		}
	}
	t.Logf("admission inventory: %d ValidatingWebhookConfigurations present", len(policy))
}

// spec: 13.7
// diagnosis: the §17.2 lenny-label-immutability webhook does not
// enforce the immutable agent-pod labels. The test confirms the
// webhook's ValidatingWebhookConfiguration is installed, scoped to Pod
// CREATE and UPDATE, and fail-closed. The label-mutation rejection
// itself fires on UPDATE of an existing agent pod; a live agent pod is
// not present in the control-plane-only dev install, so this test
// asserts the webhook configuration rather than driving an UPDATE.
// TestAdmissionPodSecurity already proves the webhook plane rejects
// non-compliant pod writes end to end.
func TestLabelImmutability(t *testing.T) {
	c := kind.InstallLenny(t)

	const webhook = "lenny-label-immutability"
	rule := webhookRule(t, c, webhook)

	if rule.failurePolicy != "Fail" {
		t.Errorf("%s has failurePolicy %q; §17.2 requires fail-closed (Fail)", webhook, rule.failurePolicy)
	}
	if !rule.coversResource("pods") {
		t.Errorf("%s is scoped to resources %v; the label-immutability rule must cover pods", webhook, rule.resources)
	}
	for _, op := range []string{"CREATE", "UPDATE"} {
		if !rule.coversOperation(op) {
			t.Errorf("%s does not intercept the %s operation; label immutability needs CREATE and UPDATE", webhook, op)
		}
	}
	t.Logf("%s webhook: failurePolicy=%s resources=%v operations=%v",
		webhook, rule.failurePolicy, rule.resources, rule.operations)
}

// spec: 13.2 (admission-webhook NetworkPolicy egress sub-rule (c)),
// 17.2 (deletion-guard HA + additive webhook-name label),
// 10.5 (runtime-upgrade probe on SandboxTemplate DELETE)
// diagnosis: the §13.2 allow-deletion-guard-gateway-egress NetworkPolicy
// is missing or the deletion-guard's gateway probe hop is still blocked
// by the lenny-system default-deny. The test creates a SandboxTemplate
// and a referencing SandboxWarmPool (no active RuntimeUpgrade), then
// deletes the template. The fail-closed lenny-sandboxtemplate-deletion-guard
// webhook lists the referencing pool and probes
// GET /internal/runtime-upgrade/active on the gateway internal port; a
// failure means that probe egress is dropped, so the DELETE times out and
// the webhook denies it with `context deadline exceeded` / a 503 instead
// of allowing the deletion. Negative-space check: the template must have a
// referencing pool, because a template with zero referencing pools is
// Allowed without any gateway hop and would not exercise the restored
// egress path.
func TestSandboxTemplateDeleteReachesGatewayProbe(t *testing.T) {
	c := kind.InstallLenny(t)

	// A §4.9-compliant SandboxTemplate (deliveryMode proxy with the
	// default spiffeBinding, isolationProfile sandboxed) so the
	// unconditionally-rendered lenny-direct-mode-isolation webhook admits
	// the CREATE. This is a real apply: the template must persist so the
	// DELETE below drives the deletion-guard.
	const template = `apiVersion: lenny.dev/v1alpha1
kind: SandboxTemplate
metadata:
  name: e2e-deletion-guard-template
  namespace: lenny-agents
spec:
  runtimeRef: e2e-nonexistent-runtime
  deliveryMode: proxy
  isolationProfile: sandboxed
`

	// A SandboxWarmPool whose spec.templateRef names the template above,
	// so the deletion-guard's decider finds a referencing pool and takes
	// the gateway-probe branch rather than the zero-pool Allow shortcut.
	// minWarm 0 keeps the poolscaling controller from provisioning agent
	// pods, and minWarm <= maxWarm satisfies the pool-config-validator.
	const pool = `apiVersion: lenny.dev/v1alpha1
kind: SandboxWarmPool
metadata:
  name: e2e-deletion-guard-pool
  namespace: lenny-agents
spec:
  templateRef: e2e-deletion-guard-template
  minWarm: 0
  maxWarm: 1
`

	// Clean up the pool and template regardless of the outcome. The pool
	// is removed first so its later teardown cannot re-arm the guard, and
	// both use --ignore-not-found so a successful in-test delete is not a
	// cleanup error.
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, pool) })
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, template) })

	// The lenny-pool-config-validator webhook admits SandboxTemplate and
	// SandboxWarmPool spec writes only from the PoolScalingController
	// service account (Postgres-authoritative pool config). Impersonate
	// that principal to seat the fixtures, as the controller does at
	// runtime.
	if out, err := applyAsPoolScalingController(t, c, template); err != nil {
		t.Fatalf("API server rejected the §4.9-compliant SandboxTemplate fixture: %v\noutput:\n%s", err, out)
	}
	if out, err := applyAsPoolScalingController(t, c, pool); err != nil {
		t.Fatalf("API server rejected the referencing SandboxWarmPool fixture: %v\noutput:\n%s", err, out)
	}

	// No RuntimeUpgrade is created, so the gateway
	// GET /internal/runtime-upgrade/active probe reports no active upgrade
	// for the referencing pool and the guard admits the delete — provided
	// the probe hop reaches the gateway. Deleting the template drives the
	// DELETE through the fail-closed deletion-guard.
	out, err := c.KubectlOut(t, "delete", "sandboxtemplate", "e2e-deletion-guard-template", "-n", "lenny-agents")
	if err != nil {
		// A dropped probe egress surfaces as a webhook call timeout. Name
		// the two signatures so a failure points at the missing NetworkPolicy
		// rather than an unrelated delete error.
		for _, blocked := range []string{"context deadline exceeded", "deadline", "timeout", "503"} {
			if strings.Contains(out, blocked) {
				t.Fatalf("SandboxTemplate DELETE was denied by the fail-closed deletion-guard: the gateway "+
					"runtime-upgrade probe hop is blocked (allow-deletion-guard-gateway-egress missing or the "+
					"lenny-system default-deny still drops it).\noutput:\n%s", out)
			}
		}
		t.Fatalf("kubectl delete sandboxtemplate failed: %v\noutput:\n%s", err, out)
	}
	// The webhook must have been the one to admit the delete: confirm the
	// object is gone.
	if !strings.Contains(out, "deleted") {
		t.Fatalf("delete did not report the SandboxTemplate as deleted; the guard may not have run.\noutput:\n%s", out)
	}
	t.Logf("deletion-guard admitted the SandboxTemplate DELETE through the restored gateway probe path: %s",
		strings.TrimSpace(out))
}

// webhookRuleInfo holds the first rule of a ValidatingWebhookConfiguration
// together with the configuration's failurePolicy.
type webhookRuleInfo struct {
	failurePolicy string
	resources     []string
	operations    []string
}

// coversResource reports whether the rule's resources list contains
// the named resource.
func (r webhookRuleInfo) coversResource(name string) bool {
	return containsValue(r.resources, name)
}

// coversOperation reports whether the rule's operations list contains
// the named operation.
func (r webhookRuleInfo) coversOperation(name string) bool {
	return containsValue(r.operations, name)
}

// webhookRule reads the failurePolicy and the first admission rule
// (its resources and operations) of the named
// ValidatingWebhookConfiguration. Each field is read with its own
// jsonpath query that returns a JSON array literal: kubectl's jsonpath
// `range` over a flat string array does not bind the element, so the
// array-literal form is the reliable way to extract a scalar list.
func webhookRule(t *testing.T, c *kind.Cluster, webhook string) webhookRuleInfo {
	t.Helper()
	info := webhookRuleInfo{
		failurePolicy: jsonpathValue(t, c, webhook, "{.webhooks[0].failurePolicy}"),
	}
	info.resources = jsonArrayValues(jsonpathValue(t, c, webhook, "{.webhooks[0].rules[0].resources}"))
	info.operations = jsonArrayValues(jsonpathValue(t, c, webhook, "{.webhooks[0].rules[0].operations}"))
	return info
}

// jsonpathValue runs a single jsonpath query against the named
// ValidatingWebhookConfiguration and returns the trimmed output. A
// query error fails the test, since every caller depends on the
// webhook existing.
func jsonpathValue(t *testing.T, c *kind.Cluster, webhook, jsonpath string) string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"get", "validatingwebhookconfiguration", webhook,
		"-o", "jsonpath="+jsonpath,
	)
	if err != nil {
		t.Fatalf("query %s on webhook %q: %v\n%s", jsonpath, webhook, err, out)
	}
	return strings.TrimSpace(out)
}

// jsonArrayValues parses a kubectl jsonpath array-literal string such
// as `["CREATE","UPDATE"]` into its element slice. A non-array input
// (an empty string, a scalar) yields nil.
func jsonArrayValues(literal string) []string {
	literal = strings.TrimSpace(literal)
	if !strings.HasPrefix(literal, "[") || !strings.HasSuffix(literal, "]") {
		return nil
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(literal, "["), "]")
	if strings.TrimSpace(inner) == "" {
		return nil
	}
	var values []string
	for _, part := range strings.Split(inner, ",") {
		part = strings.TrimSpace(part)
		part = strings.Trim(part, `"`)
		if part != "" {
			values = append(values, part)
		}
	}
	return values
}

// containsValue reports whether values contains want.
func containsValue(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// dryRunApply applies manifest with `kubectl apply --dry-run=server`,
// which runs every admission webhook without persisting the object,
// and returns the combined output and error.
func dryRunApply(t *testing.T, c *kind.Cluster, manifest string) (string, error) {
	t.Helper()
	return c.DryRunApplyStdin(t, manifest)
}

// poolScalingControllerSA is the only principal the
// lenny-pool-config-validator webhook admits SandboxTemplate and
// SandboxWarmPool spec writes from; pool configuration is
// Postgres-authoritative and manual writes are rejected with
// UNAUTHORIZED_POOL_CONFIG_WRITE. It mirrors
// pkg/admission/label_immutability.PoolScalingControllerSA.
const poolScalingControllerSA = "system:serviceaccount:lenny-system:lenny-pool-scaling-controller"

// applyAsPoolScalingController applies manifest with `kubectl apply -f -`
// impersonating the PoolScalingController service account, so the
// lenny-pool-config-validator admits the write the way the controller's
// own reconcile does at runtime. It returns the combined output and
// error and does not fail the test itself, leaving the caller to assert.
func applyAsPoolScalingController(t *testing.T, c *kind.Cluster, manifest string) (string, error) {
	t.Helper()
	cmd := c.Kubectl("apply", "--as", poolScalingControllerSA, "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
