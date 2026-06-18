// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security tests for the §5.2 / §17.2 session/service
// derived-property pool-admission gates. These gates are
// tenant-isolation and credential-handling admission boundaries: a pool
// definition that crosses one of them must fail closed at admission so a
// misconfigured pool never enters the Postgres-authoritative registry and
// no pod is ever warmed under it.
//
// The proposal that collapsed the session/task/concurrent modes into
// session/service re-keyed four cross-tenant / process-isolation gates
// onto the derived sessionPolicy predicates. This file drives each gate
// through the live admin API on the Kind cluster (the real
// poolstore/admin admission path, against a real lenny-postgres), seeding
// the runtimes the pools bind to and then attempting each adversarial
// pool definition:
//
//   - The microvm cross-tenant gate: recycle.allowCrossTenantReuse: true
//     requires isolationProfile: microvm (sequential-reuse path).
//   - The T4 cross-tenant prohibition: a pool whose runtime is
//     workspaceTier: T4 may not set recycle.allowCrossTenantReuse,
//     regardless of isolation profile.
//   - The maxConcurrentSessions > 1 categorical cross-tenant rejection:
//     simultaneous process-level cotenancy has no isolation boundary, so
//     cross-tenant reuse is never permitted with concurrent slots.
//   - The maxConcurrentSessions > 1 process-level-isolation acknowledgment
//     requirement: concurrent slots share the pod process namespace,
//     /tmp, cgroup memory, network stack, and credential group-read
//     access, so the deployer must set acknowledgeProcessLevelIsolation.
//
// Each adversarial pool is created via POST /v1/admin/pools and must be
// rejected with 400 VALIDATION_ERROR. A companion control pool (the same
// definition with the violating field corrected) must be admitted, ruling
// out a blanket-rejection false positive. Every seeded runtime and every
// admitted control pool is removed in a t.Cleanup.
//
// The §4.7 / §5.3 nonce-only acknowledgment gate
// (TestNonceOnlyAcknowledgmentGate_spec_4_7, below) is a deployer security
// opt-in of the same class as allowStandardIsolation and
// acknowledgeBestEffortScrub, so it lives alongside the derived-property
// gates here.

package tier9_security_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// poolAdmissionRuntimes are the two runtimes the gate pools bind to: a
// general session runtime (used for the microvm, concurrent-cross-tenant,
// and process-acknowledgment cases) and a T4-tier runtime (used for the
// T4 cross-tenant prohibition). Both are agent runtimes pinned to a
// digest so the registry admits them.
const (
	poolAdmissionRuntime   = "t9-padm-runtime"
	poolAdmissionRuntimeT4 = "t9-padm-runtime-t4"
	poolAdmissionImage     = "ghcr.io/anthropic/claude-code@sha256:" +
		"0000000000000000000000000000000000000000000000000000000000000000"
)

