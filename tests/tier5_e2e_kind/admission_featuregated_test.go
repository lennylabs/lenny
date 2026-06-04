// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind tests for the feature-gated §13.x admission
// webhooks. The e2e values overlay (tests/testinfra/kind/e2e-values.yaml)
// sets features.llmProxy, features.drainReadiness, and
// features.compliance to true, so the chart renders the four
// feature-gated ValidatingWebhookConfigurations:
// lenny-direct-mode-isolation, lenny-drain-readiness,
// lenny-data-residency-validator, and lenny-t4-node-isolation. Each is
// installed with a 2-replica webhook Deployment in lenny-system.
//
// Each test asserts the webhook's deployed posture against the live
// cluster — present, failurePolicy: Fail, the §13.2 agent-namespace
// selector, a populated caBundle, the documented resource and
// operation scope — and exercises the webhook's rejection behaviour
// where the dev-mode install permits it.
//
// Two webhooks cannot have their rejection behaviour exercised on this
// install. lenny-direct-mode-isolation enforces only in multi-tenant,
// non-development mode; the overlay runs tenancy.mode: single with
// global.devMode: true, so the webhook admits every SandboxTemplate.
// lenny-data-residency-validator reads its declared regions from a
// --storage-regions flag the chart's _webhook.tpl does not pass, and
// the sandboxclaims CRD schema carries no dataResidencyRegion field, so
// no SandboxClaim can reach the webhook with a non-empty region. Both
// tests assert the deployed posture; their rejection half is covered
// by the tier-2/3 component suites against the same decision logic.

package tier5_e2e_kind_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// webhookConfigured reports whether the named
// ValidatingWebhookConfiguration exists on the cluster.
func webhookConfigured(t *testing.T, c *kind.Cluster, name string) bool {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"get", "validatingwebhookconfiguration", name,
		"--ignore-not-found", "-o", "jsonpath={.metadata.name}",
	)
	if err != nil {
		t.Fatalf("query ValidatingWebhookConfiguration %q: %v\n%s", name, err, out)
	}
	return strings.TrimSpace(out) == name
}

// assertFeatureGatedWebhookPresent fails when a feature-gated webhook
// the e2e overlay enabled is absent. flagDesc names the Helm value that
// gates the webhook so a missing webhook is actionable.
func assertFeatureGatedWebhookPresent(t *testing.T, c *kind.Cluster, webhook, flagDesc string) {
	t.Helper()
	if !webhookConfigured(t, c, webhook) {
		t.Fatalf("ValidatingWebhookConfiguration %q is not installed on a release that set %s; "+
			"the chart's feature-gating did not render a webhook it should have", webhook, flagDesc)
	}
}

// caBundlePopulated reports whether the named
// ValidatingWebhookConfiguration's first webhook carries a non-empty
// clientConfig.caBundle. A fail-closed webhook with an empty caBundle
// rejects every covered write, so a populated bundle is part of a
// healthy deployed posture.
func caBundlePopulated(t *testing.T, c *kind.Cluster, webhook string) bool {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"get", "validatingwebhookconfiguration", webhook,
		"-o", "jsonpath={.webhooks[0].clientConfig.caBundle}",
	)
	if err != nil {
		t.Fatalf("query caBundle on webhook %q: %v\n%s", webhook, err, out)
	}
	return strings.TrimSpace(out) != ""
}

