// SPDX-License-Identifier: MIT

package tier0_static

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// apiGenerator is one code generator whose committed output a REST or MCP
// surface change must regenerate. pkg is the generator's import path relative
// to the module root, output is the committed file relative to the module
// root, and workRel is the directory, relative to a scratch module root, that
// the generator runs in and writes output into under the generator's own
// file name.
type apiGenerator struct {
	name    string
	pkg     string
	output  string
	workRel string
	seed    bool
}

// apiGenerators are the generators of the served OpenAPI document, the
// lenny-ops MCP tool inventory, and the gateway MCP tool input schemas.
var apiGenerators = []apiGenerator{
	{
		name:    "genopsschemas",
		pkg:     "./pkg/gateway/externalapi/openapi/internal/genopsschemas",
		output:  "pkg/gateway/externalapi/openapi/openapi.json",
		workRel: "pkg/gateway/externalapi/openapi",
		// The generator merges into the committed document, so it reads
		// the committed copy and must leave it byte-identical.
		seed: true,
	},
	{
		name:    "openapi-to-mcp",
		pkg:     "./cmd/openapi-to-mcp",
		output:  "pkg/ops/mcp/generated_tools.go",
		workRel: "",
	},
	{
		name:    "genmcpschemas",
		pkg:     "./pkg/gateway/mcpfabric/mcptools/internal/genmcpschemas",
		output:  "pkg/gateway/mcpfabric/mcptools/generated_schemas.go",
		workRel: "pkg/gateway/mcpfabric/mcptools",
	},
}

// TestAPICodegenMatchesCommittedOutput runs each REST and MCP generator into
// a scratch module root and requires the committed output to equal a fresh
// run byte for byte. A difference means a REST or MCP surface change, such as
// the deletion of sessionPolicy.maxSessionRetries or of the RESUME_FAILED
// error code, landed without regenerating openapi.json, generated_tools.go,
// or generated_schemas.go.
//
// spec: §15.1 (REST API), §7.3 (Retry and Resume)
func TestAPICodegenMatchesCommittedOutput(t *testing.T) {
	root := schematest.RepoRoot(t)
	bin := t.TempDir()
	args := []string{"build", "-o", bin + string(os.PathSeparator)}
	for _, g := range apiGenerators {
		args = append(args, g.pkg)
	}
	build := exec.Command("go", args...)
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the generators: %v\n%s", err, out)
	}
	for _, g := range apiGenerators {
		t.Run(g.name, func(t *testing.T) {
			committed, err := os.ReadFile(filepath.Join(root, g.output))
			if err != nil {
				t.Fatalf("read %s: %v", g.output, err)
			}
			got := runGenerator(t, filepath.Join(bin, g.name), g, committed)
			if !bytes.Equal(got, committed) {
				t.Errorf("the committed %s differs from a fresh %s run; regenerate it with go generate and commit the result", g.output, g.name)
			}
		})
	}
}

// runGenerator runs the generator binary in a scratch module root holding a
// go.mod, seeded with the committed output when the generator merges into
// it, and returns the file it wrote.
func runGenerator(t *testing.T, binary string, g apiGenerator, committed []byte) []byte {
	t.Helper()
	scratch := t.TempDir()
	if err := os.WriteFile(filepath.Join(scratch, "go.mod"), []byte("module scratch\n"), 0o644); err != nil {
		t.Fatalf("write scratch go.mod: %v", err)
	}
	outPath := filepath.Join(scratch, g.output)
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		t.Fatalf("make %s: %v", filepath.Dir(outPath), err)
	}
	if g.seed {
		if err := os.WriteFile(outPath, committed, 0o644); err != nil {
			t.Fatalf("seed %s: %v", g.output, err)
		}
	}
	run := exec.Command(binary)
	run.Dir = filepath.Join(scratch, g.workRel)
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("run %s: %v\n%s", g.name, err, out)
	}
	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read the generated %s: %v", g.output, err)
	}
	return got
}
