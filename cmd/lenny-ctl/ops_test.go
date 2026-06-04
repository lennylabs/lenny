// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// spec: §25.14 lenny-ctl operability command groups.

// runAgainstOps spins a fake lenny-ops server, runs the CLI command
// against it via --ops-server, and returns the exit code plus what the
// ops server saw. The --ops-server flag bypasses gateway auto-discovery.
func runAgainstOps(t *testing.T, status int, response string, args ...string) (int, *capturedRequest) {
	t.Helper()
	got := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.path = r.URL.Path
		if r.URL.RawQuery != "" {
			got.path += "?" + r.URL.RawQuery
		}
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			_ = json.Unmarshal(raw, &got.body)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	defer srv.Close()

	full := append([]string{"--ops-server", srv.URL}, args...)
	var stdout, stderr bytes.Buffer
	code := run(full, &stdout, &stderr)
	return code, got
}

func TestRunbooksListTargetsOps(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"runbooks":[]}`, "runbooks", "list")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodGet || got.path != "/v1/admin/runbooks" {
		t.Errorf("request: %s %s, want GET /v1/admin/runbooks", got.method, got.path)
	}
}

func TestRunbooksListByAlert(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"runbooks":[]}`,
		"runbooks", "list", "--alert", "PoolStarvation")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.path != "/v1/admin/runbooks?alert=PoolStarvation" {
		t.Errorf("path: %q, want /v1/admin/runbooks?alert=PoolStarvation", got.path)
	}
}

func TestRunbooksGet(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"name":"pool-starvation"}`,
		"runbooks", "get", "pool-starvation")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.path != "/v1/admin/runbooks/pool-starvation" {
		t.Errorf("path: %q", got.path)
	}
}

func TestLocksListTargetsOps(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"locks":[]}`, "locks", "list")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodGet || got.path != "/v1/admin/remediation-locks" {
		t.Errorf("request: %s %s, want GET /v1/admin/remediation-locks", got.method, got.path)
	}
}

func TestLocksAcquireSendsScopeAndOp(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusCreated, `{"id":"lock-1"}`,
		"locks", "acquire", "--scope", "pool/echo", "--op", "drain")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/remediation-locks" {
		t.Errorf("request: %s %s", got.method, got.path)
	}
	if got.body["scope"] != "pool/echo" || got.body["operation"] != "drain" {
		t.Errorf("body: %+v, want scope=pool/echo operation=drain", got.body)
	}
}

func TestLocksAcquireRequiresScopeAndOp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--ops-server", "http://ops:8090", "locks", "acquire", "--scope", "pool/echo"},
		&stdout, &stderr)
	if code != 2 {
		t.Errorf("locks acquire without --op: exit code %d, want 2", code)
	}
}

func TestLocksRelease(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"released":true}`,
		"locks", "release", "lock-1")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodDelete || got.path != "/v1/admin/remediation-locks/lock-1" {
		t.Errorf("request: %s %s", got.method, got.path)
	}
}

func TestEscalationsListTargetsOps(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"items":[]}`, "escalations", "list")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodGet || got.path != "/v1/admin/escalations" {
		t.Errorf("request: %s %s", got.method, got.path)
	}
}

func TestEscalationsCreateSendsSeverityAndSummary(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusCreated, `{"id":"esc-1"}`,
		"escalations", "create", "--severity", "high", "--summary", "pool starvation")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/escalations" {
		t.Errorf("request: %s %s", got.method, got.path)
	}
	if got.body["severity"] != "high" || got.body["summary"] != "pool starvation" {
		t.Errorf("body: %+v", got.body)
	}
}

