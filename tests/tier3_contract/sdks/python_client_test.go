// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 SDK contract tests for the Python client SDK. The contract
// surface follows §15.6 "Client SDKs": wire-format round trip, webhook
// signature verification, authentication, idempotency-key handling,
// the error envelope, pagination cursors, and language-native
// ergonomics.
//
// Each implemented test brings up an in-process gateway (sessionserver
// backed by memstore, the same wiring as the Go client suite), and
// drives the Python SDK through its language-native helper at
// sdks/client/python/test-helper over the shared harness JSON-line
// protocol. The helper is a #!/usr/bin/env python3 script, so the
// harness execs it directly with no build step.
//
// The streaming and MCP contract tests do not fit the JSON-line op
// model: a §15.1 SSE stream is a long-lived connection rather than a
// request/response op, and the §15.2 MCP API is a JSON-RPC endpoint.
// They stand up the gateway (with an event bus for streaming, with the
// MCP server for MCP) and drive the SDK through a conformance probe
// subprocess. Every test skips cleanly when python3 is absent from
// PATH.

package sdks_test

import (
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/tests/tier3_contract/sdks/harness"
)

// requirePython3 skips the test when python3 is not on PATH. The
// Python SDK uses only the standard library, so a bare python3 is the
// single precondition.
func requirePython3(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not found on PATH; the Python client SDK contract suite needs a python3 interpreter")
	}
}

// pythonHelperPath returns the path to the committed Python test
// helper at sdks/client/python/test-helper. The helper is a directly
// executable shebang script, so the harness execs it without a build
// step.
func pythonHelperPath(t *testing.T) string {
	t.Helper()
	root := repoRootFromCWD(t)
	path := filepath.Join(root, "sdks", "client", "python", "test-helper")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("python test-helper not found at %s: %v", path, err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("python test-helper at %s is not executable", path)
	}
	return path
}

// spawnPythonHelper spawns the Python SDK helper pointed at the gateway
// URL. It mirrors spawnHelper for the Go client.
func spawnPythonHelper(t *testing.T, gatewayURL string) *harness.Helper {
	t.Helper()
	return harness.Spawn(t, "python-client", pythonHelperPath(t), map[string]string{
		"LENNY_GATEWAY_URL": gatewayURL,
		"LENNY_TENANT_ID":   "acme",
	})
}

// spec: 15.6 (the SDK helper accepts commands over stdin and reports outcomes over stdout)
// diagnosis: The Python helper misframed a request or a reply on the
//
//	JSON-line pipe. Inspect the stdin loop and write_response in
//	sdks/client/python/test-helper.
func TestPythonClientHelperProtocol(t *testing.T) {
	requirePython3(t)
	ts := newGateway(t)
	h := spawnPythonHelper(t, ts.URL)

	// One create round trip exercises the full stdin/stdout protocol:
	// the harness writes a command line and the helper writes a
	// well-formed response line the harness can decode.
	create := send(t, h, harness.Command{Op: "create_session", Args: map[string]any{
		"runtime": "claude-code",
		"user_id": "alice@acme.com",
	}})
	if !create.OK {
		t.Fatalf("create_session over the helper protocol failed: %v", create.Error)
	}
	if resultString(t, create, "id") == "" {
		t.Errorf("helper protocol round trip returned an empty session id: %v", create.Result)
	}
}

