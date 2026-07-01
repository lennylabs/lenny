// SPDX-License-Identifier: MIT

package mcpschemagen_test

import (
	"encoding/json"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcpschemagen"
)

// sampleDoc is a minimal OpenAPI document exercising a request body with a
// nested $ref, a path parameter, a required and an optional query
// parameter, plus a recursive component to assert cycle detection.
const sampleDoc = `{
  "paths": {
    "/v1/sessions": {
      "post": {
        "operationId": "createThing",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": { "$ref": "#/components/schemas/CreateThingRequest" }
            }
          }
        }
      }
    },
    "/v1/things/{id}/sub": {
      "parameters": [{ "name": "ignored", "in": "path", "schema": {"type": "string"} }],
      "summary": "path-level fields must not break decoding",
      "get": {
        "operationId": "getThingSub",
        "parameters": [
          { "name": "id", "in": "path", "required": true, "schema": { "type": "string" } },
          { "name": "limit", "in": "query", "required": true, "schema": { "type": "integer" } },
          { "name": "verbose", "in": "query", "schema": { "type": "boolean" } }
        ]
      }
    },
    "/v1/cyclic": {
      "post": {
        "operationId": "cyclic",
        "requestBody": {
          "content": {
            "application/json": { "schema": { "$ref": "#/components/schemas/Node" } }
          }
        }
      }
    }
  },
  "components": {
    "schemas": {
      "CreateThingRequest": {
        "type": "object",
        "required": ["name"],
        "properties": {
          "name": { "type": "string" },
          "policy": { "$ref": "#/components/schemas/Policy" }
        }
      },
      "Policy": {
        "type": "object",
        "properties": { "mode": { "type": "string", "enum": ["a", "b"] } }
      },
      "Node": {
        "type": "object",
        "properties": { "child": { "$ref": "#/components/schemas/Node" } }
      }
    }
  }
}`

func mustObj(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("schema not an object: %v\n%s", err, raw)
	}
	return m
}

// TestBuildInlinesRefs asserts the §15.2.1 rule-4 generator resolves a
// nested $ref into a self-contained schema with no document references.
func TestBuildInlinesRefs_spec_15_2_1_1386(t *testing.T) {
	raw, err := mcpschemagen.BuildToolInputSchema([]byte(sampleDoc), "createThing", mcpschemagen.Options{})
	if err != nil {
		t.Fatal(err)
	}
	obj := mustObj(t, raw)
	props := obj["properties"].(map[string]any)
	policy, ok := props["policy"].(map[string]any)
	if !ok {
		t.Fatalf("policy property missing or not inlined: %v", props["policy"])
	}
	if _, hasRef := policy["$ref"]; hasRef {
		t.Errorf("policy still carries a $ref; refs must be inlined: %v", policy)
	}
	if policy["type"] != "object" {
		t.Errorf("inlined Policy type: got %v, want object", policy["type"])
	}
	req, _ := obj["required"].([]any)
	if len(req) != 1 || req[0] != "name" {
		t.Errorf("required: got %v, want [name]", req)
	}
}

// TestBuildMergesExtraProperties asserts MCP-only extensions merge on top
// of the OpenAPI-derived properties.
func TestBuildMergesExtraProperties_spec_15_2_1_1386(t *testing.T) {
	raw, err := mcpschemagen.BuildToolInputSchema([]byte(sampleDoc), "createThing", mcpschemagen.Options{
		ExtraProperties: map[string]json.RawMessage{
			"idempotencyKey": json.RawMessage(`{"type":"string"}`),
		},
		ExtraRequired: []string{"name"},
	})
	if err != nil {
		t.Fatal(err)
	}
	obj := mustObj(t, raw)
	props := obj["properties"].(map[string]any)
	if _, ok := props["idempotencyKey"]; !ok {
		t.Errorf("extra property idempotencyKey not merged: %v", props)
	}
}

// TestBuildMergesPathAndRequiredQueryParams asserts path parameters and
// required query parameters become required input properties while optional
// query parameters are omitted so the MCP input stays a structural subset.
func TestBuildMergesPathParams_spec_15_2_1_1386(t *testing.T) {
	raw, err := mcpschemagen.BuildToolInputSchema([]byte(sampleDoc), "getThingSub", mcpschemagen.Options{})
	if err != nil {
		t.Fatal(err)
	}
	obj := mustObj(t, raw)
	props := obj["properties"].(map[string]any)
	if _, ok := props["id"]; !ok {
		t.Errorf("path param id not added: %v", props)
	}
	if _, ok := props["limit"]; !ok {
		t.Errorf("required query param limit not added: %v", props)
	}
	if _, ok := props["verbose"]; ok {
		t.Errorf("optional query param verbose must not be added: %v", props)
	}
	req := map[string]bool{}
	for _, r := range obj["required"].([]any) {
		req[r.(string)] = true
	}
	if !req["id"] || !req["limit"] {
		t.Errorf("required set: got %v, want id and limit", obj["required"])
	}
}

// TestBuildUnknownOperation asserts an unknown operationId is a hard error
// so the build-pipeline generator fails loudly on a stale registry.
func TestBuildUnknownOperation_spec_15_2_1_1386(t *testing.T) {
	if _, err := mcpschemagen.BuildToolInputSchema([]byte(sampleDoc), "nope", mcpschemagen.Options{}); err == nil {
		t.Fatal("expected error for unknown operationId")
	}
}

// TestBuildDetectsRefCycle asserts a recursive component definition fails
// fast rather than overflowing the resolver stack.
func TestBuildDetectsRefCycle_spec_15_2_1_1386(t *testing.T) {
	if _, err := mcpschemagen.BuildToolInputSchema([]byte(sampleDoc), "cyclic", mcpschemagen.Options{}); err == nil {
		t.Fatal("expected $ref cycle error")
	}
}

// TestBuildDeterministic asserts repeated generation produces byte-identical
// output, which the drift guard relies on.
func TestBuildDeterministic_spec_15_2_1_1386(t *testing.T) {
	a, err := mcpschemagen.BuildToolInputSchema([]byte(sampleDoc), "createThing", mcpschemagen.Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := mcpschemagen.BuildToolInputSchema([]byte(sampleDoc), "createThing", mcpschemagen.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Errorf("generation not deterministic:\n%s\n%s", a, b)
	}
}