// assertAgentNamespaceScoped fails when the webhook's first
// namespaceSelector matchExpressions does not gate the agent-namespace
// label (lenny.dev/agent-namespace In [true]). The chart's _webhook.tpl
// uses this selector on every feature-gated webhook so it fires only on
// the agent namespaces declared in §13.2; a selector missing the key or
// using the wrong operator would broaden the webhook to lenny-system
// and reject control-plane writes, or narrow it to nothing and skip the
// agent namespaces entirely.
func assertAgentNamespaceScoped(t *testing.T, c *kind.Cluster, webhook string) {
	t.Helper()
	key, err := c.KubectlOut(
		t,
		"get", "validatingwebhookconfiguration", webhook,
		"-o", "jsonpath={.webhooks[0].namespaceSelector.matchExpressions[0].key}",
	)
	if err != nil {
		t.Fatalf("query namespaceSelector.matchExpressions[0].key on webhook %q: %v\n%s", webhook, err, key)
	}
	op, err := c.KubectlOut(
		t,
		"get", "validatingwebhookconfiguration", webhook,
		"-o", "jsonpath={.webhooks[0].namespaceSelector.matchExpressions[0].operator}",
	)
	if err != nil {
		t.Fatalf("query namespaceSelector.matchExpressions[0].operator on webhook %q: %v\n%s", webhook, err, op)
	}
	values, err := c.KubectlOut(
		t,
		"get", "validatingwebhookconfiguration", webhook,
		"-o", "jsonpath={.webhooks[0].namespaceSelector.matchExpressions[0].values}",
	)
	if err != nil {
		t.Fatalf("query namespaceSelector.matchExpressions[0].values on webhook %q: %v\n%s", webhook, err, values)
	}
	const wantKey = "lenny.dev/agent-namespace"
	const wantOp = "In"
	const wantValue = "true"
	if strings.TrimSpace(key) != wantKey {
		t.Errorf("%s namespaceSelector matchExpression[0].key is %q; the §13.2 chart scopes feature-gated "+
			"webhooks by %q so the webhook fires only on declared agent namespaces", webhook,
			strings.TrimSpace(key), wantKey)
	}
	if strings.TrimSpace(op) != wantOp {
		t.Errorf("%s namespaceSelector matchExpression[0].operator is %q; the §13.2 chart uses %q to "+
			"include only the agent-namespace-labelled namespaces", webhook, strings.TrimSpace(op), wantOp)
	}
	if !strings.Contains(values, wantValue) {
		t.Errorf("%s namespaceSelector matchExpression[0].values is %q; the §13.2 chart uses [%q] so the "+
			"webhook fires for namespaces with lenny.dev/agent-namespace=true", webhook, strings.TrimSpace(values), wantValue)
	}
}

// webhookDeploymentArgs returns the container args of the named
// admission-webhook Deployment in lenny-system, one arg per line. The
// feature-gated webhook Deployments all run the lenny-webhook image and
// share the args block rendered by the chart's _webhook.tpl.
func webhookDeploymentArgs(t *testing.T, c *kind.Cluster, deployment string) string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", "lenny-system", "get", "deployment", deployment,
		"-o", "jsonpath={range .spec.template.spec.containers[0].args[*]}{@}{\"\\n\"}{end}",
	)
	if err != nil {
		t.Fatalf("query args of webhook Deployment %q: %v\n%s", deployment, err, out)
	}
	return out
}

