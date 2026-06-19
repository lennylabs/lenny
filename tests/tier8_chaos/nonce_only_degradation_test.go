// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos test for the §4.7 nonce-only / PoolSecurityDegraded
// failure-and-recovery path. The test drives the live WarmPoolController
// on the Kind cluster: it injects the degraded SOPeercredDisabled state
// onto a pool's member Sandboxes (the SO_PEERCRED self-test fallback for
// confirmed gVisor divergence), asserts the controller surfaces
// SecurityDegradedMode=True on the SandboxTemplate status and publishes
// lenny_pool_security_degraded == 1 for the pool, then reverts the
// runtime field and replaces the last nonce-only pod and asserts the
// condition and the gauge clear.
//
// The injection is behavioral, not a stub: the test creates a real
// acknowledged SandboxWarmPool plus its SandboxTemplate and Runtime, then
// creates member Sandboxes owned by the pool that carry the §4.7
// nonce-only signals the Sandbox reconciler would otherwise resolve
// (spec.requireSoPeercred: false and the SOPeercredDisabled=True status
// condition). The pool is sized minWarm=0/maxWarm=0 and its members are
// stamped to the claimed (occupied) phase, so the live WarmPoolController
// neither creates real warm pods nor drains the injected members while it
// surfaces the degradation. The condition and gauge are read back off the
// live control plane (SandboxTemplate status via kubectl, the gauge by
// scraping a controller pod's metrics endpoint), so the assertion
// exercises the controller-mediated alert series the bundled
// PoolSecurityDegraded rule evaluates rather than a fake-client projection.

package tier8_chaos_test

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// nonceOnlyPool is the pool the nonce-only chaos scenario drives. The
// SandboxTemplate, SandboxWarmPool, and member Sandboxes all share this
// name/pool label, matching the §4.6.3 pool scoping the WarmPoolController
// lists members by (the lenny.dev/pool label).
const nonceOnlyPool = "chaos-nonce-only-pool"

// nonceOnlyRuntime is the deploymentModel: sidecar Runtime the pool
// references. requireSoPeercred: false on a sidecar runtime is the §4.7
// activating field; reverting it to true is the runbook's step-2
// remediation. The controller derives the pool condition from the member
// Sandboxes rather than the Runtime CR, so reverting this field stops new
// nonce-only pods but does not by itself clear the latch.
const nonceOnlyRuntime = "chaos-nonce-only-runtime"

// nonceOnlyMetricsBound is the time the test allows the live
// WarmPoolController to surface (or clear) the SecurityDegradedMode
// condition and the lenny_pool_security_degraded gauge after an
// owned-Sandbox change wakes its reconcile. The controller reconciles on
// the owned-Sandbox watch immediately; the bound covers reconcile queue
// latency and the metrics-endpoint scrape settling.
const nonceOnlyMetricsBound = 90 * time.Second

// controllerMetricsPortName is the name of the container port the
// WarmPoolController serves its Prometheus /metrics endpoint on (the
// chart names the --metrics-bind-address port "metrics"). The gauge is
// scraped by port-forwarding a controller pod's metrics port directly, so
// the read does not depend on the §16.9 metrics Service existing and is
// not subject to the in-cluster metrics-scrape NetworkPolicy.
const controllerMetricsPortName = "metrics"

