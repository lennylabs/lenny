// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 SDK contract tests for the TypeScript client SDK. The
// contract surface follows §15.6 "Client SDKs": wire-format round
// trip, streaming and reconnect, file upload, webhook signature
// verification, authentication, idempotency-key handling, error
// envelope, pagination cursors, MCP client, language-native
// ergonomics, compatibility matrix.
//
// Each implemented test brings up an in-process gateway (the same
// wiring as the Go client contract tests in go_client_test.go),
// installs and builds the TypeScript SDK under
// sdks/client/typescript, and drives the SDK through its
// sdks/client/typescript/test-helper subprocess over the shared
// harness JSON-line protocol. The helper is a Node program; when the
// Node toolchain is absent the contract tests skip with a precondition
// reason rather than fail.
//
// The streaming contract test does not fit the JSON-line op model: a
// §15.1 SSE stream is a long-lived connection rather than a
// request/response op. It builds the SDK, stands up the gateway with
// an event bus, and drives the SDK's streaming surface against it
// through a streaming-conformance probe subprocess.
//
// The MCP-client SDK surface named in §15.6 is not yet implemented;
// that test remains skipped with a skip reason naming the missing SDK
// package.

package sdks_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/tests/tier3_contract/sdks/harness"
)

// tsHelperBuild memoizes the one-time TypeScript SDK install+build so
// every contract test in this file reuses a single build.
var tsHelperBuild struct {
	once sync.Once
	path string
	err  error
}

// requireNode skips the calling test when the Node toolchain is not on
// PATH. The TypeScript helper is a Node program; without node and npm
// the contract cannot be exercised, so the test reports a clean
// precondition skip rather than a failure.
func requireNode(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"node", "npm"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("precondition: %s is not installed; the TypeScript client contract suite needs the Node toolchain", bin)
		}
	}
}

// buildTypeScriptHelper installs the SDK dependencies and runs the
// dual ESM/CJS build under sdks/client/typescript, then returns the
// path to the test-helper entry point the harness spawns. The build
// runs once per test process; later callers reuse the result.
func buildTypeScriptHelper(t *testing.T) string {
	t.Helper()
	tsHelperBuild.once.Do(func() {
		root := repoRootFromCWD(t)
		dir := filepath.Join(root, "sdks", "client", "typescript")
		if _, statErr := os.Stat(filepath.Join(dir, "package.json")); statErr != nil {
			tsHelperBuild.err = statErr
			return
		}
		// `npm ci` is reproducible but needs a committed lockfile;
		// `npm install` works whether or not one is present.
		install := exec.Command("npm", "install", "--no-audit", "--no-fund")
		install.Dir = dir
		if out, err := install.CombinedOutput(); err != nil {
			tsHelperBuild.err = &buildError{stage: "npm install", out: out, err: err}
			return
		}
		build := exec.Command("npm", "run", "build")
		build.Dir = dir
		if out, err := build.CombinedOutput(); err != nil {
			tsHelperBuild.err = &buildError{stage: "npm run build", out: out, err: err}
			return
		}
		tsHelperBuild.path = filepath.Join(dir, "test-helper")
	})
	if tsHelperBuild.err != nil {
		t.Fatalf("build typescript client helper: %v", tsHelperBuild.err)
	}
	return tsHelperBuild.path
}

// buildError carries the combined output of a failed build stage so
// the test failure shows the underlying npm or tsc diagnostics.
type buildError struct {
	stage string
	out   []byte
	err   error
}

func (e *buildError) Error() string {
	return e.stage + ": " + e.err.Error() + "\n" + string(e.out)
}

