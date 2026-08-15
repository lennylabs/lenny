// SPDX-License-Identifier: MIT

//go:build contract

// Validates the §15.1 served OpenAPI document: that it is a
// well-formed OpenAPI 3.1 document per the published meta-schema, and
// that every path this package's REST session surface advertises
// resolves to a live handler on the in-process session server this
// package already exercises. Neither check exists elsewhere:
// pkg/gateway/externalapi/openapi/completeness_test.go pins the
// registered-routes-are-documented direction (mux ⊆ doc) but not
// meta-schema validity or the reverse direction for this package's
// domain.

package rest_sessions_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/openapi"
	"github.com/lennylabs/lenny/scripts/specshift/citation"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// openAPIMetaSchemaFile is the vendored OpenAPI 3.1 meta-schema.
// tests/testdata/openapi/README.md documents provenance.
const openAPIMetaSchemaFile = "tests/testdata/openapi/schema/openapi-3.1.schema.json"

// spec: §15.1 ("The gateway serves its OpenAPI 3.x specification at
// GET /openapi.yaml (no authentication required). The same document is
// available at GET /openapi.json for clients that prefer JSON.").
// diagnosis: a failure here means the document the gateway serves at
// GET /openapi.json does not conform to the OpenAPI 3.1 meta-schema —
// an SDK generator or any OpenAPI-3.1-conformant tool consuming
// /openapi.yaml per §15.1 would reject or misinterpret the document
// even though Lenny's own handler serves it without error. Compare the
// reported JSON Pointer against pkg/gateway/externalapi/openapi/openapi.json
// to find the offending field.
func TestServedDocumentIsValidOpenAPI31(t *testing.T) {
	ts := httptest.NewServer(openapi.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/openapi.json")
	if err != nil {
		t.Fatalf("GET /openapi.json: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}

	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("served /openapi.json is not valid JSON: %v", err)
	}

	schema := schematest.Compile(t, openAPIMetaSchemaFile)
	if err := schema.Validate(doc); err != nil {
		t.Errorf("served /openapi.json does not conform to the OpenAPI 3.1 meta-schema: %v", err)
	}
}

// servedDocumentRoutes are the §15.1 routes the gateway serves the
// OpenAPI document on. Each is read separately, because a client that
// reaches the document through one mount holds whatever that mount
// returns; a strip that missed one form would ship to that client.
var servedDocumentRoutes = []string{
	"/openapi.yaml",
	"/openapi.json",
	"/v1/openapi.json",
	"/v1/openapi.yaml",
}

// lineCitationSpecimenFile holds one citation of the retired line
// form. The specimen lives under testdata/, which sits outside the read
// domain of the tree-wide citation gates, because a specimen is input
// to a gate rather than a pointer into the specification and a tracked
// Go file carrying one would register as a live citation site.
const lineCitationSpecimenFile = "testdata/line-citation-specimen.txt"

// generatedMCPToolSchemaFiles are the committed generated MCP tool
// schemas derived from the served OpenAPI document: the
// `lenny/create_session` input schema genmcpschemas writes, and the
// §25.12 `/mcp/management` tool inventory openapi-to-mcp writes. Both
// copy their descriptions out of the document verbatim, so a citation
// left in the document reaches an MCP client through them.
var generatedMCPToolSchemaFiles = []string{
	"pkg/gateway/mcpfabric/mcptools/generated_schemas.go",
	"pkg/ops/mcp/generated_tools.go",
}

// spec: §28.1 (N8, the citation rule: a specification citation names a
// heading rather than a line). A served client artifact carries no
// citation of the specification at all, so the retired line form is
// absent from the document the gateway serves under §15.1 and from the
// §15.2.1 and §25.12 tool schemas generated from it.
// diagnosis: a failure here means a citation of the retired line form
// reached a client-facing artifact. A line number in a served
// description points at a specification the client does not have and
// goes stale the next time the section moves. Remove the citation from
// pkg/gateway/externalapi/openapi/openapi.json and regenerate the two
// derived artifacts; a section-only citation is out of this
// assertion's scope and survives.
func TestServedClientArtifactsCarryNoRetiredLineCitation(t *testing.T) {
	t.Parallel()

	// The matcher is exercised on a specimen first. Every assertion
	// below reports absence, and absence is what a matcher that reads
	// nothing reports over every input.
	specimen, err := os.ReadFile(lineCitationSpecimenFile)
	if err != nil {
		t.Fatalf("read the retired-form specimen at %s: %v", lineCitationSpecimenFile, err)
	}
	if found := citation.Find(string(specimen)); len(found) != 1 {
		t.Fatalf("the citation matcher read %d citation(s) in the specimen at %s, want the one it carries; "+
			"the assertions below would pass over any input", len(found), lineCitationSpecimenFile)
	}

	ts := httptest.NewServer(openapi.Handler())
	t.Cleanup(ts.Close)

	for _, route := range servedDocumentRoutes {
		t.Run(route, func(t *testing.T) {
			body := fetchServedDocument(t, ts.URL+route)
			if !strings.Contains(body, "openapi") {
				t.Fatalf("GET %s returned no OpenAPI document, so the route carries nothing to assert over", route)
			}
			reportLineCitations(t, route, body)
		})
	}

	root := schematest.RepoRoot(t)
	for _, rel := range generatedMCPToolSchemaFiles {
		t.Run(rel, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
			if err != nil {
				t.Fatalf("read the generated tool schemas at %s: %v", rel, err)
			}
			if len(body) == 0 {
				t.Fatalf("%s is empty, so the file carries nothing to assert over", rel)
			}
			reportLineCitations(t, rel, string(body))
		})
	}
}

// fetchServedDocument reads the document the gateway serves at url.
func fetchServedDocument(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return string(body)
}

// reportLineCitations fails the test for every citation of the retired
// line form the carrier holds. The broad predicate runs beside the
// form, so a spelling the form does not read is reported here rather
// than passing unread.
func reportLineCitations(t *testing.T, carrier, content string) {
	t.Helper()
	for _, c := range citation.FindIn(carrier, content) {
		t.Errorf("%s carries a citation of the retired line form: %s", carrier, c)
	}
	for _, b := range citation.FindBroadIn(carrier, content) {
		t.Errorf("%s carries a citation naming a line: %s", carrier, b)
	}
}

// sessionServerPathPrefixes are the §15.1 path prefixes
// pkg/gateway/sessionserver.Server.Handler registers on its own mux
// (POST/GET /v1/sessions..., /v1/runtimes, /v1/pools, /v1/models,
// /v1/usage, /v1/metering/events, /v1/blobs, /internal/runtimes,
// /v1/environments/{name}/sessions). A documented path under one of
// these prefixes must resolve on the in-process session server this
// package's newTestServer helper already stands up; a path outside
// this set belongs to the admin, credential, or main-gateway mux and
// is out of scope for this package (pkg/gateway/externalapi/openapi's
// own completeness tests cover those muxes).
var sessionServerPathPrefixes = []string{
	"/v1/sessions",
	"/v1/runtimes",
	"/v1/pools",
	"/v1/models",
	"/v1/usage",
	"/v1/metering",
	"/v1/blobs",
	"/internal/runtimes",
	"/v1/environments",
}

// pathParamPattern matches an OpenAPI path-template parameter segment,
// including the Go 1.22 ServeMux multi-segment wildcard suffix
// ("{ref...}"), so a documented template substitutes to a concrete
// path a live *http.ServeMux can route.
var pathParamPattern = regexp.MustCompile(`\{[^}]+\}`)

// spec: §15.1 ("The gateway serves its OpenAPI 3.x specification at
// GET /openapi.yaml ... The spec is generated from the same
// source-of-truth that drives REST/MCP contract tests.").
// diagnosis: a failure here means the served openapi.json advertises a
// path+method under this package's session-server domain that has no
// live handler — a client following the OpenAPI document (the
// documented source-of-truth for REST contract tests) would get a
// route it cannot actually reach. Check whether the path was removed
// from pkg/gateway/sessionserver.Server.Handler without removing it
// from openapi.json, or added to openapi.json ahead of the handler.
func TestSessionDomainDocumentedPathsHaveLiveHandlers(t *testing.T) {
	var parsed map[string]any
	if err := json.Unmarshal(openapi.Document(), &parsed); err != nil {
		t.Fatalf("decode served document: %v", err)
	}
	paths, _ := parsed["paths"].(map[string]any)
	if len(paths) == 0 {
		t.Fatal("served document has no paths object")
	}

	sess := newTestServer(t)
	mux, ok := sess.Config.Handler.(*http.ServeMux)
	if !ok {
		t.Fatalf("session server handler is %T, not *http.ServeMux", sess.Config.Handler)
	}

	checked := 0
	for path, rawItem := range paths {
		if !hasSessionServerPrefix(path) {
			continue
		}
		item, _ := rawItem.(map[string]any)
		for verb := range item {
			method := httpMethodFor(verb)
			if method == "" {
				continue
			}
			concrete := pathParamPattern.ReplaceAllString(path, "x")
			req, err := http.NewRequest(method, "http://in-process"+concrete, nil)
			if err != nil {
				t.Fatalf("build request for %s %s: %v", method, concrete, err)
			}
			if _, pattern := mux.Handler(req); pattern == "" {
				t.Errorf("documented %s %s (template %s) has no live handler on the session server mux", method, concrete, path)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no documented path matched a session-server prefix — sessionServerPathPrefixes or the served document drifted")
	}
}

// hasSessionServerPrefix reports whether path falls under one of the
// session-server-owned prefixes (see sessionServerPathPrefixes).
func hasSessionServerPrefix(path string) bool {
	for _, prefix := range sessionServerPathPrefixes {
		if path == prefix || len(path) > len(prefix) && path[:len(prefix)] == prefix && path[len(prefix)] == '/' {
			return true
		}
	}
	return false
}

// httpMethodFor maps an OpenAPI operation verb key to the upper-case
// HTTP method http.ServeMux's pattern matching requires, or "" for a
// verb this package's mux never registers (OPTIONS, HEAD, TRACE —
// none of the §15.1 session endpoints use them).
func httpMethodFor(verb string) string {
	switch verb {
	case "get", "post", "put", "delete", "patch":
		return strings.ToUpper(verb)
	default:
		return ""
	}
}