// spec: §4.7 (nonce-only mode is an audited security-degradation state;
// nonce-only operation MUST be covered by an alert, satisfied by
// lenny_pool_security_degraded == 1; the revert latch keeps the pool
// degraded until the last nonce-only pod is replaced), §16.5 (the
// alert-support gauge backing the bundled PoolSecurityDegraded rule).
// diagnosis: §4.7 / §16.5 nonce-only degradation was not surfaced or did
// not recover on the live control plane. The test injects the
// SOPeercredDisabled state onto an acknowledged pool's member Sandboxes
// and asserts the live WarmPoolController writes SecurityDegradedMode=True
// on the SandboxTemplate and publishes lenny_pool_security_degraded == 1,
// then reverts the runtime field and replaces the last nonce-only pod and
// asserts the condition transitions to False and the gauge returns to 0. A
// failure means the bundled PoolSecurityDegraded alert has no live series
// when a pool runs nonce-only (operators are blind to a disabled
// SO_PEERCRED boundary), the §4.7 revert latch released early while a
// nonce-only pod still served, or the pool never recovered after the last
// one was replaced.
func TestNonceOnlyModeDegradationAndRecovery(t *testing.T) {
	c := kind.InstallLenny(t)

	if !deploymentReady(t, c, controllerDeployment) {
		t.Skipf("precondition not met: %s Deployment is not Ready (%s) before the chaos injection",
			controllerDeployment, deploymentReadyState(t, c, controllerDeployment))
	}

	// Create the Runtime, SandboxTemplate, and SandboxWarmPool the pool
	// reconcile needs. The runtime is deploymentModel: sidecar with
	// requireSoPeercred: false (the §4.7 activating configuration) and the
	// pool carries acknowledgeNonceOnlyAuth: true (the §5.3 opt-in). The
	// pool is sized minWarm=0/maxWarm=0 so the live controller creates no
	// real warm pods; the injected member Sandboxes carry the nonce-only
	// signal directly.
	runtime := nonceOnlyRuntimeManifest(nonceOnlyRuntime, false)
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, runtime) })
	if out, err := c.ApplyStdin(t, runtime); err != nil {
		t.Fatalf("failed to create the nonce-only Runtime: %v\n%s", err, out)
	}
	template := nonceOnlyTemplateManifest(nonceOnlyPool, nonceOnlyRuntime)
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, template) })
	if out, err := c.ApplyStdin(t, template); err != nil {
		t.Fatalf("failed to create the nonce-only SandboxTemplate: %v\n%s", err, out)
	}
	pool := nonceOnlyPoolManifest(nonceOnlyPool)
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, pool) })
	if out, err := c.ApplyStdin(t, pool); err != nil {
		t.Fatalf("failed to create the acknowledged SandboxWarmPool: %v\n%s", err, out)
	}
	poolUID := resourceUID(t, c, "sandboxwarmpool", nonceOnlyPool)
	if poolUID == "" {
		t.Fatalf("could not read the SandboxWarmPool UID; the member Sandboxes need it for the owner reference")
	}
	t.Logf("precondition: acknowledged pool %s/%s created (minWarm=0/maxWarm=0, sidecar runtime requireSoPeercred=false)",
		agentNamespace, nonceOnlyPool)

	// Forward a controller pod's metrics endpoint once for the run. The
	// port-forward goes through the API server, so the gauge read is not
	// subject to the in-cluster metrics-scrape NetworkPolicy. A pod target
	// (rather than the §16.9 Service) keeps the read working regardless of
	// whether the metrics Service is present.
	pod, port := controllerMetricsTarget(t, c)
	metricsURL, stop := c.PortForward(t, "pod/"+pod, lennySystemNamespace, port)
	t.Cleanup(stop)

	// Inject the degradation: two member Sandboxes owned by the pool, each
	// carrying the §4.7 nonce-only carrier (spec.requireSoPeercred: false)
	// and stamped to the claimed (occupied) phase so the planner leaves
	// them in place. The pre-revert pod additionally carries the
	// SOPeercredDisabled=True status condition the per-pod self-test
	// fallback writes on confirmed gVisor divergence.
	const preRevertPod = "sb-nonce-prerevert"
	const survivorPod = "sb-nonce-survivor"
	for _, name := range []string{preRevertPod, survivorPod} {
		sb := nonceOnlySandboxManifest(name, nonceOnlyPool, poolUID)
		t.Cleanup(func() { _, _ = c.DeleteStdin(t, sb) })
		if out, err := c.ApplyStdin(t, sb); err != nil {
			t.Fatalf("failed to create nonce-only member Sandbox %s: %v\n%s", name, err, out)
		}
		setSOPeercredDisabled(t, c, name, true)
	}
	t.Logf("injected: pool members carry spec.requireSoPeercred=false and SOPeercredDisabled=True")

	// Assert: the live WarmPoolController surfaces the degradation. The
	// owned-Sandbox watch wakes the reconcile, which writes
	// SecurityDegradedMode=True on the SandboxTemplate and publishes the
	// gauge.
	degraded := pollUntil(nonceOnlyMetricsBound, 3*time.Second, func() bool {
		return securityDegradedConditionStatus(t, c, nonceOnlyPool) == "True"
	})
	if !degraded {
		t.Errorf("§4.7 violation: after the pool's members entered nonce-only mode the controller did not write "+
			"SecurityDegradedMode=True on SandboxTemplate %s (last status %q); the bundled PoolSecurityDegraded "+
			"alert has no condition backing", nonceOnlyPool, securityDegradedConditionStatus(t, c, nonceOnlyPool))
	} else {
		t.Logf("degradation surfaced: SandboxTemplate %s carries SecurityDegradedMode=True", nonceOnlyPool)
	}
	gaugeUp := pollUntil(nonceOnlyMetricsBound, 3*time.Second, func() bool {
		v, ok := scrapeSecurityDegradedGauge(t, metricsURL, nonceOnlyPool)
		return ok && v == 1
	})
	if !gaugeUp {
		v, _ := scrapeSecurityDegradedGauge(t, metricsURL, nonceOnlyPool)
		t.Errorf("§16.5 violation: lenny_pool_security_degraded for pool %s did not reach 1 (last %v); the "+
			"controller-published gauge the bundled PoolSecurityDegraded rule evaluates has no degraded series",
			nonceOnlyPool, v)
	} else {
		t.Logf("alert series live: lenny_pool_security_degraded{pool=%q} == 1", nonceOnlyPool)
	}

	// Revert step (runbook remediation step 2): return the runtime to full
	// SO_PEERCRED enforcement. This stops new nonce-only pods; per §4.7 the
	// pool stays degraded while a pre-revert nonce-only pod still serves, so
	// the condition must hold until the carrier members are gone.
	if out, err := c.KubectlOut(
		t,
		"patch", "runtime", nonceOnlyRuntime,
		"--type=merge", "-p", `{"spec":{"requireSoPeercred":true}}`,
	); err != nil {
		t.Fatalf("failed to revert the runtime requireSoPeercred field: %v\n%s", err, out)
	}
	t.Logf("revert: runtime %s requireSoPeercred set back to true", nonceOnlyRuntime)

	// Model the §4.7 latch ordering: the pool must remain degraded while a
	// pre-revert nonce-only member survives. Delete the survivor first and
	// confirm the pool is still True; only when the last carrier member is
	// removed may the condition clear.
	if out, err := c.KubectlOut(t, "-n", agentNamespace, "delete", "sandbox", survivorPod, "--ignore-not-found"); err != nil {
		t.Fatalf("failed to delete the nonce-only survivor Sandbox: %v\n%s", err, out)
	}
	stillLatched := pollUntil(15*time.Second, 3*time.Second, func() bool {
		return securityDegradedConditionStatus(t, c, nonceOnlyPool) != "True"
	})
	if stillLatched {
		t.Errorf("§4.7 latch violation: SecurityDegradedMode cleared while member Sandbox %s still carried the "+
			"nonce-only carrier; the latch must hold until the last nonce-only pod is replaced", preRevertPod)
	} else {
		t.Logf("latch holds: pool stays SecurityDegradedMode=True while %s still carries the nonce-only carrier",
			preRevertPod)
	}

	// Replace the last nonce-only pod (runbook remediation step 3): with no
	// member Sandbox carrying spec.requireSoPeercred: false or
	// SOPeercredDisabled=True, the controller transitions the condition to
	// an explicit False and clears the gauge.
	if out, err := c.KubectlOut(t, "-n", agentNamespace, "delete", "sandbox", preRevertPod, "--ignore-not-found"); err != nil {
		t.Fatalf("failed to delete the last nonce-only Sandbox: %v\n%s", err, out)
	}
	recovered := pollUntil(nonceOnlyMetricsBound, 3*time.Second, func() bool {
		return securityDegradedConditionStatus(t, c, nonceOnlyPool) == "False"
	})
	if !recovered {
		t.Errorf("§4.7 violation: after the last nonce-only member was replaced the controller did not transition "+
			"SecurityDegradedMode to False on SandboxTemplate %s (last status %q); a reverted pool stuck degraded "+
			"keeps the PoolSecurityDegraded alert firing indefinitely", nonceOnlyPool,
			securityDegradedConditionStatus(t, c, nonceOnlyPool))
	} else {
		t.Logf("recovery: SandboxTemplate %s transitioned to SecurityDegradedMode=False", nonceOnlyPool)
	}
	gaugeCleared := pollUntil(nonceOnlyMetricsBound, 3*time.Second, func() bool {
		v, ok := scrapeSecurityDegradedGauge(t, metricsURL, nonceOnlyPool)
		return ok && v == 0
	})
	if !gaugeCleared {
		v, _ := scrapeSecurityDegradedGauge(t, metricsURL, nonceOnlyPool)
		t.Errorf("§16.5 violation: lenny_pool_security_degraded for pool %s did not return to 0 after recovery "+
			"(last %v); the bundled PoolSecurityDegraded alert keeps a stale degraded series", nonceOnlyPool, v)
	} else {
		t.Logf("alert series cleared: lenny_pool_security_degraded{pool=%q} == 0; nonce-only degradation "+
			"verified end to end", nonceOnlyPool)
	}
}

