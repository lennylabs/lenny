// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for the §25.1 enforcement-point-2 scope gate
// ("MCP tool invocation — /mcp/management tools/call checks the scope
// before dispatch") driven with a real, signed Bearer JWT against the
// deployed cmd/lenny-ops binary.
//
// The existing scope-enforcement coverage (pkg/common/scopes,
// pkg/ops/mcp/server_test.go, tests/tier3_contract/ops_endpoints/
// mcp_test.go and locks_test.go, pkg/gateway/admin/me_test.go,
// pkg/ops/me/service_test.go, pkg/gateway/playground/playground_test.go)
// pins the matching logic and the rejection response entirely in-process:
// pkg/ops/mcp/server_test.go injects the caller's scope claim via the
// X-Lenny-Scope test header directly against a mock Invoker, never
// through a verified Bearer JWT. None of it runs the real cmd/lenny-ops
// composition root's OIDC auth middleware (pkg/gateway/middleware/auth),
// so none of it proves the caller's RFC 9068 `scope` claim actually
// survives from a signed token, through the verifier, onto the
// authenticated Principal, and into the §25.1 scope gate the MCP
// dispatch loop enforces on the replayed REST call.
//
// This test signs a real Bearer JWT with the §17.4 bearer-trust HMAC key
// mechanism cmd/lenny-ops's own auth_config_test.go (TestBuildAuthConfig_
// WithKeyAndIssuer) and the tier5 e2e Kind suite (admin_scope_narrowing_
// test.go) both use to stand up a verifiable token without a live OIDC
// provider: --bearer-trust-hmac-key-file configures the same trust-key
// verification path a deployer configures against their OIDC provider's
// embedded key, and --oidc-issuer-url pins the expected `iss` claim the
// same way. It boots the real binary with that verifier wired, presents
// the token as `Authorization: Bearer`, and calls a mutating tool
// (admin.set_pool_warm_count, x-lenny-scope tools:pool:write) through
// POST /mcp/management with a token scoped only to tools:health:read.
//
// spec: §25.1 lines 89-94 — "Scopes are enforced in three places: ...
// 2. MCP tool invocation — /mcp/management tools/call checks the scope
// before dispatch." and "A request for a tool not permitted by any scope
// returns 403 SCOPE_FORBIDDEN with a response body listing the caller's
// active scopes."
package tier4_integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/opsprocess"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// scopeLiveBearerHMACKeyFile mirrors the on-disk format
// jwt.LoadHMACKeyFile reads (pkg/auth/jwt/keyfile.go): a key id and a
// raw secret, with encoding/json base64-decoding the secret
// automatically because its Go type is []byte. This is the same format
// cmd/lenny-ops/auth_config_test.go's writeHMACKeyFile and
// tests/tier5_e2e_kind/admin_scope_narrowing_test.go's hmacKeyFile use.
type scopeLiveBearerHMACKeyFile struct {
	KeyID  string `json:"keyId"`
	Secret []byte `json:"secret"`
}

// writeScopeLiveBearerHMACKeyFile writes a key file cmd/lenny-ops's
// --bearer-trust-hmac-key-file flag accepts and returns its path.
func writeScopeLiveBearerHMACKeyFile(t *testing.T, keyID, secret string) string {
	t.Helper()
	raw, err := json.Marshal(scopeLiveBearerHMACKeyFile{KeyID: keyID, Secret: []byte(secret)})
	if err != nil {
		t.Fatalf("marshal hmac key file: %v", err)
	}
	p := filepath.Join(t.TempDir(), "bearer-trust-key.json")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatalf("write hmac key file: %v", err)
	}
	return p
}

// rpcCallToolsCall posts a §25.12 tools/call JSON-RPC request to the
// deployed lenny-ops /mcp/management endpoint, presenting bearer as the
// caller's Authorization: Bearer credential, and returns the decoded
// JSON-RPC response envelope.
func rpcCallToolsCall(t *testing.T, baseURL, bearer, toolName string, args map[string]any) map[string]any {
	t.Helper()
	reqBody := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": args,
		},
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("marshal tools/call request: %v", err)
	}
	httpReq, err := http.NewRequest(http.MethodPost, baseURL+"/mcp/management", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("build tools/call request: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("POST /mcp/management tools/call: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read tools/call response body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /mcp/management tools/call: status %d, want 200 (JSON-RPC envelope); body=%s", resp.StatusCode, body)
	}
	var env map[string]any
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode JSON-RPC response: %v; body=%s", err, body)
	}
	return env
}

