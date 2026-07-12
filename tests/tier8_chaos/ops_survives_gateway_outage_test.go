// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos test for the §25.15 Failure Mode Analysis "high-value
// scenario": lenny-ops being up while the gateway is down. §25.15 states
// this directly — "lenny-ops being up while the gateway is down is the
// high-value scenario — the ops surface remains available precisely when
// it's most needed." The §25.15 Gateway crash-loop row spells out the
// observable contract: "lenny-ops stays up. Watchdog calls diagnostics
// and fetches runbooks. Remediation steps that call the gateway admin API
// will fail, as will audit queries (the audit query API is gateway-
// resident, Section 25.9) ... Gateway appears as unreachable in
// connectivity check."
//
// The companion TestOpsSurvivesGatewayOutage in ops_survives_gateway_test.go
// asserts only that the lenny-ops Deployment stays Ready and does not
// crash-loop during a gateway outage. This test drives the ops HTTP
// surface end to end: it scales the gateway to zero (a genuine outage —
// the Service loses its endpoints), then asserts the lenny-ops
// operability surface still answers, that the gateway shows unreachable
// in the connectivity check, and that the gateway-resident audit query
// flips from reachable to unreachable — the precise §25.15 behaviors that
// no test at any tier previously exercised.

package tier8_chaos_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// opsRunbooksPath is the §25.7 runbook index on lenny-ops. §25.15's
// Gateway crash-loop row lists "fetches runbooks" among the operations
// that keep working during a gateway outage; runbook serving reads no
// gateway state, so the gateway outage must not change its behavior.
const opsRunbooksPath = "/v1/admin/runbooks"

// opsConnectivityEndpoint is the §25.6 connectivity path (kept separate
// from the runbook path so each assertion names the endpoint it drives).
const opsConnectivityEndpoint = "/v1/admin/diagnostics/connectivity"

// auditQueryPath is the §25.9 audit-log query endpoint. Per §25.1 the
// gateway serves the audit query API ("The gateway serves the audit log
// query API (Section 25.9)"), so it is gateway-resident: lenny-ops does
// not route it, and a client can reach it only through the gateway. The
// query spans a bounded window so a reachable gateway returns a normal
// HTTP status rather than hanging.
const auditQueryPath = "/v1/admin/audit-events?since=2026-01-01T00:00:00Z&until=2026-01-08T00:00:00Z&limit=1"

// opsHTTPGet issues an authenticated GET against the port-forwarded
// lenny-ops surface and returns the HTTP status and body. The dev-mode
// install (e2e-values.yaml devMode: true) honours the X-Lenny-* headers
// for identity and role, so an operability agent presents platform-admin.
// A transport error yields status 0 so the caller can distinguish "the
// ops surface did not answer" from any HTTP status it did return.
func opsHTTPGet(t *testing.T, baseURL, path string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		t.Fatalf("build request for %s: %v", path, err)
	}
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// gatewayDependencyReachable reports whether the §25.6 connectivity
// report's "gateway" dependency is reachable, and whether the report
// parsed at all. The report body is the §25.6 ConnectivityReport shape:
// {"healthy":bool,"dependencies":[{"name":...,"reachable":bool}, ...]}.
func gatewayDependencyReachable(body string) (reachable, parsed bool) {
	var report struct {
		Dependencies []struct {
			Name      string `json:"name"`
			Reachable bool   `json:"reachable"`
		} `json:"dependencies"`
	}
	if err := json.Unmarshal([]byte(body), &report); err != nil {
		return false, false
	}
	for _, d := range report.Dependencies {
		if d.Name == "gateway" {
			return d.Reachable, true
		}
	}
	return false, true
}

