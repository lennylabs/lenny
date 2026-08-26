// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Shared helpers for the tier-5 e2e Kind tests that drive the live
// Lenny gateway admin API or probe NetworkPolicy enforcement. The
// gateway Service is internal to lenny-system and the lenny-system
// default-deny NetworkPolicy blocks egress from an unlabelled pod, so a
// test reaches the admin API from an in-cluster probe pod that carries
// lenny.dev/component: bootstrap — allow-bootstrap-egress permits its
// egress to the gateway and allow-gateway-ingress admits it on the
// gateway internal HTTP port. The probe runs curl with the dev-mode
// auth headers (X-Lenny-Tenant-ID / X-Lenny-Roles / X-Lenny-User-ID),
// which the e2e gateway honours because it runs in dev mode.
//
// These helpers mirror the equivalent tier-8 chaos and tier-9 security
// helpers without taking a cross-build-tag dependency on those
// packages: each build tag compiles a disjoint file set, so the shared
// gateway-probe recipe is duplicated as a small focused helper here.

package tier5_e2e_kind_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// t5SystemNS is the namespace the Lenny control plane runs in.
const t5SystemNS = "lenny-system"

// t5ProbeImage is the probe-pod container image. curlimages/curl is a
// busybox-based image with a shell, already loaded on the e2e worker
// node, so the probe pod runs with imagePullPolicy: Never and never
// reaches a remote registry.
const t5ProbeImage = "curlimages/curl:8.11.0"

// t5ProbeNode is the node the curl image is loaded on. A probe pod is
// pinned here with spec.nodeName so the kubelet finds the image locally
// rather than attempting a pull on another node.
const t5ProbeNode = "lenny-e2e-worker"

// t5ProbeRunAsUser is the numeric UID curlimages/curl runs as. The
// image declares a non-numeric user, so runAsNonRoot: true needs an
// explicit numeric runAsUser to satisfy the kubelet's root check.
const t5ProbeRunAsUser = 100

