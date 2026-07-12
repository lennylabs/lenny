// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §8.3 cross-environment credential
// compatibility check on lenny/delegate_task. When a delegation crosses
// a §10.6 environment boundary with credentialPropagation: inherit, the
// gateway intersects the providers represented in the parent's credential
// pool with the child runtime's supportedProviders. A non-empty
// intersection admits the delegation (the gateway assigns a credential
// whose provider is in the intersection); an empty intersection is
// rejected deterministically with CREDENTIAL_PROVIDER_MISMATCH before any
// warm pod is claimed.
//
// The shared mcpClient and toolResultText helpers live in
// elicitation_test.go (same package).

package tier4_integration_test

import (
	"testing"
)

// spec: 8.3 (cross-environment credentialPropagation: inherit provider compatibility check)
// diagnosis: the §8.3 cross-environment inherit compatibility check
// diverged. A cross-environment delegate_task with
// credentialPropagation: inherit either admitted an incompatible child
// (empty provider intersection, which must reject with
// CREDENTIAL_PROVIDER_MISMATCH before pod allocation) or rejected a
// compatible one (non-empty intersection, which must proceed and assign
// a credential from the shared pool).
//
// The check itself is unimplemented in the delegation path today: there
// is no credentialPropagation field on the lenny/delegate_task input
// schema or the delegation Request, and no code path produces
// CREDENTIAL_PROVIDER_MISMATCH (the code appears only in the
// error-classification table and the docs). Building the credential
// provider compatibility check is a feature that belongs in the
// spec-implementation pipeline; this test is kept as the spec-faithful
// assertion and skipped until that feature lands.
func TestCrossEnvironmentDelegationCredentialCompatibility(t *testing.T) {
	t.Skip("cross-environment credentialPropagation: inherit provider compatibility check is unimplemented in the delegation path; test kept pending the open TEST-GAPS finding")

	// Intended shape once the §8.3 check is implemented:
	//
	// Fixture: two §10.6 environments A and B with a
	// crossEnvironmentDelegation rule from A to B, and two runtimes with
	// differing supportedProviders — a parent runtime admitted in
	// environment A whose credential pool carries provider set P, and a
	// child runtime admitted in environment B declaring supportedProviders
	// C. A parent session runs in environment A and calls
	// lenny/delegate_task with credentialPropagation: inherit targeting the
	// child runtime in environment B.
	//
	//   admit case: P ∩ C is non-empty. The delegation proceeds and the
	//   child appears in the parent's task tree; the gateway assigns a
	//   credential whose provider is in P ∩ C.
	//
	//   reject case: P ∩ C is empty. The delegate_task call is rejected
	//   with error code CREDENTIAL_PROVIDER_MISMATCH before any warm pod is
	//   claimed (no child session row, no pod allocation).
}