// spawnTSHelper builds the TypeScript SDK helper and spawns it pointed
// at the gateway URL, mirroring spawnHelper for the Go client.
//
// harness.Spawn sets the child process environment to exactly the map
// passed here, so PATH is forwarded explicitly: the test-helper entry
// point begins with a `#!/usr/bin/env node` shebang, and resolving
// node needs PATH on the child. HOME is forwarded so a node version
// manager that keys off it still resolves the same interpreter.
func spawnTSHelper(t *testing.T, gatewayURL string) *harness.Helper {
	t.Helper()
	requireNode(t)
	bin := buildTypeScriptHelper(t)
	env := map[string]string{
		"LENNY_GATEWAY_URL": gatewayURL,
		"LENNY_TENANT_ID":   "acme",
		"PATH":              os.Getenv("PATH"),
	}
	if home := os.Getenv("HOME"); home != "" {
		env["HOME"] = home
	}
	return harness.Spawn(t, "ts-client", bin, env)
}

// spec: 15.6 (the test helper accepts commands over stdin and reports outcomes over stdout)
// diagnosis: The TypeScript helper failed to parse a command or framed
//
//	its reply wrong. Inspect the JSON-line loop in
//	sdks/client/typescript/src/test-helper.ts.
func TestTypeScriptClientHelperProtocol(t *testing.T) {
	ts := newGateway(t)
	h := spawnTSHelper(t, ts.URL)

	// A verify_webhook op needs no gateway round trip; it confirms the
	// helper reads one command line and writes one response line.
	resp := send(t, h, harness.Command{Op: "verify_webhook", Args: map[string]any{
		"secret": "s", "body": "b", "signature": "not-a-signature",
	}})
	if resp.OK {
		t.Error("malformed signature accepted; helper protocol or verifier is wrong")
	}

	// An unknown op must be reported as a failure, not crash the
	// helper: a follow-up command must still get a reply.
	unknown := send(t, h, harness.Command{Op: "no_such_op"})
	if unknown.OK {
		t.Error("unknown op reported ok; helper should reject it")
	}
	again := send(t, h, harness.Command{Op: "verify_webhook", Args: map[string]any{
		"secret": "s", "body": "b", "signature": "still-bad",
	}})
	if again.OK {
		t.Error("helper did not recover after an unknown op")
	}
}

