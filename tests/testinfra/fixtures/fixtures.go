// SPDX-License-Identifier: MIT

// Package fixtures holds the §18.1 reference data used across tiers.
//
// Identifiers follow the §17 cryptography convention: alice / bob /
// carol for users, acme / globex / initech for tenants.
//
// The fixture surface is intentionally narrow — every test that
// needs a tenant, user, runtime, pool, credential pool, delegation
// policy, or environment reads from this package. When the spec
// grows new reference data, the constants here are the single point
// of update.
package fixtures

// Tenant identifiers — §17 cryptography convention.
const (
	TenantAcme    = "acme"
	TenantGlobex  = "globex"
	TenantInitech = "initech"
)

// User identifiers — §17 cryptography convention.
const (
	UserAlice = "alice@acme.com"
	UserBob   = "bob@acme.com"
	UserCarol = "carol@globex.com"
	UserDave  = "dave@globex.com"
	UserErin  = "erin@initech.com"
)

// Runtime references — bundled and reference catalog. The bundled
// runtimes are echo / streaming-echo / delegation-echo per TESTING.md
// §11. The reference catalog ships per §12.10.
const (
	RuntimeEcho           = "echo"
	RuntimeStreamingEcho  = "streaming-echo"
	RuntimeDelegationEcho = "delegation-echo"
	// RuntimePreConnectEcho is the §6.1 SDK-warm reference runtime
	// (cmd/runtimes/preconnect-echo): it declares capabilities.preConnect
	// so a pool warms its pods through sdk_connecting and the gateway
	// drives them via ConfigureWorkspace / DemoteSDK.
	RuntimePreConnectEcho = "preconnect-echo"

	RuntimeClaudeCode       = "claude-code"
	RuntimeGeminiCLI        = "gemini-cli"
	RuntimeCodex            = "codex"
	RuntimeCursorCLI        = "cursor-cli"
	RuntimeChat             = "chat"
	RuntimeLangGraph        = "langgraph"
	RuntimeMastra           = "mastra"
	RuntimeOpenAIAssistants = "openai-assistants"
	RuntimeCrewAI           = "crewai"
)

// Pool names — §17 reference fixtures.
const (
	PoolAcmeDefaultRunc     = "acme-default-runc"
	PoolAcmeDefaultGvisor   = "acme-default-gvisor"
	PoolGlobexDefaultGvisor = "globex-default-gvisor"
)

// Credential pools — mock providers for tier-4 and tier-5.
const (
	CredentialPoolMockAnthropic = "mock-anthropic"
	CredentialPoolMockOpenAI    = "mock-openai"
	CredentialPoolMockGoogle    = "mock-google"
)

// Delegation policies.
const (
	DelegationPolicyCodingDefault = "coding-default"
	DelegationPolicyChatStrict    = "chat-strict"
)

// Environment paths.
const (
	EnvAcmeDev   = "acme/dev"
	EnvAcmeProd  = "acme/prod"
	EnvGlobexDev = "globex/dev"
)
