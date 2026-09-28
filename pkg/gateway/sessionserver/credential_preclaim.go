// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lennylabs/lenny/pkg/admission/direct_mode_isolation"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// DelegationCredentialQuery describes the prospective delegated child
// whose credential availability the §8.3 delegation-time pre-check
// evaluates before the delegation is admitted. CredentialOriginSessionID
// is the parent's resolved origin for an inherit hop and empty for an
// independent hop, so the checker constrains the eligible provider set to
// the origin pool exactly as a finalized inherit child would be.
// spec: §8.3
type DelegationCredentialQuery struct {
	TenantID                  string
	UserID                    string
	ChildRuntimeRef           string
	CredentialOriginSessionID string
}

// ErrDelegationCredentialUnavailable is the typed exhaustion result the
// §8.3 delegation-time pre-check returns when no credential is assignable
// for the prospective delegated child. The delegate handler branches on
// it with errors.Is rather than a string match and maps it to the
// CREDENTIAL_POOL_EXHAUSTED wire code.
// spec: §8.3; §4.9
var ErrDelegationCredentialUnavailable = errors.New("delegation credential unavailable")

// ErrDelegationUserCredentialNotFound is the typed result the §8.3
// delegation-time pre-check returns when the tenant credentialPolicy is
// user-only for every provider in the eligible set and no pre-registered
// user credential exists. It mirrors the distinct outcome session start
// draws for the identical §4.9 engine error (credrouter.ErrUserCredentialNotFound),
// so the delegate handler can surface the same USER_CREDENTIAL_NOT_FOUND
// code rather than an opaque internal error.
// spec: §8.3; §4.9
var ErrDelegationUserCredentialNotFound = errors.New("delegation user credential not found")

// CheckDelegationCredentialAvailability runs the §8.3 delegation-time
// pre-claim credential-availability check for a prospective delegated
// child. It reuses resolveCredentialPools (the §4.9 engine, including the
// inherit origin-pool constraint) against a synthetic child row and maps
// the engine's two typed pre-claim outcomes to sentinels: an exhausted
// pool (credrouter.ErrNoCredentialAvailable) to
// ErrDelegationCredentialUnavailable, and a user-only policy with no
// registered credential (credrouter.ErrUserCredentialNotFound) to
// ErrDelegationUserCredentialNotFound. It claims no pod and reserves no
// lease: this is the point-in-time read the spec requires before pod
// allocation. The synthetic row leaves ID empty, so the origin-constrained
// branch in resolveCredentialPools selects only when the query set an
// origin (inherit constrained; independent unconstrained).
// spec: §8.3; §4.9
func (s *Server) CheckDelegationCredentialAvailability(ctx context.Context, q DelegationCredentialQuery) error {
	row := sessionstore.Session{
		TenantID:                  q.TenantID,
		UserID:                    q.UserID,
		RuntimeRef:                q.ChildRuntimeRef,
		CredentialOriginSessionID: q.CredentialOriginSessionID,
	}
	if _, _, _, err := s.resolveCredentialPools(ctx, row); err != nil {
		switch {
		case errors.Is(err, credrouter.ErrNoCredentialAvailable):
			return ErrDelegationCredentialUnavailable
		case errors.Is(err, credrouter.ErrUserCredentialNotFound):
			return ErrDelegationUserCredentialNotFound
		default:
			return err
		}
	}
	return nil
}

