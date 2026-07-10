// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for §12.9.6 input fuzzing. TESTING.md states:
// "OWASP ZAP runs against the REST and MCP surfaces with the project's
// policy. Oversize payloads, malformed JSON, SQL-injection strings,
// path-traversal in artifact keys, oversize headers, and deeply nested
// objects are all rejected with the appropriate error codes." The full
// ZAP run itself is release-engineering tooling (tests/testinfra/security/
// zap wraps the CLI and is exercised by zap_test.go's report-parsing
// unit tests), gated on zap.sh being on PATH. This file supplies the
// in-tree black-box battery the ZAP run would otherwise be the only
// coverage for: it boots a real lenny-gateway subprocess (the same
// binary ZAP would point at) via tests/testinfra/gateway and drives the
// six §12.9.6-named adversarial categories (oversize headers, deeply
// nested objects, path-traversal artifact keys, oversize payloads,
// malformed JSON, and SQL-injection strings) at the REST and MCP
// surfaces, asserting each probe is rejected rather than crashing the
// process or falling through to a 200.
package tier9_security_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// blackboxFuzzTenant is the dev-mode tenant every probe in this file
// authenticates as. The probes target input validation ahead of any
// tenant-scoped business logic, so a fixed tenant is sufficient.
const blackboxFuzzTenant = "acme"

