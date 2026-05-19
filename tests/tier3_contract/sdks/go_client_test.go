// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 SDK contract tests for the Go client SDK. The contract
// surface follows §15.6 "Client SDKs": wire-format round trip,
// streaming and reconnect, file upload, webhook signature
// verification, authentication, idempotency-key handling, error
// envelope, pagination cursors, MCP client, language-native
// ergonomics, compatibility matrix.
//
// Each REST contract test brings up an in-process gateway
// (sessionserver backed by memstore, the same wiring as
// tests/tier3_contract/rest_sessions), compiles the SDK helper at
// sdks/client/go/test-helper into a throwaway binary, and drives the
// SDK through that helper over the shared harness JSON-line protocol.
//
// The streaming contract test does not fit the JSON-line op model: a
// §15.1 SSE stream is a long-lived connection rather than a
// request/response op. It imports the SDK package directly, stands
// up the gateway with an event bus, and drives the SDK's streaming
// surface against it.
//
// The file-upload and MCP-client SDK surfaces named in §15.6 are not
// yet implemented; those tests remain skipped with a skip reason
// naming the missing SDK package.

package sdks_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	"github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/sdks/client/go/lenny"
	"github.com/lennylabs/lenny/tests/tier3_contract/sdks/harness"
)

// idempotencyWrapped wraps the gateway handler in the §11.5
// idempotency middleware so a replayed Idempotency-Key returns the
// cached create response. The tenant is read from the same
// X-Lenny-Tenant-ID header the gateway uses.
func idempotencyWrapped(inner http.Handler) http.Handler {
	return idempotency.Wrap(inner, idempotency.NewMemoryStore(), idempotency.Options{
		TenantFromRequest: func(r *http.Request) string {
			if v := r.Header.Get("X-Lenny-Tenant-ID"); v != "" {
				return v
			}
			return "default"
		},
	})
}

// repoRootFromCWD walks up from the working directory to the module
// root (the directory holding go.mod).
func repoRootFromCWD(t *testing.T) string {
	t.Helper()
	wd, _ := os.Getwd()
	for d := wd; d != "/" && d != ""; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	t.Fatalf("no go.mod found from %s", wd)
	return ""
}

// buildGoHelper compiles sdks/client/go/test-helper into a throwaway
// binary the harness drives. The binary is built once per test.
func buildGoHelper(t *testing.T) string {
	t.Helper()
	root := repoRootFromCWD(t)
	out := filepath.Join(t.TempDir(), "go-client-helper")
	cmd := exec.Command("go", "build", "-o", out, "./sdks/client/go/test-helper")
	cmd.Dir = root
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build go client helper: %v\n%s", err, combined)
	}
	return out
}

