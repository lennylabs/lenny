// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos test for the §25.15 Failure Mode Analysis "lenny-ops
// crash" row: "Gateway continues serving client traffic unaffected.
// Diagnostics, runbook index, drift, backup, and upgrade APIs are
// unavailable. The audit query API stays available because it is
// gateway-resident (Section 25.9). Watchdog detects ops service down via
// Ingress health check failure (no response from `/healthz`)."
//
// The companion TestGatewayHealthSummarySurvivesOpsOutage in
// ops_survives_gateway_test.go asserts only that the gateway's
// /v1/admin/health/summary heartbeat survives a lenny-ops outage. It does
// not touch the audit query API, and no test asserts the reverse side of
// this row — that the lenny-ops-hosted diagnostics, runbook, drift,
// backup, and upgrade endpoints actually become unreachable while
// lenny-ops is down. This test scales lenny-ops to zero (a genuine
// outage — the Service loses its endpoints) and drives both halves of
// the row in one pass.

package tier8_chaos_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// gatewayAuditQueryPath is the §25.9 audit-log query endpoint on the
// gateway, scoped with a single query parameter. curlGateway/
// curlGatewayAuth pass the path unquoted into a shell command line, so a
// path with more than one query parameter joined by an unescaped `&`
// gets split into separate shell tokens (see the sibling auditQueryPath
// constant in ops_survives_gateway_outage_test.go, which is safe there
// only because it is used for curl-exit reachability, never decoded).
// One parameter avoids the hazard; the default §25.9 24h look-back
// applies for the time bound.
const gatewayAuditQueryPath = "/v1/admin/audit-events?limit=1"

// opsDriftPath is the §25.10 drift-report endpoint on lenny-ops.
const opsDriftPath = "/v1/admin/drift"

// opsBackupsPath is the §25.11 backup-listing endpoint on lenny-ops.
const opsBackupsPath = "/v1/admin/backups"

// opsUpgradeCheckPath is the §25.10/§25.14 upgrade-availability endpoint
// on lenny-ops.
const opsUpgradeCheckPath = "/v1/admin/platform/upgrade-check"

// opsDownEndpoints is the set of lenny-ops-hosted operability endpoints
// the §25.15 "lenny-ops crash" row lists as unavailable during a
// lenny-ops outage: "Diagnostics, runbook index, drift, backup, and
// upgrade APIs are unavailable."
var opsDownEndpoints = []string{
	opsConnectivityEndpoint,
	opsRunbooksPath,
	opsDriftPath,
	opsBackupsPath,
	opsUpgradeCheckPath,
}

