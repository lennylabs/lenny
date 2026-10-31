// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
)

// priorWireSchemas are the hand-written per-session input schemas the
// tools advertised before generation, decoded for a structural comparison.
// The generated schemas must advertise the same arguments, types, required
// set, and descriptions, so a client sees no change beyond the
// finalize_workspace workspacePlan description, which now comes from the
// OpenAPI FinalizeRequest component. priorFinalize also carries the required
// uploadToken argument the generated schema takes from the finalize
// operation's X-Lenny-Upload-Token header parameter (§7.4 Upload
// authorization), described as lenny/upload_files describes it.
const (
	priorSessionIDOnly = `{"type":"object","required":["sessionId"],"properties":{"sessionId":{"type":"string","description":"The target session id."}}}`
	priorFinalize      = `{"type":"object","required":["sessionId","uploadToken"],"properties":{"sessionId":{"type":"string","description":"The target session id."},"uploadToken":{"type":"string","description":"The §7.1 uploadToken returned by create_session."},"workspacePlan":{"type":["object","null"],"additionalProperties":true,"description":"Optional §14 WorkspacePlan bound at finalize when the session was created without one."}}}`
	// priorLogs had no description on sessionId; the generated schema
	// carries the shared session id description the other tools use.
	priorLogs = `{"type":"object","required":["sessionId"],"properties":{"sessionId":{"type":"string","description":"The target session id."},"since":{"type":"string","description":"RFC3339 lower bound; only log lines at or after this time are returned."},"limit":{"type":"integer","description":"Maximum number of log lines to return."}}}`
)

func decodeSchema(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode schema: %v\n%s", err, raw)
	}
	return m
}

// dropDescription removes the description of one property so a comparison
// can ignore a description that the OpenAPI document now supplies.
func dropDescription(schema map[string]any, prop string) {
	if props, ok := schema["properties"].(map[string]any); ok {
		if p, ok := props[prop].(map[string]any); ok {
			delete(p, "description")
		}
	}
}

// TestPerSessionGeneratedSchemasMatchPriorWire asserts that generating the
// per-session tool schemas from OpenAPI, with the `id` path parameter
// renamed to `sessionId`, advertises the same structure the hand-written
// schemas did, and that each tool's catalog entry is the generated schema.
//
// spec: 15.2.1 (REST/MCP Consistency Contract, item 4), 15.2 (MCP API, the
// client-facing tools)
func TestPerSessionGeneratedSchemasMatchPriorWire_spec_15_2_1(t *testing.T) {
	srv := newClientToolsMCP(t, &fakeService{})
	cases := []struct {
		tool      string
		generated json.RawMessage
		prior     string
		// ignoreDescriptionOf names a property whose description now comes
		// from OpenAPI.
		ignoreDescriptionOf string
	}{
		{tool: "lenny/start_session", generated: mcptools.GeneratedStartSessionInputSchema, prior: priorSessionIDOnly},
		{tool: "lenny/terminate_session", generated: mcptools.GeneratedTerminateSessionInputSchema, prior: priorSessionIDOnly},
		{tool: "lenny/resume_session", generated: mcptools.GeneratedResumeSessionInputSchema, prior: priorSessionIDOnly},
		{tool: "lenny/interrupt_session", generated: mcptools.GeneratedInterruptSessionInputSchema, prior: priorSessionIDOnly},
		{tool: "lenny/get_session_status", generated: mcptools.GeneratedGetSessionStatusInputSchema, prior: priorSessionIDOnly},
		{tool: "lenny/list_artifacts", generated: mcptools.GeneratedListArtifactsInputSchema, prior: priorSessionIDOnly},
		{tool: "lenny/get_token_usage", generated: mcptools.GeneratedGetTokenUsageInputSchema, prior: priorSessionIDOnly},
		{tool: "lenny/get_session_logs", generated: mcptools.GeneratedGetSessionLogsInputSchema, prior: priorLogs},
		{tool: "lenny/finalize_workspace", generated: mcptools.GeneratedFinalizeWorkspaceInputSchema, prior: priorFinalize, ignoreDescriptionOf: "workspacePlan"},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			advertised := catalogInputSchema(t, srv, tc.tool)
			if string(advertised) != string(tc.generated) {
				t.Errorf("catalog schema is not the generated schema:\n catalog:   %s\n generated: %s", advertised, tc.generated)
			}
			got := decodeSchema(t, tc.generated)
			want := decodeSchema(t, []byte(tc.prior))
			if tc.ignoreDescriptionOf != "" {
				dropDescription(got, tc.ignoreDescriptionOf)
				dropDescription(want, tc.ignoreDescriptionOf)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("generated schema differs from the prior wire schema:\n generated: %s\n prior:     %s", tc.generated, tc.prior)
			}
		})
	}
}

// TestFinalizeWorkspacePlanDescriptionFromOpenAPI asserts the
// finalize_workspace workspacePlan argument carries a description, which
// now comes from the OpenAPI FinalizeRequest component.
//
// spec: 15.2.1 (REST/MCP Consistency Contract, item 4), 14.1 (WorkspacePlan
// Schema Versioning, Plan binding)
func TestFinalizeWorkspacePlanDescriptionFromOpenAPI_spec_15_2_1(t *testing.T) {
	got := decodeSchema(t, mcptools.GeneratedFinalizeWorkspaceInputSchema)
	plan := got["properties"].(map[string]any)["workspacePlan"].(map[string]any)
	if d, _ := plan["description"].(string); d == "" {
		t.Errorf("workspacePlan has no description: %v", plan)
	}
}