// resolveCredentialPools runs the §4.9 pre-claim credential
// availability check for a session and returns the provider→pool map the
// binder mints pool leases from plus the list of providers that resolved
// to the user source (the §4.9 Pre-Authorized Credential Flow), which the
// binder materializes into proxy-mode user leases. It computes the §4.9
// intersection of
// the session runtime's supportedProviders and the tenant's
// credentialPolicy.providerPools, builds a pool descriptor per provider
// from the credential-pool registry, and asks the CredentialRouter to
// resolve a source for each provider. The check passes when at least
// one provider has an assignable credential; on miss it returns the
// router's typed error (credrouter.ErrNoCredentialAvailable →
// CREDENTIAL_POOL_EXHAUSTED, credrouter.ErrUserCredentialNotFound →
// USER_CREDENTIAL_NOT_FOUND), surfaced by writePodClaimError before any
// pod is claimed.
//
// When the tenant configures no credentialPolicy, or the tenant /
// runtime / credential-pool registries are not all wired, the
// intersection is empty and the session assigns no upstream LLM
// credentials — preserving the pre-§4.9 behavior for deployments
// without credential pools.
//
// spec: §4.9.
func (s *Server) resolveCredentialPools(ctx context.Context, row sessionstore.Session) (map[string]string, map[string]string, []string, error) {
	if s.tenants == nil || s.runtimes == nil || s.credPools == nil {
		return nil, nil, nil, nil
	}
	// spec: §8.3 — a deny hop grants the child no LLM credentials.
	// A deny row resolves to zero eligible providers, so PreClaim runs no
	// assignment and no lease is minted (fail closed). CredentialOriginSessionID
	// cannot express this: a deny child is self-origin, identical to an
	// independent child, so the persisted deny marker is the only signal.
	if row.CredentialDeny {
		return nil, nil, nil, nil
	}
	tenant, err := s.tenants.Get(ctx, row.TenantID)
	if err != nil {
		// The §10.2 tenant-claim extractor already gated the request; an
		// unresolvable tenant row here means no credentialPolicy applies.
		return nil, nil, nil, nil
	}
	policy := tenant.CredentialPolicy
	if !policy.Configured() {
		return nil, nil, nil, nil
	}
	rt, err := runtimestore.Resolve(ctx, s.runtimes, row.RuntimeRef)
	if err != nil {
		// Runtime-resolution failure is surfaced by the pool-resolution
		// path; the §4.9 layer contributes no credentials.
		return nil, nil, nil, nil
	}
	intersection := credrouter.Intersection(rt.SupportedProviders, policy)

	// spec: §8.3. A delegated child that inherited its credential
	// origin draws its provider from the origin pool rather than its own
	// independent set. A session inherited iff its
	// CredentialOriginSessionID is a non-empty ancestor id; a self-origin
	// (or empty) id leaves the top-level/independent path unchanged.
	// Constrain the child's eligible providers to the origin runtime's
	// eligible set, live-resolved at this hop so a mid-tree change to the
	// origin runtime's providers takes effect here. An unresolvable
	// origin session or origin runtime fails closed (empty intersection →
	// CREDENTIAL_POOL_EXHAUSTED at assignment) rather than falling back to
	// the child's own unconstrained set, which would defeat the inherit
	// guarantee. A deny origin row also fails closed: a deny hop holds no
	// origin pool (§8.3), so an inherit hop whose origin
	// traces to a deny session has nothing to inherit and must not derive
	// eligibility from the deny runtime's supportedProviders.
	if row.CredentialOriginSessionID != "" && row.CredentialOriginSessionID != row.ID {
		originRow, originErr := s.store.Get(ctx, row.TenantID, row.CredentialOriginSessionID)
		if originErr != nil {
			intersection = nil
		} else if originRow.CredentialDeny {
			// spec: §8.3 — a deny session holds no origin pool, so
			// an inherit hop from it has nothing to inherit and fails closed to
			// CREDENTIAL_POOL_EXHAUSTED rather than deriving eligibility from the
			// deny runtime's supportedProviders.
			intersection = nil
		} else if originRt, rtErr := runtimestore.Resolve(ctx, s.runtimes, originRow.RuntimeRef); rtErr != nil {
			intersection = nil
		} else {
			originEligible := credrouter.Intersection(originRt.SupportedProviders, policy)
			intersection = intersectProviders(intersection, originEligible)
		}
	}

	allPools, err := s.credPools.List(ctx, row.TenantID, credentialpoolstore.ListFilter{})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("sessionserver: load credential pools for pre-claim: %w", err)
	}
	byName := make(map[string]credentialpoolstore.CredentialPool, len(allPools))
	for _, p := range allPools {
		byName[p.Name] = p
	}

	// spec: §14 credentialPolicy; §4.9 — a per-session
	// credentialPolicy override narrows the tenant policy's
	// preferredSource (the gateway validated at admission that the
	// override only restricts, never expands). When the session carries
	// one, it wins over the tenant default for this session's credential
	// resolution. F-14.1.14.
	preferred := policy.PreferredSource
	if row.CredentialPolicyOverride != nil && row.CredentialPolicyOverride.PreferredSource != "" {
		preferred = credential.PreferredSource(row.CredentialPolicyOverride.PreferredSource)
	}
	in := credrouter.PreClaimInput{
		TenantID:        row.TenantID,
		UserID:          row.UserID,
		PreferredSource: preferred,
	}
	for _, provider := range intersection {
		var descs []credrouter.PoolDescriptor
		for _, poolName := range policy.PoolOrderFor(provider) {
			if p, ok := byName[poolName]; ok {
				descs = append(descs, poolDescriptor(p))
			}
		}
		userAvail := s.userCredChecker != nil &&
			s.userCredChecker(ctx, row.TenantID, row.UserID, provider)
		in.Providers = append(in.Providers, credrouter.ProviderInput{
			Provider:                provider,
			AllowedPools:            descs,
			UserCredentialAvailable: userAvail,
		})
	}

	res, err := credrouter.PreClaim(ctx, s.credRouter, in)
	if err != nil {
		return nil, nil, nil, err
	}

	// spec: §4.9 (per-provider deliveryMode is authoritatively a
	// CredentialPool field) — surface each resolved provider pool's
	// effective deliveryMode alongside the assignment map, keyed by pool
	// name, so the session-start credential-delivery gate evaluates the
	// delivery mode leasing actually uses against the bound pod's
	// isolationProfile/spiffeBinding. The warm-pool/SandboxTemplate
	// deliveryMode the registration and admission layers inspect is a
	// denormalized copy that can diverge from this authoritative value.
	poolDeliveryModes := make(map[string]string, len(res.PoolAssignments))
	for _, poolName := range res.PoolAssignments {
		if p, ok := byName[poolName]; ok {
			poolDeliveryModes[poolName] = p.DeliveryMode
		}
	}

	// spec: §4.9 — the proxy-dialect admission boundary at the
	// runtime↔pool join. A proxy-mode pool declares the wire dialect its
	// lease exposes (`proxyDialect`); the agent pod's SDK can only speak a
	// dialect the runtime declares in credentialCapabilities.proxyDialect.
	// A credential pool carries no static runtime binding, so the runtime
	// and pool first meet concretely here, at the session's pre-claim
	// provider intersection. Reject the session with
	// INVALID_POOL_PROXY_DIALECT before a pod is claimed when an assigned
	// proxy-mode pool declares a dialect the session runtime does not
	// speak (a direct-mode pool declares no dialect and is skipped).
	for _, poolName := range res.PoolAssignments {
		p, ok := byName[poolName]
		if !ok || p.ProxyDialect == "" {
			continue
		}
		if !rt.CredentialCapabilities.AllowsProxyDialect(p.ProxyDialect) {
			return nil, nil, nil, &PoolProxyDialectError{Pool: poolName, Dialect: p.ProxyDialect}
		}
	}

	// spec: §4.9 — the same runtime↔dialect boundary applies to a
	// user-source provider. A user credential is delivered in proxy mode
	// (the secret stays gateway-side), so the agent pod's SDK must speak the
	// provider's canonical proxy dialect. Reject the session when the
	// resolved runtime does not declare it, before a pod is claimed.
	for _, provider := range res.UserProviders {
		dialect, ok := credential.UserProxyDialect(credential.Provider(provider))
		if !ok {
			// The userCredChecker only reports a provider available when it
			// has a canonical dialect, so this is unreachable; treat a
			// dialect-less provider defensively as not deliverable.
			return nil, nil, nil, &PoolProxyDialectError{Pool: "user:" + provider, Dialect: ""}
		}
		if !rt.CredentialCapabilities.AllowsProxyDialect(string(dialect)) {
			return nil, nil, nil, &PoolProxyDialectError{Pool: "user:" + provider, Dialect: string(dialect)}
		}
	}
	return res.PoolAssignments, poolDeliveryModes, res.UserProviders, nil
}