// spec: 25.15
// diagnosis: the §25.15 high-value scenario — "lenny-ops being up while
// the gateway is down ... the ops surface remains available precisely
// when it's most needed" — did not hold end to end. §25.15 Gateway
// crash-loop row: "lenny-ops stays up. Watchdog calls diagnostics and
// fetches runbooks. Remediation steps that call the gateway admin API
// will fail, as will audit queries (the audit query API is gateway-
// resident, Section 25.9) ... Gateway appears as unreachable in
// connectivity check." The test scales the gateway Deployment to zero
// (its Service loses its endpoints — a total gateway outage) and asserts:
// (1) lenny-ops still answers GET /v1/admin/diagnostics/connectivity with
// 200 during the outage (the ops surface stays available); (2) that
// report shows the gateway dependency as unreachable; (3) runbook fetching
// is unaffected — the runbook endpoint returns the same HTTP status it
// returned before the outage, because it reads no gateway state; and
// (4) the audit query is gateway-resident — lenny-ops does not serve it
// (404), and the gateway-hosted audit endpoint flips from reachable
// before the outage to unreachable during it. A failure means one of the
// §25.15 guarantees is broken: either the ops surface went down with the
// gateway, the connectivity check did not observe the gateway outage, or
// the audit query did not fail as §25.15/§25.9 require.
func TestOpsSurfaceSurvivesGatewayOutageEndToEnd(t *testing.T) {
	c := kind.InstallLenny(t)

	if !deploymentReady(t, c, gatewayDeployment) {
		t.Skipf("precondition not met: %s Deployment is not fully Ready (%s) before the chaos injection",
			gatewayDeployment, deploymentReadyState(t, c, gatewayDeployment))
	}
	if !deploymentReady(t, c, opsDeployment) {
		t.Skipf("precondition not met: %s Deployment is not fully Ready (%s) before the chaos injection",
			opsDeployment, deploymentReadyState(t, c, opsDeployment))
	}

	// An in-cluster probe pod reaches the gateway ClusterIP directly. The
	// §25.9 audit query API is gateway-resident, so this is the network
	// path a client uses to reach it; when the gateway is scaled to zero
	// the Service has no endpoints and the request fails to connect.
	probe := "chaos-gateway-outage-audit-probe"
	gatewayIP := startGatewayProbePod(t, c, probe)

	// Port-forward reaches lenny-ops through the API-server tunnel, which
	// bypasses the §13.2 external-only NetworkPolicy (the same escape
	// hatch the §25.15 Total-Outage Path D documents). The forward is the
	// operability agent's stand-in for the external Ingress path.
	opsBase, _ := c.PortForward(t, "svc/"+opsDeployment, lennySystemNamespace, 8090)

	// --- Preconditions (gateway UP) -----------------------------------

	// The connectivity endpoint must answer before the injection so a
	// later failure is attributable to the gateway outage and not to a
	// pre-existing lenny-ops problem.
	if st, body := opsHTTPGet(t, opsBase, opsConnectivityEndpoint); st != http.StatusOK {
		t.Skipf("precondition not met: lenny-ops %s is not 200 before the injection (status %d)\n%s",
			opsConnectivityEndpoint, st, body)
	}
	// Record the runbook endpoint's baseline status. §25.15 requires
	// runbook fetching to keep working during the outage; the assertion
	// below is that the outage does not change this status, whatever it is
	// (the dev install may or may not configure a runbook index — either
	// way the behavior is gateway-independent).
	runbookStatusBefore, _ := opsHTTPGet(t, opsBase, opsRunbooksPath)

	// The audit query is gateway-resident (§25.1): lenny-ops must not
	// serve it. A 404 from lenny-ops proves the query can only reach the
	// gateway, so the during-outage failure below is a gateway-residence
	// property rather than a lenny-ops outage.
	if st, _ := opsHTTPGet(t, opsBase, auditQueryPath); st != http.StatusNotFound {
		t.Fatalf("lenny-ops answered the §25.9 audit query %s with status %d; §25.1 makes the audit query API "+
			"gateway-resident, so lenny-ops must not serve it (expected 404)", auditQueryPath, st)
	}
	// The gateway-resident audit query must be reachable before the
	// injection (curl exit 0 means the gateway answered with some HTTP
	// status), so its during-outage failure is attributable to the outage.
	if p := curlGateway(t, c, probe, gatewayIP, auditQueryPath); p.curlExit != 0 {
		t.Skipf("precondition not met: the gateway-resident audit query is not reachable before the injection "+
			"(curl exit %d, status %d)", p.curlExit, p.statusCode)
	}
	t.Logf("precondition: gateway Ready, lenny-ops connectivity 200, runbooks status=%d, "+
		"audit query gateway-resident (ops 404) and reachable on the gateway", runbookStatusBefore)

	// --- Inject: scale the gateway to zero ----------------------------

	gatewayReplicas := scaleDownAndRestore(t, c, gatewayDeployment)
	if !waitDeploymentScaledDown(t, c, gatewayDeployment, storeRecoveryBound) {
		t.Fatalf("%s did not scale down to zero replicas after the scale command", gatewayDeployment)
	}
	// The endpoint set must be empty before the outage assertions so the
	// gateway is genuinely unreachable — a request that still reached a
	// draining pod would not exercise the outage.
	endpointGone := pollUntil(storeRecoveryBound, 2*time.Second, func() bool {
		return endpointCount(t, c, gatewayDeployment) == 0
	})
	if !endpointGone {
		t.Fatalf("Service %s still has endpoints after the Deployment scaled to zero; "+
			"the gateway is not genuinely unreachable", gatewayDeployment)
	}
	t.Logf("injected: %s scaled to zero, Service has no endpoints; the gateway is unreachable", gatewayDeployment)

	// --- Assert (gateway DOWN) ----------------------------------------

	// Assertion 1 — the ops surface stays available. §25.15: "the ops
	// surface remains available precisely when it's most needed." The
	// connectivity endpoint reads lenny-ops-local and Kubernetes state, so
	// it must keep returning 200 throughout the gateway outage.
	for i := 0; i < 5; i++ {
		st, body := opsHTTPGet(t, opsBase, opsConnectivityEndpoint)
		if st != http.StatusOK {
			t.Errorf("lenny-ops %s returned status %d during the gateway outage; §25.15 requires the ops "+
				"surface to remain available while the gateway is down\n%s", opsConnectivityEndpoint, st, body)
			break
		}
		// Assertion 2 — the gateway appears unreachable in the connectivity
		// check. §25.15: "Gateway appears as unreachable in connectivity
		// check." §25.6: the connectivity report probes the gateway admin
		// API and lists it as a failed dependency when unreachable.
		reachable, parsed := gatewayDependencyReachable(body)
		if !parsed {
			t.Errorf("lenny-ops %s body did not parse as a §25.6 connectivity report during the outage\n%s",
				opsConnectivityEndpoint, body)
			break
		}
		if reachable {
			t.Errorf("the §25.6 connectivity report lists the gateway as reachable during the gateway outage; "+
				"§25.15 requires the gateway to appear as unreachable in the connectivity check\n%s", body)
			break
		}
		time.Sleep(1 * time.Second)
	}

	// Assertion 3 — runbook fetching is unaffected by the gateway outage.
	// §25.15 lists "fetches runbooks" among the operations that keep
	// working; runbook serving reads no gateway state, so its HTTP status
	// must be unchanged from the pre-outage baseline.
	if st, body := opsHTTPGet(t, opsBase, opsRunbooksPath); st != runbookStatusBefore {
		t.Errorf("lenny-ops %s returned status %d during the gateway outage, was %d before; §25.15 requires "+
			"runbook fetching to keep working (its behavior does not depend on the gateway)\n%s",
			opsRunbooksPath, st, runbookStatusBefore, body)
	}

	// Assertion 4 — the gateway-resident audit query fails during the
	// outage. §25.15: "as will audit queries (the audit query API is
	// gateway-resident, Section 25.9)." With the gateway scaled to zero
	// the Service has no endpoints, so the request fails to connect (curl
	// exit != 0) rather than returning an HTTP status.
	if p := curlGateway(t, c, probe, gatewayIP, auditQueryPath); p.curlExit == 0 {
		t.Errorf("the gateway-resident §25.9 audit query returned HTTP status %d during the gateway outage; "+
			"§25.15 requires audit queries to fail while the gateway is down", p.statusCode)
	}
	// lenny-ops still does not serve the audit query during the outage —
	// it never did (§25.1 gateway-residence), so an agent cannot fall back
	// to lenny-ops for audit data.
	if st, _ := opsHTTPGet(t, opsBase, auditQueryPath); st != http.StatusNotFound {
		t.Errorf("lenny-ops answered the §25.9 audit query with status %d during the outage; the audit query "+
			"API is gateway-resident and lenny-ops must not serve it (expected 404)", st)
	}
	t.Logf("asserted: ops surface available (connectivity 200, gateway unreachable), runbooks unchanged, " +
		"gateway-resident audit query failed during the outage")

	// --- Restore and confirm recovery ---------------------------------

	restoreDeployment(t, c, gatewayDeployment, gatewayReplicas)
	recovered := pollUntil(storeRecoveryBound, 2*time.Second, func() bool {
		return deploymentReady(t, c, gatewayDeployment) && endpointCount(t, c, gatewayDeployment) > 0
	})
	if !recovered {
		t.Fatalf("%s did not return to Ready with Service endpoints within %s after restore (state %s, %d endpoints)",
			gatewayDeployment, storeRecoveryBound, deploymentReadyState(t, c, gatewayDeployment),
			endpointCount(t, c, gatewayDeployment))
	}
	// The gateway-resident audit query is reachable again once the gateway
	// is back, closing the loop on the §25.15 scenario.
	auditBack := pollUntil(storeRecoveryBound, 2*time.Second, func() bool {
		return curlGateway(t, c, probe, gatewayIP, auditQueryPath).curlExit == 0
	})
	if !auditBack {
		t.Errorf("the gateway-resident audit query did not become reachable again within %s after the gateway "+
			"was restored", storeRecoveryBound)
	}
	t.Logf("recovery: gateway restored to Ready with %d Service endpoints; audit query reachable again",
		endpointCount(t, c, gatewayDeployment))
}
