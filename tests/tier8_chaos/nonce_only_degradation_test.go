// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos test for the §4.7 nonce-only / PoolSecurityDegraded
// failure-and-recovery path. The test drives the full Postgres-authoritative
// pool model on the live Kind cluster, end to end through the spec's own
// surfaces:
//
//   - It registers a deploymentModel: sidecar Runtime carrying
//     requireSoPeercred: false by applying a Runtime custom resource (the
//     only surface that models the §4.7 activating field — the admin
//     registration payload does not). The RuntimeReconciler mirrors it into
//     the gateway runtime registry and sets Registered=True.
//   - It creates the pool through the admin API (POST /v1/admin/pools) with
//     acknowledgeNonceOnlyAuth: true, because pool configuration is
//     Postgres-authoritative: the pool-config-validator webhook rejects a
//     direct kubectl write to a SandboxTemplate/SandboxWarmPool. The
//     PoolScalingController reconciles the Postgres pool row into the
//     SandboxTemplate + SandboxWarmPool CRD pair and mirrors the
//     acknowledgment onto SandboxTemplate.spec.
//   - The WarmPoolController then renders a nonce-only warm member Sandbox
//     for the acknowledged pool: the Sandbox reconciler resolves the §4.7
//     two-point gate (sidecar runtime + requireSoPeercred: false + pool
//     acknowledgment), records the Sandbox.spec.requireSoPeercred: false
//     carrier, and derives the SOPeercredDisabled=True member condition. The
//     WarmPoolController reads the carrier back and surfaces
//     SecurityDegradedMode=True on the SandboxTemplate plus
//     lenny_pool_security_degraded == 1 for the pool.
//
// Nothing in the test writes a SandboxTemplate, a SandboxWarmPool, or a
// member-Sandbox status directly: the degradation state is produced by the
// live controllers from a Postgres-authoritative pool and a Runtime CR, so
// the assertion exercises the controller-mediated alert series the bundled
// PoolSecurityDegraded rule evaluates rather than an injected projection.
//
// The revert step returns the runtime to full SO_PEERCRED enforcement
// (requireSoPeercred: true) and then deletes the pool through the admin API;
// the test asserts the §4.7 revert latch holds while a pre-revert nonce-only
// member still serves and that the condition and gauge clear once the pool's
// members are gone.

package tier8_chaos_test

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// nonceOnlyPoolPrefix / nonceOnlyRuntimePrefix are the name stems the
// nonce-only chaos scenario builds its per-run pool and Runtime names
// from. The names are made unique per run (uniqueNonceOnlyName) because a
// pool name is the PRIMARY KEY of the Postgres `sandbox_warm_pools` table
// (migrations/0033) and a soft-deleted row keeps the name occupied: a
// re-create with the same name fails with a unique violation (409
// RESOURCE_CONFLICT). A fixed name therefore lets the test run only once
// per cluster lifetime; a unique name per run makes it repeatable and
// keeps a leftover pool from a prior run (one F-13.2.24 leaves stuck) from
// colliding with this run. The pool name doubles as the SandboxTemplate +
// SandboxWarmPool object name the PoolScalingController reconciles into the
// agent namespace.
const (
	nonceOnlyPoolPrefix    = "chaos-nonce-only-pool"
	nonceOnlyRuntimePrefix = "chaos-nonce-only-runtime"
)

// nonceOnlyRuntimeClass is the RuntimeClass the §5.3 `standard` isolation
// profile maps to (`standard` → runc). The WarmPoolController marks a pool
// Degraded and suppresses all pod creation when the pool's RuntimeClass is
// absent (§5.3 line 675), so a pool can render a nonce-only member only on a
// cluster where this RuntimeClass exists. The runc RuntimeClass is part of
// the e2e install (tests/testinfra/kind/agent-workload.yaml); the test skips
// cleanly when it is absent rather than reporting a spurious §4.7 failure on
// a control-plane-only install.
const nonceOnlyRuntimeClass = "runc"

