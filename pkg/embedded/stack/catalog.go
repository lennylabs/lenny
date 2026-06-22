// SPDX-License-Identifier: MIT

package stack

import "strings"

// The §26.2 shared coding-agent blocks and the §26.1 catalog fields the
// Embedded Mode bootstrap seeds so `lenny up` reaches §26 parity with a
// Helm install. The JSON tags mirror the gateway admin RuntimePayload
// (pkg/gateway/admin.RuntimePayload) so POST /v1/admin/bootstrap applies
// the same Runtime record the chart's reference-runtimes.yaml renders.
// The blocks are declared locally so the stack package does not depend on
// the gateway's admin package. F-26.2.3 / F-26.1.3.
type runtimeCapabilities struct {
	Interaction string         `json:"interaction,omitempty"`
	Injection   *injectionCaps `json:"injection,omitempty"`
}

type injectionCaps struct {
	Supported bool     `json:"supported,omitempty"`
	Modes     []string `json:"modes,omitempty"`
}

type credentialCapabilities struct {
	HotRotation  bool     `json:"hotRotation,omitempty"`
	ProxyDialect []string `json:"proxyDialect,omitempty"`
}

type runtimeLimits struct {
	MaxSessionAgeSeconds       int   `json:"maxSessionAgeSeconds,omitempty"`
	MaxUploadSizeBytes         int64 `json:"maxUploadSizeBytes,omitempty"`
	MaxRequestInputWaitSeconds int   `json:"maxRequestInputWaitSeconds,omitempty"`
}

type setupCommandPolicy struct {
	Mode        string   `json:"mode,omitempty"`
	Shell       bool     `json:"shell,omitempty"`
	Allowlist   []string `json:"allowlist,omitempty"`
	MaxCommands int      `json:"maxCommands,omitempty"`
}

type setupPolicy struct {
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
	OnTimeout      string `json:"onTimeout,omitempty"`
}

type defaultPoolConfig struct {
	WarmCount     int    `json:"warmCount,omitempty"`
	ResourceClass string `json:"resourceClass,omitempty"`
	EgressProfile string `json:"egressProfile,omitempty"`
}

// ReferenceRuntime is one §26 reference-runtime catalog entry as the
// Embedded Mode bootstrap registers it.
type ReferenceRuntime struct {
	// Name is the runtime identifier.
	Name string
	// Image is the canonical OCI image, digest-pinned. §17.4 pulls it
	// lazily on the first session start for the runtime.
	Image string
	// IntegrationLevel is the §15.4.3 level the runtime declares.
	IntegrationLevel string
	// Description is the catalog one-line summary.
	Description string
	// Labels are the §5.1 line 51 required runtime labels — the primary
	// mechanism for environment runtimeSelector/connectorSelector matching
	// (§10.6). Every reference runtime carries the chart's reference-runtime
	// marker plus the §26.3 maintainer/upstream labels.
	Labels map[string]string

	// The §26.1 / §26.2 declarations the registered Runtime record
	// carries. Coding-agent entries populate all of them; chat carries
	// the smaller-resource posture (§26.1 line 22); the framework
	// runtimes carry the basic fields only. nil blocks are omitted from
	// the bootstrap seed.
	AllowedResourceClasses []string
	SupportedProviders     []string
	Capabilities           *runtimeCapabilities
	CredentialCapabilities *credentialCapabilities
	Limits                 *runtimeLimits
	SetupCommandPolicy     *setupCommandPolicy
	SetupPolicy            *setupPolicy
	DefaultPoolConfig      *defaultPoolConfig
}

// placeholderDigest is the digest suffix used on each §26 reference
// image. §5.1 and §13.1 require runtime image references to be
// digest-pinned; the gateway's admin handler rejects a tag-only
// reference. The §26 reference-runtime images are published by their
// own first-party repositories' CI, so the canonical digest is not
// known at lenny build time. This placeholder satisfies the
// digest-pinning syntax so the runtimes register as platform-global
// records. §17.4 pulls a runtime image lazily on its first session
// start; pulling a placeholder-pinned image fails until the operator
// re-registers the runtime with the published digest, or imports a
// local image with `lenny image import`.
const placeholderDigest = "@sha256:0000000000000000000000000000000000000000000000000000000000000000"

