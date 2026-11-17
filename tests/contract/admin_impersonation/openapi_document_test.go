// SPDX-License-Identifier: MIT

//go:build contract

// Validates the §15.1 DELETE /v1/admin/impersonation/{id} operation in the
// OpenAPI document the gateway serves. The document is hand-maintained for
// this path, so a client or SDK generated from it sees whatever the file
// declares rather than what the handler answers.

package admin_impersonation_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/openapi"
)

// errorSchemaRef is the shared error envelope every catalogued error
// response references.
const errorSchemaRef = "#/components/schemas/Error"

// documentedResponse is the subset of an OpenAPI response object the
// assertions read.
type documentedResponse struct {
	Content map[string]struct {
		Schema struct {
			Ref        string                     `json:"$ref"`
			Required   []string                   `json:"required"`
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"schema"`
	} `json:"content"`
}

// servedImpersonationDelete fetches the served document and returns the
// responses of the delete operation on /v1/admin/impersonation/{id}.
func servedImpersonationDelete(t *testing.T) map[string]documentedResponse {
	t.Helper()
	ts := httptest.NewServer(openapi.Handler())
	t.Cleanup(ts.Close)
	resp, err := http.Get(ts.URL + "/openapi.json")
	if err != nil {
		t.Fatalf("GET /openapi.json: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /openapi.json: status %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /openapi.json: %v", err)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			Responses map[string]documentedResponse `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode /openapi.json: %v", err)
	}
	op, ok := doc.Paths["/v1/admin/impersonation/{id}"]["delete"]
	if !ok {
		t.Fatal("served document has no delete operation on /v1/admin/impersonation/{id}")
	}
	return op.Responses
}

// spec: §15.1 (REST API: the DELETE /v1/admin/impersonation/{id} row), §13.3 (Credential Flow: Platform-admin impersonation, rule 3)
// diagnosis: a failure here means the served OpenAPI document declares a
// DELETE /v1/admin/impersonation/{id} contract the handler does not
// answer: a 204 with no body, a 200 body without impersonationSessionId and
// endedAt, or a 404 or 409 that does not reference the shared Error
// envelope. Fix paths./v1/admin/impersonation/{id}.delete in
// pkg/gateway/externalapi/openapi/openapi.json by hand; go generate does
// not regenerate this entry.
func TestServedDocumentImpersonationDeleteResponses_spec_15_1(t *testing.T) {
	responses := servedImpersonationDelete(t)

	if _, ok := responses["204"]; ok {
		t.Error("the delete operation declares 204; the handler answers 200 with a body")
	}
	ok200, has := responses["200"]
	if !has {
		t.Fatal("the delete operation declares no 200 response")
	}
	schema := ok200.Content["application/json"].Schema
	for _, field := range []string{"impersonationSessionId", "endedAt"} {
		if _, ok := schema.Properties[field]; !ok {
			t.Errorf("the 200 body declares no %s property", field)
		}
		if !slices.Contains(schema.Required, field) {
			t.Errorf("the 200 body does not require %s", field)
		}
	}
	if _, ok := schema.Properties["endReason"]; ok {
		t.Error("the 200 body declares endReason, which the response no longer carries")
	}
	for _, code := range []string{"404", "409"} {
		r, has := responses[code]
		if !has {
			t.Errorf("the delete operation declares no %s response", code)
			continue
		}
		if got := r.Content["application/json"].Schema.Ref; got != errorSchemaRef {
			t.Errorf("the %s response references %q, want %q", code, got, errorSchemaRef)
		}
	}
}
