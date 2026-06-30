// SPDX-License-Identifier: MIT

// Package connectorauthz enforces the §9.3 connector-access boundary:
// the gateway validates the connector_id in every external tool call
// against the calling pod's effective §8.3 delegation policy before
// proxying. A child session cannot use a connector its policy does not
// permit, even when a gateway-held credential exists for that connector
// at the root level.
//
// spec: §9.3 line 164.
package connectorauthz

import (
	"context"
	"errors"

	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// CandidateType is the §8.3 candidate type a connector is evaluated
// under. It matches the `Types` filter a DelegationPolicy target uses to
// scope a rule to connectors.
const CandidateType = "connector"

// ErrConnectorNotPermitted reports that the calling session's effective
// delegation policy denies the connector. spec: §9.3 line 164.
var ErrConnectorNotPermitted = errors.New("connectorauthz: connector not permitted by effective delegation policy")

// PolicyResolver resolves the §8.3 delegation policies that govern a
// session. *delegation.Service satisfies it: EffectiveDelegationPolicy
// resolves the runtime-level policy, ResolveActivePolicy resolves a
// named active policy (used for the §10.6 environment default).
type PolicyResolver interface {
	// EffectiveDelegationPolicy resolves the runtime-level §8.3 policy
	// named by the session's resolved runtime. ok is false when the
	// session names no policy, the runtime or policy is missing or
	// soft-deleted, or no registry is wired.
	EffectiveDelegationPolicy(ctx context.Context, tenantID, sessionID string) (delegationpolicystore.DelegationPolicy, bool, error)
	// ResolveActivePolicy resolves a named active §8.3 policy. ok is
	// false when the name is empty or the policy is missing or
	// soft-deleted.
	ResolveActivePolicy(ctx context.Context, tenantID, name string) (delegationpolicystore.DelegationPolicy, bool, error)
}

// Authorizer composes the two policy layers that scope a session's
// connector access: the runtime-level §8.3 effective policy and the
// §10.6 environment-default policy. A connector is permitted only when
// every resolved layer permits it — the §8.3 least-privilege discipline
// ("restriction only, never expansion") makes the intersection the safe
// composition. This mirrors the agent-discovery filter
// (mcptools.filterByEffectiveDelegationPolicy) so discovery and
// connector invocation agree on which policy governs a session.
type Authorizer struct {
	policies     PolicyResolver
	sessions     sessionstore.Store
	environments environmentstore.Store
}

// New wires an Authorizer. sessions and environments back the §10.6
// environment-default layer; with either absent only the runtime-level
// layer applies. policies may be nil, in which case AuthorizeConnector
// imposes no restriction — the same conservative fall-through the
// discovery path takes when no policy registry is wired.
func New(policies PolicyResolver, sessions sessionstore.Store, environments environmentstore.Store) *Authorizer {
	return &Authorizer{policies: policies, sessions: sessions, environments: environments}
}

// AuthorizeConnector returns nil when connectorID is permitted by every
// resolved policy layer and ErrConnectorNotPermitted when a layer denies
// it. When no policy layer resolves — no registry wired, an empty
// sessionID, an unresolved session, or neither a runtime-level nor an
// environment-default reference — the connector is permitted, matching
// the conservative fall-through delegation and discovery apply to an
// unresolved policy reference.
//
// spec: §9.3 line 164; §8.3 line 244; §10.6 line 601, line 629.
func (a *Authorizer) AuthorizeConnector(ctx context.Context, tenantID, sessionID, connectorID string, labels map[string]string) error {
	if a == nil || a.policies == nil || sessionID == "" {
		return nil
	}
	policies, err := a.governing(ctx, tenantID, sessionID)
	if err != nil {
		return err
	}
	if len(policies) == 0 {
		return nil
	}
	cand := delegationpolicystore.Candidate{ID: connectorID, Type: CandidateType, Labels: labels}
	for _, p := range policies {
		if !p.Evaluate(cand) {
			return ErrConnectorNotPermitted
		}
	}
	return nil
}

// governing resolves the runtime-level and environment-default policies
// that scope sessionID. A policy layer that does not resolve is omitted
// rather than treated as a deny-all.
func (a *Authorizer) governing(ctx context.Context, tenantID, sessionID string) ([]delegationpolicystore.DelegationPolicy, error) {
	var policies []delegationpolicystore.DelegationPolicy
	if pol, ok, err := a.policies.EffectiveDelegationPolicy(ctx, tenantID, sessionID); err != nil {
		return nil, err
	} else if ok {
		policies = append(policies, pol)
	}
	if pol, ok, err := a.environmentDefault(ctx, tenantID, sessionID); err != nil {
		return nil, err
	} else if ok {
		policies = append(policies, pol)
	}
	return policies, nil
}

// environmentDefault resolves the §10.6 defaultDelegationPolicy of the
// environment the session was created in. Every unresolved case returns
// (zero, false, nil), leaving the environment layer imposing no
// restriction: no session/environment registry wired, a session that
// names no environment, a missing session or environment, or an
// environment with no default policy.
func (a *Authorizer) environmentDefault(ctx context.Context, tenantID, sessionID string) (delegationpolicystore.DelegationPolicy, bool, error) {
	if a.sessions == nil || a.environments == nil {
		return delegationpolicystore.DelegationPolicy{}, false, nil
	}
	sess, err := a.sessions.Get(ctx, tenantID, sessionID)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			return delegationpolicystore.DelegationPolicy{}, false, nil
		}
		return delegationpolicystore.DelegationPolicy{}, false, err
	}
	if sess.Environment == "" {
		return delegationpolicystore.DelegationPolicy{}, false, nil
	}
	env, err := a.environments.Get(ctx, tenantID, sess.Environment)
	if err != nil {
		if errors.Is(err, environmentstore.ErrNotFound) {
			return delegationpolicystore.DelegationPolicy{}, false, nil
		}
		return delegationpolicystore.DelegationPolicy{}, false, err
	}
	return a.policies.ResolveActivePolicy(ctx, tenantID, env.DefaultDelegationPolicy)
}
