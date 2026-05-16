// SPDX-License-Identifier: MIT

package llmproxy

import (
	"fmt"
	"net/url"
)

// ProviderVertexAI is the §4.9 upstream provider identifier for
// Anthropic Claude models served on Google Vertex AI.
const ProviderVertexAI = "vertex_ai"

// vertexAnthropicVersion is the anthropic_version value Vertex AI
// requires in the request body for Claude models.
const vertexAnthropicVersion = "vertex-2023-10-16"

// VertexAITranslator is the §4.9 translator for the vertex_ai upstream
// provider. Vertex AI serves Anthropic Claude models in the Anthropic
// Messages wire format with two differences from the first-party API:
// the model is named in the URL path rather than the body, and the
// body carries anthropic_version in place of model. The translator
// moves the model into the URL and injects anthropic_version.
type VertexAITranslator struct {
	// Region is the Vertex AI region, for example us-east5. Required.
	Region string
	// Project is the GCP project id. Required.
	Project string
}

var _ Translator = (*VertexAITranslator)(nil)

// Provider returns the vertex_ai provider identifier.
func (t *VertexAITranslator) Provider() string { return ProviderVertexAI }

// TranslateRequest validates the pod's Anthropic Messages request and
// builds the upstream Vertex AI request: the model field is moved out
// of the body and into the URL path, the body gains the Vertex
// anthropic_version, and the injected Authorization header carries the
// GCP access token. A request in a dialect other than anthropic is
// rejected.
func (t *VertexAITranslator) TranslateRequest(req Request, apiKey string) (*UpstreamRequest, error) {
	if req.Dialect != DialectAnthropic {
		return nil, translationErrorf(ErrUnsupportedField,
			"vertex_ai translator serves the anthropic dialect, not %q", req.Dialect)
	}
	if apiKey == "" {
		return nil, translationErrorf(ErrAuthFailed,
			"no upstream credential supplied for the vertex_ai request")
	}
	if t.Region == "" || t.Project == "" {
		return nil, translationErrorf(ErrSchemaMismatch,
			"vertex_ai translator is not configured with a region and project")
	}

	// Vertex names the model in the URL; the body carries
	// anthropic_version in place of the model field.
	model, upstreamBody, err := rehostAnthropicBody(req.Body, vertexAnthropicVersion)
	if err != nil {
		return nil, err
	}

	// PathEscape the model so a crafted value cannot break out of the
	// path segment and redirect the upstream call.
	upstreamURL := fmt.Sprintf(
		"https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/anthropic/models/%s:rawPredict",
		t.Region, t.Project, t.Region, url.PathEscape(model))
	return &UpstreamRequest{
		URL:  upstreamURL,
		Body: upstreamBody,
		Header: map[string]string{
			"content-type":  "application/json",
			"authorization": "Bearer " + apiKey,
		},
	}, nil
}

// TranslateResponse passes a successful upstream Vertex AI response
// back to the pod and extracts the authoritative token usage. Vertex
// serves the Anthropic Messages response format, so the usage
// extraction is shared with anthropic_direct. A non-2xx upstream
// status is mapped to the §4.9 error taxonomy.
func (t *VertexAITranslator) TranslateResponse(dialect Dialect, resp UpstreamResponse) (*Response, error) {
	if dialect != DialectAnthropic {
		return nil, translationErrorf(ErrUnsupportedField,
			"vertex_ai translator serves the anthropic dialect, not %q", dialect)
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
