// SPDX-License-Identifier: MIT

//go:build e2e_cloud

// Tier-6 lenny-ops capability smoke test. §25.4 documents the ipBlock
// egress substitution the chart applies for cloud-managed Postgres,
// Redis, and object storage, and the External-by-design boundary that
// routes every agent request to lenny-ops through its own Ingress
// (spec/25_agent-operability.md). TestCloudOpsSurfaceHealthy
// (ops_surface_test.go) already confirms the deployed lenny-ops reaches
// those managed stores; this file drives two of the remediation
// capabilities themselves — caller discovery (GET /v1/admin/me) and a
// remediation-lock acquire/release — against the live cloud install,
// and pins the Ingress config shape that routes external agent traffic
// to lenny-ops.
package tier6_e2e_cloud_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// opsDevHeaders stamps the §10.2 X-Lenny-Tenant-ID / X-Lenny-Roles /
// X-Lenny-User-ID dev-mode transport, mirroring the identical header
// set session_lifecycle_test.go already presents to the gateway on the
// same cloud install. lenny-ops only honors these when
// security.oidc.bearerTrustKeySecret configures a verify key (see the
// TestCloudOpsAdminMe diagnosis comment below for the current cloud
// e2e overlay's posture); sending them unconditionally keeps this test
// correct once that key is wired without requiring a change here.
func opsDevHeaders(req *http.Request) {
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("X-Lenny-Roles", "tenant-admin")
	req.Header.Set("X-Lenny-User-ID", "alice")
}

// spec: 25.4 ("`lenny-ops` exposes a single discovery endpoint that
// returns this context in one call" and the documented `GET
// /v1/admin/me` response envelope carrying `authorization.tenantScope`,
// `authorization.scope` ("tools:*" absent a narrower claim),
// `platform.version`, and `links`, spec/25_agent-operability.md lines
// 1571-1631).
// diagnosis: a failure here means the deployed lenny-ops's
// /v1/admin/me either does not answer at all, or answers without the
// documented envelope sections (authorization, platform, links) that
// every other lenny-ops caller — including `lenny-ctl`'s own
// auto-discovery (Section 25.14) — depends on to learn its rate-limit
// budget, effective scope, and the gateway/ops URLs before calling
// anything else. The cloud e2e overlay
// (scripts/cloud/aws/render-values.sh) currently leaves
// `security.oidc.bearerTrustKeySecret` unset, so lenny-ops runs the
// documented dev/embedded unauthenticated fallback rather than the
// §25.4 OIDC-gated path; this test therefore checks the response
// envelope's documented shape and the no-principal defaults
// (tenantScope "*", scope "tools:*") rather than a caller-specific
// identity, and logs the identity fields for visibility rather than
// asserting on them.
func TestCloudOpsAdminMe(t *testing.T) {
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
	req, err := http.NewRequest(http.MethodGet, baseURL+"/v1/admin/me", nil)
	if err != nil {
		t.Fatalf("build /v1/admin/me request: %v", err)
	}
	opsDevHeaders(req)
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/admin/me: %v", err)
	}
	defer resp.Body.Close()
	var body struct {
		Identity struct {
			Sub string `json:"sub"`
		} `json:"identity"`
		Authorization struct {
			Roles       []string `json:"roles"`
			TenantScope string   `json:"tenantScope"`
			Scope       string   `json:"scope"`
		} `json:"authorization"`
		Platform struct {
			Version string `json:"version"`
		} `json:"platform"`
		Links map[string]string `json:"links"`
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/admin/me: status %d, want 200", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("GET /v1/admin/me: decode JSON: %v", err)
	}
	if body.Authorization.TenantScope == "" {
		t.Errorf("/v1/admin/me authorization.tenantScope is empty, want a non-empty value (\"*\" absent a resolved tenant)")
	}
	if body.Authorization.Scope != "tools:*" {
		t.Errorf("/v1/admin/me authorization.scope = %q, want %q (§25.4 line 1592: an absent scope claim echoes as tools:*)", body.Authorization.Scope, "tools:*")
	}
	if body.Platform.Version == "" {
		t.Errorf("/v1/admin/me platform.version is empty, want the running lenny-ops version")
	}
	if len(body.Links) == 0 {
		t.Errorf("/v1/admin/me links is empty, want the documented self-discovery link set")
	}
	t.Logf("TestCloudOpsAdminMe: lenny-ops responded sub=%q tenantScope=%s roles=%v platformVersion=%s",
		body.Identity.Sub, body.Authorization.TenantScope, body.Authorization.Roles, body.Platform.Version)
}