// nonceOnlyRuntimeManifest renders a deploymentModel: sidecar Runtime with
// the given requireSoPeercred value. The Runtime CRD is cluster-scoped, so
// the manifest carries no namespace. The image is pinned by digest per
// §5.3 (the CRD pattern requires an @sha256 reference); the referenced
// image need not be pullable because the pool is sized to zero warm pods.
func nonceOnlyRuntimeManifest(name string, requireSoPeercred bool) string {
	return fmt.Sprintf(`apiVersion: lenny.dev/v1alpha1
kind: Runtime
metadata:
  name: %s
  labels:
    lenny.dev/test: chaos-nonce-only
spec:
  type: agent
  image: ghcr.io/lennylabs/chaos-nonce-only@sha256:0000000000000000000000000000000000000000000000000000000000000000
  integrationLevel: full
  deploymentModel: sidecar
  requireSoPeercred: %t
`, name, requireSoPeercred)
}

// nonceOnlyTemplateManifest renders the SandboxTemplate the pool reconcile
// reads. It carries acknowledgeNonceOnlyAuth: true so the pool is in the
// §5.3-admitted state that legitimately runs nonce-only pods, and points
// runtimeRef at the sidecar runtime.
func nonceOnlyTemplateManifest(name, runtimeRef string) string {
	return fmt.Sprintf(`apiVersion: lenny.dev/v1alpha1
kind: SandboxTemplate
metadata:
  name: %s
  namespace: %s
  labels:
    lenny.dev/test: chaos-nonce-only
spec:
  runtimeRef: %s
  isolationProfile: sandboxed
  deliveryMode: proxy
  acknowledgeNonceOnlyAuth: true
`, name, agentNamespace, runtimeRef)
}