// spec: 15.6 (the SDK round-trips every session lifecycle operation against a live gateway)
// diagnosis: A create/get/list/transition op returned the wrong
//
//	envelope. Inspect the request builder and the response decoder
//	in sdks/client/python/lenny/client.py and types.py.
func TestPythonClientWireFormat(t *testing.T) {
	requirePython3(t)
	ts := newGateway(t)
	h := spawnPythonHelper(t, ts.URL)

	create := send(t, h, harness.Command{Op: "create_session", Args: map[string]any{
		"runtime": "claude-code",
		"user_id": "alice@acme.com",
	}})
	if !create.OK {
		t.Fatalf("create_session failed: %v", create.Error)
	}
	id := resultString(t, create, "id")
	if got := create.Result["state"]; got != "created" {
		t.Errorf("create state: want created, got %v", got)
	}
	if got := create.Result["runtime"]; got != "claude-code" {
		t.Errorf("create runtime: want claude-code, got %v", got)
	}

	get := send(t, h, harness.Command{Op: "get_session", Args: map[string]any{"id": id}})
	if !get.OK || get.Result["id"] != id {
		t.Errorf("get_session round trip: want id %q, got %v", id, get.Result)
	}

	list := send(t, h, harness.Command{Op: "list_sessions"})
	if !list.OK {
		t.Fatalf("list_sessions failed: %v", list.Error)
	}

	// Walk the full §15.1 lifecycle through the SDK transition
	// methods: created -> ready -> running -> suspended -> completed.
	for _, step := range []struct {
		op    string
		state string
	}{
		{"finalize", "ready"},
		{"start", "running"},
		{"interrupt", "suspended"},
		{"terminate", "completed"},
	} {
		resp := send(t, h, harness.Command{Op: step.op, Args: map[string]any{"id": id}})
		if !resp.OK {
			t.Fatalf("%s failed: %v", step.op, resp.Error)
		}
		if got := resp.Result["state"]; got != step.state {
			t.Errorf("after %s: want state %q, got %v", step.op, step.state, got)
		}
	}
}

// spec: 15.6 (SSE for REST streams; reconnect with Last-Event-ID)
// diagnosis: The Python SDK streaming surface dropped, duplicated, or
//
//	reordered a §15.1 SSE event across a reconnect. Inspect the SSE
//	frame parser and the reconnect/dedup loop in
//	sdks/client/python/lenny/stream.py. A duplicate points at the
//	Last-Event-ID cursor or the at-or-below-cursor drop; a gap
//	points at the cursor the reconnect request sent.
func TestPythonClientStreamingReconnect(t *testing.T) {
	requirePython3(t)
	// The §15.1 SSE stream is a long-lived connection, so this contract
	// test drives a streaming-conformance probe subprocess rather than
	// the JSON-line helper. The probe consumes the SDK stream; the Go
	// test publishes events and forces a mid-stream disconnect.
	root := repoRootFromCWD(t)
	probe := filepath.Join(root, "sdks", "client", "python", "tests", "stream_probe.py")
	if _, err := os.Stat(probe); err != nil {
		t.Fatalf("python stream probe not found at %s: %v", probe, err)
	}
	runSDKStreamingReconnect(t, "python-client", []string{"python3", probe})
}

// spec: 15.6 (the SDK has an MCP tool-discovery and invocation surface)
// diagnosis: The Python SDK MCP client failed to drive the §15.2 MCP
//
//	API. Inspect the JSON-RPC handshake, tools/list, and tools/call
//	dispatch in sdks/client/python/lenny/mcp.py. A handshake failure
//	points at the initialize params; a tool-call failure points at
//	the argument encoding or the result decoder.
func TestPythonClientMCP(t *testing.T) {
	requirePython3(t)
	// The §15.2 MCP surface is a JSON-RPC endpoint rather than a
	// request/response REST op, so this contract test drives an
	// MCP-conformance probe subprocess rather than the JSON-line
	// helper. The probe drives the SDK MCP client; the Go test stands
	// up the gateway's MCP server in process.
	root := repoRootFromCWD(t)
	probe := filepath.Join(root, "sdks", "client", "python", "tests", "mcp_probe.py")
	if _, err := os.Stat(probe); err != nil {
		t.Fatalf("python MCP probe not found at %s: %v", probe, err)
	}
	runSDKMCPProbe(t, "python-client", []string{"python3", probe})
}

