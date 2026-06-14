// SPDX-License-Identifier: MIT

package ctlcli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseGlobalFlags(t *testing.T) {
	f, rest := parseGlobalFlags([]string{
		"--api-url", "http://gw:9000",
		"--bearer", "tok",
		"--dev-tenant", "platform",
		"--dev-roles", "platform-admin",
		"admin", "tenants", "list",
	})
	if f.apiURL != "http://gw:9000" {
		t.Errorf("apiURL: %q", f.apiURL)
	}
	if f.bearer != "tok" {
		t.Errorf("bearer: %q", f.bearer)
	}
	if f.devTenant != "platform" || f.devRoles != "platform-admin" {
		t.Errorf("dev flags: %+v", f)
	}
	if len(rest) != 3 || rest[0] != "admin" || rest[2] != "list" {
		t.Errorf("rest: %v", rest)
	}
}

// clearCLIEnv neutralizes the ambient operator environment so flag-default
// assertions are deterministic regardless of the developer's shell.
func clearCLIEnv(t *testing.T) {
	t.Helper()
	t.Setenv("LENNY_API_URL", "")
	t.Setenv("LENNY_OPS_URL", "")
	t.Setenv("LENNY_API_TOKEN", "")
}

func TestParseGlobalFlagsDefaultsAPIURL(t *testing.T) {
	clearCLIEnv(t)
	f, rest := parseGlobalFlags([]string{"health"})
	if f.apiURL != "http://localhost:8080" {
		t.Errorf("default apiURL: %q", f.apiURL)
	}
	if len(rest) != 1 || rest[0] != "health" {
		t.Errorf("rest: %v", rest)
	}
}

func TestParseGlobalFlagsStopsAtCommand(t *testing.T) {
	clearCLIEnv(t)
	// A bare command with no global flags.
	f, rest := parseGlobalFlags([]string{"admin", "--api-url", "ignored"})
	if f.apiURL != "http://localhost:8080" {
		t.Errorf("apiURL should keep default when flags come after command: %q", f.apiURL)
	}
	if len(rest) != 3 {
		t.Errorf("rest should include everything from the command on: %v", rest)
	}
}

// spec: §24.0 line 26, §24.16 lines 197/199 — LENNY_API_URL / LENNY_OPS_URL
// / LENNY_API_TOKEN supply defaults when the matching flag is absent.
// Closes F-24.0.6 and its duplicate F-24.16.1.
func TestParseGlobalFlagsHonorsEnv(t *testing.T) {
	t.Setenv("LENNY_API_URL", "https://gw.acme.example")
	t.Setenv("LENNY_OPS_URL", "https://ops.acme.example")
	t.Setenv("LENNY_API_TOKEN", "env-tok")
	f, rest := parseGlobalFlags([]string{"health"})
	if f.apiURL != "https://gw.acme.example" {
		t.Errorf("apiURL from env: %q", f.apiURL)
	}
	if f.opsServer != "https://ops.acme.example" {
		t.Errorf("opsServer from env: %q", f.opsServer)
	}
	if f.bearer != "env-tok" {
		t.Errorf("bearer from env: %q", f.bearer)
	}
	if len(rest) != 1 || rest[0] != "health" {
		t.Errorf("rest: %v", rest)
	}
}

// spec: §24.0 line 26 — an explicit flag overrides the environment.
func TestParseGlobalFlagsFlagOverridesEnv(t *testing.T) {
	t.Setenv("LENNY_API_URL", "https://env.example")
	t.Setenv("LENNY_API_TOKEN", "env-tok")
	f, _ := parseGlobalFlags([]string{
		"--api-url", "https://flag.example",
		"--token", "flag-tok",
		"health",
	})
	if f.apiURL != "https://flag.example" {
		t.Errorf("flag should override env apiURL: %q", f.apiURL)
	}
	if f.bearer != "flag-tok" {
		t.Errorf("--token should set bearer over env: %q", f.bearer)
	}
}

// An explicitly-empty env var is treated as unset so `export LENNY_API_URL=`
// does not blank the default gateway URL.
func TestParseGlobalFlagsEmptyEnvIsUnset(t *testing.T) {
	t.Setenv("LENNY_API_URL", "   ")
	f, _ := parseGlobalFlags([]string{"health"})
	if f.apiURL != "http://localhost:8080" {
		t.Errorf("blank env should fall back to default: %q", f.apiURL)
	}
}

// spec: §24 preamble line 8 — --token is the spec-facing flag; --bearer is
// an alias. Both populate the bearer credential.
func TestParseGlobalFlagsTokenAlias(t *testing.T) {
	clearCLIEnv(t)
	f, _ := parseGlobalFlags([]string{"--token", "abc", "health"})
	if f.bearer != "abc" {
		t.Errorf("--token should populate bearer: %q", f.bearer)
	}
	g, _ := parseGlobalFlags([]string{"--bearer", "xyz", "health"})
	if g.bearer != "xyz" {
		t.Errorf("--bearer alias should populate bearer: %q", g.bearer)
	}
}

