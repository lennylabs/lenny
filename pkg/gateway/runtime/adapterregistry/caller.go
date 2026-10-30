// SPDX-License-Identifier: MIT

package adapterregistry

import (
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// CallerIdentity is the §15 projection of the authenticated caller's JWT
// claims that the gateway exposes to adapters on
// SessionMetadata.CallerIdentity. It is derived from the §13.3 Lenny JWT
// claim structure at session-create time and is immutable for the
// session's lifetime. Adapters treat it as read-only and do not
// re-validate or re-resolve the claims, because the gateway auth
// middleware has already verified the token and enforced scope narrowing
// and tenant scope before the principal reaches a handler.
//
// spec: §15 "Shared Adapter Types" CallerIdentity; §13.3 "Lenny JWT claim
// structure".
type CallerIdentity struct {
	// Sub is the authenticated subject identifier (JWT `sub` claim). It is
	// opaque and scoped to the issuing IdP; adapters do not parse it.
	Sub string

	// CallerType is the JWT `caller_type` claim: "human", "service", or
	// "agent" (§25.1).
	CallerType string

	// Scope is the space-separated scope set granted to the token (JWT
	// `scope` claim), verbatim. Empty when the token carries no scope
	// claim.
	Scope string

	// Act carries the RFC 8693 `act` claim of the caller's bearer. It is
	// nil when the bearer carries none. An adapter that surfaces
	// delegation provenance renders Act.Sub as the delegating actor.
	Act *ActorClaim
}

// ActorClaim is the §15 projection of the RFC 8693 `act` claim exposed to
// adapters. Its fields mirror the JWT `act` claim one-for-one.
//
// It is a distinct type from jwt.ActorClaim, whose four fields carry the
// same values, because the two types serve different contracts. The JWT
// type is a wire encoding with JSON tags and the Go field name Subject,
// and the §15 type is the normative adapter-facing Go structure, whose
// field is named Sub. A third-party adapter compiles against the §15
// field names, so aliasing the JWT type would expose a structure the
// specification does not state. ActorClaimFromJWT is the single mapping
// between them.
//
// spec: §15 "Shared Adapter Types" ActorClaim; §13.3 "Lenny JWT claim
// structure" (the `act` row).
type ActorClaim struct {
	// Sub is the delegating actor's JWT `sub`.
	Sub string

	// TenantID is the delegating actor's tenant scope. §13.3 forbids
	// cross-tenant delegation, so it equals SessionMetadata.TenantID.
	TenantID string

	// SessionID is the delegating actor's session ID. It is empty for an
	// actor that is not itself a Lenny session.
	SessionID string

	// DelegationDepth is the delegating actor's delegation depth.
	DelegationDepth int
}

// ActorClaimFromJWT projects a verified JWT `act` claim onto the §15
// ActorClaim. It returns nil when the bearer carries no `act` claim, so
// CallerIdentity.Act is nil exactly when the bearer has none. The result
// is a fresh value, so an adapter that holds it cannot reach the verified
// claims.
//
// spec: §15 "Shared Adapter Types" CallerIdentity.Act and ActorClaim.
func ActorClaimFromJWT(a *jwt.ActorClaim) *ActorClaim {
	if a == nil {
		return nil
	}
	return &ActorClaim{
		Sub:             a.Subject,
		TenantID:        a.TenantID,
		SessionID:       a.SessionID,
		DelegationDepth: a.DelegationDepth,
	}
}

// CallerIdentityFromPrincipal projects the authenticated principal the
// gateway auth middleware attached to the session-create request onto the
// §15 CallerIdentity: Sub from Principal.Subject, CallerType from
// Principal.CallerType, Scope from the verbatim scope claim, and Act from
// Principal.Act.
//
// spec: §15 "Shared Adapter Types" SessionMetadata.CallerIdentity and
// CallerIdentity.
func CallerIdentityFromPrincipal(p authmw.Principal) CallerIdentity {
	return CallerIdentity{
		Sub:        p.Subject,
		CallerType: p.CallerType,
		Scope:      p.Scopes.Raw,
		Act:        ActorClaimFromJWT(p.Act),
	}
}