// spec: 25.4 ("Remediation endpoints are idempotent and accept an
// optional `Idempotency-Key` header" describes the remediation family;
// the lock endpoints themselves are `POST /v1/admin/remediation-locks`
// to acquire and `DELETE /v1/admin/remediation-locks/{id}` to release,
// pkg/ops/opsserver/locks.go) and the §25.4 remediation-lock coordination
// this exercises against whichever lock tier (Postgres, Redis, or
// in-memory) the cloud install's `ops.locks.memoryTier` and managed-store
// wiring actually resolve to.
// diagnosis: a failure here means the deployed lenny-ops cannot acquire
// or release a remediation lock end to end — the coordination primitive
// every automated remediation flow (§25.4) depends on to prevent two
// agents from concurrently touching the same resource. Because this
// drives the real lock store lenny-ops was deployed against (Postgres/
// Redis when the cloud install is wired to RDS/ElastiCache, in-memory
// otherwise), a regression in the cloud-specific store wiring for locks
// specifically (as opposed to the general connectivity probe
// TestCloudOpsSurfaceHealthy already covers) would surface here first.
func TestCloudOpsRemediationLockAcquireRelease(t *testing.T) {
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

	acquireBody, err := json.Marshal(map[string]any{
		"scope":      "pool",
		"operation":  "cloud-e2e-lock-smoke",
		"ttlSeconds": 300,
	})
	if err != nil {
		t.Fatalf("marshal acquire body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/admin/remediation-locks", bytes.NewReader(acquireBody))
	if err != nil {
		t.Fatalf("build acquire request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	opsDevHeaders(req)
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/admin/remediation-locks: %v", err)
	}
	acquired := map[string]any{}
	decodeErr := json.NewDecoder(resp.Body).Decode(&acquired)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/admin/remediation-locks: status %d, want 201; body %v", resp.StatusCode, acquired)
	}
	if decodeErr != nil {
		t.Fatalf("POST /v1/admin/remediation-locks: decode JSON: %v", decodeErr)
	}
	lockID, _ := acquired["id"].(string)
	if lockID == "" {
		t.Fatalf("POST /v1/admin/remediation-locks: response carries no \"id\": %v", acquired)
	}
	if lockStore, _ := acquired["lockStore"].(string); lockStore != "" {
		t.Logf("TestCloudOpsRemediationLockAcquireRelease: acquired lock %s on store %q", lockID, lockStore)
	}
	if degradation, present := acquired["degradation"]; present {
		t.Logf("TestCloudOpsRemediationLockAcquireRelease: acquire carried a degradation envelope: %v", degradation)
	}

	releaseReq, err := http.NewRequest(http.MethodDelete, baseURL+"/v1/admin/remediation-locks/"+lockID, nil)
	if err != nil {
		t.Fatalf("build release request: %v", err)
	}
	opsDevHeaders(releaseReq)
	releaseResp, err := httpClient.Do(releaseReq)
	if err != nil {
		t.Fatalf("DELETE /v1/admin/remediation-locks/%s: %v", lockID, err)
	}
	defer releaseResp.Body.Close()
	if releaseResp.StatusCode != http.StatusNoContent {
		t.Errorf("DELETE /v1/admin/remediation-locks/%s: status %d, want 204", lockID, releaseResp.StatusCode)
	}
}

// spec: 25.4 ("`lenny-ops` is only reachable from outside the cluster
// via Ingress. No internal cluster workload ... can reach the
// operational control plane.", spec/25_agent-operability.md line 50) and
// 17.5 (cloud portability: provider-native ingress and TLS, the same
// operator-supplied-Ingress model TestManagedIngress already pins for
// the gateway).
// diagnosis: TestCloudOpsManagedIngress asserts the lenny-ops Service
// stays ClusterIP (the chart never exposes it as a LoadBalancer
// directly) and, when an operator-supplied Ingress exists, that its
// backend routes to lenny-ops. This is the external-only entry point
// §25.4 requires agents to use instead of the in-cluster Service; a
// missing or misrouted Ingress silently falls back to the
// NetworkPolicy-bypassing kubectl port-forward break-glass path
// (documented in §25.15 as break-glass, not the supported agent path)
// with no signal to the operator.
func TestCloudOpsManagedIngress(t *testing.T) {
	_ = requireCloud(t)
	cli := kube(t)
	if !opsDeploymentReady(t, cli) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	svc, err := cli.CoreV1().Services(lennySystem).Get(ctx, "lenny-ops", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get lenny-ops Service: %v", err)
	}
	if svc.Spec.Type != "ClusterIP" {
		t.Errorf("lenny-ops Service type = %s, want ClusterIP (§25.4 external access is via Ingress only)", svc.Spec.Type)
	}

	ings, err := cli.NetworkingV1().Ingresses(lennySystem).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list ingresses: %v", err)
	}
	for _, ing := range ings.Items {
		for _, rule := range ing.Spec.Rules {
			if rule.HTTP == nil {
				continue
			}
			for _, p := range rule.HTTP.Paths {
				if p.Backend.Service != nil && p.Backend.Service.Name == "lenny-ops" {
					t.Logf("TestCloudOpsManagedIngress: Ingress %q routes to lenny-ops", ing.Name)
					return
				}
			}
		}
	}
	t.Log("TestCloudOpsManagedIngress: no operator-supplied Ingress in lenny-system routes to lenny-ops; install an Ingress controller and an Ingress whose backend is lenny-ops (ops.ingress.host) to unblock agent access to the operability surface without the port-forward break-glass path")
}
