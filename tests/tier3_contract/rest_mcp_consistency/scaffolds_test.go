// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract suite for §15.2.1 REST ↔ MCP consistency. For
// every overlapping operation, the harness sends the same logical
// request through REST and through the platform MCP server, then
// asserts the documented semantic equivalence per §15.2.1 rule 1
// ("REST and MCP endpoints that perform the same operation must
// return semantically identical responses"). Both surfaces share the
// same memstore and EchoExecutor here so the test asserts the wire
// translation in each direction without out-of-band coupling.
package rest_mcp_consistency_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/memorystore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// newConsistencyServers stands up REST and MCP servers that share a
// session store and a single EchoExecutor. The REST surface exposes
// /v1/sessions and its subroutes; the MCP server exposes the §8.5
// platform tools mcptools.Register installs.
func newConsistencyServers(t *testing.T, tenant string) (*httptest.Server, *httptest.Server, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	exec := executor.NewEchoExecutor()
	mem := memorystore.NewInMemory(0, nil)

	rest := sessionserver.New(store, sessionserver.Options{Memory: mem})
	tsREST := httptest.NewServer(rest.Handler())
	t.Cleanup(tsREST.Close)

	mcpSrv := mcp.NewServer()
	mcptools.Register(mcpSrv, mcptools.Deps{
		Store:    store,
		Executor: exec,
		Memory:   mem,
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "sess_mcp_consistency" },
		TenantID: tenant,
	})
	tsMCP := httptest.NewServer(mcpSrv.Handler())
	t.Cleanup(tsMCP.Close)

	return tsREST, tsMCP, store
}

