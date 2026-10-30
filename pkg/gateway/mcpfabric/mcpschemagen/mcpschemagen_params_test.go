// SPDX-License-Identifier: MIT

package mcpschemagen_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcpschemagen"
)

// paramsDoc is an OpenAPI document with a per-session operation that has
// both a path parameter and a request body, and a path parameter whose
// schema carries its own description.
const paramsDoc = `{
  "paths": {
    "/v1/sessions/{id}/finalize": {
      "post": {
        "operationId": "finalize",
        "parameters": [{ "name": "id", "in": "path", "required": true, "schema": { "type": "string" } }],
        "requestBody": {
          "content": { "application/json": { "schema": { "$ref": "#/components/schemas/FinalizeRequest" } } }
        }
      }
    },
    "/v1/sessions/{id}/collide": {
      "post": {
        "operationId": "collide",
        "parameters": [{ "name": "id", "in": "path", "required": true, "schema": { "type": "string" } }],
        "requestBody": {
          "content": { "application/json": { "schema": { "type": "object", "properties": { "sessionId": { "type": "string" } } } } }
        }
      }
    },
    "/v1/things/{thingId}": {
      "get": {
        "operationId": "describedThing",
        "parameters": [
          { "name": "thingId", "in": "path", "required": true, "schema": { "type": "string", "description": "From OpenAPI." } },
          { "name": "other", "in": "query", "schema": { "type": "string" } }
        ]
      }
    }
  },
  "components": {
    "schemas": {
      "FinalizeRequest": {
        "type": "object",
        "properties": { "workspacePlan": { "type": ["object", "null"], "additionalProperties": true } }
      }
    }
  }
}`

var sessionIDRename = mcpschemagen.Options{Params: map[string]mcpschemagen.ParamOptions{
	"id": {Name: "sessionId", Description: "The target session id."},
}}

// TestBuildRenamesPathParam asserts the path-parameter rename option maps
// the OpenAPI `id` onto the MCP `sessionId` argument, keeps it required,
// drops the OpenAPI name, and merges the request body alongside it.
//
// spec: 15.2.1 (REST/MCP Consistency Contract, item 4)
func TestBuildRenamesPathParam_spec_15_2_1(t *testing.T) {
	raw, err := mcpschemagen.BuildToolInputSchema([]byte(paramsDoc), "finalize", sessionIDRename)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"properties":{"sessionId":{"description":"The target session id.","type":"string"},"workspacePlan":{"additionalProperties":true,"type":["object","null"]}},"required":["sessionId"],"type":"object"}`
	if string(raw) != want {
		t.Errorf("schema =\n %s\nwant\n %s", raw, want)
	}
}

// TestBuildParamDescriptionDefersToOpenAPI asserts an OpenAPI parameter
// description wins over the option's description, and that a rename with
// no Name keeps the OpenAPI parameter name.
//
// spec: 15.2.1 (REST/MCP Consistency Contract, item 4: the OpenAPI
// document is the single authoritative schema)
func TestBuildParamDescriptionDefersToOpenAPI_spec_15_2_1(t *testing.T) {
	raw, err := mcpschemagen.BuildToolInputSchema([]byte(paramsDoc), "describedThing", mcpschemagen.Options{
		Params: map[string]mcpschemagen.ParamOptions{"thingId": {Description: "From the tool."}},
	})
	if err != nil {
		t.Fatal(err)
	}
	props := mustObj(t, raw)["properties"].(map[string]any)
	thing, ok := props["thingId"].(map[string]any)
	if !ok {
		t.Fatalf("thingId missing: %v", props)
	}
	if thing["description"] != "From OpenAPI." {
		t.Errorf("description = %v, want the OpenAPI description", thing["description"])
	}
}

// TestBuildIncludesNamedOptionalQuery asserts an optional query parameter
// named in OptionalQuery becomes an optional argument, and one not named
// stays out.
//
// spec: 15.2.1 (REST/MCP Consistency Contract, item 4)
func TestBuildIncludesNamedOptionalQuery_spec_15_2_1(t *testing.T) {
	raw, err := mcpschemagen.BuildToolInputSchema([]byte(sampleDoc), "getThingSub", mcpschemagen.Options{
		OptionalQuery: []string{"verbose"},
		Params:        map[string]mcpschemagen.ParamOptions{"verbose": {Description: "Verbose output."}},
	})
	if err != nil {
		t.Fatal(err)
	}
	obj := mustObj(t, raw)
	props := obj["properties"].(map[string]any)
	v, ok := props["verbose"].(map[string]any)
	if !ok {
		t.Fatalf("named optional query param verbose not added: %v", props)
	}
	if v["type"] != "boolean" || v["description"] != "Verbose output." {
		t.Errorf("verbose = %v, want boolean with the option description", v)
	}
	for _, r := range obj["required"].([]any) {
		if r == "verbose" {
			t.Errorf("optional query param verbose marked required: %v", obj["required"])
		}
	}
}

// TestBuildRejectsBadParamOptions asserts each misconfiguration of the
// parameter options fails the generation rather than producing a schema
// that silently drops or duplicates an argument.
//
// spec: 15.2.1 (REST/MCP Consistency Contract, item 4: structural
// consistency by construction)
func TestBuildRejectsBadParamOptions_spec_15_2_1(t *testing.T) {
	cases := []struct {
		name    string
		doc     string
		op      string
		opts    mcpschemagen.Options
		wantErr string
	}{
		{
			name: "rename of an undeclared parameter", doc: paramsDoc, op: "finalize",
			opts:    mcpschemagen.Options{Params: map[string]mcpschemagen.ParamOptions{"sessionID": {Name: "sessionId"}}},
			wantErr: "does not declare",
		},
		{
			name: "optional query that is undeclared", doc: sampleDoc, op: "getThingSub",
			opts:    mcpschemagen.Options{OptionalQuery: []string{"cursor"}},
			wantErr: "not an optional query parameter",
		},
		{
			name: "optional query naming a required query parameter", doc: sampleDoc, op: "getThingSub",
			opts:    mcpschemagen.Options{OptionalQuery: []string{"limit"}},
			wantErr: "not an optional query parameter",
		},
		{
			name: "optional query naming a path parameter", doc: sampleDoc, op: "getThingSub",
			opts:    mcpschemagen.Options{OptionalQuery: []string{"id"}},
			wantErr: "not an optional query parameter",
		},
		{
			name: "two parameters renamed to one argument", doc: sampleDoc, op: "getThingSub",
			opts:    mcpschemagen.Options{Params: map[string]mcpschemagen.ParamOptions{"limit": {Name: "id"}}},
			wantErr: "two parameters map to",
		},
		{
			name: "renamed parameter colliding with a body property", doc: paramsDoc, op: "collide",
			opts:    sessionIDRename,
			wantErr: "collides with a parameter argument",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := mcpschemagen.BuildToolInputSchema([]byte(tc.doc), tc.op, tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
}