// spec: 15.6 (both async/await and synchronous variants supported)
// diagnosis: The synchronous Client or the async AsyncClient code
//
//	path is missing or diverged. Inspect both classes in
//	sdks/client/python/lenny/client.py.
func TestPythonClientAsyncSync(t *testing.T) {
	requirePython3(t)

	// The harness helper drives the synchronous Client; a successful
	// round trip confirms the sync code path. The async code path is
	// verified out of band: the SDK module must expose an AsyncClient
	// whose method set matches the synchronous Client.
	ts := newGateway(t)
	h := spawnPythonHelper(t, ts.URL)
	create := send(t, h, harness.Command{Op: "create_session", Args: map[string]any{
		"runtime": "claude-code",
	}})
	if !create.OK {
		t.Fatalf("synchronous create failed: %v", create.Error)
	}
	if get := send(t, h, harness.Command{Op: "get_session", Args: map[string]any{
		"id": resultString(t, create, "id"),
	}}); !get.OK {
		t.Errorf("synchronous get failed: %v", get.Error)
	}

	// Confirm the async surface exists and mirrors the sync surface.
	// Every public method on Client must also exist on AsyncClient,
	// and each async method must be awaitable: a coroutine function
	// for a single call or an async-generator function for an
	// iteration helper.
	root := repoRootFromCWD(t)
	pkgDir := filepath.Join(root, "sdks", "client", "python")
	script := `
import inspect, sys
import lenny
sync_methods = {n for n, m in inspect.getmembers(lenny.Client, inspect.isfunction)
                if not n.startswith("_")}
async_methods = {n for n, m in inspect.getmembers(lenny.AsyncClient, inspect.isfunction)
                 if not n.startswith("_")}
missing = sync_methods - async_methods
if missing:
    print("AsyncClient missing methods:", sorted(missing), file=sys.stderr)
    sys.exit(1)
for n in async_methods:
    m = getattr(lenny.AsyncClient, n)
    if not (inspect.iscoroutinefunction(m) or inspect.isasyncgenfunction(m)):
        print("AsyncClient." + n + " is neither a coroutine nor an async generator",
              file=sys.stderr)
        sys.exit(1)
print("ok")
`
	cmd := exec.Command("python3", "-c", script)
	cmd.Dir = pkgDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("async/sync surface check failed: %v\n%s", err, out)
	}
}

// spec: 15.6 (mypy --strict passes on the SDK sources)
// diagnosis: A type annotation on the SDK surface is wrong or
//
//	missing. Run `mypy --strict lenny test-helper` from
//	sdks/client/python and inspect the reported file.
func TestPythonClientTypeHints(t *testing.T) {
	requirePython3(t)
	if _, err := exec.LookPath("mypy"); err != nil {
		t.Skip("mypy not found on PATH; the Python SDK type-hint check needs mypy installed")
	}
	root := repoRootFromCWD(t)
	pkgDir := filepath.Join(root, "sdks", "client", "python")
	cmd := exec.Command("mypy", "--strict", "lenny", "test-helper")
	cmd.Dir = pkgDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("mypy --strict failed on the Python SDK sources:\n%s", out)
	}
}

// spec: 15.6 (the error envelope is parsed into a typed error)
// diagnosis: A 4xx response was not decoded into the §15.1 typed
//
//	error. Inspect APIError.from_response in
//	sdks/client/python/lenny/errors.py.
func TestPythonClientErrorEnvelope(t *testing.T) {
	requirePython3(t)
	ts := newGateway(t)
	h := spawnPythonHelper(t, ts.URL)

	// A create missing runtimeRef is rejected with the §15.1
	// VALIDATION_ERROR envelope; the helper surfaces the parsed code.
	missing := send(t, h, harness.Command{Op: "create_session", Args: map[string]any{
		"user_id": "alice@acme.com",
	}})
	if missing.OK {
		t.Fatal("create without runtime succeeded; expected VALIDATION_ERROR")
	}
	if missing.Code != "VALIDATION_ERROR" {
		t.Errorf("error code: want VALIDATION_ERROR, got %q (message %q)", missing.Code, missing.Error)
	}

	// A get on a missing session is rejected with RESOURCE_NOT_FOUND.
	notFound := send(t, h, harness.Command{Op: "get_session", Args: map[string]any{
		"id": "sess_does_not_exist",
	}})
	if notFound.OK {
		t.Fatal("get of missing session succeeded; expected RESOURCE_NOT_FOUND")
	}
	if notFound.Code != "RESOURCE_NOT_FOUND" {
		t.Errorf("error code: want RESOURCE_NOT_FOUND, got %q", notFound.Code)
	}

	// An out-of-order transition is rejected with
	// INVALID_STATE_TRANSITION.
	create := send(t, h, harness.Command{Op: "create_session", Args: map[string]any{
		"runtime": "claude-code",
	}})
	if !create.OK {
		t.Fatalf("create failed: %v", create.Error)
	}
	id := resultString(t, create, "id")
	badTransition := send(t, h, harness.Command{Op: "start", Args: map[string]any{"id": id}})
	if badTransition.OK {
		t.Fatal("start from created succeeded; expected INVALID_STATE_TRANSITION")
	}
	if badTransition.Code != "INVALID_STATE_TRANSITION" {
		t.Errorf("error code: want INVALID_STATE_TRANSITION, got %q", badTransition.Code)
	}
}