// spec: §24.0 line 23, §17.6 line 360 — `version` and `--version` print
// the local CLI build and never touch the gateway, so they work before a
// deployment exists. Closes F-24.0.4 and F-24.0.7.
func TestVersionCommandIsOfflineLocal(t *testing.T) {
	clearCLIEnv(t)
	for _, arg := range []string{"version", "--version", "-V"} {
		var stdout, stderr bytes.Buffer
		// An unroutable gateway URL would force a non-zero exit if the
		// command attempted a network call.
		code := run([]string{"--api-url", "http://127.0.0.1:0", arg}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("%s: exit %d, stderr=%q", arg, code, stderr.String())
		}
		out := stdout.String()
		if !strings.Contains(out, version) {
			t.Errorf("%s: output %q missing version %q", arg, out, version)
		}
		if !strings.HasPrefix(out, "lenny-ctl ") {
			t.Errorf("%s: output %q should name the binary", arg, out)
		}
		if stderr.Len() != 0 {
			t.Errorf("%s: unexpected stderr %q", arg, stderr.String())
		}
	}
}

// capturedRequest records what the fake gateway received.
type capturedRequest struct {
	method string
	path   string
	query  string
	body   map[string]any
}

// runAgainstGateway spins a fake gateway, runs the CLI command against
// it, and returns the exit code plus what the gateway saw.
func runAgainstGateway(t *testing.T, status int, response string, args ...string) (int, *capturedRequest) {
	t.Helper()
	got := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.path = r.URL.Path
		if r.URL.Path != "/healthz" {
			got.query = r.URL.RawQuery
		}
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			_ = json.Unmarshal(raw, &got.body)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	defer srv.Close()

	full := append([]string{"--api-url", srv.URL}, args...)
	var stdout, stderr bytes.Buffer
	code := run(full, &stdout, &stderr)
	return code, got
}

func TestAdminCircuitBreakersList(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"breakers":[]}`,
		"admin", "circuit-breakers", "list")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodGet || got.path != "/v1/admin/circuit-breakers" {
		t.Errorf("request: %s %s, want GET /v1/admin/circuit-breakers", got.method, got.path)
	}
}

func TestAdminCircuitBreakersOpen(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"name":"rt-emergency"}`,
		"admin", "circuit-breakers", "open", "rt-emergency",
		"--limit-tier", "runtime", "--scope", "runtime=echo", "--reason", "runaway runtime")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/circuit-breakers/rt-emergency/open" {
		t.Fatalf("request: %s %s", got.method, got.path)
	}
	if got.body["reason"] != "runaway runtime" || got.body["limit_tier"] != "runtime" {
		t.Errorf("body: %+v", got.body)
	}
	scope, _ := got.body["scope"].(map[string]any)
	if scope["runtime"] != "echo" {
		t.Errorf("scope: %+v, want runtime=echo", scope)
	}
}

func TestAdminCircuitBreakersClose(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"name":"rt-emergency"}`,
		"admin", "circuit-breakers", "close", "rt-emergency")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/circuit-breakers/rt-emergency/close" {
		t.Errorf("request: %s %s", got.method, got.path)
	}
}

func TestAdminTenantsListUsesAdminPrefix(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"tenants":[]}`,
		"admin", "tenants", "list")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.path != "/v1/admin/tenants" {
		t.Errorf("path: %q, want /v1/admin/tenants", got.path)
	}
}

// TestAdminTenantsGet covers `lenny-ctl admin tenants get <id>` mapping to
// GET /v1/admin/tenants/{id}; the response carries the §12.8 `state`
// field operators use to monitor the deletion lifecycle. spec: §24.10
// line 127. F-24.10.1, F-24.10.4.
func TestAdminTenantsGet(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"id":"acme","state":"deleting"}`,
		"admin", "tenants", "get", "acme")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodGet || got.path != "/v1/admin/tenants/acme" {
		t.Errorf("request: %s %s, want GET /v1/admin/tenants/acme", got.method, got.path)
	}
}

// TestAdminTenantsDelete covers `lenny-ctl admin tenants delete <id>`
// mapping to DELETE /v1/admin/tenants/{id} (204 No Content), which
// initiates the §12.8 deletion lifecycle. spec: §24.10 line 128.
// F-24.10.1.
func TestAdminTenantsDelete(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusNoContent, ``,
		"admin", "tenants", "delete", "acme")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodDelete || got.path != "/v1/admin/tenants/acme" {
		t.Errorf("request: %s %s, want DELETE /v1/admin/tenants/acme", got.method, got.path)
	}
}

// TestAdminTenantsForceDelete covers `lenny-ctl admin tenants
// force-delete <id> --acknowledge-hold-override --justification <text>`
// mapping to POST /v1/admin/tenants/{id}/force-delete with the override
// body. spec: §24.10 row 4. F-12.8.2, F-24.10.2.
func TestAdminTenantsForceDelete(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusAccepted, `{"id":"acme","state":"disabling"}`,
		"admin", "tenants", "force-delete", "acme",
		"--acknowledge-hold-override", "--justification", "business wind-down")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/tenants/acme/force-delete" {
		t.Fatalf("request: %s %s, want POST /v1/admin/tenants/acme/force-delete", got.method, got.path)
	}
	if got.body["acknowledgeHoldOverride"] != true || got.body["justification"] != "business wind-down" {
		t.Errorf("body: %+v, want the override + justification", got.body)
	}
}