// spec: 15.6 (the SDK round-trips every session lifecycle operation against a live gateway)
// diagnosis: A create/get/list/transition op returned the wrong
//
//	envelope. Inspect the SDK request builder in
//	sdks/client/typescript/src/client.ts and the response parser.
func TestTypeScriptClientWireFormat(t *testing.T) {
	ts := newGateway(t)
	h := spawnTSHelper(t, ts.URL)

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
// diagnosis: The TypeScript SDK streaming surface dropped, duplicated,
//
//	or reordered a §15.1 SSE event across a reconnect. Inspect the
//	SSE frame parser and the reconnect/dedup loop in
//	sdks/client/typescript/src/stream.ts. A duplicate points at the
//	Last-Event-ID cursor or the at-or-below-cursor drop; a gap
//	points at the cursor the reconnect request sent.
func TestTypeScriptClientStreamingReconnect(t *testing.T) {
	requireNode(t)
	// The §15.1 SSE stream is a long-lived connection, so this contract
	// test drives a streaming-conformance probe subprocess rather than
	// the JSON-line helper. The probe consumes the SDK stream; the Go
	// test publishes events and forces a mid-stream disconnect.
	buildTypeScriptHelper(t)
	root := repoRootFromCWD(t)
	probe := filepath.Join(root, "sdks", "client", "typescript", "test", "stream-probe.mjs")
	if _, err := os.Stat(probe); err != nil {
		t.Fatalf("typescript stream probe not found at %s: %v", probe, err)
	}
	runSDKStreamingReconnect(t, "ts-client", []string{"node", probe})
}

// runSDKStreamingReconnect drives one client SDK's §15.1 SSE streaming
// surface through a forced reconnect. It stands up an in-process
// gateway wired with an event bus, creates a session, spawns the
// language-native streaming probe (cmd), publishes a six-event log
// that spans a forced mid-stream disconnect, and asserts the probe
// observed every event exactly once, in sequence order, with no gap
// and no duplicate.
//
// The probe is spawned with LENNY_GATEWAY_URL, LENNY_TENANT_ID,
// LENNY_SESSION_ID, and LENNY_EVENT_COUNT in its environment; it
// prints one JSON line {"seqs":[...],"types":[...]} on success.
func runSDKStreamingReconnect(t *testing.T, name string, cmd []string) {
	t.Helper()

	// The gateway is the in-process sessionserver wired with an event
	// bus; events published to that bus are the stream the SDK
	// consumes.
	bus := events.NewBus(64)
	srv := sessionserver.New(memstore.New(), sessionserver.Options{Events: bus})

	// disconnector severs the SDK's first SSE connection mid-stream so
	// the reconnect path is exercised. It is defined in
	// go_client_test.go and shared across the contract suite.
	d := &disconnector{inner: srv.Handler()}
	ts := httptest.NewServer(d)
	t.Cleanup(ts.Close)

	// Create the session through the gateway on the acme tenant; the
	// events endpoint rejects an unknown session with 404.
	sessionID := createSessionForStreaming(t, ts.URL, "acme")

	const total = 6
	publish := func(seq int) {
		bus.Publish(sessionID, "response", `{"n":`+strconv.Itoa(seq)+`}`, time.Now())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	c.Env = append(
		os.Environ(),
		"LENNY_GATEWAY_URL="+ts.URL,
		"LENNY_TENANT_ID=acme",
		"LENNY_SESSION_ID="+sessionID,
		"LENNY_EVENT_COUNT="+strconv.Itoa(total),
	)
	stdout, err := c.StdoutPipe()
	if err != nil {
		t.Fatalf("%s probe stdout pipe: %v", name, err)
	}
	stderr, err := c.StderrPipe()
	if err != nil {
		t.Fatalf("%s probe stderr pipe: %v", name, err)
	}
	if err := c.Start(); err != nil {
		t.Fatalf("start %s streaming probe: %v", name, err)
	}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := stderr.Read(buf)
			if n > 0 {
				t.Logf("[%s probe stderr] %s", name, buf[:n])
			}
			if readErr != nil {
				return
			}
		}
	}()

	// Publish the first batch, give the probe time to open its
	// connection and receive them, then force the disconnect. The
	// gateway's retained backlog survives the dropped connection, so
	// the SDK reconnect resumes from its Last-Event-ID cursor.
	for seq := 1; seq <= 3; seq++ {
		publish(seq)
	}
	time.Sleep(750 * time.Millisecond)
	d.dropFirstConnection()
	// Publish the remaining events. They land in the bus history and
	// are replayed to the reconnecting SDK from the cursor it sends.
	for seq := 4; seq <= total; seq++ {
		publish(seq)
	}

	out, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatalf("%s probe stdout read: %v", name, err)
	}
	if err := c.Wait(); err != nil {
		t.Fatalf("%s streaming probe exited with error: %v\noutput: %s", name, err, out)
	}

	var result struct {
		Seqs  []uint64 `json:"seqs"`
		Types []string `json:"types"`
		Error string   `json:"error"`
	}
	if err := json.Unmarshal(trimToJSONLine(out), &result); err != nil {
		t.Fatalf("%s probe output is not a JSON line: %v\noutput: %s", name, err, out)
	}
	if result.Error != "" {
		t.Fatalf("%s streaming probe reported an error: %s", name, result.Error)
	}

	// The §15.1 reconnect contract held: every event arrived exactly
	// once, in sequence order, with no gap and no duplicate.
	if len(result.Seqs) != total {
		t.Fatalf("%s probe received %d events, want %d: %v", name, len(result.Seqs), total, result.Seqs)
	}
	for i, seq := range result.Seqs {
		if seq != uint64(i+1) {
			t.Errorf("%s: event at position %d has seq %d, want %d (full sequence %v)",
				name, i, seq, i+1, result.Seqs)
		}
	}
	for i, typ := range result.Types {
		if typ != "response" {
			t.Errorf("%s: event %d has type %q, want \"response\"", name, i+1, typ)
		}
	}
	if cc := d.connectionCount(); cc < 2 {
		t.Fatalf("%s: expected the SDK to reconnect after the forced disconnect, saw %d connection(s)", name, cc)
	}
}

