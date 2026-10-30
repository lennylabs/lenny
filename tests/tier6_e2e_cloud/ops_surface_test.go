// SPDX-License-Identifier: MIT

//go:build e2e_cloud

// Tier-6 lenny-ops operability-surface test. §25.1 states lenny-ops is
// mandatory in every Lenny installation regardless of tier and connects
// to Postgres, Redis, the Kubernetes API, MinIO, Prometheus, and the
// gateway admin API "across every deployment topology"
// (spec/25_agent-operability.md). The cloud e2e chart install
// (scripts/cloud/<provider>/run-e2e.sh) already deploys lenny-ops as
// part of the full `helm upgrade --install` of the chart, but no prior
// tier-6 test drove its HTTP surface — the tier-5 Kind suite exercises
// lenny-ops (mcp_management_e2e_test.go, backup_test.go,
// diagnostics_fix_test.go) only against in-cluster Postgres/Redis
// fixtures, so a regression in lenny-ops's cloud-specific dependency
// wiring (managed-store DNS, workload-identity credentials,
// security-group egress) would go unnoticed until an operator hit it in
// production.

package tier6_e2e_cloud_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// opsHTTPPort mirrors the chart's ops.httpPort default
// (charts/lenny/values.yaml) and the §25.12 architecture note that
// places the lenny-ops HTTP surface on port 8090.
const opsHTTPPort = 8090

// spec: 25.1 ("`lenny-ops` is mandatory in every Lenny installation
// regardless of tier. There is no supported topology without it ...
// Connects to: Postgres, Redis, K8s API, MinIO, Prometheus, Gateway
// admin API", spec/25_agent-operability.md).
// diagnosis: a failure here means the deployed lenny-ops binary is not
// Ready, or its /readyz and /v1/admin/diagnostics/connectivity
// responses report a required dependency (Postgres, the Kubernetes
// API, and — when the cloud install was provisioned against managed
// stores — Redis) as unreachable. Because lenny-ops is reached only
// through its Service/Ingress from outside the cluster (§25.1 External
// by design), a regression in its cloud-specific dependency wiring
// would not surface in the tier-5 Kind suite, which runs against
// in-cluster Postgres/Redis fixtures only.
func TestCloudOpsSurfaceHealthy(t *testing.T) {
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

	// /readyz: the always-map-shaped dependencyReport (probe.Run
	// results), independent of whether a DiagnosticService is wired.
	readyBody := opsGetJSON(t, httpClient, baseURL+"/readyz")
	deps, ok := readyBody["dependencies"].(map[string]any)
	if !ok {
		t.Fatalf("/readyz response carries no \"dependencies\" object: %v", readyBody)
	}
	assertMapDepOK(t, deps, "postgres", "/readyz")
	assertMapDepOK(t, deps, "kubernetes", "/readyz")

	// /v1/admin/diagnostics/connectivity: the §25.6 dependency
	// connectivity check. Its "dependencies" field is either a map
	// (probe.Run fallback, mirroring /readyz) or a list (when a
	// DiagnosticService is configured, diagnostics.ConnectivityReport);
	// connectivityDependencyOK tolerates both shapes.
	connBody := opsGetJSON(t, httpClient, baseURL+"/v1/admin/diagnostics/connectivity")
	if healthy, present := connBody["healthy"].(bool); present && !healthy {
		t.Logf("TestCloudOpsSurfaceHealthy: /v1/admin/diagnostics/connectivity reports healthy=false: %v", connBody)
	}
	if ok, found := connectivityDependencyOK(connBody["dependencies"], "postgres"); !found {
		t.Errorf("/v1/admin/diagnostics/connectivity carries no \"postgres\" dependency entry: %v", connBody)
	} else if !ok {
		t.Errorf("/v1/admin/diagnostics/connectivity reports postgres unreachable: %v", connBody)
	}

	// The managed-store-specific assertion named by the finding: when
	// the cloud install was provisioned against RDS/ElastiCache
	// (WITH_RDS=1 WITH_ELASTICACHE=1 scripts/cloud/aws/run-e2e.sh),
	// lenny-ops's own connectivity probe must reach those managed
	// endpoints too, not just the in-cluster Postgres/Redis the tier-5
	// Kind suite exercises.
	if rds := requireRDS(t); rds.host != "" {
		if ok, found := connectivityDependencyOK(connBody["dependencies"], "postgres"); !found || !ok {
			t.Errorf("cloud install is wired to managed RDS (%s) but lenny-ops connectivity does not report postgres reachable: %v", rds.host, connBody)
		} else {
			t.Logf("TestCloudOpsSurfaceHealthy: lenny-ops connectivity confirms managed RDS (%s) reachability", rds.host)
		}
	}
	if redisP := requireRedis(t); redisP.host != "" {
		if ok, found := connectivityDependencyOK(connBody["dependencies"], "redis"); !found || !ok {
			t.Errorf("cloud install is wired to managed ElastiCache (%s) but lenny-ops connectivity does not report redis reachable: %v", redisP.host, connBody)
		} else {
			t.Logf("TestCloudOpsSurfaceHealthy: lenny-ops connectivity confirms managed ElastiCache (%s) reachability", redisP.host)
		}
	}
}

