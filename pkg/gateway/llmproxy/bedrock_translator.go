// SPDX-License-Identifier: MIT

package llmproxy

import (
	"fmt"
	"net/url"
)

// ProviderAWSBedrock is the §4.9 upstream provider identifier for
// Anthropic Claude models served on AWS Bedrock.
const ProviderAWSBedrock = "aws_bedrock"

// bedrockAnthropicVersion is the anthropic_version value AWS Bedrock
// requires in the request body for Claude models.
const bedrockAnthropicVersion = "bedrock-2023-05-31"

// AWSBedrockTranslator is the §4.9 translator for the aws_bedrock
// upstream provider. Bedrock serves Anthropic Claude models in the
// Anthropic Messages wire format: the model is named in the URL path,
// and the body carries anthropic_version in place of model. The
// injected credential is a Bedrock API key presented as a bearer
// token — the single-credential model the Translator contract
// supplies, in contrast to AWS SigV4's access-key pair.
type AWSBedrockTranslator struct {
	// Region is the AWS region, for example us-east-1. Required.
	Region string
}

var _ Translator = (*AWSBedrockTranslator)(nil)

// Provider returns the aws_bedrock provider identifier.
func (t *AWSBedrockTranslator) Provider() string { return ProviderAWSBedrock }

// TranslateRequest validates the pod's Anthropic Messages request and
// builds the upstream Bedrock request: the model field is moved out of
// the body and into the invoke URL, the body gains the Bedrock
// anthropic_version, and the injected Authorization header carries the
// Bedrock API key. A request in a dialect other than anthropic is
// rejected.
func (t *AWSBedrockTranslator) TranslateRequest(req Request, apiKey string) (*UpstreamRequest, error) {
	if req.Dialect != DialectAnthropic {
		return nil, translationErrorf(ErrUnsupportedField,
			"aws_bedrock translator serves the anthropic dialect, not %q", req.Dialect)
	}
	if apiKey == "" {
		return nil, translationErrorf(ErrAuthFailed,
			"no upstream credential supplied for the aws_bedrock request")
	}
	if t.Region == "" {
		return nil, translationErrorf(ErrSchemaMismatch,
			"aws_bedrock translator is not configured with a region")
	}

	model, upstreamBody, err := rehostAnthropicBody(req.Body, bedrockAnthropicVersion)
	if err != nil {
		return nil, err
	}

	// PathEscape the model id so a crafted value cannot break out of
	// the path segment and redirect the upstream call.
	upstreamURL := fmt.Sprintf(
		"https://bedrock-runtime.%s.amazonaws.com/model/%s/invoke",
		t.Region, url.PathEscape(model))
	return &UpstreamRequest{
		URL:  upstreamURL,
		Body: upstreamBody,
		Header: map[string]string{
			"content-type":  "application/json",
			"authorization": "Bearer " + apiKey,
		},
	}, nil
}

// TranslateResponse passes a successful upstream Bedrock response back
// to the pod and extracts the authoritative token usage. Bedrock
// serves the Anthropic Messages response format, so usage extraction
// is shared with anthropic_direct. A non-2xx upstream status is mapped
// to the §4.9 error taxonomy.
func (t *AWSBedrockTranslator) TranslateResponse(dialect Dialect, resp UpstreamResponse) (*Response, error) {
	if dialect != DialectAnthropic {
		return nil, translationErrorf(ErrUnsupportedField,
			"aws_bedrock translator serves the anthropic dialect, not %q", dialect)
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