// spec: 13.15
// diagnosis: §4.9/§13.2 lenny-direct-mode-isolation webhook deployed
// posture: failurePolicy: Fail, sandboxtemplates CREATE/UPDATE rule,
// agent-namespace selector, populated caBundle. The test also admits a
// compliant SandboxTemplate (deliveryMode: proxy) so the webhook plane
// is reachable. The §13.15 direct/standard rejection enforces only in
// multi-tenant mode with devMode off; the e2e overlay runs
// tenancy.mode: single, devMode: true, so the rejection half is
// covered by the tier-2/3 component suites.
func TestAdmissionDirectModeIsolation(t *testing.T) {
	c := kind.InstallLenny(t)

	const webhook = "lenny-direct-mode-isolation"
	assertFeatureGatedWebhookPresent(t, c, webhook, "features.llmProxy=true")

	rule := webhookRule(t, c, webhook)
	if rule.failurePolicy != "Fail" {
		t.Errorf("%s has failurePolicy %q; §13.2 requires fail-closed (Fail)", webhook, rule.failurePolicy)
	}
	if !rule.coversResource("sandboxtemplates") {
		t.Errorf("%s is scoped to resources %v; the §4.9 webhook must gate sandboxtemplates",
			webhook, rule.resources)
	}
	for _, op := range []string{"CREATE", "UPDATE"} {
		if !rule.coversOperation(op) {
			t.Errorf("%s does not intercept %s; the §4.9 webhook must gate CREATE and UPDATE",
				webhook, op)
		}
	}
	assertAgentNamespaceScoped(t, c, webhook)
	if !caBundlePopulated(t, c, webhook) {
		t.Errorf("%s has an empty clientConfig.caBundle; a fail-closed webhook with no caBundle "+
			"rejects every covered write", webhook)
	}

	// Positive control: a compliant SandboxTemplate (deliveryMode proxy
	// with the default spiffeBinding) must be admitted. This drives a
	// real AdmissionReview through the webhook Service and proves the
	// webhook plane is reachable and admits a valid template. The apply
	// is a server-side dry-run so nothing persists.
	const goodTemplate = `apiVersion: lenny.dev/v1alpha1
kind: SandboxTemplate
metadata:
  name: e2e-direct-mode-goodtemplate
  namespace: lenny-agents
spec:
  runtimeRef: e2e-nonexistent-runtime
  deliveryMode: proxy
  isolationProfile: sandboxed
`
	out, err := dryRunApply(t, c, goodTemplate)
	if err != nil {
		t.Fatalf("API server rejected a §4.9-compliant SandboxTemplate; "+
			"the %s webhook is over-broad or its Service is unreachable.\noutput:\n%s", webhook, out)
	}
	t.Logf("%s admitted a §4.9-compliant SandboxTemplate: %s", webhook, strings.TrimSpace(out))
}

// spec: 13.19
// diagnosis: §12.5/§13.2 NET-037 lenny-drain-readiness webhook deployed
// posture: failurePolicy: Fail, pods/eviction CREATE rule,
// agent-namespace selector, populated caBundle, and the §12.5 gateway
// callback URL on its Deployment. The §13.19 eviction-blocking
// rejection fires on a pods/eviction CREATE for a Ready agent pod
// while the gateway MinIO probe reports unhealthy; driving the probe
// to an unhealthy state would disrupt the other tier-5/8/9 tests, so
// the rejection half is covered by the tier-2/3 component suites.
func TestDrainReadinessWebhook(t *testing.T) {
	c := kind.InstallLenny(t)

	const webhook = "lenny-drain-readiness"
	assertFeatureGatedWebhookPresent(t, c, webhook, "features.drainReadiness=true")

	rule := webhookRule(t, c, webhook)
	if rule.failurePolicy != "Fail" {
		t.Errorf("%s has failurePolicy %q; §12.5 requires fail-closed (Fail)", webhook, rule.failurePolicy)
	}
	if !rule.coversResource("pods/eviction") {
		t.Errorf("%s is scoped to resources %v; the §12.5 webhook must gate the pods/eviction subresource",
			webhook, rule.resources)
	}
	if !rule.coversOperation("CREATE") {
		t.Errorf("%s does not intercept CREATE; a node drain creates a pods/eviction, so the §12.5 "+
			"webhook must gate CREATE (operations: %v)", webhook, rule.operations)
	}
	assertAgentNamespaceScoped(t, c, webhook)
	if !caBundlePopulated(t, c, webhook) {
		t.Errorf("%s has an empty clientConfig.caBundle; a fail-closed webhook with no caBundle "+
			"rejects every covered eviction", webhook)
	}

	// The §12.5 webhook probes the gateway GET /internal/drain-readiness
	// endpoint before admitting an eviction. The chart's _webhook.tpl
	// passes that URL via --gateway-drain-readiness-url; without it the
	// webhook cannot reach the MinIO health probe and fails every
	// eviction closed. Confirm the Deployment carries the flag.
	args := webhookDeploymentArgs(t, c, webhook)
	const drainURLFlag = "--gateway-drain-readiness-url=http://lenny-gateway.lenny-system.svc:8080/internal/drain-readiness"
	if !strings.Contains(args, drainURLFlag) {
		t.Errorf("%s Deployment is missing the §12.5 gateway callback flag %q; the webhook cannot "+
			"reach the drain-readiness probe.\nargs:\n%s", webhook, drainURLFlag, args)
	}
	t.Logf("%s deployed fail-closed, scoped to pods/eviction CREATE on agent namespaces, with the "+
		"§12.5 gateway callback URL wired", webhook)
}

