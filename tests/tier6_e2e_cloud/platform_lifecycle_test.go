// SPDX-License-Identifier: MIT

//go:build e2e_cloud

// Tier-6 §25.8 Platform Lifecycle Management parity coverage. Prior to
// this file, no tier-6 suite drove the version-aggregation or
// cert-manager-integration surfaces against a live managed cluster: the
// upgrade state machine, version drift reporting, and cert-manager
// health derivation were only exercised against fakes and envtest
// (pkg/ops/upgradeservice, cmd/lenny-ops/cert_manager_source_test.go).
// This file closes the parity-matrix gap for those two capabilities
// with read-only observations against the cloud-deployed lenny-ops: a
// no-op version-aggregation call (no upgrade is started) and a
// cert-manager health-state observation (no certificate is rotated).

package tier6_e2e_cloud_test

import (
	"net/http"
	"testing"
	"time"
)

// spec: §25.8 ("`GET /v1/admin/platform/version/full` aggregates: -
// Gateway binary metadata from `GatewayClient.GetVersion()` ... -
// `lenny-ops` binary metadata (local — compiled-in via `ldflags`). -
// Controller Deployment versions from K8s API. - CRD versions from K8s
// API. - Helm chart version from K8s API (`helm.sh/release.v1` Secret).
// - Postgres schema version from `SELECT version FROM schema_migrations
// ORDER BY version DESC LIMIT 1`.", spec/25_agent-operability.md).
// diagnosis: a failure here means the deployed lenny-ops's
// GET /v1/admin/platform/version/full either does not answer, or
// answers without the compiled-in "ops" component every report carries
// (VersionAggregator anchors the report on lenny-ops's own build
// version, cmd/lenny-ops/deps.go buildVersionAggregator). This is a
// read-only aggregation call — it starts no upgrade and mutates no
// state — so a regression here means the version-introspection path an
// agent calls before any upgrade decision is broken on a real managed
// cluster, a gap tier2 (tests/tier2_component/observability/version_aggregation_test.go)
// cannot detect because it runs the aggregator against fakes rather
// than a live gateway/K8s-API/Postgres stack behind a real Ingress.
func TestCloudPlatformVersionAggregation(t *testing.T) {
	_ = requireCloud(t)
	cli := kube(t)
	if !opsDeploymentReady(t, cli) {
		return
	}

	baseURL, stop := portForwardOpsCloud(t, cli)
	if baseURL == "" {
		return
	}
	defer stop()

	httpClient := &http.Client{Timeout: 30 * time.Second}
	body := opsGetJSON(t, httpClient, baseURL+"/v1/admin/platform/version/full")

	requiredVersion, _ := body["requiredVersion"].(string)
	if requiredVersion == "" {
		t.Errorf("GET /v1/admin/platform/version/full: requiredVersion is empty, want the running lenny-ops build version")
	}

	components, ok := body["components"].([]any)
	if !ok || len(components) == 0 {
		t.Fatalf("GET /v1/admin/platform/version/full: components is empty or not an array: %v", body)
	}

	var opsComponent map[string]any
	for _, c := range components {
		m, isMap := c.(map[string]any)
		if !isMap {
			continue
		}
		if name, _ := m["name"].(string); name == "ops" {
			opsComponent = m
			break
		}
	}
	if opsComponent == nil {
		t.Fatalf("GET /v1/admin/platform/version/full: no \"ops\" component in components: %v", components)
	}
	if available, _ := opsComponent["available"].(bool); !available {
		t.Errorf("GET /v1/admin/platform/version/full: \"ops\" component available=false, want true (lenny-ops always reports its own compiled-in version)")
	}
	if current, _ := opsComponent["current"].(string); current == "" {
		t.Errorf("GET /v1/admin/platform/version/full: \"ops\" component current version is empty")
	}

	t.Logf("TestCloudPlatformVersionAggregation: requiredVersion=%s versionDrift=%v components=%v",
		requiredVersion, body["versionDrift"], components)
}

// spec: §25.8 Cert-Manager Integration ("`lenny-ops`'s health API probes
// cert-manager's certificate status (Section 25.3, Data Sources). The
// `certManager` component in the health response reports: `healthy` —
// all Lenny-managed certificates are renewed and valid for >30 days.
// `degraded` — at least one certificate expires within 30 days AND
// renewal has failed in the last attempt. `unhealthy` — at least one
// certificate expires within 7 days OR has already expired.",
// spec/25_agent-operability.md).
// diagnosis: a failure here means the deployed lenny-ops's
// GET /v1/admin/ops/health does not carry a cert-manager self-health
// check (opsservice.CheckCertManager, wired in
// cmd/lenny-ops/services_wiring.go whenever a K8s dynamic client is
// available, which it always is on a real cluster), or reports a
// status outside the three documented states. This is a read-only
// observation of the check the CertExpiryImminent alert (§25.13) and
// the cert-manager-outage runbook (§25.7) key off — no certificate is
// rotated or mutated. envtest coverage
// (cmd/lenny-ops/cert_manager_source_test.go) already pins the
// classification logic against a fake dynamic client; this closes the
// parity gap that no test drove the same check against a live
// dynamic client on a real managed cluster.
func TestCloudCertManagerHealthObservation(t *testing.T) {
	_ = requireCloud(t)
	cli := kube(t)
	if !opsDeploymentReady(t, cli) {
		return
	}

	baseURL, stop := portForwardOpsCloud(t, cli)
	if baseURL == "" {
		return
	}
	defer stop()

	httpClient := &http.Client{Timeout: 30 * time.Second}
	body := opsGetJSON(t, httpClient, baseURL+"/v1/admin/ops/health")

	checks, ok := body["checks"].([]any)
	if !ok || len(checks) == 0 {
		t.Fatalf("GET /v1/admin/ops/health: checks is empty or not an array: %v", body)
	}

	var certCheck map[string]any
	for _, c := range checks {
		m, isMap := c.(map[string]any)
		if !isMap {
			continue
		}
		if name, _ := m["name"].(string); name == "cert_manager" {
			certCheck = m
			break
		}
	}
	if certCheck == nil {
		t.Fatalf("GET /v1/admin/ops/health: no \"cert_manager\" check in checks: %v", checks)
	}

	status, _ := certCheck["status"].(string)
	switch status {
	case "healthy", "degraded", "unhealthy":
		// One of the three §25.8 documented states.
	default:
		t.Errorf("GET /v1/admin/ops/health: cert_manager check status = %q, want one of healthy, degraded, or unhealthy", status)
	}

	t.Logf("TestCloudCertManagerHealthObservation: cert_manager check status=%s detail=%v", status, certCheck["detail"])
}
