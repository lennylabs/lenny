// SPDX-License-Identifier: MIT

package llmproxy_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/llmproxy"
)

// spec: §4.9 — the anthropic_direct translator for the Anthropic
// Messages proxy dialect.

const sampleRequest = `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"hi"}]}`

// wantErrorType asserts err is a *TranslationError with the given type.
func wantErrorType(t *testing.T, err error, want llmproxy.ErrorType) {
	t.Helper()
	var te *llmproxy.TranslationError
	if !errors.As(err, &te) {
		t.Fatalf("error = %v, want a *TranslationError", err)
	}
	if te.Type != want {
		t.Errorf("error type = %q, want %q", te.Type, want)
	}
}

func TestAnthropicDirectProvider(t *testing.T) {
	tr := &llmproxy.AnthropicDirectTranslator{}
	if tr.Provider() != llmproxy.ProviderAnthropicDirect {
		t.Errorf("Provider() = %q, want %q", tr.Provider(), llmproxy.ProviderAnthropicDirect)
	}
}

func TestTranslateRequestInjectsCredentialAndDefaultVersion(t *testing.T) {
	tr := &llmproxy.AnthropicDirectTranslator{DefaultAnthropicVersion: "2023-06-01"}
	up, err := tr.TranslateRequest(llmproxy.Request{
		Dialect: llmproxy.DialectAnthropic,
		Body:    []byte(sampleRequest),
	}, "sk-ant-secret")
	if err != nil {
		t.Fatalf("TranslateRequest: %v", err)
	}
	if up.URL != "https://api.anthropic.com/v1/messages" {
		t.Errorf("URL = %q, want the Anthropic Messages endpoint", up.URL)
	}
	if string(up.Body) != sampleRequest {
		t.Errorf("body = %q, want the request passed through unchanged", up.Body)
	}
	if up.Header["x-api-key"] != "sk-ant-secret" {
		t.Errorf("x-api-key = %q, want the injected upstream credential", up.Header["x-api-key"])
	}
	if up.Header["anthropic-version"] != "2023-06-01" {
		t.Errorf("anthropic-version = %q, want the configured default", up.Header["anthropic-version"])
	}
	if up.Header["content-type"] != "application/json" {
		t.Errorf("content-type = %q, want application/json", up.Header["content-type"])
	}
}

func TestTranslateRequestForwardsPodAnthropicVersion(t *testing.T) {
	tr := &llmproxy.AnthropicDirectTranslator{DefaultAnthropicVersion: "2023-06-01"}
	up, err := tr.TranslateRequest(llmproxy.Request{
		Dialect:          llmproxy.DialectAnthropic,
		Body:             []byte(sampleRequest),
		AnthropicVersion: "2099-12-31",
	}, "sk-ant-secret")
	if err != nil {
		t.Fatalf("TranslateRequest: %v", err)
	}
	if up.Header["anthropic-version"] != "2099-12-31" {
		t.Errorf("anthropic-version = %q, want the pod-supplied version", up.Header["anthropic-version"])
	}
}

func TestTranslateRequestRejectsMissingAnthropicVersion(t *testing.T) {
	tr := &llmproxy.AnthropicDirectTranslator{} // no default configured
	_, err := tr.TranslateRequest(llmproxy.Request{
		Dialect: llmproxy.DialectAnthropic,
		Body:    []byte(sampleRequest),
	}, "sk-ant-secret")
	wantErrorType(t, err, llmproxy.ErrSchemaMismatch)
}

func TestTranslateRequestRejectsMissingCredential(t *testing.T) {
	tr := &llmproxy.AnthropicDirectTranslator{DefaultAnthropicVersion: "2023-06-01"}
	_, err := tr.TranslateRequest(llmproxy.Request{
		Dialect: llmproxy.DialectAnthropic,
		Body:    []byte(sampleRequest),
	}, "")
	wantErrorType(t, err, llmproxy.ErrAuthFailed)
}

func TestTranslateRequestRejectsMalformedBody(t *testing.T) {
	tr := &llmproxy.AnthropicDirectTranslator{DefaultAnthropicVersion: "2023-06-01"}
	_, err := tr.TranslateRequest(llmproxy.Request{
		Dialect: llmproxy.DialectAnthropic,
		Body:    []byte(`{not json`),
	}, "sk-ant-secret")
	wantErrorType(t, err, llmproxy.ErrSchemaMismatch)
}

