// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos tests for the §25.1 independent-failure-domain guarantee
// between the gateway and lenny-ops. §25.1 states that lenny-ops "is a
// separate Deployment from the gateway and survives gateway failures",
// and that when lenny-ops is unreachable an external agent falls back to
// the gateway's `GET /v1/admin/health/summary` heartbeat. These two
// directions are the design premise of §25: neither component's outage
// takes the other down. Each test scales one component's Deployment to
// zero (a genuine outage — the Service loses its endpoints), asserts the
// other component keeps serving, then restores the scaled-down component
// and asserts recovery. Every test registers the restore as a t.Cleanup
// so the shared cluster is left healthy on a mid-test failure.

package tier8_chaos_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// opsDeployment is the §25.4 mandatory lenny-ops Deployment.
const opsDeployment = "lenny-ops"

// opsSelector is the label the lenny-ops Deployment, its pods, and its
// Service key on. lenny-ops carries app: lenny-ops rather than the
// lenny.dev/component key the rest of the control plane uses, because it
// is a separate operability plane (§13.2 line 201, ops-deployment.yaml).
const opsSelector = "app=lenny-ops"

// spec: 25.1
// diagnosis: §25.1 independent-failure-domain guarantee did not hold in
// the gateway→lenny-ops direction. §25.1 line 43: "Agents connect to
// lenny-ops (Section 25.4), which is a separate Deployment from the
// gateway and survives gateway failures." The test scales the gateway
// Deployment to zero (the gateway Service loses its endpoints — a total
// gateway outage). §25.1 line 20 lists the gateway admin API as a
// graceful-degradation dependency of lenny-ops, so lenny-ops must stay
// Ready and must not crash-loop while the gateway is down: its lifecycle
// is independent of Lenny's (§25.1 line 41). The test asserts lenny-ops
// stays Ready with a stable restart count throughout the gateway outage,
// then restores the gateway and asserts it returns to Ready.
func TestOpsSurvivesGatewayOutage(t *testing.T) {
	c := kind.InstallLenny(t)

	if !deploymentReady(t, c, gatewayDeployment) {
		t.Skipf("precondition not met: %s Deployment is not fully Ready (%s) before the chaos injection",
			gatewayDeployment, deploymentReadyState(t, c, gatewayDeployment))
	}
	if !deploymentReady(t, c, opsDeployment) {
		t.Skipf("precondition not met: %s Deployment is not fully Ready (%s) before the chaos injection",
			opsDeployment, deploymentReadyState(t, c, opsDeployment))
	}
	opsRestartsBefore := deploymentRestartCount(t, c, opsSelector)
	t.Logf("precondition: gateway Ready, lenny-ops Ready, lenny-ops restarts=%d", opsRestartsBefore)

	// Inject: scale the gateway to zero. scaleDownAndRestore registers the
	// cleanup that scales it back to its original replica count and waits
	// for it to return to Ready.
	gatewayReplicas := scaleDownAndRestore(t, c, gatewayDeployment)
	if !waitDeploymentScaledDown(t, c, gatewayDeployment, storeRecoveryBound) {
		t.Fatalf("%s did not scale down to zero replicas after the scale command", gatewayDeployment)
	}
	if got := endpointCount(t, c, gatewayDeployment); got != 0 {
		t.Logf("note: Service %s still reports %d endpoints shortly after scale-down", gatewayDeployment, got)
	}
	t.Logf("injected: %s scaled to zero; the gateway is unreachable", gatewayDeployment)

	// Assert: lenny-ops stays Ready throughout the gateway outage. §25.1
	// line 43 requires lenny-ops to survive a gateway failure; §25.1 line
	// 20 makes the gateway admin API a graceful-degradation dependency, so
	// the outage must not flip lenny-ops NotReady or restart its pods.
	for i := 0; i < 5; i++ {
		if !deploymentReady(t, c, opsDeployment) {
			t.Errorf("lenny-ops Deployment is not Ready (%s) during the gateway outage; "+
				"§25.1 requires lenny-ops to survive a gateway failure as an independent failure domain",
				deploymentReadyState(t, c, opsDeployment))
			break
		}
		time.Sleep(2 * time.Second)
	}

	// Assert: lenny-ops did not crash-loop on the gateway outage. A stable
	// restart count proves the GatewayClient degraded gracefully rather
	// than the kubelet killing the pod.
	assertNoCrashLoop(t, c, opsDeployment, opsSelector, opsRestartsBefore)

	// Restore the gateway (the t.Cleanup also restores).
	restoreDeployment(t, c, gatewayDeployment, gatewayReplicas)

	// Assert recovery: the gateway returns to Ready with Service endpoints.
	recovered := pollUntil(storeRecoveryBound, 2*time.Second, func() bool {
		return deploymentReady(t, c, gatewayDeployment) &&
			endpointCount(t, c, gatewayDeployment) > 0
	})
	if !recovered {
		t.Fatalf("%s did not return to Ready with Service endpoints within %s after restore (state %s, %d endpoints)",
			gatewayDeployment, storeRecoveryBound, deploymentReadyState(t, c, gatewayDeployment),
			endpointCount(t, c, gatewayDeployment))
	}
	t.Logf("recovery: gateway restored to Ready with %d Service endpoints; "+
		"lenny-ops rode out the gateway outage end to end", endpointCount(t, c, gatewayDeployment))
}