// codingAgentShared returns a fresh copy of the §26.2 shared
// coding-agent blocks (capabilities, limits, setupCommandPolicy,
// setupPolicy, defaultPoolConfig, allowedResourceClasses). The four
// coding-agent runtimes differ only in image, supportedProviders, and
// credentialCapabilities.proxyDialect, so each entry composes these
// shared defaults with its per-runtime overrides. Fresh pointers are
// returned per call so callers never alias the shared block.
//
// The values mirror the chart's referenceRuntimes.catalog entries in
// charts/lenny/values.yaml verbatim.
//
// spec: §26.2 lines 38-92 — the shared coding-agent isolation, limits,
// setupCommandPolicy, setupPolicy, and defaultPoolConfig.
func codingAgentShared() ReferenceRuntime {
	return ReferenceRuntime{
		IntegrationLevel:       "full",
		AllowedResourceClasses: []string{"small", "medium", "large"},
		Capabilities: &runtimeCapabilities{
			Interaction: "multi_turn",
			Injection:   &injectionCaps{Supported: true, Modes: []string{"immediate", "queued"}},
		},
		Limits: &runtimeLimits{
			MaxSessionAgeSeconds:       14400,
			MaxUploadSizeBytes:         524288000,
			MaxRequestInputWaitSeconds: 1800,
		},
		SetupCommandPolicy: &setupCommandPolicy{
			Mode:        "allowlist",
			Shell:       false,
			Allowlist:   []string{"npm", "pnpm", "yarn", "pip", "poetry", "go", "cargo", "make", "mvn", "gradle", "apt-get", "chmod", "mkdir", "cp", "mv", "ln"},
			MaxCommands: 20,
		},
		SetupPolicy:       &setupPolicy{TimeoutSeconds: 600, OnTimeout: "fail"},
		DefaultPoolConfig: &defaultPoolConfig{WarmCount: 2, ResourceClass: "medium", EgressProfile: "restricted"},
	}
}

// codingAgent composes the §26.2 shared block with a coding-agent's
// per-runtime name, image, providers, proxy dialect, and §26.3
// maintainer/upstream labels.
func codingAgent(name string, providers, proxyDialect []string, description, upstream string) ReferenceRuntime {
	rt := codingAgentShared()
	rt.Name = name
	rt.Image = "ghcr.io/lennylabs/runtime-" + name + ":1.0.0" + placeholderDigest
	rt.Description = description
	rt.SupportedProviders = providers
	rt.CredentialCapabilities = &credentialCapabilities{HotRotation: true, ProxyDialect: proxyDialect}
	rt.Labels = referenceLabels(upstream)
	return rt
}

// referenceLabels builds the §5.1-required label set every reference
// runtime carries: the chart's reference-runtime marker plus the §26.3
// maintainer/upstream labels.
func referenceLabels(upstream string) map[string]string {
	l := map[string]string{
		"lenny.dev/reference-runtime": "true",
		"maintainer":                  "lennylabs",
	}
	if upstream != "" {
		l["upstream"] = upstream
	}
	return l
}

