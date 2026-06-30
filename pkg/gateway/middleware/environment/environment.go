// SPDX-License-Identifier: MIT

// Package environment is the §10.6 transparent-filtering
// environment-resolver middleware. It runs on the request path after
// the §10.2 authentication middleware, resolves the caller's §10.6
// environment membership (including OIDC-group-derived membership),
// and attaches a Resolution to the request context.
//
// The §10.6 spec defines two access paths. This middleware implements
// the default "transparent filtering" path: the caller connects to a
// standard endpoint and the gateway returns the union of runtimes (and
// the internal union of connectors) authorized across every
// environment where the caller's groups or subject hold a member role,
// without the caller naming an environment. Downstream handlers — the
// §9.1 runtime-discovery surfaces and the session-creation path — read
// the Resolution from context and call FilterRuntimes / FilterConnectors
// so there is a single filtering implementation backed by
// pkg/gateway/envaccess.
//
// The middleware is nil-safe: when the environment or tenant registry
// is not wired, or the request carries no authenticated principal, it
// passes the request through with an unconfigured Resolution and
// FilterRuntimes / FilterConnectors return their input unchanged. When
// a registry is wired but a store read fails the middleware fails
// closed with 500 rather than serve an unfiltered view, because §10.6
// transparent filtering is an authorization boundary.
package environment

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/envaccess"
	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
)

// Resolution is the §10.6 environment context the middleware resolves
// for one request and attaches to its context. Downstream handlers
// retrieve it with FromContext.
type Resolution struct {
	// Configured reports whether §10.6 transparent filtering is in
	// effect for this request. It is false when the environment or
	// tenant registry is not wired or the request carries no
	// authenticated principal; in that case FilterRuntimes and
	// FilterConnectors return their input unchanged.
	Configured bool

	// TenantID is the authenticated caller's tenant. Empty when
	// Configured is false.
	TenantID string

	// Caller is the §10.6 identity whose environment membership was
	// resolved: the caller's subject and OIDC group set.
	Caller envaccess.Caller

	// Policy is the resolved §10.6 noEnvironmentPolicy that governs a
	// caller with no environment membership: the per-tenant value when
	// the tenant set one, otherwise the platform-wide default. An empty
	// Policy is treated as deny-all by the envaccess resolver.
	Policy string

	// MemberEnvironments are the §10.6 environments, in name order, the
	// caller is a member of through a subject or OIDC-group match.
	MemberEnvironments []environmentstore.Environment

	// AllEnvironments are every §10.6 environment defined for the
	// tenant, in name order. The session-creation and delegation paths
	// need the full set to evaluate the §10.6 explicit-environment
	// membership check and the bilateral cross-environment-delegation
	// declarations.
	AllEnvironments []environmentstore.Environment
}

type resolutionCtxKey struct{}

// FromContext returns the Resolution the middleware attached to ctx.
// When no Resolution is set — a request that did not pass through the
// middleware — it returns the zero value, whose Configured field is
// false, so callers treat it as "filtering not in effect".
func FromContext(ctx context.Context) Resolution {
	r, _ := ctx.Value(resolutionCtxKey{}).(Resolution)
	return r
}

// WithResolution returns a copy of ctx carrying res. Exposed for tests
// and for handlers that resolve the §10.6 context outside the HTTP
// middleware boundary.
func WithResolution(ctx context.Context, res Resolution) context.Context {
	return context.WithValue(ctx, resolutionCtxKey{}, res)
}

type explicitEnvCtxKey struct{}

// WithExplicitEnvironment marks ctx as scoped to the §10.6
// explicit-environment named name. The §10.6 explicit-environment access
// paths (`/mcp/environments/{name}`, the scoped model namespace on the
// OpenAI/Open Responses surfaces) set it so session-creation and
// discovery default to that environment without the caller repeating it
// on each call. spec: §10.6 line 557.
func WithExplicitEnvironment(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, explicitEnvCtxKey{}, name)
}

// ExplicitEnvironmentFromContext returns the §10.6 explicit-environment
// scope a dedicated environment endpoint attached to ctx, or empty when
// the request did not opt into an explicit environment.
func ExplicitEnvironmentFromContext(ctx context.Context) string {
	name, _ := ctx.Value(explicitEnvCtxKey{}).(string)
	return name
}

// FilterRuntimes narrows runtimes to the §10.6 transparent-filtering
// view: the union of runtimes the caller's environment membership
// authorizes. When the Resolution is not Configured the list is
// returned unchanged — the transparent-filter boundary is not in
// effect for this request. The supplied runtimes must already be
// tenant-scoped; the §10.6 resolver does not perform tenant filtering.
//
// Callers pass the runtime list they are about to surface (after any
// type filter and §5.1 derived-runtime resolution) so the filtering
// applies to exactly the rows the response will contain.
func (r Resolution) FilterRuntimes(runtimes []runtimestore.Runtime) []runtimestore.Runtime {
	if !r.Configured {
		return runtimes
	}
	return envaccess.AuthorizedRuntimes(r.Caller, r.AllEnvironments, runtimes, r.Policy)
}

