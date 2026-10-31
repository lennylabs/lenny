// SPDX-License-Identifier: MIT

// Package mcpschemagen derives MCP tool input JSON schemas from the
// gateway OpenAPI document.
//
// spec: §15.2.1 rule 4 — "The REST API's OpenAPI spec is the
// single authoritative schema for all overlapping operations. MCP tool
// schemas for overlapping operations (e.g., create_session) are generated
// from the OpenAPI spec's request/response definitions, not maintained
// independently. A code generation step in the build pipeline produces MCP
// tool JSON schemas from OpenAPI operation definitions, ensuring
// structural consistency by construction."
//
// The transform takes an OpenAPI operationId, resolves the operation's
// request body schema plus any path / required-query parameters, inlines
// every `$ref` into a self-contained JSON Schema (MCP tool input schemas
// carry no document-relative references), and merges any MCP-only
// extension properties the tool surfaces on top of the REST request. The
// produced bytes are deterministic: objects marshal with sorted keys so a
// committed generated schema and a fresh generation compare byte-for-byte,
// which is what the §15.2.1 drift guard asserts.
package mcpschemagen

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Options controls the generation for a single operation.
type Options struct {
	// ExtraProperties are MCP-only input properties merged on top of the
	// OpenAPI-derived request properties. They model fields a tool accepts
	// over MCP that have no REST request-body counterpart (e.g. the §11.5
	// `idempotencyKey` the create_session tool reads from the MCP body).
	// Each value is a JSON Schema fragment for that property.
	ExtraProperties map[string]json.RawMessage
	// ExtraRequired adds property names to the schema's `required` list on
	// top of the OpenAPI-derived required set.
	ExtraRequired []string
	// Params adjusts how OpenAPI parameters appear as MCP arguments, keyed
	// by the OpenAPI parameter name. A per-session REST operation names its
	// path parameter `id`, while the per-session MCP tools take the session
	// as `sessionId`, so the tool renames the parameter rather than
	// hand-writing its schema. A key that names no parameter of the
	// operation fails the generation, so a renamed OpenAPI parameter cannot
	// silently drop the MCP argument.
	Params map[string]ParamOptions
	// OptionalQuery names optional query parameters the tool accepts as
	// optional arguments. The generator leaves optional query parameters out
	// by default; a tool that forwards one (for example `since` on
	// get_session_logs) names it here. A name that is not an optional query
	// parameter of the operation fails the generation.
	OptionalQuery []string
	// Header names header parameters the tool accepts as arguments. The
	// generator leaves header parameters out by default, because most carry
	// transport metadata the MCP session supplies (tenant, idempotency). A
	// tool whose REST operation requires a caller-held header value, such as
	// the §7.1 uploadToken that every finalize request carries, names it
	// here and usually renames it through Params. A name that is not a
	// header parameter of the operation fails the generation.
	Header []string
}

// ParamOptions adjusts one OpenAPI parameter's MCP argument.
type ParamOptions struct {
	// Name is the MCP argument name. Empty keeps the OpenAPI name.
	Name string
	// Description is set on the argument schema when the OpenAPI parameter
	// schema carries no description of its own. The OpenAPI description,
	// when present, wins, because the OpenAPI document is the source of
	// truth.
	Description string
}

// OverlapSpec binds an MCP tool to the OpenAPI operation it overlaps.
type OverlapSpec struct {
	// ToolName is the MCP tool name (e.g. "lenny/create_session").
	ToolName string
	// OperationID is the OpenAPI operationId of the overlapping operation.
	OperationID string
	// Options carries the MCP-only extensions for this tool.
	Options Options
}

