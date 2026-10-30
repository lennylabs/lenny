// SPDX-License-Identifier: MIT

//go:build contract

// Pins the /v1/responses wire body against the published OpenAI
// OpenAPI schema, vendored at
// tests/testdata/openai_responses/schema/response.schema.json (see
// the README next to it for provenance). The scaffold suite in this
// package only checks the response against Lenny's own translator
// structs and the documented Translation Fidelity Matrix; neither of
// those catch a response that is well-formed per Lenny's model but
// violates the real OpenAI contract (a missing required key, a wrong
// enum value, a wrong type). This file closes that gap.
package rest_openai_responses_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: §15 ("Built-in adapter inventory": OpenResponsesAdapter at
// `/v1/responses`, protocol "Open Responses Specification", status V1;
// and "OpenResponsesAdapter covers both Open Responses-compliant
// clients and OpenAI Responses API clients ... OpenAI's Responses API
// is a proper superset of Open Responses").
// diagnosis: a failure here means the gateway's non-streaming
// Responses body no longer validates against OpenAI's own published
// `Response` object schema — an OpenAI Responses API client would
// fail to deserialize the response or reject it as malformed, even
// though Lenny's own translator round-trip tests still pass. This is
// the schema the OpenAI-Responses-API-client half of the superset
// claim above is checked against.
func TestRESTOpenAIResponsesMatchesPublishedSchema(t *testing.T) {
	sch := schematest.Compile(t, "tests/testdata/openai_responses/schema/response.schema.json")

	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	tsResp, _, _ := newResponsesServers(t, clock, "sess_resp_published_schema")

	body := readFixture(t, "openai_responses/simple/request.json")
	resp, raw := postJSON(t, tsResp.URL+"/v1/responses", "acme", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("responses status: %d, body=%s", resp.StatusCode, raw)
	}

	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if err := sch.Validate(instance); err != nil {
		t.Errorf("response does not validate against the published OpenAI Responses schema: %v", err)
	}
}
