// Package tier0_static_test exercises Tier 0 of the test taxonomy
// (see TESTING.md §12.0): lint, schema validation, and traceability
// checks. Phase 1 lands the schema-validation tests.
//
// These tests run as `go test ./tests/tier0_static/...` and are also
// dispatched by `lenny-test --tier static`.
package tier0_static_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// spec: 14, 14.1, 15.4
// diagnosis: A schema file under schemas/ does not parse as JSON Schema
//
//	2020-12. Run `cat schemas/<file>` and check for syntax errors.
//	Most likely cause: a trailing comma or unquoted key.
func TestPhase1SchemasParse(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	cases := []string{
		"schemas/outputpart.schema.json",
		"schemas/lenny-adapter-jsonl.schema.json",
		"schemas/workspaceplan-v1.json",
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
	validator := compileSchema(t, "schemas/workspaceplan-v1.json")

	root := repoRoot(t)
	examples := []struct {
		path        string
		expectValid bool
	}{
		{"schemas/examples/workspaceplan.minimal.json", true},
		{"schemas/examples/workspaceplan.full.json", true},
		{"schemas/examples/workspaceplan.invalid-setuid.json", false},
		{"schemas/examples/workspaceplan.invalid-ssh.json", false},
	}

	for _, ex := range examples {
		ex := ex
		t.Run(filepath.Base(ex.path), func(t *testing.T) {
			t.Parallel()
			data := readJSON(t, filepath.Join(root, ex.path))
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
	validator := compileSchema(t, "schemas/outputpart.schema.json")

	root := repoRoot(t)
	for _, name := range []string{
		"schemas/examples/outputpart.text.json",
		"schemas/examples/outputpart.code.json",
		"schemas/examples/outputpart.image_ref.json",
	} {
		name := name
		t.Run(filepath.Base(name), func(t *testing.T) {
			t.Parallel()
			data := readJSON(t, filepath.Join(root, name))
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
	c := newCompiler(t)
	mustAddLocalSchema(t, c, "https://schemas.lenny.dev/outputpart/v1.json", "schemas/outputpart.schema.json")
	jsonlSchema := mustCompile(t, c, "schemas/lenny-adapter-jsonl.schema.json")

	root := repoRoot(t)
	for _, name := range []string{
		"schemas/examples/jsonl.message.json",
		"schemas/examples/jsonl.heartbeat.json",
		"schemas/examples/jsonl.tool_call.json",
		"schemas/examples/jsonl.response.json",
	} {
		name := name
		t.Run(filepath.Base(name), func(t *testing.T) {
			t.Parallel()
			data := readJSON(t, filepath.Join(root, name))
			if err := jsonlSchema.Validate(data); err != nil {
				t.Errorf("expected %s to validate, got: %v", name, err)
			}
		})
	}
}

// helpers

func compileSchema(t *testing.T, rel string) *jsonschema.Schema {
	t.Helper()
	c := newCompiler(t)
	return mustCompile(t, c, rel)
}

func newCompiler(t *testing.T) *jsonschema.Compiler {
	t.Helper()
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	return c
}

func mustCompile(t *testing.T, c *jsonschema.Compiler, rel string) *jsonschema.Schema {
	t.Helper()
	full := filepath.Join(repoRoot(t), rel)
	s, err := c.Compile(full)
	if err != nil {
		t.Fatalf("compile %s: %v", rel, err)
	}
	return s
}

func mustAddLocalSchema(t *testing.T, c *jsonschema.Compiler, url, rel string) {
	t.Helper()
	full := filepath.Join(repoRoot(t), rel)
	f, err := os.Open(full)
	if err != nil {
		t.Fatalf("open %s: %v", rel, err)
	}
	defer f.Close()
	if err := c.AddResource(url, f); err != nil {
		t.Fatalf("AddResource(%s): %v", url, err)
	}
}

func readJSON(t *testing.T, path string) any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return v
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for d := wd; d != "/" && d != ""; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	t.Fatalf("could not find repo root containing go.mod from %s", wd)
	return ""
}

var _ = strings.HasPrefix // silence unused-import on minimal builds
