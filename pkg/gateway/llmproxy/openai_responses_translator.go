// SPDX-License-Identifier: MIT

package llmproxy

import (
	"encoding/json"
	"strings"
)

// OpenAIResponsesTranslator is the §4.9 translator for the OpenAI
// Responses API dialect (POST /v1/responses on api.openai.com). The
// Responses API is a superset of Chat Completions: a request in the
// openai_responses dialect carries `model` and `input` rather than
// `model` and `messages`. The translator validates the dialect and
// the required schema fields, then injects Authorization: Bearer and
// routes the upstream call to /v1/responses on the configured base.
type OpenAIResponsesTranslator struct {
	// BaseURL overrides the upstream base. Empty selects
	// https://api.openai.com. Tests point this at a mock provider.
	BaseURL string
	// Organization is the optional OpenAI-Organization header that
	// scopes a request to a specific organization. Empty omits the
	// header.
	Organization string
}

var _ Translator = (*OpenAIResponsesTranslator)(nil)

// Provider returns the openai_direct provider identifier. The
// openai_direct provider serves both the Chat Completions dialect
// (via OpenAIDirectTranslator) and the Responses dialect (via this
// translator); the dispatcher selects by (provider, dialect).
func (t *OpenAIResponsesTranslator) Provider() string { return ProviderOpenAIDirect }

// TranslateRequest validates the pod's OpenAI Responses request and
// builds the upstream OpenAI request: the body passes through
// unchanged, the Authorization header is injected, and the URL
// points at /v1/responses. A request in a dialect other than
// openai_responses is rejected.
func (t *OpenAIResponsesTranslator) TranslateRequest(req Request, apiKey string) (*UpstreamRequest, error) {
	if req.Dialect != DialectOpenAIResponses {
		return nil, translationErrorf(ErrUnsupportedField,
			"openai_responses translator serves the openai_responses dialect, not %q", req.Dialect)
	}
	if apiKey == "" {
		return nil, translationErrorf(ErrAuthFailed,
			"no upstream credential supplied for the openai_responses request")
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return nil, translationErrorf(ErrSchemaMismatch,
			"request body is not a JSON object: %v", err)
	}
	if _, ok := body["model"]; !ok {
		return nil, translationErrorf(ErrSchemaMismatch,
			"OpenAI Responses request is missing the required model field")
	}
	// §15.4 — the Responses API requires the `input` field. Chat
	// Completions uses `messages`; rejecting `messages` here catches
	// the common mistake of pointing the wrong dialect at this
	// endpoint.
	if _, ok := body["input"]; !ok {
		return nil, translationErrorf(ErrSchemaMismatch,
			"OpenAI Responses request is missing the required input field")
	}

	headers := map[string]string{
		"content-type":  "application/json",
		"authorization": "Bearer " + apiKey,
	}
	if t.Organization != "" {
		headers["openai-organization"] = t.Organization
	}

	upstreamURL := strings.TrimRight(t.baseURL(), "/") + "/v1/responses"
	return &UpstreamRequest{
		URL:    upstreamURL,
		Body:   req.Body,
		Header: headers,
	}, nil
}

// TranslateResponse passes a successful upstream Responses API
// response back to the pod and extracts the authoritative token
// usage. The Responses envelope mirrors Chat Completions for the
// usage block (prompt_tokens, completion_tokens), so the existing
// extractor handles it without dialect-specific logic. A non-2xx
// upstream status is mapped through the §4.9 error taxonomy.
func (t *OpenAIResponsesTranslator) TranslateResponse(dialect Dialect, resp UpstreamResponse) (*Response, error) {
	if dialect != DialectOpenAIResponses {
		return nil, translationErrorf(ErrUnsupportedField,
			"openai_responses translator serves the openai_responses dialect, not %q", dialect)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, upstreamStatusError(resp.StatusCode, resp.Body)
	}
	usage, err := extractOpenAIUsage(resp.Body)
	if err != nil {
		return nil, err
	}
	return &Response{Body: resp.Body, Usage: usage}, nil
}

// baseURL returns the configured upstream base or the documented
// default when BaseURL is empty.
func (t *OpenAIResponsesTranslator) baseURL() string {
	if t.BaseURL != "" {
		return t.BaseURL
	}
	return defaultOpenAIDirectBaseURL
}