// diagnosis: a §5.2 session/service derived-property gate did not fail
// closed at pool admission. The test seeds two runtimes, then drives four
// adversarial pool definitions through the live POST /v1/admin/pools
// admission path (microvm cross-tenant gate, T4 cross-tenant prohibition,
// maxConcurrentSessions > 1 categorical cross-tenant rejection, and the
// maxConcurrentSessions > 1 process-level-isolation acknowledgment) and
// asserts each is rejected with 400 VALIDATION_ERROR while the corrected
// control pool is admitted. An admitted adversarial pool means a
// tenant-isolation or credential-handling boundary is bypassable.
// spec: 5.2, 17.2
func TestPoolAdmissionDerivedPropertyGates_spec_5_2(t *testing.T) {
	c := kind.InstallLenny(t)
	if !deploymentReadyT9(t, c, gatewayDeploymentName) {
		t.Skipf("precondition not met: %s is not Ready; the admin API is the gateway", gatewayDeploymentName)
	}
	if !deploymentReadyT9(t, c, auditDeployment) {
		t.Skipf("precondition not met: %s is not Ready; the registry is Postgres-backed", auditDeployment)
	}

	probe := "t9-padm-probe"
	gatewayIP := startGatewayProbe(t, c, probe)
	admin := platformAdmin()

	// Seed the runtimes the pools bind to and register cleanup for them
	// plus every control pool the cases create.
	seedPoolAdmissionRuntimes(t, c, probe, gatewayIP, admin)

	for _, tc := range poolAdmissionGateCases() {
		t.Run(tc.name, func(t *testing.T) {
			// The adversarial pool must be rejected at admission.
			bad := gatewayRequestRetry(t, c, probe, gatewayIP, "POST", "/v1/admin/pools", admin, tc.badBody)
			if bad.curlExit != 0 {
				t.Fatalf("admin pool create did not complete (curl exit %d, body %q)", bad.curlExit, bad.body)
			}
			if bad.statusCode != 400 {
				// A 201 here means the gate is open: the pool was admitted.
				cleanupPool(t, c, probe, gatewayIP, admin, tc.poolName)
				t.Fatalf("§5.2 violation: the %q adversarial pool was admitted with status %d, expected 400 "+
					"VALIDATION_ERROR; the %s gate did not fail closed (body %q)",
					tc.poolName, bad.statusCode, tc.name, bad.body)
			}
			if code := bad.errorCode(); code != "VALIDATION_ERROR" {
				t.Errorf("§5.2: the %q rejection carries error code %q, expected VALIDATION_ERROR (body %q)",
					tc.poolName, code, bad.body)
			}
			if tc.wantSub != "" && !strings.Contains(bad.body, tc.wantSub) {
				t.Errorf("§5.2: the %q rejection body does not mention %q; the gate may be firing for the "+
					"wrong reason (body %q)", tc.poolName, tc.wantSub, bad.body)
			}

			// The corrected control pool must be admitted. The gate rejects
			// the specific violating field while admitting an otherwise
			// identical pool, which rules out a blanket-rejection false
			// positive.
			good := gatewayRequestRetry(t, c, probe, gatewayIP, "POST", "/v1/admin/pools", admin, tc.goodBody)
			t.Cleanup(func() { cleanupPool(t, c, probe, gatewayIP, admin, tc.controlPoolName) })
			if good.curlExit != 0 || good.statusCode != 201 {
				t.Errorf("control: the corrected %q pool was rejected with status %d, expected 201; the %s "+
					"gate is over-broad (body %q)", tc.controlPoolName, good.statusCode, tc.name, good.body)
			} else {
				t.Logf("§5.2: the %s gate rejected %q at admission and admitted the corrected control %q",
					tc.name, tc.poolName, tc.controlPoolName)
			}
		})
	}
}

// poolAdmissionGate is one derived-property gate case: an adversarial
// pool body that must be rejected and a corrected control body that must
// be admitted, with the runtime each binds to and a substring the
// rejection should mention.
type poolAdmissionGate struct {
	name            string
	poolName        string
	controlPoolName string
	badBody         string
	goodBody        string
	wantSub         string
}

