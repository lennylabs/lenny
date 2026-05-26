// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestParseGlobalFlagsDefaultsAPIURL(t *testing.T) {
	f, rest := parseGlobalFlags([]string{"health"})
	if f.apiURL != "http://localhost:8080" {
		t.Errorf("default apiURL: %q", f.apiURL)
	}
	if len(rest) != 1 || rest[0] != "health" {
		t.Errorf("rest: %v", rest)
	}
}

func TestParseGlobalFlagsStopsAtCommand(t *testing.T) {
	// A bare command with no global flags.
	f, rest := parseGlobalFlags([]string{"admin", "--api-url", "ignored"})
	if f.apiURL != "http://localhost:8080" {
		t.Errorf("apiURL should keep default when flags come after command: %q", f.apiURL)
	}
	if len(rest) != 3 {
		t.Errorf("rest should include everything from the command on: %v", rest)
	}
}

// capturedRequest records what the fake gateway received.
type capturedRequest struct {
	method string
	path   string
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
	code := run([]string{"bootstrap", "--from-values", path}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("malformed seed file: exit code %d, want 1", code)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("not valid YAML or JSON")) {
		t.Errorf("stderr: %q, want a YAML-or-JSON parse error", stderr.String())
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
