// SPDX-License-Identifier: MIT

// Package openapi serves the §15.1 OpenAPI 3.x specification at
// GET /openapi.yaml, GET /openapi.json, GET /v1/openapi.json, and
// GET /v1/openapi.yaml.
//
// The canonical document is embedded as JSON from openapi.json at
// build time. YAML 1.2 is a strict superset of JSON, so the same
// bytes serialise correctly with the YAML Content-Type for SDK
// generators that prefer YAML input.
//
// §15.1 line 589 names `/openapi.yaml` and `/openapi.json` as the
// canonical gateway-side endpoints; the `/v1/openapi.json` and
// `/v1/openapi.yaml` mounts are the admin-API paths §15.1 (OpenAPI
// generation and discovery) lists so the §25.12 schema-discovery block
// and the §25.4 `/me` `links.openApi` hop resolve against the gateway.
// F-15.1.17 / F-COV-1.
//
// The spec is the canonical source for community SDK generators and
// for the §13 MCP Management Server's `openapi-to-mcp` tool generator
// (Phase 13). Every admin endpoint carries `x-lenny-mcp-tool`,
// `x-lenny-scope`, `x-lenny-required-role`, and `x-lenny-category`
// extensions per §15.1.
package openapi

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"strings"
)

// The lenny-ops operability route schemas are merged into openapi.json by
// the genopsschemas generator so the single served document covers the
// entire operability surface (F-COV-3, §25.12). Run it after adding or
// removing a lenny-ops route; the completeness test guards the committed
// document against the opsserver.RouteSchemas registry.
//
//go:generate go run ./internal/genopsschemas
//go:embed openapi.json
var openapiDoc []byte

// Handler returns the http.Handler that serves the OpenAPI document
// with `info.version` left at its embedded default. Prefer
// HandlerWithVersion for production wiring so the served document
// matches the gateway's release version.
//
// Mounts:
//
//	GET /openapi.yaml — YAML form (JSON is valid YAML 1.2)
//	GET /openapi.json — JSON form (§15.1 line 589 canonical mount)
//	GET /v1/openapi.json — JSON form (§15.1 admin-API discovery path)
//	GET /v1/openapi.yaml — YAML form (§15.1 admin-API discovery path)
//
// Every endpoint is unauthenticated per §15.1: the spec must be
// discoverable so SDK generators and the MCP Management Server can
// fetch it without a bearer token.
func Handler() http.Handler { return HandlerWithVersion("") }

// HandlerWithVersion returns the http.Handler that serves the
// OpenAPI document with `info.version` overridden to the supplied
// gateway release version. Empty or "dev" leaves the embedded value
// untouched.
//
// spec: §15.1 line 589 — `info.version` field in the spec matches
// the gateway's release version.
func HandlerWithVersion(buildVersion string) http.Handler {
	body := versionedDocument(buildVersion)
	serve := func(contentType string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", contentType)
			w.Header().Set("Cache-Control", "public, max-age=60")
			_, _ = w.Write(body)
		}
	}
	yaml := serve("application/yaml")
	jsonDoc := serve("application/json")
	mux := http.NewServeMux()
	// spec: §15.1 line 589 — gateway serves the JSON and YAML forms at
	// the root `/openapi.*` paths. F-15.1.17.
	mux.HandleFunc("GET /openapi.yaml", yaml)
	mux.HandleFunc("GET /openapi.json", jsonDoc)
	// spec: §15.1 (OpenAPI generation and discovery) — the admin-API
	// discovery paths §15.1 lists as `/v1/openapi.json` and
	// `/v1/openapi.yaml`. §25.12 (schema discovery) and the §25.4 `/me`
	// `links.openApi` hop resolve against these gateway-served paths, so
	// both forms are mounted here rather than only JSON. F-COV-1.
	mux.HandleFunc("GET /v1/openapi.json", jsonDoc)
	mux.HandleFunc("GET /v1/openapi.yaml", yaml)
	return mux
}

// Document returns the OpenAPI JSON bytes with the embedded
// `info.version` plus the §15.5 item 6 default-stability stamp. Useful
// for tests that validate the document shape. F-15.5.10.
func Document() []byte {
	return append([]byte(nil), versionedDocument("")...)
}

// DocumentWithVersion returns the OpenAPI JSON bytes with
// `info.version` set to the supplied gateway release version. Useful
// for the §13 MCP Management Server's `openapi-to-mcp` generator and
// for tests that assert the gateway-release imprint on the document.
//
// spec: §15.1 line 589.
func DocumentWithVersion(buildVersion string) []byte {
	return append([]byte(nil), versionedDocument(buildVersion)...)
}

// versionedDocument returns the embedded document bytes with the
// `info.version` field rewritten to buildVersion and the §15.5 item 6
// default-stability stamp applied to every operation that lacks an
// explicit override. The embedded value is kept whenever the embedded
// document fails to round-trip through json — that path preserves the
// served bytes' validity even under a future hand-edited document.
//
// Hand-edited overrides in openapi.json (e.g. an explicit
// `"x-lenny-stability": "beta"` on `/v1/responses`) are preserved
// verbatim; only operations missing the field get the default stamp.
// F-15.5.10.
func versionedDocument(buildVersion string) []byte {
	var doc map[string]any
	if err := json.Unmarshal(openapiDoc, &doc); err != nil {
		return openapiDoc
	}
	stampDefaultStability(doc)
	v := strings.TrimSpace(buildVersion)
	if v != "" && v != "dev" {
		if info, _ := doc["info"].(map[string]any); info != nil {
			info["version"] = v
			doc["info"] = info
		}
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return openapiDoc
	}
	return out
}