// poolAdmissionGateCases builds the four derived-property gate cases. The
// JSON bodies carry no single quotes so the probe's shell quoting holds.
func poolAdmissionGateCases() []poolAdmissionGate {
	return []poolAdmissionGate{
		{
			// Sequential cross-tenant reuse requires microvm isolation.
			name:            "microvm-cross-tenant-gate",
			poolName:        "t9-padm-xtenant-sandboxed",
			controlPoolName: "t9-padm-xtenant-microvm",
			wantSub:         "isolationProfile is microvm",
			badBody: poolBodyJSON(poolFields{
				name: "t9-padm-xtenant-sandboxed", runtimeRef: poolAdmissionRuntime,
				isolationProfile: "sandboxed", executionMode: "session",
				sessionPolicy: recycleCrossTenant(),
			}),
			goodBody: poolBodyJSON(poolFields{
				name: "t9-padm-xtenant-microvm", runtimeRef: poolAdmissionRuntime,
				isolationProfile: "microvm", executionMode: "session",
				sessionPolicy: recycleCrossTenant(),
			}),
		},
		{
			// A T4-tier runtime forbids cross-tenant reuse regardless of
			// isolation profile (even microvm).
			name:            "t4-cross-tenant-prohibition",
			poolName:        "t9-padm-t4-xtenant",
			controlPoolName: "t9-padm-t4-noxtenant",
			wantSub:         "T4",
			badBody: poolBodyJSON(poolFields{
				name: "t9-padm-t4-xtenant", runtimeRef: poolAdmissionRuntimeT4,
				isolationProfile: "microvm", executionMode: "session",
				sessionPolicy: recycleCrossTenant(),
			}),
			goodBody: poolBodyJSON(poolFields{
				name: "t9-padm-t4-noxtenant", runtimeRef: poolAdmissionRuntimeT4,
				isolationProfile: "microvm", executionMode: "session",
				sessionPolicy: recycleNoCrossTenant(),
			}),
		},
		{
			// Concurrent slots never permit cross-tenant reuse, regardless of
			// isolation profile.
			name:            "concurrent-cross-tenant-rejection",
			poolName:        "t9-padm-concurrent-xtenant",
			controlPoolName: "t9-padm-concurrent-noxtenant",
			wantSub:         "maxConcurrentSessions > 1",
			badBody: poolBodyJSON(poolFields{
				name: "t9-padm-concurrent-xtenant", runtimeRef: poolAdmissionRuntime,
				isolationProfile: "microvm", executionMode: "session",
				sessionPolicy: `"sessionPolicy":{"maxConcurrentSessions":4,` +
					`"acknowledgeProcessLevelIsolation":true,` +
					`"recycle":{"allowCrossTenantReuse":true}}`,
			}),
			goodBody: poolBodyJSON(poolFields{
				name: "t9-padm-concurrent-noxtenant", runtimeRef: poolAdmissionRuntime,
				isolationProfile: "microvm", executionMode: "session",
				sessionPolicy: `"sessionPolicy":{"maxConcurrentSessions":4,` +
					`"acknowledgeProcessLevelIsolation":true}`,
			}),
		},
		{
			// maxConcurrentSessions > 1 requires the process-level isolation
			// acknowledgment.
			name:            "concurrent-process-ack-requirement",
			poolName:        "t9-padm-concurrent-noack",
			controlPoolName: "t9-padm-concurrent-ack",
			wantSub:         "acknowledgeProcessLevelIsolation",
			badBody: poolBodyJSON(poolFields{
				name: "t9-padm-concurrent-noack", runtimeRef: poolAdmissionRuntime,
				isolationProfile: "sandboxed", executionMode: "session",
				sessionPolicy: `"sessionPolicy":{"maxConcurrentSessions":4}`,
			}),
			goodBody: poolBodyJSON(poolFields{
				name: "t9-padm-concurrent-ack", runtimeRef: poolAdmissionRuntime,
				isolationProfile: "sandboxed", executionMode: "session",
				sessionPolicy: `"sessionPolicy":{"maxConcurrentSessions":4,` +
					`"acknowledgeProcessLevelIsolation":true}`,
			}),
		},
	}
}

// recycleCrossTenant is a sessionPolicy fragment requesting sequential
// cross-tenant pod reuse (maxConcurrentSessions stays at the 1 default).
func recycleCrossTenant() string {
	return `"sessionPolicy":{"recycle":{"enabled":true,"acknowledgeBestEffortScrub":true,` +
		`"maxSessionsPerPod":10,"allowCrossTenantReuse":true}}`
}

// recycleNoCrossTenant is the same recycling sessionPolicy without the
// cross-tenant flag, the corrected control for the T4 case.
func recycleNoCrossTenant() string {
	return `"sessionPolicy":{"recycle":{"enabled":true,"acknowledgeBestEffortScrub":true,` +
		`"maxSessionsPerPod":10}}`
}

// poolFields are the variable fields of an admin pool create body.
type poolFields struct {
	name             string
	runtimeRef       string
	isolationProfile string
	executionMode    string
	sessionPolicy    string // a raw JSON fragment, e.g. "sessionPolicy":{...}
}

// poolBodyJSON renders a POST /v1/admin/pools request body. warmCount is
// omitted (it defaults). Every case uses a sandboxed or microvm isolation
// profile, so the §5.3 standard-isolation opt-in gate never fires and
// allowStandardIsolation is left unset.
func poolBodyJSON(f poolFields) string {
	return fmt.Sprintf(
		`{"name":%q,"runtimeRef":%q,"isolationProfile":%q,"executionMode":%q,%s}`,
		f.name, f.runtimeRef, f.isolationProfile, f.executionMode, f.sessionPolicy,
	)
}