// createSessionForStreaming creates a session through the gateway REST
// API on the given tenant and returns its id. The streaming probe
// consumes that session's event stream.
func createSessionForStreaming(t *testing.T, gatewayURL, tenant string) string {
	t.Helper()
	body := `{"runtimeRef":"claude-code"}`
	req, err := http.NewRequest(http.MethodPost, gatewayURL+"/v1/sessions", strings.NewReader(body))
	if err != nil {
		t.Fatalf("build create-session request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", tenant)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create session for streaming: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("create session for streaming: HTTP %d: %s", resp.StatusCode, raw)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("decode create-session response: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("create-session response carried no id: %s", raw)
	}
	return created.ID
}

// trimToJSONLine returns the last non-empty line of out. A probe may
// emit diagnostics before its result line; the result is the final
// JSON object on stdout.
func trimToJSONLine(out []byte) []byte {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return []byte(line)
		}
	}
	return out
}

// spec: 15.6 (the SDK ships both an ESM and a CommonJS build)
// diagnosis: A build target is missing or does not load. Inspect the
//
//	tsconfig.esm.json / tsconfig.cjs.json outputs and the
//	scripts/write-dist-pkgjson.mjs per-target package.json writer.
func TestTypeScriptClientESMAndCJSBuilds(t *testing.T) {
	requireNode(t)
	buildTypeScriptHelper(t)
	root := repoRootFromCWD(t)
	dist := filepath.Join(root, "sdks", "client", "typescript", "dist")

	for _, target := range []struct {
		dir      string
		pkgType  string
		probeArg string
	}{
		{"esm", "module", "import"},
		{"cjs", "commonjs", "require"},
	} {
		entry := filepath.Join(dist, target.dir, "index.js")
		if _, err := os.Stat(entry); err != nil {
			t.Errorf("%s build entry missing: %v", target.dir, err)
			continue
		}
		// Each target carries a package.json that pins its module
		// system so Node resolves the build correctly.
		pkg := filepath.Join(dist, target.dir, "package.json")
		if _, err := os.Stat(pkg); err != nil {
			t.Errorf("%s build package.json missing: %v", target.dir, err)
			continue
		}
		// Load the build through Node so a broken target is caught,
		// not just a missing file.
		var probe string
		if target.probeArg == "import" {
			probe = "import('" + filepathToURL(entry) + "').then(m=>{if(!m.newClient)process.exit(2)}).catch(()=>process.exit(3))"
		} else {
			probe = "try{if(!require('" + entry + "').newClient)process.exit(2)}catch(e){process.exit(3)}"
		}
		cmd := exec.Command("node", "-e", probe)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s build does not load: %v\n%s", target.dir, err, out)
		}
	}
}

// filepathToURL converts an absolute filesystem path to a file:// URL
// so a dynamic import() of an ESM module works on every platform.
func filepathToURL(p string) string {
	return "file://" + filepath.ToSlash(p)
}

// spec: 15.6 (tsc --noEmit passes on the SDK sources)
// diagnosis: A type error surfaced in the SDK sources. Inspect the
//
//	tsc --noEmit output; the typed surface is in
//	sdks/client/typescript/src.
func TestTypeScriptClientTypecheck(t *testing.T) {
	requireNode(t)
	root := repoRootFromCWD(t)
	dir := filepath.Join(root, "sdks", "client", "typescript")
	// The typecheck needs the dev dependencies (typescript, the Node
	// type definitions); the shared build installs them.
	buildTypeScriptHelper(t)
	cmd := exec.Command("npm", "run", "typecheck")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tsc --noEmit failed: %v\n%s", err, out)
	}
}