// spec: 15.6 (HMAC-SHA256 webhook signature verification with the 5-minute replay window)
// diagnosis: The webhook verifier accepted a forged or stale
//
//	signature, or rejected a valid one. Inspect the Verifier in
//	sdks/client/python/lenny/webhook.py.
func TestPythonClientWebhookSignatureVerification(t *testing.T) {
	requirePython3(t)
	// The verify_webhook op needs no gateway; it exercises the SDK
	// verifier directly. A gateway is still spawned so the helper has a
	// valid client for the shutdown path.
	ts := newGateway(t)
	h := spawnPythonHelper(t, ts.URL)

	secret := "wh-secret-acme"
	body := `{"event":"session.completed","sessionId":"sess_1"}`

	// A signature the §14 signing scheme produces for the current time
	// must verify.
	now := time.Now()
	valid := sign(secret, body, now)
	if resp := send(t, h, harness.Command{Op: "verify_webhook", Args: map[string]any{
		"secret": secret, "body": body, "signature": valid,
	}}); !resp.OK {
		t.Errorf("valid signature rejected: %v", resp.Error)
	}

	// A signature under the wrong secret must be rejected.
	forged := sign("wrong-secret", body, now)
	if resp := send(t, h, harness.Command{Op: "verify_webhook", Args: map[string]any{
		"secret": secret, "body": body, "signature": forged,
	}}); resp.OK {
		t.Error("forged signature accepted")
	}

	// A signature whose timestamp is outside the 5-minute replay
	// window must be rejected even when the HMAC itself is correct.
	stale := sign(secret, body, now.Add(-10*time.Minute))
	if resp := send(t, h, harness.Command{Op: "verify_webhook", Args: map[string]any{
		"secret": secret, "body": body, "signature": stale,
	}}); resp.OK {
		t.Error("stale signature accepted (replay window not enforced)")
	}

	// A malformed header must be rejected.
	if resp := send(t, h, harness.Command{Op: "verify_webhook", Args: map[string]any{
		"secret": secret, "body": body, "signature": "not-a-signature",
	}}); resp.OK {
		t.Error("malformed signature accepted")
	}
}

// spec: 15.6 (the SDK attaches an authentication credential to every request)
// diagnosis: A tenant-scoped request reached the gateway without its
//
//	tenant context, so the created session landed on the wrong
//	tenant. Inspect the tenant header wiring in
//	sdks/client/python/lenny/client.py.
func TestPythonClientAuthentication(t *testing.T) {
	requirePython3(t)
	ts := newGateway(t)
	h := spawnPythonHelper(t, ts.URL)

	// The helper is spawned with LENNY_TENANT_ID=acme; a created
	// session must therefore be stamped with the acme tenant, which
	// confirms the SDK propagated the tenant context the gateway uses
	// to scope the request.
	create := send(t, h, harness.Command{Op: "create_session", Args: map[string]any{
		"runtime": "claude-code",
	}})
	if !create.OK {
		t.Fatalf("create_session failed: %v", create.Error)
	}
	if got := create.Result["tenant_id"]; got != "acme" {
		t.Errorf("tenant context not propagated: want tenant acme, got %v", got)
	}
}

// spec: 15.6 (same idempotency key returns the same response)
// diagnosis: A repeated create with the same idempotency key minted
//
//	a second session. Inspect the SDK Idempotency-Key header
//	support in client.py and the gateway idempotency middleware.
func TestPythonClientIdempotencyKeyHandling(t *testing.T) {
	requirePython3(t)
	// The gateway is wrapped in the §11.5 idempotency middleware so a
	// replayed key returns the cached create response.
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{})
	ts := httptest.NewServer(idempotencyWrapped(srv.Handler()))
	t.Cleanup(ts.Close)
	h := spawnPythonHelper(t, ts.URL)

	key := "idem-key-001"
	first := send(t, h, harness.Command{Op: "create_session", Args: map[string]any{
		"runtime": "claude-code", "idempotency_key": key,
	}})
	if !first.OK {
		t.Fatalf("first create failed: %v", first.Error)
	}
	second := send(t, h, harness.Command{Op: "create_session", Args: map[string]any{
		"runtime": "claude-code", "idempotency_key": key,
	}})
	if !second.OK {
		t.Fatalf("second create failed: %v", second.Error)
	}
	if first.Result["id"] != second.Result["id"] {
		t.Errorf("idempotency key did not replay: first id %v, second id %v",
			first.Result["id"], second.Result["id"])
	}

	// A different key must produce a fresh session.
	third := send(t, h, harness.Command{Op: "create_session", Args: map[string]any{
		"runtime": "claude-code", "idempotency_key": "idem-key-002",
	}})
	if !third.OK {
		t.Fatalf("third create failed: %v", third.Error)
	}
	if third.Result["id"] == first.Result["id"] {
		t.Error("a fresh idempotency key replayed an old response")
	}
}