// nonceOnlyPoolManifest renders the SandboxWarmPool. minWarm=0/maxWarm=0
// keeps the live WarmPoolController from creating real warm pods (the
// injected member Sandboxes supply the nonce-only signal directly), while
// still being an admissible pool (minWarm <= maxWarm, both non-negative).
func nonceOnlyPoolManifest(name string) string {
	return fmt.Sprintf(`apiVersion: lenny.dev/v1alpha1
kind: SandboxWarmPool
metadata:
  name: %s
  namespace: %s
  labels:
    lenny.dev/test: chaos-nonce-only
spec:
  minWarm: 0
  maxWarm: 0
  templateRef: %s
`, name, agentNamespace, name)
}

// nonceOnlySandboxManifest renders a member Sandbox carrying the §4.7
// nonce-only carrier (spec.requireSoPeercred: false) and labeled with the
// pool so the WarmPoolController lists it as a member. The Sandbox is owned
// by the pool (controller: true) so it wakes the controller's owned-Sandbox
// watch on create and delete, making the reconcile deterministic. The
// phase is set later via the status subresource.
func nonceOnlySandboxManifest(name, pool, poolUID string) string {
	return fmt.Sprintf(`apiVersion: lenny.dev/v1alpha1
kind: Sandbox
metadata:
  name: %s
  namespace: %s
  labels:
    lenny.dev/pool: %s
    lenny.dev/test: chaos-nonce-only
  ownerReferences:
    - apiVersion: lenny.dev/v1alpha1
      kind: SandboxWarmPool
      name: %s
      uid: %s
      controller: true
      blockOwnerDeletion: true
spec:
  poolRef: %s
  runtimeRef: %s
  requireSoPeercred: false
`, name, agentNamespace, pool, pool, poolUID, pool, nonceOnlyRuntime)
}

// setSOPeercredDisabled writes (or clears) the §4.7 SOPeercredDisabled
// status condition on a member Sandbox and stamps its phase to claimed so
// the §4.6.1 planner treats the pod as occupied and never drains it while
// the pool is sized to zero. The condition is the per-pod self-test
// fallback signal the SO_PEERCRED check writes on confirmed gVisor
// divergence; the pool-level trigger reads it back. Status is a
// subresource, so it is written with kubectl --subresource=status.
func setSOPeercredDisabled(t *testing.T, c *kind.Cluster, name string, disabled bool) {
	t.Helper()
	status := "True"
	reason := "RenderedNonceOnly"
	message := "Pod rendered with --require-so-peercred=false."
	if !disabled {
		status = "False"
		reason = "SOPeercredEnforced"
		message = "SO_PEERCRED enforcement is active."
	}
	// claimed is an occupied phase: the planner counts it neither warm nor
	// ready and never lists it as a drain candidate, so a zero-sized pool
	// leaves the injected member in place across reconciles.
	patch := fmt.Sprintf(`{"status":{"phase":"claimed","conditions":[`+
		`{"type":"SOPeercredDisabled","status":%q,"reason":%q,"message":%q,`+
		`"lastTransitionTime":%q}]}}`,
		status, reason, message, time.Now().UTC().Format(time.RFC3339))
	if out, err := c.KubectlOut(
		t,
		"-n", agentNamespace, "patch", "sandbox", name,
		"--subresource=status", "--type=merge", "-p", patch,
	); err != nil {
		t.Fatalf("failed to set SOPeercredDisabled=%s on Sandbox %s: %v\n%s", status, name, err, out)
	}
}

