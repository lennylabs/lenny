// SPDX-License-Identifier: MIT

//go:build contract

package rest_mcp_consistency_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// advertisedInputSchema reads the gateway-edge tools/list catalog over
// the wire and returns the named tool's inputSchema.
func advertisedInputSchema(t *testing.T, mcpURL, tenant, tool string) json.RawMessage {
	t.Helper()
	raw := rpcResult(t, mcpURL, tenant, "tools/list", nil)
	var listing struct {
		Tools []struct {
			Name        string          `json:"name"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &listing); err != nil {
		t.Fatalf("decode tools/list result: %v", err)
	}
	for _, entry := range listing.Tools {
		if entry.Name == tool {
			return entry.InputSchema
		}
	}
	t.Fatalf("tools/list does not advertise %s", tool)
	return nil
}

// compileInputSchema compiles an advertised tool inputSchema as a JSON
// Schema document so arguments can be validated against it.
func compileInputSchema(t *testing.T, raw json.RawMessage) *jsonschema.Schema {
	t.Helper()
	const resource = "mem://finalize_workspace.input.json"
	c := jsonschema.NewCompiler()
	if err := c.AddResource(resource, bytes.NewReader(raw)); err != nil {
		t.Fatalf("add inputSchema resource: %v (%s)", err, raw)
	}
	sch, err := c.Compile(resource)
	if err != nil {
		t.Fatalf("advertised inputSchema is not a valid JSON Schema: %v (%s)", err, raw)
	}
	return sch
}

// spec: 15.2.1 (REST/MCP Consistency Contract), 15.2 (MCP API), 14.1 (WorkspacePlan Schema Versioning), 7.4 (Upload Safety, Upload authorization)
// diagnosis: a failure here means the `lenny/finalize_workspace` tool
// advertised on `tools/list` no longer declares the optional
// `workspacePlan` argument the REST finalize body accepts (§14.1 Plan
// binding), or declares it with a type that rejects a plan object or a
// null. A schema-validating MCP client then refuses to send the plan the
// REST surface binds, so a finalize-time bind is reachable on REST and
// unreachable on MCP, breaching the §15.2.1 schema-consistency rule.
func TestFinalizeWorkspaceAdvertisesWorkspacePlan_spec_15_2_1(t *testing.T) {
	_, tsMCP, _ := newConsistencyServers(t, "acme")
	sch := compileInputSchema(t, advertisedInputSchema(t, tsMCP.URL+"/mcp", "acme", "lenny/finalize_workspace"))

	cases := []struct {
		name  string
		args  string
		valid bool
	}{
		{name: "session id and upload token only", args: `{"sessionId":"s1","uploadToken":"tok"}`, valid: true},
		{name: "plan object", args: `{"sessionId":"s1","uploadToken":"tok","workspacePlan":{"schemaVersion":1,"sources":[{"type":"uploadFile","path":"a.txt","uploadRef":"opaque-ref"}]}}`, valid: true},
		{name: "explicit null plan", args: `{"sessionId":"s1","uploadToken":"tok","workspacePlan":null}`, valid: true},
		{name: "plan as string", args: `{"sessionId":"s1","uploadToken":"tok","workspacePlan":"not-a-plan"}`, valid: false},
		{name: "plan as array", args: `{"sessionId":"s1","uploadToken":"tok","workspacePlan":[]}`, valid: false},
		{name: "missing session id", args: `{"uploadToken":"tok","workspacePlan":{"schemaVersion":1}}`, valid: false},
		// spec: §7.4 (Upload authorization) — every finalize request carries
		// the uploadToken, which the REST operation requires as a header.
		{name: "missing upload token", args: `{"sessionId":"s1"}`, valid: false},
		{name: "upload token not a string", args: `{"sessionId":"s1","uploadToken":7}`, valid: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var instance any
			if err := json.Unmarshal([]byte(tc.args), &instance); err != nil {
				t.Fatalf("decode args: %v", err)
			}
			err := sch.Validate(instance)
			if tc.valid && err != nil {
				t.Errorf("advertised schema rejects %s: %v", tc.args, err)
			}
			if !tc.valid && err == nil {
				t.Errorf("advertised schema accepts %s", tc.args)
			}
		})
	}
}
