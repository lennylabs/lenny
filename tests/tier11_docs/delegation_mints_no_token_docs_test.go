// SPDX-License-Identifier: MIT

// Tier-11 documentation checks for the reader-facing pages that state that
// pods hold no Lenny-issued JWT: session creation mints no token, internal
// delegation through lenny/delegate_task mints no token, a delegated child
// is refused when its parent is terminal or absent, and the only
// actor_token exchange is the external a2a_delegation one. Each case pins
// the sentence or table row the specification fixes and names the retired
// wording it replaced. No external infrastructure.

package tier11_docs_test

import "testing"

// TestErrorCatalogDelegationParentRevokedRow_spec_15_1 asserts the error
// catalog describes DELEGATION_PARENT_REVOKED as the creation-fence refusal
// of a delegated child whose parent is terminal or absent, carries no
// DELEGATION_AUDIT_CONTENTION row, and points a mid-session upload at the
// caller's bearer token.
//
// diagnosis: A failure means the catalog still ties the refusal to a
// rotated or revoked parent token and the internal child-token exchange,
// still lists revocationReason, still documents the deleted audit-contention
// code, or still names a session bearer credential that no session holds.
//
// spec: 15.1 (error table DELEGATION_PARENT_REVOKED and
// UPLOAD_TOKEN_CONSUMED rows), 8.2 (Parent freshness), 12.8 (Creation fence)
func TestErrorCatalogDelegationParentRevokedRow_spec_15_1(t *testing.T) {
	runDocPageChecks(t, []docPageCheck{
		{
			page:       "reference/error-catalog.md",
			row:        "| `DELEGATION_PARENT_REVOKED` |",
			contains:   []string{"| 403 |", "terminal or absent when the child session row was inserted", "No child session is created and no pod is allocated", "Only `details.parentSessionId` is included", "Not retryable"},
			notContain: []string{"revocationReason", "child-token exchange", "token_rotated", "recursive_revocation"},
		},
		{
			page:       "reference/error-catalog.md",
			notContain: []string{"DELEGATION_AUDIT_CONTENTION"},
		},
		{
			page:       "reference/error-catalog.md",
			row:        "| `UPLOAD_TOKEN_CONSUMED` |",
			contains:   []string{"use the caller's bearer token"},
			notContain: []string{"session bearer credential"},
		},
	})
}

// TestTokenEndpointDocsNameOnlyExternalActorTokenExchange_spec_13_3 asserts
// the authentication guide, the admin API reference, the curl examples, the
// configuration reference, the credentials tutorial, and the architecture
// overview describe actor_token minting as the external a2a_delegation
// exchange only, and state that internal delegation and session creation
// mint no token.
//
// diagnosis: A failure means a page still lists the gateway as an internal
// caller of the token endpoint, still tells a reader to pass a parent
// session token as actor_token, still says a playground session delegates
// by exchanging its bearer, or still lists session-capability tokens among
// the gateway's in-process mints.
//
// spec: 13.3 (Token exchange), 8.2 (Child identity), 10.2 (Signing
// topology), 15.1 (POST /v1/oauth/token row)
func TestTokenEndpointDocsNameOnlyExternalActorTokenExchange_spec_13_3(t *testing.T) {
	runDocPageChecks(t, []docPageCheck{
		{
			page:       "client-guide/authentication.md",
			row:        "For token lifecycle operations inside Lenny",
			contains:   []string{"rotating an admin token or minting a narrowed scoped token for an agent service account"},
			notContain: []string{"internal delegation child-token issuance"},
		},
		{
			page:       "api/admin.md",
			notContain: []string{"| Gateway (internal) |", "parent_session_token", "as it does when a playground session delegates"},
		},
		{
			page:     "api/admin.md",
			row:      "For rotation, use `grant_type=",
			contains: []string{"For `a2a_delegation` child-token minting", "the delegating caller's token"},
		},
		{
			page:       "client-guide/sdk-examples/curl.md",
			row:        "child-token minting, additionally supply",
			contains:   []string{"For `a2a_delegation` child-token minting", "the delegating caller's token"},
			notContain: []string{"PARENT_SESSION_TOKEN"},
		},
		{
			page:       "reference/configuration.md",
			row:        "| `tokenExchange.rateLimitPerUser` |",
			contains:   []string{"Token rotation and `a2a_delegation` child-token minting share this budget."},
			notContain: []string{"delegation minting share"},
		},
		{
			page:     "reference/configuration.md",
			row:      "| `tokenExchange.requireActorToken` |",
			contains: []string{"Require `actor_token` on `a2a_delegation` child-token minting."},
		},
		{
			page:       "tutorials/user-credentials.md",
			row:        "for the full RFC 8693 parameter set",
			contains:   []string{"`actor_token` for `a2a_delegation` child-token minting"},
			notContain: []string{"for delegation minting"},
		},
		{
			page:       "getting-started/architecture.md",
			row:        "Externally, token issuance",
			contains:   []string{"`a2a_delegation` child-token minting", "delegating caller's token in the `actor_token` field", "Internal delegation through `lenny/delegate_task` mints no token"},
			notContain: []string{"session-capability tokens of the session-creation path", "parent session token"},
		},
	})
}

