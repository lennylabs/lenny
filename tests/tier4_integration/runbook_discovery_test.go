//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration test for the §25.7 Path B agent discovery loop, run
// across the two components that own its ends: the gateway health service
// stamps a degraded component with a `suggestedAction.runbook` name, and
// the real cmd/lenny-ops runbook index must resolve that name through
// GET /v1/admin/runbooks/{name} and GET /v1/admin/runbooks/{name}/steps.
//
// The join between the gateway's issue → runbook map and the lenny-ops
// runbook index is otherwise only pinned against in-process stubs
// (tier-3 ops_endpoints). A runbook name the gateway emits that the
// lenny-ops index cannot resolve breaks the closed loop and would
// otherwise ship undetected, because the gateway does not validate the
// name against lenny-ops by design (spec §25.7 line 3238). This test
// drives the real lenny-ops process reading the bundled docs/runbooks/
// so a divergence is caught.
//
// The gateway end runs the real health.Handler over an httptest server
// with a checker forced into WARM_POOL_EXHAUSTED (the exhausted-pool
// state a warm-pool checker stamps). Driving a live warm pool to
// exhaustion through the deployed gateway needs a real cluster and is a
// tier-5 (Kind) e2e concern; at tier-4 the health JSON the exhausted
// pool would serialize is produced by the real aggregation code, and the
// cross-component runbook resolution it feeds runs against the real
// lenny-ops process.
package tier4_integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/operability/health"
	"github.com/lennylabs/lenny/tests/testinfra/opsprocess"
)

// specRequiredPathBIssues is the §17.7 line 776 set of health-API issue
// codes the spec names as required by §25.7 Path B, each mapped to the
// runbook the gateway surfaces for it. Every one of these names must
// resolve in the lenny-ops runbook index for the closed loop to hold.
// spec: §25.7 lines 3221-3238 (Path B: the runbook field is the name used
// in GET /v1/admin/runbooks/{name}; the mapping is unvalidated by
// convention); §17.7 line 776 (the eight codes required by Path B).
var specRequiredPathBIssues = map[string]string{
	"WARM_POOL_EXHAUSTED":       "warm-pool-exhaustion",
	"WARM_POOL_LOW":             "warm-pool-exhaustion",
	"CREDENTIAL_POOL_EXHAUSTED": "credential-pool-exhaustion",
	"POSTGRES_UNREACHABLE":      "postgres-failover",
	"REDIS_UNREACHABLE":         "redis-failure",
	"MINIO_UNREACHABLE":         "minio-failure",
	"CERT_EXPIRY_IMMINENT":      "cert-manager-outage",
	"CIRCUIT_BREAKER_OPEN":      "gateway-replica-failure",
}

// startGatewayHealth serves the real §25.3 health.Handler over an httptest
// server, with a single checker that reports the component unhealthy under
// the given issue code. The aggregator back-fills the §25.3
// suggestedAction(s) and their §25.7 Path B runbook from the issue code,
// exactly as the deployed gateway would serialize the state.
func startGatewayHealth(t *testing.T, component, issue string) string {
	t.Helper()
	agg := health.NewAggregator()
	agg.Register(health.CheckerFunc{
		ComponentName: component,
		Fn: func(context.Context) health.Component {
			return health.Component{
				Name:   component,
				Status: health.StatusUnhealthy,
				Issue:  issue,
			}
		},
	})
	srv := httptest.NewServer(health.Handler(agg, nil))
	t.Cleanup(srv.Close)
	return srv.URL
}