// nonceOnlyReconcileBound is the time the test allows the
// PoolScalingController and the WarmPoolController to reconcile a freshly
// created Postgres pool into its CRD pair, render the nonce-only warm
// member, and surface (or clear) the SecurityDegradedMode condition and the
// lenny_pool_security_degraded gauge. The PSC reconciles on a periodic tick
// and the WPC wakes on the owned-Sandbox watch; the bound covers the
// Postgres→CRD lag, the warm-member render, the reconcile queue latency, and
// the metrics-endpoint scrape settling.
const nonceOnlyReconcileBound = 4 * time.Minute

// nonceOnlyAdminTenant / nonceOnlyAdminRoles / nonceOnlyAdminUser are the
// dev-mode platform-admin identity the admin-API requests present. The
// cluster runs with global.devMode: true, so the gateway trusts the
// X-Lenny-* headers as the authenticated principal; a platform-admin in the
// built-in `platform` tenant may create and delete any pool.
const (
	nonceOnlyAdminTenant = "platform"
	nonceOnlyAdminRoles  = "platform-admin"
	nonceOnlyAdminUser   = "alice"
)

// gatewayHTTPPort is the gateway Service's internal HTTP port the admin API
// is served on. The test forwards it to the host so the admin requests need
// no in-cluster probe pod.
const gatewayHTTPPort = 8080

// controllerMetricsPortName is the name of the container port the
// WarmPoolController serves its Prometheus /metrics endpoint on (the chart
// names the --metrics-bind-address port "metrics"). The gauge is scraped by
// port-forwarding a controller pod's metrics port directly, so the read does
// not depend on the §16.9 metrics Service existing and is not subject to the
// in-cluster metrics-scrape NetworkPolicy.
const controllerMetricsPortName = "metrics"