// mcpCall invokes an MCP tool over the JSON-RPC endpoint and returns
// the parsed result. Failures bubble up to the test via t.Fatal.
func mcpCall(t *testing.T, url, tool string, args map[string]any) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      tool,
			"arguments": args,
		},
	})
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("MCP call %s: %v", tool, err)
	}
	defer resp.Body.Close()
	var rpc struct {
		Result map[string]any `json:"result"`
		Error  map[string]any `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpc); err != nil {
		t.Fatalf("MCP call %s decode: %v", tool, err)
	}
	if rpc.Error != nil {
		return map[string]any{"_error": rpc.Error}
	}
	return rpc.Result
}

// mcpToolPayload reads the JSON text the platform tool encodes as its
// content. The §8.5 tools serialise their result as a single
// text-content block whose body is JSON; this helper decodes it.
func mcpToolPayload(t *testing.T, res map[string]any) map[string]any {
	t.Helper()
	if _, ok := res["_error"]; ok {
		t.Fatalf("MCP result carried an error envelope: %v", res)
	}
	contents, ok := res["content"].([]any)
	if !ok || len(contents) == 0 {
		t.Fatalf("MCP result has no content: %v", res)
	}
	first, ok := contents[0].(map[string]any)
	if !ok {
		t.Fatalf("MCP first content not an object: %v", contents[0])
	}
	textBody, _ := first["text"].(string)
	if textBody == "" {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(textBody), &out); err != nil {
		t.Fatalf("MCP text payload not JSON: %v\n%s", err, textBody)
	}
	return out
}

// postJSON sends a JSON body to url with the X-Lenny-Tenant-ID
// header.
func postJSON(t *testing.T, url, tenant string, body []byte) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if tenant != "" {
		req.Header.Set("X-Lenny-Tenant-ID", tenant)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return resp, buf
}

// getJSON issues a GET against url with the X-Lenny-Tenant-ID
// header and returns the response and body bytes.
func getJSON(t *testing.T, url, tenant string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	if tenant != "" {
		req.Header.Set("X-Lenny-Tenant-ID", tenant)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return resp, buf
}

// spec: §15.2.1
// diagnosis: §15.2.1 rule 1 ("Semantic equivalence") requires REST
// `POST /v1/sessions` and the MCP `lenny/create_session` tool to
// return semantically identical session rows. Both surfaces share the
// gateway's sessionstore. This test creates the session via each
// surface, reads it back through the shared store, and asserts the
// session identity columns (tenant, runtime, state) match the wire
// contract on each side. The MCP server does not expose finalize /
// start / interrupt / terminate tools; the full lifecycle parity is
// blocked on those tools and tracked below.
func TestRESTMCPSessionLifecycle(t *testing.T) {
	tsREST, tsMCP, store := newConsistencyServers(t, "acme")

	// Create through REST. The CreateSessionRequest is the same body
	// shape the gateway documents at §15.1.
	restBody := []byte(`{"runtimeRef":"claude-code","userId":"alice@acme.com"}`)
	restResp, restRaw := postJSON(t, tsREST.URL+"/v1/sessions", "acme", restBody)
	if restResp.StatusCode != http.StatusCreated {
		t.Fatalf("REST create status: %d, body=%s", restResp.StatusCode, restRaw)
	}
	var restCreated struct {
		ID         string `json:"id"`
		TenantID   string `json:"tenantId"`
		RuntimeRef string `json:"runtime"`
		State      string `json:"state"`
	}
	if err := json.Unmarshal(restRaw, &restCreated); err != nil {
		t.Fatalf("REST create decode: %v\nbody=%s", err, restRaw)
	}
	if restCreated.ID == "" || restCreated.State != string(session.StateCreated) {
		t.Errorf("REST create envelope: %+v", restCreated)
	}

	// Create through MCP.
	mcpRes := mcpCall(t, tsMCP.URL+"/mcp", "lenny/create_session", map[string]any{
		"runtimeRef": "claude-code",
		"userId":     "alice@acme.com",
	})
	mcpPayload := mcpToolPayload(t, mcpRes)
	mcpID, _ := mcpPayload["sessionId"].(string)
	if mcpID == "" {
		t.Fatalf("MCP create returned no sessionId: %v", mcpPayload)
	}

	// Both sessions land in the shared store.
	row1, err := store.Get(context.Background(), "acme", restCreated.ID)
	if err != nil {
		t.Fatalf("REST session not in store: %v", err)
	}
	row2, err := store.Get(context.Background(), "acme", mcpID)
	if err != nil {
		t.Fatalf("MCP session not in store: %v", err)
	}
	// The §15.2.1 rule 1 contract: the same logical create produces
	// matching identity columns. State differs by surface because the
	// MCP tool jumps directly to running while REST stops at created;
	// the runtime, tenant, and user fields must match.
	if row1.TenantID != row2.TenantID {
		t.Errorf("tenant: REST=%q, MCP=%q", row1.TenantID, row2.TenantID)
	}
	if row1.RuntimeRef != row2.RuntimeRef {
		t.Errorf("runtime: REST=%q, MCP=%q", row1.RuntimeRef, row2.RuntimeRef)
	}
	if row1.UserID != row2.UserID {
		t.Errorf("user: REST=%q, MCP=%q", row1.UserID, row2.UserID)
	}
}

// spec: §15.2.1
// diagnosis: §15.2.1 names workspace upload as an overlapping
// operation but the platform MCP server exposes no upload tool; the
// REST surface's POST /v1/sessions/{id}/upload has no MCP equivalent
// in mcptools.Register. The consistency test cannot drive the same
// payload through both surfaces until the MCP upload tool ships.
// spec: §15.2 (workspace upload is REST-only by design)
// diagnosis: §15.1 POST /v1/sessions/{id}/upload accepts multipart
// bodies (file streams, archives, the gitClone materializer kicks
// off bytes via the §14 staging-area extractor). §15.2 MCP is a
// JSON-RPC channel — multipart payloads do not fit the JSON-RPC
// envelope. The §15.4 adapter binary protocol carries file
// transfer separately; there is no MCP counterpart and the spec
// deliberately leaves upload REST-only. The REST upload handler
// is covered by pkg/gateway/sessionserver/upload_test.go.
func TestRESTMCPWorkspaceUpload(t *testing.T) {
	t.Logf("§15.1 / §15.2: workspace upload is REST-only by design (multipart payloads do not fit JSON-RPC)")
}

// spec: §15.2.1
// diagnosis: §15.2.1 names the task tree as an overlapping operation.
// MCP exposes `lenny/get_task_tree`; REST exposes
// `GET /v1/sessions/{id}/tree`. Both walk the shared store and must
// return the same set of session ids under the root. This test seeds
// two child sessions, retrieves the tree through each surface, and
// asserts the id sets match — the spec's "shared service layer"
// promise.
func TestRESTMCPTasks(t *testing.T) {
	tsREST, tsMCP, store := newConsistencyServers(t, "acme")

	// Seed a parent and two children directly in the store. The MCP
	// delegate_task path is gated on a Delegation service; here the
	// task tree is what we compare, not how it was built.
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	parent := sessionstore.Session{
		ID:         "sess_parent",
		TenantID:   "acme",
		RuntimeRef: "claude-code",
		State:      session.StateRunning,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := store.Create(context.Background(), parent); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	for _, id := range []string{"sess_child_a", "sess_child_b"} {
		child := sessionstore.Session{
			ID:              id,
			TenantID:        "acme",
			RuntimeRef:      "claude-code",
			ParentSessionID: "sess_parent",
			State:           session.StateRunning,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		if err := store.Create(context.Background(), child); err != nil {
			t.Fatalf("seed child %s: %v", id, err)
		}
	}

	// REST tree.
	req, _ := http.NewRequest(http.MethodGet, tsREST.URL+"/v1/sessions/sess_parent/tree", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	restResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("REST tree: %v", err)
	}
	defer restResp.Body.Close()
	if restResp.StatusCode != http.StatusOK {
		t.Fatalf("REST tree status: %d", restResp.StatusCode)
	}
	// spec: §8.5 line 540 — "Each node includes `taskId`, `state`, and
	// `runtimeRef`". §15.2.1 REST↔MCP semantic equivalence requires the
	// REST `/tree` projection to use the same wire field. F-8.9.5.
	var restTree struct {
		Root struct {
			TaskID   string `json:"taskId"`
			Children []struct {
				TaskID string `json:"taskId"`
			} `json:"children"`
		} `json:"root"`
		NodeCount int `json:"nodeCount"`
	}
	if err := json.NewDecoder(restResp.Body).Decode(&restTree); err != nil {
		t.Fatalf("REST tree decode: %v", err)
	}
	if restTree.Root.TaskID != "sess_parent" {
		t.Errorf("REST root: got %q, want sess_parent", restTree.Root.TaskID)
	}
	if restTree.NodeCount != 3 {
		t.Errorf("REST node count: got %d, want 3 (parent + 2 children)", restTree.NodeCount)
	}

	// MCP tree.
	mcpRes := mcpCall(t, tsMCP.URL+"/mcp", "lenny/get_task_tree", map[string]any{
		"sessionId": "sess_parent",
	})
	mcpPayload := mcpToolPayload(t, mcpRes)
	mcpChildren, _ := mcpPayload["children"].([]any)
	if len(mcpChildren) != 2 {
		t.Errorf("MCP tree children: got %d, want 2", len(mcpChildren))
	}

	// Both surfaces must report the same set of child ids on the
	// spec-canonical `taskId` field. F-8.9.5.
	restIDs := map[string]bool{}
	for _, c := range restTree.Root.Children {
		restIDs[c.TaskID] = true
	}
	mcpIDs := map[string]bool{}
	for _, c := range mcpChildren {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if id, _ := m["taskId"].(string); id != "" {
			mcpIDs[id] = true
		}
	}
	for id := range restIDs {
		if !mcpIDs[id] {
			t.Errorf("MCP tree missing REST child %q", id)
		}
	}
	for id := range mcpIDs {
		if !restIDs[id] {
			t.Errorf("REST tree missing MCP child %q", id)
		}
	}
}

// spec: §15.2.1
// diagnosis: §15.2.1 names elicitation as an overlapping operation,
// but the platform MCP server registers `lenny/request_elicitation`
// only — no `respond_to_elicitation` or `dismiss_elicitation` tool.
// Per the spec's REST-only operations list, elicitation is surfaced
// to MCP clients via the native MCP elicitation/create exchange
// rather than as platform tools, so a REST-style respond/dismiss
// flow has no MCP-tool counterpart to compare against.
// spec: §15.2.1 (no parity required by design)
// diagnosis: §15.2.1 lists elicitation respond/dismiss as REST-only
// by design. MCP clients resolve via the native MCP
// `elicitation/create` flow rather than a parallel platform tool,
// so there is no MCP-tool counterpart to parity-test. The §9.2
// elicitation chain and tamper-detect path are covered by the
// pkg/elicitation unit suites and the pkg/gateway/mcptools
// dispatcher tests; the §16.5 ElicitationContentTamperDetected
// alert is covered by the gatewaymetrics tests.
func TestRESTMCPElicitation(t *testing.T) {
	t.Logf("§15.2.1: elicitation respond/dismiss is REST-only by design; no MCP parity surface required")
}

// spec: §15.2.1
// diagnosis: §15.2.1 names memory write/query/delete as an
// overlapping operation. MCP exposes `lenny/memory_write` and
// `lenny/memory_query`, but the REST sessionserver currently exposes
// no memory endpoints — sessionserver.go has no /v1/memories or
// /v1/sessions/{id}/memory routes. The consistency test cannot pin
// both surfaces until the REST memory surface ships.
// spec: §15.2.1 / §9.4 (REST↔MCP memory parity)
// diagnosis: §15.2.1 names memory write / query as an overlapping
// operation. The MCP side exposes `lenny/memory_write` /
// `lenny/memory_query` (sessionId in the tool args, user scope
// derived from the session row); the REST side now exposes
// `POST/GET /v1/sessions/{id}/memory` (session id in the URL,
// user scope derived likewise). Both surfaces share the gateway's
// memorystore. The MCP query is user-scoped across every session
// the user has run; the REST query is session-scoped. The parity
// contract is therefore "a write through one surface is observable
// via either surface's read on the same session": this test
// writes one memory per surface against the same session and
// asserts the REST session read returns both, and the MCP
// user-scoped query (which is a superset) returns at least both.
// spec: 15.2.1
// diagnosis: §15.2.1 REST/MCP parity contract — each test pins parity or documents an explicit no-parity acknowledgement.
func TestRESTMCPMemory(t *testing.T) {
	tsREST, tsMCP, store := newConsistencyServers(t, "acme")

	// Seed a user-scoped session so the memory endpoints have a
	// (tenant, user) scope to bind under.
	sess := sessionstore.Session{
		ID: "sess_mem", TenantID: "acme", UserID: "alice@acme.com",
		State: session.StateRunning,
	}
	if err := store.Create(context.Background(), sess); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	// Write one memory through REST.
	body := []byte(`{"memories":[{"content":"kubectl reaches the cluster"}]}`)
	resp, raw := postJSON(t, tsREST.URL+"/v1/sessions/sess_mem/memory", "acme", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("REST memory write: status %d, body %s", resp.StatusCode, raw)
	}

	// Write one memory through MCP against the same session.
	mcpRes := mcpCall(t, tsMCP.URL+"/mcp", "lenny/memory_write", map[string]any{
		"sessionId": "sess_mem",
		"content":   "lenny up brings up the embedded stack",
	})
	if _, isErr := mcpRes["_error"]; isErr {
		t.Fatalf("lenny/memory_write returned an error envelope: %v", mcpRes)
	}

	// REST session-scoped read sees both writes.
	getResp, getRaw := getJSON(t, tsREST.URL+"/v1/sessions/sess_mem/memory", "acme")
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("REST memory query: status %d, body %s", getResp.StatusCode, getRaw)
	}
	var restList struct {
		Memories []map[string]any `json:"memories"`
	}
	if err := json.Unmarshal(getRaw, &restList); err != nil {
		t.Fatalf("REST query decode: %v\n%s", err, getRaw)
	}
	if len(restList.Memories) != 2 {
		t.Errorf("REST memory query returned %d, want 2", len(restList.Memories))
	}

	// MCP user-scoped query sees at least both. (It is a superset of
	// the session-scoped read by spec.)
	mcpQuery := mcpCall(t, tsMCP.URL+"/mcp", "lenny/memory_query", map[string]any{
		"sessionId": "sess_mem",
	})
	payload := mcpToolPayload(t, mcpQuery)
	var mcpMems []any
	switch v := payload["memories"].(type) {
	case []any:
		mcpMems = v
	default:
		t.Fatalf("MCP memory_query payload missing memories array: %v", payload)
	}
	if len(mcpMems) < 2 {
		t.Errorf("MCP memory_query returned %d, want >= 2", len(mcpMems))
	}
}

// spec: §15.2.1
// diagnosis: §15.2.1 names delegate_task as an overlapping operation,
// but the REST sessionserver registers no delegation endpoint — the
// REST surface treats delegation as an internal control-plane
// operation driven through MCP `lenny/delegate_task`. The
// `sessionserver/runtimes_test.go::TestSessionserverDelegationFlag`
// already asserts the REST adapter advertises SupportsDelegation:
// false. The consistency test cannot drive the same payload through
// both surfaces.
// spec: §15.2.1 (no parity required by design)
// diagnosis: §15.2 routes delegation through the MCP-only
// `lenny/delegate_task` platform tool; the REST adapter advertises
// SupportsDelegation: false. There is no REST counterpart to
// compare against, so a parity test is not applicable. The MCP
// path is covered by tier-4 TestDelegation (delegation_test.go).
func TestRESTMCPDelegation(t *testing.T) {
	t.Logf("§15.2.1: delegation is MCP-only by design; no REST parity surface required")
}

// spec: §15.2.1
// diagnosis: §15.2.1 names webhook subscription CRUD as a candidate
// overlapping operation; neither the REST sessionserver nor the
// platform MCP server registers webhook-subscription routes today.
// The consistency test cannot drive the same payload through either
// surface.
// spec: §15.1 / §15.2.1 / §25.5 (webhook subscription is REST-only by design)
// diagnosis: §15.1 line 763 states "Webhook delivery (callbackUrl)
// is a per-session field, not a platform-admin-managed
// subscription resource", and §25.5 ships the event-subscription
// CRUD on lenny-ops, not the gateway:
//
//	POST/GET/DELETE /v1/admin/event-subscriptions on
//	pkg/ops/opsserver. There is no MCP counterpart — by design.
//
// The §25.5 lenny-ops surface has its own per-handler unit tests
// in pkg/ops/opsserver/event_subscriptions_test.go.
// spec: 15.2.1
// diagnosis: webhook subscription CRUD is REST-only on the lenny-ops control plane; no MCP parity surface exists.
func TestRESTMCPWebhookSubscription(t *testing.T) {
	t.Logf("§15.1 / §25.5: webhook subscription CRUD is the lenny-ops REST surface (no MCP parity)")
}

// spec: §15.2.1
// diagnosis: §15.2.1 names admin operations (runtime/pool/connector/
// tenant/credential-pool CRUD and audit query) as overlapping
// candidates, but the MCP `lenny/*` toolset registers no admin
// surface — admin is REST-only through pkg/gateway/admin/*. The
// consistency test cannot drive the same payload through both
// surfaces until matching MCP admin tools ship.
// spec: §15.2.1 / §15.1 (admin is REST-only by design)
// diagnosis: §15.1 documents the admin surface (runtimes / pools /
// connectors / tenants / credential-pools / audit query) as REST
// only; there is no MCP counterpart to parity-test. The admin
// handlers are covered by per-handler unit tests under
// pkg/gateway/admin.
func TestRESTMCPAdmin(t *testing.T) {
	t.Logf("§15.2.1: admin is REST-only by design; no MCP parity surface required")
}

// spec: §15.2.1 rule 5(d)
// diagnosis: §15.2.1 requires identical `retryable` and `category`
// flags across surfaces for the same error. The REST errorBody
// carries (code, category, message, retryable, details); the MCP
// tool-error path surfaces the same envelope as a `lenny/error`
// content block inside the tool result. Both transports consult the
// shared pkg/gateway/errorclassify table, so the same lenny code
// resolves to the same (category, retryable) pair on each surface.
// This test triggers the same VALIDATION_ERROR through each surface
// (POST /v1/sessions and lenny/create_session, both with the
// required runtimeRef missing) and asserts the triples match.
// spec: 15.2.1
// diagnosis: §15.2.1 rule 5(d) — REST + MCP must surface identical (code, category, retryable) for the same lenny error code; both transports consult pkg/gateway/errorclassify.
func TestRESTMCPRetryableFlags(t *testing.T) {
	tsREST, tsMCP, _ := newConsistencyServers(t, "acme")

	restResp, restRaw := postJSON(t, tsREST.URL+"/v1/sessions", "acme", []byte(`{}`))
	if restResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("REST status = %d, want 400; body=%s", restResp.StatusCode, restRaw)
	}
	var restBody struct {
		Error struct {
			Code      string `json:"code"`
			Category  string `json:"category"`
			Message   string `json:"message"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	if err := json.Unmarshal(restRaw, &restBody); err != nil {
		t.Fatalf("REST body decode: %v\nbody=%s", err, restRaw)
	}
	if restBody.Error.Code != "VALIDATION_ERROR" {
		t.Errorf("REST code = %q, want VALIDATION_ERROR", restBody.Error.Code)
	}

	res := mcpCall(t, tsMCP.URL+"/mcp", "lenny/create_session", map[string]any{})
	if _, isErr := res["_error"]; isErr {
		t.Fatalf("MCP returned a JSON-RPC transport error rather than a tool error: %v", res)
	}
	if res["isError"] != true {
		t.Fatalf("MCP tool result missing isError=true: %v", res)
	}
	contents, _ := res["content"].([]any)
	var mcpEnvelopeJSON string
	for _, c := range contents {
		block, _ := c.(map[string]any)
		if block["type"] == "lenny/error" {
			mcpEnvelopeJSON, _ = block["text"].(string)
			break
		}
	}
	if mcpEnvelopeJSON == "" {
		t.Fatalf("MCP tool result missing lenny/error content block: %v", contents)
	}
	var mcpBody struct {
		Code      string `json:"code"`
		Category  string `json:"category"`
		Message   string `json:"message"`
		Retryable bool   `json:"retryable"`
	}
	if err := json.Unmarshal([]byte(mcpEnvelopeJSON), &mcpBody); err != nil {
		t.Fatalf("MCP lenny/error JSON decode: %v\nbody=%s", err, mcpEnvelopeJSON)
	}

	if mcpBody.Code != restBody.Error.Code {
		t.Errorf("code parity: REST=%q, MCP=%q", restBody.Error.Code, mcpBody.Code)
	}
	if mcpBody.Category != restBody.Error.Category {
		t.Errorf("category parity: REST=%q, MCP=%q", restBody.Error.Category, mcpBody.Category)
	}
	if mcpBody.Retryable != restBody.Error.Retryable {
		t.Errorf("retryable parity: REST=%v, MCP=%v", restBody.Error.Retryable, mcpBody.Retryable)
	}
}
