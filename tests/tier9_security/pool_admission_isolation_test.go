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

	"github.com/lennylabs/lenny/pkg/sandbox/podscrub"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
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

	// Pool DELETE is a §15.1 soft delete: it stamps deleted_at and leaves
	// the name occupied, so a create reusing that name returns 409
	// RESOURCE_ALREADY_EXISTS. A per-run suffix gives each pool a fresh name
	// so a soft-deleted leftover from a prior run never collides and the
	// test is re-runnable against the same cluster.
	suffix := fmt.Sprintf("-%d", time.Now().UnixNano())

	for _, tc := range poolAdmissionGateCases(suffix) {
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

// poolAdmissionGateCases builds the four derived-property gate cases. Each
// pool name carries the per-run suffix so a soft-deleted leftover from a
// prior run never collides with a fresh create. The JSON bodies carry no
// single quotes so the probe's shell quoting holds.
func poolAdmissionGateCases(suffix string) []poolAdmissionGate {
	badXtenantSandboxed := "t9-padm-xtenant-sandboxed" + suffix
	ctlXtenantMicrovm := "t9-padm-xtenant-microvm" + suffix
	badT4Xtenant := "t9-padm-t4-xtenant" + suffix
	ctlT4NoXtenant := "t9-padm-t4-noxtenant" + suffix
	badConcurrentXtenant := "t9-padm-concurrent-xtenant" + suffix
	ctlConcurrentNoXtenant := "t9-padm-concurrent-noxtenant" + suffix
	badConcurrentNoAck := "t9-padm-concurrent-noack" + suffix
	ctlConcurrentAck := "t9-padm-concurrent-ack" + suffix
	return []poolAdmissionGate{
		{
			// Sequential cross-tenant reuse requires microvm isolation.
			name:            "microvm-cross-tenant-gate",
			poolName:        badXtenantSandboxed,
			controlPoolName: ctlXtenantMicrovm,
			wantSub:         "isolationProfile is microvm",
			badBody: poolBodyJSON(poolFields{
				name: badXtenantSandboxed, runtimeRef: poolAdmissionRuntime,
				isolationProfile: "sandboxed", executionMode: "session",
				sessionPolicy: recycleCrossTenant(),
			}),
			goodBody: poolBodyJSON(poolFields{
				name: ctlXtenantMicrovm, runtimeRef: poolAdmissionRuntime,
				isolationProfile: "microvm", executionMode: "session",
				// §5.2: a cross-tenant-reuse microvm pool must set
				// scrubProfile vm-restart or in-place; the standard scrub
				// (steps 0-6) is insufficient for the cross-tenant
				// residual-state boundary because the guest VM persists
				// across sessions. scrubProfile vm-restart retires the pod at
				// the occupancy-zero recycle boundary and the gateway
				// provisions a fresh replacement pod (a fresh guest VM), which
				// structurally eliminates the guest-kernel residual state the
				// scrub cannot reach. The control pool sets vm-restart so it is
				// admitted, isolating the isolationProfile gate the bad pool
				// trips (sandboxed) from the scrubProfile requirement.
				sessionPolicy: recycleCrossTenantScrubbed(),
			}),
		},
		{
			// A T4-tier runtime forbids cross-tenant reuse regardless of
			// isolation profile (even microvm).
			name:            "t4-cross-tenant-prohibition",
			poolName:        badT4Xtenant,
			controlPoolName: ctlT4NoXtenant,
			wantSub:         "T4",
			badBody: poolBodyJSON(poolFields{
				name: badT4Xtenant, runtimeRef: poolAdmissionRuntimeT4,
				isolationProfile: "microvm", executionMode: "session",
				sessionPolicy: recycleCrossTenant(),
			}),
			goodBody: poolBodyJSON(poolFields{
				name: ctlT4NoXtenant, runtimeRef: poolAdmissionRuntimeT4,
				isolationProfile: "microvm", executionMode: "session",
				sessionPolicy: recycleNoCrossTenant(),
			}),
		},
		{
			// Concurrent slots never permit cross-tenant reuse, regardless of
			// isolation profile.
			name:            "concurrent-cross-tenant-rejection",
			poolName:        badConcurrentXtenant,
			controlPoolName: ctlConcurrentNoXtenant,
			// The gate message contains a greater-than sign that the JSON
			// error body renders as a > escape, so a wantSub carrying
			// the literal sign never matches. wantSub matches the distinctive
			// escape-free tail of the §5.2 message instead.
			wantSub: "cross-tenant slot sharing has no isolation boundary",
			badBody: poolBodyJSON(poolFields{
				name: badConcurrentXtenant, runtimeRef: poolAdmissionRuntime,
				isolationProfile: "microvm", executionMode: "session",
				sessionPolicy: `"sessionPolicy":{"maxConcurrentSessions":4,` +
					`"acknowledgeProcessLevelIsolation":true,` +
					`"recycle":{"allowCrossTenantReuse":true}}`,
			}),
			goodBody: poolBodyJSON(poolFields{
				name: ctlConcurrentNoXtenant, runtimeRef: poolAdmissionRuntime,
				isolationProfile: "microvm", executionMode: "session",
				sessionPolicy: `"sessionPolicy":{"maxConcurrentSessions":4,` +
					`"acknowledgeProcessLevelIsolation":true}`,
			}),
		},
		{
			// maxConcurrentSessions > 1 requires the process-level isolation
			// acknowledgment.
			name:            "concurrent-process-ack-requirement",
			poolName:        badConcurrentNoAck,
			controlPoolName: ctlConcurrentAck,
			wantSub:         "acknowledgeProcessLevelIsolation",
			badBody: poolBodyJSON(poolFields{
				name: badConcurrentNoAck, runtimeRef: poolAdmissionRuntime,
				isolationProfile: "sandboxed", executionMode: "session",
				sessionPolicy: `"sessionPolicy":{"maxConcurrentSessions":4}`,
			}),
			goodBody: poolBodyJSON(poolFields{
				name: ctlConcurrentAck, runtimeRef: poolAdmissionRuntime,
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

// recycleCrossTenantScrubbed is the cross-tenant recycling sessionPolicy
// with scrubProfile vm-restart set, the corrected control for the microvm
// cross-tenant gate. §5.2 rejects cross-tenant reuse on a microvm pool
// unless scrubProfile is vm-restart or in-place, so the admitted control
// pool must carry it (vm-restart avoids the in-place
// acknowledgeMicrovmResidualState requirement). scrubProfile vm-restart is
// the retire-and-reprovision mechanism: at the occupancy-zero recycle
// boundary the pod is retired and the gateway provisions a fresh
// replacement pod from the warm pool (a fresh guest VM), so no vm-restart
// pod is rebound to a second tenant's session without a fresh guest. The
// profile name is unchanged; only the mechanism it selects is
// retire-and-reprovision rather than an in-guest guest reboot.
func recycleCrossTenantScrubbed() string {
	return `"sessionPolicy":{"recycle":{"enabled":true,"acknowledgeBestEffortScrub":true,` +
		`"maxSessionsPerPod":10,"allowCrossTenantReuse":true,"scrubProfile":"vm-restart"}}`
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
		// §5.1 line 51: labels are required from v1. The seed runtime must
		// declare at least one label or the gateway rejects the create with
		// 400 VALIDATION_ERROR before the gate cases can bind a pool to it.
		body := fmt.Sprintf(
			`{"name":%q,"type":"agent","image":%q,"executionMode":"session",`+
				`"isolationProfile":"sandboxed","integrationLevel":"full",`+
				`"labels":{"lenny.dev/test":"tier9-pool-admission"}%s}`,
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

// diagnosis: the §4.9 layer-1 warm-pool credential-delivery gate did not
// fail closed at pool admission. The test drives a pool that combines
// deliveryMode: proxy with egressProfile: provider-direct through the live
// POST /v1/admin/pools admission path and asserts it is rejected with 422
// carrying the InvalidPoolEgressDeliveryCombo code, while the corrected
// control pool (proxy + restricted) is admitted. An admitted adversarial
// pool means the §13.2 NET-006 mutual-exclusivity boundary is bypassable at
// registration: proxy mode keeps API keys off the pod, but provider-direct
// egress opens a direct CIDR path to the same provider endpoints, and the
// stored pool would reconcile into a SandboxTemplate the failurePolicy: Fail
// layer-2 webhook then rejects, wedging the PoolScalingController. NET-006 is
// rejected in every tenancy mode, so this runs on the standard install.
// spec: 4.9, 13.2
func TestPoolAdmissionCredentialDeliveryGate_spec_4_9(t *testing.T) {
	c := kind.InstallLenny(t)
	if !deploymentReadyT9(t, c, gatewayDeploymentName) {
		t.Skipf("precondition not met: %s is not Ready; the admin API is the gateway", gatewayDeploymentName)
	}
	if !deploymentReadyT9(t, c, auditDeployment) {
		t.Skipf("precondition not met: %s is not Ready; the registry is Postgres-backed", auditDeployment)
	}

	probe := "t9-padm-cred-probe"
	gatewayIP := startGatewayProbe(t, c, probe)
	admin := platformAdmin()
	seedPoolAdmissionRuntimes(t, c, probe, gatewayIP, admin)

	suffix := fmt.Sprintf("-%d", time.Now().UnixNano())
	badName := "t9-padm-proxy-providerdirect" + suffix
	ctlName := "t9-padm-proxy-restricted" + suffix

	badBody := fmt.Sprintf(
		`{"name":%q,"runtimeRef":%q,"isolationProfile":"sandboxed","executionMode":"session",`+
			`"deliveryMode":"proxy","egressProfile":"provider-direct"}`,
		badName, poolAdmissionRuntime,
	)
	bad := gatewayRequestRetry(t, c, probe, gatewayIP, "POST", "/v1/admin/pools", admin, badBody)
	if bad.curlExit != 0 {
		t.Fatalf("admin pool create did not complete (curl exit %d, body %q)", bad.curlExit, bad.body)
	}
	if bad.statusCode != 422 {
		cleanupPool(t, c, probe, gatewayIP, admin, badName)
		t.Fatalf("§4.9/§13.2 violation: the proxy + provider-direct pool was admitted with status %d, "+
			"expected 422; the NET-006 credential-delivery gate did not fail closed (body %q)",
			bad.statusCode, bad.body)
	}
	if !strings.Contains(bad.body, "InvalidPoolEgressDeliveryCombo") {
		t.Errorf("§13.2: the proxy + provider-direct rejection does not carry the "+
			"InvalidPoolEgressDeliveryCombo code; the gate may be firing for the wrong reason (body %q)", bad.body)
	}

	ctlBody := fmt.Sprintf(
		`{"name":%q,"runtimeRef":%q,"isolationProfile":"sandboxed","executionMode":"session",`+
			`"deliveryMode":"proxy","egressProfile":"restricted"}`,
		ctlName, poolAdmissionRuntime,
	)
	good := gatewayRequestRetry(t, c, probe, gatewayIP, "POST", "/v1/admin/pools", admin, ctlBody)
	t.Cleanup(func() { cleanupPool(t, c, probe, gatewayIP, admin, ctlName) })
	if good.curlExit != 0 || good.statusCode != 201 {
		t.Errorf("control: the corrected proxy + restricted pool was rejected with status %d, expected 201; "+
			"the NET-006 gate is over-broad (body %q)", good.statusCode, good.body)
	}
}

// --- §5.2 step 7 vm-restart cross-tenant recycle-boundary retire ---
//
// The microvm cross-tenant gate above admits a scrubProfile: vm-restart
// pool as the corrected control. Admission is only half the boundary: it
// permits the pool, but the residual-state isolation the deployer opts into
// with vm-restart is enforced at the recycle boundary, where the gateway
// decides whether to reuse the scrubbed pod for the next session or retire
// it. On a vm-restart pool that decision must be a retire-and-reprovision:
// the standard whole-pod scrub (steps 0-6) cannot reach guest-kernel
// residual state (DNS cache, TCP TIME_WAIT, page cache, inotify
// registrations, kernel module state), so returning the pod to the pool
// would hand the next session a pod whose guest VM persists across the
// tenant boundary. The fail-closed disposition is to retire the pod at the
// occupancy-zero boundary and let the warm pool provision a fresh
// replacement pod, which is a fresh guest VM.
//
// This drives podscrub.Decide, the gateway recycle-boundary decision, at a
// vm-restart cross-tenant boundary in-process (no Kind cluster) so it fails
// closed wherever the suite runs, mirroring the in-process style of
// concurrent_slot_isolation_test.go. It is the security-tier regression for
// the C3 retire branch: before that branch existed, Decide had no
// scrub-profile input and a clean vm-restart scrub took the reuse path
// (Reserved or SDKConnecting), returning the pod to cross-tenant service
// without a fresh guest. The assertions below fail against that pre-fix
// disposition.

// diagnosis: a vm-restart pod crossed the tenant boundary without a fresh
// guest. The recycle-boundary disposition (podscrub.Decide) reused a
// vm-restart pod (Reserved/SDKConnecting/idle) on a clean scrub instead of
// retiring it, so the next tenant's session would rebind a pod whose guest
// VM persists across the boundary and carries the previous tenant's
// guest-kernel residual state (DNS cache, TCP TIME_WAIT, page cache). The
// fail-closed disposition is a Draining retire; the warm pool then
// provisions a fresh replacement pod (a fresh guest VM).
// spec: 5.2 step 7 (fresh-guest reprovision), 13.1, 13.2 (cross-tenant
// residual-state boundary)
func TestVMRestartCrossTenantRecycleRetires_spec_5_2_step_7(t *testing.T) {
	// A vm-restart pool is eligible for cross-tenant sequential reuse
	// (maxConcurrentSessions: 1, allowCrossTenantReuse: true, microvm).
	// At the occupancy-zero recycle boundary the pool has served its
	// session and the whole-pod scrub reported. The recycle-boundary
	// decision must be a retire, not a reuse, so the pod never rebinds to a
	// second tenant's session without a fresh guest.
	//
	// Each case covers a preConnect / non-preConnect and clean / warn-failed
	// combination: on a vm-restart pool the retire preempts both reuse paths
	// (the non-preConnect reserved hold and the preConnect sdk_connecting
	// re-warm) and both scrub outcomes (a clean scrub and a warn-policy
	// failure that has not exhausted maxScrubFailures), so no vm-restart pod
	// reaches the pool for the next tenant regardless of these axes.
	cases := []struct {
		name         string
		preConnect   bool
		scrub        podscrub.ScrubResult
		wantWarnAnno bool
	}{
		{name: "non-preConnect-clean-scrub", preConnect: false, scrub: podscrub.ScrubSucceeded, wantWarnAnno: false},
		{name: "preConnect-clean-scrub", preConnect: true, scrub: podscrub.ScrubSucceeded, wantWarnAnno: false},
		{name: "non-preConnect-warn-failed-scrub", preConnect: false, scrub: podscrub.ScrubFailed, wantWarnAnno: true},
		{name: "preConnect-warn-failed-scrub", preConnect: true, scrub: podscrub.ScrubFailed, wantWarnAnno: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := podscrub.Decide(podscrub.Inputs{
				// The cross-tenant residual-state control the microvm gate
				// admits: scrubProfile vm-restart.
				VMRestart: true,
				// The recycle boundary reached occupancy zero after the
				// pod served one session on a sequential (maxConcurrentSessions:
				// 1) cross-tenant pool. maxSessionsPerPod: 10 is not yet reached,
				// so any retire here is the vm-restart reprovision rather than
				// the session-count limit.
				SessionsServed:    1,
				MaxSessionsPerPod: 10,
				PreConnect:        tc.preConnect,
				Scrub:             tc.scrub,
				// A warn-policy scrub failure (below maxScrubFailures) would
				// otherwise return the pod to the pool with a scrub_warning; on
				// a vm-restart pool it retires instead.
				OnCleanupFailure:  podscrub.OnCleanupWarn,
				ScrubFailureCount: 1,
				MaxScrubFailures:  3,
				// The host node is schedulable, so a reuse (not a cordon-drain)
				// is the disposition the retire must preempt. If Decide reused
				// the pod, the host-schedulability gate would not mask it.
				HostSchedulable: true,
			})

			if !d.Ready {
				t.Fatalf("§5.2 step 7: the vm-restart recycle disposition is not Ready; the boundary produced no decision")
			}
			// The fail-closed disposition: retire and drain. A reuse
			// (Reserved or SDKConnecting) would return the pod to
			// cross-tenant service without a fresh guest.
			if !d.Retire || d.NextPhase != state.Draining {
				t.Fatalf("§5.2/§13.1 cross-tenant boundary: a vm-restart pod was NOT retired at the recycle "+
					"boundary (Retire=%v, NextPhase=%q); it would rebind to a second tenant's session without a "+
					"fresh guest, so the guest-kernel residual state crosses the tenant boundary", d.Retire, d.NextPhase)
			}
			if d.NextPhase == state.Reserved || d.NextPhase == state.SDKConnecting {
				t.Fatalf("§5.2/§13.1: a vm-restart pod took the reuse path (%q); the pod is held for reuse across "+
					"the tenant boundary without a fresh guest", d.NextPhase)
			}
			// The retire is the non-counting vm_restart_reprovision reason: a
			// routine per-boundary reprovision, not a §16.1 retirement-limit
			// trigger, so it is audit-trail-only and does not widen the frozen
			// lenny_gateway_pod_retirement_total{reason} vocabulary.
			if d.Reason != podscrub.ReasonVMRestartReprovision {
				t.Errorf("§5.2 step 7: the vm-restart retire carries reason %q, want %q",
					d.Reason, podscrub.ReasonVMRestartReprovision)
			}
			if d.Reason.CountsOnRetirementTotal() {
				t.Errorf("§16.1: the vm-restart reprovision reason %q must not count on lenny_gateway_pod_retirement_total; "+
					"it is a routine per-boundary reprovision, not a retirement-limit trigger", d.Reason)
			}
			// A warn-policy scrub failure stamps scrub_warning onto the drain
			// so the degraded-state marker reaches the retired pod's audit
			// trail; a clean scrub carries no annotation.
			if d.ScrubWarning != tc.wantWarnAnno {
				t.Errorf("§5.2 onScrubFailure: warn: the vm-restart retire ScrubWarning=%v, want %v",
					d.ScrubWarning, tc.wantWarnAnno)
			}
		})
	}
}

// diagnosis: a same-tenant (standard/in-place) microvm recycle boundary was
// retired as if it were a vm-restart pool, or the vm-restart retire fired
// on the wrong scrub-profile signal. This control asserts that a non-vm-restart
// pool reuses the pod on a clean scrub, so the vm-restart retire above is
// specific to the scrubProfile: vm-restart residual-state opt-in and is not a
// blanket retire that would defeat same-tenant reuse.
// spec: 5.2 step 7 (fresh-guest reprovision), 5.2 (recycle lifecycle reuse)
func TestNonVMRestartRecycleReusesControl_spec_5_2(t *testing.T) {
	// The identical recycle boundary WITHOUT the vm-restart opt-in: a
	// standard/in-place pool reuses the scrubbed pod for the next session
	// (Reserved on a non-preConnect pool), so the retire above is driven by
	// the vm-restart signal rather than a blanket retire.
	d := podscrub.Decide(podscrub.Inputs{
		VMRestart:         false,
		SessionsServed:    1,
		MaxSessionsPerPod: 10,
		PreConnect:        false,
		Scrub:             podscrub.ScrubSucceeded,
		OnCleanupFailure:  podscrub.OnCleanupWarn,
		MaxScrubFailures:  3,
		HostSchedulable:   true,
	})
	if !d.Ready {
		t.Fatalf("control: the standard recycle disposition is not Ready")
	}
	if d.Retire || d.NextPhase != state.Reserved {
		t.Fatalf("control: a standard (non-vm-restart) pool did NOT reuse the pod on a clean scrub "+
			"(Retire=%v, NextPhase=%q, want Reserved); the vm-restart retire is over-broad and would defeat "+
			"same-tenant reuse", d.Retire, d.NextPhase)
	}
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

	// Pool DELETE is a §15.1 soft delete: it stamps deleted_at and leaves the
	// name occupied, so a create reusing that name returns 409
	// RESOURCE_ALREADY_EXISTS. A per-run suffix gives each pool a fresh name so
	// a soft-deleted leftover from a prior run never collides and the test is
	// re-runnable against the same cluster.
	suffix := fmt.Sprintf("-%d", time.Now().UnixNano())
	noackPool := "t9-nonce-noack" + suffix
	ackPool := "t9-nonce-ack" + suffix
	swapPool := "t9-nonce-swap" + suffix

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
		body := noncePoolJSON(noackPool, nonceOnlyRuntime, false)
		res := gatewayRequestRetry(t, c, probe, gatewayIP, "POST", "/v1/admin/pools", admin, body)
		if res.statusCode != 400 {
			cleanupPool(t, c, probe, gatewayIP, admin, noackPool)
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
		body := noncePoolJSON(ackPool, nonceOnlyRuntime, true)
		res := gatewayRequestRetry(t, c, probe, gatewayIP, "POST", "/v1/admin/pools", admin, body)
		t.Cleanup(func() { cleanupPool(t, c, probe, gatewayIP, admin, ackPool) })
		if res.statusCode != 201 {
			t.Fatalf("§4.7: acknowledged nonce-only pool rejected with status %d, want 201 (body %q)",
				res.statusCode, res.body)
		}
	})

	// Create an enforcing-runtime pool, then swap its runtimeRef to the
	// nonce-only runtime without the ack: rejected. The same swap with the
	// ack is admitted.
	t.Run("runtimeref-swap-to-nonce-only", func(t *testing.T) {
		pool := swapPool
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