// spec: 12.9.6
// diagnosis: an oversize request header crashed the gateway subprocess,
// hung the connection, or reached a handler and returned 2xx instead of
// being rejected at the transport boundary. The gateway's http.Server
// does not override MaxHeaderBytes, so net/http's built-in cap (1 MiB)
// governs; this test pins that a request whose headers exceed the cap
// is rejected with a 4xx status and the gateway process survives to
// serve the next request.
func TestBlackboxFuzzOversizeHeaderRejected(t *testing.T) {
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all")

	req, err := http.NewRequest(http.MethodGet, gw.BaseURL()+"/v1/sessions", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("X-Lenny-Tenant-ID", blackboxFuzzTenant)
	// 2 MiB comfortably exceeds net/http's 1 MiB DefaultMaxHeaderBytes.
	req.Header.Set("X-Lenny-Fuzz-Oversize-Header", strings.Repeat("a", 2*1024*1024))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("oversize-header request errored instead of receiving a rejection response: %v", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 400 || resp.StatusCode > 499 {
		t.Errorf("oversize header: want a 4xx rejection, got %d", resp.StatusCode)
	}

	// The gateway process must still be answering normal requests: an
	// oversize-header probe that took the listener down would be a far
	// more serious defect than a wrong status code.
	follow, err := http.NewRequest(http.MethodGet, gw.BaseURL()+"/v1/sessions", nil)
	if err != nil {
		t.Fatalf("build follow-up request: %v", err)
	}
	follow.Header.Set("X-Lenny-Tenant-ID", blackboxFuzzTenant)
	followResp, err := http.DefaultClient.Do(follow)
	if err != nil {
		t.Fatalf("gateway did not survive the oversize-header probe: %v", err)
	}
	defer followResp.Body.Close()
	io.Copy(io.Discard, followResp.Body)
	if followResp.StatusCode != http.StatusOK {
		t.Errorf("follow-up request after oversize header: want 200, got %d", followResp.StatusCode)
	}
}

// spec: 12.9.6
// diagnosis: a deeply nested JSON body either crashed or hung the
// decoder, or was silently accepted where the wire contract required a
// string (runtimeRef). POST /v1/sessions/start decodes the body into
// CreateAndStartRequest, whose RuntimeRef field is a string; supplying
// a 64-level-deep object where a string is expected forces the decoder
// to walk the full nested structure before failing the type check, and
// the handler must still answer with the documented 400 VALIDATION_ERROR
// envelope rather than a 500 or an unhandled panic.
func TestBlackboxFuzzDeeplyNestedJSONBodyRejected(t *testing.T) {
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all")

	body := fmt.Sprintf(`{"runtimeRef":%s,"userId":"fuzz-probe"}`, nestedJSON(64))

	req, err := http.NewRequest(http.MethodPost, gw.BaseURL()+"/v1/sessions/start", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", blackboxFuzzTenant)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("deeply-nested-JSON request errored instead of receiving a rejection response: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("deeply nested JSON body: want 400, got %d (body %s)", resp.StatusCode, raw)
	}
	envelope := decodeErrorEnvelope(t, raw)
	if envelope["code"] != "VALIDATION_ERROR" {
		t.Errorf("deeply nested JSON body: want error.code VALIDATION_ERROR, got %v", envelope["code"])
	}
}

// spec: 12.9.6
// diagnosis: a path-traversal-laden artifact key reached the blob store
// instead of being rejected at the URI parse boundary, or the rejection
// surfaced as something other than the documented 400 VALIDATION_ERROR
// (e.g. a 500 that leaks an internal path). GET /v1/blobs/{ref} parses
// the path segment as a lenny-blob:// URI before any store lookup; both
// a bare traversal string (no lenny-blob:// scheme at all) and a
// scheme-valid URI whose path segments include ".." must fail that
// parse and never reach the store.
func TestBlackboxFuzzPathTraversalArtifactKeyRejected(t *testing.T) {
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all")

	cases := []struct {
		name string
		ref  string
	}{
		{"bare_traversal_no_scheme", "../../../../etc/passwd"},
		{"scheme_valid_traversal_segments", "lenny-blob://acme/upload/../../../etc/passwd?ttl=60"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// url.PathEscape (matching the pkg/gateway/sessionserver
			// blobURL test helper's convention) percent-encodes the
			// traversal segments and the scheme's doubled slash so
			// http.ServeMux's path-cleaning redirect never sees a raw
			// ".." segment to collapse; r.PathValue re-decodes the
			// segment before handleBlob sees it, so the traversal
			// payload reaches ParseURI intact.
			target := gw.BaseURL() + "/v1/blobs/" + url.PathEscape(tc.ref)
			req, err := http.NewRequest(http.MethodGet, target, nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			req.Header.Set("X-Lenny-Tenant-ID", blackboxFuzzTenant)

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("path-traversal artifact-key request errored instead of receiving a rejection response: %v", err)
			}
			defer resp.Body.Close()
			raw, _ := io.ReadAll(resp.Body)

			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("path-traversal artifact key %q: want 400, got %d (body %s)", tc.ref, resp.StatusCode, raw)
			}
			envelope := decodeErrorEnvelope(t, raw)
			if envelope["code"] != "VALIDATION_ERROR" {
				t.Errorf("path-traversal artifact key %q: want error.code VALIDATION_ERROR, got %v", tc.ref, envelope["code"])
			}
		})
	}
}

// spec: 12.9.6
// diagnosis: the MCP JSON-RPC transport accepted or crashed on a
// deeply nested value in a string-typed field (method) instead of
// rejecting it with the shared lenny error envelope. §15.2.1 rule 3
// (TESTING.md 12.9.6's "REST and MCP surfaces" — carried at the wire
// level by pkg/gateway/mcpfabric/mcp) states that even transport-level
// JSON-RPC errors carry the shared lenny envelope (code, category,
// retryable) in error.data, so an MCP-surface rejection is asserted
// against that envelope rather than an HTTP status: JSON-RPC reports
// errors inside a 200 response body, not via the HTTP status line.
func TestBlackboxFuzzDeeplyNestedJSONRPCBodyRejectedByMCP(t *testing.T) {
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all")

	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%s}`, nestedJSON(64))

	req, err := http.NewRequest(http.MethodPost, gw.BaseURL()+"/mcp", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", blackboxFuzzTenant)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("deeply-nested JSON-RPC request errored instead of receiving a rejection response: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var rpc struct {
		Error *struct {
			Code    int             `json:"code"`
			Message string          `json:"message"`
			Data    json.RawMessage `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &rpc); err != nil {
		t.Fatalf("MCP response is not valid JSON-RPC: %v (body %s)", err, raw)
	}
	if rpc.Error == nil {
		t.Fatalf("deeply nested JSON-RPC method: want a JSON-RPC error, got success (body %s)", raw)
	}
	var data map[string]any
	if err := json.Unmarshal(rpc.Error.Data, &data); err != nil {
		t.Fatalf("JSON-RPC error.data is not the shared lenny envelope: %v (data %s)", err, rpc.Error.Data)
	}
	if data["code"] != "VALIDATION_ERROR" {
		t.Errorf("deeply nested JSON-RPC method: want error.data.code VALIDATION_ERROR, got %v", data["code"])
	}
}