// Without the override, force-delete sends no override body so the gateway
// applies its TENANT_DELETE_BLOCKED_BY_LEGAL_HOLD preflight.
func TestAdminTenantsForceDeleteNoOverride(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusAccepted, `{"id":"acme","state":"disabling"}`,
		"admin", "tenants", "force-delete", "acme")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.path != "/v1/admin/tenants/acme/force-delete" {
		t.Errorf("path: %q", got.path)
	}
	if _, ok := got.body["acknowledgeHoldOverride"]; ok {
		t.Errorf("body must omit the override when not requested: %+v", got.body)
	}
}

// The override requires a justification; the CLI rejects it locally.
func TestAdminTenantsForceDeleteOverrideRequiresJustification(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--api-url", "http://127.0.0.1:0",
		"admin", "tenants", "force-delete", "acme", "--acknowledge-hold-override",
	}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code: got %d, want 2", code)
	}
}

// force-delete requires an <id>.
func TestAdminTenantsForceDeleteRequiresID(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--api-url", "http://127.0.0.1:0",
		"admin", "tenants", "force-delete",
	}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code: got %d, want 2", code)
	}
}

// TestAdminTenantsRotateErasureSalt covers `lenny-ctl admin tenants
// rotate-erasure-salt <id>` mapping to POST
// /v1/admin/tenants/{id}/rotate-erasure-salt. spec: §12.8 line 857.
// F-12.8.5.
func TestAdminTenantsRotateErasureSalt(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"tenantId":"acme","rotated":true}`,
		"admin", "tenants", "rotate-erasure-salt", "acme")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/tenants/acme/rotate-erasure-salt" {
		t.Errorf("request: %s %s, want POST /v1/admin/tenants/acme/rotate-erasure-salt", got.method, got.path)
	}
}

// TestAdminTenantsRotateErasureSaltRequiresID asserts the rotate
// subcommand rejects a missing <id> with exit 2 before any request.
// F-12.8.5.
func TestAdminTenantsRotateErasureSaltRequiresID(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--api-url", "http://127.0.0.1:0", "admin", "tenants", "rotate-erasure-salt"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code: got %d, want 2; stderr=%s", code, stderr.String())
	}
}

// TestAdminTenantsDeleteRequiresID asserts the §24.10 delete subcommand
// rejects a missing <id> with exit 2 before any request. F-24.10.1.
func TestAdminTenantsDeleteRequiresID(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--api-url", "http://127.0.0.1:0", "admin", "tenants", "delete"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("tenants delete without <id>: exit %d, want 2", code)
	}
}

// TestAdminTenantsDeletePropagatesError asserts a server-side rejection
// (e.g. RESOURCE_NOT_FOUND) surfaces as a non-zero exit. F-24.10.1.
func TestAdminTenantsDeletePropagatesError(t *testing.T) {
	code, _ := runAgainstGateway(t, http.StatusNotFound,
		`{"error":{"code":"RESOURCE_NOT_FOUND","message":"tenant not found"}}`,
		"admin", "tenants", "delete", "ghost")
	if code != 1 {
		t.Errorf("tenants delete on missing tenant: exit %d, want 1", code)
	}
}

// TestAdminTenantsUnknownSubcommand asserts the dispatcher rejects an
// unrecognized tenants subcommand with exit 2. F-24.10.1.
func TestAdminTenantsUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--api-url", "http://127.0.0.1:0", "admin", "tenants", "frobnicate"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("unknown tenants subcommand: exit %d, want 2", code)
	}
}

func TestCircuitBreakersOpenRequiresLimitTier(t *testing.T) {
	var stdout, stderr bytes.Buffer
	// No gateway needed: the flag validation fails before any request.
	code := run([]string{"admin", "circuit-breakers", "open", "rt-x"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("missing --limit-tier: exit code %d, want 2", code)
	}
}

// TestAdminPoolsList covers `lenny-ctl admin pools list` mapping to
// GET /v1/admin/pools. spec: §24.4 line 61.
func TestAdminPoolsList(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"pools":[]}`,
		"admin", "pools", "list")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodGet || got.path != "/v1/admin/pools" {
		t.Errorf("request: %s %s, want GET /v1/admin/pools", got.method, got.path)
	}
}