// runbookFromHealth calls GET /v1/admin/health on the gateway health
// server, finds the degraded component, and returns the primary §25.7
// Path B runbook name it carries: suggestedActions[0].runbook for a
// ranked issue, or suggestedAction.runbook for a singular one.
func runbookFromHealth(t *testing.T, gwBase string) string {
	t.Helper()
	resp, err := http.Get(gwBase + "/v1/admin/health")
	if err != nil {
		t.Fatalf("GET gateway /v1/admin/health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway health status = %d, want 200", resp.StatusCode)
	}
	var report struct {
		Components []struct {
			Name            string `json:"name"`
			SuggestedAction *struct {
				Runbook string `json:"runbook"`
			} `json:"suggestedAction"`
			SuggestedActions []struct {
				Runbook string `json:"runbook"`
			} `json:"suggestedActions"`
		} `json:"components"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		t.Fatalf("decode gateway health: %v", err)
	}
	if len(report.Components) == 0 {
		t.Fatal("gateway health carried no components")
	}
	comp := report.Components[0]
	if len(comp.SuggestedActions) > 0 {
		return comp.SuggestedActions[0].Runbook
	}
	if comp.SuggestedAction != nil {
		return comp.SuggestedAction.Runbook
	}
	t.Fatalf("component %q carried no suggestedAction runbook", comp.Name)
	return ""
}

// getJSON issues a GET against the lenny-ops surface and returns the
// status code and decoded body.
func getJSON(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out
}

// TestWarmPoolExhaustionRunbookDiscoveryLoopE2E walks the §25.7 Path B
// closed loop end to end for the WARM_POOL_EXHAUSTED case: an exhausted
// warm pool surfaces on the gateway health API with a
// suggestedAction.runbook, and the real lenny-ops runbook index resolves
// that name to full markdown and to structured steps carrying the api
// access path an external watchdog uses.
//
// spec: §25.7 lines 3221 ("The runbook field is the runbook name as used
// in GET /v1/admin/runbooks/{name}. This closes the loop ... agent calls
// health API → sees degraded component with suggested action → fetches
// the linked runbook"), 3062-3063 (/{name} full markdown, /{name}/steps
// structured representation), 3049-3051 (an external watchdog with only
// API access uses the api path).
// diagnosis: a failure means the §25.7 Path B closed loop is broken
// across the gateway and lenny-ops. Either the gateway health service did
// not surface suggestedAction.runbook for an exhausted warm pool, or the
// runbook name it emits does not resolve in the lenny-ops runbook index
// (GET /v1/admin/runbooks/{name} 404, or /steps empty of api paths) — the
// join the gateway does not validate at runtime by design has diverged.
func TestWarmPoolExhaustionRunbookDiscoveryLoopE2E(t *testing.T) {
	opsprocess.SkipUnlessAvailable(t)

	ops := opsprocess.StartWith(t)
	opsBase := ops.BaseURL()

	gwBase := startGatewayHealth(t, "warmPools", "WARM_POOL_EXHAUSTED")
	runbook := runbookFromHealth(t, gwBase)
	if runbook != "warm-pool-exhaustion" {
		t.Fatalf("gateway health suggestedAction.runbook = %q, want warm-pool-exhaustion", runbook)
	}

	// Hop 1: the full-markdown fetch (§25.7 line 3062).
	code, body := getJSON(t, opsBase+"/v1/admin/runbooks/"+runbook)
	if code != http.StatusOK {
		t.Fatalf("GET /v1/admin/runbooks/%s: status %d (%v) — the gateway-emitted runbook name did not resolve in the lenny-ops index", runbook, code, body)
	}
	if name, _ := body["name"].(string); name != runbook {
		t.Errorf("runbook name = %q, want %q", name, runbook)
	}
	if md, _ := body["markdown"].(string); md == "" {
		t.Errorf("runbook %s resolved with empty markdown", runbook)
	}

	// Hop 2: the structured steps (§25.7 line 3063). At least one step
	// must expose the api access path so an external watchdog agent with
	// only API access can execute it (§25.7 lines 3049-3051).
	code, stepsBody := getJSON(t, opsBase+"/v1/admin/runbooks/"+runbook+"/steps")
	if code != http.StatusOK {
		t.Fatalf("GET /v1/admin/runbooks/%s/steps: status %d (%v)", runbook, code, stepsBody)
	}
	steps, _ := stepsBody["steps"].([]any)
	if len(steps) == 0 {
		t.Fatalf("runbook %s resolved with no structured steps", runbook)
	}
	if !hasAPIAccessPath(steps) {
		t.Errorf("runbook %s steps carried no api access path; an external watchdog cannot execute it", runbook)
	}
}

// hasAPIAccessPath reports whether any step exposes an access path with
// access == "api" (the §25.7 form an API-only watchdog executes).
func hasAPIAccessPath(steps []any) bool {
	for _, s := range steps {
		step, ok := s.(map[string]any)
		if !ok {
			continue
		}
		paths, _ := step["paths"].([]any)
		for _, p := range paths {
			path, ok := p.(map[string]any)
			if !ok {
				continue
			}
			if access, _ := path["access"].(string); access == "api" {
				return true
			}
		}
	}
	return false
}

// TestPathBRequiredRunbooksResolveInOpsIndexE2E verifies the full §25.7
// Path B join: every issue code the spec names as required by Path B
// (§17.7 line 776) surfaces a runbook name on the gateway health API, and
// every such name resolves in the real lenny-ops runbook index. This is
// the guarantee the gateway itself does not enforce — it emits the name
// without validating it exists (§25.7 line 3238), so the convention that
// each name has a bundled runbook is only ever checked here across the
// two components.
//
// spec: §25.7 lines 3221-3238 (Path B closed loop; the name is the one
// used in GET /v1/admin/runbooks/{name}; the mapping is maintained by
// convention and unvalidated by the gateway); §17.7 line 776 (the eight
// required Path B issue codes).
// diagnosis: a failure names an issue code the gateway health API can
// emit whose runbook the lenny-ops index cannot resolve. The §25.7 Path B
// closed loop is broken for that code: an agent that follows the health
// API's suggestedAction.runbook receives a RUNBOOK_NOT_FOUND from
// lenny-ops and cannot reach the remediation steps.
func TestPathBRequiredRunbooksResolveInOpsIndexE2E(t *testing.T) {
	opsprocess.SkipUnlessAvailable(t)

	ops := opsprocess.StartWith(t)
	opsBase := ops.BaseURL()

	for issue, want := range specRequiredPathBIssues {
		t.Run(issue, func(t *testing.T) {
			gwBase := startGatewayHealth(t, "component-under-"+issue, issue)
			runbook := runbookFromHealth(t, gwBase)
			if runbook != want {
				t.Fatalf("issue %s surfaced runbook %q, want %q", issue, runbook, want)
			}
			code, body := getJSON(t, opsBase+"/v1/admin/runbooks/"+runbook)
			if code != http.StatusOK {
				t.Fatalf("issue %s → runbook %q: GET /v1/admin/runbooks/%s status %d (%v); the Path B loop does not close", issue, runbook, runbook, code, body)
			}
			code, stepsBody := getJSON(t, opsBase+"/v1/admin/runbooks/"+runbook+"/steps")
			if code != http.StatusOK {
				t.Fatalf("issue %s → runbook %q: /steps status %d (%v)", issue, runbook, code, stepsBody)
			}
		})
	}
}
