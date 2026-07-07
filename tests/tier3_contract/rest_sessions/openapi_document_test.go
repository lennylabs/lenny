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
	"regexp"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/openapi"
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