// TestAdminPoolsGet covers `lenny-ctl admin pools get <name>`. spec:
// §24.4 line 62.
func TestAdminPoolsGet(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"name":"p1"}`,
		"admin", "pools", "get", "p1")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodGet || got.path != "/v1/admin/pools/p1" {
		t.Errorf("request: %s %s, want GET /v1/admin/pools/p1", got.method, got.path)
	}
}

// TestAdminPoolsCreate covers `lenny-ctl admin pools create
// --from-file <pool.json>` mapping to POST /v1/admin/pools.
func TestAdminPoolsCreate(t *testing.T) {
	path := writeSeedFile(t, "pool.json",
		`{"name":"p1","runtimeRef":"claude-prod","isolationProfile":"strict"}`)
	code, got := runAgainstGateway(t, http.StatusOK, `{"name":"p1"}`,
		"admin", "pools", "create", "--from-file", path)
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/pools" {
		t.Errorf("request: %s %s, want POST /v1/admin/pools", got.method, got.path)
	}
	if got.body["name"] != "p1" || got.body["isolationProfile"] != "strict" {
		t.Errorf("body: %+v", got.body)
	}
}

// TestAdminPoolsCreateRequiresFromFile rejects a create with no body.
func TestAdminPoolsCreateRequiresFromFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"admin", "pools", "create"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("missing --from-file: exit code %d, want 2", code)
	}
}

// TestAdminPoolsUpdate covers `lenny-ctl admin pools update <name>
// --from-file <pool.json>` mapping to PUT /v1/admin/pools/{name}.
func TestAdminPoolsUpdate(t *testing.T) {
	path := writeSeedFile(t, "pool.json", `{"minWarm":4}`)
	code, got := runAgainstGateway(t, http.StatusOK, `{"name":"p1"}`,
		"admin", "pools", "update", "p1", "--from-file", path)
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPut || got.path != "/v1/admin/pools/p1" {
		t.Errorf("request: %s %s, want PUT /v1/admin/pools/p1", got.method, got.path)
	}
}

// TestAdminPoolsDelete covers DELETE /v1/admin/pools/{name}. spec:
// §15.1 line 796.
func TestAdminPoolsDelete(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{}`,
		"admin", "pools", "delete", "p1")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodDelete || got.path != "/v1/admin/pools/p1" {
		t.Errorf("request: %s %s, want DELETE /v1/admin/pools/p1", got.method, got.path)
	}
}

// TestAdminPoolsSyncStatus covers GET /v1/admin/pools/{name}/sync-status.
// spec: §15.1 line 798.
func TestAdminPoolsSyncStatus(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"inSync":true}`,
		"admin", "pools", "sync-status", "p1")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodGet || got.path != "/v1/admin/pools/p1/sync-status" {
		t.Errorf("request: %s %s, want GET /v1/admin/pools/p1/sync-status", got.method, got.path)
	}
}

// TestAdminPoolsDrain covers POST /v1/admin/pools/{name}/drain.
func TestAdminPoolsDrain(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"status":"draining"}`,
		"admin", "pools", "drain", "p1")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/pools/p1/drain" {
		t.Errorf("request: %s %s, want POST /v1/admin/pools/p1/drain", got.method, got.path)
	}
}

// TestAdminPoolsResumeReconciliation covers
// POST /v1/admin/pools/{name}/resume-reconciliation. spec: §15.1 line 799.
func TestAdminPoolsResumeReconciliation(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"pool":"p1"}`,
		"admin", "pools", "resume-reconciliation", "p1")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/pools/p1/resume-reconciliation" {
		t.Errorf("request: %s %s, want POST /v1/admin/pools/p1/resume-reconciliation", got.method, got.path)
	}
}

// TestAdminPoolsExitBootstrap covers
// DELETE /v1/admin/pools/{name}/bootstrap-override. spec: §24.4 line 64,
// §15.1 line 875.
func TestAdminPoolsExitBootstrap(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"name":"p1"}`,
		"admin", "pools", "exit-bootstrap", "--pool", "p1")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodDelete || got.path != "/v1/admin/pools/p1/bootstrap-override" {
		t.Errorf("request: %s %s, want DELETE /v1/admin/pools/p1/bootstrap-override", got.method, got.path)
	}
}

// TestAdminPoolsExitBootstrapRequiresPool fails fast with exit code 2 when
// --pool is omitted. spec: §24.4 line 64.
func TestAdminPoolsExitBootstrapRequiresPool(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"admin", "pools", "exit-bootstrap"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("exit-bootstrap without --pool: exit code %d, want 2", code)
	}
}

// TestAdminPoolsCircuitBreaker covers
// PUT /v1/admin/pools/{name}/circuit-breaker. The CLI fetches the pool's
// ETag (GET) then issues the PUT with the override body; the captured
// request is the trailing PUT. spec: §24.4 line 75, §15.1 line 801.
func TestAdminPoolsCircuitBreaker(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"name":"p1"}`,
		"admin", "pools", "circuit-breaker", "--pool", "p1", "--state", "enabled")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPut || got.path != "/v1/admin/pools/p1/circuit-breaker" {
		t.Fatalf("request: %s %s, want PUT /v1/admin/pools/p1/circuit-breaker", got.method, got.path)
	}
	sdkWarm, _ := got.body["sdkWarm"].(map[string]any)
	if sdkWarm["circuitBreakerOverride"] != "enabled" {
		t.Errorf("body: %+v, want sdkWarm.circuitBreakerOverride=enabled", got.body)
	}
}