// spec: 25.15
// diagnosis: the §25.15 "lenny-ops crash" row did not hold end to end.
// The row states: "Gateway continues serving client traffic unaffected.
// Diagnostics, runbook index, drift, backup, and upgrade APIs are
// unavailable. The audit query API stays available because it is
// gateway-resident (Section 25.9)." The test scales the lenny-ops
// Deployment to zero (its Service loses its endpoints — a total
// lenny-ops outage) and asserts: (1) GET /v1/admin/audit-events on the
// gateway keeps returning 200 throughout the outage; and (2) the
// lenny-ops-hosted diagnostics, runbook index, drift, backup, and
// upgrade endpoints become unreachable (the request fails to connect
// rather than receiving any HTTP response) — the same "no response"
// unreachability the row's watchdog sentence describes for `/healthz`.
// A failure means either the audit query API is not actually
// gateway-resident and goes down with lenny-ops, or one of the
// lenny-ops-hosted endpoints keeps answering when it should be
// unreachable.
func TestGatewayAuditSurvivesOpsOutage(t *testing.T) {
	c := kind.InstallLenny(t)

	if !deploymentReady(t, c, gatewayDeployment) {
		t.Skipf("precondition not met: %s Deployment is not fully Ready (%s) before the chaos injection",
			gatewayDeployment, deploymentReadyState(t, c, gatewayDeployment))
	}
	if !deploymentReady(t, c, opsDeployment) {
		t.Skipf("precondition not met: %s Deployment is not fully Ready (%s) before the chaos injection",
			opsDeployment, deploymentReadyState(t, c, opsDeployment))
	}

	// An in-cluster probe pod reaches the gateway ClusterIP directly for
	// the gateway-resident audit query.
	probe := "chaos-ops-outage-audit-probe"
	gatewayIP := startGatewayProbePod(t, c, probe)

	// Port-forward reaches lenny-ops through the API-server tunnel, which
	// bypasses the §13.2 default-deny NetworkPolicy (bootstrap-labeled
	// pods have no egress path to lenny-ops; see allow-bootstrap-egress).
	// The forward is established while lenny-ops is up; once lenny-ops is
	// scaled to zero the backing pod is gone and requests over the same
	// tunnel fail to connect, which is exactly the unreachability this
	// test asserts.
	opsBase, _ := c.PortForward(t, "svc/"+opsDeployment, lennySystemNamespace, 8090)

	// --- Preconditions (lenny-ops UP) ----------------------------------

	// The gateway-resident audit query must answer 200 before the
	// injection so a later failure is attributable to the lenny-ops
	// outage and not to a pre-existing gateway problem.
	if p := curlGatewayAuth(t, c, probe, gatewayIP, gatewayAuditQueryPath); !p.ok(200) {
		t.Skipf("precondition not met: gateway %s is not 200 before the injection "+
			"(curl exit %d, status %d, body %q)", gatewayAuditQueryPath, p.curlExit, p.statusCode, p.body)
	}
	// Each lenny-ops-hosted endpoint must answer (any HTTP status — a
	// transport-level status 0 means opsHTTPGet could not even reach the
	// port-forward) before the injection, so the outage assertion below
	// proves the endpoint went from reachable to unreachable rather than
	// having been unreachable all along.
	for _, path := range opsDownEndpoints {
		if st, body := opsHTTPGet(t, opsBase, path); st == 0 {
			t.Skipf("precondition not met: lenny-ops %s is not reachable before the injection (body %q)",
				path, body)
		}
	}
	t.Logf("precondition: gateway Ready, lenny-ops Ready, gateway audit query 200, "+
		"%d lenny-ops-hosted endpoints reachable", len(opsDownEndpoints))

	// --- Inject: scale lenny-ops to zero --------------------------------

	opsReplicas := scaleDownAndRestore(t, c, opsDeployment)
	if !waitDeploymentScaledDown(t, c, opsDeployment, storeRecoveryBound) {
		t.Fatalf("%s did not scale down to zero replicas after the scale command", opsDeployment)
	}
	// The endpoint set must be empty before the outage assertions so
	// lenny-ops is genuinely unreachable — a request that still reached a
	// draining pod would not exercise the outage.
	endpointGone := pollUntil(storeRecoveryBound, 2*time.Second, func() bool {
		return endpointCount(t, c, opsDeployment) == 0
	})
	if !endpointGone {
		t.Fatalf("Service %s still has endpoints after the Deployment scaled to zero; "+
			"lenny-ops is not genuinely unreachable", opsDeployment)
	}
	t.Logf("injected: %s scaled to zero, Service has no endpoints; lenny-ops is unreachable", opsDeployment)

	// --- Assert (lenny-ops DOWN) ----------------------------------------

	// Assertion 1 — the gateway-resident audit query keeps answering 200.
	// §25.15: "The audit query API stays available because it is
	// gateway-resident (Section 25.9)."
	for i := 0; i < 5; i++ {
		if p := curlGatewayAuth(t, c, probe, gatewayIP, gatewayAuditQueryPath); !p.ok(200) {
			t.Errorf("gateway %s returned curl exit %d / status %d while lenny-ops is down; §25.15 requires "+
				"the audit query API to stay available during a lenny-ops outage because it is gateway-resident "+
				"(body %q)", gatewayAuditQueryPath, p.curlExit, p.statusCode, p.body)
			break
		}
		time.Sleep(2 * time.Second)
	}

	// Assertion 2 — every lenny-ops-hosted endpoint is unreachable.
	// §25.15: "Diagnostics, runbook index, drift, backup, and upgrade
	// APIs are unavailable ... (no response from `/healthz`)." A status
	// of 0 from opsHTTPGet means the request failed to connect rather
	// than receiving any HTTP response — the same "no response"
	// unavailability the row documents.
	for _, path := range opsDownEndpoints {
		if st, body := opsHTTPGet(t, opsBase, path); st != 0 {
			t.Errorf("lenny-ops %s returned status %d while lenny-ops is down; §25.15 requires this endpoint "+
				"to be unreachable during a lenny-ops outage (body %q)", path, st, body)
		}
	}
	t.Logf("asserted: gateway audit query stayed available, lenny-ops-hosted diagnostics/runbook/drift/backup/" +
		"upgrade endpoints were unreachable during the lenny-ops outage")

	// --- Restore and confirm recovery -----------------------------------

	restoreDeployment(t, c, opsDeployment, opsReplicas)
	recovered := pollUntil(storeRecoveryBound, 2*time.Second, func() bool {
		return deploymentReady(t, c, opsDeployment) && endpointCount(t, c, opsDeployment) > 0
	})
	if !recovered {
		t.Fatalf("%s did not return to Ready with Service endpoints within %s after restore (state %s, %d endpoints)",
			opsDeployment, storeRecoveryBound, deploymentReadyState(t, c, opsDeployment),
			endpointCount(t, c, opsDeployment))
	}
	t.Logf("recovery: lenny-ops restored to Ready with %d Service endpoints", endpointCount(t, c, opsDeployment))
}