// spec: 13.28
// diagnosis: §12.8/§12.9 lenny-data-residency-validator webhook
// deployed posture: failurePolicy: Fail, sandboxclaims CREATE/UPDATE
// rule, agent-namespace selector, populated caBundle. The test guards
// against a chart change that wires --storage-regions without a
// corresponding rejection test. The §13.28 region rejection cannot be
// exercised on this install: the chart passes no --storage-regions and
// the sandboxclaims CRD has no dataResidencyRegion field, so a
// region-bearing claim is rejected by CRD decoding before the webhook.
func TestAdmissionDataResidency(t *testing.T) {
	c := kind.InstallLenny(t)

	const webhook = "lenny-data-residency-validator"
	assertFeatureGatedWebhookPresent(t, c, webhook, "features.compliance=true")

	rule := webhookRule(t, c, webhook)
	if rule.failurePolicy != "Fail" {
		t.Errorf("%s has failurePolicy %q; §12.8 requires fail-closed (Fail)", webhook, rule.failurePolicy)
	}
	if !rule.coversResource("sandboxclaims") {
		t.Errorf("%s is scoped to resources %v; the chart scopes the §12.8 webhook to sandboxclaims",
			webhook, rule.resources)
	}
	for _, op := range []string{"CREATE", "UPDATE"} {
		if !rule.coversOperation(op) {
			t.Errorf("%s does not intercept %s; the §12.8 webhook must gate CREATE and UPDATE",
				webhook, op)
		}
	}
	assertAgentNamespaceScoped(t, c, webhook)
	if !caBundlePopulated(t, c, webhook) {
		t.Errorf("%s has an empty clientConfig.caBundle; a fail-closed webhook with no caBundle "+
			"rejects every covered write", webhook)
	}

	// Confirm the chart's _webhook.tpl does not pass --storage-regions:
	// with no declared regions the validator has nothing to reject a
	// resolved region against, which is one half of why the §13.28
	// rejection is unreachable. The absence is asserted so a future
	// chart change that wires the flag turns this guard into a failure
	// and prompts the rejection half to be written.
	args := webhookDeploymentArgs(t, c, webhook)
	if strings.Contains(args, "--storage-regions") {
		t.Fatalf("%s Deployment now carries --storage-regions; the chart wires declared regions, so "+
			"the §13.28 data-residency rejection is now exercisable and this test must drive it.\nargs:\n%s",
			webhook, args)
	}
	t.Logf("%s deployed fail-closed, scoped to sandboxclaims CREATE/UPDATE on agent namespaces; "+
		"chart still does not wire --storage-regions so the §13.28 region rejection is unreachable "+
		"and remains covered by the tier-2/3 component suites", webhook)
}