// seedPoolAdmissionRuntimes registers the session runtime and the T4
// runtime the gate pools bind to, and registers a t.Cleanup that removes
// them. A registration failure is fatal: the gate cases cannot run
// without their runtimes.
func seedPoolAdmissionRuntimes(t *testing.T, c *kind.Cluster, probe, gatewayIP string, admin gwRole) {
	t.Helper()
	runtimes := []struct {
		name string
		tier string
	}{
		{poolAdmissionRuntime, ""},
		{poolAdmissionRuntimeT4, "T4"},
	}
	t.Cleanup(func() {
		for _, rt := range runtimes {
			_ = gatewayRequest(t, c, probe, gatewayIP, "DELETE", "/v1/admin/runtimes/"+rt.name, admin, "")
		}
	})
	for _, rt := range runtimes {
		tierField := ""
		if rt.tier != "" {
			tierField = fmt.Sprintf(`,"workspaceTier":%q`, rt.tier)
		}
		body := fmt.Sprintf(
			`{"name":%q,"type":"agent","image":%q,"executionMode":"session",`+
				`"isolationProfile":"sandboxed","integrationLevel":"full"%s}`,
			rt.name, poolAdmissionImage, tierField,
		)
		res := gatewayRequestRetry(t, c, probe, gatewayIP, "POST", "/v1/admin/runtimes", admin, body)
		// A 409 means a prior run left the runtime; that is acceptable.
		if res.curlExit != 0 || (res.statusCode != 201 && res.statusCode != 409) {
			t.Fatalf("seeding runtime %s failed (curl exit %d, status %d, body %q)",
				rt.name, res.curlExit, res.statusCode, res.body)
		}
	}
}

// cleanupPool best-effort deletes a pool created during a case. It
// tolerates a 404 (the pool was never admitted) so it is safe to call on
// both the admitted control and a never-created adversarial pool.
func cleanupPool(t *testing.T, c *kind.Cluster, probe, gatewayIP string, admin gwRole, name string) {
	t.Helper()
	_ = gatewayRequest(t, c, probe, gatewayIP, "DELETE", "/v1/admin/pools/"+name, admin, "")
}

// --- §4.7 / §5.3 nonce-only acknowledgment gate ---
//
// Nonce-only mode (requireSoPeercred: false on a pool's runtime) weakens
// the adapter-agent authentication boundary, so it is a deployer security
// opt-in of the same class as allowStandardIsolation and
// acknowledgeBestEffortScrub: a pool referencing a nonce-only runtime is
// admitted only when it carries acknowledgeNonceOnlyAuth: true. The check
// is unconditional and applies in every tenancy mode.
//
// This drives the gate through the live admin API on the Kind cluster (the
// real poolstore/admin admission path against a real lenny-postgres). The
// activation field is settable only through Runtime CRD registration (the
// admin runtime payload does not model it), so the test applies the two
// runtimes as Runtime CRs and waits for the §5.1 RuntimeReconciler to
// mirror requireSoPeercred into the gateway registry the gate reads. It
// then attempts each adversarial pool write and asserts it fails closed,
// with the acknowledged or enforcing control admitted.
//
// The gate does not branch on tenancy mode; the deployed cluster exercises
// one mode, and the dev/tenant-mode axis is covered by the in-process
// component tests (pkg/gateway/admin/pools_nonce_only_test.go). This test
// verifies the gate against the real cluster admission path and the real
// CRD-to-registry mirror.

const (
	nonceOnlyRuntime = "t9-nonce-only-runtime"
	nonceEnforceRT   = "t9-nonce-enforce-runtime"
	nonceOnlyPoolImg = "ghcr.io/anthropic/claude-code@sha256:" +
		"0000000000000000000000000000000000000000000000000000000000000000"
)

