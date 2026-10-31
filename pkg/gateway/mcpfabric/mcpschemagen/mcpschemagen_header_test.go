// SPDX-License-Identifier: MIT

package mcpschemagen_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcpschemagen"
)

// headerDoc is an OpenAPI document whose finalize operation requires a
// header parameter alongside its path parameter and request body, and
// declares an optional header and a query parameter.
const headerDoc = `{
  "paths": {
    "/v1/sessions/{id}/finalize": {
      "post": {
        "operationId": "finalize",
        "parameters": [
          { "name": "id", "in": "path", "required": true, "schema": { "type": "string" } },
          { "name": "X-Lenny-Upload-Token", "in": "header", "required": true, "schema": { "type": "string" } },
          { "name": "X-Optional", "in": "header", "schema": { "type": "string" } },
          { "name": "q", "in": "query", "schema": { "type": "string" } }
        ],
        "requestBody": {
          "content": { "application/json": { "schema": { "type": "object", "properties": { "workspacePlan": { "type": ["object", "null"] } } } } }
        }
      }
    }
  }
}`

// TestBuildHeaderParams_spec_15_2_1 asserts the Header option: a header
// parameter becomes an MCP argument only when the tool names it, it keeps
// the OpenAPI required flag, a rename applies to it, and naming a parameter
// that is not a header of the operation fails the generation.
//
// spec: 15.2.1 (REST/MCP Consistency Contract, item 4), 7.4 (Upload Safety, Upload authorization)
func TestBuildHeaderParams_spec_15_2_1(t *testing.T) {
	cases := []struct {
		name    string
		opts    mcpschemagen.Options
		want    string
		wantErr string
	}{
		{
			name: "header left out by default",
			opts: mcpschemagen.Options{Params: map[string]mcpschemagen.ParamOptions{"id": {Name: "sessionId"}}},
			want: `{"properties":{"sessionId":{"type":"string"},"workspacePlan":{"type":["object","null"]}},"required":["sessionId"],"type":"object"}`,
		},
		{
			name: "required header named and renamed",
			opts: mcpschemagen.Options{
				Params: map[string]mcpschemagen.ParamOptions{
					"id":                   {Name: "sessionId"},
					"X-Lenny-Upload-Token": {Name: "uploadToken", Description: "token"},
				},
				Header: []string{"X-Lenny-Upload-Token"},
			},
			want: `{"properties":{"sessionId":{"type":"string"},"uploadToken":{"description":"token","type":"string"},"workspacePlan":{"type":["object","null"]}},"required":["sessionId","uploadToken"],"type":"object"}`,
		},
		{
			name: "optional header named stays optional",
			opts: mcpschemagen.Options{
				Params: map[string]mcpschemagen.ParamOptions{"id": {Name: "sessionId"}},
				Header: []string{"X-Optional"},
			},
			want: `{"properties":{"X-Optional":{"type":"string"},"sessionId":{"type":"string"},"workspacePlan":{"type":["object","null"]}},"required":["sessionId"],"type":"object"}`,
		},
		{
			name:    "query parameter named as header",
			opts:    mcpschemagen.Options{Header: []string{"q"}},
			wantErr: "not a header parameter",
		},
		{
			name:    "undeclared header named",
			opts:    mcpschemagen.Options{Header: []string{"X-Missing"}},
			wantErr: "not a header parameter",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := mcpschemagen.BuildToolInputSchema([]byte(headerDoc), "finalize", tc.opts)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != tc.want {
				t.Errorf("schema =\n %s\nwant\n %s", raw, tc.want)
			}
		})
	}
}
