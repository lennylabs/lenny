// SPDX-License-Identifier: MIT

// Command openapi-to-mcp is the §25.12 build-time generator that derives the
// `/mcp/management` MCP tool inventory from the gateway-served OpenAPI
// document.
//
// §25.12 mandates the inventory be generated, not hand-maintained: "a single
// build-time `openapi-to-mcp` step produces the tool inventory from the
// OpenAPI" and "every admin-API endpoint with documented RBAC becomes an MCP
// tool automatically via the build-time OpenAPI → MCP generation". This
// binary is that step. It mirrors the §15.2 `genmcpschemas` generator for the
// gateway MCP surface: it reads `openapi.Document()`, emits one tool per
// documented operability endpoint carrying `x-lenny-mcp-tool`, and writes the
// committed `pkg/ops/mcp/generated_tools.go`. The tier-0/3 drift guard
// (TestGeneratedToolsMatchOpenAPI) fails the build when the committed output
// diverges from a fresh generation, so any openapi.json edit without a regen
// fails the build.
//
// Run it via `go run ./cmd/openapi-to-mcp` (wired into `make generate`) after
// editing openapi.json or the tool classification table.
//
// spec: §25.12 (build-time openapi-to-mcp generation), §15.1 (OpenAPI x-lenny-*
// contract), §18 (Phase 13 openapi-to-mcp generator).
package main

import (
	"fmt"
	"go/format"
	"os"
	"path/filepath"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/openapi"
	"github.com/lennylabs/lenny/pkg/ops/mcp/mcptoolgen"
)

// outputRel is the committed generated inventory path relative to the module
// root. The generator resolves the module root itself (moduleRoot) so it
// writes the same file whether invoked from the module root (make generate)
// or the package directory (go generate ./pkg/ops/mcp/...).
const outputRel = "pkg/ops/mcp/generated_tools.go"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "openapi-to-mcp:", err)
		os.Exit(1)
	}
}

func run() error {
	tools, err := mcptoolgen.Generate(openapi.Document())
	if err != nil {
		return fmt.Errorf("generate tool inventory: %w", err)
	}
	src, err := mcptoolgen.RenderSource(tools)
	if err != nil {
		return fmt.Errorf("render generated source: %w", err)
	}
	formatted, err := format.Source(src)
	if err != nil {
		return fmt.Errorf("gofmt generated source: %w\n%s", err, src)
	}
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	outputFile := filepath.Join(root, outputRel)
	if err := os.WriteFile(outputFile, formatted, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outputFile, err)
	}
	fmt.Fprintf(os.Stderr, "openapi-to-mcp: wrote %d tool(s) to %s\n", len(tools), outputFile)
	return nil
}

// moduleRoot walks up from the working directory to the directory holding
// go.mod, so the generator resolves the same output path regardless of the
// caller's working directory.
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod found above %s", dir)
		}
		dir = parent
	}
}