// spec: 12.9.6
// diagnosis: an oversize request payload on a critical operation
// crashed the gateway subprocess, hung the connection, or reached the
// inner handler and returned 2xx instead of being rejected at the
// idempotency boundary. Any POST carrying an Idempotency-Key header on
// one of the §11.5 critical-operation paths is buffered and hashed by
// the idempotency middleware before the inner handler runs; a body
// past the configured cap must be rejected with a 413 BODY_TOO_LARGE
// envelope and the gateway process must survive to serve the next
// request.
func TestBlackboxFuzzOversizePayloadRejected(t *testing.T) {
	// A tight cap keeps the oversize probe body small and the test
	// fast; the default (8 MiB) is exercised in the idempotency
	// package's own unit tests (idempotency_test.go).
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all", "--idempotency-max-body-bytes=1024")

	oversizeBody := []byte(fmt.Sprintf(`{"runtimeRef":"fuzz-runtime","userId":"%s"}`, strings.Repeat("a", 2048)))

	req, err := http.NewRequest(http.MethodPost, gw.BaseURL()+"/v1/sessions/start", bytes.NewReader(oversizeBody))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", blackboxFuzzTenant)
	req.Header.Set("Idempotency-Key", "blackbox-fuzz-oversize-payload")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("oversize-payload request errored instead of receiving a rejection response: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize payload: want 413, got %d (body %s)", resp.StatusCode, raw)
	}
	envelope := decodeErrorEnvelope(t, raw)
	if envelope["code"] != "BODY_TOO_LARGE" {
		t.Errorf("oversize payload: want error.code BODY_TOO_LARGE, got %v", envelope["code"])
	}

	// The gateway process must still be answering normal requests after
	// the oversize-payload probe.
	follow, err := http.NewRequest(http.MethodGet, gw.BaseURL()+"/v1/sessions", nil)
	if err != nil {
		t.Fatalf("build follow-up request: %v", err)
	}
	follow.Header.Set("X-Lenny-Tenant-ID", blackboxFuzzTenant)
	followResp, err := http.DefaultClient.Do(follow)
	if err != nil {
		t.Fatalf("gateway did not survive the oversize-payload probe: %v", err)
	}
	defer followResp.Body.Close()
	io.Copy(io.Discard, followResp.Body)
	if followResp.StatusCode != http.StatusOK {
		t.Errorf("follow-up request after oversize payload: want 200, got %d", followResp.StatusCode)
	}
}

// spec: 12.9.6
// diagnosis: malformed or truncated JSON either crashed the decoder or
// was silently accepted, instead of surfacing the documented 400
// VALIDATION_ERROR envelope. POST /v1/sessions/start decodes the body
// with encoding/json; a body that is not syntactically valid JSON at
// all (unterminated object) must fail the decode and the handler must
// still answer with the shared error envelope rather than a 500 or an
// unhandled panic.
func TestBlackboxFuzzMalformedJSONBodyRejected(t *testing.T) {
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all")

	cases := []struct {
		name string
		body string
	}{
		{"truncated_object", `{"runtimeRef":"fuzz-runtime","userId":`},
		{"unquoted_garbage", `{not valid json at all`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, gw.BaseURL()+"/v1/sessions/start", strings.NewReader(tc.body))
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Lenny-Tenant-ID", blackboxFuzzTenant)

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("malformed-JSON request errored instead of receiving a rejection response: %v", err)
			}
			defer resp.Body.Close()
			raw, _ := io.ReadAll(resp.Body)

			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("malformed JSON body %q: want 400, got %d (body %s)", tc.body, resp.StatusCode, raw)
			}
			envelope := decodeErrorEnvelope(t, raw)
			if envelope["code"] != "VALIDATION_ERROR" {
				t.Errorf("malformed JSON body %q: want error.code VALIDATION_ERROR, got %v", tc.body, envelope["code"])
			}
		})
	}
}

