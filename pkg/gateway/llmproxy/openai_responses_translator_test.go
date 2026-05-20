// SPDX-License-Identifier: MIT

package llmproxy

import (
	"encoding/json"
	"testing"
)

// spec: §4.9 (openai_responses translator: /v1/responses passthrough)
// diagnosis: a request in the openai_responses dialect routes to
// /v1/responses with Authorization: Bearer injected; the body
// passes through unchanged.
func TestOpenAIResponsesTranslateRequestPassthrough(t *testing.T) {
	tr := &OpenAIResponsesTranslator{}
	body := []byte(`{"model":"gpt-4o","input":"hello"}`)
	up, err := tr.TranslateRequest(Request{Dialect: DialectOpenAIResponses, Body: body}, "sk-test")
	if err != nil {
		t.Fatalf("TranslateRequest: %v", err)
	}
	if up.URL != "https://api.openai.com/v1/responses" {
		t.Errorf("URL = %q, want https://api.openai.com/v1/responses", up.URL)
	}
	if up.Header["authorization"] != "Bearer sk-test" {
		t.Errorf("authorization header = %q, want Bearer sk-test", up.Header["authorization"])
	}
	if string(up.Body) != string(body) {
		t.Errorf("body rewritten: got %q, want %q", string(up.Body), string(body))
	}
}

// spec: §4.9 (organization header)
func TestOpenAIResponsesInjectsOrganization(t *testing.T) {
	tr := &OpenAIResponsesTranslator{Organization: "org_acme"}
	body := []byte(`{"model":"gpt-4o","input":"hi"}`)
	up, err := tr.TranslateRequest(Request{Dialect: DialectOpenAIResponses, Body: body}, "sk-test")
	if err != nil {
		t.Fatalf("TranslateRequest: %v", err)
	}
	if up.Header["openai-organization"] != "org_acme" {
		t.Errorf("openai-organization = %q, want org_acme", up.Header["openai-organization"])
	}
}

// spec: §4.9 (BaseURL override)
func TestOpenAIResponsesBaseURLOverride(t *testing.T) {
	tr := &OpenAIResponsesTranslator{BaseURL: "http://127.0.0.1:9999"}
	body := []byte(`{"model":"gpt-4o","input":"hi"}`)
	up, err := tr.TranslateRequest(Request{Dialect: DialectOpenAIResponses, Body: body}, "sk-test")
	if err != nil {
		t.Fatalf("TranslateRequest: %v", err)
	}
	if up.URL != "http://127.0.0.1:9999/v1/responses" {
		t.Errorf("URL = %q, want override base with /v1/responses", up.URL)
	}
}

// spec: §4.9 (dialect rejection)
// diagnosis: the openai_responses translator rejects requests in
// openai (Chat Completions) or anthropic dialects so the dispatcher
// cannot accidentally route the wrong dialect at /v1/responses.
func TestOpenAIResponsesRejectsWrongDialect(t *testing.T) {
	tr := &OpenAIResponsesTranslator{}
	cases := []Dialect{DialectAnthropic, DialectOpenAI, Dialect("unknown")}
	for _, d := range cases {
		t.Run(string(d), func(t *testing.T) {
			_, err := tr.TranslateRequest(Request{Dialect: d, Body: []byte(`{"model":"x","input":"y"}`)}, "sk-test")
			if err == nil {
				t.Fatalf("expected an error for dialect %q", d)
			}
			terr, ok := err.(*TranslationError)
			if !ok || terr.Type != ErrUnsupportedField {
				t.Errorf("error = %v, want ErrUnsupportedField", err)
			}
		})
	}
}

// spec: §4.9 (schema validation: input is required)
// diagnosis: the Responses API requires the input field. A request
// that carries messages (the Chat Completions shape) at this
// endpoint is rejected at the translator boundary.
func TestOpenAIResponsesRejectsMessagesShape(t *testing.T) {
	tr := &OpenAIResponsesTranslator{}
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	_, err := tr.TranslateRequest(Request{Dialect: DialectOpenAIResponses, Body: body}, "sk-test")
	if err == nil {
		t.Fatal("expected ErrSchemaMismatch when input is missing")
	}
	terr, ok := err.(*TranslationError)
	if !ok || terr.Type != ErrSchemaMismatch {
		t.Errorf("error = %v, want ErrSchemaMismatch", err)
	}
}

// spec: §4.9 (missing credential)
func TestOpenAIResponsesRejectsMissingKey(t *testing.T) {
	tr := &OpenAIResponsesTranslator{}
	body := []byte(`{"model":"gpt-4o","input":"hi"}`)
	_, err := tr.TranslateRequest(Request{Dialect: DialectOpenAIResponses, Body: body}, "")
	if err == nil {
		t.Fatal("expected ErrAuthFailed when apiKey is empty")
	}
	terr, ok := err.(*TranslationError)
	if !ok || terr.Type != ErrAuthFailed {
		t.Errorf("error = %v, want ErrAuthFailed", err)
	}
}

// spec: §4.9 (response usage extraction)
// diagnosis: the Responses API envelope uses the same usage block
// (prompt_tokens, completion_tokens) as Chat Completions, so the
// shared extractor populates Usage correctly.
func TestOpenAIResponsesTranslateResponseExtractsUsage(t *testing.T) {
	tr := &OpenAIResponsesTranslator{}
	respBody, _ := json.Marshal(map[string]any{
		"id":     "resp_123",
		"output": []any{},
		"usage": map[string]int{
			"prompt_tokens":     100,
			"completion_tokens": 25,
		},
	})
	r, err := tr.TranslateResponse(DialectOpenAIResponses, UpstreamResponse{StatusCode: 200, Body: respBody})
	if err != nil {
		t.Fatalf("TranslateResponse: %v", err)
	}
	if r.Usage.InputTokens != 100 || r.Usage.OutputTokens != 25 {
		t.Errorf("Usage = %+v, want {Input:100 Output:25}", r.Usage)
	}
}

// spec: §4.9 (upstream auth failure)
func TestOpenAIResponsesMapsUpstream403(t *testing.T) {
	tr := &OpenAIResponsesTranslator{}
	_, err := tr.TranslateResponse(DialectOpenAIResponses, UpstreamResponse{
		StatusCode: 403, Body: []byte(`{"error":{"message":"forbidden"}}`),
	})
	if err == nil {
		t.Fatal("expected an upstream-status error")
	}
	terr, ok := err.(*TranslationError)
	if !ok || terr.Type != ErrAuthFailed {
		t.Errorf("error type = %v, want ErrAuthFailed", err)
	}
}
