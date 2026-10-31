// SPDX-License-Identifier: MIT

// Tier-11 reconciliation of the reader-facing upload and finalize
// documentation with the plan-binding contract.
//
// A session binds one WorkspacePlan, at creation or, when creation carried
// none, in the optional body of POST /v1/sessions/{id}/finalize. An upload
// is sent as the raw request body with the upload token header the OpenAPI
// document names, and its 201 response carries an opaque uploadRef that a
// source of the plan bound to the same session names. Finalize
// materializes only the uploads the bound plan names. The pages under
// docs/ restate that contract in prose and in examples, and each of the
// sweeps below catches one way a page falls back to the retired
// multipart, create-time-only description.
//
// spec: 14.1 (WorkspacePlan Schema Versioning), 14 (Workspace Plan Schema), 15.1 (REST API), 15.2 (MCP API), 7.4 (Upload Safety)

package tier11_docs_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// openAPIDocumentPath is the authoritative description of the upload
// request header and the UploadResponse fields.
var openAPIDocumentPath = []string{"pkg", "gateway", "externalapi", "openapi", "openapi.json"}

// mcpClientToolsSourcePath holds the hand-written input schemas of the
// per-session MCP tools the MCP API page documents.
var mcpClientToolsSourcePath = []string{"pkg", "gateway", "mcpfabric", "mcptools", "client_tools.go"}

// mcpGeneratedSchemasSourcePath holds the MCP tool input schemas generated from
// the OpenAPI document.
var mcpGeneratedSchemasSourcePath = []string{"pkg", "gateway", "mcpfabric", "mcptools", "generated_schemas.go"}

// planBindRejectionTokens are the plan-bind rejections a finalize request
// can return for its plan. Every page that documents the finalize body
// names each of them.
var planBindRejectionTokens = []string{
	"plan_already_set",
	"invalid_upload_ref",
	"upload_ref_foreign_session",
	"WORKSPACE_PLAN_INVALID",
}

// uploadTokenHeaderPattern matches any spelling of an upload token header,
// so a page that invents or keeps a retired header name is caught
// alongside one that keeps the current name.
var uploadTokenHeaderPattern = regexp.MustCompile(`X-[A-Za-z-]*Upload-Token`)

// literalUploadRefPattern matches an example uploadRef written as a
// concrete-looking identifier rather than the <uploadRef> placeholder.
var literalUploadRefPattern = regexp.MustCompile(`uploadRef"?\s*[:=]\s*"upload_[A-Za-z0-9]+"`)

// multipartUploadPattern matches the retired multipart upload request in
// a curl or requests example.
var multipartUploadPattern = regexp.MustCompile(`-F\s+"files=@|files=\{"files"`)

// finalizeRequestPattern matches a line that issues a finalize request: a
// curl command, a raw HTTP request line, or a Python requests call. A
// diagram edge label that names the endpoint is not a request.
var finalizeRequestPattern = regexp.MustCompile(`(?m)(curl\b.*/finalize|^POST /v1/sessions/\S+/finalize\s*$|requests\.post\(.*/finalize)`)

// fencedCodeBlocks returns the body of each fenced code block in page,
// splitting on lines that open or close a fence so an inline backtick run
// in prose never pairs with a fence.
func fencedCodeBlocks(page string) []string {
	var blocks []string
	var current []string
	inside := false
	for _, line := range strings.Split(page, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if inside {
				blocks = append(blocks, strings.Join(current, "\n"))
				current = nil
			}
			inside = !inside
			continue
		}
		if inside {
			current = append(current, line)
		}
	}
	return blocks
}

// docsMarkdownPages returns every markdown page under docs/, keyed by its
// repository-relative path. ADRs record a decision at a point in time and
// are not held to the current contract.
func docsMarkdownPages(t *testing.T, root string) map[string]string {
	t.Helper()
	pages := map[string]string{}
	docsRoot := filepath.Join(root, "docs")
	err := filepath.WalkDir(docsRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "adr" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		pages[rel] = string(body)
		return nil
	})
	if err != nil {
		t.Fatalf("walk docs: %v", err)
	}
	if len(pages) == 0 {
		t.Fatal("no markdown pages found under docs/")
	}
	return pages
}

// openAPIUploadTokenHeader returns the name of the required header
// parameter that carries the upload token on the given upload path.
func openAPIUploadTokenHeader(t *testing.T, root, path string) string {
	t.Helper()
	var doc struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name string `json:"name"`
				In   string `json:"in"`
			} `json:"parameters"`
		} `json:"paths"`
	}
	if err := json.Unmarshal([]byte(readRepoFile(t, root, openAPIDocumentPath...)), &doc); err != nil {
		t.Fatalf("parse OpenAPI document: %v", err)
	}
	op, ok := doc.Paths[path]["post"]
	if !ok {
		t.Fatalf("OpenAPI document has no POST %s", path)
	}
	for _, p := range op.Parameters {
		if p.In == "header" && uploadTokenHeaderPattern.MatchString(p.Name) {
			return p.Name
		}
	}
	t.Fatalf("POST %s declares no upload token header parameter", path)
	return ""
}

