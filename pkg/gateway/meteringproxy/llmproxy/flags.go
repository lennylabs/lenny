// SPDX-License-Identifier: MIT

package llmproxy

import (
	"flag"
	"net/http"
	"os"
	"time"

	"github.com/lennylabs/lenny/pkg/common/envdefault"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credfallback"
)

// Flags holds the flags only the LLM proxy reads: its listen address, the
// per-provider translator configuration, and the credential fallback
// budget. Each default comes from the environment variable its help text
// names.
type Flags struct {
	Addr                    *string
	AnthropicVersion        *string
	OpenAIBaseURL           *string
	OpenAIOrganization      *string
	BedrockRegion           *string
	VertexRegion            *string
	VertexProject           *string
	AzureEndpoint           *string
	AzureAPIVersion         *string
	FallbackMaxRotations    *int
	FallbackCooldownSeconds *int
}

// RegisterFlags registers the LLM proxy's flags on fs and returns their
// values, which fs fills when it parses.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	f.Addr = fs.String("llm-proxy-addr", os.Getenv("LENNY_LLM_PROXY_ADDR"),
		"§4.9 LLM reverse-proxy listen address (host:port, e.g. :8443). When set, the gateway serves the proxy for proxy-mode agent pods on this address. Empty disables the LLM proxy listener.")
	f.FallbackMaxRotations = fs.Int("credential-fallback-max-rotations", envdefault.Int("LENNY_CREDENTIAL_FALLBACK_MAX_ROTATIONS", 3),
		"§4.9 credentialPolicy fallback.maxRotationsPerSession: the rotation budget shared across all providers in a session before the fallback chain is exhausted and the session terminates with CREDENTIAL_FALLBACK_EXHAUSTED. The spec default is 3; operator-tunable. Override via LENNY_CREDENTIAL_FALLBACK_MAX_ROTATIONS.")
	f.FallbackCooldownSeconds = fs.Int("credential-fallback-cooldown-seconds", envdefault.Int("LENNY_CREDENTIAL_FALLBACK_COOLDOWN_SECONDS", 60),
		"§4.9 credentialPolicy fallback.cooldownOnRateLimit: seconds a faulted credential pool is held on cooldown before the fallback chain selects it again. The spec default is 60s; operator-tunable. Override via LENNY_CREDENTIAL_FALLBACK_COOLDOWN_SECONDS.")
	f.AnthropicVersion = fs.String("anthropic-version", os.Getenv("LENNY_ANTHROPIC_VERSION"),
		"default anthropic-version header the §4.9 LLM proxy injects when a request omits it. Empty rejects a request that omits the header.")
	f.OpenAIBaseURL = fs.String("llm-openai-base-url", os.Getenv("LENNY_LLM_OPENAI_BASE_URL"),
		"§4.9 openai_direct upstream base URL the LLM proxy targets. Empty selects https://api.openai.com.")
	f.OpenAIOrganization = fs.String("llm-openai-organization", os.Getenv("LENNY_LLM_OPENAI_ORGANIZATION"),
		"§4.9 optional OpenAI-Organization header the LLM proxy adds to openai_direct requests.")
	f.BedrockRegion = fs.String("llm-bedrock-region", os.Getenv("LENNY_LLM_BEDROCK_REGION"),
		"§4.9 AWS region for the aws_bedrock translator (e.g. us-east-1). Empty leaves aws_bedrock out of the proxy translator registry.")
	f.VertexRegion = fs.String("llm-vertex-region", os.Getenv("LENNY_LLM_VERTEX_REGION"),
		"§4.9 Vertex AI region for the vertex_ai translator (e.g. us-east5). Required with --llm-vertex-project to register vertex_ai.")
	f.VertexProject = fs.String("llm-vertex-project", os.Getenv("LENNY_LLM_VERTEX_PROJECT"),
		"§4.9 GCP project id for the vertex_ai translator. Required with --llm-vertex-region to register vertex_ai.")
	f.AzureEndpoint = fs.String("llm-azure-endpoint", os.Getenv("LENNY_LLM_AZURE_ENDPOINT"),
		"§4.9 Azure OpenAI resource base URL for the azure_openai translator. Required with --llm-azure-api-version to register azure_openai.")
	f.AzureAPIVersion = fs.String("llm-azure-api-version", os.Getenv("LENNY_LLM_AZURE_API_VERSION"),
		"§4.9 Azure OpenAI api-version query value. Required with --llm-azure-endpoint to register azure_openai.")
	return f
}

// Translators returns the provider translator registry the flags configure.
// anthropic_direct and openai_direct carry safe global defaults and are
// always registered. aws_bedrock, vertex_ai, and azure_openai need
// per-deployment region, project, or endpoint configuration, so each
// registers only when its flags are set; a lease for an unconfigured
// provider is rejected with UPSTREAM_PROVIDER_UNSUPPORTED.
// spec: §4.9.
func (f *Flags) Translators() TranslatorRegistry {
	translators := []Translator{
		&AnthropicDirectTranslator{DefaultAnthropicVersion: *f.AnthropicVersion},
		&OpenAIDirectTranslator{BaseURL: *f.OpenAIBaseURL, Organization: *f.OpenAIOrganization},
	}
	if *f.BedrockRegion != "" {
		translators = append(translators, &AWSBedrockTranslator{Region: *f.BedrockRegion})
	}
	if *f.VertexRegion != "" && *f.VertexProject != "" {
		translators = append(translators, &VertexAITranslator{Region: *f.VertexRegion, Project: *f.VertexProject})
	}
	if *f.AzureEndpoint != "" && *f.AzureAPIVersion != "" {
		translators = append(translators, &AzureOpenAITranslator{Endpoint: *f.AzureEndpoint, APIVersion: *f.AzureAPIVersion})
	}
	return NewTranslatorRegistry(translators...)
}

// FallbackController returns the credential fallback controller with the
// rotation budget and the cooldown the flags set.
func (f *Flags) FallbackController() *credfallback.Controller {
	return credfallback.NewController(*f.FallbackMaxRotations,
		time.Duration(*f.FallbackCooldownSeconds)*time.Second)
}

// NewServer returns the LLM proxy's HTTP server on addr, with h mounted on
// the proxy's route, or nil when addr is empty, which disables the proxy
// listener. The proxy serves on a listener of its own rather than through
// the API front.
func NewServer(addr string, h http.Handler) *http.Server {
	if addr == "" {
		return nil
	}
	mux := http.NewServeMux()
	mux.Handle("POST /llm-proxy/v1/messages", h)
	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
}