// spec: 25.1
// diagnosis: §25.1 heartbeat fallback did not survive a lenny-ops outage.
// §25.1 line 45: "If lenny-ops is unreachable, the agent falls back to
// the gateway's health summary endpoint (GET /v1/admin/health/summary)
// as a heartbeat." The gateway health surface reads only in-process state
// (§25.3 Data Sources) and does not depend on lenny-ops, so the heartbeat
// must keep answering while lenny-ops is entirely down. The test scales
// the lenny-ops Deployment to zero (its Service loses its endpoints — a
// total lenny-ops outage) and asserts GET /v1/admin/health/summary on the
// gateway still returns 200 throughout, then restores lenny-ops and
// asserts it returns to Ready.
func TestGatewayHealthSummarySurvivesOpsOutage(t *testing.T) {
	c := kind.InstallLenny(t)
	probe := "chaos-ops-outage-probe"
	gatewayIP := startGatewayProbePod(t, c, probe)

	if !deploymentReady(t, c, gatewayDeployment) {
		t.Skipf("precondition not met: %s Deployment is not fully Ready (%s) before the chaos injection",
			gatewayDeployment, deploymentReadyState(t, c, gatewayDeployment))
	}
	if !deploymentReady(t, c, opsDeployment) {
		t.Skipf("precondition not met: %s Deployment is not fully Ready (%s) before the chaos injection",
			opsDeployment, deploymentReadyState(t, c, opsDeployment))
	}
	// The heartbeat endpoint must answer before the injection so a later
	// failure is attributable to the lenny-ops outage and not to a
	// pre-existing gateway problem.
	if p := curlGateway(t, c, probe, gatewayIP, "/v1/admin/health/summary"); !p.ok(200) {
		t.Skipf("precondition not met: gateway /v1/admin/health/summary is not 200 before the injection "+
			"(curl exit %d, status %d)", p.curlExit, p.statusCode)
	}
	t.Logf("precondition: gateway Ready, lenny-ops Ready, gateway health/summary heartbeat is 200")

	// Inject: scale lenny-ops to zero. scaleDownAndRestore registers the
	// cleanup that scales it back to its original replica count.
	opsReplicas := scaleDownAndRestore(t, c, opsDeployment)
	if !waitDeploymentScaledDown(t, c, opsDeployment, storeRecoveryBound) {
		t.Fatalf("%s did not scale down to zero replicas after the scale command", opsDeployment)
	}
	// The endpoint set must be empty before the assertion so the outage is
	// genuine — an agent that reached lenny-ops would not fall back.
	endpointGone := pollUntil(storeRecoveryBound, 2*time.Second, func() bool {
		return endpointCount(t, c, opsDeployment) == 0
	})
	if !endpointGone {
		t.Fatalf("Service %s still has endpoints after the Deployment scaled to zero; "+
			"lenny-ops is not genuinely unreachable", opsDeployment)
	}
	t.Logf("injected: %s scaled to zero, Service has no endpoints; lenny-ops is unreachable", opsDeployment)

	// Assert: the gateway heartbeat keeps answering 200 throughout the
	// lenny-ops outage. §25.1 line 45 makes this endpoint the agent's
	// fallback heartbeat precisely because it does not depend on lenny-ops.
	for i := 0; i < 5; i++ {
		if p := curlGateway(t, c, probe, gatewayIP, "/v1/admin/health/summary"); !p.ok(200) {
			t.Errorf("gateway /v1/admin/health/summary returned curl exit %d / status %d while lenny-ops is down; "+
				"§25.1 requires the gateway heartbeat to survive a lenny-ops outage as the agent's fallback",
				p.curlExit, p.statusCode)
			break
		}
		time.Sleep(2 * time.Second)
	}

	// Restore lenny-ops (the t.Cleanup also restores).
	restoreDeployment(t, c, opsDeployment, opsReplicas)

	// Assert recovery: lenny-ops returns to Ready with Service endpoints.
	recovered := pollUntil(storeRecoveryBound, 2*time.Second, func() bool {
		return deploymentReady(t, c, opsDeployment) &&
			endpointCount(t, c, opsDeployment) > 0
	})
	if !recovered {
		t.Fatalf("%s did not return to Ready with Service endpoints within %s after restore (state %s, %d endpoints)",
			opsDeployment, storeRecoveryBound, deploymentReadyState(t, c, opsDeployment),
			endpointCount(t, c, opsDeployment))
	}
	t.Logf("recovery: lenny-ops restored to Ready with %d Service endpoints; "+
		"the gateway heartbeat survived the lenny-ops outage end to end", endpointCount(t, c, opsDeployment))
}
