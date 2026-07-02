// SPDX-License-Identifier: MIT

// Command genopsschemas merges the §25 lenny-ops operability route schemas
// into the gateway-owned, embedded openapi.json so the single served OpenAPI
// document covers the entire operability surface (F-COV-3).
//
// The gateway builds openapi.json from its own Go type definitions, so the
// lenny-ops routes — served by a separate Deployment (pkg/ops/opsserver) with
// its own request/response types — are absent by construction. §25.12 requires
// the served document to cover "the entire operability surface — both gateway
// admin-API endpoints and lenny-ops's own endpoints", and the build-time
// openapi-to-mcp generator emits one MCP tool per operability route from that
// completed document. This step writes the lenny-ops path schemas into
// openapi.json from opsserver.RouteSchemas(), the schema-emission surface a
// package-internal drift guard pins to the served opsserver routes, so the
// merge is regenerable rather than hand-drifting.
//
// The generator inserts the lenny-ops path entries into the existing paths
// object without reformatting the hand-authored gateway entries: it emits each
// added path block in the same compact per-operation style the file already
// uses and splices it before the paths object's closing brace, so the diff is
// scoped to the added routes.
//
// Run it via `go generate ./pkg/gateway/externalapi/openapi/...` after adding
// or removing a lenny-ops route (and updating opsserver.RouteSchemas). The
// gateway completeness test (TestGatewayDocumentCoversLennyOpsRoutes) fails the
// build when the committed openapi.json omits a route the registry names.
//
// spec: §15.1 (OpenAPI document + x-lenny-* extensions), §25.12 (operability
// surface, OpenAPI→MCP generation), §18 (Phase 13 lenny-ops-schema merge).
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// outputFile is the gateway-embedded document, relative to the openapi package
// directory the go:generate directive runs from.
const outputFile = "openapi.json"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "genopsschemas:", err)
		os.Exit(1)
	}
}

func run() error {
	raw, err := os.ReadFile(outputFile)
	if err != nil {
		return fmt.Errorf("read %s: %w", outputFile, err)
	}

	// Parse the current path set so we add only the lenny-ops verbs that are
	// absent, and merge a new verb onto a path the gateway already documents
	// (e.g. GET /v1/admin/platform/config keeps its gateway entry while PUT is
	// added) rather than clobbering it.
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("decode %s: %w", outputFile, err)
	}
	paths, _ := doc["paths"].(map[string]any)
	if paths == nil {
		return fmt.Errorf("%s has no paths object", outputFile)
	}

	merged, err := insertOpsPaths(string(raw), paths, opsserver.RouteSchemas())
	if err != nil {
		return err
	}
	if err := os.WriteFile(outputFile, []byte(merged), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outputFile, err)
	}
	return nil
}

// insertOpsPaths splices the lenny-ops route blocks into the raw document
// text. It groups the registry by path, skips verbs already present, and for
// each affected path emits a compact block that carries every lenny-ops verb
// on that path plus (for a collision path) the gateway verbs already there.
// The block replaces the existing path entry when the path already exists, or
// is appended before the paths object's closing brace when it is new.
func insertOpsPaths(raw string, existing map[string]any, schemas []opsserver.RouteSchema) (string, error) {
	byPath := map[string][]opsserver.RouteSchema{}
	var order []string
	for _, r := range schemas {
		if item, ok := existing[r.Path].(map[string]any); ok {
			if _, present := item[strings.ToLower(r.Method)]; present {
				continue // verb already documented (e.g. the two /me GETs)
			}
		}
		if _, seen := byPath[r.Path]; !seen {
			order = append(order, r.Path)
		}
		byPath[r.Path] = append(byPath[r.Path], r)
	}
	sort.Strings(order)

	var appendBlocks []string
	for _, path := range order {
		if _, exists := existing[path]; exists {
			// A verb-merge onto an existing path. The single collision today is
			// PUT /v1/admin/platform/config; the gateway GET stays. Rewrite the
			// existing path block to add the lenny-ops verb.
			var err error
			raw, err = mergeVerbIntoExistingPath(raw, path, byPath[path])
			if err != nil {
				return "", err
			}
			continue
		}
		appendBlocks = append(appendBlocks, renderPathBlock(path, byPath[path]))
	}

	if len(appendBlocks) == 0 {
		return raw, nil
	}
	return spliceBeforePathsClose(raw, appendBlocks)
}

// spliceBeforePathsClose inserts the rendered path blocks just before the
// paths object's closing brace, after the last existing path entry.
func spliceBeforePathsClose(raw string, blocks []string) (string, error) {
	// The document ends with:
	//     }       <- last path entry close
	//   }         <- paths object close
	// }           <- document close
	// Find the paths-object close: the "\n  }\n}" tail.
	marker := "\n  }\n}"
	idx := strings.LastIndex(raw, marker)
	if idx < 0 {
		return "", fmt.Errorf("could not locate paths-object close marker in %s", outputFile)
	}
	// The character before the marker is the last path entry's close brace; add
	// a comma after it and insert the new blocks.
	head := raw[:idx]
	tail := raw[idx:]
	joined := strings.Join(blocks, ",\n")
	return head + ",\n" + joined + tail, nil
}

