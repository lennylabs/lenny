// SPDX-License-Identifier: MIT

package llmproxy_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
)

func azureTranslator() *llmproxy.AzureOpenAITranslator {
	return &llmproxy.AzureOpenAITranslator{
		Endpoint:   "https://acme.openai.azure.com",
		APIVersion: "2024-10-21",
	}
}

// translationErrorType extracts the §4.9 error_type from a translator
// error, failing the test when err is not a *TranslationError.
func translationErrorType(t *testing.T, err error) llmproxy.ErrorType {
	t.Helper()
	var te *llmproxy.TranslationError
	if !errors.As(err, &te) {
		t.Fatalf("error %v is not a *TranslationError", err)
	}
	return te.Type
}

func TestAzureProviderIdentifier(t *testing.T) {
	if got := azureTranslator().Provider(); got != "azure_openai" {
		t.Errorf("Provider() = %q, want azure_openai", got)
	}
}

func TestAzureTranslateRequestBuildsDeploymentURL(t *testing.T) {
	req := llmproxy.Request{
		Dialect: llmproxy.DialectOpenAI,
		Body:    []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`),
	}
	up, err := azureTranslator().TranslateRequest(req, "secret-key")
	if err != nil {
		t.Fatalf("TranslateRequest: %v", err)
	}
	want := "https://acme.openai.azure.com/openai/deployments/gpt-4o/chat/completions?api-version=2024-10-21"
	if up.URL != want {
		t.Errorf("URL = %q, want %q", up.URL, want)
	}
	if up.Header["api-key"] != "secret-key" {
		t.Errorf("api-key header = %q, want the injected credential", up.Header["api-key"])
	}
	if string(up.Body) != string(req.Body) {
		t.Errorf("body was not passed through unchanged")
	}
}

func TestAzureTranslateRequestEscapesModelInURL(t *testing.T) {
	// A crafted model value must not break out of the path segment.
	req := llmproxy.Request{
		Dialect: llmproxy.DialectOpenAI,
		Body:    []byte(`{"model":"../../evil?x","messages":[]}`),
	}
	up, err := azureTranslator().TranslateRequest(req, "secret-key")
	if err != nil {
		t.Fatalf("TranslateRequest: %v", err)
	}
	if strings.Contains(up.URL, "../") {
		t.Errorf("URL %q contains an unescaped path-traversal segment", up.URL)
	}
	if strings.Contains(up.URL, "evil?x") {
		t.Errorf("URL %q contains an unescaped query break-out", up.URL)
	}
}

func TestAzureTranslateRequestRejectsNonOpenAIDialect(t *testing.T) {
	req := llmproxy.Request{Dialect: llmproxy.DialectAnthropic, Body: []byte(`{}`)}
	_, err := azureTranslator().TranslateRequest(req, "secret-key")
	if code := translationErrorType(t, err); code != llmproxy.ErrUnsupportedField {
		t.Errorf("error type = %q, want unsupported_field", code)
	}
}

func TestAzureTranslateRequestRejectsMissingCredential(t *testing.T) {
	req := llmproxy.Request{
		Dialect: llmproxy.DialectOpenAI,
		Body:    []byte(`{"model":"gpt-4o","messages":[]}`),
	}
	_, err := azureTranslator().TranslateRequest(req, "")
	if code := translationErrorType(t, err); code != llmproxy.ErrAuthFailed {
		t.Errorf("error type = %q, want auth_failed", code)
	}
}

func TestAzureTranslateRequestRejectsMissingModel(t *testing.T) {
	req := llmproxy.Request{
		Dialect: llmproxy.DialectOpenAI,
		Body:    []byte(`{"messages":[]}`),
	}
	_, err := azureTranslator().TranslateRequest(req, "secret-key")
	if code := translationErrorType(t, err); code != llmproxy.ErrSchemaMismatch {
		t.Errorf("error type = %q, want schema_mismatch", code)
	}
}

func TestAzureTranslateResponseExtractsUsage(t *testing.T) {
	resp := llmproxy.UpstreamResponse{
		StatusCode: 200,
		Body:       []byte(`{"choices":[],"usage":{"prompt_tokens":120,"completion_tokens":45}}`),
	}
	out, err := azureTranslator().TranslateResponse(llmproxy.DialectOpenAI, resp)
	if err != nil {
		t.Fatalf("TranslateResponse: %v", err)
	}
	if out.Usage.InputTokens != 120 || out.Usage.OutputTokens != 45 {
		t.Errorf("usage = %+v, want input=120 output=45", out.Usage)
	}
}

func TestAzureTranslateResponseMapsUpstreamErrors(t *testing.T) {
	for status, want := range map[int]llmproxy.ErrorType{
		401: llmproxy.ErrAuthFailed,
		429: llmproxy.ErrAuthFailed,
		400: llmproxy.ErrUpstream4xx,
		503: llmproxy.ErrUpstream5xx,
	} {
		resp := llmproxy.UpstreamResponse{StatusCode: status, Body: []byte(`{}`)}
		_, err := azureTranslator().TranslateResponse(llmproxy.DialectOpenAI, resp)
		if code := translationErrorType(t, err); code != want {
			t.Errorf("status %d: error type = %q, want %q", status, code, want)
		}
	}
}