// spec: 13.28
// diagnosis: the §6.4 STR-003 lenny-t4-node-isolation webhook does not
// enforce the T4 dedicated-node rule. The test applies a T4-labelled
// pod with no T4 nodeSelector or toleration and expects the §6.4
// STR-003 rejection, then as a positive control applies a T4 pod that
// pins the T4 node label and tolerates the T4 taint and expects it
// admitted. Both pods satisfy §13.1 pod-security so the rejection is
// unambiguously the t4-node-isolation webhook's.
func TestAdmissionT4NodeIsolation(t *testing.T) {
	c := kind.InstallLenny(t)

	const webhook = "lenny-t4-node-isolation"
	assertFeatureGatedWebhookPresent(t, c, webhook, "features.compliance=true")

	rule := webhookRule(t, c, webhook)
	if rule.failurePolicy != "Fail" {
		t.Errorf("%s has failurePolicy %q; §6.4 requires fail-closed (Fail)", webhook, rule.failurePolicy)
	}

	// A T4 pod (lenny.dev/workspace-tier: t4) with no T4 nodeSelector
	// and no T4 toleration — a §6.4 STR-003 violation. The pod sets the
	// full §13.1 hardened securityContext so the lenny-pod-security
	// webhook, which also fires on Pod CREATE in agent namespaces,
	// admits it; the only rejection in scope is the T4 webhook's.
	const badT4Pod = `apiVersion: v1
kind: Pod
metadata:
  name: e2e-t4-isolation-badpod
  namespace: lenny-agents
  labels:
    lenny.dev/workspace-tier: t4
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
	out, err := dryRunApply(t, c, badT4Pod)
	if err == nil {
		t.Fatalf("API server admitted a T4 pod with no T4 nodeSelector or toleration; "+
			"the %s webhook did not reject it.\noutput:\n%s", webhook, out)
	}
	if !strings.Contains(out, "t4-node-isolation.lenny.dev") {
		t.Fatalf("rejection did not come from the t4-node-isolation webhook.\noutput:\n%s", out)
	}
	if !strings.Contains(out, "T4_NODE_ISOLATION_VIOLATION") {
		t.Errorf("rejection lacks the §6.4 T4_NODE_ISOLATION_VIOLATION code.\noutput:\n%s", out)
	}
	if !strings.Contains(out, "STR-003") {
		t.Errorf("rejection lacks the §6.4 STR-003 message.\noutput:\n%s", out)
	}
	t.Logf("t4-node-isolation rejected the unisolated T4 pod: %s", strings.TrimSpace(out))

	// Positive control: a T4 pod that pins the T4 node label via
	// nodeSelector and tolerates the T4 NoSchedule taint satisfies the
	// §6.4 predicate and must be admitted. It is also §13.1-compliant,
	// so neither the T4 webhook nor pod-security rejects it.
	const goodT4Pod = `apiVersion: v1
kind: Pod
metadata:
  name: e2e-t4-isolation-goodpod
  namespace: lenny-agents
  labels:
    lenny.dev/workspace-tier: t4
spec:
  nodeSelector:
    lenny.dev/workspace-tier: t4
  tolerations:
    - key: lenny.dev/workspace-tier
      operator: Equal
      value: t4
      effect: NoSchedule
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
	goodOut, goodErr := dryRunApply(t, c, goodT4Pod)
	if goodErr != nil {
		t.Fatalf("API server rejected a §6.4-compliant T4 pod (T4 node selector and toleration); "+
			"the %s webhook is over-broad.\noutput:\n%s", webhook, goodOut)
	}
	t.Logf("t4-node-isolation admitted the compliant T4 pod: %s", strings.TrimSpace(goodOut))
}

// spec: 13.15
// diagnosis: the §13.15 LLM Proxy proxy-mode admission posture is not
// deployed fail-closed. The LLM Proxy and its proxy-mode admission
// webhook (lenny-direct-mode-isolation) are rendered when
// features.llmProxy is true; the e2e overlay sets it, so the test
// confirms the proxy-mode admission webhook is fail-closed, carries a
// populated caBundle, and is scoped to agent namespaces. Exercising
// the proxy-mode credential path itself needs a running agent pod
// making LLM calls through the proxy, which the dev-mode install does
// not drive from the gateway-mediated session path.
func TestLLMProxyProxyMode(t *testing.T) {
	c := kind.InstallLenny(t)

	const webhook = "lenny-direct-mode-isolation"
	assertFeatureGatedWebhookPresent(t, c, webhook, "features.llmProxy=true")

	rule := webhookRule(t, c, webhook)
	if rule.failurePolicy != "Fail" {
		t.Errorf("%s has failurePolicy %q; §13.2 requires fail-closed (Fail)", webhook, rule.failurePolicy)
	}
	assertAgentNamespaceScoped(t, c, webhook)
	if !caBundlePopulated(t, c, webhook) {
		t.Errorf("%s has an empty clientConfig.caBundle; a fail-closed webhook with no caBundle "+
			"rejects every covered write", webhook)
	}
	t.Logf("%s deployed fail-closed for the §13.15 proxy-mode admission posture; the proxy-mode "+
		"credential path itself needs a running agent pod making LLM calls through the proxy", webhook)
}