// TestAdminPoolsCircuitBreakerRejectsBadState rejects a --state value
// outside the {enabled, disabled, auto} set before any HTTP call.
// spec: §24.4 line 75.
func TestAdminPoolsCircuitBreakerRejectsBadState(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"admin", "pools", "circuit-breaker", "--pool", "p1", "--state", "sideways"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("circuit-breaker bad --state: exit code %d, want 2", code)
	}
}

// TestAdminPoolsGrantAccess covers
// POST /v1/admin/pools/{name}/tenant-access. spec: §24.4 line 76,
// §15.1 line 802.
func TestAdminPoolsGrantAccess(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{}`,
		"admin", "pools", "grant-access", "--pool", "p1", "--tenant", "acme")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/pools/p1/tenant-access" {
		t.Fatalf("request: %s %s, want POST /v1/admin/pools/p1/tenant-access", got.method, got.path)
	}
	if got.body["tenantId"] != "acme" {
		t.Errorf("body: %+v, want tenantId=acme", got.body)
	}
}

// TestAdminPoolsListAccess covers
// GET /v1/admin/pools/{name}/tenant-access. spec: §24.4 line 77,
// §15.1 line 803.
func TestAdminPoolsListAccess(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"tenants":[]}`,
		"admin", "pools", "list-access", "--pool", "p1")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodGet || got.path != "/v1/admin/pools/p1/tenant-access" {
		t.Errorf("request: %s %s, want GET /v1/admin/pools/p1/tenant-access", got.method, got.path)
	}
}

// TestAdminPoolsRevokeAccess covers
// DELETE /v1/admin/pools/{name}/tenant-access/{tenantId}. spec: §24.4 line
// 78, §15.1 line 804.
func TestAdminPoolsRevokeAccess(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusNoContent, ``,
		"admin", "pools", "revoke-access", "--pool", "p1", "--tenant", "acme")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodDelete || got.path != "/v1/admin/pools/p1/tenant-access/acme" {
		t.Errorf("request: %s %s, want DELETE /v1/admin/pools/p1/tenant-access/acme", got.method, got.path)
	}
}

// TestAdminPoolsAccessRequiresPool fails fast with exit code 2 when --pool
// is omitted from a tenant-access verb. spec: §24.4 lines 76-78.
func TestAdminPoolsAccessRequiresPool(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"admin", "pools", "grant-access", "--tenant", "acme"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("grant-access without --pool: exit code %d, want 2", code)
	}
}

// TestAdminPoolsGrantAccessRequiresTenant fails fast when --tenant is
// omitted from grant-access. spec: §24.4 line 76.
func TestAdminPoolsGrantAccessRequiresTenant(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"admin", "pools", "grant-access", "--pool", "p1"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("grant-access without --tenant: exit code %d, want 2", code)
	}
}

// TestAdminPoolsUnknownSubcommand fails fast with exit code 2.
func TestAdminPoolsUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"admin", "pools", "frobnicate"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("unknown pools subcommand: exit code %d, want 2", code)
	}
}

func TestAdminUnknownResource(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"admin", "widgets"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("unknown admin resource: exit code %d, want 2", code)
	}
}

// spec: §24.12 lines 140-144 — erasure-jobs get / retry /
// clear-restriction CLI group. F-24.12.1.
func TestAdminErasureJobsGet(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"jobId":"erasure_x","phase":"failed"}`,
		"admin", "erasure-jobs", "get", "erasure_x")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodGet || got.path != "/v1/admin/erasure-jobs/erasure_x" {
		t.Errorf("request: %s %s", got.method, got.path)
	}
}

func TestAdminErasureJobsRetry(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusAccepted, `{"jobId":"erasure_x","phase":"initiated"}`,
		"admin", "erasure-jobs", "retry", "erasure_x")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/erasure-jobs/erasure_x/retry" {
		t.Errorf("request: %s %s", got.method, got.path)
	}
}

func TestAdminErasureJobsClearRestriction(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"processingRestricted":false}`,
		"admin", "erasure-jobs", "clear-restriction", "erasure_x", "--justification", "unrecoverable failure")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPost ||
		got.path != "/v1/admin/erasure-jobs/erasure_x/clear-processing-restriction" {
		t.Errorf("request: %s %s", got.method, got.path)
	}
	if got.body["justification"] != "unrecoverable failure" {
		t.Errorf("body: %+v, want justification set", got.body)
	}
}

