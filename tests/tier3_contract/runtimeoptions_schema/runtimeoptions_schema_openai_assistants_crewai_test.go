//go:build contract

// SPDX-License-Identifier: MIT

// This file extends the Tier 3 contract suite in
// runtimeoptions_schema_test.go to cover the openai-assistants and
// crewai published runtimeOptions JSON Schema documents
// (schemas/runtime-options/openai-assistants/v1.json and
// schemas/runtime-options/crewai/v1.json). Section 26.10 and 26.11 of
// spec/26_reference-runtime-catalog.md declare these runtimes'
// runtimeOptionsSchema as (in the openai-assistants case) an update to
// the existing §14 schema renamed from openai-agents to
// openai-assistants, and (in the crewai case) a $ref to
// https://schemas.lenny.dev/runtime-options/crewai/v1.json;
// spec/14_workspace-plan-schema.md inlines the canonical schema body
// each publishes. This suite validates the on-disk documents against
// that canonical body: the required field, the enum, and
// additionalProperties: false each reject the payload the spec
// describes, and a minimal and fully-populated valid payload are
// accepted.
package runtimeoptionsschema_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: §14 lines 194-206 (openai-assistants runtimeOptions schema
// body: "assistantId": {"type":"string",...}, "required":
// ["assistantId"], "additionalProperties": false); §26.10
// ("runtimeOptionsSchema updates the existing §14 openai-agents schema:
// assistantId (required)... The schema name in §14 is renamed from
// openai-agents to openai-assistants for consistency")
// diagnosis: a failure here means schemas/runtime-options/openai-assistants/v1.json
// no longer matches the canonical schema body spec/14_workspace-plan-schema.md
// publishes for the openai-assistants runtime, or the schema has
// regressed to the pre-rename openai-agents name, so a client
// validating locally against the published URL and the gateway's own
// §14 line 155 session-creation validation would diverge.
func TestOpenAIAssistantsRuntimeOptionsSchema(t *testing.T) {
	t.Parallel()
	schema := schematest.Compile(t, "schemas/runtime-options/openai-assistants/v1.json")

	// spec: §26.10 — "The schema name in §14 is renamed from
	// openai-agents to openai-assistants for consistency". The
	// canonical $id (surfaced as Location, with a trailing JSON
	// Pointer fragment) and on-disk path both use the post-rename
	// name; neither string below is "openai-agents".
	if got := strings.TrimSuffix(schema.Location, "#"); got != "https://schemas.lenny.dev/runtime-options/openai-assistants/v1.json" {
		t.Errorf("schema $id = %q, want the openai-assistants v1 URL (post-rename name)", got)
	}

	cases := []struct {
		name    string
		doc     string
		wantErr bool
	}{
		{
			name:    "minimal valid: only the required assistantId",
			doc:     `{"assistantId":"asst_abc123"}`,
			wantErr: false,
		},
		{
			name:    "valid: every declared property set within bounds",
			doc:     `{"assistantId":"asst_abc123","model":"gpt-4.1","temperature":1.5,"parallelToolCalls":false,"responseFormat":"json_object"}`,
			wantErr: false,
		},
		{
			// spec: §14 line 204 — "required": ["assistantId"].
			name:    "missing required assistantId is rejected",
			doc:     `{"model":"gpt-4.1"}`,
			wantErr: true,
		},
		{
			// spec: §14 line 200 — temperature "minimum": 0, "maximum": 2.
			name:    "temperature above the maximum is rejected",
			doc:     `{"assistantId":"asst_abc123","temperature":2.1}`,
			wantErr: true,
		},
		{
			// spec: §14 line 200 — temperature "minimum": 0, "maximum": 2.
			name:    "temperature below the minimum is rejected",
			doc:     `{"assistantId":"asst_abc123","temperature":-0.1}`,
			wantErr: true,
		},
		{
			// spec: §14 line 202 — responseFormat "enum": ["text", "json_object", "json_schema"].
			name:    "responseFormat outside the enum is rejected",
			doc:     `{"assistantId":"asst_abc123","responseFormat":"xml"}`,
			wantErr: true,
		},
		{
			// spec: §14 line 205 — "additionalProperties": false.
			name:    "unknown top-level key is rejected",
			doc:     `{"assistantId":"asst_abc123","bogus":true}`,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var v any
			if err := json.Unmarshal([]byte(tc.doc), &v); err != nil {
				t.Fatalf("unmarshal fixture: %v", err)
			}
			err := schema.Validate(v)
			if tc.wantErr && err == nil {
				t.Errorf("doc %s: expected validation error, got none", tc.doc)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("doc %s: expected valid, got error: %v", tc.doc, err)
			}
		})
	}
}

// spec: §14 lines 286-297 (crewai runtimeOptions schema body:
// "crewModule": {"type":"string",...}, "process": {"type":"string",
// "enum":["sequential","hierarchical"],"default":"sequential"},
// "required": ["crewModule"], "additionalProperties": false); §26.11
// ("runtimeOptionsSchema at
// https://schemas.lenny.dev/runtime-options/crewai/v1.json —
// crewModule (required, Python dotted path to the Crew object),
// process (sequential|hierarchical), verbose.")
// diagnosis: a failure here means schemas/runtime-options/crewai/v1.json
// no longer matches the canonical schema body spec/14_workspace-plan-schema.md
// publishes for the crewai runtime.
func TestCrewAIRuntimeOptionsSchema(t *testing.T) {
	t.Parallel()
	schema := schematest.Compile(t, "schemas/runtime-options/crewai/v1.json")

	cases := []struct {
		name    string
		doc     string
		wantErr bool
	}{
		{
			name:    "minimal valid: only the required crewModule",
			doc:     `{"crewModule":"crew.research:build_crew"}`,
			wantErr: false,
		},
		{
			name:    "valid: every declared property set within bounds",
			doc:     `{"crewModule":"crew.research:build_crew","process":"hierarchical","verbose":true,"configSchema":{"foo":"bar"}}`,
			wantErr: false,
		},
		{
			// spec: §14 line 295 — "required": ["crewModule"].
			name:    "missing required crewModule is rejected",
			doc:     `{"process":"sequential"}`,
			wantErr: true,
		},
		{
			// spec: §14 line 291 — process "enum": ["sequential", "hierarchical"].
			name:    "process outside the enum is rejected",
			doc:     `{"crewModule":"crew.research:build_crew","process":"parallel"}`,
			wantErr: true,
		},
		{
			// spec: §14 line 296 — "additionalProperties": false.
			name:    "unknown top-level key is rejected",
			doc:     `{"crewModule":"crew.research:build_crew","bogus":true}`,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var v any
			if err := json.Unmarshal([]byte(tc.doc), &v); err != nil {
				t.Fatalf("unmarshal fixture: %v", err)
			}
			err := schema.Validate(v)
			if tc.wantErr && err == nil {
				t.Errorf("doc %s: expected validation error, got none", tc.doc)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("doc %s: expected valid, got error: %v", tc.doc, err)
			}
		})
	}
}