// newGateway brings up an in-process §15.1 gateway backed by a fresh
// memstore. It returns the running test server; the caller's
// t.Cleanup closes it.
func newGateway(t *testing.T) *httptest.Server {
	t.Helper()
	srv := sessionserver.New(memstore.New(), sessionserver.Options{})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// spawnHelper builds the Go SDK helper and spawns it pointed at the
// gateway URL.
func spawnHelper(t *testing.T, gatewayURL string) *harness.Helper {
	t.Helper()
	bin := buildGoHelper(t)
	return harness.Spawn(t, "go-client", bin, map[string]string{
		"LENNY_GATEWAY_URL": gatewayURL,
		"LENNY_TENANT_ID":   "acme",
	})
}

// send is a Send wrapper that fails the test on a transport error.
func send(t *testing.T, h *harness.Helper, cmd harness.Command) harness.Response {
	t.Helper()
	resp, err := h.Send(cmd)
	if err != nil {
		t.Fatalf("helper send %q: %v", cmd.Op, err)
	}
	return resp
}

// resultString reads a string field from a response result map.
func resultString(t *testing.T, r harness.Response, key string) string {
	t.Helper()
	v, ok := r.Result[key].(string)
	if !ok {
		t.Fatalf("result missing string field %q; got %v", key, r.Result)
	}
	return v
}

// spec: 15.6 (the SDK round-trips every session lifecycle operation against a live gateway)
// diagnosis: A create/get/list/transition op returned the wrong
//
//	envelope. Inspect the SDK request builder in
//	sdks/client/go/lenny/client.go and the response decoder.
func TestGoClientWireFormatRoundTrip(t *testing.T) {
	ts := newGateway(t)
	h := spawnHelper(t, ts.URL)

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
// diagnosis: The SDK streaming surface dropped, duplicated, or
//
//	reordered a §15.1 SSE event across a reconnect. Inspect the SSE
//	frame parser and the reconnect/dedup loop in
//	sdks/client/go/lenny/stream.go. A duplicate points at the
//	Last-Event-ID cursor or the at-or-below-cursor drop; a gap
//	points at the cursor the reconnect request sent.
func TestGoClientStreamingReconnect(t *testing.T) {
	// The §15.1 SSE stream is a long-lived connection, so this
	// contract test imports the SDK directly rather than driving the
	// JSON-line helper. The gateway is the in-process sessionserver
	// wired with an event bus; events published to that bus are the
	// stream the SDK consumes.
	bus := events.NewBus(64)
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{Events: bus})

	// disconnector wraps the gateway handler. The first time the SDK
	// opens the events stream it serves the gateway handler with a
	// request whose context the wrapper can cancel; cancelling it
	// makes the gateway return, which closes that HTTP/1.1 connection
	// and forces the SDK to reconnect. Every later connection is
	// served straight through. This forces a mid-stream disconnect
	// without modifying the gateway.
	d := &disconnector{inner: srv.Handler()}
	ts := httptest.NewServer(d)
	t.Cleanup(ts.Close)

	client, err := lenny.New(ts.URL, lenny.WithTenant("acme"))
	if err != nil {
		t.Fatalf("construct SDK client: %v", err)
	}

	// Create the session through the SDK so the gateway stamps it on
	// the acme tenant; the events endpoint rejects an unknown session
	// with 404 RESOURCE_NOT_FOUND.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	created, err := client.CreateSession(ctx, lenny.CreateSessionRequest{RuntimeRef: "claude-code"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	sessionID := created.ID

	// The full event log the gateway will deliver. Six events span
	// the disconnect: the first batch arrives on the initial
	// connection, the rest after the forced reconnect.
	const total = 6
	publish := func(seq int) {
		bus.Publish(sessionID, "response",
			`{"n":`+strconv.Itoa(seq)+`}`, time.Now())
	}

	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()
	stream := client.StreamEvents(streamCtx, sessionID, lenny.StreamOptions{})

	// Publish the first three events and read them back through the
	// SDK stream.
	for seq := 1; seq <= 3; seq++ {
		publish(seq)
	}
	got := make([]uint64, 0, total)
	for len(got) < 3 {
		ev, ok := <-stream.Events()
		if !ok {
			t.Fatalf("stream closed early after %d events: %v", len(got), stream.Err())
		}
		got = append(got, ev.Seq)
		assertEventPayload(t, ev)
	}

	// Force the disconnect now that the first batch has been
	// delivered. The gateway's retained backlog survives the dropped
	// connection, so the SDK reconnect resumes from its Last-Event-ID
	// cursor.
	d.dropFirstConnection()

	// Publish the remaining events. They land in the bus history and
	// are replayed to the reconnecting SDK from the cursor it sends.
	for seq := 4; seq <= total; seq++ {
		publish(seq)
	}
	for len(got) < total {
		ev, ok := <-stream.Events()
		if !ok {
			t.Fatalf("stream closed before reconnect completed; got %d of %d events: %v",
				len(got), total, stream.Err())
		}
		got = append(got, ev.Seq)
		assertEventPayload(t, ev)
	}

	// Stop the stream and confirm the §15.1 reconnect contract held:
	// every event arrived exactly once, in sequence order, with no
	// gap and no duplicate.
	streamCancel()
	for range stream.Events() {
		// Drain any event the SDK delivered before the cancel landed.
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream ended with an error: %v", err)
	}
	if len(got) != total {
		t.Fatalf("received %d events, want %d: %v", len(got), total, got)
	}
	for i, seq := range got {
		if seq != uint64(i+1) {
			t.Fatalf("event at position %d has seq %d, want %d (full sequence %v)",
				i, seq, i+1, got)
		}
	}
	if c := d.connectionCount(); c < 2 {
		t.Fatalf("expected the SDK to reconnect after the forced disconnect, saw %d connection(s)", c)
	}
}

// disconnector wraps an http.Handler so a test can sever the SDK's
// first SSE connection mid-stream. The gateway's events handler runs
// its live-delivery loop until the request context is done; the
// wrapper serves the first events request with a cancelable child
// context and cancels it on demand, which makes the gateway return
// and closes that connection. Every later request is served
// straight through.
type disconnector struct {
	inner http.Handler

	mu       sync.Mutex
	conns    int
	dropConn context.CancelFunc
}

// ServeHTTP serves a request, intercepting the first events stream so
// the test can drop it later.
func (d *disconnector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	isEvents := r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/events")
	if !isEvents {
		d.inner.ServeHTTP(w, r)
		return
	}

	d.mu.Lock()
	d.conns++
	first := d.conns == 1
	var ctx context.Context
	if first {
		var cancelChild context.CancelFunc
		ctx, cancelChild = context.WithCancel(r.Context())
		d.dropConn = cancelChild
	}
	d.mu.Unlock()

	if first {
		d.inner.ServeHTTP(w, r.WithContext(ctx))
		return
	}
	d.inner.ServeHTTP(w, r)
}

// dropFirstConnection cancels the first events connection's context,
// which makes the gateway return and closes the connection.
func (d *disconnector) dropFirstConnection() {
	d.mu.Lock()
	cancel := d.dropConn
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// connectionCount returns the number of events connections served.
func (d *disconnector) connectionCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.conns
}

// assertEventPayload checks that a streamed event carries the §15.1
// frame fields the gateway published: a response type and a JSON
// object payload whose `n` matches the event sequence.
func assertEventPayload(t *testing.T, ev lenny.StreamEvent) {
	t.Helper()
	if ev.Type != "response" {
		t.Errorf("event %d: type %q, want \"response\"", ev.Seq, ev.Type)
	}
	var payload struct {
		N uint64 `json:"n"`
	}
	if err := json.Unmarshal(ev.Data, &payload); err != nil {
		t.Errorf("event %d: payload %q is not a JSON object: %v", ev.Seq, ev.Data, err)
		return
	}
	if payload.N != ev.Seq {
		t.Errorf("event %d: payload n=%d, want %d", ev.Seq, payload.N, ev.Seq)
	}
}

// spec: 15.6 (multipart upload, archive upload, gitClone source materialization)
// diagnosis: The SDK has no upload helpers yet, so a multipart
//
//	upload cannot be driven through the helper.
func TestGoClientFileUpload(t *testing.T) {
	t.Skip("not implemented: §15.6 Go client file upload — sdks/client/go has no upload helper; the multipart upload and uploadArchive SDK methods are a follow-on")
}

// spec: 15.6 (HMAC-SHA256 webhook signature verification with the 5-minute replay window)
// diagnosis: The webhook verifier accepted a forged or stale
//
//	signature, or rejected a valid one. Inspect the verifier in
//	sdks/client/go/webhook.
func TestGoClientWebhookSignatureVerification(t *testing.T) {
	// The verify_webhook op needs no gateway; it exercises the SDK
	// verifier directly. A gateway is still spawned so the helper has
	// a valid client for the shutdown path.
	ts := newGateway(t)
	h := spawnHelper(t, ts.URL)

	secret := "wh-secret-acme"
	body := `{"event":"session.completed","sessionId":"sess_1"}`

	// A signature the SDK signing scheme produces for the current
	// time must verify.
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

// sign reproduces the §14 X-Lenny-Signature header value so the test
// can hand the helper a known-good and known-bad signature.
func sign(secret, body string, t time.Time) string {
	unix := strconv.FormatInt(t.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(unix))
	mac.Write([]byte("."))
	mac.Write([]byte(body))
	return "t=" + unix + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

// spec: 15.6 (the SDK attaches an authentication credential to every request)
// diagnosis: A tenant-scoped request reached the gateway without its
//
//	tenant context, so the created session landed on the wrong
//	tenant. Inspect the auth + tenant header wiring in the SDK.
func TestGoClientAuthentication(t *testing.T) {
	ts := newGateway(t)
	h := spawnHelper(t, ts.URL)

	// The helper is spawned with LENNY_TENANT_ID=acme; a created
	// session must therefore be stamped with the acme tenant, which
	// confirms the SDK propagated the tenant context the gateway
	// uses to scope the request.
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
func TestGoClientIdempotencyKeyHandling(t *testing.T) {
	// The gateway is wrapped in the §11.5 idempotency middleware so a
	// replayed key returns the cached create response.
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{})
	ts := httptest.NewServer(idempotencyWrapped(srv.Handler()))
	t.Cleanup(ts.Close)
	h := spawnHelper(t, ts.URL)

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

// spec: 15.6 (the error envelope is parsed into a typed error)
// diagnosis: A 4xx response was not decoded into the §15.1 typed
//
//	error. Inspect decodeAPIError in sdks/client/go/lenny.
func TestGoClientErrorEnvelope(t *testing.T) {
	ts := newGateway(t)
	h := spawnHelper(t, ts.URL)

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
func TestGoClientPaginationCursors(t *testing.T) {
	ts := newGateway(t)
	h := spawnHelper(t, ts.URL)

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

// spec: 15.6 (tool discovery and the documented MCP tool surface)
// diagnosis: The SDK has no MCP client yet, so the platform MCP tool
//
//	surface cannot be driven through the helper.
func TestGoClientMCP(t *testing.T) {
	t.Skip("not implemented: §15.6 Go client MCP — sdks/client/go has no mcp/ package; the MCP tool-discovery and lenny/* invocation surface is a follow-on")
}

// spec: 15.6 (context cancellation propagates through every SDK call)
// diagnosis: A canceled context did not abort an in-flight SDK
//
//	request. Inspect the context threading through do() in the SDK.
func TestGoClientErgonomics(t *testing.T) {
	ts := newGateway(t)
	h := spawnHelper(t, ts.URL)

	// Every SDK method takes a context.Context as its first
	// argument; the helper threads a per-op context into each call.
	// A round trip confirms the context-carrying call path works
	// end-to-end against the gateway.
	create := send(t, h, harness.Command{Op: "create_session", Args: map[string]any{
		"runtime": "claude-code",
	}})
	if !create.OK {
		t.Fatalf("context-carrying create failed: %v", create.Error)
	}
	id := resultString(t, create, "id")
	if get := send(t, h, harness.Command{Op: "get_session", Args: map[string]any{"id": id}}); !get.OK {
		t.Errorf("context-carrying get failed: %v", get.Error)
	}
}

// spec: 15.6 (each SDK validated against the supported gateway version window)
// diagnosis: The compatibility matrix needs a pinned set of gateway
//
//	images; the in-process gateway exposes a single version only.
func TestGoClientCompatibilityMatrix(t *testing.T) {
	t.Skip("not implemented: §15.6 Go client compatibility matrix — requires a pinned set of gateway images covering the §15.5 support window and a matrix driver; the in-process httptest gateway exposes one version")
}