func TestAdminErasureJobsClearRestrictionRequiresJustification(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"admin", "erasure-jobs", "clear-restriction", "erasure_x"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("clear-restriction without --justification: exit code %d, want 2", code)
	}
}

func TestAdminErasureJobsUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"admin", "erasure-jobs", "frobnicate"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("unknown erasure-jobs subcommand: exit code %d, want 2", code)
	}
}

// writeSeedFile writes a bootstrap seed file into a temp dir and
// returns its path.
func writeSeedFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	return path
}

func TestBootstrapRequiresFromValues(t *testing.T) {
	var stdout, stderr bytes.Buffer
	// No gateway needed: the flag validation fails before any request.
	code := run([]string{"bootstrap"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("bootstrap without --from-values: exit code %d, want 2", code)
	}
}

// spec: §17.6 line 473 — a first run (the gateway created the Secret)
// prints the first-use prompt with the retrieve command; a re-run prints
// the "already exists" notice. F-24.1.7.
func TestBootstrapAdminTokenFirstUsePrompt_spec_17_6_473(t *testing.T) {
	path := writeSeedFile(t, "bootstrap-values.json", `{"tenants":[]}`)

	created := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"adminToken":{"secretCreated":true,"secretNamespace":"lenny-system","secretName":"lenny-admin-token"}}`))
	}))
	defer created.Close()
	var stdout, stderr bytes.Buffer
	if code := run([]string{
		"--api-url", created.URL, "--token", "t",
		"bootstrap", "--from-values", path, "--wait-timeout", "0",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("bootstrap: exit %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Initial admin token written to Secret lenny-system/lenny-admin-token") {
		t.Errorf("first-run prompt missing; stderr=%q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "kubectl get secret lenny-admin-token -n lenny-system") {
		t.Errorf("retrieve command missing; stderr=%q", stderr.String())
	}

	existing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"adminToken":{"secretCreated":false,"secretNamespace":"lenny-system","secretName":"lenny-admin-token"}}`))
	}))
	defer existing.Close()
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{
		"--api-url", existing.URL, "--token", "t",
		"bootstrap", "--from-values", path, "--wait-timeout", "0",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("bootstrap re-run: exit %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Admin token Secret already exists") {
		t.Errorf("re-run notice missing; stderr=%q", stderr.String())
	}
}

func TestBootstrapAppliesJSONSeedFile(t *testing.T) {
	path := writeSeedFile(t, "bootstrap-values.json",
		`{"tenants":[{"id":"acme","displayName":"Acme Corp"}]}`)
	code, got := runAgainstGateway(t, http.StatusOK, `{"tenants":{"createdCount":1}}`,
		"bootstrap", "--from-values", path)
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/bootstrap" {
		t.Fatalf("request: %s %s, want POST /v1/admin/bootstrap", got.method, got.path)
	}
	tenants, _ := got.body["tenants"].([]any)
	if len(tenants) != 1 {
		t.Fatalf("tenants: %+v", got.body)
	}
	tenant, _ := tenants[0].(map[string]any)
	if tenant["id"] != "acme" || tenant["displayName"] != "Acme Corp" {
		t.Errorf("tenant payload: %+v", tenant)
	}
}

func TestBootstrapAppliesYAMLSeedFile(t *testing.T) {
	// sigs.k8s.io/yaml decodes the bootstrap-values.yaml form the
	// Helm chart renders; the gateway receives the same JSON body a
	// JSON seed file would have produced.
	path := writeSeedFile(t, "bootstrap-values.yaml", `tenants:
  - id: acme
    displayName: Acme Corp
runtimes:
  - name: echo
    image: ghcr.io/lennylabs/echo:latest
users:
  - subject: alice@acme.com
    tenantId: acme
    roles:
      - user
`)
	code, got := runAgainstGateway(t, http.StatusOK, `{"tenants":{"createdCount":1}}`,
		"bootstrap", "--from-values", path)
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/bootstrap" {
		t.Fatalf("request: %s %s, want POST /v1/admin/bootstrap", got.method, got.path)
	}
	tenants, _ := got.body["tenants"].([]any)
	runtimes, _ := got.body["runtimes"].([]any)
	users, _ := got.body["users"].([]any)
	if len(tenants) != 1 || len(runtimes) != 1 || len(users) != 1 {
		t.Fatalf("seed body: %+v", got.body)
	}
	tenant, _ := tenants[0].(map[string]any)
	if tenant["id"] != "acme" {
		t.Errorf("tenant from YAML: %+v", tenant)
	}
	runtime, _ := runtimes[0].(map[string]any)
	if runtime["name"] != "echo" || runtime["image"] != "ghcr.io/lennylabs/echo:latest" {
		t.Errorf("runtime from YAML: %+v", runtime)
	}
	user, _ := users[0].(map[string]any)
	if user["subject"] != "alice@acme.com" || user["tenantId"] != "acme" {
		t.Errorf("user from YAML: %+v", user)
	}
}