func TestEscalationsResolveUsesPut(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"status":"resolved"}`,
		"escalations", "resolve", "esc-1")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPut || got.path != "/v1/admin/escalations/esc-1" {
		t.Errorf("request: %s %s, want PUT /v1/admin/escalations/esc-1", got.method, got.path)
	}
	if got.body["status"] != "resolved" {
		t.Errorf("body: %+v, want status=resolved", got.body)
	}
}

func TestDiagnoseSession(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"sessionId":"s1"}`,
		"diagnose", "session", "s1")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.path != "/v1/admin/diagnostics/sessions/s1" {
		t.Errorf("path: %q", got.path)
	}
}

func TestDiagnosePool(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"pool":"echo"}`,
		"diagnose", "pool", "echo")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.path != "/v1/admin/diagnostics/pools/echo" {
		t.Errorf("path: %q", got.path)
	}
}

func TestDiagnoseConnectivity(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"healthy":true}`,
		"diagnose", "connectivity")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.path != "/v1/admin/diagnostics/connectivity" {
		t.Errorf("path: %q", got.path)
	}
}

func TestDiagnoseCredentialPool(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"pool":"creds"}`,
		"diagnose", "credential-pool", "creds")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.path != "/v1/admin/diagnostics/credential-pools/creds" {
		t.Errorf("path: %q", got.path)
	}
}

// spec: §24.2 line 44 — `lenny-ctl doctor` POSTs to the read-only
// diagnostics run endpoint.
func TestDoctorReadOnly(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"fix":false,"findings":[]}`, "doctor")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/diagnostics/run" {
		t.Errorf("request: %s %s, want POST /v1/admin/diagnostics/run", got.method, got.path)
	}
}

// spec: §24.2 line 45 — `lenny-ctl doctor --fix` sets ?fix=true.
func TestDoctorFixSetsQuery(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"fix":true,"findings":[]}`, "doctor", "--fix")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.path != "/v1/admin/diagnostics/run?fix=true" {
		t.Errorf("path: %q, want /v1/admin/diagnostics/run?fix=true", got.path)
	}
}

// `--findings a,b` is parsed into the request body's findings array.
func TestDoctorFindingsFlag(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"fix":true,"findings":[]}`,
		"doctor", "--fix", "--findings", "coreDnsStuckEndpoint, certManagerExpiring")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	list, _ := got.body["findings"].([]any)
	if len(list) != 2 || list[0] != "coreDnsStuckEndpoint" || list[1] != "certManagerExpiring" {
		t.Errorf("findings body = %v", got.body["findings"])
	}
}

func TestDriftReport(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"drift":[]}`, "drift", "report")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodGet || got.path != "/v1/admin/drift" {
		t.Errorf("request: %s %s", got.method, got.path)
	}
}

func TestDriftReportWithScopeAndAgainst(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"drift":[]}`,
		"drift", "report", "--scope", "runtimes", "--against", "both")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	// The query parameters are URL-encoded; both must be present.
	if got.path != "/v1/admin/drift?against=both&scope=runtimes" {
		t.Errorf("path: %q, want both scope and against query params", got.path)
	}
}

func TestDriftReconcileRequiresConfirm(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--ops-server", "http://ops:8090", "drift", "reconcile"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("drift reconcile without --confirm: exit code %d, want 2", code)
	}
}

func TestDriftReconcileWithConfirm(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"reconciled":3}`,
		"drift", "reconcile", "--confirm")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/drift/reconcile" {
		t.Errorf("request: %s %s", got.method, got.path)
	}
	if got.body["confirm"] != true {
		t.Errorf("body: %+v, want confirm=true", got.body)
	}
}

// TestOpsAutoDiscoveryFromGateway covers the §25.14 auto-discovery: when
// --ops-server is omitted, lenny-ctl reads the gateway's
// GET /v1/admin/platform/version response and uses its opsServiceURL.
func TestOpsAutoDiscoveryFromGateway(t *testing.T) {
	opsGot := &capturedRequest{}
	ops := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opsGot.method = r.Method
		opsGot.path = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"runbooks":[]}`))
	}))
	defer ops.Close()

	// The gateway advertises the ops URL in the version response.
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/platform/version" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"gatewayVersion":"dev","opsServiceURL":"` + ops.URL + `"}`))
	}))
	defer gateway.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{"--api-url", gateway.URL, "runbooks", "list"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0 (stderr %s)", code, stderr.String())
	}
	if opsGot.path != "/v1/admin/runbooks" {
		t.Errorf("auto-discovery did not route to the ops server: ops saw %q", opsGot.path)
	}
}