// t5ProbePodManifest renders a long-sleeping probe-pod manifest in the
// given namespace with the given pod labels. The pod runs the §13.1
// hardened securityContext so the lenny-pod-security webhook does not
// interfere, and is pinned to the node the curl image is loaded on.
func t5ProbePodManifest(name, namespace string, labels map[string]string) string {
	var labelLines strings.Builder
	labelLines.WriteString("    lenny.dev/test: tier5-gateway-probe\n")
	for k, v := range labels {
		fmt.Fprintf(&labelLines, "    %s: %q\n", k, v)
	}
	return fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
  labels:
%sspec:
  nodeName: %s
  restartPolicy: Never
  terminationGracePeriodSeconds: 1
  containers:
    - name: probe
      image: %s
      imagePullPolicy: Never
      command: ["sleep", "1800"]
      securityContext:
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        runAsNonRoot: true
        runAsUser: %d
        capabilities:
          drop: ["ALL"]
`, name, namespace, labelLines.String(), t5ProbeNode, t5ProbeImage, t5ProbeRunAsUser)
}

// t5CreateProbePod applies a probe-pod manifest, registers a t.Cleanup
// to delete it, and waits for the pod to become Ready. A pod that never
// becomes Ready fails the test, because a later probe would otherwise
// misreport a scheduling failure as a gateway or NetworkPolicy result.
func t5CreateProbePod(t *testing.T, c *kind.Cluster, name, namespace string, labels map[string]string) {
	t.Helper()
	manifest := t5ProbePodManifest(name, namespace, labels)
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, manifest) })
	if out, err := c.ApplyStdin(t, manifest); err != nil {
		t.Fatalf("failed to create probe pod %s in %s: %v\n%s", name, namespace, err, out)
	}
	if out, err := c.KubectlOut(
		t,
		"-n", namespace, "wait", "--for=condition=Ready",
		"pod/"+name, "--timeout=120s",
	); err != nil {
		desc, _ := c.KubectlOut(t, "-n", namespace, "describe", "pod", name)
		t.Fatalf("probe pod %s in %s did not become Ready: %v\n%s\n--- describe ---\n%s",
			name, namespace, err, out, desc)
	}
}

// t5StartGatewayProbe creates a bootstrap-labelled probe pod in
// lenny-system, waits for it to become Ready, and returns the
// lenny-gateway Service ClusterIP. The bootstrap label brings
// allow-bootstrap-egress and allow-gateway-ingress into scope so the
// probe can drive the gateway internal HTTP port.
func t5StartGatewayProbe(t *testing.T, c *kind.Cluster, name string) string {
	t.Helper()
	t5CreateProbePod(t, c, name, t5SystemNS, map[string]string{
		"lenny.dev/component": "bootstrap",
	})
	ip := t5ServiceClusterIP(t, c, "lenny-gateway")
	if ip == "" {
		t.Fatalf("the lenny-gateway Service has no ClusterIP; cannot drive the admin API")
	}
	return ip
}

// t5ServiceClusterIP returns the .spec.clusterIP of the named Service
// in lenny-system, or "" when the read fails.
func t5ServiceClusterIP(t *testing.T, c *kind.Cluster, service string) string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", t5SystemNS, "get", "service", service,
		"-o", "jsonpath={.spec.clusterIP}",
	)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// t5Role carries the dev-mode auth headers a probe request presents.
type t5Role struct {
	tenant string
	roles  string
	user   string
}

// t5PlatformAdmin is the dev-header identity for a platform-admin in
// the built-in `platform` tenant.
func t5PlatformAdmin() t5Role {
	return t5Role{tenant: "platform", roles: "platform-admin", user: "alice"}
}

// t5Response is the parsed outcome of an admin-API probe request.
// headers is populated only by t5GatewayRequestWithHeaders; its keys are
// lowercased header names.
type t5Response struct {
	curlExit   int
	statusCode int
	body       string
	headers    map[string]string
}

// t5GatewayRequest runs curl inside the probe pod against an admin-API
// path on the gateway internal HTTP port (8080) with the dev-mode auth
// headers for role, and returns the curl exit code, HTTP status, and
// response body.
func t5GatewayRequest(t *testing.T, c *kind.Cluster, pod, gatewayIP, method, path string, role t5Role, body string) t5Response {
	t.Helper()
	return t5GatewayCurl(t, c, pod, gatewayIP, method, path, role, body, nil, false)
}

// t5GatewayRequestWithHeaders behaves like t5GatewayRequest and also
// sends each entry of extra as an additional request header ("Name:
// value") and returns the response headers in t5Response.headers. A
// conditional write such as the §15.1 rbac-config PUT needs both: the
// ETag comes back on the GET and goes out as If-Match on the PUT.
func t5GatewayRequestWithHeaders(t *testing.T, c *kind.Cluster, pod, gatewayIP, method, path string, role t5Role, body string, extra []string) t5Response {
	t.Helper()
	return t5GatewayCurl(t, c, pod, gatewayIP, method, path, role, body, extra, true)
}

// t5GatewayCurl builds and runs the probe-pod curl the two request
// helpers above share. dumpHeaders adds curl's -D - so the response
// headers precede the body on stdout, and the parser splits them back
// apart.
func t5GatewayCurl(t *testing.T, c *kind.Cluster, pod, gatewayIP, method, path string, role t5Role, body string, extra []string, dumpHeaders bool) t5Response {
	t.Helper()
	url := fmt.Sprintf("http://%s:8080%s", gatewayIP, path)
	var sb strings.Builder
	sb.WriteString("curl -sS -m 10 -X ")
	sb.WriteString(method)
	if dumpHeaders {
		sb.WriteString(" -D -")
	}
	sb.WriteString(" -H 'X-Lenny-Tenant-ID: ")
	sb.WriteString(role.tenant)
	sb.WriteString("' -H 'X-Lenny-Roles: ")
	sb.WriteString(role.roles)
	sb.WriteString("' -H 'X-Lenny-User-ID: ")
	sb.WriteString(role.user)
	sb.WriteString("'")
	for _, h := range extra {
		if strings.Contains(h, "'") {
			t.Fatalf("t5GatewayCurl header contains a single quote, which the shell-quoting cannot carry: %q", h)
		}
		sb.WriteString(" -H '")
		sb.WriteString(h)
		sb.WriteString("'")
	}
	if body != "" {
		// Single-quote the JSON body for the shell. The bodies these
		// tests build carry no single quotes; guard the assumption.
		if strings.Contains(body, "'") {
			t.Fatalf("t5GatewayRequest body contains a single quote, which the shell-quoting cannot carry: %q", body)
		}
		sb.WriteString(" -H 'Content-Type: application/json' --data '")
		sb.WriteString(body)
		sb.WriteString("'")
	}
	sb.WriteString(" -w '\\nLENNYPROBE status=%{http_code} exit=%{exitcode}\\n' ")
	sb.WriteString(url)
	sb.WriteString(" 2>&1")
	out, _ := c.KubectlOut(
		t,
		"-n", t5SystemNS, "exec", pod, "--", "sh", "-c", sb.String(),
	)
	res := parseT5Response(out)
	if dumpHeaders {
		res.headers, res.body = splitT5Headers(res.body)
	}
	return res
}

// t5AllowSessionsWithNoEnvironment sets the tenant's §10.6
// noEnvironmentPolicy to allow-all through the admin rbac-config
// surface, reached from the probe pod. A tenant carries the stricter
// deny-all policy unless it is relaxed, and the session server's
// environment admission gate then answers every session-creation
// request that names no environment with 403 FORBIDDEN and reason
// no_environment_policy_deny_all. A probe-driven test whose assertions
// sit on another axis pins the precondition explicitly, exactly as the
// sessiondriver-driven tests do through
// sessiondriver.Driver.AllowSessionsWithNoEnvironment.
//
// The write is conditional on the ETag the GET returns, per §15.1, and
// the call is idempotent: a tenant already on allow-all is left alone.
//
// spec: §10.6, §11.1, §15.1
func t5AllowSessionsWithNoEnvironment(t *testing.T, c *kind.Cluster, pod, gatewayIP, tenantID string) {
	t.Helper()
	admin := t5Role{tenant: tenantID, roles: "platform-admin", user: "alice"}
	path := "/v1/admin/tenants/" + tenantID + "/rbac-config"
	get := t5GatewayRequestWithHeaders(t, c, pod, gatewayIP, "GET", path, admin, "", nil)
	if get.statusCode != 200 {
		t.Fatalf("GET %s: status %d, body=%s", path, get.statusCode, get.body)
	}
	etag := get.headers["etag"]
	if etag == "" {
		t.Fatalf("GET %s returned no ETag; the conditional write cannot be built", path)
	}
	var config map[string]any
	t5DecodeJSON(t, get.body, &config)
	if config["noEnvironmentPolicy"] == "allow-all" {
		return
	}
	config["noEnvironmentPolicy"] = "allow-all"
	body, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("encode rbac-config for tenant %q: %v", tenantID, err)
	}
	put := t5GatewayRequestWithHeaders(t, c, pod, gatewayIP, "PUT", path, admin,
		string(body), []string{"If-Match: " + etag})
	if put.statusCode != 200 {
		t.Fatalf("PUT %s: status %d, body=%s", path, put.statusCode, put.body)
	}
}

// splitT5Headers separates a curl -D - status line and header block
// from the response body, returning the headers keyed by lowercased
// name. Text carrying no header block is returned unchanged as the
// body.
func splitT5Headers(out string) (map[string]string, string) {
	headers := map[string]string{}
	lines := strings.Split(out, "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "HTTP/") {
		return headers, out
	}
	for i := 1; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		if line == "" {
			return headers, strings.Join(lines[i+1:], "\n")
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		headers[strings.ToLower(strings.TrimSpace(name))] = strings.TrimSpace(value)
	}
	return headers, ""
}

// parseT5Response extracts the status code, curl exit code, and body
// from t5GatewayRequest's output. The body is everything before the
// LENNYPROBE marker line.
func parseT5Response(out string) t5Response {
	r := t5Response{curlExit: -1, statusCode: -1}
	marker := "LENNYPROBE "
	idx := strings.LastIndex(out, marker)
	if idx < 0 {
		r.body = out
		return r
	}
	r.body = strings.TrimRight(out[:idx], "\n")
	for _, field := range strings.Fields(out[idx+len(marker):]) {
		if v, ok := strings.CutPrefix(field, "status="); ok {
			n := 0
			if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
				r.statusCode = n
			}
		}
		if v, ok := strings.CutPrefix(field, "exit="); ok {
			n := 0
			if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
				r.curlExit = n
			}
		}
	}
	return r
}

// t5GatewayRequestRetry runs t5GatewayRequest and, when the request
// fails to complete (curl exit non-zero) or returns a 5xx, retries up
// to a short bound. The e2e gateway runs two replicas; a rollout can
// leave one replica briefly unready behind the Service, and a transient
// 502/503 from that window is not the property under test. A 4xx is
// returned immediately — it is a definitive verdict.
func t5GatewayRequestRetry(t *testing.T, c *kind.Cluster, pod, gatewayIP, method, path string, role t5Role, body string) t5Response {
	t.Helper()
	var last t5Response
	deadline := time.Now().Add(30 * time.Second)
	for {
		last = t5GatewayRequest(t, c, pod, gatewayIP, method, path, role, body)
		if last.curlExit == 0 && last.statusCode >= 0 && last.statusCode < 500 {
			return last
		}
		if time.Now().After(deadline) {
			return last
		}
		time.Sleep(2 * time.Second)
	}
}

// t5DeploymentReady reports whether the named lenny-system Deployment
// has its full desired replica count Ready. A read error or an
// unparseable status yields false so callers skip rather than fail.
func t5DeploymentReady(t *testing.T, c *kind.Cluster, deployment string) bool {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", t5SystemNS, "get", "deployment", deployment,
		"-o", "jsonpath={.spec.replicas}/{.status.readyReplicas}",
	)
	if err != nil {
		return false
	}
	desired, ready, ok := strings.Cut(strings.TrimSpace(out), "/")
	if !ok || desired == "" {
		return false
	}
	if ready == "" {
		ready = "0"
	}
	return desired == ready
}

// t5DataStorePodIP returns the pod IP of the named e2e data store
// (postgres, redis, or minio). The test process runs kubectl from
// outside the cluster, so it resolves the pod IP directly without
// in-cluster DNS. An empty result means the store pod is absent or not
// yet assigned an IP.
func t5DataStorePodIP(t *testing.T, c *kind.Cluster, store string) string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", t5SystemNS, "get", "pod",
		"-l", "lenny.dev/e2e-datastore="+store,
		"-o", "jsonpath={.items[0].status.podIP}",
	)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// t5RunPsqlQuery runs a read-only SQL query against the e2e Postgres
// via a one-shot postgres:16.4 pod connected to pgIP. The pod connects
// to the pod IP rather than the Service DNS name because the
// lenny-system default-deny NetworkPolicy denies a throwaway pod's
// egress to CoreDNS. It returns the rows as raw lines (psql -tA:
// tuples-only, unaligned). A query failure fails the test.
func t5RunPsqlQuery(t *testing.T, c *kind.Cluster, pgIP, podName, sql string) string {
	t.Helper()
	_, _ = c.KubectlOut(t, "-n", t5SystemNS, "delete", "pod", podName,
		"--ignore-not-found", "--wait=true")
	out, err := c.KubectlOut(
		t,
		"-n", t5SystemNS, "run", podName,
		"--image=postgres:16.4", "--image-pull-policy=IfNotPresent",
		"--restart=Never", "--attach", "--rm", "--quiet",
		"--env=PGPASSWORD=lenny",
		"--command", "--",
		"psql", "-h", pgIP, "-U", "lenny", "-d", "lenny",
		"-tA", "-v", "ON_ERROR_STOP=1", "-c", sql,
	)
	if err != nil {
		t.Fatalf("psql query %q failed: %v\n%s", podName, err, out)
	}
	return out
}

// t5RunPsqlExec runs a SQL script against the e2e Postgres via a
// one-shot psql pod. ON_ERROR_STOP makes any failed statement a
// non-zero exit, which fails the test — a cleanup that did not apply is
// not a recoverable condition.
func t5RunPsqlExec(t *testing.T, c *kind.Cluster, pgIP, podName, sql string) {
	t.Helper()
	_, _ = c.KubectlOut(t, "-n", t5SystemNS, "delete", "pod", podName,
		"--ignore-not-found", "--wait=true")
	if out, err := c.KubectlOut(
		t,
		"-n", t5SystemNS, "run", podName,
		"--image=postgres:16.4", "--image-pull-policy=IfNotPresent",
		"--restart=Never", "--attach", "--rm", "--quiet",
		"--env=PGPASSWORD=lenny",
		"--command", "--",
		"psql", "-h", pgIP, "-U", "lenny", "-d", "lenny",
		"-v", "ON_ERROR_STOP=1", "-c", sql,
	); err != nil {
		t.Fatalf("psql script %q failed: %v\n%s", podName, err, out)
	}
}

// t5DecodeJSON unmarshals body into v, failing the test with the body
// text on a decode error.
func t5DecodeJSON(t *testing.T, body string, v any) {
	t.Helper()
	if strings.TrimSpace(body) == "" {
		t.Fatalf("expected a JSON body, got an empty response")
	}
	if err := json.Unmarshal([]byte(body), v); err != nil {
		t.Fatalf("response body is not valid JSON: %v\nbody: %q", err, body)
	}
}