// intersectProviders returns the order-stable (a-ordered) set
// intersection of two provider lists: an element of a is emitted at most
// once, in a's order, when it also appears in b. It is the §8.3 inherit
// constraint's set-∩, kept local to sessionserver so the delegation-path
// lease.IntersectProviders primitive stays out of the llmproxy import
// this package already carries. spec: §8.3.
func intersectProviders(a, b []string) []string {
	inB := make(map[string]struct{}, len(b))
	for _, p := range b {
		inB[p] = struct{}{}
	}
	seen := make(map[string]struct{}, len(a))
	out := make([]string, 0, len(a))
	for _, p := range a {
		if _, ok := inB[p]; !ok {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

// PoolProxyDialectError is the §4.9 runtime↔pool proxy-dialect
// mismatch surfaced at session-creation credential resolution: an
// assigned proxy-mode pool declares a wire dialect the session runtime
// does not declare in credentialCapabilities.proxyDialect.
// writePodClaimError maps it to 422 INVALID_POOL_PROXY_DIALECT carrying
// the spec-verbatim message.
type PoolProxyDialectError struct {
	Pool    string
	Dialect string
}

func (e *PoolProxyDialectError) Error() string {
	return fmt.Sprintf(
		"pool proxyDialect %s is not declared in runtime credentialCapabilities.proxyDialect",
		e.Dialect,
	)
}

// CredentialDeliveryIsolationError is the §4.9 session-start
// credential-delivery rejection: a resolved CredentialPool's effective
// deliveryMode paired with the bound pod's isolationProfile/spiffeBinding
// is one of the two cross-tenant-risky combinations
// direct_mode_isolation.Decide rejects in multi-tenant mode
// (deliveryMode: direct + isolationProfile: standard, or deliveryMode:
// proxy + spiffeBinding: disabled). Unlike the pool-registration and
// admission-webhook layers, which inspect the warm-pool/SandboxTemplate
// deliveryMode copy, this gate reads the CredentialPool deliveryMode
// leasing actually uses, closing the case where the two diverge.
// writePodClaimError maps it to 422 carrying the guard's rejection Code
// and Decision.Reason. spec: §4.9.
type CredentialDeliveryIsolationError struct {
	// Code is the direct_mode_isolation guard's rejection code
	// (DirectModeStandardIsolationMultiTenantRejected or
	// ProxyModeSpiffeBindingDisabledMultiTenantRejected).
	Code string
	// Reason is the guard's Decision.Reason remediation message.
	Reason string
}

func (e *CredentialDeliveryIsolationError) Error() string { return e.Reason }

// checkCredentialDeliveryIsolation runs the §4.9 session-start
// credential-delivery gate: for each resolved provider pool's effective
// deliveryMode it builds a direct_mode_isolation.Request pairing that mode
// with the bound pod's isolationProfile and spiffeBinding, keyed on the
// gateway's tenancy mode, and calls the same canonical Decide the
// registration and admission layers run. On the first rejection it returns
// a CredentialDeliveryIsolationError carrying the guard's code and reason;
// it returns nil when every resolved pool is permitted (including in
// single-tenant or development mode, where Decide allows both combinations).
//
// EgressProfile is not carried here: the NET-006 proxy/provider-direct
// mutual exclusivity is a pool-definition property the registration and
// admission layers already reject, and the bound pod's egressProfile is not
// in scope at the lease-mint seam. This gate closes the delivery-mode
// divergence §4.9 identifies between the CredentialPool deliveryMode leasing
// uses and the warm-pool/SandboxTemplate copy the earlier layers inspect.
//
// spec: §4.9.
func (s *Server) checkCredentialDeliveryIsolation(match podsession.PoolMatch, poolDeliveryModes map[string]string) error {
	for _, deliveryMode := range poolDeliveryModes {
		decision := direct_mode_isolation.Decide(direct_mode_isolation.Request{
			TenancyMode:      s.tenancyMode,
			DevMode:          s.devMode,
			Kind:             "CredentialPool",
			DeliveryMode:     deliveryMode,
			IsolationProfile: match.IsolationProfile,
			SpiffeBinding:    match.SpiffeBinding,
		})
		if !decision.Allowed {
			return &CredentialDeliveryIsolationError{Code: rejectionCode(decision.Reason), Reason: decision.Reason}
		}
	}
	return nil
}

// rejectionCode extracts the leading guard rejection code from a
// direct_mode_isolation Decision.Reason, which the guard formats as
// "<Code>: <kind> <message>". It returns the reason unchanged when no
// colon-delimited code prefix is present.
func rejectionCode(reason string) string {
	if i := strings.Index(reason, ":"); i > 0 {
		return reason[:i]
	}
	return reason
}

// poolDescriptor maps a §4.9 credential pool to the router's pool
// descriptor. A pool is assignable when it holds at least one
// non-revoked credential; cooldown is rotation-time state and is false
// at session creation. The live active-leases-versus-maxConcurrent
// refinement (spec §4.9) tightens HasCapacity once a lease-
// utilization reader is wired; until then a pool with a usable
// credential is treated as having capacity.
func poolDescriptor(p credentialpoolstore.CredentialPool) credrouter.PoolDescriptor {
	assignable := false
	for _, c := range p.Credentials {
		if !c.IsRevoked() {
			assignable = true
			break
		}
	}
	return credrouter.PoolDescriptor{
		PoolID:      p.Name,
		Healthy:     assignable,
		HasCapacity: assignable,
	}
}