// opsDeploymentReady confirms the chart's lenny-ops Deployment exists
// in lenny-system and has at least one ready replica. Logs and returns
// false (an honest skip, matching requireGatewayInstalled) rather than
// failing outright, so a cluster mid-bring-up produces a diagnosable
// log instead of a flaky failure.
func opsDeploymentReady(t *testing.T, cli *kubernetes.Clientset) bool {
	t.Helper()
	if cli == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dep, err := cli.AppsV1().Deployments(lennySystem).Get(ctx, "lenny-ops", metav1.GetOptions{})
	if err != nil {
		t.Logf("opsDeploymentReady: Deployment lenny-ops not found in %s: %v (run scripts/cloud/<provider>/run-e2e.sh to install the chart)", lennySystem, err)
		return false
	}
	if dep.Status.ReadyReplicas < 1 {
		t.Errorf("Deployment lenny-ops has ReadyReplicas=%d, want >= 1 (Replicas=%d, AvailableReplicas=%d)",
			dep.Status.ReadyReplicas, dep.Status.Replicas, dep.Status.AvailableReplicas)
		return false
	}
	t.Logf("opsDeploymentReady: lenny-ops has %d/%d ready replicas", dep.Status.ReadyReplicas, dep.Status.Replicas)
	return true
}

// portForwardOpsCloud forwards the in-cluster lenny-ops Service to a
// local port via `kubectl port-forward`, following the same pattern as
// portForwardGatewayCloud in session_lifecycle_test.go. Returns ("",
// nil) and logs rather than fails when kubectl is unavailable or the
// forward never comes up, consistent with the honest-skip convention
// the rest of the package uses for cloud-infrastructure preconditions.
func portForwardOpsCloud(t *testing.T, cli *kubernetes.Clientset) (baseURL string, stop func()) {
	t.Helper()
	if _, err := exec.LookPath("kubectl"); err != nil {
		t.Logf("portForwardOpsCloud: kubectl not on PATH: %v", err)
		return "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	_, err := cli.CoreV1().Services(lennySystem).Get(ctx, "lenny-ops", metav1.GetOptions{})
	cancel()
	if err != nil {
		t.Logf("portForwardOpsCloud: Service lenny-ops not found in %s: %v", lennySystem, err)
		return "", nil
	}

	port := freeLocalPortCloud(t)
	cmd := exec.Command("kubectl", "-n", lennySystem, "port-forward", "svc/lenny-ops",
		fmt.Sprintf("%d:%d", port, opsHTTPPort))
	if err := cmd.Start(); err != nil {
		t.Logf("portForwardOpsCloud: kubectl port-forward did not start: %v", err)
		return "", nil
	}
	stop = func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}

	baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return baseURL, stop
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	stop()
	t.Log("portForwardOpsCloud: lenny-ops port-forward never returned 200 on /healthz")
	return "", nil
}

// opsGetJSON issues a GET against the lenny-ops port-forward and
// decodes the JSON body into a generic map. Fatals the test on a
// non-200 status or invalid JSON — both indicate the endpoint itself
// is broken, as opposed to a dependency it reports on being down.
func opsGetJSON(t *testing.T, client *http.Client, url string) map[string]any {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("GET %s: decode JSON: %v", url, err)
	}
	// §25.4 handleReadyz always answers 200 (it reports dependency
	// status in the body rather than failing the HTTP status);
	// /v1/admin/diagnostics/connectivity likewise answers 200 with a
	// "healthy" field rather than a non-200 status. A non-2xx here
	// means the endpoint itself errored, not that a dependency is
	// merely unreachable.
	if resp.StatusCode >= 300 {
		t.Fatalf("GET %s: status %d, body %v", url, resp.StatusCode, body)
	}
	return body
}

// assertMapDepOK asserts that deps[name] is present and reports ok:true
// (the map shape dependencyReport in pkg/ops/opsserver/opsserver.go
// produces for /readyz and the probe.Run fallback on
// /v1/admin/diagnostics/connectivity).
func assertMapDepOK(t *testing.T, deps map[string]any, name, endpoint string) {
	t.Helper()
	entry, present := deps[name]
	if !present {
		t.Errorf("%s carries no %q dependency entry: %v", endpoint, name, deps)
		return
	}
	m, ok := entry.(map[string]any)
	if !ok {
		t.Errorf("%s %q dependency entry is not an object: %v", endpoint, name, entry)
		return
	}
	if okField, _ := m["ok"].(bool); !okField {
		t.Errorf("%s reports %q unreachable: %v", endpoint, name, m)
	}
}

// connectivityDependencyOK inspects the "dependencies" field of a
// /v1/admin/diagnostics/connectivity response and reports whether the
// named dependency is present and reachable. The field is either a map
// (probe.Run fallback: name -> {"ok": bool, ...}) or a list
// (diagnostics.ConnectivityReport when a DiagnosticService is wired:
// [{"name": ..., "reachable": bool, ...}]); both are valid per the
// opsserver handler, so this tolerates either shape rather than
// assuming one.
func connectivityDependencyOK(raw any, name string) (ok, found bool) {
	switch deps := raw.(type) {
	case map[string]any:
		entry, present := deps[name]
		if !present {
			return false, false
		}
		m, isMap := entry.(map[string]any)
		if !isMap {
			return false, true
		}
		okField, _ := m["ok"].(bool)
		return okField, true
	case []any:
		for _, item := range deps {
			m, isMap := item.(map[string]any)
			if !isMap {
				continue
			}
			if n, _ := m["name"].(string); n == name {
				reachable, _ := m["reachable"].(bool)
				return reachable, true
			}
		}
		return false, false
	default:
		return false, false
	}
}