// FilterConnectors narrows connectors to the §10.6 transparent-filtering
// view: the union of connectors the caller's environment membership
// authorizes through each environment's connectorSelector. §10.6 scopes
// connectorSelector to the internal connector surface. When the
// Resolution is not Configured the list is returned unchanged.
func (r Resolution) FilterConnectors(connectors []connectorstore.Connector) []connectorstore.Connector {
	if !r.Configured {
		return connectors
	}
	return envaccess.AuthorizedConnectors(r.Caller, r.AllEnvironments, connectors, r.Policy)
}

// Options configures the environment-resolver middleware.
type Options struct {
	// Environments is the §10.6 per-tenant environment registry. A nil
	// Environments disables transparent filtering: every request passes
	// through with an unconfigured Resolution.
	Environments environmentstore.Store

	// Tenants is the §10.2 tenant registry, consulted for the caller
	// tenant's §10.6 noEnvironmentPolicy. A nil Tenants disables
	// transparent filtering for the same reason as a nil Environments —
	// the no-environment fallback policy cannot be resolved without it.
	Tenants tenantstore.Store

	// DefaultNoEnvironmentPolicy is the §10.6 platform-wide
	// noEnvironmentPolicy applied to a caller whose tenant set no
	// per-tenant value. It is the resolvedNoEnvPolicy the gateway
	// validates at startup. An empty value is treated as deny-all by
	// the envaccess resolver, which is the §10.6 platform default.
	DefaultNoEnvironmentPolicy string
}

// Resolve builds the §10.6 environment Resolution for caller in
// tenantID from the supplied registries. It is the resolution step the
// HTTP middleware runs, factored out so a handler reached outside the
// HTTP middleware boundary (an MCP tool dispatched by a server that was
// itself mounted under the middleware in production, exercised directly
// in a unit test) can build the same Resolution from the same stores.
//
// When environments or tenants is nil, or tenantID is empty, Resolve
// returns an unconfigured Resolution: FilterRuntimes and
// FilterConnectors then return their input unchanged. A failure to list
// the tenant's environments is returned as an error so the caller can
// fail closed; §10.6 transparent filtering is an authorization boundary
// and an unfiltered fallback would leak runtimes the caller's
// environments do not authorize.
func Resolve(ctx context.Context, environments environmentstore.Store, tenants tenantstore.Store, defaultPolicy string, caller envaccess.Caller, tenantID string) (Resolution, error) {
	if environments == nil || tenants == nil || tenantID == "" {
		return Resolution{}, nil
	}
	envs, err := environments.List(ctx, tenantID)
	if err != nil {
		return Resolution{}, err
	}
	policy := defaultPolicy
	if tenant, terr := tenants.Get(ctx, tenantID); terr == nil && tenant.NoEnvironmentPolicy != "" {
		policy = tenant.NoEnvironmentPolicy
	}
	return Resolution{
		Configured:         true,
		TenantID:           tenantID,
		Caller:             caller,
		Policy:             policy,
		MemberEnvironments: envaccess.MemberEnvironments(caller, envs),
		AllEnvironments:    envs,
	}, nil
}

// Wrap returns the §10.6 environment-resolver middleware around inner.
// Place it immediately inside the §10.2 auth middleware so the caller's
// identity and OIDC groups are already resolved on the request context.
func Wrap(inner http.Handler, opts Options) http.Handler {
	return &middleware{inner: inner, opts: opts}
}

type middleware struct {
	inner http.Handler
	opts  Options
}

func (m *middleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// No-op when the §10.6 registries are not wired — a minimal
	// deployment with no environment registry passes through with an
	// unconfigured Resolution.
	if m.opts.Environments == nil || m.opts.Tenants == nil {
		m.inner.ServeHTTP(w, r)
		return
	}
	// No-op for an unauthenticated request: with no principal there is
	// no identity to resolve environment membership against. Such a
	// request reaches only the routes that do not require auth; the
	// authenticated routes are already rejected by the auth middleware.
	principal, ok := authmw.FromContext(r.Context())
	if !ok || principal.TenantID == "" {
		m.inner.ServeHTTP(w, r)
		return
	}

	caller := envaccess.Caller{Subject: principal.Subject, Groups: principal.Groups}
	res, err := Resolve(r.Context(), m.opts.Environments, m.opts.Tenants,
		m.opts.DefaultNoEnvironmentPolicy, caller, principal.TenantID)
	if err != nil {
		// §10.6 transparent filtering is an authorization boundary. A
		// store read failure must fail closed: serving the request with
		// an unfiltered view would leak runtimes the caller's
		// environments do not authorize.
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"environment registry lookup failed: "+err.Error())
		return
	}
	m.inner.ServeHTTP(w, r.WithContext(WithResolution(r.Context(), res)))
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": code, "message": message},
	})
}
