// SPDX-License-Identifier: MIT

package llmproxy

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// ProviderAzureOpenAI is the §4.9 upstream provider identifier for
// Azure OpenAI Service.
const ProviderAzureOpenAI = "azure_openai"

// AzureOpenAITranslator is the §4.9 translator for the azure_openai
// upstream provider. Azure OpenAI Service serves the OpenAI Chat
// Completions wire format, so a request in the openai dialect passes
// through unchanged; the translator builds the Azure deployment URL
// and injects the api-key header.
type AzureOpenAITranslator struct {
	// Endpoint is the Azure OpenAI resource base URL, for example
	// https://my-resource.openai.azure.com. Required.
	Endpoint string
	// APIVersion is the Azure OpenAI `api-version` query parameter,
	// for example 2024-10-21. Required.
	APIVersion string
}

var _ Translator = (*AzureOpenAITranslator)(nil)

// Provider returns the azure_openai provider identifier.
func (t *AzureOpenAITranslator) Provider() string { return ProviderAzureOpenAI }

// TranslateRequest validates the pod's OpenAI Chat Completions request
// and builds the upstream Azure request: Azure routes to a model by
// deployment name in the URL path, so the request's `model` field
// names the deployment, the body passes through unchanged, and the
// injected api-key header carries the real upstream credential. A
// request in a dialect other than openai is rejected.
func (t *AzureOpenAITranslator) TranslateRequest(req Request, apiKey string) (*UpstreamRequest, error) {
	if req.Dialect != DialectOpenAI {
		return nil, translationErrorf(ErrUnsupportedField,
			"azure_openai translator serves the openai dialect, not %q", req.Dialect)
	}
	if apiKey == "" {
		return nil, translationErrorf(ErrAuthFailed,
			"no upstream credential supplied for the azure_openai request")
	}
	if t.Endpoint == "" || t.APIVersion == "" {
		return nil, translationErrorf(ErrSchemaMismatch,
			"azure_openai translator is not configured with an endpoint and api-version")
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return nil, translationErrorf(ErrSchemaMismatch,
			"request body is not a JSON object: %v", err)
	}
	var model string
	if raw, ok := body["model"]; ok {
		_ = json.Unmarshal(raw, &model)
	}
	if model == "" {
		return nil, translationErrorf(ErrSchemaMismatch,
			"OpenAI Chat Completions request is missing the required model field")
	}
	if _, ok := body["messages"]; !ok {
		return nil, translationErrorf(ErrSchemaMismatch,
			"OpenAI Chat Completions request is missing the required messages field")
	}

	// PathEscape the deployment name so a crafted model value cannot
	// break out of the path segment and redirect the upstream call.
	upstreamURL := fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=%s",
		strings.TrimRight(t.Endpoint, "/"), url.PathEscape(model), url.QueryEscape(t.APIVersion))
	return &UpstreamRequest{
		URL:  upstreamURL,
		Body: req.Body,
		Header: map[string]string{
			"content-type": "application/json",
			"api-key":      apiKey,
		},
	}, nil
}

// TranslateResponse passes a successful upstream Azure OpenAI response
// back to the pod and extracts the authoritative token usage. A non-2xx
// upstream status is mapped to the §4.9 error taxonomy.
func (t *AzureOpenAITranslator) TranslateResponse(dialect Dialect, resp UpstreamResponse) (*Response, error) {
	if dialect != DialectOpenAI {
		return nil, translationErrorf(ErrUnsupportedField,
			"azure_openai translator serves the openai dialect, not %q", dialect)
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

// openAIUsageEnvelope is the subset of the OpenAI Chat Completions
// response carrying the authoritative token counts.
type openAIUsageEnvelope struct {
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// extractOpenAIUsage parses the authoritative token usage from a
// non-streaming OpenAI Chat Completions response body.
func extractOpenAIUsage(body []byte) (Usage, error) {
	var env openAIUsageEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return Usage{}, translationErrorf(ErrSchemaMismatch,
			"upstream response is not valid JSON: %v", err)
	}
	return Usage{
		InputTokens:  env.Usage.PromptTokens,
		OutputTokens: env.Usage.CompletionTokens,
	}, nil
}
