//go:build contract

// SPDX-License-Identifier: MIT

// Package runtimeoptionsschema_test is the Tier 3 contract suite for the
// published langgraph and mastra runtimeOptions JSON Schema documents
// (schemas/runtime-options/langgraph/v1.json and
// schemas/runtime-options/mastra/v1.json). Section 26.8 and 26.9 of
// spec/26_reference-runtime-catalog.md declare these runtimes'
// runtimeOptionsSchema as a $ref to
// https://schemas.lenny.dev/runtime-options/<runtime-name>/v1.json, and
// spec/14_workspace-plan-schema.md inlines the canonical schema body each
// URL is published from. This suite validates the on-disk documents
// against that canonical body: the required field, the enum, and the
// numeric bounds each reject the payload the spec describes, and
// additionalProperties: false rejects an unknown top-level key.
package runtimeoptionsschema_test

import (
	"encoding/json"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: §14 lines 176-188 (langgraph runtimeOptions schema body); §26.8
// ("runtimeOptionsSchema already in §14 ... graphModule (required),
// checkpointBackend, recursionLimit, configSchema")
// diagnosis: a failure here means schemas/runtime-options/langgraph/v1.json
// no longer matches the canonical schema body spec/14_workspace-plan-schema.md
// publishes for the langgraph runtime, so a client validating locally
// against the published URL and the gateway's own §14 line 155
// session-creation validation would diverge.
func TestLanggraphRuntimeOptionsSchema(t *testing.T) {
	t.Parallel()
	schema := schematest.Compile(t, "schemas/runtime-options/langgraph/v1.json")

	cases := []struct {
		name    string
		doc     string
		wantErr bool
	}{
		{
			name:    "minimal valid: only the required graphModule",
			doc:     `{"graphModule":"agents.graph:build"}`,
			wantErr: false,
		},
		{
			name:    "valid: every declared property set within bounds",
			doc:     `{"graphModule":"agents.graph:build","checkpointBackend":"redis","recursionLimit":500,"configSchema":{"foo":"bar"}}`,
			wantErr: false,
		},
		{
			// spec: §14 line 187 — "required": ["graphModule"].
			name:    "missing required graphModule is rejected",
			doc:     `{"checkpointBackend":"postgres"}`,
			wantErr: true,
		},
		{
			// spec: §14 line 184 — recursionLimit "minimum": 1, "maximum": 500.
			name:    "recursionLimit above the maximum is rejected",
			doc:     `{"graphModule":"agents.graph:build","recursionLimit":501}`,
			wantErr: true,
		},
		{
			// spec: §14 line 184 — recursionLimit "minimum": 1, "maximum": 500.
			name:    "recursionLimit below the minimum is rejected",
			doc:     `{"graphModule":"agents.graph:build","recursionLimit":0}`,
			wantErr: true,
		},
		{
			// spec: §14 line 183 — checkpointBackend "enum": ["memory", "postgres", "redis"].
			name:    "checkpointBackend outside the enum is rejected",
			doc:     `{"graphModule":"agents.graph:build","checkpointBackend":"sqlite"}`,
			wantErr: true,
		},
		{
			// spec: §14 line 188 — "additionalProperties": false.
			name:    "unknown top-level key is rejected",
			doc:     `{"graphModule":"agents.graph:build","bogus":true}`,
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

// spec: §14 lines 270-281 (mastra runtimeOptions schema body); §26.9
// ("runtimeOptionsSchema at https://schemas.lenny.dev/runtime-options/mastra/v1.json
// — loads a Mastra agent definition by module path")
// diagnosis: a failure here means schemas/runtime-options/mastra/v1.json no
// longer matches the canonical schema body spec/14_workspace-plan-schema.md
// publishes for the mastra runtime.
func TestMastraRuntimeOptionsSchema(t *testing.T) {
	t.Parallel()
	schema := schematest.Compile(t, "schemas/runtime-options/mastra/v1.json")

	cases := []struct {
		name    string
		doc     string
		wantErr bool
	}{
		{
			name:    "minimal valid: only the required agentModule",
			doc:     `{"agentModule":"src/agent.ts"}`,
			wantErr: false,
		},
		{
			name:    "valid: agentModule plus configSchema",
			doc:     `{"agentModule":"src/agent.ts","configSchema":{"foo":"bar"}}`,
			wantErr: false,
		},
		{
			// spec: §14 line 279 — "required": ["agentModule"].
			name:    "missing required agentModule is rejected",
			doc:     `{"configSchema":{}}`,
			wantErr: true,
		},
		{
			// spec: §14 line 280 — "additionalProperties": false.
			name:    "unknown top-level key is rejected",
			doc:     `{"agentModule":"src/agent.ts","bogus":true}`,
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
