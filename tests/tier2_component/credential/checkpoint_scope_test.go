// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component test for the §26.8 langgraph checkpointing credential
// scope. §26.8 states that when a langgraph session sets
// runtimeOptions.checkpointBackend to postgres or redis, "the runtime's
// adapter connects via short-lived credentials issued by the credential
// leasing service with scope datastore.checkpoint.rw" against "Lenny's
// platform Postgres/Redis, scoped to the session's tenant."
//
// See the sibling tests/tier9_security/checkpoint_tenant_isolation_test.go
// for the negative cross-tenant rejection half of this contract.
package credential_test

import "testing"

// spec: §26.8 ("Checkpointing (when runtimeOptions.checkpointBackend:
// postgres or redis) uses Lenny's platform Postgres/Redis, scoped to the
// session's tenant. The runtime's adapter connects via short-lived
// credentials issued by the credential leasing service with scope
// datastore.checkpoint.rw.")
//
// diagnosis: once unskipped, a failure here means the credential leasing
// service either does not issue a datastore.checkpoint.rw-scoped lease for
// a langgraph session with checkpointing enabled, or issues one that is
// not bound to the session's own tenant.
func TestCredentialLeasingServiceIssuesCheckpointScopeBoundToSessionTenant(t *testing.T) {
	// §4.9 ("Credential Leasing Service") defines the full credential
	// model this behavior would need: the Provider enum
	// (pkg/credential.Provider, built-ins anthropic_direct, aws_bedrock,
	// vertex_ai, azure_openai, github, vault_transit, plus a pass-through
	// "Custom" case), the CredentialPool/hostPatterns pool-routing
	// mechanism, and the Lease type minted by pkg/credential/lease_mint.go.
	// None of the three model any notion of a "datastore" provider, a
	// "checkpoint" resource, or a "<namespace>.<resource>.<mode>" scope
	// grammar comparable to the §14 gitClone.auth.leaseScope
	// ("vcs.<provider>.read|write") string — the credential.Lease type
	// (pkg/credential/lease.go) carries no Scope field at all, and no
	// package in this repository mints a lease keyed on the string
	// "datastore.checkpoint.rw" or on runtimeOptions.checkpointBackend. A
	// repository-wide search for "datastore.checkpoint" and
	// "checkpointBackend" (pkg/, tests/) confirms nothing implements or
	// exercises this beyond the single spec sentence at
	// spec/26_reference-runtime-catalog.md:386 and the schema-only
	// enum/default declared for runtimeOptions.checkpointBackend at
	// spec/14_workspace-plan-schema.md:183 (validated generically as an
	// opaque JSON Schema field, never inspected by gateway logic). The
	// langgraph reference runtime itself is deferred to Wave 6 / Phase 11
	// per tests/spec-map-exceptions.yaml section 26.8, matching the
	// existing non-blocking skip on
	// tests/tier5_e2e_kind/framework_runtime_langgraph_test.go.
	t.Skip("credential leasing service defines no datastore/checkpoint provider, scope grammar, or checkpointBackend-triggered lease issuance yet (§4.9 is silent on this scope; §26.8 langgraph is itself deferred to Phase 11) — needs a spec/product decision before this can be driven against real code")
}
