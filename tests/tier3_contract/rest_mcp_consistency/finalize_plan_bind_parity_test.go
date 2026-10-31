// SPDX-License-Identifier: MIT

//go:build contract

package rest_mcp_consistency_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// createForFinalizeParity creates a session over REST with the given create
// body and returns its ID.
func createForFinalizeParity(t *testing.T, restURL string, body []byte) string {
	t.Helper()
	resp, raw := postJSON(t, restURL+"/v1/sessions", "acme", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed create: status %d, body %s", resp.StatusCode, raw)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.ID == "" {
		t.Fatalf("seed create decode: %v\nbody=%s", err, raw)
	}
	return created.ID
}

// restFinalizeReason decodes details.reason from a REST error envelope.
func restFinalizeReason(t *testing.T, raw []byte) string {
	t.Helper()
	var body struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("REST error decode: %v\nbody=%s", err, raw)
	}
	reason, _ := body.Error.Details["reason"].(string)
	return reason
}

// spec: 15.2.1 (REST/MCP Consistency Contract), 14.1 (WorkspacePlan Schema Versioning), 14 (Workspace Plan Schema)
// diagnosis: REST POST /v1/sessions/{id}/finalize and MCP
// lenny/finalize_workspace answered the same plan-bind rejection with a
// different code, category, or retryable flag, or one surface accepted a
// plan the other rejected. The MCP tool forwards its workspacePlan
// argument as the REST finalize body, so a divergence means the argument
// is dropped or reshaped on the MCP path, or the two surfaces classify
// the code differently. A session left outside created means a rejected
// finalize bound the plan or transitioned the row.
func TestFinalizePlanBindRejectionParity(t *testing.T) {
	foreignRef := blobstore.URI{
		TenantID: "acme", ObjectType: blobstore.ObjectTypeUpload,
		SessionID: "sess_other", PartID: "p1", TTL: time.Hour,
	}.String()
	uploadPlan := json.RawMessage(`{"schemaVersion":1,"sources":[{"type":"uploadFile","path":"a.txt","uploadRef":"` + foreignRef + `"}]}`)
	cases := []struct {
		name       string
		createBody string
		wantReason string
	}{
		{
			name:       "upload_ref_foreign_session",
			createBody: `{"runtimeRef":"claude-code","userId":"alice@acme.com"}`,
			wantReason: "upload_ref_foreign_session",
		},
		{
			name: "plan_already_set",
			createBody: `{"runtimeRef":"claude-code","userId":"alice@acme.com",` +
				`"workspacePlan":{"schemaVersion":1,"sources":[{"type":"inlineFile","path":"seed.txt","content":"seed"}]}}`,
			wantReason: "plan_already_set",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tsREST, tsMCP, store := newConsistencyServers(t, "acme")
			id := createForFinalizeParity(t, tsREST.URL, []byte(tc.createBody))

			restBody, err := json.Marshal(map[string]json.RawMessage{"workspacePlan": uploadPlan})
			if err != nil {
				t.Fatalf("marshal finalize body: %v", err)
			}
			resp, raw := postJSON(t, tsREST.URL+"/v1/sessions/"+id+"/finalize", "acme", restBody)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("REST finalize status = %d, want 400; body %s", resp.StatusCode, raw)
			}
			if got := restFinalizeReason(t, raw); got != tc.wantReason {
				t.Errorf("REST details.reason = %q, want %q", got, tc.wantReason)
			}
			rest := decodeRESTError(t, raw)
			mcpRes := decodeMCPError(t, mcpCall(t, tsMCP.URL+"/mcp", "lenny/finalize_workspace", map[string]any{
				// The consistency servers wire no upload-token verifier, so
				// any non-empty token reaches the plan-bind check.
				"sessionId": id, "uploadToken": "tok-parity", "workspacePlan": uploadPlan,
			}))

			if rest.Code != "VALIDATION_ERROR" {
				t.Errorf("REST code = %q, want VALIDATION_ERROR", rest.Code)
			}
			if rest.Code != mcpRes.Code {
				t.Errorf("code parity: REST=%q MCP=%q", rest.Code, mcpRes.Code)
			}
			if rest.Category != mcpRes.Category {
				t.Errorf("category parity: REST=%q MCP=%q", rest.Category, mcpRes.Category)
			}
			if rest.Retryable != mcpRes.Retryable {
				t.Errorf("retryable parity: REST=%v MCP=%v", rest.Retryable, mcpRes.Retryable)
			}
			assertStillCreated(t, store, id)
		})
	}
}

// assertStillCreated fails unless the session is still in created after
// both rejected finalize calls.
func assertStillCreated(t *testing.T, store sessionstore.Store, id string) {
	t.Helper()
	row, err := store.Get(context.Background(), "acme", id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	if row.State != session.StateCreated {
		t.Errorf("state = %q after the rejected finalize calls, want created", row.State)
	}
}
