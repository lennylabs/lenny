// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for the §26.8 langgraph checkpointing credential
// scope's tenant isolation: a datastore.checkpoint.rw-scoped credential
// must be bound to the issuing session's own tenant, so a langgraph
// checkpoint write against another tenant's platform Postgres/Redis
// checkpoint storage is rejected.
//
// See the sibling
// tests/tier2_component/credential/checkpoint_scope_test.go for the
// positive issuance half of this contract.
package tier9_security_test

import "testing"

// spec: §26.8 ("Checkpointing ... uses Lenny's platform Postgres/Redis,
// scoped to the session's tenant. The runtime's adapter connects via
// short-lived credentials issued by the credential leasing service with
// scope datastore.checkpoint.rw.")
//
// diagnosis: once unskipped, a failure here means a datastore.checkpoint.rw
// credential minted for one tenant's session was accepted for a checkpoint
// write against a different tenant's checkpoint storage — a cross-tenant
// data-isolation failure.
func TestCheckpointCredentialRejectsCrossTenantWrite(t *testing.T) {
	// This is the negative half of the same gap documented in
	// tests/tier2_component/credential/checkpoint_scope_test.go: the
	// credential leasing service (pkg/credential, §4.9) has no
	// datastore.checkpoint.rw scope, no "datastore" provider, and no
	// checkpointBackend-triggered lease issuance to scope to a tenant in
	// the first place, so there is no tenant-bound credential here to
	// attempt a cross-tenant write with. The platform's tenant-isolation
	// primitive for direct-to-Postgres access is PostgreSQL RLS keyed on
	// app.current_tenant (spec/04_system-components.md §4.2), but nothing
	// wires a langgraph checkpoint connection through that path today —
	// the langgraph reference runtime is deferred to Wave 6 / Phase 11 per
	// tests/spec-map-exceptions.yaml section 26.8.
	t.Skip("credential leasing service defines no datastore/checkpoint provider or scope to bind to a tenant, so no cross-tenant rejection path exists yet to drive (§4.9 is silent on this scope; §26.8 langgraph is itself deferred to Phase 11) — needs a spec/product decision before this can be driven against real code")
}