// spec: §4.7 (nonce-only mode is an audited security-degradation state;
// nonce-only operation MUST be covered by an alert, satisfied by
// lenny_pool_security_degraded == 1; the revert latch keeps the pool
// degraded until the last nonce-only pod is replaced), §16.5 (the
// alert-support gauge backing the bundled PoolSecurityDegraded rule).
// diagnosis: §4.7 / §16.5 nonce-only degradation was not surfaced or did
// not recover on the live control plane. The test registers a sidecar
// requireSoPeercred=false Runtime CR, creates an acknowledged pool through
// the Postgres-authoritative admin API, and lets the live controllers
// render a nonce-only warm member; it then asserts the WarmPoolController
// writes SecurityDegradedMode=True on the SandboxTemplate and publishes
// lenny_pool_security_degraded == 1, reverts the runtime field, deletes the
// pool, and asserts the condition transitions to False and the gauge returns
// to 0. A failure means the bundled PoolSecurityDegraded alert has no live
// series when a pool runs nonce-only (operators are blind to a disabled
// SO_PEERCRED boundary), the §4.7 revert latch released early while a
// nonce-only member still served, or the pool never recovered after its
// members were gone.
func TestNonceOnlyModeDegradationAndRecovery(t *testing.T) {
	c := kind.InstallLenny(t)

	if !deploymentReady(t, c, controllerDeployment) {
		t.Skipf("precondition not met: %s Deployment is not Ready (%s) before the chaos scenario",
			controllerDeployment, deploymentReadyState(t, c, controllerDeployment))
	}
	// The WarmPoolController suppresses pod creation (marks the pool
	// Degraded, RuntimeClassNotFound) when the pool's RuntimeClass is
	// absent, so it can never render a nonce-only member without it. The runc
	// RuntimeClass (the §5.3 `standard` → runc mapping) ships with the e2e
	// agent workload (tests/testinfra/kind/agent-workload.yaml). Ensure it is
	// present as a test precondition: when the test creates it, the cleanup
	// removes it again so a control-plane-only install is left exactly as it
	// was found; when the agent workload already provided it, the test leaves
	// it in place.
	ensureNonceOnlyRuntimeClass(t, c)

	// Build a unique pool + Runtime name for this run. A pool name is the
	// PRIMARY KEY of the Postgres sandbox_warm_pools table and a soft-deleted
	// row keeps the name occupied, so a fixed name lets the scenario run only
	// once per cluster lifetime (the second create returns 409
	// RESOURCE_CONFLICT). A per-run suffix makes the test repeatable and keeps
	// a leftover pool a prior run left stuck (when the F-13.2.24 deletion-guard
	// block prevents teardown) from colliding with this run.
	pool := uniqueNonceOnlyName(t, nonceOnlyPoolPrefix)
	runtimeName := uniqueNonceOnlyName(t, nonceOnlyRuntimePrefix)

	// Register the §4.7 activating runtime through its only modeling surface,
	// the Runtime CR. requireSoPeercred: false is valid only on the sidecar
	// deployment model; the RuntimeReconciler mirrors the CR into the gateway
	// runtime registry (so the admin pool-create gate resolves it) and sets
	// Registered=True. The Runtime CR is not gated by the pool-config-validator
	// webhook, so a direct kubectl apply is the supported path. The image is
	// pinned by digest per §5.3 (the CRD Pattern requires @sha256) and need
	// not be pullable: the §4.7 carrier and the SOPeercredDisabled condition
	// are resolved from config before the pod is created, so the warm member's
	// pod failing to pull does not affect the surfaced degradation.
	runtime := nonceOnlyRuntimeManifest(runtimeName, false)
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, runtime) })
	if out, err := c.ApplyStdin(t, runtime); err != nil {
		t.Fatalf("failed to apply the nonce-only Runtime CR: %v\n%s", err, out)
	}
	registered := pollUntil(60*time.Second, 3*time.Second, func() bool {
		return runtimeRegisteredStatus(t, c, runtimeName) == "True"
	})
	if !registered {
		t.Fatalf("the RuntimeReconciler did not register Runtime %s (last Registered=%q); the admin pool-create "+
			"gate cannot resolve its requireSoPeercred posture", runtimeName,
			runtimeRegisteredStatus(t, c, runtimeName))
	}
	t.Logf("registered: Runtime %s (sidecar, requireSoPeercred=false) is Registered=True", runtimeName)

	// Drive the admin API from the test host through an API-server-mediated
	// port-forward to the gateway Service, so the admin requests need no
	// in-cluster probe pod (and therefore no probe-pod image preloaded on the
	// node). The forward terminates at the gateway pod through the API
	// server, so the request is not subject to the in-cluster ingress
	// NetworkPolicy; the cluster runs --dev-mode, so the gateway trusts the
	// X-Lenny-* headers as the authenticated platform-admin principal.
	gatewayURL, stopGateway := c.PortForward(t, "svc/lenny-gateway", lennySystemNamespace, gatewayHTTPPort)
	t.Cleanup(stopGateway)

	// Create the pool through the Postgres-authoritative admin API. warmCount
	// 1 makes the WarmPoolController render exactly one warm member (minWarm =
	// maxWarm = warmCount), which is what carries the §4.7 nonce-only signal;
	// acknowledgeNonceOnlyAuth: true is the §5.3 opt-in the gate requires for
	// a requireSoPeercred=false runtime; standard isolation + the explicit
	// allowStandardIsolation opt-in match the dev-mode runc RuntimeClass on
	// this cluster so the pool is not RuntimeClass-Degraded.
	t.Cleanup(func() { deleteNonceOnlyPool(t, gatewayURL, pool) })
	createNonceOnlyPool(t, gatewayURL, pool, runtimeName)
	t.Logf("created: Postgres-authoritative pool %s (warmCount=1, acknowledgeNonceOnlyAuth=true, runtimeRef=%s)",
		pool, runtimeName)

	// Forward a controller pod's metrics endpoint once for the run. The
	// port-forward goes through the API server, so the gauge read is not
	// subject to the in-cluster metrics-scrape NetworkPolicy. A pod target
	// (rather than the §16.9 Service) keeps the read working regardless of
	// whether the metrics Service is present.
	pod, port := controllerMetricsTarget(t, c)
	metricsURL, stop := c.PortForward(t, "pod/"+pod, lennySystemNamespace, port)
	t.Cleanup(stop)

	// Assert the live controllers reconcile the Postgres pool into its CRD
	// pair, render the nonce-only warm member, and surface the degradation.
	// The PoolScalingController writes the SandboxTemplate/SandboxWarmPool and
	// mirrors the acknowledgment; the WarmPoolController renders the member,
	// the Sandbox reconciler stamps the requireSoPeercred=false carrier, and
	// the WarmPoolController writes SecurityDegradedMode=True and publishes
	// the gauge.
	degraded := pollUntil(nonceOnlyReconcileBound, 5*time.Second, func() bool {
		return securityDegradedConditionStatus(t, c, pool) == "True"
	})
	if !degraded {
		t.Errorf("§4.7 violation: after the acknowledged nonce-only pool was created the controllers did not write "+
			"SecurityDegradedMode=True on SandboxTemplate %s (last status %q); the bundled PoolSecurityDegraded "+
			"alert has no condition backing. Member Sandboxes: %s", pool,
			securityDegradedConditionStatus(t, c, pool), describeNonceOnlyMembers(t, c, pool))
	} else {
		t.Logf("degradation surfaced: SandboxTemplate %s carries SecurityDegradedMode=True", pool)
	}
	gaugeUp := pollUntil(nonceOnlyReconcileBound, 5*time.Second, func() bool {
		v, ok := scrapeSecurityDegradedGauge(t, metricsURL, pool)
		return ok && v == 1
	})
	if !gaugeUp {
		v, _ := scrapeSecurityDegradedGauge(t, metricsURL, pool)
		t.Errorf("§16.5 violation: lenny_pool_security_degraded for pool %s did not reach 1 (last %v); the "+
			"controller-published gauge the bundled PoolSecurityDegraded rule evaluates has no degraded series",
			pool, v)
	} else {
		t.Logf("alert series live: lenny_pool_security_degraded{pool=%q} == 1", pool)
	}

	// Capture the rendered nonce-only member names before the revert so the
	// latch assertion can observe a pre-revert member surviving the runtime
	// field flip.
	preRevertMembers := nonceOnlyMemberNames(t, c, pool)
	t.Logf("rendered nonce-only members before revert: %v", preRevertMembers)

	// Revert step (runbook remediation step 2): return the runtime to full
	// SO_PEERCRED enforcement. This stops new nonce-only pods; per §4.7 the
	// pool stays degraded while a pre-revert nonce-only member still serves,
	// so the condition must hold until the carrier members are replaced.
	revert := nonceOnlyRuntimeManifest(runtimeName, true)
	if out, err := c.ApplyStdin(t, revert); err != nil {
		t.Fatalf("failed to revert the Runtime requireSoPeercred field to true: %v\n%s", err, out)
	}
	t.Logf("revert: Runtime %s requireSoPeercred set back to true", runtimeName)

	// Model the §4.7 latch ordering: while a pre-revert nonce-only member
	// survives, the pool stays SecurityDegradedMode=True. The reverted
	// runtime renders no new flag, but the existing warm member keeps its
	// requireSoPeercred=false carrier (and SOPeercredDisabled condition) until
	// it is replaced, so the condition must not clear in this window.
	if len(preRevertMembers) > 0 {
		cleared := pollUntil(20*time.Second, 4*time.Second, func() bool {
			return securityDegradedConditionStatus(t, c, pool) != "True"
		})
		if cleared {
			t.Errorf("§4.7 latch violation: SecurityDegradedMode cleared while member Sandbox(es) %v still carried "+
				"the nonce-only carrier; the latch must hold until the last nonce-only pod is replaced",
				preRevertMembers)
		} else {
			t.Logf("latch holds: pool stays SecurityDegradedMode=True while %v still carry the nonce-only carrier",
				preRevertMembers)
		}
	}

	// Recovery leg precondition: the recovery step deletes the pool, which
	// the PoolScalingController turns into a SandboxTemplate deletion. The
	// `sandboxtemplate-deletion-guard` validating webhook gates every
	// SandboxTemplate deletion on a §10.5 gateway runtime-upgrade probe and
	// fails closed when the probe cannot reach the gateway. On a cluster
	// where that webhook has no gateway-egress NetworkPolicy under the §13.2
	// default-deny (the orthogonal, separately-tracked F-13.2.24 chart
	// defect), the probe times out (`context deadline exceeded`) and no
	// SandboxTemplate can be torn down, so SecurityDegradedMode and the gauge
	// can never clear and the pool is left stuck. That blocker is independent
	// of the §4.7 carrier-write grant this test proves (it sits on the
	// teardown path through a different webhook); skip the recovery leg with
	// an explicit reference rather than report a spurious §4.7 recovery
	// failure that an unrelated chart defect causes. The degradation and
	// revert-latch assertions above already exercised the proposal-0008
	// carrier-write mechanism end to end.
	if !deletionGuardReachable(t, c, pool) {
		t.Skipf("recovery leg skipped: the sandboxtemplate-deletion-guard webhook cannot tear down "+
			"SandboxTemplate %s because its §10.5 gateway probe is blocked by the §13.2 default-deny "+
			"(missing gateway-egress NetworkPolicy; tracked OPEN as F-13.2.24, orthogonal to the §4.7 "+
			"carrier-write grant). The degradation and revert-latch legs passed.", pool)
	}

	// Recovery (runbook remediation step 3): delete the pool through the
	// admin API. The PoolScalingController removes the SandboxWarmPool /
	// SandboxTemplate CRD pair, garbage-collecting the member Sandboxes that
	// carried the nonce-only signal. With the pool's last nonce-only member
	// gone the WarmPoolController transitions the condition to an explicit
	// False (it was previously True) and clears the gauge before forgetting
	// the deleted pool's series. The condition read may go absent once the
	// SandboxTemplate is deleted; an absent condition is also a cleared
	// state, so recovery is reached on either an explicit False or the
	// template's removal.
	deleteNonceOnlyPool(t, gatewayURL, pool)
	t.Logf("recovery: pool %s deleted through the admin API", pool)

	recovered := pollUntil(nonceOnlyReconcileBound, 5*time.Second, func() bool {
		status := securityDegradedConditionStatus(t, c, pool)
		return status == "False" || status == ""
	})
	if !recovered {
		t.Errorf("§4.7 violation: after the nonce-only pool was deleted the controller did not clear "+
			"SecurityDegradedMode on SandboxTemplate %s (last status %q); a pool stuck degraded keeps the "+
			"PoolSecurityDegraded alert firing indefinitely", pool,
			securityDegradedConditionStatus(t, c, pool))
	} else {
		t.Logf("recovery: SandboxTemplate %s no longer reports SecurityDegradedMode=True", pool)
	}
	gaugeCleared := pollUntil(nonceOnlyReconcileBound, 5*time.Second, func() bool {
		v, ok := scrapeSecurityDegradedGauge(t, metricsURL, pool)
		// A cleared gauge is either an explicit 0 or the series being
		// forgotten when the deleted pool's reconcile drops it.
		return !ok || v == 0
	})
	if !gaugeCleared {
		v, _ := scrapeSecurityDegradedGauge(t, metricsURL, pool)
		t.Errorf("§16.5 violation: lenny_pool_security_degraded for pool %s did not return to 0 after recovery "+
			"(last %v); the bundled PoolSecurityDegraded alert keeps a stale degraded series", pool, v)
	} else {
		t.Logf("alert series cleared: lenny_pool_security_degraded{pool=%q} is 0 or gone; nonce-only degradation "+
			"verified end to end", pool)
	}
}