// diagnosis: the §4.7 nonce-only acknowledgment gate did not fail closed at
// pool admission. The test applies a Runtime CR carrying requireSoPeercred:
// false (sidecar model), waits for the RuntimeReconciler to mirror it into
// the gateway registry, then drives three adversarial writes through the
// live admin API: an unacknowledged pool create, a runtimeRef swap from an
// enforcing runtime to the nonce-only runtime without the ack, and a PUT
// toggling the ack off on a nonce-only pool. Each must be rejected with 400
// VALIDATION_ERROR while the acknowledged create, the acknowledged swap,
// and an enforcing-runtime pool are admitted. An admitted unacknowledged
// pool means a deployer can put a pool into nonce-only mode without the
// security opt-in, weakening the adapter-agent authentication boundary.
// spec: 4.7, 5.3
func TestNonceOnlyAcknowledgmentGate_spec_4_7(t *testing.T) {
	c := kind.InstallLenny(t)
	if !deploymentReadyT9(t, c, gatewayDeploymentName) {
		t.Skipf("precondition not met: %s is not Ready; the admin API is the gateway", gatewayDeploymentName)
	}
	if !deploymentReadyT9(t, c, auditDeployment) {
		t.Skipf("precondition not met: %s is not Ready; the registry is Postgres-backed", auditDeployment)
	}

	probe := "t9-nonce-probe"
	gatewayIP := startGatewayProbe(t, c, probe)
	admin := platformAdmin()

	// Apply the two runtimes as Runtime CRs: a nonce-only sidecar runtime
	// (requireSoPeercred: false) and an SO_PEERCRED-enforcing one. The
	// reconciler mirrors requireSoPeercred CRD-to-store, where the gate reads
	// it; the admin runtime payload does not model the field, so a Runtime CR
	// is the only registration path that activates it.
	applyNonceRuntimeCR(t, c, nonceOnlyRuntime, false)
	applyNonceRuntimeCR(t, c, nonceEnforceRT, true)
	waitRuntimeMirrored(t, c, probe, gatewayIP, admin, nonceOnlyRuntime)
	waitRuntimeMirrored(t, c, probe, gatewayIP, admin, nonceEnforceRT)

	// Create: an unacknowledged nonce-only pool is rejected.
	t.Run("create-rejected-without-ack", func(t *testing.T) {
		body := noncePoolJSON("t9-nonce-noack", nonceOnlyRuntime, false)
		res := gatewayRequestRetry(t, c, probe, gatewayIP, "POST", "/v1/admin/pools", admin, body)
		if res.statusCode != 400 {
			cleanupPool(t, c, probe, gatewayIP, admin, "t9-nonce-noack")
			t.Fatalf("§4.7: unacknowledged nonce-only pool admitted with status %d, want 400 (body %q)",
				res.statusCode, res.body)
		}
		if code := res.errorCode(); code != "VALIDATION_ERROR" {
			t.Errorf("§4.7: rejection code %q, want VALIDATION_ERROR (body %q)", code, res.body)
		}
		if !strings.Contains(res.body, "acknowledgeNonceOnlyAuth") {
			t.Errorf("§4.7: rejection does not mention acknowledgeNonceOnlyAuth (body %q)", res.body)
		}
	})

	// Create: an acknowledged nonce-only pool is admitted.
	t.Run("create-admitted-with-ack", func(t *testing.T) {
		body := noncePoolJSON("t9-nonce-ack", nonceOnlyRuntime, true)
		res := gatewayRequestRetry(t, c, probe, gatewayIP, "POST", "/v1/admin/pools", admin, body)
		t.Cleanup(func() { cleanupPool(t, c, probe, gatewayIP, admin, "t9-nonce-ack") })
		if res.statusCode != 201 {
			t.Fatalf("§4.7: acknowledged nonce-only pool rejected with status %d, want 201 (body %q)",
				res.statusCode, res.body)
		}
	})

	// Create an enforcing-runtime pool, then swap its runtimeRef to the
	// nonce-only runtime without the ack: rejected. The same swap with the
	// ack is admitted.
	t.Run("runtimeref-swap-to-nonce-only", func(t *testing.T) {
		pool := "t9-nonce-swap"
		create := noncePoolJSON(pool, nonceEnforceRT, false)
		res := gatewayRequestRetry(t, c, probe, gatewayIP, "POST", "/v1/admin/pools", admin, create)
		t.Cleanup(func() { cleanupPool(t, c, probe, gatewayIP, admin, pool) })
		if res.statusCode != 201 {
			t.Fatalf("seed enforcing pool: status %d, want 201 (body %q)", res.statusCode, res.body)
		}
		etag := poolETag(t, c, probe, gatewayIP, admin, pool)

		// Swap to the nonce-only runtime without the ack: rejected.
		swap := fmt.Sprintf(`{"runtimeRef":%q}`, nonceOnlyRuntime)
		res = putPoolWithETag(t, c, probe, gatewayIP, admin, pool, swap, etag)
		if res.statusCode != 400 {
			t.Fatalf("§4.7: unacknowledged runtimeRef swap admitted with status %d, want 400 (body %q)",
				res.statusCode, res.body)
		}
		if !strings.Contains(res.body, "acknowledgeNonceOnlyAuth") {
			t.Errorf("§4.7: swap rejection does not mention acknowledgeNonceOnlyAuth (body %q)", res.body)
		}

		// The same swap with the ack set is admitted.
		swapAck := fmt.Sprintf(`{"runtimeRef":%q,"acknowledgeNonceOnlyAuth":true}`, nonceOnlyRuntime)
		res = putPoolWithETag(t, c, probe, gatewayIP, admin, pool, swapAck, etag)
		if res.statusCode != 200 {
			t.Fatalf("§4.7: acknowledged runtimeRef swap rejected with status %d, want 200 (body %q)",
				res.statusCode, res.body)
		}

		// Toggling the ack back off on the now-nonce-only pool is rejected.
		etag2 := poolETag(t, c, probe, gatewayIP, admin, pool)
		off := `{"acknowledgeNonceOnlyAuth":false}`
		res = putPoolWithETag(t, c, probe, gatewayIP, admin, pool, off, etag2)
		if res.statusCode != 400 {
			t.Fatalf("§4.7: acknowledgment toggle-off admitted with status %d, want 400 (body %q)",
				res.statusCode, res.body)
		}
	})
}

