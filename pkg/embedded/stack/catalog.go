// SPDX-License-Identifier: MIT

package stack

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

// referenceRuntimes is the §26 reference-runtime catalog. lenny up
// registers every entry as a platform-global record and grants the
// default tenant access to it. The §26 catalog publishes images under
// ghcr.io/lennylabs/runtime-<name>:1.0.0. The §26.1 catalog table
// fixes the integration level of each runtime: chat is Standard, every
// other reference runtime is Full.
var referenceRuntimes = []ReferenceRuntime{
	{
		Name:             "claude-code",
		Image:            "ghcr.io/lennylabs/runtime-claude-code:1.0.0" + placeholderDigest,
		IntegrationLevel: "full",
		Description:      "Anthropic's Claude Code CLI inside a Lenny-managed sandbox",
	},
	{
		Name:             "gemini-cli",
		Image:            "ghcr.io/lennylabs/runtime-gemini-cli:1.0.0" + placeholderDigest,
		IntegrationLevel: "full",
		Description:      "Google's Gemini CLI inside a Lenny-managed sandbox",
	},
	{
		Name:             "codex",
		Image:            "ghcr.io/lennylabs/runtime-codex:1.0.0" + placeholderDigest,
		IntegrationLevel: "full",
		Description:      "OpenAI's Codex CLI inside a Lenny-managed sandbox",
	},
	{
		Name:             "cursor-cli",
		Image:            "ghcr.io/lennylabs/runtime-cursor-cli:1.0.0" + placeholderDigest,
		IntegrationLevel: "full",
		Description:      "Cursor's agent CLI inside a Lenny-managed sandbox",
	},
	{
		Name:             "chat",
		Image:            "ghcr.io/lennylabs/runtime-chat:1.0.0" + placeholderDigest,
		IntegrationLevel: "standard",
		Description:      "Talk to an LLM with no tools; the minimum useful runtime",
	},
	{
		Name:             "langgraph",
		Image:            "ghcr.io/lennylabs/runtime-langgraph:1.0.0" + placeholderDigest,
		IntegrationLevel: "full",
		Description:      "LangGraph graph-based agents (Python)",
	},
	{
		Name:             "mastra",
		Image:            "ghcr.io/lennylabs/runtime-mastra:1.0.0" + placeholderDigest,
		IntegrationLevel: "full",
		Description:      "Mastra agent framework (TypeScript)",
	},
	{
		Name:             "openai-assistants",
		Image:            "ghcr.io/lennylabs/runtime-openai-assistants:1.0.0" + placeholderDigest,
		IntegrationLevel: "full",
		Description:      "OpenAI Assistants API-compatible runtime",
	},
	{
		Name:             "crewai",
		Image:            "ghcr.io/lennylabs/runtime-crewai:1.0.0" + placeholderDigest,
		IntegrationLevel: "full",
		Description:      "CrewAI multi-agent framework with delegation wired to lenny/delegate_task",
	},
}

// ReferenceRuntimes returns a copy of the §26 catalog the Embedded
// Mode stack installs.
func ReferenceRuntimes() []ReferenceRuntime {
	out := make([]ReferenceRuntime, len(referenceRuntimes))
	copy(out, referenceRuntimes)
	return out
}