func TestBootstrapRejectsMalformedSeedFile(t *testing.T) {
	path := writeSeedFile(t, "bootstrap-values.yaml", "tenants: [unterminated")
	var stdout, stderr bytes.Buffer
	// --wait-timeout=0 skips the readiness poll so a unit test that
	// never starts a gateway does not block on /healthz.
	code := run([]string{"bootstrap", "--wait-timeout", "0", "--from-values", path}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("malformed seed file: exit code %d, want 1", code)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("not valid YAML or JSON")) {
		t.Errorf("stderr: %q, want a YAML-or-JSON parse error", stderr.String())
	}
}

// spec: §17.6 line 421 — the bootstrap CLI accepts --wait-timeout
// (default 120s) for the gateway readiness poll.
func TestBootstrapWaitTimeoutFlag_spec_17_6_421(t *testing.T) {
	path := writeSeedFile(t, "bootstrap-values.json",
		`{"tenants":[{"id":"acme","displayName":"Acme Corp"}]}`)
	code, got := runAgainstGateway(t, http.StatusOK, `{"tenants":{"createdCount":1}}`,
		"bootstrap", "--wait-timeout", "0", "--from-values", path)
	if code != 0 {
		t.Fatalf("--wait-timeout: exit code %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/bootstrap" {
		t.Fatalf("--wait-timeout: %s %s, want POST /v1/admin/bootstrap", got.method, got.path)
	}
}

// spec: §24.1 line 35 — --dry-run maps to ?dryRun=true; §17.6 line 450 —
// --force-update maps to ?forceUpdate=true.
func TestBootstrapDryRunAndForceUpdateQuery_spec_24_1_35(t *testing.T) {
	for _, tc := range []struct {
		name      string
		flags     []string
		wantQuery string
	}{
		{"dry-run", []string{"--dry-run"}, "dryRun=true"},
		{"force-update", []string{"--force-update"}, "forceUpdate=true"},
		{"both", []string{"--dry-run", "--force-update"}, "dryRun=true&forceUpdate=true"},
		{"neither", nil, ""},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			path := writeSeedFile(t, "bootstrap-values.json",
				`{"tenants":[{"id":"acme","displayName":"Acme Corp"}]}`)
			args := append([]string{"bootstrap", "--wait-timeout", "0", "--from-values", path}, tc.flags...)
			code, got := runAgainstGateway(t, http.StatusOK, `{"tenants":{"createdCount":1}}`, args...)
			if code != 0 {
				t.Fatalf("exit code %d, want 0", code)
			}
			if got.query != tc.wantQuery {
				t.Errorf("query = %q, want %q", got.query, tc.wantQuery)
			}
		})
	}
}

// spec: §17.6 line 420 — exit 0 = all seeded, 1 = validation error,
// 2 = partial failure.
func TestBootstrapExitCodeMapping_spec_17_6_420(t *testing.T) {
	for _, tc := range []struct {
		name string
		body bootstrapResult
		want int
	}{
		{
			name: "all created",
			body: bootstrapResult{Tenants: bootstrapSection{CreatedCount: 2}},
			want: 0,
		},
		{
			name: "all skipped is still success",
			body: bootstrapResult{Tenants: bootstrapSection{SkippedCount: 2}},
			want: 0,
		},
		{
			name: "partial: one created, one store error",
			body: bootstrapResult{Tenants: bootstrapSection{
				CreatedCount: 1,
				Errors:       []bootstrapErr{{ID: "bad", Code: "SEED_STORE_ERROR"}},
			}},
			want: 2,
		},
		{
			name: "pure validation failure, nothing seeded",
			body: bootstrapResult{Tenants: bootstrapSection{
				Errors: []bootstrapErr{{ID: "bad", Code: "SEED_VALIDATION"}},
			}},
			want: 1,
		},
		{
			name: "security-critical block dominates even with successes",
			body: bootstrapResult{Runtimes: bootstrapSection{
				CreatedCount: 3,
				Errors:       []bootstrapErr{{ID: "echo", Code: "SEED_SECURITY_CRITICAL_FIELD"}},
			}},
			want: 1,
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.body.exitCode(); got != tc.want {
				t.Errorf("exitCode() = %d, want %d", got, tc.want)
			}
		})
	}
}

// spec: §17.6 line 420 — a 207 partial-failure POST response makes the
// CLI exit 2; the readiness poll is skipped so the test never blocks.
func TestBootstrapPartialFailureExitsTwo_spec_17_6_420(t *testing.T) {
	path := writeSeedFile(t, "bootstrap-values.json",
		`{"tenants":[{"id":"acme"},{"id":"with space"}]}`)
	code, _ := runAgainstGateway(t, http.StatusMultiStatus,
		`{"tenants":{"createdCount":1,"errors":[{"id":"with space","code":"SEED_VALIDATION","message":"bad id"}]}}`,
		"bootstrap", "--wait-timeout", "0", "--from-values", path)
	if code != 2 {
		t.Errorf("partial failure: exit code %d, want 2", code)
	}
}