// mergeVerbIntoExistingPath rewrites the JSON block for an existing path so it
// gains the lenny-ops verbs, by re-decoding just that path entry, adding the
// verbs, and re-encoding the whole path block compactly. The rest of the file
// is untouched.
func mergeVerbIntoExistingPath(raw, path string, verbs []opsserver.RouteSchema) (string, error) {
	// Locate the path key. Path keys are quoted and unique.
	key := fmt.Sprintf("%q: {", path)
	start := strings.Index(raw, key)
	if start < 0 {
		return "", fmt.Errorf("path %q not found for verb merge", path)
	}
	// Walk to the matching close brace of the path object.
	braceStart := start + len(key) - 1
	end := matchBrace(raw, braceStart)
	if end < 0 {
		return "", fmt.Errorf("unbalanced braces for path %q", path)
	}
	var existingItem map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw[braceStart:end+1]), &existingItem); err != nil {
		return "", fmt.Errorf("decode existing path %q: %w", path, err)
	}
	block := renderPathBlockWithExisting(path, verbs, existingItem)
	// Replace from the path key start through the path close brace.
	return raw[:start] + strings.TrimPrefix(block, "    ") + raw[end+1:], nil
}

// matchBrace returns the index of the '}' matching the '{' at open, or -1.
func matchBrace(s string, open int) int {
	depth := 0
	inStr := false
	esc := false
	for i := open; i < len(s); i++ {
		c := s[i]
		if inStr {
			if esc {
				esc = false
			} else if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// renderPathBlock renders `"<path>": { <verbs> }` at the file's path-entry
// indentation (4 spaces) for a brand-new path.
func renderPathBlock(path string, verbs []opsserver.RouteSchema) string {
	return renderPathBlockWithExisting(path, verbs, nil)
}

// renderPathBlockWithExisting renders a path block that carries the lenny-ops
// verbs plus any gateway verbs already present (preserved verbatim). Verbs are
// emitted in canonical HTTP order.
func renderPathBlockWithExisting(path string, verbs []opsserver.RouteSchema, existing map[string]json.RawMessage) string {
	ops := map[string]string{}
	for k, v := range existing {
		var buf bytes.Buffer
		_ = json.Indent(&buf, v, "      ", "  ")
		ops[k] = buf.String()
	}
	for _, r := range verbs {
		ops[strings.ToLower(r.Method)] = renderOperation(r)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "    %q: {\n", path)
	keys := make([]string, 0, len(ops))
	for k := range ops {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return verbRank(keys[i]) < verbRank(keys[j]) })
	for i, k := range keys {
		fmt.Fprintf(&b, "      %q: %s", k, ops[k])
		if i < len(keys)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString("    }")
	return b.String()
}

// renderOperation emits one operation object body indented to sit under a verb
// key at 6-space indentation, carrying the four mandatory x-lenny-* extensions.
func renderOperation(r opsserver.RouteSchema) string {
	var mcpTool any
	if r.MCPTool != "" {
		mcpTool = r.MCPTool
	}
	mcpJSON, _ := json.Marshal(mcpTool)
	var b strings.Builder
	b.WriteString("{\n")
	fmt.Fprintf(&b, "        \"operationId\": %q,\n", operationID(r))
	fmt.Fprintf(&b, "        \"summary\": %q,\n", r.Summary)
	b.WriteString("        \"security\": [{ \"BearerAuth\": [] }],\n")
	fmt.Fprintf(&b, "        \"x-lenny-mcp-tool\": %s,\n", mcpJSON)
	fmt.Fprintf(&b, "        \"x-lenny-scope\": %q,\n", r.Scope)
	fmt.Fprintf(&b, "        \"x-lenny-required-role\": %q,\n", r.RequiredRole)
	fmt.Fprintf(&b, "        \"x-lenny-category\": %q,\n", r.RequiredCategory)
	fmt.Fprintf(&b, "        \"responses\": { %q: { \"description\": %q } }\n",
		r.SuccessStatus, statusDescription(r.SuccessStatus))
	b.WriteString("      }")
	return b.String()
}

// verbRank orders HTTP verbs canonically (get, post, put, patch, delete).
func verbRank(v string) int {
	switch v {
	case "get":
		return 0
	case "post":
		return 1
	case "put":
		return 2
	case "patch":
		return 3
	case "delete":
		return 4
	default:
		return 5
	}
}

// operationID derives a stable camelCase operationId from the verb and path,
// e.g. GET /v1/admin/runbooks/{name}/steps → getV1AdminRunbooksNameSteps.
func operationID(r opsserver.RouteSchema) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(r.Method))
	word := true
	for _, c := range r.Path {
		switch c {
		case '/', '-', '{', '}':
			word = true
		default:
			if word {
				b.WriteRune(upperRune(c))
				word = false
			} else {
				b.WriteRune(c)
			}
		}
	}
	return b.String()
}

func statusDescription(code string) string {
	switch code {
	case "201":
		return "Created"
	case "202":
		return "Accepted"
	case "204":
		return "No Content"
	default:
		return "OK"
	}
}

func upperRune(c rune) rune {
	if c >= 'a' && c <= 'z' {
		return c - ('a' - 'A')
	}
	return c
}
