// SPDX-License-Identifier: MIT

//go:build security

// Shared helpers for the tier-9 security tests that drive the live
// Lenny gateway's REST admin API on the Kind cluster. The gateway
// Service is internal to lenny-system and the lenny-system default-deny
// NetworkPolicy blocks egress from an unlabelled pod, so a test reaches
// the admin API from an in-cluster probe pod that carries
// lenny.dev/component: gateway — the label allow-gateway-egress and
// allow-gateway-ingress both admit. The probe runs curl with the
// dev-mode auth headers (X-Lenny-Tenant-ID / X-Lenny-Roles /
// X-Lenny-User-ID), which the e2e gateway honours because it runs in
// dev mode (global.devMode: true → AllowDevHeaders + AllowDevRoles).
//
// The helpers here are the gateway-facing companion to the kubectl-read
// helpers the network and TLS tier-9 files use. Postgres-direct SQL (the
// audit-chain gap injection) uses a one-shot psql pod, mirroring the
// tier-8 audit-chain test.

package tier9_security_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// gatewayProbeImage is the probe-pod container image. curlimages/curl
// is a busybox-based image with a shell, already loaded on the e2e
// worker node, so the probe pod runs imagePullPolicy: Never.
const gatewayProbeImage = "curlimages/curl:8.11.0"

// gatewayProbeNode is the node the curl image is loaded on. The probe
// pod is pinned here so the kubelet finds the image locally rather than
// attempting a remote pull.
const gatewayProbeNode = "lenny-e2e-worker"

// gatewayProbeRunAsUser is the numeric UID curlimages/curl runs as; the
// image declares a non-numeric user, so runAsNonRoot needs an explicit
// numeric UID to satisfy the kubelet root check.
const gatewayProbeRunAsUser = 100

// gatewayProbePodManifest renders a long-sleeping probe-pod manifest.
// The pod carries lenny.dev/component: bootstrap so allow-bootstrap-
// egress permits its egress to the gateway and allow-gateway-ingress
// admits it on TCP 8080 through the lenny-system default-deny
// NetworkPolicy. (allow-gateway-egress would only let a pod reach the
// gateway's dependencies, not the gateway Service itself, so a
// gateway-labelled probe cannot drive the admin API.) The pod runs
// with the §13.1 hardened securityContext so the lenny-pod-security
// webhook — which is not scoped to lenny-system — does not interfere.
func gatewayProbePodManifest(name string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
  labels:
    lenny.dev/component: bootstrap
    lenny.dev/test: tier9-gateway-probe
spec:
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
`, name, lennySystemNS, gatewayProbeNode, gatewayProbeImage, gatewayProbeRunAsUser)
}

// startGatewayProbe creates a probe pod, registers a t.Cleanup to
// delete it, waits for it to become Ready, and returns the lenny-gateway
// Service ClusterIP the probe targets. A probe pod that never becomes
// Ready fails the test, since a later HTTP probe would otherwise
// misreport a scheduling failure as a gateway failure.
func startGatewayProbe(t *testing.T, c *kind.Cluster, name string) string {
	t.Helper()
	manifest := gatewayProbePodManifest(name)
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, manifest) })
	if out, err := c.ApplyStdin(t, manifest); err != nil {
		t.Fatalf("failed to create gateway probe pod %s: %v\n%s", name, err, out)
	}
	if out, err := c.KubectlOut(
		t,
		"-n", lennySystemNS, "wait", "--for=condition=Ready",
		"pod/"+name, "--timeout=120s",
	); err != nil {
		desc, _ := c.KubectlOut(t, "-n", lennySystemNS, "describe", "pod", name)
		t.Fatalf("gateway probe pod %s did not become Ready: %v\n%s\n--- describe ---\n%s",
			name, err, out, desc)
	}
	ip := serviceClusterIP(t, c, "lenny-gateway")
	if ip == "" {
		t.Fatalf("the lenny-gateway Service has no ClusterIP; cannot drive the admin API")
	}
	return ip
}

// gwRole carries the dev-mode auth headers a probe request presents.
// The e2e gateway runs in dev mode, so X-Lenny-Roles is honoured and
// the named role drives the §10.2 RBAC gate.
type gwRole struct {
	tenant string
	roles  string
	user   string
}

// platformAdmin is the dev-header identity for a platform-admin in the
// built-in `platform` tenant.
func platformAdmin() gwRole {
	return gwRole{tenant: "platform", roles: "platform-admin", user: "alice"}
}

// gwResponse is the parsed outcome of an admin-API probe request.
type gwResponse struct {
	curlExit   int
	statusCode int
	body       string
}

// errorCode extracts the error.code field from a Lenny error envelope
// body, or "" when the body is not an error envelope.
func (r gwResponse) errorCode() string {
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(r.body), &env); err != nil {
		return ""
	}
	return env.Error.Code
}

// gatewayRequest runs curl inside the probe pod against an admin-API
// path with the dev-mode auth headers for role, and returns the curl
// exit code, HTTP status, and response body. method is the HTTP verb;
// body, when non-empty, is sent as a JSON request body.
func gatewayRequest(t *testing.T, c *kind.Cluster, pod, gatewayIP, method, path string, role gwRole, body string) gwResponse {
	t.Helper()
	url := fmt.Sprintf("http://%s:8080%s", gatewayIP, path)
	// curl writes the response body to stdout, then a marker line carries
	// the HTTP status and curl's exit code out of the exec together.
	var sb strings.Builder
	sb.WriteString("curl -sS -m 10 -X ")
	sb.WriteString(method)
	sb.WriteString(" -H 'X-Lenny-Tenant-ID: ")
	sb.WriteString(role.tenant)
	sb.WriteString("' -H 'X-Lenny-Roles: ")
	sb.WriteString(role.roles)
	sb.WriteString("' -H 'X-Lenny-User-ID: ")
	sb.WriteString(role.user)
	sb.WriteString("'")
	if body != "" {
		// Single-quote the JSON body for the shell. The bodies this file
		// builds carry no single quotes, so a plain single-quote wrap is
		// safe; guard the assumption with a fatal check.
		if strings.Contains(body, "'") {
			t.Fatalf("gatewayRequest body contains a single quote, which the shell-quoting cannot carry: %q", body)
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
		"-n", lennySystemNS, "exec", pod, "--", "sh", "-c", sb.String(),
	)
	return parseGatewayResponse(out)
}

// parseGatewayResponse extracts the status code, curl exit code, and
// body from gatewayRequest's output. The body is everything before the
// LENNYPROBE marker line.
func parseGatewayResponse(out string) gwResponse {
	r := gwResponse{curlExit: -1, statusCode: -1}
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

// gatewayRequestRetry runs gatewayRequest and, when the request fails to
// complete (curl exit non-zero) or returns a 5xx, retries up to a short
// bound. The e2e gateway runs two replicas; a rollout can leave one
// replica briefly unready behind the Service, and a transient 502/503
// from that window is not the security property under test. A 4xx is
// returned immediately — it is a definitive authorization or validation
// verdict, not a transient fault.
func gatewayRequestRetry(t *testing.T, c *kind.Cluster, pod, gatewayIP, method, path string, role gwRole, body string) gwResponse {
	t.Helper()
	var last gwResponse
	deadline := time.Now().Add(30 * time.Second)
	for {
		last = gatewayRequest(t, c, pod, gatewayIP, method, path, role, body)
		if last.curlExit == 0 && last.statusCode >= 0 && last.statusCode < 500 {
			return last
		}
		if time.Now().After(deadline) {
			return last
		}
		time.Sleep(2 * time.Second)
	}
}