func TestParseOpenBreaker(t *testing.T) {
	body, err := parseOpenBreaker([]string{
		"--limit-tier", "pool", "--scope", "pool=pool-1", "--reason", "drain",
	})
	if err != nil {
		t.Fatalf("parseOpenBreaker: %v", err)
	}
	if body["limit_tier"] != "pool" || body["reason"] != "drain" {
		t.Errorf("body: %+v", body)
	}
	scope, _ := body["scope"].(map[string]string)
	if scope["pool"] != "pool-1" {
		t.Errorf("scope: %+v", scope)
	}

	if _, err := parseOpenBreaker([]string{"--scope", "pool=pool-1"}); err == nil {
		t.Error("parseOpenBreaker without --limit-tier should error")
	}
	if _, err := parseOpenBreaker([]string{"--limit-tier", "pool", "--scope", "nokey"}); err == nil {
		t.Error("parseOpenBreaker with a malformed --scope should error")
	}
	if _, err := parseOpenBreaker([]string{"--limit-tier", "pool", "--bogus"}); err == nil {
		t.Error("parseOpenBreaker with an unknown flag should error")
	}
}

// TestParseOpenBreakerRequiresReason asserts the §24.7 line 106
// required `--reason <text>` flag is enforced client-side so the audit
// event does not silently degrade. F-24.7.1.
func TestParseOpenBreakerRequiresReason_spec_24_7_106(t *testing.T) {
	_, err := parseOpenBreaker([]string{"--limit-tier", "runtime", "--scope", "runtime=echo"})
	if err == nil {
		t.Fatal("parseOpenBreaker without --reason should error")
	}
	if !strings.Contains(err.Error(), "--reason") {
		t.Errorf("error %q should mention --reason", err.Error())
	}
	// An empty-string reason is rejected the same way as a missing one.
	_, err = parseOpenBreaker([]string{"--limit-tier", "runtime", "--scope", "runtime=echo", "--reason", "   "})
	if err == nil {
		t.Fatal("parseOpenBreaker with whitespace-only --reason should error")
	}
}

// TestParseOpenBreakerRequiresScope asserts the §24.7 line 106
// required `--scope <key>=<value>` flag is enforced client-side so
// the operator sees a deterministic CLI error rather than a server
// 422 INVALID_BREAKER_SCOPE. F-24.7.2.
func TestParseOpenBreakerRequiresScope_spec_24_7_106(t *testing.T) {
	_, err := parseOpenBreaker([]string{"--limit-tier", "runtime", "--reason", "incident"})
	if err == nil {
		t.Fatal("parseOpenBreaker without --scope should error")
	}
	if !strings.Contains(err.Error(), "--scope") {
		t.Errorf("error %q should mention --scope", err.Error())
	}
}

// TestLennyCtlDelegatesLocalStatus confirms the §24.19 line 266
// "one binary, two names" alias: `lenny-ctl status` behaves identically
// to `lenny status`, dispatching to the Embedded Mode stack rather than
// failing with an unknown-command error. F-24.19.2.
func TestLennyCtlDelegatesLocalStatus_spec_24_19_266(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := run([]string{"status"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d (stderr %q), want 0", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "no embedded stack is running") {
		t.Errorf("stdout = %q, want the local-stack status output", stdout.String())
	}
}

// TestLennyCtlDelegatesTokenPrint confirms the §24.9.3 alias: `lenny-ctl
// token print` resolves to the embedded token-mint path and returns the
// §24.9 exit code 3 EMBEDDED_MODE_REQUIRED when no stack is running,
// rather than `unknown command "token"`. F-24.9.3 / F-24.9.2.
func TestLennyCtlDelegatesTokenPrint_spec_24_9_120(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := run([]string{"token", "print"}, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("exit = %d (stderr %q), want 3 (EMBEDDED_MODE_REQUIRED)", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "unknown command") {
		t.Errorf("stderr = %q, want the token command to be recognised", stderr.String())
	}
}

// TestLennyCtlDelegatesRestart confirms `lenny-ctl restart <component>`
// is wired to the §24.19 local-command surface. F-24.19.1 / F-24.19.2.
func TestLennyCtlDelegatesRestart_spec_24_19_264(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := run([]string{"restart", "gateway"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (no running stack)", code)
	}
	if !strings.Contains(stderr.String(), "no embedded stack is running") {
		t.Errorf("stderr = %q, want a no-stack message", stderr.String())
	}
}

// TestSessionDelegatesToLocalCLI confirms `lenny-ctl session ...` reaches
// the shared §24.19 localcli session dispatcher, so the §24.17 session
// command tree is available under the long binary name (the "one binary,
// two names" contract). F-24.17.1.
func TestSessionDelegatesToLocalCLI(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"session"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("lenny-ctl session (no subcommand): code=%d, want 2 (stderr=%s)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "new --runtime") {
		t.Errorf("lenny-ctl session did not reach the session usage: %q", stderr.String())
	}
}