// spec: §25.1 lines 89-94 ("Scopes are enforced in three places: ...
// 2. MCP tool invocation — /mcp/management tools/call checks the scope
// before dispatch." "A request for a tool not permitted by any scope
// returns 403 SCOPE_FORBIDDEN with a response body listing the caller's
// active scopes.")
//
// diagnosis: a failure here means the deployed cmd/lenny-ops binary's
// §25.1 scope gate does not actually enforce a real Bearer JWT's RFC
// 9068 `scope` claim on an MCP tools/call invocation. Every existing
// scope-rejection test injects the claim in-process (a mock Principal or
// the X-Lenny-Scope test header) rather than through a verified
// Authorization: Bearer token, so none of them would catch a regression
// in the OIDC auth middleware -> Principal.Scopes -> MCP-replay wiring
// this test exercises end to end: a narrowly-scoped, genuinely signed
// and verified token invoking a tool outside its scope must be rejected
// before the underlying admin handler runs, and the rejection must name
// both the tool's required scope and the caller's actual active scope.
func TestOpsMCPToolsCallScopeGateRejectsLiveBearerE2E(t *testing.T) {
	opsprocess.SkipUnlessAvailable(t)

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	rd := containers.StartRedis(t, containers.RedisOptions{})

	const keyID = "tier4-scope-probe-key"
	const secret = "tier4-scope-probe-secret-material"
	const issuer = "https://idp.acme.test/scope-probe"
	keyPath := writeScopeLiveBearerHMACKeyFile(t, keyID, secret)

	ops := opsprocess.StartWith(
		t,
		"--postgres-dsn="+pg.DSN,
		"--redis-url=redis://"+rd.Addr+"/0",
		"--redis-allow-insecure",
		"--bearer-trust-hmac-key-file="+keyPath,
		"--oidc-issuer-url="+issuer,
	)
	base := ops.BaseURL()

	// Sign the token with the same HMAC trust key --bearer-trust-hmac-
	// key-file wires into the deployed lenny-ops verifier, so this is a
	// genuine Authorization: Bearer credential the running binary
	// verifies (signature + issuer) rather than an injected Principal.
	signer := jwt.NewHMACSigner(keyID, []byte(secret))
	now := time.Now()
	bearer, err := signer.Sign(jwt.Claims{
		Subject:  "sa-tier4-pool-scaling-bot",
		Issuer:   issuer,
		Roles:    []auth.Role{auth.RolePlatformAdmin},
		Scope:    "tools:health:read",
		IssuedAt: now.Unix(),
		Expiry:   now.Add(time.Hour).Unix(),
		JWTID:    fmt.Sprintf("tier4-scope-probe-%d", now.UnixNano()),
	})
	if err != nil {
		t.Fatalf("sign scoped bearer: %v", err)
	}

	// admin.set_pool_warm_count declares x-lenny-scope tools:pool:write
	// (pkg/ops/mcp/generated_tools.go): the §25.17 pool-scale tool this
	// finding's suggested test names. The bearer above carries only
	// tools:health:read, so the tools/call scope gate must reject it
	// before the mapped PUT /v1/admin/pools/{name}/warm-count replay
	// runs.
	env := rpcCallToolsCall(t, base, bearer, "admin.set_pool_warm_count",
		map[string]any{"name": "tier4-scope-probe-pool"})

	if env["error"] != nil {
		t.Fatalf("tools/call returned a JSON-RPC error instead of a tool result: %v", env["error"])
	}
	result, _ := env["result"].(map[string]any)
	if result == nil {
		t.Fatalf("tools/call response carries no result object: %v", env)
	}
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Fatalf("result.isError = %v, want true (scope-forbidden tool call); result=%v", result["isError"], result)
	}
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("result.content is empty; result=%v", result)
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	if text == "" {
		t.Fatalf("result.content[0].text is empty; result=%v", result)
	}

	// text carries the replayed REST call's §25.2 canonical error
	// envelope (pkg/ops/opsserver/scope_enforce.go writeOpsScopeForbidden):
	// {"error":{"code":"SCOPE_FORBIDDEN","details":{"requiredScope":...,
	// "activeScope":...}}}.
	var errEnvelope struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(text), &errEnvelope); err != nil {
		t.Fatalf("decode replayed REST error envelope from content[0].text: %v; text=%s", err, text)
	}
	if errEnvelope.Error.Code != "SCOPE_FORBIDDEN" {
		t.Errorf("error.code = %q, want SCOPE_FORBIDDEN; text=%s", errEnvelope.Error.Code, text)
	}
	if got := errEnvelope.Error.Details["requiredScope"]; got != "tools:pool:write" {
		t.Errorf("error.details.requiredScope = %v, want tools:pool:write; text=%s", got, text)
	}
	if got := errEnvelope.Error.Details["activeScope"]; got != "tools:health:read" {
		t.Errorf("error.details.activeScope = %v, want tools:health:read (the caller's active scopes); text=%s", got, text)
	}
}