// referenceRuntimes is the §26 reference-runtime catalog. lenny up
// registers every entry as a platform-global record and grants the
// default tenant access to it. The §26 catalog publishes images under
// ghcr.io/lennylabs/runtime-<name>:1.0.0. The §26.1 catalog table
// fixes the integration level of each runtime: every reference
// runtime, including chat, is Full.
var referenceRuntimes = []ReferenceRuntime{
	codingAgent("claude-code",
		[]string{"anthropic_direct", "aws_bedrock", "gcp_vertex_anthropic"},
		[]string{"anthropic"},
		"Anthropic's Claude Code CLI inside a Lenny-managed sandbox",
		"anthropic/claude-code"),
	codingAgent("gemini-cli",
		[]string{"gcp_vertex_gemini", "google_ai_studio"},
		[]string{"google"},
		"Google's Gemini CLI inside a Lenny-managed sandbox",
		"google-gemini/gemini-cli"),
	codingAgent("codex",
		[]string{"openai_direct", "azure_openai"},
		[]string{"openai"},
		"OpenAI's Codex CLI inside a Lenny-managed sandbox",
		"openai/codex"),
	codingAgent("cursor-cli",
		[]string{"cursor_direct"},
		[]string{"cursor"},
		"Cursor's agent CLI inside a Lenny-managed sandbox",
		"cursor/cli"),
	{
		// spec: §26.1 line 22 / §26.7 — chat is the minimum useful runtime:
		// Full level (hotRotation: true requires the Full-only lifecycle
		// channel per §15.4.3), the small resource class only, multi_turn
		// with immediate (no queued) injection.
		Name:                   "chat",
		Image:                  "ghcr.io/lennylabs/runtime-chat:1.0.0" + placeholderDigest,
		IntegrationLevel:       "full",
		Description:            "Talk to an LLM with no tools; the minimum useful runtime",
		AllowedResourceClasses: []string{"small"},
		SupportedProviders:     []string{"anthropic_direct", "openai_direct", "gcp_vertex_gemini"},
		Capabilities: &runtimeCapabilities{
			Interaction: "multi_turn",
			Injection:   &injectionCaps{Supported: true, Modes: []string{"immediate"}},
		},
		CredentialCapabilities: &credentialCapabilities{HotRotation: true, ProxyDialect: []string{"anthropic", "openai", "google"}},
		Labels:                 referenceLabels(""),
	},
	{
		Name:             "langgraph",
		Image:            "ghcr.io/lennylabs/runtime-langgraph:1.0.0" + placeholderDigest,
		IntegrationLevel: "full",
		Description:      "LangGraph graph-based agents (Python)",
		Labels:           referenceLabels("langchain-ai/langgraph"),
	},
	{
		Name:             "mastra",
		Image:            "ghcr.io/lennylabs/runtime-mastra:1.0.0" + placeholderDigest,
		IntegrationLevel: "full",
		Description:      "Mastra agent framework (TypeScript)",
		Labels:           referenceLabels("mastra-ai/mastra"),
	},
	{
		// spec: §26.10 operator warning — OpenAI's hosted code interpreter
		// runs outside Lenny's sandbox; operators concerned about code
		// execution isolation should disable code_interpreter in their
		// assistant configuration on OpenAI's side.
		Name:             "openai-assistants",
		Image:            "ghcr.io/lennylabs/runtime-openai-assistants:1.0.0" + placeholderDigest,
		IntegrationLevel: "full",
		Description:      "OpenAI Assistants API-compatible runtime; OpenAI's code_interpreter runs outside Lenny's sandbox (see §26.10).",
		Labels:           referenceLabels("openai/openai-python"),
	},
	{
		Name:             "crewai",
		Image:            "ghcr.io/lennylabs/runtime-crewai:1.0.0" + placeholderDigest,
		IntegrationLevel: "full",
		Description:      "CrewAI multi-agent framework with delegation wired to lenny/delegate_task",
		Labels:           referenceLabels("crewAIInc/crewAI"),
	},
}

// EchoRuntimeName is the runtime identifier the Embedded Mode bootstrap
// seeds for the §15.4.4 echo conformance exemplar. The runtime record,
// the echo warm pool's runtimeRef, and the applied echo Runtime CRD all
// use this name so the gateway resolves `lenny session new --runtime echo`
// to the seeded pod path.
const EchoRuntimeName = "echo"

// echoImageRepository is the canonical OCI repository the embedded echo
// runtime image is imported into the embedded containerd under. The
// bring-up resolves the imported image's content digest at `lenny up`
// time and overwrites echoRuntime.Image with
// echoImageRepository + "@sha256:<resolved-digest>" before the bootstrap
// seed and the Runtime-CRD apply read it (S5/S6); the static literal here
// carries only the sentinel placeholder. spec: §26.2 line 38 (canonical
// ghcr.io/lennylabs/runtime-<name> path).
const echoImageRepository = "ghcr.io/lennylabs/runtime-echo-embedded"