// DefaultOverlaps lists the operations whose MCP tool schemas are generated
// from OpenAPI. The list is the single source of truth shared by the
// generator command and the drift-guard test, so the two cannot diverge.
//
// An operation qualifies when an MCP tool forwards its arguments as the
// operation's JSON request body, path parameters, and query parameters.
// `POST /v1/sessions/{id}/messages` is excluded because the MCP
// `lenny/send_message` surface (`to`/`message` delegation routing) does not
// overlap the REST message-injection body.
func DefaultOverlaps() []OverlapSpec {
	return append([]OverlapSpec{
		{
			ToolName:    "lenny/create_session",
			OperationID: "postV1Sessions",
			Options: Options{
				// spec: §11.5 — the MCP create_session body
				// carries an optional idempotencyKey read by the MCP
				// idempotency hook. It has no REST request-body field, so
				// it is an MCP-only extension merged on top of the
				// OpenAPI CreateSessionRequest schema.
				ExtraProperties: map[string]json.RawMessage{
					"idempotencyKey": json.RawMessage(`{"type":"string","maxLength":128,"description":"§11.5 idempotency key: a duplicate request with the same key (within 24h) replays the cached result without re-executing."}`),
				},
			},
		},
	}, perSessionOverlaps()...)
}

// sessionIDParam renames a per-session operation's `id` path parameter to
// the `sessionId` argument the per-session MCP tools take.
var sessionIDParam = ParamOptions{Name: "sessionId", Description: "The target session id."}

// uploadTokenParam renames the X-Lenny-Upload-Token header parameter to the
// `uploadToken` argument the MCP tools take, matching lenny/upload_files.
var uploadTokenParam = ParamOptions{Name: "uploadToken", Description: "The §7.1 uploadToken returned by create_session."}

// perSessionOverlaps lists the per-session MCP tools whose REST counterpart
// is a `/v1/sessions/{id}/...` operation. Each renames the `id` path
// parameter to `sessionId`; the tool handler strips `sessionId` from the
// arguments and forwards the remainder as the REST body.
//
// spec: §15.2 (the client-facing tools); §15.2.1 (REST/MCP Consistency
// Contract, item 4).
func perSessionOverlaps() []OverlapSpec {
	byID := Options{Params: map[string]ParamOptions{"id": sessionIDParam}}
	return []OverlapSpec{
		{ToolName: "lenny/start_session", OperationID: "postV1SessionsByIdStart", Options: byID},
		{
			ToolName:    "lenny/finalize_workspace",
			OperationID: "postV1SessionsByIdFinalize",
			Options: Options{
				// spec: §7.4 Upload Safety (Upload authorization) — every
				// finalize request carries the §7.1 uploadToken, which the
				// REST operation takes in the X-Lenny-Upload-Token header.
				// The tool takes it as the `uploadToken` argument, as
				// lenny/upload_files does, and forwards it in that header.
				Params: map[string]ParamOptions{
					"id":                   sessionIDParam,
					"X-Lenny-Upload-Token": uploadTokenParam,
				},
				Header: []string{"X-Lenny-Upload-Token"},
			},
		},
		{ToolName: "lenny/terminate_session", OperationID: "postV1SessionsByIdTerminate", Options: byID},
		{ToolName: "lenny/resume_session", OperationID: "postV1SessionsByIdResume", Options: byID},
		{ToolName: "lenny/interrupt_session", OperationID: "postV1SessionsByIdInterrupt", Options: byID},
		{ToolName: "lenny/get_session_status", OperationID: "getV1SessionsById", Options: byID},
		{ToolName: "lenny/list_artifacts", OperationID: "getV1SessionsByIdArtifacts", Options: byID},
		{ToolName: "lenny/get_token_usage", OperationID: "getV1SessionsByIdUsage", Options: byID},
		{
			ToolName:    "lenny/get_session_logs",
			OperationID: "getV1SessionsByIdLogs",
			Options: Options{
				Params: map[string]ParamOptions{
					"id":    sessionIDParam,
					"since": {Description: "RFC3339 lower bound; only log lines at or after this time are returned."},
					"limit": {Description: "Maximum number of log lines to return."},
				},
				// The tool forwards `since` and `limit`; it does not page
				// with `cursor`.
				OptionalQuery: []string{"since", "limit"},
			},
		},
	}
}

// openapiDoc is the minimal slice of the OpenAPI document the generator
// reads. Each path item is decoded as raw method→object entries so a
// path-level `parameters` array or `summary` string does not break the
// decode; only HTTP-method keys are interpreted as operations.
type openapiDoc struct {
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components struct {
		Schemas map[string]json.RawMessage `json:"schemas"`
	} `json:"components"`
}

