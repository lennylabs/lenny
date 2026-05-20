// SPDX-License-Identifier: MIT

// Package llmproxy is the gateway's §4.9 LLM reverse proxy: the
// credential-injecting path that lets a proxy-mode agent pod reach an
// upstream LLM provider without ever holding the provider's API key.
//
// A translator converts the agent pod's proxy-dialect request into an
// upstream provider's native wire format, the proxy injects the real
// upstream credential read from the Token Service in-memory cache, and
// the translator converts the upstream response back into the pod's
// dialect. This file holds the translator contract and the
// anthropic_direct provider translation for the Anthropic Messages
// dialect.
package llmproxy

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Dialect is a §4.9 proxy dialect: the stable, provider-agnostic wire
// format an agent pod speaks to the proxy.
type Dialect string

const (
	// DialectAnthropic is the Anthropic Messages API dialect, served at
	// POST {proxyUrl}/v1/messages.
	DialectAnthropic Dialect = "anthropic"
	// DialectOpenAI is the OpenAI Chat Completions dialect, served at
	// POST {proxyUrl}/v1/chat/completions.
	DialectOpenAI Dialect = "openai"
	// DialectOpenAIResponses is the OpenAI Responses API dialect, served
	// at POST {proxyUrl}/v1/responses. Codex and other Responses-aware
	// runtimes speak this dialect when the pool's provider identity
	// advertises Responses support (§26).
	DialectOpenAIResponses Dialect = "openai_responses"
)

// ProviderAnthropicDirect is the §4.9 upstream provider identifier for
// the Anthropic first-party API.
const ProviderAnthropicDirect = "anthropic_direct"

// defaultAnthropicBaseURL is the Anthropic first-party API base used
// when AnthropicDirectTranslator.BaseURL is empty.
const defaultAnthropicBaseURL = "https://api.anthropic.com"

// ErrorType is the §4.9 translator error taxonomy. It labels every
// translator failure for the lenny_gateway_llm_translation_errors_total
// metric and determines the pod-facing error the proxy returns.
type ErrorType string

const (
	// ErrUnsupportedField reports that the pod's request carries a
	// dialect field the upstream provider does not accept.
	ErrUnsupportedField ErrorType = "unsupported_field"
	// ErrSchemaMismatch reports that the pod request or the upstream
	// response failed translator schema validation.
	ErrSchemaMismatch ErrorType = "schema_mismatch"
	// ErrAuthFailed reports that the upstream provider rejected the
	// injected credential as invalid, expired, or auth-layer
	// rate-limited.
	ErrAuthFailed ErrorType = "auth_failed"
	// ErrTimeout reports that the translator exceeded its per-request
	// deadline waiting for the upstream.
	ErrTimeout ErrorType = "timeout"
	// ErrStreamingInterrupted reports a mid-stream upstream failure
	// after the first byte reached the pod.
	ErrStreamingInterrupted ErrorType = "streaming_interrupted"
	// ErrUpstream4xx reports a non-auth upstream 4xx, surfaced to the
	// pod as PROVIDER_REQUEST_INVALID.
	ErrUpstream4xx ErrorType = "upstream_4xx"
	// ErrUpstream5xx reports an upstream 5xx or pre-first-byte
	// transport failure, surfaced to the pod as PROVIDER_UNAVAILABLE
	// and fed to the circuit breaker.
	ErrUpstream5xx ErrorType = "upstream_5xx"
)

// TranslationError is a translator failure tagged with its §4.9
// error_type so the proxy can pick the pod-facing error code and the
// lenny_gateway_llm_translation_errors_total metric label.
type TranslationError struct {
	Type    ErrorType
	Message string
}

func (e *TranslationError) Error() string {
	return fmt.Sprintf("llmproxy: %s: %s", e.Type, e.Message)
}

// translationErrorf builds a TranslationError with a formatted message.
func translationErrorf(t ErrorType, format string, args ...any) *TranslationError {
	return &TranslationError{Type: t, Message: fmt.Sprintf(format, args...)}
}