// TestOpsAutoDiscoveryMissingURLFallsBackToGateway covers the §24.16
// routing rule 3: when the gateway advertises no opsServiceURL and
// --ops-server is not passed, lenny-ctl falls back to the gateway host
// (rather than aborting) and surfaces a warning. The ops call is routed
// to the gateway host. spec: §24.16 line 201 rule 3.
func TestOpsAutoDiscoveryMissingURLFallsBackToGateway(t *testing.T) {
	got := &capturedRequest{}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/admin/platform/version" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"gatewayVersion":"dev"}`)) // no opsServiceURL.
			return
		}
		got.method = r.Method
		got.path = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"runbooks":[]}`))
	}))
	defer gateway.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{"--api-url", gateway.URL, "runbooks", "list"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("fallback: exit code %d, want 0 (stderr %s)", code, stderr.String())
	}
	if got.path != "/v1/admin/runbooks" {
		t.Errorf("fallback did not route the ops call to the gateway host: gateway saw %q", got.path)
	}
	if !strings.Contains(stderr.String(), "WARN") || !strings.Contains(stderr.String(), "falling back to the gateway host") {
		t.Errorf("fallback should warn on stderr, got %q", stderr.String())
	}
}

// TestOpsAutoDiscoveryGatewayUnreachableFallsBack covers the rule-3
// fallback when the version probe itself fails (gateway unreachable for
// that path): the ops call is still attempted against the gateway host
// with a warning, rather than aborting at exit 2. spec: §24.16 line 201.
func TestOpsAutoDiscoveryGatewayUnreachableFallsBack(t *testing.T) {
	got := &capturedRequest{}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/admin/platform/version" {
			w.WriteHeader(http.StatusInternalServerError) // discovery fails.
			return
		}
		got.path = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"runbooks":[]}`))
	}))
	defer gateway.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{"--api-url", gateway.URL, "runbooks", "list"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("fallback on discovery failure: exit %d, want 0 (stderr %s)", code, stderr.String())
	}
	if got.path != "/v1/admin/runbooks" {
		t.Errorf("fallback did not route to the gateway host: gateway saw %q", got.path)
	}
}

// TestOpsErrorPropagates covers the failure path: an ops 4xx surfaces
// as a non-zero exit code.
func TestOpsErrorPropagates(t *testing.T) {
	code, _ := runAgainstOps(t, http.StatusNotFound, `{"error":{"code":"RUNBOOK_NOT_FOUND","message":"no such runbook"}}`,
		"runbooks", "get", "ghost")
	if code != 1 {
		t.Errorf("ops 404: exit code %d, want 1", code)
	}
}

func TestUnknownOpsSubcommandIsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	for _, group := range []string{"runbooks", "locks", "escalations", "diagnose", "drift"} {
		code := run([]string{"--ops-server", "http://ops:8090", group, "bogus"}, &stdout, &stderr)
		if code != 2 {
			t.Errorf("%s bogus: exit code %d, want 2", group, code)
		}
	}
}

func TestParseGlobalFlagsParsesOpsServer(t *testing.T) {
	f, rest := parseGlobalFlags([]string{
		"--ops-server", "http://ops.example.com", "runbooks", "list",
	})
	if f.opsServer != "http://ops.example.com" {
		t.Errorf("opsServer: %q", f.opsServer)
	}
	if len(rest) != 2 || rest[0] != "runbooks" {
		t.Errorf("rest: %v", rest)
	}
}