// markdownSection returns the text of the section that starts at the
// heading line exactly equal to heading and ends at the next heading of
// the same or a higher level.
func markdownSection(t *testing.T, page, heading string) string {
	t.Helper()
	level := len(heading) - len(strings.TrimLeft(heading, "#"))
	lines := strings.Split(page, "\n")
	start := -1
	for i, line := range lines {
		if line == heading {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("heading %q not found", heading)
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		trimmed := strings.TrimLeft(lines[i], "#")
		depth := len(lines[i]) - len(trimmed)
		if depth > 0 && depth <= level && strings.HasPrefix(trimmed, " ") {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

// mcpToolInputProperties returns the property names a hand-written MCP
// tool input schema declares.
func mcpToolInputProperties(t *testing.T, schemaJSON string) []string {
	t.Helper()
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal([]byte(schemaJSON), &schema); err != nil {
		t.Fatalf("parse tool input schema %q: %v", schemaJSON, err)
	}
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	if len(names) == 0 {
		t.Fatalf("tool input schema declares no properties: %s", schemaJSON)
	}
	return names
}

// spec: 15.1 (REST API), 7.4 (Upload Safety)
// diagnosis: a docs page names an upload token header other than the one
//
//	the OpenAPI document declares for the upload endpoints. A client that
//	copies the page sends a header the gateway does not read, and the
//	upload or finalize fails authorization.
func TestDocsUploadTokenHeaderMatchesOpenAPI_spec_15_1(t *testing.T) {
	root := repoRoot(t)
	want := openAPIUploadTokenHeader(t, root, "/v1/sessions/{id}/upload")
	if archive := openAPIUploadTokenHeader(t, root, "/v1/sessions/{id}/upload-archive"); archive != want {
		t.Fatalf("OpenAPI upload header %q differs from upload-archive header %q", want, archive)
	}
	for rel, body := range docsMarkdownPages(t, root) {
		for i, line := range strings.Split(body, "\n") {
			for _, got := range uploadTokenHeaderPattern.FindAllString(line, -1) {
				if got != want {
					t.Errorf("%s:%d names upload token header %q; the OpenAPI document declares %q", rel, i+1, got, want)
				}
			}
		}
	}
}

// spec: 15.1 (REST API), 14 (Workspace Plan Schema)
// diagnosis: a docs example uploads with a multipart form, or writes an
//
//	uploadRef as a concrete-looking literal. The upload endpoints take the
//	raw body, and an uploadRef is the opaque value of one upload response,
//	so either example teaches a request the gateway rejects.
func TestDocsUploadExamplesUseRawBodyAndPlaceholderRefs_spec_14(t *testing.T) {
	root := repoRoot(t)
	for rel, body := range docsMarkdownPages(t, root) {
		for i, line := range strings.Split(body, "\n") {
			if multipartUploadPattern.MatchString(line) {
				t.Errorf("%s:%d uploads with a multipart form; the upload endpoints take the raw body: %s", rel, i+1, strings.TrimSpace(line))
			}
			if literalUploadRefPattern.MatchString(line) {
				t.Errorf("%s:%d writes a literal uploadRef; use the <uploadRef> placeholder from the upload response: %s", rel, i+1, strings.TrimSpace(line))
			}
			if strings.Contains(line, "multipart/form-data") && strings.Contains(strings.ToLower(line), "upload") {
				t.Errorf("%s:%d describes the upload request as multipart/form-data: %s", rel, i+1, strings.TrimSpace(line))
			}
			if strings.Contains(line, "tar.bz2") {
				t.Errorf("%s:%d lists tar.bz2; the archive formats are tar, tar.gz, and zip: %s", rel, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// spec: 14.1 (WorkspacePlan Schema Versioning), 7.4 (Upload Safety)
// diagnosis: a docs example posts to the finalize endpoint without the
//
//	upload token header. Every finalize request carries the upload token,
//	so an example that omits it teaches a request that a token check
//	rejects.
func TestDocsFinalizeExamplesCarryUploadToken_spec_14_1(t *testing.T) {
	root := repoRoot(t)
	header := openAPIUploadTokenHeader(t, root, "/v1/sessions/{id}/upload")
	checked := 0
	for rel, body := range docsMarkdownPages(t, root) {
		for _, block := range fencedCodeBlocks(body) {
			if !finalizeRequestPattern.MatchString(block) {
				continue
			}
			checked++
			if !strings.Contains(block, header) {
				t.Errorf("%s has a finalize example without the %s header:\n%s", rel, header, block)
			}
		}
	}
	if checked == 0 {
		t.Fatal("found no finalize example under docs/; the sweep matched nothing")
	}
}

// spec: 15.1 (REST API), 15.2 (MCP API), 14.1 (WorkspacePlan Schema Versioning)
// diagnosis: the REST or MCP API page does not document the finalize plan
//
//	body, its plan-bind rejections, or the archive upload endpoint. A
//	client reading the page cannot bind a plan that names its uploads, or
//	cannot tell why a finalize request was rejected.
func TestAPIDocsDocumentFinalizePlanBinding_spec_15_1(t *testing.T) {
	root := repoRoot(t)
	rest := readRepoFile(t, root, "docs", "api", "rest.md")
	restFinalize := markdownSection(t, rest, "### POST /v1/sessions/{id}/finalize")
	mcpPage := readRepoFile(t, root, "docs", "api", "mcp.md")
	mcpFinalize := markdownSection(t, mcpPage, "### `finalize_workspace`")

	cases := []struct {
		name    string
		section string
	}{
		{"rest finalize", restFinalize},
		{"mcp finalize_workspace", mcpFinalize},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(tc.section, "`workspacePlan`") {
				t.Errorf("%s does not document the optional workspacePlan", tc.name)
			}
			for _, token := range planBindRejectionTokens {
				if !strings.Contains(tc.section, token) {
					t.Errorf("%s does not name the plan-bind rejection %s", tc.name, token)
				}
			}
			if !strings.Contains(tc.section, "leaves the session in `created`") {
				t.Errorf("%s does not state that a rejected plan leaves the session in created", tc.name)
			}
		})
	}
	archive := markdownSection(t, rest, "### POST /v1/sessions/{id}/upload-archive")
	for _, format := range []string{"`tar`", "`tar.gz`", "`zip`"} {
		if !strings.Contains(archive, format) {
			t.Errorf("rest.md upload-archive section does not list the %s format", format)
		}
	}
	if strings.Contains(restFinalize, "Moves uploaded files from the session's staging area") {
		t.Error("rest.md finalize still describes moving every uploaded file from staging")
	}
}

// spec: 15.2 (MCP API), 15.1 (REST API)
// diagnosis: the MCP API page documents an upload_files or
//
//	finalize_workspace argument that the registered tool schema does not
//	declare, or omits one it does. A client that follows the page calls the
//	tool with arguments the gateway rejects or ignores.
func TestMCPDocsToolArgumentsMatchRegisteredSchemas_spec_15_2(t *testing.T) {
	root := repoRoot(t)
	source := readRepoFile(t, root, mcpClientToolsSourcePath...)
	page := readRepoFile(t, root, "docs", "api", "mcp.md")

	uploadSchema := regexp.MustCompile("(?s)Name:\\s+\"lenny/upload_files\",.*?InputSchema:\\s+json\\.RawMessage\\(`([^`]+)`\\)").FindStringSubmatch(source)
	if uploadSchema == nil {
		t.Fatal("lenny/upload_files input schema not found in the tool source")
	}
	// The finalize schema is generated from the OpenAPI operation (§15.2.1
	// item 4), so it is read from the generated file.
	generated := readRepoFile(t, root, mcpGeneratedSchemasSourcePath...)
	finalizeSchema := regexp.MustCompile("var GeneratedFinalizeWorkspaceInputSchema = json\\.RawMessage\\(`([^`]+)`\\)").FindStringSubmatch(generated)
	if finalizeSchema == nil {
		t.Fatal("finalize_workspace input schema not found in the tool source")
	}

	cases := []struct {
		heading string
		schema  string
	}{
		{"### `upload_files`", uploadSchema[1]},
		{"### `finalize_workspace`", finalizeSchema[1]},
	}
	argRow := regexp.MustCompile("(?m)^\\| `([A-Za-z0-9]+)` \\|")
	for _, tc := range cases {
		t.Run(tc.heading, func(t *testing.T) {
			section := markdownSection(t, page, tc.heading)
			want := map[string]bool{}
			for _, name := range mcpToolInputProperties(t, tc.schema) {
				want[name] = true
			}
			inputTable := section
			if i := strings.Index(section, "**Input schema:**"); i >= 0 {
				inputTable = section[i:]
			}
			if j := strings.Index(inputTable, "**Output schema"); j >= 0 {
				inputTable = inputTable[:j]
			}
			documented := map[string]bool{}
			for _, m := range argRow.FindAllStringSubmatch(inputTable, -1) {
				documented[m[1]] = true
			}
			for name := range want {
				if !documented[name] {
					t.Errorf("%s does not document the registered argument %q", tc.heading, name)
				}
			}
			for name := range documented {
				if !want[name] {
					t.Errorf("%s documents argument %q, which the registered schema does not declare", tc.heading, name)
				}
			}
		})
	}
	if !strings.Contains(markdownSection(t, page, "### `upload_files`"), "uploadRef") {
		t.Error("the upload_files section does not document the uploadRef in its output")
	}
}

// spec: 14.1 (WorkspacePlan Schema Versioning), 14 (Workspace Plan Schema)
// diagnosis: the concepts page that owns workspace sources states that
//
//	sources are materialized or refs are pinned at session creation, or
//	does not state that only the uploads the bound plan names are
//	materialized. A plan can bind at finalize, so a reader takes the
//	create-time statement as a rule that uploads cannot be placed.
func TestConceptsWorkspaceSourcesStateBindTiming_spec_14_1(t *testing.T) {
	root := repoRoot(t)
	page := readRepoFile(t, root, "docs", "getting-started", "concepts.md")
	sources := markdownSection(t, page, "### Workspace sources")
	for _, retired := range []string{"at session creation time", "commit SHA at session creation and"} {
		if strings.Contains(sources, retired) {
			t.Errorf("Workspace sources still says %q", retired)
		}
	}
	for _, required := range []string{
		"finalize request",
		"when the plan binds",
		"only the uploads that an `uploadFile` or `uploadArchive` source of the bound plan names",
	} {
		if !strings.Contains(sources, required) {
			t.Errorf("Workspace sources does not state %q", required)
		}
	}
	setup := markdownSection(t, page, "### Setup commands")
	if !strings.Contains(setup, "exempt from the allowlist and from the command count") {
		t.Error("Setup commands does not state that runtime default setup commands are exempt from the policy")
	}
}

// backtickedTokenPattern matches an inline code span in markdown prose.
var backtickedTokenPattern = regexp.MustCompile("`([^`]+)`")

// uploadPreconditionRowPrefix opens the State Transition Preconditions
// table row that covers both upload endpoints.
const uploadPreconditionRowPrefix = "| `POST /v1/sessions/{id}/upload`, `POST /v1/sessions/{id}/upload-archive` |"

// sectionPreconditionLine returns the "Valid precondition states" line of
// a markdown section.
func sectionPreconditionLine(t *testing.T, section string) string {
	t.Helper()
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(line, "**Valid precondition states:**") {
			return line
		}
	}
	t.Fatal("section has no Valid precondition states line")
	return ""
}

// uploadPreconditionStates returns the session states the session
// lifecycle page's precondition table lists for the upload endpoints.
func uploadPreconditionStates(t *testing.T, page string) []string {
	t.Helper()
	for _, line := range strings.Split(page, "\n") {
		if !strings.HasPrefix(line, uploadPreconditionRowPrefix) {
			continue
		}
		cells := strings.Split(strings.TrimPrefix(line, uploadPreconditionRowPrefix), "|")
		var states []string
		for _, match := range backtickedTokenPattern.FindAllStringSubmatch(cells[0], -1) {
			states = append(states, match[1])
		}
		return states
	}
	t.Fatal("session-lifecycle.md has no precondition row for the upload endpoints")
	return nil
}

// spec: 15.1 (REST API), 7.4 (Upload Safety), 7.1 (Session Lifecycle)
// diagnosis: the REST page's upload precondition line lists fewer states
//
//	than the session lifecycle precondition table, so the page states the
//	mid-session upload precondition twice with two different answers and
//	the archive endpoint, which inherits that line, inherits the narrower
//	one.
func TestRESTUploadPreconditionMatchesLifecycleTable_spec_15_1(t *testing.T) {
	root := repoRoot(t)
	rest := readRepoFile(t, root, "docs", "api", "rest.md")
	line := sectionPreconditionLine(t, markdownSection(t, rest, "### POST /v1/sessions/{id}/upload"))
	lifecycle := readRepoFile(t, root, "docs", "client-guide", "session-lifecycle.md")
	states := uploadPreconditionStates(t, lifecycle)
	if len(states) == 0 {
		t.Fatal("the upload precondition row names no states")
	}
	for _, state := range states {
		if !strings.Contains(line, "`"+state+"`") {
			t.Errorf("rest.md upload precondition line %q omits the state %q the lifecycle table lists", line, state)
		}
	}
	if !strings.Contains(line, "`capabilities.midSessionUpload: true`") {
		t.Errorf("rest.md upload precondition line %q does not name the mid-session upload capability", line)
	}
}