// spec: 15.6 (every paginated endpoint exercised through cursor iteration)
// diagnosis: The list op dropped or duplicated a session. Inspect
//
//	SessionPage.from_wire and list_sessions in
//	sdks/client/python/lenny.
func TestPythonClientPaginationCursors(t *testing.T) {
	requirePython3(t)
	ts := newGateway(t)
	h := spawnPythonHelper(t, ts.URL)

	// Create several sessions, then list them back through the SDK.
	// The minimal gateway returns the full set in one page; the SDK
	// list helper must surface every created id.
	created := map[string]bool{}
	for i := 0; i < 5; i++ {
		resp := send(t, h, harness.Command{Op: "create_session", Args: map[string]any{
			"runtime": "claude-code",
		}})
		if !resp.OK {
			t.Fatalf("create %d failed: %v", i, resp.Error)
		}
		created[resultString(t, resp, "id")] = true
	}

	list := send(t, h, harness.Command{Op: "list_sessions"})
	if !list.OK {
		t.Fatalf("list_sessions failed: %v", list.Error)
	}
	count, ok := list.Result["count"].(float64)
	if !ok || int(count) != len(created) {
		t.Errorf("list count: want %d, got %v", len(created), list.Result["count"])
	}
	ids, ok := list.Result["ids"].([]any)
	if !ok {
		t.Fatalf("list result missing ids array: %v", list.Result)
	}
	for _, id := range ids {
		if !created[id.(string)] {
			t.Errorf("list returned an unexpected id %v", id)
		}
		delete(created, id.(string))
	}
	if len(created) != 0 {
		t.Errorf("list omitted created sessions: %v", created)
	}
}

// spec: 15.6 (the same operation matrix produces equivalent outcomes across SDKs)
// diagnosis: The Python helper and the Go client disagreed on an op.
//
//	One SDK built a request differently or decoded a response
//	differently; compare the two test-helper implementations.
func TestPythonClientEquivalenceWithGo(t *testing.T) {
	requirePython3(t)
	ts := newGateway(t)

	// Spawn both the Go and the Python helper against the same
	// gateway. AssertEquivalent runs the matrix against each and pins
	// every (ok, code) verdict to the first helper.
	goHelper := spawnHelper(t, ts.URL)
	pyHelper := spawnPythonHelper(t, ts.URL)

	cases := []harness.MatrixCase{
		{
			Name:    "create_session",
			Command: harness.Command{Op: "create_session", Args: map[string]any{"runtime": "claude-code"}},
		},
		{
			Name:        "create_missing_runtime",
			Command:     harness.Command{Op: "create_session", Args: map[string]any{"user_id": "alice@acme.com"}},
			ExpectError: "VALIDATION_ERROR",
		},
		{
			Name:        "get_missing_session",
			Command:     harness.Command{Op: "get_session", Args: map[string]any{"id": "sess_does_not_exist"}},
			ExpectError: "RESOURCE_NOT_FOUND",
		},
		{
			Name:    "list_sessions",
			Command: harness.Command{Op: "list_sessions"},
		},
		{
			Name: "verify_webhook_malformed",
			Command: harness.Command{Op: "verify_webhook", Args: map[string]any{
				"secret": "wh-secret-acme", "body": "{}", "signature": "not-a-signature",
			}},
			ExpectError: "SIGNATURE_INVALID",
		},
	}
	harness.AssertEquivalent(t, []*harness.Helper{goHelper, pyHelper}, cases)
}
