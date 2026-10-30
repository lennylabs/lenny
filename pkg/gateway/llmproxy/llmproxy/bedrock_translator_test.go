// SPDX-License-Identifier: MIT

package llmproxy_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
)

func bedrockTranslator() *llmproxy.AWSBedrockTranslator {
	return &llmproxy.AWSBedrockTranslator{Region: "us-east-1"}
}

func TestBedrockProviderIdentifier(t *testing.T) {
	if got := bedrockTranslator().Provider(); got != "aws_bedrock" {
		t.Errorf("Provider() = %q, want aws_bedrock", got)
	}
}

func TestBedrockTranslateRequestBuildsURLAndTransformsBody(t *testing.T) {
	req := llmproxy.Request{
		Dialect: llmproxy.DialectAnthropic,
		Body:    []byte(`{"model":"anthropic.claude-sonnet-4","messages":[{"role":"user","content":"hi"}]}`),
	}
	up, err := bedrockTranslator().TranslateRequest(req, "bedrock-api-key")
	if err != nil {
		t.Fatalf("TranslateRequest: %v", err)
	}
	want := "https://bedrock-runtime.us-east-1.amazonaws.com/model/anthropic.claude-sonnet-4/invoke"
	if up.URL != want {
		t.Errorf("URL = %q, want %q", up.URL, want)
	}
	if up.Header["authorization"] != "Bearer bedrock-api-key" {
		t.Errorf("authorization header = %q, want the injected bearer token", up.Header["authorization"])
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(up.Body, &body); err != nil {
		t.Fatalf("upstream body is not JSON: %v", err)
	}
	if _, ok := body["model"]; ok {
		t.Error("upstream body still carries the model field")
	}
	var version string
	_ = json.Unmarshal(body["anthropic_version"], &version)
	if version != "bedrock-2023-05-31" {
		t.Errorf("anthropic_version = %q, want bedrock-2023-05-31", version)
	}
}

func TestBedrockTranslateRequestEscapesModelInURL(t *testing.T) {
	req := llmproxy.Request{
		Dialect: llmproxy.DialectAnthropic,
		Body:    []byte(`{"model":"../../evil","messages":[]}`),
	}
	up, err := bedrockTranslator().TranslateRequest(req, "bedrock-api-key")
	if err != nil {
		t.Fatalf("TranslateRequest: %v", err)
	}
	if strings.Contains(up.URL, "../") {
		t.Errorf("URL %q contains an unescaped path-traversal segment", up.URL)
	}
}

func TestBedrockTranslateRequestRejectsNonAnthropicDialect(t *testing.T) {
	req := llmproxy.Request{Dialect: llmproxy.DialectOpenAI, Body: []byte(`{}`)}
	_, err := bedrockTranslator().TranslateRequest(req, "bedrock-api-key")
	if code := translationErrorType(t, err); code != llmproxy.ErrUnsupportedField {
		t.Errorf("error type = %q, want unsupported_field", code)
	}
}

func TestBedrockTranslateRequestRejectsMissingCredential(t *testing.T) {
	req := llmproxy.Request{
		Dialect: llmproxy.DialectAnthropic,
		Body:    []byte(`{"model":"anthropic.claude-sonnet-4","messages":[]}`),
	}
	_, err := bedrockTranslator().TranslateRequest(req, "")
	if code := translationErrorType(t, err); code != llmproxy.ErrAuthFailed {
		t.Errorf("error type = %q, want auth_failed", code)
	}
}

func TestBedrockTranslateRequestRejectsMissingModel(t *testing.T) {
	req := llmproxy.Request{
		Dialect: llmproxy.DialectAnthropic,
		Body:    []byte(`{"messages":[]}`),
	}
	_, err := bedrockTranslator().TranslateRequest(req, "bedrock-api-key")
	if code := translationErrorType(t, err); code != llmproxy.ErrSchemaMismatch {
		t.Errorf("error type = %q, want schema_mismatch", code)
	}
}

func TestBedrockTranslateResponseExtractsUsage(t *testing.T) {
	resp := llmproxy.UpstreamResponse{
		StatusCode: 200,
		Body:       []byte(`{"content":[],"usage":{"input_tokens":210,"output_tokens":60}}`),
	}
	out, err := bedrockTranslator().TranslateResponse(llmproxy.DialectAnthropic, resp)
	if err != nil {
		t.Fatalf("TranslateResponse: %v", err)
	}
	if out.Usage.InputTokens != 210 || out.Usage.OutputTokens != 60 {
		t.Errorf("usage = %+v, want input=210 output=60", out.Usage)
	}
}

func TestBedrockTranslateResponseMapsUpstreamErrors(t *testing.T) {
	for status, want := range map[int]llmproxy.ErrorType{
		401: llmproxy.ErrAuthFailed,
		400: llmproxy.ErrUpstream4xx,
		502: llmproxy.ErrUpstream5xx,
	} {
		resp := llmproxy.UpstreamResponse{StatusCode: status, Body: []byte(`{}`)}
		_, err := bedrockTranslator().TranslateResponse(llmproxy.DialectAnthropic, resp)
		if code := translationErrorType(t, err); code != want {
			t.Errorf("status %d: error type = %q, want %q", status, code, want)
		}
	}
}
