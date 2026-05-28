// SPDX-License-Identifier: MIT

// Package openapi serves the §15.1 OpenAPI 3.x specification at
// GET /openapi.yaml, GET /openapi.json, and GET /v1/openapi.json.
//
// The canonical document is embedded as JSON from openapi.json at
// build time. YAML 1.2 is a strict superset of JSON, so the same
// bytes serialise correctly with the YAML Content-Type for SDK
// generators that prefer YAML input.
//
// §15.1 line 589 names `/openapi.yaml` and `/openapi.json` as the
// canonical gateway-side endpoints; the `/v1/openapi.json` mount is
// kept so the §18 build-sequence reference and the §25.4 lenny-ops
// listing remain aligned. F-15.1.17.
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
//	GET /v1/openapi.json — JSON form (legacy alias retained for §18
//	    build-sequence references and §25.4 lenny-ops parity)
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
	mux := http.NewServeMux()
	mux.HandleFunc("GET /openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		w.Header().Set("Cache-Control", "public, max-age=60")
		_, _ = w.Write(body)
	})
	// spec: §15.1 line 589 — gateway serves the JSON form at
	// `/openapi.json`. F-15.1.17.
	mux.HandleFunc("GET /openapi.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=60")
		_, _ = w.Write(body)
	})
	mux.HandleFunc("GET /v1/openapi.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=60")
		_, _ = w.Write(body)
	})
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