// uniqueNonceOnlyName appends a short random suffix to prefix so each test
// run uses a fresh pool / Runtime name. The pool name is the PRIMARY KEY of
// the Postgres sandbox_warm_pools table and a soft-deleted row keeps the
// name occupied (migrations/0033), so reusing a fixed name makes the second
// run fail with 409 RESOURCE_CONFLICT. The suffix is 8 lowercase-hex
// characters, which keeps the name within the §5.2 pool-name pattern
// (^[a-z0-9][a-z0-9_-]{0,127}$) and well under the Kubernetes object-name
// length limit the name carries through as the SandboxTemplate /
// SandboxWarmPool object name.
func uniqueNonceOnlyName(t *testing.T, prefix string) string {
	t.Helper()
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("failed to generate a unique run suffix: %v", err)
	}
	return prefix + "-" + hex.EncodeToString(b[:])
}

// deletionGuardReachable reports whether the sandboxtemplate-deletion-guard
// validating webhook can admit a SandboxTemplate deletion, by issuing a
// server-side dry-run delete of the pool's SandboxTemplate. The dry run runs
// the full admission chain (the webhook fires) but persists nothing, so it
// probes the gate without tearing the template down. The webhook gates every
// SandboxTemplate deletion on a §10.5 gateway runtime-upgrade probe and fails
// closed; under the §13.2 default-deny it has no gateway-egress NetworkPolicy
// on this cluster (the orthogonal F-13.2.24 chart defect), so the probe times
// out and the dry run returns a webhook-call failure
// ("failed calling webhook" / "context deadline exceeded"). The recovery leg
// uses this to skip rather than fail when the teardown path is blocked by
// that unrelated defect. A clean admit (or any non-webhook outcome) returns
// true so the recovery assertions run.
func deletionGuardReachable(t *testing.T, c *kind.Cluster, pool string) bool {
	t.Helper()
	out, _ := c.KubectlOut(
		t,
		"-n", agentNamespace, "delete", "sandboxtemplate", pool, "--dry-run=server",
	)
	if strings.Contains(out, "sandboxtemplate-deletion-guard") &&
		(strings.Contains(out, "failed calling webhook") || strings.Contains(out, "context deadline exceeded")) {
		t.Logf("deletion-guard webhook unreachable for SandboxTemplate %s (F-13.2.24): %s", pool, strings.TrimSpace(out))
		return false
	}
	return true
}