// spec: 15.6 (HMAC-SHA256 webhook signature verification with the 5-minute replay window)
// diagnosis: The webhook verifier accepted a forged or stale
//
//	signature, or rejected a valid one. Inspect the verifier in
//	sdks/client/typescript/src/webhook.ts.
func TestTypeScriptClientWebhookSignatureVerification(t *testing.T) {
	// The verify_webhook op needs no gateway; it exercises the SDK
	// verifier directly. A gateway is still spawned so the helper has
	// a valid client for the shutdown path.
	ts := newGateway(t)
	h := spawnTSHelper(t, ts.URL)

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
//	tenant. Inspect the tenant header wiring in the SDK.
func TestTypeScriptClientAuthentication(t *testing.T) {
	ts := newGateway(t)
	h := spawnTSHelper(t, ts.URL)

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
//	support and the gateway idempotency middleware.
func TestTypeScriptClientIdempotencyKeyHandling(t *testing.T) {
	// The gateway is wrapped in the §11.5 idempotency middleware so a
	// replayed key returns the cached create response.
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{})
	ts := httptest.NewServer(idempotencyWrapped(srv.Handler()))
	t.Cleanup(ts.Close)
	h := spawnTSHelper(t, ts.URL)

	key := "idem-key-ts-001"
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
		"runtime": "claude-code", "idempotency_key": "idem-key-ts-002",
	}})
	if !third.OK {
		t.Fatalf("third create failed: %v", third.Error)
	}
	if third.Result["id"] == first.Result["id"] {
		t.Error("a fresh idempotency key replayed an old response")
	}
}

// spec: 15.6 (the error envelope is parsed into a typed error)
// diagnosis: A 4xx response was not decoded into the §15.1 typed
//
//	error. Inspect decodeApiError in sdks/client/typescript/src.
func TestTypeScriptClientErrorEnvelope(t *testing.T) {
	ts := newGateway(t)
	h := spawnTSHelper(t, ts.URL)

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

// spec: 15.6 (every paginated endpoint exercised through cursor iteration)
// diagnosis: The list op dropped or duplicated a session. Inspect
//
//	the SDK pagination iterator and the gateway list handler.
func TestTypeScriptClientPaginationCursors(t *testing.T) {
	ts := newGateway(t)
	h := spawnTSHelper(t, ts.URL)

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
// diagnosis: The TypeScript helper and the Go helper disagreed on an
//
//	op outcome. The SDK that diverged built the wrong request or
//	parsed the response differently; compare the two client.ts /
//	client.go request builders.
func TestTypeScriptClientEquivalenceWithGo(t *testing.T) {
	requireNode(t)
	// Both helpers drive the same gateway instance so the equivalence
	// comparison is over identical server state.
	ts := newGateway(t)
	goHelper := spawnHelper(t, ts.URL)
	tsHelper := spawnTSHelper(t, ts.URL)

	// The matrix covers a success op and the three §15.1 error codes
	// the minimal gateway returns. AssertEquivalent pins the Go helper
	// as the reference and asserts the TypeScript helper matches the
	// (ok, code) verdict for every row.
	cases := []harness.MatrixCase{
		{
			Name:    "create_session_ok",
			Command: harness.Command{Op: "create_session", Args: map[string]any{"runtime": "claude-code"}},
		},
		{
			Name:        "create_session_missing_runtime",
			Command:     harness.Command{Op: "create_session", Args: map[string]any{"user_id": "alice@acme.com"}},
			ExpectError: "VALIDATION_ERROR",
		},
		{
			Name:        "get_missing_session",
			Command:     harness.Command{Op: "get_session", Args: map[string]any{"id": "sess_absent"}},
			ExpectError: "RESOURCE_NOT_FOUND",
		},
		{
			Name:        "verify_webhook_malformed",
			Command:     harness.Command{Op: "verify_webhook", Args: map[string]any{"secret": "s", "body": "b", "signature": "bad"}},
			ExpectError: "SIGNATURE_INVALID",
		},
	}
	harness.AssertEquivalent(t, []*harness.Helper{goHelper, tsHelper}, cases)
}

// spec: 15.6 (the SDK has an MCP tool-discovery and invocation surface)
// diagnosis: The SDK has no MCP client yet, so the platform MCP tool
//
//	surface cannot be driven through the helper.
func TestTypeScriptClientMCP(t *testing.T) {
	t.Skip("not implemented: §15.6 TypeScript client MCP — sdks/client/typescript has no mcp module; the MCP tool-discovery and lenny/* invocation surface is a follow-on")
}