var httpMethods = map[string]bool{
	"get": true, "post": true, "put": true, "delete": true, "patch": true,
	"head": true, "options": true, "trace": true,
}

type operation struct {
	OperationID string       `json:"operationId"`
	Parameters  []parameter  `json:"parameters"`
	RequestBody *requestBody `json:"requestBody"`
}

type parameter struct {
	Name     string          `json:"name"`
	In       string          `json:"in"`
	Required bool            `json:"required"`
	Schema   json.RawMessage `json:"schema"`
}

type requestBody struct {
	Content map[string]struct {
		Schema json.RawMessage `json:"schema"`
	} `json:"content"`
}

// BuildToolInputSchema resolves the self-contained MCP input schema for the
// operation identified by operationID. spec: §15.2.1 rule 4.
func BuildToolInputSchema(rawDoc []byte, operationID string, opts Options) (json.RawMessage, error) {
	var doc openapiDoc
	if err := json.Unmarshal(rawDoc, &doc); err != nil {
		return nil, fmt.Errorf("decode openapi document: %w", err)
	}

	op, ok := findOperation(doc, operationID)
	if !ok {
		return nil, fmt.Errorf("operationId %q not found in openapi document", operationID)
	}

	properties := map[string]any{}
	required := map[string]bool{}

	// Path parameters become required input properties; a tool that
	// overlaps a `/v1/sessions/{id}/...` operation must accept the path id
	// as a tool argument. Required query parameters carry over the same
	// way. Optional query parameters are advisory and left out, so the MCP
	// surface stays a strict structural subset of the REST request, unless
	// the tool names them in opts.OptionalQuery.
	if err := checkParamOptions(op, opts); err != nil {
		return nil, fmt.Errorf("operation %q: %w", operationID, err)
	}
	optionalQuery := map[string]bool{}
	for _, name := range opts.OptionalQuery {
		optionalQuery[name] = true
	}
	header := map[string]bool{}
	for _, name := range opts.Header {
		header[name] = true
	}
	for _, p := range op.Parameters {
		included := p.In == "path" ||
			(p.In == "query" && (p.Required || optionalQuery[p.Name])) ||
			(p.In == "header" && header[p.Name])
		if !included {
			continue
		}
		schema := map[string]any{}
		if len(p.Schema) > 0 {
			if err := json.Unmarshal(p.Schema, &schema); err != nil {
				return nil, fmt.Errorf("decode parameter %q schema: %w", p.Name, err)
			}
		} else {
			schema["type"] = "string"
		}
		po := opts.Params[p.Name]
		if _, has := schema["description"]; !has && po.Description != "" {
			schema["description"] = po.Description
		}
		resolved, err := resolveRefs(schema, doc.Components.Schemas, nil)
		if err != nil {
			return nil, err
		}
		name := p.Name
		if po.Name != "" {
			name = po.Name
		}
		if _, dup := properties[name]; dup {
			return nil, fmt.Errorf("operation %q: two parameters map to the MCP argument %q", operationID, name)
		}
		properties[name] = resolved
		if p.In == "path" || p.Required {
			required[name] = true
		}
	}
	paramNames := make(map[string]bool, len(properties))
	for name := range properties {
		paramNames[name] = true
	}

	// Merge the request-body object's properties and required list.
	if op.RequestBody != nil {
		media, ok := op.RequestBody.Content["application/json"]
		if !ok {
			return nil, fmt.Errorf("operation %q has no application/json request body", operationID)
		}
		bodySchema := map[string]any{}
		if err := json.Unmarshal(media.Schema, &bodySchema); err != nil {
			return nil, fmt.Errorf("decode request body schema: %w", err)
		}
		resolved, err := resolveRefs(bodySchema, doc.Components.Schemas, nil)
		if err != nil {
			return nil, err
		}
		obj, ok := resolved.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("operation %q request body is not an object schema", operationID)
		}
		if props, ok := obj["properties"].(map[string]any); ok {
			for k, v := range props {
				if paramNames[k] {
					return nil, fmt.Errorf("operation %q: request body property %q collides with a parameter argument", operationID, k)
				}
				properties[k] = v
			}
		}
		if reqList, ok := obj["required"].([]any); ok {
			for _, r := range reqList {
				if name, ok := r.(string); ok {
					required[name] = true
				}
			}
		}
	}

	// Merge MCP-only extension properties on top of the REST-derived set.
	for name, frag := range opts.ExtraProperties {
		var v any
		if err := json.Unmarshal(frag, &v); err != nil {
			return nil, fmt.Errorf("decode extra property %q: %w", name, err)
		}
		properties[name] = v
	}
	for _, name := range opts.ExtraRequired {
		required[name] = true
	}

	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		names := make([]string, 0, len(required))
		for name := range required {
			names = append(names, name)
		}
		sort.Strings(names)
		schema["required"] = names
	}

	out, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal generated schema: %w", err)
	}
	return json.RawMessage(out), nil
}