// stampDefaultStability walks doc.paths and stamps
// `x-lenny-stability: "stable"` onto every operation body that does
// not already carry one. The walk mutates doc in place.
//
// spec: §15.5 item 6 — F-15.5.10.
func stampDefaultStability(doc map[string]any) {
	paths, _ := doc["paths"].(map[string]any)
	if paths == nil {
		return
	}
	verbs := map[string]struct{}{
		"get": {}, "put": {}, "post": {}, "delete": {},
		"options": {}, "head": {}, "patch": {}, "trace": {},
	}
	for _, raw := range paths {
		methods, _ := raw.(map[string]any)
		if methods == nil {
			continue
		}
		for name, m := range methods {
			if _, ok := verbs[name]; !ok {
				continue
			}
			body, _ := m.(map[string]any)
			if body == nil {
				continue
			}
			if _, ok := body["x-lenny-stability"]; ok {
				continue
			}
			body["x-lenny-stability"] = string(StabilityStable)
		}
	}
}

// Stability is the §15.5 item 6 tier the OpenAPI document defaults
// every operation to when `x-lenny-stability` is omitted from
// openapi.json. The MCP Tool descriptor and any future stability-aware
// surface share the same string values (`stable`, `beta`, `alpha`).
//
// spec: §15.5 item 6 — F-15.5.10.
type Stability string

const (
	StabilityStable Stability = "stable"
	StabilityBeta   Stability = "beta"
	StabilityAlpha  Stability = "alpha"
)

// RouteScopes resolves the §15.1 `x-lenny-scope` a request route requires
// before the admin REST surface dispatches a handler. It is built once from
// the served OpenAPI document and answers `(method, request path)` lookups
// for the scope-enforcement middleware (§25.1 enforcement point 1).
//
// The matcher reuses Go's http.ServeMux pattern routing, the same engine the
// admin router registers its handlers on, so a request path resolves to the
// same `{param}` template the live mux would match and the completeness walk
// enumerates. A registered scope template and the live admin route therefore
// match a request identically; the document is the single source of the
// route-to-scope mapping.
//
// spec: §15.1 (scope enforcement before routing, line 914,920),
// §25.1 (middleware checks scopes before routing, line 94).
type RouteScopes struct {
	mux *http.ServeMux
}

// scopeForPattern carries the required scope for one registered
// `(method, path-template)` pattern. The ServeMux handler stores it so a
// matched request recovers the scope without re-parsing the document.
type scopeForPattern struct {
	scope string
}

func (h scopeForPattern) ServeHTTP(http.ResponseWriter, *http.Request) {}

// NewRouteScopes builds the route-to-scope lookup from the served document.
// It registers every operation that carries a non-empty `x-lenny-scope` as a
// `METHOD /path-template` pattern on an http.ServeMux whose handler holds the
// scope, so RequiredScope can recover the scope a matched route requires. A
// malformed document yields an empty matcher that reports no required scope
// for every route; the caller fails closed on its own (an unresolved scope on
// a destructive admin route defers to the role ceiling per §25.1, line 90).
//
// spec: §15.1 (x-lenny-scope per operation, line 920),
// §25.1 (scope enforcement point 1).
func NewRouteScopes() *RouteScopes {
	rs := &RouteScopes{mux: http.NewServeMux()}
	var doc map[string]any
	if err := json.Unmarshal(Document(), &doc); err != nil {
		return rs
	}
	paths, _ := doc["paths"].(map[string]any)
	for path, raw := range paths {
		methods, _ := raw.(map[string]any)
		for method, m := range methods {
			body, _ := m.(map[string]any)
			if body == nil {
				continue
			}
			scope, _ := body["x-lenny-scope"].(string)
			if scope == "" {
				continue
			}
			// The OpenAPI `{param}` template is syntactically the same
			// single-segment wildcard http.ServeMux matches, so the
			// pattern registers verbatim. The verb is upper-cased to the
			// canonical HTTP method ServeMux keys on.
			pattern := strings.ToUpper(method) + " " + path
			rs.mux.Handle(pattern, scopeForPattern{scope: scope})
		}
	}
	return rs
}

// RequiredScope returns the §15.1 scope the `(method, path)` route requires
// and whether the document declares one. An unmatched route, or a matched
// route with no `x-lenny-scope`, returns ("", false): the route declares no
// route-level scope and the caller defers to the role ceiling. The path is
// the request path (e.g. `/v1/admin/legal-hold`), matched against the
// document's templates through the same http.ServeMux engine the admin
// router routes on.
//
// spec: §15.1 (scope enforcement before routing, line 914,920),
// §25.1 (middleware checks scopes before routing, line 94).
func (rs *RouteScopes) RequiredScope(method, path string) (string, bool) {
	if rs == nil || rs.mux == nil {
		return "", false
	}
	req, err := http.NewRequest(strings.ToUpper(method), path, nil)
	if err != nil {
		return "", false
	}
	h, pattern := rs.mux.Handler(req)
	if pattern == "" {
		// No registered template matched the request.
		return "", false
	}
	sp, ok := h.(scopeForPattern)
	if !ok {
		// A non-scope handler (the ServeMux 404/405 default) matched.
		return "", false
	}
	return sp.scope, true
}
