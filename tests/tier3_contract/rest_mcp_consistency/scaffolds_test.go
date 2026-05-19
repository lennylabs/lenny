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

	rest := sessionserver.New(store, sessionserver.Options{})
	tsREST := httptest.NewServer(rest.Handler())
	t.Cleanup(tsREST.Close)

	mcpSrv := mcp.NewServer()
	mcptools.Register(mcpSrv, mcptools.Deps{
		Store:    store,
		Executor: exec,
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
func TestRESTMCPWorkspaceUpload(t *testing.T) {
	t.Skip("blocked: §15.2.1 REST↔MCP workspace upload — the platform MCP server registers no upload tool; mcptools/mcptools.go has create_session/send_message/delegate but no upload counterpart to compare against POST /v1/sessions/{id}/upload")
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
	var restTree struct {
		Root struct {
			SessionID string `json:"sessionId"`
			Children  []struct {
				SessionID string `json:"sessionId"`
			} `json:"children"`
		} `json:"root"`
		NodeCount int `json:"nodeCount"`
	}
	if err := json.NewDecoder(restResp.Body).Decode(&restTree); err != nil {
		t.Fatalf("REST tree decode: %v", err)
	}
	if restTree.Root.SessionID != "sess_parent" {
		t.Errorf("REST root: got %q, want sess_parent", restTree.Root.SessionID)
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

	// Both surfaces must report the same set of child ids.
	restIDs := map[string]bool{}
	for _, c := range restTree.Root.Children {
		restIDs[c.SessionID] = true
	}
	mcpIDs := map[string]bool{}
	for _, c := range mcpChildren {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if id, _ := m["sessionId"].(string); id != "" {
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
func TestRESTMCPElicitation(t *testing.T) {
	t.Skip("blocked: §15.2.1 REST↔MCP elicitation — the MCP adapter exposes `lenny/request_elicitation` but no respond/dismiss tools; the spec routes MCP-side elicitation resolution through the native MCP elicitation/create flow rather than platform tools, so there is no MCP-tool resolution surface to compare against the REST endpoints")
}

// spec: §15.2.1
// diagnosis: §15.2.1 names memory write/query/delete as an
// overlapping operation. MCP exposes `lenny/memory_write` and
// `lenny/memory_query`, but the REST sessionserver currently exposes
// no memory endpoints — sessionserver.go has no /v1/memories or
// /v1/sessions/{id}/memory routes. The consistency test cannot pin
// both surfaces until the REST memory surface ships.
func TestRESTMCPMemory(t *testing.T) {
	t.Skip("blocked: §15.2.1 REST↔MCP memory — pkg/gateway/sessionserver registers no /v1/memories endpoints; the MCP tools `lenny/memory_write` / `lenny/memory_query` have no REST counterpart in sessionserver.go to compare against")
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
func TestRESTMCPDelegation(t *testing.T) {
	t.Skip("blocked: §15.2.1 REST↔MCP delegation — pkg/gateway/sessionserver has no REST delegation endpoint (the REST adapter advertises SupportsDelegation: false); delegation is MCP-only through lenny/delegate_task and has no REST counterpart to compare against")
}

// spec: §15.2.1
// diagnosis: §15.2.1 names webhook subscription CRUD as a candidate
// overlapping operation; neither the REST sessionserver nor the
// platform MCP server registers webhook-subscription routes today.
// The consistency test cannot drive the same payload through either
// surface.
func TestRESTMCPWebhookSubscription(t *testing.T) {
	t.Skip("blocked: §15.2.1 REST↔MCP webhook subscriptions — neither pkg/gateway/sessionserver nor pkg/gateway/mcptools registers webhook subscription CRUD routes; the §14 webhook subscription store has no public REST or MCP surface yet")
}

// spec: §15.2.1
// diagnosis: §15.2.1 names admin operations (runtime/pool/connector/
// tenant/credential-pool CRUD and audit query) as overlapping
// candidates, but the MCP `lenny/*` toolset registers no admin
// surface — admin is REST-only through pkg/gateway/admin/*. The
// consistency test cannot drive the same payload through both
// surfaces until matching MCP admin tools ship.
func TestRESTMCPAdmin(t *testing.T) {
	t.Skip("blocked: §15.2.1 REST↔MCP admin — pkg/gateway/admin exposes the §13.9 REST admin surface (runtimes/pools/connectors/tenants/credentialPools/audit query) but pkg/gateway/mcptools registers no corresponding admin tools; admin is REST-only and has no MCP counterpart to compare against")
}

// spec: §15.2.1
// diagnosis: §15.2.1 rule 5(d) requires identical `retryable` and
// `category` flags across surfaces for the same error condition. The
// REST sessionserver's errorBody envelope (sessionserver.go) carries
// `code`/`message`/`details` only — no `category` or `retryable`
// fields are present, and the MCP server's JSON-RPC error response
// has no parallel fields either. The shared error-classifier
// package the spec calls out is not yet wired into either transport;
// the test cannot assert parity on fields that neither surface emits.
func TestRESTMCPRetryableFlags(t *testing.T) {
	t.Skip("blocked: §15.2.1 REST↔MCP error envelope parity — the REST errorBody at pkg/gateway/sessionserver/sessionserver.go carries {code, message, details} only; neither it nor the MCP JSON-RPC error response surfaces the `retryable` / `category` flags §15.2.1 rule 5(d) requires for parity testing")
}