// checkParamOptions fails when opts.Params or opts.OptionalQuery names a
// parameter the operation does not declare, so an OpenAPI rename surfaces
// as a generation error rather than a silently dropped argument.
func checkParamOptions(op operation, opts Options) error {
	declared := map[string]parameter{}
	for _, p := range op.Parameters {
		declared[p.Name] = p
	}
	names := make([]string, 0, len(opts.Params))
	for name := range opts.Params {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, ok := declared[name]; !ok {
			return fmt.Errorf("parameter option names %q, which the operation does not declare", name)
		}
	}
	for _, name := range opts.OptionalQuery {
		p, ok := declared[name]
		if !ok || p.In != "query" || p.Required {
			return fmt.Errorf("optional query parameter %q is not an optional query parameter of the operation", name)
		}
	}
	for _, name := range opts.Header {
		p, ok := declared[name]
		if !ok || p.In != "header" {
			return fmt.Errorf("header parameter %q is not a header parameter of the operation", name)
		}
	}
	return nil
}

func findOperation(doc openapiDoc, operationID string) (operation, bool) {
	for _, methods := range doc.Paths {
		for method, raw := range methods {
			if !httpMethods[strings.ToLower(method)] {
				continue
			}
			var op operation
			if err := json.Unmarshal(raw, &op); err != nil {
				continue
			}
			if op.OperationID == operationID {
				return op, true
			}
		}
	}
	return operation{}, false
}

// resolveRefs walks an arbitrary JSON value and replaces every
// `{"$ref": "#/components/schemas/X"}` object with the recursively-resolved
// component schema, producing a self-contained value with no references.
// The visited stack guards against a $ref cycle so a recursive component
// definition fails fast instead of overflowing the stack.
func resolveRefs(v any, components map[string]json.RawMessage, visiting []string) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		if ref, ok := t["$ref"].(string); ok && len(t) == 1 {
			name, ok := componentName(ref)
			if !ok {
				return nil, fmt.Errorf("unsupported $ref %q (only #/components/schemas/ is resolvable)", ref)
			}
			for _, seen := range visiting {
				if seen == name {
					return nil, fmt.Errorf("$ref cycle detected at component %q", name)
				}
			}
			raw, ok := components[name]
			if !ok {
				return nil, fmt.Errorf("$ref target component %q not found", name)
			}
			var resolved any
			if err := json.Unmarshal(raw, &resolved); err != nil {
				return nil, fmt.Errorf("decode component %q: %w", name, err)
			}
			return resolveRefs(resolved, components, append(visiting, name))
		}
		out := make(map[string]any, len(t))
		for k, val := range t {
			r, err := resolveRefs(val, components, visiting)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			r, err := resolveRefs(val, components, visiting)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	default:
		return v, nil
	}
}

func componentName(ref string) (string, bool) {
	const prefix = "#/components/schemas/"
	if !strings.HasPrefix(ref, prefix) {
		return "", false
	}
	return strings.TrimPrefix(ref, prefix), true
}