// Request is a proxy-dialect LLM request received from an agent pod.
type Request struct {
	// Dialect is the proxy dialect the body is shaped in.
	Dialect Dialect
	// Body is the raw request body.
	Body []byte
	// AnthropicVersion is the value of the pod's anthropic-version
	// header. Empty when the pod omitted it; the translator then
	// injects the configured default.
	AnthropicVersion string
}

// UpstreamRequest is the translated request the proxy forwards to the
// upstream provider, including the injected credential header.
type UpstreamRequest struct {
	// URL is the upstream provider endpoint.
	URL string
	// Body is the request body in the upstream provider's wire format.
	Body []byte
	// Header carries the provider headers the proxy sets, including the
	// injected credential.
	Header map[string]string
}

// UpstreamResponse is a non-streaming response received from the
// upstream provider.
type UpstreamResponse struct {
	// StatusCode is the upstream HTTP status.
	StatusCode int
	// Body is the raw upstream response body.
	Body []byte
}

// Response is the translated response the proxy returns to the agent
// pod, with the authoritative token usage extracted from the upstream.
type Response struct {
	// Body is the response body in the pod's proxy dialect.
	Body []byte
	// Usage is the authoritative token count parsed from the upstream
	// response. Pod-reported counts are not accepted in proxy mode.
	Usage Usage
}

// Usage is the §4.9 normalized authoritative token count.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Translator converts proxy-dialect LLM requests and responses to and
// from one upstream provider's native wire format (§4.9).
type Translator interface {
	// Provider is the §4.9 upstream provider identifier this
	// translator serves.
	Provider() string
	// TranslateRequest converts a proxy-dialect request into the
	// upstream request. apiKey is the real upstream credential the
	// proxy reads from the Token Service cache for this call.
	TranslateRequest(req Request, apiKey string) (*UpstreamRequest, error)
	// TranslateResponse converts a non-streaming upstream response back
	// into the pod's dialect and extracts the authoritative usage.
	TranslateResponse(dialect Dialect, resp UpstreamResponse) (*Response, error)
}

// AnthropicDirectTranslator is the §4.9 translator for the
// anthropic_direct upstream provider. The Anthropic Messages dialect
// and the anthropic_direct provider share the Anthropic Messages wire
// format, so translation is a passthrough plus credential injection,
// anthropic-version header handling, and authoritative usage
// extraction. The openai dialect against this provider is served by a
// separate cross-dialect translator.
type AnthropicDirectTranslator struct {
	// BaseURL is the upstream Anthropic API base. Defaults to
	// https://api.anthropic.com when empty.
	BaseURL string
	// DefaultAnthropicVersion is the anthropic-version value injected
	// when the pod's request omits the header, sourced from the
	// llmProxy.anthropicVersion Helm value.
	DefaultAnthropicVersion string
}

var _ Translator = (*AnthropicDirectTranslator)(nil)

// Provider returns the anthropic_direct provider identifier.
func (t *AnthropicDirectTranslator) Provider() string { return ProviderAnthropicDirect }

// TranslateRequest validates the pod's Anthropic Messages request and
// builds the upstream request: the body passes through unchanged, the
// injected x-api-key carries the real upstream credential, and the
// anthropic-version header is taken from the pod request or the
// configured default. A request in a dialect other than anthropic is
// rejected — the openai dialect is handled by a separate translator.
func (t *AnthropicDirectTranslator) TranslateRequest(req Request, apiKey string) (*UpstreamRequest, error) {
	if req.Dialect != DialectAnthropic {
		return nil, translationErrorf(ErrUnsupportedField,
			"anthropic_direct translator serves the anthropic dialect, not %q", req.Dialect)
	}
	if apiKey == "" {
		return nil, translationErrorf(ErrAuthFailed,
			"no upstream credential supplied for the anthropic_direct request")
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return nil, translationErrorf(ErrSchemaMismatch,
			"request body is not a JSON object: %v", err)
	}
	if _, ok := body["model"]; !ok {
		return nil, translationErrorf(ErrSchemaMismatch,
			"Anthropic Messages request is missing the required model field")
	}
	if _, ok := body["messages"]; !ok {
		return nil, translationErrorf(ErrSchemaMismatch,
			"Anthropic Messages request is missing the required messages field")
	}

	version := req.AnthropicVersion
	if version == "" {
		version = t.DefaultAnthropicVersion
	}
	if version == "" {
		return nil, translationErrorf(ErrSchemaMismatch,
			"request omits anthropic-version and the proxy has no default configured")
	}

	return &UpstreamRequest{
		URL:  t.baseURL() + "/v1/messages",
		Body: req.Body,
		Header: map[string]string{
			"content-type":      "application/json",
			"x-api-key":         apiKey,
			"anthropic-version": version,
		},
	}, nil
}