// resourceUID reads the .metadata.uid of a namespaced lenny.dev resource in
// the agent namespace. The member Sandboxes need the pool UID for a valid
// controller owner reference.
func resourceUID(t *testing.T, c *kind.Cluster, kind, name string) string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", agentNamespace, "get", kind, name, "-o", "jsonpath={.metadata.uid}",
	)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// securityDegradedConditionStatus reads the §4.7 SecurityDegradedMode
// condition status off the live SandboxTemplate. It returns "True",
// "False", or "" when the condition (or the template) is absent. A read
// error yields "" so the caller keeps polling.
func securityDegradedConditionStatus(t *testing.T, c *kind.Cluster, pool string) string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", agentNamespace, "get", "sandboxtemplate", pool,
		"-o", `jsonpath={.status.conditions[?(@.type=="SecurityDegradedMode")].status}`,
	)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// controllerMetricsTarget resolves a Ready WarmPoolController pod and the
// container port it serves /metrics on. It reads the named "metrics" port
// from the controller pod spec; a controller pod that does not declare a
// named metrics port fails the test, since the gauge assertion has no
// endpoint to scrape. The first pod matching the controller selector is
// used (any replica publishes the gauge for every pool it reconciles, and
// the lenny_pool_security_degraded series is set every reconcile by the
// leader).
func controllerMetricsTarget(t *testing.T, c *kind.Cluster) (pod string, port int) {
	t.Helper()
	pods := podNames(t, c, controllerSelector)
	if len(pods) == 0 {
		t.Fatalf("no %s pods found; the gauge assertion needs a controller metrics endpoint", controllerSelector)
	}
	// Prefer the leader-election Lease holder when it is resolvable: the
	// leader is the reconcile writer, so its metrics endpoint is guaranteed
	// to carry the series. Fall back to the first pod otherwise.
	pod = pods[0]
	if holder := leaseHolderPod(leaseHolderIdentity(t, c, warmPoolControllerLease)); holder != "" {
		for _, p := range pods {
			if p == holder {
				pod = holder
				break
			}
		}
	}
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "get", "pod", pod,
		"-o", fmt.Sprintf(
			"jsonpath={range .spec.containers[*].ports[?(@.name==%q)]}{.containerPort}{end}",
			controllerMetricsPortName,
		),
	)
	if err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("could not resolve the %q container port on controller pod %s: %v\n%s",
			controllerMetricsPortName, pod, err, out)
	}
	n, convErr := strconv.Atoi(strings.TrimSpace(out))
	if convErr != nil {
		t.Fatalf("controller metrics port %q on pod %s is not an integer: %v", strings.TrimSpace(out), pod, convErr)
	}
	return pod, n
}

// scrapeSecurityDegradedGauge reads the lenny_pool_security_degraded gauge
// value for the given pool off the controller metrics endpoint. It returns
// the value and whether a series for the pool was found. A scrape error or
// a missing series yields ok=false so the caller keeps polling.
func scrapeSecurityDegradedGauge(t *testing.T, metricsURL, pool string) (float64, bool) {
	t.Helper()
	body, ok := scrapeMetrics(t, metricsURL)
	if !ok {
		return 0, false
	}
	// Match the gauge sample for the pool label regardless of label order
	// in the exposition line: lenny_pool_security_degraded{pool="<pool>"} <v>.
	want := fmt.Sprintf(`pool="%s"`, pool)
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "lenny_pool_security_degraded{") {
			continue
		}
		brace := strings.IndexByte(line, '}')
		if brace < 0 || !strings.Contains(line[:brace], want) {
			continue
		}
		fields := strings.Fields(line[brace+1:])
		if len(fields) == 0 {
			continue
		}
		v, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			continue
		}
		return v, true
	}
	return 0, false
}

// scrapeMetrics fetches the controller /metrics endpoint through the
// port-forward and returns the body. A request failure or non-200 status
// yields ok=false so the caller keeps polling rather than failing on a
// transient scrape error.
func scrapeMetrics(t *testing.T, metricsURL string) (string, bool) {
	t.Helper()
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Get(metricsURL + "/metrics")
	if err != nil {
		return "", false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false
	}
	return string(body), true
}
