// SPDX-License-Identifier: MIT

package ctlcli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// spec: §24.15 line 192 — `lenny-ctl logs pods <namespace> <name>` proxies
// a pod's container logs from the §25.4 lenny-ops log-proxy endpoint. The
// `pods` subcommand disambiguates the clustered proxy from the §24.19
// embedded `logs <component>` stack-log tailer.

// runLogs spins a fake lenny-ops server that returns text/plain, runs the
// CLI against it via --ops-server, and reports the exit code, the path the
// server saw, whether it was hit, and what the CLI wrote to stdout.
func runLogs(t *testing.T, status int, body string, args ...string) (code int, path string, hit bool, stdout string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		path = r.URL.Path
		if r.URL.RawQuery != "" {
			path += "?" + r.URL.RawQuery
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	full := append([]string{"--ops-server", srv.URL}, args...)
	var out, errOut bytes.Buffer
	code = run(full, &out, &errOut)
	return code, path, hit, out.String()
}

func TestLogsPodsTargetsOps_spec_24_15_192(t *testing.T) {
	const logBody = "2026-06-04T00:00:00Z hello\n2026-06-04T00:00:01Z world\n"
	code, path, hit, stdout := runLogs(t, http.StatusOK, logBody,
		"logs", "pods", "lenny-system", "lenny-gateway-abc")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if !hit {
		t.Fatal("ops server was not contacted")
	}
	if path != "/v1/admin/logs/pods/lenny-system/lenny-gateway-abc" {
		t.Errorf("path: %q", path)
	}
	if stdout != logBody {
		t.Errorf("stdout: got %q, want %q", stdout, logBody)
	}
}

func TestLogsPodsMapsQueryParams_spec_25_4(t *testing.T) {
	code, path, _, _ := runLogs(t, http.StatusOK, "ok\n",
		"logs", "pods", "ns", "pod",
		"--container", "gateway", "--since", "5m", "--tail", "100", "--previous")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	// url.Values.Encode sorts keys: container, previous, since, tail.
	want := "/v1/admin/logs/pods/ns/pod?container=gateway&previous=true&since=5m&tail=100"
	if path != want {
		t.Errorf("path: got %q, want %q", path, want)
	}
}

func TestLogsPodsRequiresNamespaceAndName(t *testing.T) {
	code, _, _, _ := runLogs(t, http.StatusOK, "", "logs", "pods", "only-namespace")
	if code != 2 {
		t.Errorf("exit code: got %d, want 2 (missing pod name)", code)
	}
}

func TestLogsPodsSurfacesNotFound_spec_25_4(t *testing.T) {
	// A 404 from the proxy (POD_NOT_FOUND) must surface as a non-zero exit
	// rather than a silently-empty success.
	code, _, hit, stdout := runLogs(t, http.StatusNotFound,
		`{"error":{"code":"POD_NOT_FOUND","message":"no pod ns/ghost"}}`,
		"logs", "pods", "ns", "ghost")
	if code != 1 {
		t.Errorf("exit code: got %d, want 1", code)
	}
	if !hit {
		t.Error("ops server was not contacted")
	}
	if stdout != "" {
		t.Errorf("stdout should be empty on error: %q", stdout)
	}
}

// TestLogsComponentStaysEmbedded confirms the disambiguation: `lenny-ctl
// logs <component>` (no `pods` subcommand) still routes to the §24.19
// embedded stack-log tailer and never contacts lenny-ops.
func TestLogsComponentStaysEmbedded(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir()) // isolate from any real ~/.lenny stack
	_, _, hit, _ := runLogs(t, http.StatusOK, "should-not-be-served",
		"logs", "gateway")
	if hit {
		t.Error("`logs gateway` reached the ops server; the embedded tailer was shadowed")
	}
}
