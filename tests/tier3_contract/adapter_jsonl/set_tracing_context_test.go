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
//	set_tracing_context frame carrying a sessionId. The frame is
//	addressed to the session that sessionId names, so a schema
//	that does not declare sessionId makes the addressed form
//	unpublishable. The untagged form stays valid because a runtime
//	on a pod holding at most one slot may omit the identifier and
//	have it resolve to the receiving stream's own binding.
func TestSetTracingContextFrameCarriesSessionID(t *testing.T) {
	t.Parallel()
	schema := compileJSONL(t)

	example := schematest.ReadJSON(t, filepath.Join(schematest.RepoRoot(t), "schemas/examples/jsonl.set_tracing_context.json"))
	if err := schema.Validate(example); err != nil {
		t.Errorf("published set_tracing_context example failed the JSONL schema: %v", err)
	}

	for name, frame := range map[string]string{
		"untagged": `{"type":"set_tracing_context","context":{"langsmith_run_id":"run_abc123"}}`,
		"tagged":   `{"type":"set_tracing_context","context":{"langsmith_run_id":"run_abc123"},"sessionId":"sess_abc123"}`,
	} {
		if err := validateFrame(t, schema, frame); err != nil {
			t.Errorf("%s set_tracing_context frame failed the JSONL schema: %v\n  payload: %s", name, err, frame)
		}
	}
}

// spec: 28.5.3 (CH-MSGSOCK outbound set_tracing_context schema)
// diagnosis: the published JSONL schema accepted a set_tracing_context
//
//	frame whose sessionId is not a non-empty string. The adapter compares
//	the frame's sessionId to the delivering stream's session as exact
//	string equality and reads any value it cannot decode as a string as no
//	identifier at all, so a non-string value silently becomes an
//	unaddressed frame that a pod holding more than one slot rejects. An
//	empty string is a third case the addressing rule leaves undefined: it
//	is present, so it does not resolve to the receiving stream's binding on
//	a single-slot pod, and it equals no session, so it is relayed nowhere.
//	The schema admits an address or its absence and nothing between them.
func TestSetTracingContextRejectsNonStringOrEmptySessionID(t *testing.T) {
	t.Parallel()
	schema := compileJSONL(t)

	for name, frame := range map[string]string{
		"number": `{"type":"set_tracing_context","context":{"langsmith_run_id":"run_abc123"},"sessionId":1}`,
		"null":   `{"type":"set_tracing_context","context":{"langsmith_run_id":"run_abc123"},"sessionId":null}`,
		"object": `{"type":"set_tracing_context","context":{"langsmith_run_id":"run_abc123"},"sessionId":{"id":"sess_abc123"}}`,
		"empty":  `{"type":"set_tracing_context","context":{"langsmith_run_id":"run_abc123"},"sessionId":""}`,
	} {
		if err := validateFrame(t, schema, frame); err == nil {
			t.Errorf("%s sessionId validated against the JSONL schema, want rejection: %s", name, frame)
		}
	}
}