func TestTranslateRequestRejectsMissingRequiredFields(t *testing.T) {
	tr := &llmproxy.AnthropicDirectTranslator{DefaultAnthropicVersion: "2023-06-01"}
	for _, tc := range []struct {
		name string
		body string
	}{
		{"no model", `{"messages":[{"role":"user","content":"hi"}]}`},
		{"no messages", `{"model":"claude-3-5-sonnet"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tr.TranslateRequest(llmproxy.Request{
				Dialect: llmproxy.DialectAnthropic,
				Body:    []byte(tc.body),
			}, "sk-ant-secret")
			wantErrorType(t, err, llmproxy.ErrSchemaMismatch)
		})
	}
}

func TestTranslateRequestRejectsNonAnthropicDialect(t *testing.T) {
	tr := &llmproxy.AnthropicDirectTranslator{DefaultAnthropicVersion: "2023-06-01"}
	_, err := tr.TranslateRequest(llmproxy.Request{
		Dialect: llmproxy.DialectOpenAI,
		Body:    []byte(sampleRequest),
	}, "sk-ant-secret")
	wantErrorType(t, err, llmproxy.ErrUnsupportedField)
}

func TestTranslateRequestTrimsCustomBaseURL(t *testing.T) {
	tr := &llmproxy.AnthropicDirectTranslator{
		BaseURL:                 "https://anthropic.test/",
		DefaultAnthropicVersion: "2023-06-01",
	}
	up, err := tr.TranslateRequest(llmproxy.Request{
		Dialect: llmproxy.DialectAnthropic,
		Body:    []byte(sampleRequest),
	}, "sk-ant-secret")
	if err != nil {
		t.Fatalf("TranslateRequest: %v", err)
	}
	if up.URL != "https://anthropic.test/v1/messages" {
		t.Errorf("URL = %q, want the custom base joined without a double slash", up.URL)
	}
}

func TestTranslateResponseExtractsAuthoritativeUsage(t *testing.T) {
	tr := &llmproxy.AnthropicDirectTranslator{}
	body := []byte(`{"id":"msg_1","usage":{"input_tokens":12,"output_tokens":34}}`)
	resp, err := tr.TranslateResponse(llmproxy.DialectAnthropic, llmproxy.UpstreamResponse{
		StatusCode: 200,
		Body:       body,
	})
	if err != nil {
		t.Fatalf("TranslateResponse: %v", err)
	}
	if string(resp.Body) != string(body) {
		t.Errorf("body = %q, want the upstream response passed through", resp.Body)
	}
	if resp.Usage.InputTokens != 12 || resp.Usage.OutputTokens != 34 {
		t.Errorf("usage = %+v, want 12 in / 34 out", resp.Usage)
	}
}

func TestTranslateResponseMapsUpstreamStatuses(t *testing.T) {
	tr := &llmproxy.AnthropicDirectTranslator{}
	for _, tc := range []struct {
		status int
		want   llmproxy.ErrorType
	}{
		{401, llmproxy.ErrAuthFailed},
		{403, llmproxy.ErrAuthFailed},
		{429, llmproxy.ErrAuthFailed},
		{400, llmproxy.ErrUpstream4xx},
		{404, llmproxy.ErrUpstream4xx},
		{500, llmproxy.ErrUpstream5xx},
		{503, llmproxy.ErrUpstream5xx},
	} {
		t.Run("status_"+strconv.Itoa(tc.status), func(t *testing.T) {
			_, err := tr.TranslateResponse(llmproxy.DialectAnthropic, llmproxy.UpstreamResponse{
				StatusCode: tc.status,
				Body:       []byte(`{"error":"upstream"}`),
			})
			wantErrorType(t, err, tc.want)
		})
	}
}

func TestTranslateResponseRejectsMalformedSuccessBody(t *testing.T) {
	tr := &llmproxy.AnthropicDirectTranslator{}
	_, err := tr.TranslateResponse(llmproxy.DialectAnthropic, llmproxy.UpstreamResponse{
		StatusCode: 200,
		Body:       []byte(`{not json`),
	})
	wantErrorType(t, err, llmproxy.ErrSchemaMismatch)
}

func TestTranslateResponseRejectsNonAnthropicDialect(t *testing.T) {
	tr := &llmproxy.AnthropicDirectTranslator{}
	_, err := tr.TranslateResponse(llmproxy.DialectOpenAI, llmproxy.UpstreamResponse{
		StatusCode: 200,
		Body:       []byte(`{}`),
	})
	wantErrorType(t, err, llmproxy.ErrUnsupportedField)
}

func TestTranslationErrorMessageCarriesType(t *testing.T) {
	tr := &llmproxy.AnthropicDirectTranslator{DefaultAnthropicVersion: "2023-06-01"}
	_, err := tr.TranslateRequest(llmproxy.Request{Dialect: llmproxy.DialectAnthropic, Body: []byte(`{`)}, "k")
	if err == nil || !strings.Contains(err.Error(), string(llmproxy.ErrSchemaMismatch)) {
		t.Errorf("error %q does not name its error_type", err)
	}
}