// spec: 12.9.6
// diagnosis: a SQL-injection-shaped string presented as a tenant claim
// either reached a downstream query unescaped or was accepted where
// the wire contract requires the §10.2 tenant_id pattern. The
// dev-header auth path (X-Lenny-Tenant-ID) runs every claim through
// auth.ValidateTenantID's ^[a-zA-Z0-9_-]{1,128}$ pattern before it is
// ever used to scope a lookup; a classic SQLi payload contains
// characters the pattern rejects (quotes, spaces, semicolons), so the
// request must fail closed with the documented 401
// TENANT_CLAIM_INVALID_FORMAT envelope rather than reach any query or
// crash the process.
func TestBlackboxFuzzSQLInjectionTenantClaimRejected(t *testing.T) {
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all")

	cases := []struct {
		name   string
		tenant string
	}{
		{"classic_or_true", `' OR '1'='1`},
		{"stacked_drop_table", `acme'; DROP TABLE sessions; --`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, gw.BaseURL()+"/v1/sessions", nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			req.Header.Set("X-Lenny-Tenant-ID", tc.tenant)

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("SQL-injection tenant-claim request errored instead of receiving a rejection response: %v", err)
			}
			defer resp.Body.Close()
			raw, _ := io.ReadAll(resp.Body)

			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("SQL-injection tenant claim %q: want 401, got %d (body %s)", tc.tenant, resp.StatusCode, raw)
			}
			envelope := decodeErrorEnvelope(t, raw)
			if envelope["code"] != "TENANT_CLAIM_INVALID_FORMAT" {
				t.Errorf("SQL-injection tenant claim %q: want error.code TENANT_CLAIM_INVALID_FORMAT, got %v", tc.tenant, envelope["code"])
			}
		})
	}

	// The gateway process must still be answering normal requests after
	// the SQL-injection probes.
	follow, err := http.NewRequest(http.MethodGet, gw.BaseURL()+"/v1/sessions", nil)
	if err != nil {
		t.Fatalf("build follow-up request: %v", err)
	}
	follow.Header.Set("X-Lenny-Tenant-ID", blackboxFuzzTenant)
	followResp, err := http.DefaultClient.Do(follow)
	if err != nil {
		t.Fatalf("gateway did not survive the SQL-injection tenant-claim probes: %v", err)
	}
	defer followResp.Body.Close()
	io.Copy(io.Discard, followResp.Body)
	if followResp.StatusCode != http.StatusOK {
		t.Errorf("follow-up request after SQL-injection probes: want 200, got %d", followResp.StatusCode)
	}
}

// nestedJSON builds a JSON value nested depth levels deep, e.g. for
// depth=2: {"nested":{"nested":"leaf"}}. It stresses a decoder's
// object-skip / type-check path without approaching Go's recursion
// limits (depth stays in the tens, not the thousands).
func nestedJSON(depth int) string {
	v := `"leaf"`
	for i := 0; i < depth; i++ {
		v = fmt.Sprintf(`{"nested":%s}`, v)
	}
	return v
}

// decodeErrorEnvelope unmarshals the shared {"error":{"code",...}}
// envelope (pkg/gateway/sessionserver.errorEnvelope) and returns the
// inner error object as a generic map so callers can assert on
// individual fields without importing the internal type.
func decodeErrorEnvelope(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var envelope struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("response is not the documented error envelope: %v (body %s)", err, raw)
	}
	if envelope.Error == nil {
		t.Fatalf("response has no \"error\" object (body %s)", raw)
	}
	return envelope.Error
}
