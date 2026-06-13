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

package tier9_security_test

import (
	"fmt"
	"strings"
	"testing"

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

// spec: 5.2, 17.2
// diagnosis: a §5.2 session/service derived-property gate did not fail
// closed at pool admission. The test seeds two runtimes, then drives four
// adversarial pool definitions through the live POST /v1/admin/pools
// admission path — the microvm cross-tenant gate, the T4 cross-tenant
// prohibition, the maxConcurrentSessions > 1 categorical cross-tenant
// rejection, and the maxConcurrentSessions > 1 process-level-isolation
// acknowledgment requirement — and asserts each is rejected with 400
// VALIDATION_ERROR while the corrected control pool is admitted. An
// admitted adversarial pool means a tenant-isolation or
// credential-handling boundary is bypassable at admission.
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