// echoImageSentinel is the image reference the static echo catalog entry
// carries before the bring-up resolves the imported image's real digest.
// It is digest-pinned (so the entry satisfies the §5.1/§13.1 syntax) but
// distinct from placeholderDigest so the §26-only placeholder-pinned
// scans — placeholderPinnedRuntimes and the bootstrap-seed loop — never
// treat the runnable echo record as a placeholder-pinned §26 entry. The
// bring-up overwrites it with the import-time-resolved reference. spec:
// §15.4.4, §17.4.
const echoImageSentinel = echoImageRepository + "@sha256:1111111111111111111111111111111111111111111111111111111111111111"

// echoRuntime is the §15.4.4 echo conformance exemplar the Embedded Mode
// bootstrap seeds as a credential-free, pod-deployable runtime, distinct
// from the §26 reference runtimes (echo is "a conformance exemplar
// embedded in the platform repo", §26.1). It is declared outside the
// referenceRuntimes slice so the §26-only loops — placeholderPinnedRuntimes
// and the bootstrap-seed runtime loop in buildBootstrapSeed — do not treat
// it as a placeholder-pinned §26 record; the echo seed is wired
// explicitly. The entry carries no LLM provider, no supportedProviders,
// and no credentialCapabilities, so the runtime leases no credentials
// (§13). echo-embedded is Basic-level (cmd/runtimes/echo-embedded), so the
// entry declares integrationLevel: basic, and it carries a single-pod warm
// pool (warmCount: 1, the §5.2 hot-pool taxonomy) so the WarmPoolController
// pre-warms one echo pod and the first session claims it. The Image is the
// sentinel placeholder the bring-up overwrites with the import-time-resolved
// echo-embedded digest (S5/S6).
//
// spec: §15.4.4 (echo conformance exemplar), §5.2 (warm pool), §17.4
// (Embedded Mode seed).
var echoRuntime = ReferenceRuntime{
	Name:             EchoRuntimeName,
	Image:            echoImageSentinel,
	IntegrationLevel: "basic",
	Description:      "Credential-free echo conformance exemplar; the pod-deployable runtime lenny up runs out of the box",
	// echocore consumes one `message` and produces one `response` with no
	// mid-session injection channel, so the runtime declares the §5.1
	// one_shot interaction model (multi_turn requires injection.supported,
	// which echocore has no channel for). spec: §5.1 (interaction model).
	Capabilities: &runtimeCapabilities{
		Interaction: "one_shot",
	},
	// Credential-free, single-pod, restricted-egress posture matching the
	// Kind precedent echo-pool-embedded (tests/testinfra/kind/install.sh).
	DefaultPoolConfig: &defaultPoolConfig{WarmCount: 1, ResourceClass: "small", EgressProfile: "restricted"},
	// echo carries the credential-free label set: no reference-runtime
	// marker, no provider. It is the §15.4.4 exemplar rather than a §26
	// reference runtime.
	Labels: map[string]string{
		"lenny.dev/echo-runtime": "true",
		"maintainer":             "lennylabs",
	},
}

// EchoRuntime returns a copy of the §15.4.4 echo conformance-exemplar
// catalog entry the Embedded Mode stack seeds. spec: §15.4.4, §17.4.
func EchoRuntime() ReferenceRuntime {
	return echoRuntime
}

// ReferenceRuntimes returns a copy of the §26 catalog the Embedded
// Mode stack installs.
func ReferenceRuntimes() []ReferenceRuntime {
	out := make([]ReferenceRuntime, len(referenceRuntimes))
	copy(out, referenceRuntimes)
	return out
}

// hasPlaceholderDigest reports whether image carries the placeholder
// digest sentinel. §17.4 pulls a runtime image lazily on its first
// session start; a placeholder-pinned image fails the pull until the
// operator re-registers the runtime with the published digest or
// imports a local image with `lenny image import`.
func hasPlaceholderDigest(image string) bool {
	return strings.Contains(image, placeholderDigest)
}

// placeholderPinnedRuntimes returns the names of catalog entries whose
// image is placeholder-pinned, in catalog order. The bootstrap output
// warns the operator that these runtimes register but cannot start a
// session until re-pinned. spec: §26.1 line 5 ("functioning agents");
// §26.3 lines 215-223 (Bootstrap behavior).
func placeholderPinnedRuntimes() []string {
	var names []string
	for _, rt := range referenceRuntimes {
		if hasPlaceholderDigest(rt.Image) {
			names = append(names, rt.Name)
		}
	}
	return names
}
