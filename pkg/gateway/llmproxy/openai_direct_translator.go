// SPDX-License-Identifier: MIT

package llmproxy

import (
	"encoding/json"
	"strings"
)

// ProviderOpenAIDirect is the §4.9 upstream provider identifier for
// the OpenAI first-party API (api.openai.com).
const ProviderOpenAIDirect = "openai_direct"

// defaultOpenAIDirectBaseURL is the OpenAI first-party API base used
// when OpenAIDirectTranslator.BaseURL is empty.
const defaultOpenAIDirectBaseURL = "https://api.openai.com"

// OpenAIDirectTranslator is the §4.9 translator for the openai_direct
// upstream provider. The OpenAI API serves the same OpenAI Chat
// Completions wire format the agent pod speaks, so a request in the
// openai dialect passes through with only the Authorization header
// injected.
type OpenAIDirectTranslator struct {
	// BaseURL overrides the upstream base. Empty selects
	// https://api.openai.com. Tests point this at a mock provider.
	BaseURL string
	// Organization is the optional OpenAI-Organization header that
	// scopes a request to a specific organization. Empty omits the
	// header.
	Organization string
}

var _ Translator = (*OpenAIDirectTranslator)(nil)

// Provider returns the openai_direct provider identifier.
func (t *OpenAIDirectTranslator) Provider() string { return ProviderOpenAIDirect }

// TranslateRequest validates the pod's OpenAI Chat Completions
// request and builds the upstream OpenAI request: the body passes
// through unchanged, and the injected Authorization: Bearer header
// carries the real upstream credential. A request in a dialect
// other than openai is rejected.
func (t *OpenAIDirectTranslator) TranslateRequest(req Request, apiKey string) (*UpstreamRequest, error) {
	if req.Dialect != DialectOpenAI {
		return nil, translationErrorf(ErrUnsupportedField,
			"openai_direct translator serves the openai dialect, not %q", req.Dialect)
	}
	if apiKey == "" {
		return nil, translationErrorf(ErrAuthFailed,
			"no upstream credential supplied for the openai_direct request")
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return nil, translationErrorf(ErrSchemaMismatch,
			"request body is not a JSON object: %v", err)
	}
	if _, ok := body["model"]; !ok {
		return nil, translationErrorf(ErrSchemaMismatch,
			"OpenAI Chat Completions request is missing the required model field")
	}
	if _, ok := body["messages"]; !ok {
		return nil, translationErrorf(ErrSchemaMismatch,
			"OpenAI Chat Completions request is missing the required messages field")
	}

	headers := map[string]string{
		"content-type":  "application/json",
		"authorization": "Bearer " + apiKey,
	}
	if t.Organization != "" {
		headers["openai-organization"] = t.Organization
	}

	upstreamURL := strings.TrimRight(t.baseURL(), "/") + "/v1/chat/completions"
	return &UpstreamRequest{
		URL:    upstreamURL,
		Body:   req.Body,
		Header: headers,
	}, nil
}

// TranslateResponse passes a successful upstream OpenAI response back
// to the pod and extracts the authoritative token usage. A non-2xx
// upstream status is mapped to the §4.9 error taxonomy.
func (t *OpenAIDirectTranslator) TranslateResponse(dialect Dialect, resp UpstreamResponse) (*Response, error) {
	if dialect != DialectOpenAI {
		return nil, translationErrorf(ErrUnsupportedField,
			"openai_direct translator serves the openai dialect, not %q", dialect)
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
func (t *OpenAIDirectTranslator) baseURL() string {
	if t.BaseURL != "" {
		return t.BaseURL
	}
	return defaultOpenAIDirectBaseURL
}
