// SPDX-License-Identifier: MIT

// Package openapi serves the §15.1 OpenAPI 3.x specification at
// GET /openapi.yaml and GET /v1/openapi.json.
//
// The canonical document is embedded as JSON from openapi.json at
// build time. YAML 1.2 is a strict superset of JSON, so the same
// bytes serialise correctly with the YAML Content-Type for SDK
// generators that prefer YAML input.
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
//	GET /v1/openapi.json — JSON form
//
// Both endpoints are unauthenticated per §15.1: the spec must be
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
	mux.HandleFunc("GET /v1/openapi.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=60")
		_, _ = w.Write(body)
	})
	return mux
}

// Document returns the raw OpenAPI JSON bytes with the embedded
// `info.version`. Useful for tests that validate the document shape.
func Document() []byte { return append([]byte(nil), openapiDoc...) }

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
// `info.version` field rewritten to buildVersion. The embedded value
// is kept whenever buildVersion is empty, equal to "dev", or the
// embedded document fails to round-trip through json — the latter
// preserves the served bytes' validity even under a future
// hand-edited document.
func versionedDocument(buildVersion string) []byte {
	v := strings.TrimSpace(buildVersion)
	if v == "" || v == "dev" {
		return openapiDoc
	}
	var doc map[string]any
	if err := json.Unmarshal(openapiDoc, &doc); err != nil {
		return openapiDoc
	}
	info, _ := doc["info"].(map[string]any)
	if info == nil {
		return openapiDoc
	}
	info["version"] = v
	doc["info"] = info
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return openapiDoc
	}
	return out
}
