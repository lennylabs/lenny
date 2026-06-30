// SPDX-License-Identifier: MIT

package connectorauthz_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/connectorauthz"
	"github.com/lennylabs/lenny/pkg/gateway/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// fakeResolver stands in for *delegation.Service: it maps a session id to
// its runtime-level effective policy and a name to a resolved active
// policy (the §10.6 environment-default layer).
type fakeResolver struct {
	effective map[string]delegationpolicystore.DelegationPolicy
	named     map[string]delegationpolicystore.DelegationPolicy
	err       error
}

func (f *fakeResolver) EffectiveDelegationPolicy(_ context.Context, _, sessionID string) (delegationpolicystore.DelegationPolicy, bool, error) {
	if f.err != nil {
		return delegationpolicystore.DelegationPolicy{}, false, f.err
	}
	p, ok := f.effective[sessionID]
	return p, ok, nil
}

func (f *fakeResolver) ResolveActivePolicy(_ context.Context, _, name string) (delegationpolicystore.DelegationPolicy, bool, error) {
	if f.err != nil {
		return delegationpolicystore.DelegationPolicy{}, false, f.err
	}
	p, ok := f.named[name]
	return p, ok, nil
}

// allowConnectors returns a policy that permits exactly the named
// connector ids and denies everything else (allow-list default-deny).
func allowConnectors(ids ...string) delegationpolicystore.DelegationPolicy {
	return delegationpolicystore.DelegationPolicy{
		Rules: []delegationpolicystore.Rule{{
			Target: delegationpolicystore.Target{Types: []string{"connector"}, IDs: ids},
			Allow:  true,
		}},
	}
}

func ctx() context.Context { return context.Background() }

// TestAuthorizeConnectorRuntimePolicyDenies_spec_9_3_164 confirms a
// connector outside the session's runtime-level effective policy is
// denied — the §9.3 line 164 boundary.
func TestAuthorizeConnectorRuntimePolicyDenies_spec_9_3_164(t *testing.T) {
	res := &fakeResolver{effective: map[string]delegationpolicystore.DelegationPolicy{
		"sess-1": allowConnectors("github"),
	}}
	a := connectorauthz.New(res, nil, nil)
	if err := a.AuthorizeConnector(ctx(), "acme", "sess-1", "gitlab", nil); !errors.Is(err, connectorauthz.ErrConnectorNotPermitted) {
		t.Fatalf("AuthorizeConnector(gitlab) = %v, want ErrConnectorNotPermitted", err)
	}
	if err := a.AuthorizeConnector(ctx(), "acme", "sess-1", "github", nil); err != nil {
		t.Fatalf("AuthorizeConnector(github) = %v, want nil", err)
	}
}

// TestAuthorizeConnectorNoPolicyPermits_spec_9_3_164 confirms the
// conservative fall-through: when no policy layer resolves, the connector
// is permitted (discovery and delegation take the same fall-through).
func TestAuthorizeConnectorNoPolicyPermits_spec_9_3_164(t *testing.T) {
	a := connectorauthz.New(&fakeResolver{}, nil, nil)
	if err := a.AuthorizeConnector(ctx(), "acme", "sess-x", "anything", nil); err != nil {
		t.Fatalf("no-policy AuthorizeConnector = %v, want nil", err)
	}
}

// TestAuthorizeConnectorNilResolverPermits_spec_9_3_164 confirms a nil
// policy registry imposes no restriction.
func TestAuthorizeConnectorNilResolverPermits_spec_9_3_164(t *testing.T) {
	a := connectorauthz.New(nil, nil, nil)
	if err := a.AuthorizeConnector(ctx(), "acme", "sess-1", "github", nil); err != nil {
		t.Fatalf("nil-resolver AuthorizeConnector = %v, want nil", err)
	}
}

// TestAuthorizeConnectorEmptySessionPermits_spec_9_3_164 confirms an
// empty session id skips the gate (no calling pod to scope against).
func TestAuthorizeConnectorEmptySessionPermits_spec_9_3_164(t *testing.T) {
	res := &fakeResolver{effective: map[string]delegationpolicystore.DelegationPolicy{
		"sess-1": allowConnectors("github"),
	}}
	a := connectorauthz.New(res, nil, nil)
	if err := a.AuthorizeConnector(ctx(), "acme", "", "gitlab", nil); err != nil {
		t.Fatalf("empty-session AuthorizeConnector = %v, want nil", err)
	}
}

// TestAuthorizeConnectorEnvironmentDefaultIntersects_spec_10_6_629 confirms
// the §10.6 environment-default layer intersects with the runtime-level
// policy: a connector the runtime layer allows is still denied when the
// environment-default policy denies it.
func TestAuthorizeConnectorEnvironmentDefaultIntersects_spec_10_6_629(t *testing.T) {
	sessions := memstore.New()
	seedSession(t, sessions, "acme", "sess-1", "prod")
	envs := environmentstore.NewMemory()
	seedEnvironment(t, envs, "acme", "prod", "env-default-policy")

	res := &fakeResolver{
		effective: map[string]delegationpolicystore.DelegationPolicy{
			"sess-1": allowConnectors("github", "gitlab"),
		},
		named: map[string]delegationpolicystore.DelegationPolicy{
			"env-default-policy": allowConnectors("github"), // env default omits gitlab
		},
	}
	a := connectorauthz.New(res, sessions, envs)

	// github is in both layers → permitted.
	if err := a.AuthorizeConnector(ctx(), "acme", "sess-1", "github", nil); err != nil {
		t.Fatalf("github = %v, want nil", err)
	}
	// gitlab is in the runtime layer but not the env default → denied.
	if err := a.AuthorizeConnector(ctx(), "acme", "sess-1", "gitlab", nil); !errors.Is(err, connectorauthz.ErrConnectorNotPermitted) {
		t.Fatalf("gitlab = %v, want ErrConnectorNotPermitted (env-default intersection)", err)
	}
}

// TestAuthorizeConnectorResolverErrorPropagates_spec_9_3_164 confirms a
// resolver error surfaces rather than silently permitting.
func TestAuthorizeConnectorResolverErrorPropagates_spec_9_3_164(t *testing.T) {
	boom := errors.New("policy store down")
	a := connectorauthz.New(&fakeResolver{err: boom}, nil, nil)
	if err := a.AuthorizeConnector(ctx(), "acme", "sess-1", "github", nil); !errors.Is(err, boom) {
		t.Fatalf("AuthorizeConnector err = %v, want %v", err, boom)
	}
}

func seedSession(t *testing.T, s sessionstore.Store, tenant, id, env string) {
	t.Helper()
	if err := s.Create(context.Background(), sessionstore.Session{
		TenantID: tenant, ID: id, Environment: env, CreatedAt: time.Unix(0, 0).UTC(),
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
}

func seedEnvironment(t *testing.T, s *environmentstore.Memory, tenant, name, defaultPolicy string) {
	t.Helper()
	if err := s.Create(context.Background(), environmentstore.Environment{
		TenantID: tenant, Name: name, DefaultDelegationPolicy: defaultPolicy,
	}); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
}
