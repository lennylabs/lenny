// SPDX-License-Identifier: MIT

package llmproxy

import (
	"encoding/json"
	"strings"
	"testing"
)

// spec: §4.9 (openai_direct translator: request passthrough)
// diagnosis: an OpenAI Chat Completions request in the openai
// dialect passes through unchanged with Authorization: Bearer
// injected and the api.openai.com path appended.
func TestOpenAIDirectTranslateRequestPassthrough(t *testing.T) {
	tr := &OpenAIDirectTranslator{}
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	up, err := tr.TranslateRequest(Request{Dialect: DialectOpenAI, Body: body}, "sk-test")
	if err != nil {
		t.Fatalf("TranslateRequest: %v", err)
	}
	if up.URL != "https://api.openai.com/v1/chat/completions" {
		t.Errorf("URL = %q, want https://api.openai.com/v1/chat/completions", up.URL)
	}
	if up.Header["authorization"] != "Bearer sk-test" {
		t.Errorf("authorization header = %q, want Bearer sk-test", up.Header["authorization"])
	}
	if up.Header["content-type"] != "application/json" {
		t.Errorf("content-type = %q, want application/json", up.Header["content-type"])
	}
	if string(up.Body) != string(body) {
		t.Errorf("body was rewritten: got %q, want %q", string(up.Body), string(body))
	}
}

// spec: §4.9 (OpenAI-Organization header is honored when set)
// diagnosis: the Organization field on the translator becomes the
// openai-organization header so a deployer can scope traffic to a
// specific organization without mutating the pod's request.
func TestOpenAIDirectInjectsOrganizationHeader(t *testing.T) {
	tr := &OpenAIDirectTranslator{Organization: "org_acme"}
	body := []byte(`{"model":"gpt-4o","messages":[]}`)
	up, err := tr.TranslateRequest(Request{Dialect: DialectOpenAI, Body: body}, "sk-test")
	if err != nil {
		t.Fatalf("TranslateRequest: %v", err)
	}
	if up.Header["openai-organization"] != "org_acme" {
		t.Errorf("openai-organization header = %q, want org_acme", up.Header["openai-organization"])
	}
}

// spec: §4.9 (BaseURL override)
// diagnosis: a non-empty BaseURL replaces api.openai.com so tests
// point the translator at a mock provider.
func TestOpenAIDirectBaseURLOverride(t *testing.T) {
	tr := &OpenAIDirectTranslator{BaseURL: "http://127.0.0.1:9999/proxy/"}
	body := []byte(`{"model":"gpt-4o","messages":[]}`)
	up, err := tr.TranslateRequest(Request{Dialect: DialectOpenAI, Body: body}, "sk-test")
	if err != nil {
		t.Fatalf("TranslateRequest: %v", err)
	}
	if up.URL != "http://127.0.0.1:9999/proxy/v1/chat/completions" {
		t.Errorf("URL = %q, want override base with /v1/chat/completions", up.URL)
	}
}

// spec: §4.9 (request rejection on dialect mismatch)
// diagnosis: a request in a non-openai dialect is rejected with
// ErrUnsupportedField; the upstream URL is never built for the
// wrong dialect.
func TestOpenAIDirectRejectsAnthropicDialect(t *testing.T) {
	tr := &OpenAIDirectTranslator{}
	body := []byte(`{"model":"claude","messages":[]}`)
	_, err := tr.TranslateRequest(Request{Dialect: DialectAnthropic, Body: body}, "sk-test")
	if err == nil {
		t.Fatal("expected an error for anthropic dialect against openai_direct")
	}
	terr, ok := err.(*TranslationError)
	if !ok || terr.Type != ErrUnsupportedField {
		t.Errorf("error = %v, want ErrUnsupportedField", err)
	}
}

// spec: §4.9 (missing credential is fail-closed)
// diagnosis: a translator must reject a request with no upstream
// credential as ErrAuthFailed so the proxy returns the
// CREDENTIAL_REQUIRED diagnosis to the pod.
func TestOpenAIDirectRejectsMissingAPIKey(t *testing.T) {
	tr := &OpenAIDirectTranslator{}
	body := []byte(`{"model":"gpt-4o","messages":[]}`)
	_, err := tr.TranslateRequest(Request{Dialect: DialectOpenAI, Body: body}, "")
	if err == nil {
		t.Fatal("expected an error for empty apiKey")
	}
	terr, ok := err.(*TranslationError)
	if !ok || terr.Type != ErrAuthFailed {
		t.Errorf("error = %v, want ErrAuthFailed", err)
	}
}

// spec: §4.9 (schema validation on required OpenAI fields)
// diagnosis: a request missing the required model or messages field
// is rejected at the translator boundary so the upstream never sees
// a malformed body.
func TestOpenAIDirectRejectsMissingRequiredFields(t *testing.T) {
	tr := &OpenAIDirectTranslator{}
	cases := []struct {
		name string
		body string
	}{
		{"missing_model", `{"messages":[]}`},
		{"missing_messages", `{"model":"gpt-4o"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := tr.TranslateRequest(Request{Dialect: DialectOpenAI, Body: []byte(c.body)}, "sk-test")
			if err == nil {
				t.Fatal("expected a schema-mismatch error")
			}
			terr, ok := err.(*TranslationError)
			if !ok || terr.Type != ErrSchemaMismatch {
				t.Errorf("error = %v, want ErrSchemaMismatch", err)
			}
		})
	}
}

// spec: §4.9 (response usage extraction)
// diagnosis: a successful upstream response yields a Usage with
// prompt_tokens and completion_tokens populated from the OpenAI
// envelope.
func TestOpenAIDirectTranslateResponseExtractsUsage(t *testing.T) {
	tr := &OpenAIDirectTranslator{}
	respBody, _ := json.Marshal(map[string]any{
		"choices": []any{},
		"usage": map[string]int{
			"prompt_tokens":     42,
			"completion_tokens": 7,
		},
	})
	r, err := tr.TranslateResponse(DialectOpenAI, UpstreamResponse{StatusCode: 200, Body: respBody})
	if err != nil {
		t.Fatalf("TranslateResponse: %v", err)
	}
	if r.Usage.InputTokens != 42 {
		t.Errorf("InputTokens = %d, want 42", r.Usage.InputTokens)
	}
	if r.Usage.OutputTokens != 7 {
		t.Errorf("OutputTokens = %d, want 7", r.Usage.OutputTokens)
	}
}

// spec: §4.9 (upstream 401 maps to ErrAuthFailed)
// diagnosis: a 401 upstream response is a credential-class failure;
// the translator maps it through the §4.9 error taxonomy so the
// proxy returns CREDENTIAL_REJECTED to the pod.
func TestOpenAIDirectMapsUpstream401(t *testing.T) {
	tr := &OpenAIDirectTranslator{}
	_, err := tr.TranslateResponse(DialectOpenAI, UpstreamResponse{
		StatusCode: 401,
		Body:       []byte(`{"error":{"message":"invalid api key"}}`),
	})
	if err == nil {
		t.Fatal("expected an upstream-status error")
	}
	terr, ok := err.(*TranslationError)
	if !ok {
		t.Fatalf("error type = %T, want *TranslationError", err)
	}
	if terr.Type != ErrAuthFailed {
		t.Errorf("error type = %q, want ErrAuthFailed", terr.Type)
	}
	if !strings.Contains(terr.Message, "401") {
		t.Errorf("error message does not mention 401: %q", terr.Message)
	}
}
