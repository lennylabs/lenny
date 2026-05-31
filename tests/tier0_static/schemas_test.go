// SPDX-License-Identifier: MIT

// Package tier0_static exercises Tier 0 of the test taxonomy
// (see TESTING.md §12.0): lint, schema validation, and traceability
// checks. Phase 1 lands the schema-validation tests.
//
// These tests run as `go test ./tests/tier0_static/...` and are also
// dispatched by `lenny-test --tier static`.
//
// The jsonschema-using helpers live in pkg/internal/schematest so
// the import is anchored outside *_test.go files (a golangci-lint 1.61
// typecheck quirk).
package tier0_static

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: 14, 14.1, 15.4
// diagnosis: A schema file under schemas/ does not parse as JSON Schema
//
//	2020-12. Run `cat schemas/<file>` and check for syntax errors.
//	Most likely cause: a trailing comma or unquoted key.
func TestPhase1SchemasParse(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	cases := []string{
		"schemas/outputpart.schema.json",
		"schemas/lenny-adapter-jsonl.schema.json",
		"schemas/workspaceplan-v1.json",
		"schemas/lifecycle-events.schema.json",
	}

	for _, rel := range cases {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			t.Parallel()
			full := filepath.Join(root, rel)
			data, err := os.ReadFile(full)
			if err != nil {
				t.Fatalf("read %s: %v", rel, err)
			}
			var parsed map[string]any
			if err := json.Unmarshal(data, &parsed); err != nil {
				t.Fatalf("parse %s: %v", rel, err)
			}
			if parsed["$schema"] == nil {
				t.Errorf("%s missing $schema", rel)
			}
			if parsed["$id"] == nil {
				t.Errorf("%s missing $id", rel)
			}
		})
	}
}

// spec: 14, 14.1
// diagnosis: schemas/examples/workspaceplan.*.json failed to validate
//
//	against schemas/workspaceplan-v1.json. Either the example
//	is wrong or the schema rejects a shape it should accept.
func TestWorkspacePlanExamplesValidate(t *testing.T) {
	t.Parallel()
	validator := schematest.Compile(t, "schemas/workspaceplan-v1.json")

	root := schematest.RepoRoot(t)
	examples := []struct {
		path        string
		expectValid bool
	}{
		{"schemas/examples/workspaceplan.minimal.json", true},
		{"schemas/examples/workspaceplan.full.json", true},
		{"schemas/examples/workspaceplan.invalid-setuid.json", false},
		{"schemas/examples/workspaceplan.invalid-ssh.json", false},
		// spec: §14 line 336 — the published schema MUST use allOf +
		// per-variant if/then so an unknown source.type matches no branch
		// and passes (the consumer skips it per the open-string
		// discriminator). A oneOf-based schema would reject this. F-14.1.4.
		{"schemas/examples/workspaceplan.unknown-source-type.json", true},
		// spec: §14 line 336 — per-variant field strictness: a known type
		// carrying an unknown field is still rejected (the variant subschema
		// keeps additionalProperties:false under the allOf construction).
		// F-14.1.4.
		{"schemas/examples/workspaceplan.invalid-unknown-field.json", false},
	}

	for _, ex := range examples {
		ex := ex
		t.Run(filepath.Base(ex.path), func(t *testing.T) {
			t.Parallel()
			data := schematest.ReadJSON(t, filepath.Join(root, ex.path))
			err := validator.Validate(data)
			if ex.expectValid && err != nil {
				t.Errorf("expected %s to validate, got: %v", ex.path, err)
			}
			if !ex.expectValid && err == nil {
				t.Errorf("expected %s to fail validation but it passed; the schema is missing a constraint", ex.path)
			}
		})
	}
}

// spec: 15.4
// diagnosis: schemas/examples/outputpart.*.json failed to validate
//
//	against schemas/outputpart.schema.json.
func TestOutputPartExamplesValidate(t *testing.T) {
	t.Parallel()
	validator := schematest.Compile(t, "schemas/outputpart.schema.json")

	root := schematest.RepoRoot(t)
	for _, name := range []string{
		"schemas/examples/outputpart.text.json",
		"schemas/examples/outputpart.code.json",
		"schemas/examples/outputpart.image_ref.json",
	} {
		name := name
		t.Run(filepath.Base(name), func(t *testing.T) {
			t.Parallel()
			data := schematest.ReadJSON(t, filepath.Join(root, name))
			if err := validator.Validate(data); err != nil {
				t.Errorf("expected %s to validate, got: %v", name, err)
			}
		})
	}
}

// spec: 15.4.3, 15.4.6
// diagnosis: a lifecycle-events example failed to validate against
//
//	schemas/lifecycle-events.schema.json. Verify the envelope
//	shape, the `type` discriminator, and the capability enum.
func TestLifecycleEventExamplesValidate(t *testing.T) {
	t.Parallel()
	validator := schematest.Compile(t, "schemas/lifecycle-events.schema.json")

	root := schematest.RepoRoot(t)
	for _, name := range []string{
		"schemas/examples/lifecycle.lifecycle_capabilities.json",
		"schemas/examples/lifecycle.lifecycle_support.json",
		"schemas/examples/lifecycle.checkpoint_request.json",
		"schemas/examples/lifecycle.checkpoint_ready.json",
		"schemas/examples/lifecycle.checkpoint_complete.json",
		"schemas/examples/lifecycle.interrupt_request.json",
		"schemas/examples/lifecycle.interrupt_acknowledged.json",
		"schemas/examples/lifecycle.credentials_rotated.json",
		"schemas/examples/lifecycle.deadline_approaching.json",
		"schemas/examples/lifecycle.terminate.json",
		"schemas/examples/lifecycle.task_complete.json",
	} {
		name := name
		t.Run(filepath.Base(name), func(t *testing.T) {
			t.Parallel()
			data := schematest.ReadJSON(t, filepath.Join(root, name))
			if err := validator.Validate(data); err != nil {
				t.Errorf("expected %s to validate, got: %v", name, err)
			}
		})
	}
}

// spec: 15.4
// diagnosis: an adapter JSONL example failed to validate against
//
//	schemas/lenny-adapter-jsonl.schema.json. Verify the
//	envelope shape and the `type` discriminator.
func TestAdapterJSONLExamplesValidate(t *testing.T) {
	t.Parallel()
	// The JSONL schema $refs the OutputPart schema by its $id URL.
	// Wire the compiler to resolve that URL locally.
	c := schematest.NewCompiler(t)
	schematest.MustAddLocalSchema(t, c, "https://schemas.lenny.dev/outputpart/v1.json", "schemas/outputpart.schema.json")
	jsonlSchema := schematest.MustCompile(t, c, "schemas/lenny-adapter-jsonl.schema.json")

	root := schematest.RepoRoot(t)
	for _, name := range []string{
		"schemas/examples/jsonl.message.json",
		"schemas/examples/jsonl.heartbeat.json",
		"schemas/examples/jsonl.tool_call.json",
		"schemas/examples/jsonl.response.json",
		"schemas/examples/jsonl.task_complete.json",
		"schemas/examples/jsonl.task_complete_acknowledged.json",
		"schemas/examples/jsonl.task_ready.json",
	} {
		name := name
		t.Run(filepath.Base(name), func(t *testing.T) {
			t.Parallel()
			data := schematest.ReadJSON(t, filepath.Join(root, name))
			if err := jsonlSchema.Validate(data); err != nil {
				t.Errorf("expected %s to validate, got: %v", name, err)
			}
		})
	}
}