// TestSecurityGuideBearerTokensItemDropsInternalMints_spec_10_2 asserts the
// bearer-tokens item of the security guide names a2a_delegation child
// tokens, lists credential leasing as the only internal Token Service call,
// and states that an impersonation bearer cannot create a session and that
// the refusal is audited.
//
// diagnosis: A failure means the item still lists delegation minting or
// session-capability tokens as mints, or no longer states the audited
// session-creation refusal of an impersonation bearer.
//
// spec: 10.2 (Signing topology, step (1a) caller check), 13.3
// (Platform-admin impersonation), 16.7 (token.exchanged session_mint
// rejection)
func TestSecurityGuideBearerTokensItemDropsInternalMints_spec_10_2(t *testing.T) {
	runDocPageChecks(t, []docPageCheck{
		{
			page:       "operator-guide/security.md",
			row:        "3. **Bearer tokens**",
			contains:   []string{"`a2a_delegation` child tokens", "(credential leasing)", "An impersonation bearer cannot create a session; the refusal is audited."},
			notContain: []string{"delegation minting", "admin tokens, delegation child tokens"},
		},
	})
}

// TestInternalAPIIdentityVerificationNamesProjectedSAToken_spec_10_3
// asserts the internal API reference states that the gateway validates the
// pod's projected ServiceAccount token on every pod-to-gateway request.
//
// diagnosis: A failure means the page still says pods hold session JWTs
// whose signature the gateway checks, which contradicts the boundary table:
// pods authenticate with mTLS and a projected ServiceAccount token.
//
// spec: 10.3 (Projected SA token), 13.3 (credential flow)
func TestInternalAPIIdentityVerificationNamesProjectedSAToken_spec_10_3(t *testing.T) {
	runDocPageChecks(t, []docPageCheck{
		{
			page:     "api/internal.md",
			row:      "projected ServiceAccount token on every pod-to-gateway request",
			contains: []string{"signature, expiry, and audience"},
		},
		{
			page:       "api/internal.md",
			notContain: []string{"Pods cannot forge or extend session JWTs"},
		},
	})
}

// TestKMSSignRateDocsDropSessionAndDelegationTerms_spec_17_8_2 asserts the
// KMS Sign quota section of the key-rotation runbook sizes the quota from
// token renewals and operator mints only, and names playground-bearer or
// impersonation mints as the gateway's sign traffic.
//
// diagnosis: A failure means the runbook still counts a session-creation or
// delegation sign rate, which no longer exists because neither path mints a
// token.
//
// spec: 17.8.2 (KMS Sign rate), 16.5 (KMSSignQuotaApproaching)
func TestKMSSignRateDocsDropSessionAndDelegationTerms_spec_17_8_2(t *testing.T) {
	runDocPageChecks(t, []docPageCheck{
		{
			page:       "runbooks/jwt-key-rotation.md",
			row:        "A rate that rose suddenly on one component",
			contains:   []string{"a burst of playground-bearer or impersonation mints on the gateway"},
			notContain: []string{"burst of session creation"},
		},
		{
			page:       "runbooks/jwt-key-rotation.md",
			row:        "Size the request to at least the deployment's peak sign rate",
			contains:   []string{"the sum of the token renewal rate of every holder that renews before expiry and infrequent operator mints"},
			notContain: []string{"session creation rate", "delegation rate"},
		},
	})
}

// TestTokenStoreUnavailableDocsDropDelegationMinting_spec_16_5 asserts the
// observability alert table and the token-store-unavailable runbook name
// session creation and credential leasing as the paths that fail closed,
// and no longer list delegation minting.
//
// diagnosis: A failure means a page still says the token store outage
// blocks delegation minting, which no longer exists, or dropped session
// creation, which still fails closed on its issued_tokens read.
//
// spec: 16.5 (TokenStoreUnavailable), 10.2 (step (1a) caller check), 8.2
// (Child identity)
func TestTokenStoreUnavailableDocsDropDelegationMinting_spec_16_5(t *testing.T) {
	runDocPageChecks(t, []docPageCheck{
		{
			page:       "operator-guide/observability.md",
			row:        "| `TokenStoreUnavailable` |",
			contains:   []string{"session creation and credential leasing both fail"},
			notContain: []string{"delegation minting"},
		},
		{
			page:       "runbooks/token-store-unavailable.md",
			contains:   []string{"session creation and credential leasing are blocked platform-wide"},
			notContain: []string{"delegation minting"},
		},
	})
}
