//go:build contract

// SPDX-License-Identifier: MIT

package adapter_jsonl_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// compileJSONL compiles the published CH-MSGSOCK JSONL schema with the
// MessagePart schema resolved locally, the way a runtime author's
// validator resolves it from the published artifacts.
func compileJSONL(t *testing.T) *jsonschema.Schema {
	t.Helper()
	c := schematest.NewCompiler(t)
	schematest.MustAddLocalSchema(t, c, "https://schemas.lenny.dev/messagepart/v1.json", "schemas/messagepart.schema.json")
	return schematest.MustCompile(t, c, "schemas/lenny-adapter-jsonl.schema.json")
}

// validateFrame validates one JSONL line against the compiled schema.
func validateFrame(t *testing.T, schema *jsonschema.Schema, raw string) error {
	t.Helper()
	var doc any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("frame is not JSON: %v (%s)", err, raw)
	}
	return schema.Validate(doc)
}

// spec: 28.5.3 (CH-MSGSOCK outbound set_tracing_context schema), 15.4 (published wire artifacts)
// diagnosis: the published JSONL schema rejected a conforming
//
//	set_tracing_context frame carrying a slotId. The adapter
//	addresses the frame by the slot it names, so a schema that
//	does not declare slotId makes the addressed form
//	unpublishable.
func TestSetTracingContextFrameCarriesSlotID(t *testing.T) {
	t.Parallel()
	schema := compileJSONL(t)

	example := schematest.ReadJSON(t, filepath.Join(schematest.RepoRoot(t), "schemas/examples/jsonl.set_tracing_context.json"))
	if err := schema.Validate(example); err != nil {
		t.Errorf("published set_tracing_context example failed the JSONL schema: %v", err)
	}

	for name, frame := range map[string]string{
		"untagged": `{"type":"set_tracing_context","context":{"langsmith_run_id":"run_abc123"}}`,
		"tagged":   `{"type":"set_tracing_context","context":{"langsmith_run_id":"run_abc123"},"slotId":"slot_01"}`,
		"empty":    `{"type":"set_tracing_context","context":{"langsmith_run_id":"run_abc123"},"slotId":""}`,
	} {
		if err := validateFrame(t, schema, frame); err != nil {
			t.Errorf("%s set_tracing_context frame failed the JSONL schema: %v\n  payload: %s", name, err, frame)
		}
	}
}

// spec: 28.5.3 (CH-MSGSOCK outbound set_tracing_context schema)
// diagnosis: the published JSONL schema accepted a set_tracing_context
//
//	frame whose slotId is not a string. The adapter compares the
//	frame's slotId to the delivering stream's slotId as exact
//	string equality and reads any value it cannot decode as a
//	string as no slotId at all, so a non-string value silently
//	becomes an untagged frame that a concurrent pod drops. The
//	schema is where that authoring mistake is caught.
func TestSetTracingContextRejectsNonStringSlotID(t *testing.T) {
	t.Parallel()
	schema := compileJSONL(t)

	for name, frame := range map[string]string{
		"number": `{"type":"set_tracing_context","context":{"langsmith_run_id":"run_abc123"},"slotId":1}`,
		"null":   `{"type":"set_tracing_context","context":{"langsmith_run_id":"run_abc123"},"slotId":null}`,
		"object": `{"type":"set_tracing_context","context":{"langsmith_run_id":"run_abc123"},"slotId":{"id":"slot_01"}}`,
	} {
		if err := validateFrame(t, schema, frame); err == nil {
			t.Errorf("%s slotId validated against the JSONL schema, want rejection: %s", name, frame)
		}
	}
}