// applyNonceRuntimeCR applies a cluster-scoped sidecar Runtime CR with the
// §4.7 requireSoPeercred field set, and registers a t.Cleanup that deletes
// it. require=false is the nonce-only activation the gate trips on.
func applyNonceRuntimeCR(t *testing.T, c *kind.Cluster, name string, require bool) {
	t.Helper()
	manifest := fmt.Sprintf(`apiVersion: lenny.dev/v1alpha1
kind: Runtime
metadata:
  name: %s
  labels:
    lenny.dev/test: tier9-nonce-only
spec:
  type: agent
  image: %s
  integrationLevel: full
  executionMode: session
  isolationProfile: sandboxed
  deploymentModel: sidecar
  requireSoPeercred: %t
`, name, nonceOnlyPoolImg, require)
	if out, err := c.ApplyStdin(t, manifest); err != nil {
		t.Fatalf("apply Runtime CR %s: %v (output %q)", name, err, out)
	}
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, manifest) })
}

// waitRuntimeMirrored polls the gateway registry until the named runtime is
// resolvable, so the gate sees the CRD-to-store mirror the RuntimeReconciler
// performs. A runtime that never mirrors fails the test rather than leaving
// the gate cases to misreport an unmirrored runtime as a gate bug.
func waitRuntimeMirrored(t *testing.T, c *kind.Cluster, probe, gatewayIP string, admin gwRole, name string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		res := gatewayRequest(t, c, probe, gatewayIP, "GET", "/v1/admin/runtimes/"+name, admin, "")
		if res.curlExit == 0 && res.statusCode == 200 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("runtime %s not mirrored into the gateway registry within 60s (last status %d, body %q)",
				name, res.statusCode, res.body)
		}
		time.Sleep(2 * time.Second)
	}
}

// noncePoolJSON renders a POST /v1/admin/pools body for a sandboxed
// session-mode pool, with the §5.3 acknowledgeNonceOnlyAuth opt-in set or
// cleared.
func noncePoolJSON(name, runtimeRef string, ack bool) string {
	return fmt.Sprintf(
		`{"name":%q,"runtimeRef":%q,"isolationProfile":"sandboxed","executionMode":"session","acknowledgeNonceOnlyAuth":%t}`,
		name, runtimeRef, ack,
	)
}

// poolETag reads the §15.1 optimistic-concurrency ETag a GET on the pool
// reports so a later PUT satisfies the If-Match precondition.
func poolETag(t *testing.T, c *kind.Cluster, probe, gatewayIP string, admin gwRole, name string) string {
	t.Helper()
	res := gatewayRequest(t, c, probe, gatewayIP, "GET", "/v1/admin/pools/"+name, admin, "")
	var env struct {
		ETag string `json:"etag"`
	}
	_ = json.Unmarshal([]byte(res.body), &env)
	return env.ETag
}

// putPoolWithETag runs a pool PUT carrying the §15.1 If-Match header from
// etag, using a curl invocation that adds the header the shared
// gatewayRequest helper does not set.
func putPoolWithETag(t *testing.T, c *kind.Cluster, probe, gatewayIP string, admin gwRole, name, body, etag string) gwResponse {
	t.Helper()
	if strings.Contains(body, "'") {
		t.Fatalf("putPoolWithETag body contains a single quote: %q", body)
	}
	url := fmt.Sprintf("http://%s:8080/v1/admin/pools/%s", gatewayIP, name)
	cmd := fmt.Sprintf(
		"curl -sS -m 10 -X PUT -H 'X-Lenny-Tenant-ID: %s' -H 'X-Lenny-Roles: %s' "+
			"-H 'X-Lenny-User-ID: %s' -H 'Content-Type: application/json' -H 'If-Match: %s' "+
			"--data '%s' -w '\\nLENNYPROBE status=%%{http_code} exit=%%{exitcode}\\n' %s 2>&1",
		admin.tenant, admin.roles, admin.user, etag, body, url,
	)
	out, _ := c.KubectlOut(t, "-n", lennySystemNS, "exec", probe, "--", "sh", "-c", cmd)
	return parseGatewayResponse(out)
}
