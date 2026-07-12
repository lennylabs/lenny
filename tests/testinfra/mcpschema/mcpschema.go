// SPDX-License-Identifier: MIT

// Package mcpschema validates MCP JSON-RPC frames against the published
// MCP protocol schema vendored under tests/testdata/mcp/schema. It is
// the reusable validator behind the §4.1 dedicated /mcp/runtimes/{name}
// conformance test: given a raw JSON-RPC response frame produced by a
// type: mcp runtime, it asserts both that the frame is a well-formed
// JSON-RPC 2.0 success envelope (JSONRPCResponse) and that its result
// satisfies the method-specific MCP result contract (InitializeResult,
// ListToolsResult, CallToolResult, and the Tool object).
//
// The gateway's own map[string]any construction and Lenny's unit tests
// check field presence against Lenny's expectations; neither catches a
// response that round-trips through Lenny yet violates the real MCP
// contract (a wrong key casing, a missing required field, an inputSchema
// whose type is not the literal "object" the MCP schema requires). This
// package pins the exchange against the external contract a conforming
// MCP client validates before proceeding.
//
// It wraps tests/testinfra/schematest so the santhosh-tekuri/jsonschema
// import stays anchored in a regular Go package (see that package's doc
// for why). Compilation of a vendored schema definition is an
// infrastructure invariant and fails the test through t; a validation
// mismatch is returned as an error so the caller can attach its own
// diagnosis.
//
// spec: §4.1 (dedicated MCP endpoints for type: mcp runtimes at
// /mcp/runtimes/{name}); §15.2 (MCP version negotiation pins tool
// schemas and error formats to the negotiated revision).
package mcpschema

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// CurrentVersion is the current negotiated MCP protocol revision, matching
// pkg/adapter/mcp.ProtocolVersion and the reference type: mcp runtime. It
// is the default the exchange helpers validate against.
const CurrentVersion = "2025-03-26"

// Result definition names in the vendored MCP schema. These are the
// method-specific result contracts the /mcp/runtimes/{name} exchange
// exercises.
const (
	DefInitializeResult = "InitializeResult"
	DefListToolsResult  = "ListToolsResult"
	DefCallToolResult   = "CallToolResult"
	DefTool             = "Tool"

	// defResponseEnvelope is the JSON-RPC 2.0 success-response envelope
	// every result frame is wrapped in.
	defResponseEnvelope = "JSONRPCResponse"
)

// Validator validates MCP JSON-RPC frames for one negotiated protocol
// version. Compiled schemas are cached across calls.
type Validator struct {
	t       testing.TB
	version string
	cache   map[string]*jsonschema.Schema
}

// New returns a Validator bound to version (for example CurrentVersion),
// resolving the vendored schema file for that revision. A version with no
// vendored schema fails the first Validate call through t.
func New(t testing.TB, version string) *Validator {
	t.Helper()
	return &Validator{t: t, version: version, cache: map[string]*jsonschema.Schema{}}
}

// schemaRef is the compiler reference for a definition in the vendored
// schema file for this Validator's protocol version.
func schemaRef(version, definition string) string {
	return "tests/testdata/mcp/schema/" + version + ".schema.json#/definitions/" + definition
}

// schema compiles and caches the named definition out of the vendored
// schema file. A compile failure is an infrastructure invariant and fails
// the test through t.
func (v *Validator) schema(definition string) *jsonschema.Schema {
	v.t.Helper()
	if s, ok := v.cache[definition]; ok {
		return s
	}
	s := schematest.Compile(v.t, schemaRef(v.version, definition))
	v.cache[definition] = s
	return s
}

// ValidateResult validates a still-encoded MCP result payload against the
// named result definition. It returns a descriptive error on a mismatch.
func (v *Validator) ValidateResult(raw json.RawMessage, definition string) error {
	v.t.Helper()
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		return fmt.Errorf("decode result for %s: %w", definition, err)
	}
	if err := v.schema(definition).Validate(instance); err != nil {
		return fmt.Errorf("result does not validate against the published MCP %s (%s): %w",
			definition, v.version, err)
	}
	return nil
}

// ValidateResponseFrame validates a full JSON-RPC response frame from a
// type: mcp runtime. It asserts the frame is a well-formed JSON-RPC 2.0
// success envelope (JSONRPCResponse), that it carries no JSON-RPC error,
// and that its result satisfies resultDefinition. resultDefinition is one
// of the Def* constants.
func (v *Validator) ValidateResponseFrame(frame []byte, resultDefinition string) error {
	v.t.Helper()
	var instance any
	if err := json.Unmarshal(frame, &instance); err != nil {
		return fmt.Errorf("decode response frame: %w", err)
	}
	if err := v.schema(defResponseEnvelope).Validate(instance); err != nil {
		return fmt.Errorf("frame does not validate against the published MCP JSONRPCResponse envelope (%s): %w",
			v.version, err)
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(frame, &env); err != nil {
		return fmt.Errorf("decode response envelope: %w", err)
	}
	if len(env.Error) > 0 {
		return fmt.Errorf("frame carries a JSON-RPC error rather than a result: %s", env.Error)
	}
	if len(env.Result) == 0 {
		return errors.New("frame has no result field")
	}
	return v.ValidateResult(env.Result, resultDefinition)
}

// Tools decodes the tools array out of a validated tools/list result and
// validates each entry against the MCP Tool definition, returning the raw
// tool objects so the caller can make further assertions.
func (v *Validator) Tools(listResult json.RawMessage) ([]json.RawMessage, error) {
	v.t.Helper()
	var listing struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(listResult, &listing); err != nil {
		return nil, fmt.Errorf("decode tools/list result: %w", err)
	}
	for i, tool := range listing.Tools {
		if err := v.ValidateResult(tool, DefTool); err != nil {
			return nil, fmt.Errorf("tools[%d]: %w", i, err)
		}
	}
	return listing.Tools, nil
}
