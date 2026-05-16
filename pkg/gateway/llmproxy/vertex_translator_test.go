// SPDX-License-Identifier: MIT

package llmproxy_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/llmproxy"
)

func vertexTranslator() *llmproxy.VertexAITranslator {
	return &llmproxy.VertexAITranslator{Region: "us-east5", Project: "acme-prod"}
}

func TestVertexProviderIdentifier(t *testing.T) {
	if got := vertexTranslator().Provider(); got != "vertex_ai" {
		t.Errorf("Provider() = %q, want vertex_ai", got)
	}
}

func TestVertexTranslateRequestBuildsURLAndTransformsBody(t *testing.T) {
	req := llmproxy.Request{
		Dialect: llmproxy.DialectAnthropic,
		Body:    []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"max_tokens":100}`),
	}
	up, err := vertexTranslator().TranslateRequest(req, "gcp-access-token")
	if err != nil {
		t.Fatalf("TranslateRequest: %v", err)
	}
	want := "https://us-east5-aiplatform.googleapis.com/v1/projects/acme-prod/locations/us-east5/" +
		"publishers/anthropic/models/claude-sonnet-4:rawPredict"
	if up.URL != want {
		t.Errorf("URL = %q, want %q", up.URL, want)
	}
	if up.Header["authorization"] != "Bearer gcp-access-token" {
		t.Errorf("authorization header = %q, want the injected bearer token", up.Header["authorization"])
	}
	// The body moved model out and gained anthropic_version.
	var body map[string]json.RawMessage
	if err := json.Unmarshal(up.Body, &body); err != nil {
		t.Fatalf("upstream body is not JSON: %v", err)
	}
	if _, ok := body["model"]; ok {
		t.Error("upstream body still carries the model field")
	}
	var version string
	_ = json.Unmarshal(body["anthropic_version"], &version)
	if version != "vertex-2023-10-16" {
		t.Errorf("anthropic_version = %q, want vertex-2023-10-16", version)
	}
	if _, ok := body["messages"]; !ok {
		t.Error("upstream body dropped the messages field")
	}
}

func TestVertexTranslateRequestEscapesModelInURL(t *testing.T) {
	req := llmproxy.Request{
		Dialect: llmproxy.DialectAnthropic,
		Body:    []byte(`{"model":"../../evil","messages":[]}`),
	}
	up, err := vertexTranslator().TranslateRequest(req, "gcp-access-token")
	if err != nil {
		t.Fatalf("TranslateRequest: %v", err)
	}
	if strings.Contains(up.URL, "../") {
		t.Errorf("URL %q contains an unescaped path-traversal segment", up.URL)
	}
}

func TestVertexTranslateRequestRejectsNonAnthropicDialect(t *testing.T) {
	req := llmproxy.Request{Dialect: llmproxy.DialectOpenAI, Body: []byte(`{}`)}
	_, err := vertexTranslator().TranslateRequest(req, "gcp-access-token")
	if code := translationErrorType(t, err); code != llmproxy.ErrUnsupportedField {
		t.Errorf("error type = %q, want unsupported_field", code)
	}
}

func TestVertexTranslateRequestRejectsMissingCredential(t *testing.T) {
	req := llmproxy.Request{
		Dialect: llmproxy.DialectAnthropic,
		Body:    []byte(`{"model":"claude-sonnet-4","messages":[]}`),
	}
	_, err := vertexTranslator().TranslateRequest(req, "")
	if code := translationErrorType(t, err); code != llmproxy.ErrAuthFailed {
		t.Errorf("error type = %q, want auth_failed", code)
	}
}

func TestVertexTranslateRequestRejectsMissingModel(t *testing.T) {
	req := llmproxy.Request{
		Dialect: llmproxy.DialectAnthropic,
		Body:    []byte(`{"messages":[]}`),
	}
	_, err := vertexTranslator().TranslateRequest(req, "gcp-access-token")
	if code := translationErrorType(t, err); code != llmproxy.ErrSchemaMismatch {
		t.Errorf("error type = %q, want schema_mismatch", code)
	}
}

func TestVertexTranslateResponseExtractsUsage(t *testing.T) {
	resp := llmproxy.UpstreamResponse{
		StatusCode: 200,
		Body:       []byte(`{"content":[],"usage":{"input_tokens":300,"output_tokens":80}}`),
	}
	out, err := vertexTranslator().TranslateResponse(llmproxy.DialectAnthropic, resp)
	if err != nil {
		t.Fatalf("TranslateResponse: %v", err)
	}
	if out.Usage.InputTokens != 300 || out.Usage.OutputTokens != 80 {
		t.Errorf("usage = %+v, want input=300 output=80", out.Usage)
	}
}

func TestVertexTranslateResponseMapsUpstreamErrors(t *testing.T) {
	for status, want := range map[int]llmproxy.ErrorType{
		403: llmproxy.ErrAuthFailed,
		404: llmproxy.ErrUpstream4xx,
		500: llmproxy.ErrUpstream5xx,
	} {
		resp := llmproxy.UpstreamResponse{StatusCode: status, Body: []byte(`{}`)}
		_, err := vertexTranslator().TranslateResponse(llmproxy.DialectAnthropic, resp)
		if code := translationErrorType(t, err); code != want {
			t.Errorf("status %d: error type = %q, want %q", status, code, want)
		}
	}
}