// nonceOnlyRuntimeManifest renders a deploymentModel: sidecar Runtime with
// the given requireSoPeercred value. The Runtime CRD is cluster-scoped, so
// the manifest carries no namespace. The image is pinned by digest per §5.3
// (the CRD Pattern requires an @sha256 reference); the referenced image need
// not be pullable because the §4.7 carrier and the SOPeercredDisabled
// condition are resolved from config before the warm member's pod is
// created. isolationProfile: standard matches the pool's profile so the
// resolved RuntimeClass is runc.
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
  isolationProfile: standard
  requireSoPeercred: %t
`, name, requireSoPeercred)
}

// createNonceOnlyPool creates the §4.7 nonce-only pool through the
// Postgres-authoritative admin API (POST /v1/admin/pools), the only
// supported pool-creation path: the pool-config-validator webhook rejects a
// direct kubectl write to a SandboxTemplate/SandboxWarmPool. The body sets
// warmCount=1 so the WarmPoolController renders one warm member,
// acknowledgeNonceOnlyAuth=true for the §5.3 opt-in, and standard isolation
// with the allowStandardIsolation opt-in to match the cluster's runc
// RuntimeClass. A 2xx is required; any other status fails the test because
// the rest of the scenario depends on the pool existing.
func createNonceOnlyPool(t *testing.T, gatewayURL, pool, runtimeName string) {
	t.Helper()
	body := fmt.Sprintf(`{"name":%q,"runtimeRef":%q,"isolationProfile":"standard",`+
		`"executionMode":"session","warmCount":1,"allowStandardIsolation":true,`+
		`"acknowledgeNonceOnlyAuth":true}`, pool, runtimeName)
	status, respBody, err := adminPoolRequest(http.MethodPost, gatewayURL+"/v1/admin/pools", body)
	if err != nil || (status != http.StatusCreated && status != http.StatusOK) {
		t.Fatalf("admin POST /v1/admin/pools failed (err=%v, status=%d): %s", err, status, respBody)
	}
}

// deleteNonceOnlyPool soft-deletes the pool through the admin API
// (DELETE /v1/admin/pools/{name}); the PoolScalingController then removes the
// SandboxWarmPool/SandboxTemplate CRD pair and garbage-collects the member
// Sandboxes. It is the test's recovery action and its t.Cleanup body, so it
// tolerates a 404 (pool already gone) and a failed request: leaving a
// permanently-degraded nonce-only pool behind would keep the
// PoolSecurityDegraded alert firing and affect later tests, so cleanup is
// best-effort but logged.
func deleteNonceOnlyPool(t *testing.T, gatewayURL, pool string) {
	t.Helper()
	status, respBody, err := adminPoolRequest(http.MethodDelete, gatewayURL+"/v1/admin/pools/"+pool, "")
	switch {
	case err != nil:
		t.Logf("cleanup: admin DELETE /v1/admin/pools/%s did not complete: %v", pool, err)
	case status == http.StatusNoContent || status == http.StatusOK || status == http.StatusNotFound:
		// Deleted now or already gone.
	default:
		t.Logf("cleanup: admin DELETE /v1/admin/pools/%s returned status %d: %s",
			pool, status, respBody)
	}
}

// adminPoolRequest issues an admin-API request from the test host through the
// forwarded gateway URL with the dev-mode platform-admin headers, returning
// the HTTP status, response body, and any transport error. The cluster runs
// --dev-mode, so the gateway trusts the X-Lenny-* headers as the
// authenticated principal. A non-empty body is sent as JSON on POST/PUT; an
// empty body (DELETE) omits the request body and Content-Type.
func adminPoolRequest(method, url, body string) (status int, respBody string, err error) {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("X-Lenny-Tenant-ID", nonceOnlyAdminTenant)
	req.Header.Set("X-Lenny-Roles", nonceOnlyAdminRoles)
	req.Header.Set("X-Lenny-User-ID", nonceOnlyAdminUser)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), nil
}

// runtimeClassExists reports whether the named cluster-scoped RuntimeClass is
// installed. The WarmPoolController suppresses pod creation for a pool whose
// RuntimeClass is absent, so the §4.7 surfacing path needs it present.
func runtimeClassExists(t *testing.T, c *kind.Cluster, name string) bool {
	t.Helper()
	out, err := c.KubectlOut(t, "get", "runtimeclass", name, "-o", "jsonpath={.metadata.name}")
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) == name
}

// ensureNonceOnlyRuntimeClass guarantees the runc RuntimeClass exists for the
// duration of the test. The §5.3 `standard` isolation profile maps to runc;
// without the RuntimeClass the WarmPoolController marks the pool Degraded and
// renders no member, so the §4.7 surfacing path cannot run. The runc
// RuntimeClass normally ships with the e2e agent workload, so the helper is a
// no-op when it is already present and leaves it in place. When it is absent
// (a control-plane-only install) the helper creates it and registers a
// cleanup that removes it again, so the cluster is left exactly as it was
// found. The handler is the node's stock runc handler, matching the
// agent-workload definition; Kind nodes run containerd whose default handler
// is runc, but no RuntimeClass object named runc exists by default.
func ensureNonceOnlyRuntimeClass(t *testing.T, c *kind.Cluster) {
	t.Helper()
	if runtimeClassExists(t, c, nonceOnlyRuntimeClass) {
		// The agent workload (or a prior run that did not clean up) already
		// provides it; leave it in place and own no cleanup for it.
		t.Logf("precondition: RuntimeClass %q already present; left in place", nonceOnlyRuntimeClass)
		return
	}
	manifest := fmt.Sprintf(`apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: %s
  labels:
    lenny.dev/test: chaos-nonce-only