// TranslateResponse passes a successful upstream Anthropic Messages
// response back to the pod and extracts the authoritative token usage.
// A non-2xx upstream status is mapped to the §4.9 error taxonomy: 401,
// 403, and 429 are auth-layer failures, other 4xx are upstream_4xx, and
// 5xx are upstream_5xx.
func (t *AnthropicDirectTranslator) TranslateResponse(dialect Dialect, resp UpstreamResponse) (*Response, error) {
	if dialect != DialectAnthropic {
		return nil, translationErrorf(ErrUnsupportedField,
			"anthropic_direct translator serves the anthropic dialect, not %q", dialect)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, upstreamStatusError(resp.StatusCode, resp.Body)
	}

	usage, err := extractAnthropicUsage(resp.Body)
	if err != nil {
		return nil, err
	}
	return &Response{Body: resp.Body, Usage: usage}, nil
}

// baseURL returns the configured Anthropic base URL or the default,
// with any trailing slash trimmed so path joins do not double up.
func (t *AnthropicDirectTranslator) baseURL() string {
	if t.BaseURL == "" {
		return defaultAnthropicBaseURL
	}
	return strings.TrimRight(t.BaseURL, "/")
}

// upstreamStatusError maps a non-2xx upstream status to a
// TranslationError tagged with the §4.9 error_type.
func upstreamStatusError(status int, body []byte) *TranslationError {
	switch {
	case status == 401 || status == 403 || status == 429:
		return translationErrorf(ErrAuthFailed,
			"upstream rejected the injected credential with status %d: %s", status, string(body))
	case status >= 400 && status < 500:
		return translationErrorf(ErrUpstream4xx,
			"upstream returned status %d: %s", status, string(body))
	default:
		return translationErrorf(ErrUpstream5xx,
			"upstream returned status %d: %s", status, string(body))
	}
}

// anthropicUsageEnvelope is the subset of the Anthropic Messages
// response carrying the authoritative token counts.
type anthropicUsageEnvelope struct {
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// rehostAnthropicBody validates an Anthropic Messages request body and
// rewrites it for a provider that names the model in the URL path: it
// returns the model and a body with the `model` field removed and
// `anthropic_version` set to the provider's required value. Vertex AI
// and AWS Bedrock both serve Claude in this form, differing only in
// the anthropic_version string and the URL.
func rehostAnthropicBody(reqBody []byte, anthropicVersion string) (model string, body []byte, err error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(reqBody, &fields); err != nil {
		return "", nil, translationErrorf(ErrSchemaMismatch,
			"request body is not a JSON object: %v", err)
	}
	if raw, ok := fields["model"]; ok {
		_ = json.Unmarshal(raw, &model)
	}
	if model == "" {
		return "", nil, translationErrorf(ErrSchemaMismatch,
			"Anthropic Messages request is missing the required model field")
	}
	if _, ok := fields["messages"]; !ok {
		return "", nil, translationErrorf(ErrSchemaMismatch,
			"Anthropic Messages request is missing the required messages field")
	}
	delete(fields, "model")
	fields["anthropic_version"], _ = json.Marshal(anthropicVersion)
	body, err = json.Marshal(fields)
	if err != nil {
		return "", nil, translationErrorf(ErrSchemaMismatch,
			"could not re-encode the request body: %v", err)
	}
	return model, body, nil
}

// extractAnthropicUsage parses the authoritative token usage from a
// non-streaming Anthropic Messages response body.
func extractAnthropicUsage(body []byte) (Usage, error) {
	var env anthropicUsageEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return Usage{}, translationErrorf(ErrSchemaMismatch,
			"upstream response is not valid JSON: %v", err)
	}
	return Usage{
		InputTokens:  env.Usage.InputTokens,
		OutputTokens: env.Usage.OutputTokens,
	}, nil
}
