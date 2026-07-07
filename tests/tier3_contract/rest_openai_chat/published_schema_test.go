// SPDX-License-Identifier: MIT

//go:build contract

// Pins the /v1/chat/completions wire body against the published
// OpenAI OpenAPI schema, vendored at
// tests/testdata/openai_chat/schema/create-chat-completion-response.schema.json
// (see the README next to it for provenance). The scaffold suite in
// this package only checks the response against Lenny's own
// translator structs and the documented Translation Fidelity Matrix;
// neither of those catch a response that is well-formed per Lenny's
// model but violates the real OpenAI contract (a missing required
// key, a wrong enum value, a wrong type). This file closes that gap.
package rest_openai_chat_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: §15 ("Built-in adapter inventory": OpenAICompletionsAdapter at
// `/v1/chat/completions`, protocol "OpenAI Chat Completions", status V1).
// diagnosis: a failure here means the gateway's non-streaming Chat
// Completions response no longer validates against OpenAI's own
// published CreateChatCompletionResponse schema — a strict,
// schema-generated OpenAI client (for example a Stainless-generated
// SDK) would fail to deserialize the response or reject it as
// malformed, even though Lenny's own translator round-trip tests
// still pass.
func TestRESTOpenAIChatMatchesPublishedSchema(t *testing.T) {
	sch := schematest.Compile(t, "tests/testdata/openai_chat/schema/create-chat-completion-response.schema.json")

	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	tsOpenAI, _, _ := newOpenAIServers(t, clock, "sess_oa_published_schema")

	body := readFixture(t, "openai_chat/simple/request.json")
	resp, raw := postJSON(t, tsOpenAI.URL+"/v1/chat/completions", "acme", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("OpenAI chat status: %d, body=%s", resp.StatusCode, raw)
	}

	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if err := sch.Validate(instance); err != nil {
		t.Errorf("response does not validate against the published OpenAI Chat Completions schema: %v", err)
	}
}