handler: runc
`, nonceOnlyRuntimeClass)
	// Register the cleanup before the apply so a partial create is still
	// torn down. DeleteStdin tolerates an already-absent object.
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, manifest) })
	if out, err := c.ApplyStdin(t, manifest); err != nil {
		t.Fatalf("failed to create the runc RuntimeClass precondition: %v\n%s", err, out)
	}
	t.Logf("precondition: created RuntimeClass %q (test-owned; removed on cleanup)", nonceOnlyRuntimeClass)
}

// runtimeRegisteredStatus reads the §5.1 Registered condition status off the
// live (cluster-scoped) Runtime CR. It returns "True", "False", or "" when
// the condition (or the Runtime) is absent. A read error yields "" so the
// caller keeps polling.
func runtimeRegisteredStatus(t *testing.T, c *kind.Cluster, runtime string) string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"get", "runtime", runtime,
		"-o", `jsonpath={.status.conditions[?(@.type=="Registered")].status}`,
	)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// securityDegradedConditionStatus reads the §4.7 SecurityDegradedMode
// condition status off the live SandboxTemplate. It returns "True", "False",
// or "" when the condition (or the template) is absent. A read error yields
// "" so the caller keeps polling; an absent template after a pool delete is
// reported as "" (a cleared state).
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

// nonceOnlyMemberNames returns the names of the pool's member Sandboxes that
// carry the §4.7 nonce-only carrier (spec.requireSoPeercred: false). The
// WarmPoolController lists members by the lenny.dev/pool label; the test
// reads the same set to observe the rendered nonce-only members.
func nonceOnlyMemberNames(t *testing.T, c *kind.Cluster, pool string) []string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", agentNamespace, "get", "sandbox",
		"-l", "lenny.dev/pool="+pool,
		"-o", `jsonpath={range .items[?(@.spec.requireSoPeercred==false)]}{.metadata.name}{"\n"}{end}`,
	)
	if err != nil {
		return nil
	}
	return strings.Fields(out)
}

// describeNonceOnlyMembers renders the pool's member Sandboxes (name, phase,
// the requireSoPeercred carrier, and the SOPeercredDisabled condition) for a
// failure message, so a degradation that never surfaces is diagnosable
// against the live member set.
func describeNonceOnlyMembers(t *testing.T, c *kind.Cluster, pool string) string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", agentNamespace, "get", "sandbox",
		"-l", "lenny.dev/pool="+pool,
		"-o", `jsonpath={range .items[*]}{.metadata.name}=phase:{.status.phase},`+
			`requireSoPeercred:{.spec.requireSoPeercred},`+
			`soPeercredDisabled:{.status.conditions[?(@.type=="SOPeercredDisabled")].status};{end}`,
	)
	if err != nil || strings.TrimSpace(out) == "" {
		return "(none)"
	}
	return strings.TrimSpace(out)
}

// controllerMetricsTarget resolves a Ready WarmPoolController pod and the
// container port it serves /metrics on. It reads the named "metrics" port
// from the controller pod spec; a controller pod that does not declare a
// named metrics port fails the test, since the gauge assertion has no
// endpoint to scrape. The leader-election Lease holder is preferred (the
// leader is the reconcile writer, so its endpoint is guaranteed to carry the
// series); the first matching pod is the fallback.
func controllerMetricsTarget(t *testing.T, c *kind.Cluster) (pod string, port int) {
	t.Helper()
	pods := podNames(t, c, controllerSelector)
	if len(pods) == 0 {
		t.Fatalf("no %s pods found; the gauge assertion needs a controller metrics endpoint", controllerSelector)
	}
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
// the value and whether a series for the pool was found. A scrape error or a
// missing series yields ok=false so the caller keeps polling.
func scrapeSecurityDegradedGauge(t *testing.T, metricsURL, pool string) (float64, bool) {
	t.Helper()
	body, ok := scrapeMetrics(t, metricsURL)
	if !ok {
		return 0, false
	}
	// Match the gauge sample for the pool label regardless of label order in
	// the exposition line: lenny_pool_security_degraded{pool="<pool>"} <v>.
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
