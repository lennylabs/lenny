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
	"net/http"
)

//go:embed openapi.json
var openapiDoc []byte

// Handler returns the http.Handler that serves the OpenAPI document.
// It mounts:
//
//	GET /openapi.yaml — YAML form (JSON is valid YAML 1.2)
//	GET /v1/openapi.json — JSON form
//
// Both endpoints are unauthenticated per §15.1: the spec must be
// discoverable so SDK generators and the MCP Management Server can
// fetch it without a bearer token.
func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /openapi.yaml", serveYAML)
	mux.HandleFunc("GET /v1/openapi.json", serveJSON)
	return mux
}

func serveYAML(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	w.Header().Set("Cache-Control", "public, max-age=60")
	_, _ = w.Write(openapiDoc)
}

func serveJSON(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=60")
	_, _ = w.Write(openapiDoc)
}

// Document returns the raw OpenAPI JSON bytes. Useful for the §13
// MCP Management Server's `openapi-to-mcp` generator and for tests
// that validate the document shape.
func Document() []byte { return append([]byte(nil), openapiDoc...) }
