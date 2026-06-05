// SPDX-License-Identifier: MIT

//go:build contract

// §15.2.1 `RegisterAdapterUnderTest` test matrix — state-transition
// sequences (spec §15.2.1 line 1411) and pagination (line 1412). The
// overlapping client-facing lifecycle and read tools dispatch through
// the one shared §15.1 service layer (F-15.2.3), so `get_session_status`
// and `list_sessions` run the identical REST route. These tests assert
// state identity and pagination identity across the two surfaces after
// an identical sequence of operations.
package rest_mcp_consistency_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// mcpStatePayload decodes the `state` field from a get_session_status
// tool result (the verbatim REST SessionResponse body).
func mcpStatePayload(t *testing.T, res map[string]any) string {
	t.Helper()
	payload := mcpToolPayload(t, res)
	state, _ := payload["state"].(string)
	return state
}

// restState reads the `state` field from a REST SessionResponse body.
func restState(t *testing.T, raw []byte) string {
	t.Helper()
	var body struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("REST session decode: %v\nbody=%s", err, raw)
	}
	return body.State
}

// spec: §15.2.1 line 1411
// diagnosis: after an identical operation sequence, GET
// /v1/sessions/{id} and the get_session_status MCP tool must report the
// same session state. This drives the create → terminated sequence (the
// transitions reachable without a live runtime adapter; the running →
// completed sequences require a real pod and are exercised in tier-5)
// and asserts state identity at each observable boundary.
func TestRegisterAdapterUnderTestStateSequence(t *testing.T) {
	tsREST, tsMCP, _ := newConsistencyServers(t, "acme")

	resp, raw := postJSON(t, tsREST.URL+"/v1/sessions", "acme",
		[]byte(`{"runtimeRef":"claude-code","userId":"alice@acme.com"}`))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", resp.StatusCode, raw)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.ID == "" {
		t.Fatalf("create decode: %v\nbody=%s", err, raw)
	}

	assertStateParity := func(step string) string {
		t.Helper()
		_, getRaw := getJSON(t, tsREST.URL+"/v1/sessions/"+created.ID, "acme")
		rest := restState(t, getRaw)
		mcp := mcpStatePayload(t, mcpCall(t, tsMCP.URL+"/mcp", "lenny/get_session_status",
			map[string]any{"sessionId": created.ID}))
		if rest == "" {
			t.Errorf("%s: REST returned empty state", step)
		}
		if rest != mcp {
			t.Errorf("%s: state parity REST=%q MCP=%q", step, rest, mcp)
		}
		return rest
	}

	// Step 1 — just created.
	if got := assertStateParity("after create"); got != "created" {
		t.Errorf("after create: state = %q, want created", got)
	}

	// Step 2 — terminate (created → completed is an allowed §7.2 edge).
	termResp, termRaw := postJSON(t, tsREST.URL+"/v1/sessions/"+created.ID+"/terminate", "acme", nil)
	if termResp.StatusCode != http.StatusOK {
		t.Fatalf("terminate: status %d, body %s", termResp.StatusCode, termRaw)
	}
	if got := assertStateParity("after terminate"); got != "completed" {
		t.Errorf("after terminate: state = %q, want completed", got)
	}
}

// listEnvelope is the §15.1 cursor-paginated list shape both surfaces
// return for an overlapping list operation.
type listEnvelope struct {
	Items []struct {
		ID string `json:"id"`
	} `json:"items"`
	Cursor  string `json:"cursor"`
	HasMore bool   `json:"hasMore"`
	Total   int    `json:"total"`
}

func decodeList(t *testing.T, raw []byte) listEnvelope {
	t.Helper()
	var env listEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("list decode: %v\nbody=%s", err, raw)
	}
	return env
}

func (e listEnvelope) ids() map[string]bool {
	out := map[string]bool{}
	for _, it := range e.Items {
		out[it.ID] = true
	}
	return out
}

// spec: §15.2.1 line 1412
// diagnosis: for overlapping list operations the default page size,
// cursor semantics, and empty-result shape must be identical across REST
// and the adapter surface. The MCP list_sessions tool forwards GET
// /v1/sessions through the shared service, so this asserts the same id
// set, total, and hasMore on both surfaces, plus an identical empty-set
// shape for a tenant with no sessions.
func TestRegisterAdapterUnderTestPaginationParity(t *testing.T) {
	tsREST, tsMCP, _ := newConsistencyServers(t, "acme")

	// Seed two sessions so the list is non-trivial.
	for _, user := range []string{"alice@acme.com", "bob@acme.com"} {
		body := []byte(`{"runtimeRef":"claude-code","userId":"` + user + `"}`)
		resp, raw := postJSON(t, tsREST.URL+"/v1/sessions", "acme", body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("seed %s: status %d, body %s", user, resp.StatusCode, raw)
		}
	}

	_, restRaw := getJSON(t, tsREST.URL+"/v1/sessions", "acme")
	restList := decodeList(t, restRaw)

	mcpList := decodeList(t, mustMCPListBody(t,
		mcpCall(t, tsMCP.URL+"/mcp", "lenny/list_sessions", map[string]any{})))

	if len(restList.Items) != 2 {
		t.Errorf("REST list returned %d sessions, want 2", len(restList.Items))
	}
	if restList.Total != mcpList.Total {
		t.Errorf("total parity: REST=%d MCP=%d", restList.Total, mcpList.Total)
	}
	if restList.HasMore != mcpList.HasMore {
		t.Errorf("hasMore parity: REST=%v MCP=%v", restList.HasMore, mcpList.HasMore)
	}
	restIDs, mcpIDs := restList.ids(), mcpList.ids()
	for id := range restIDs {
		if !mcpIDs[id] {
			t.Errorf("MCP list missing REST session %q", id)
		}
	}
	for id := range mcpIDs {
		if !restIDs[id] {
			t.Errorf("REST list missing MCP session %q", id)
		}
	}

	// Empty-result shape parity: a tenant with no sessions returns the
	// same empty envelope (items empty, total 0, hasMore false) on both
	// surfaces.
	tsRESTEmpty, tsMCPEmpty, _ := newConsistencyServers(t, "globex")
	_, emptyRESTRaw := getJSON(t, tsRESTEmpty.URL+"/v1/sessions", "globex")
	emptyREST := decodeList(t, emptyRESTRaw)
	emptyMCP := decodeList(t, mustMCPListBody(t,
		mcpCall(t, tsMCPEmpty.URL+"/mcp", "lenny/list_sessions", map[string]any{})))
	if len(emptyREST.Items) != 0 || emptyREST.Total != 0 || emptyREST.HasMore {
		t.Errorf("REST empty list not empty: %+v", emptyREST)
	}
	if len(emptyMCP.Items) != 0 || emptyMCP.Total != 0 || emptyMCP.HasMore {
		t.Errorf("MCP empty list not empty: %+v", emptyMCP)
	}
}

// mustMCPListBody returns the raw JSON text the list_sessions tool
// encodes as its single content block. The tool forwards the REST list
// body verbatim, so this is the §15.1 paginated envelope.
func mustMCPListBody(t *testing.T, res map[string]any) []byte {
	t.Helper()
	if _, isErr := res["_error"]; isErr {
		t.Fatalf("MCP list returned an error envelope: %v", res)
	}
	contents, ok := res["content"].([]any)
	if !ok || len(contents) == 0 {
		t.Fatalf("MCP list result has no content: %v", res)
	}
	first, _ := contents[0].(map[string]any)
	text, _ := first["text"].(string)
	if text == "" {
		t.Fatalf("MCP list result empty text block: %v", res)
	}
	return []byte(text)
}
