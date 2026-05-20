// SPDX-License-Identifier: MIT

// Package admin implements the §15.1 admin API surface
// (`/v1/admin/*`). Each resource lives in its own handler so the
// surface can be wired piecemeal; the package exports a Router that
// composes the active handlers behind the §10.2 RBAC authorization
// gates.
//
// Each admin route declares the §10.2 gate matching its permission-
// matrix row. Genuinely platform-scoped routes (tenant CRUD, runtime /
// pool creation and deletion, connectors, circuit breakers,
// billing corrections, platform settings) use requireAdmin, which
// admits the `platform-admin` role only. Routes the §10.2 matrix
// grants to `tenant-admin` for their own tenant (runtime / pool /
// credential-pool / delegation-policy / environment management, tenant
// RBAC config, user management) use the permission-aware gates
// (requirePermission, requireResourceManage, requireUserAdmin), which
// admit `platform-admin`, `tenant-admin`, and any tenant custom role
// whose permission set includes the required §10.2 permission. A caller
// lacking the required role or permission receives 403 FORBIDDEN before
// the resource-specific handler runs.
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/breakerstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/correctionstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/customrolestore"
	"github.com/lennylabs/lenny/pkg/gateway/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/erasurejob"
	"github.com/lennylabs/lenny/pkg/gateway/evalstore"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/opsevents"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/recommendations"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantaccessstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// rfc3339Nano serialises a time.Time using the shared RFC3339Nano
// format every admin payload uses. Zero times serialise to empty so
// optional `deletedAt` is omitted from the wire when absent.
func rfc3339Nano(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// AuditSink receives §11.7 admin audit events. The router emits one
// event per successful mutation (create / update / soft-delete);
// reads do not emit. Implementations must be non-blocking — the
// admin handler does not wait for delivery.
type AuditSink interface {
	EmitAdminEvent(ctx context.Context, event AuditEvent)
}

// AuditEvent is the §11.7 admin-event payload. Fields match the
// canonical OCSF mapping used by the §11.7 hash-chain audit
// pipeline.
type AuditEvent struct {
	// Type is the §11.7 event type (e.g., `admin.tenant.created`).
	Type string

	// ActorSubject is the JWT `sub` of the calling user.
	ActorSubject string

	// ActorTenantID is the JWT `tenant_id` of the calling user
	// (usually `platform` for platform-admin calls).
	ActorTenantID string

	// TargetResource is the resource the operation affects (e.g.,
	// the tenant id).
	TargetResource string

	// Detail carries event-specific fields the auditor records
	// verbatim in the hash-chain entry.
	Detail map[string]any

	// At is the gateway clock instant the audit event fired.
	At time.Time
}

// Router is the §15.1 admin sub-router. The minimal admin API wires
// only the resources the gateway has stores for; future commits add
// users, pools, connectors, circuit breakers, etc.
type Router struct {
	tenants            tenantstore.Store
	runtimes           runtimestore.Store
	users              userstore.Store
	pools              poolstore.Store
	breakers           breakerstore.Store
	connectors         connectorstore.Store
	connectorOAuth     *ConnectorOAuth
	delegationPolicies delegationpolicystore.Store
	credentialPools    credentialpoolstore.Store
	customRoles        customrolestore.Store
	tenantAccess       tenantaccessstore.Store
	auditLog           AuditLog
	tokenRevoker       IssuedTokenRevoker
	revocationCache    RevocationCache
	userPods           UserPodTerminator
	userLeases         UserLeaseRevoker
	userTokens         UserTokenRevoker
	erasureRunner      ErasureRunner
	erasureJobs        erasurejob.Store
	billing            billingstore.Store
	corrections        correctionstore.Store
	dualControlThresh  float64
	sessions           sessionstore.Store
	interactions       interactionstore.Store
	experiments        experimentstore.Store
	environments       environmentstore.Store
	evals              evalstore.Store
	clock              func() time.Time
	audit              AuditSink
	metrics            RBACConfigMetrics

	platformInfo   PlatformInfo
	platformConfig map[string]string
	platformWired  bool

	recommendations RecommendationService
	eventBuffer     EventBufferQuerier
	eventEmitter    *opsevents.Emitter

	kmsProbe KMSProbe
}

// KMSProbe is the §12.5 T4 per-tenant KMS availability probe seam.
// `PUT /v1/admin/tenants/{id}` calls ProbeAvailability before
// persisting a `workspaceTier: T4` transition; on failure the handler
// rejects the update with `CLASSIFICATION_CONTROL_VIOLATION`
// (`details.reason: kms_probe_failed`). `GET /v1/admin/tenants/{id}`
// reads LastProbeSuccess to surface `t4KmsLastProbeSuccessAt` on the
// response for T4 tenants. `*tenantkms.Lifecycle` satisfies the
// interface.
type KMSProbe interface {
	// ProbeAvailability runs the per-tenant probe. Non-T4 tiers
	// return nil without contacting the KMS.
	ProbeAvailability(ctx context.Context, tenantID, workspaceTier string) error
	// LastProbeSuccess returns the time of the most recent
	// successful probe for tenantID. The boolean is false when no
	// success has been recorded.
	LastProbeSuccess(tenantID string) (time.Time, bool)
}

// RecommendationService is the §25.3 capacity-recommendation read
// surface the admin Router exposes at GET /v1/admin/recommendations.
type RecommendationService interface {
	GetRecommendations(ctx context.Context, category *string) (*recommendations.RecommendationsResponse, error)
}

// EventBufferQuerier is the §25.3 event-buffer read surface the admin
// Router exposes at GET /v1/admin/events/buffer.
type EventBufferQuerier interface {
	Query(since uint64, filter opsevents.EventFilter, limit int) opsevents.BufferedEventPage
}

// RBACConfigMetrics records the §10.6 observability counters the RBAC
// config endpoints emit. *gatewaymetrics.Metrics satisfies it.
type RBACConfigMetrics interface {
	// RecordNoEnvironmentPolicyAllowAll counts a tenant rbac-config
	// write that set noEnvironmentPolicy to allow-all.
	RecordNoEnvironmentPolicyAllowAll(tenantID string)
}

// Options configures the Router.
type Options struct {
	// Clock overrides time.Now. Pass nil for production.
	Clock func() time.Time

	// Audit, when set, receives one event per successful admin
	// mutation per §11.7. Nil disables emission (the operation still
	// succeeds).
	Audit AuditSink

	// Metrics, when set, receives the §10.6 RBAC-config observability
	// counters. Nil disables them (the operation still succeeds).
	Metrics RBACConfigMetrics
}

// NewRouter returns a Router. Pass nil for opts to use the defaults.
func NewRouter(tenants tenantstore.Store, opts Options) *Router {
	clock := opts.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Router{tenants: tenants, clock: clock, audit: opts.Audit, metrics: opts.Metrics}
}

// WithKMSProbe wires the §12.5 T4 KMS availability probe onto the
// Router. With it set, `PUT /v1/admin/tenants/{id}` runs the probe
// before persisting a workspaceTier T4 transition (rejecting with
// CLASSIFICATION_CONTROL_VIOLATION on failure), and
// `GET /v1/admin/tenants/{id}` surfaces `t4KmsLastProbeSuccessAt`
// for T4 tenants. Without it the §15.1 admin handlers behave as
// before — admin-time probing is documented as best-effort when the
// gateway has no KMS lifecycle wired.
func (r *Router) WithKMSProbe(p KMSProbe) *Router {
	r.kmsProbe = p
	return r
}

// emit fires an audit event when an AuditSink is wired. Never
// blocks the caller — sinks must do their own async delivery.
func (r *Router) emit(ctx context.Context, p authmw.Principal, eventType, resource string, detail map[string]any) {
	if r.audit == nil {
		return
	}
	r.audit.EmitAdminEvent(ctx, AuditEvent{
		Type:           eventType,
		ActorSubject:   p.Subject,
		ActorTenantID:  p.TenantID,
		TargetResource: resource,
		Detail:         detail,
		At:             r.clock(),
	})
}

// Handler returns an http.Handler routing the wired admin endpoints.
func (r *Router) Handler() http.Handler {
	mux := http.NewServeMux()
	if r.tenants != nil {
		mux.Handle("POST /v1/admin/tenants", r.requireAdmin(http.HandlerFunc(r.handleCreateTenant)))
		mux.Handle("GET /v1/admin/tenants", r.requireAdmin(http.HandlerFunc(r.handleListTenants)))
		mux.Handle("GET /v1/admin/tenants/{id}", r.requireAdmin(http.HandlerFunc(r.handleGetTenant)))
		mux.Handle("PUT /v1/admin/tenants/{id}", r.requireAdmin(http.HandlerFunc(r.handleUpdateTenant)))
		mux.Handle("DELETE /v1/admin/tenants/{id}", r.requireAdmin(http.HandlerFunc(r.handleDeleteTenant)))
		mux.Handle("GET /v1/admin/tenants/{id}/elicitation-content-integrity",
			r.requireAdmin(http.HandlerFunc(r.handleGetElicitationIntegrity)))
		mux.Handle("PUT /v1/admin/tenants/{id}/elicitation-content-integrity",
			r.requireAdmin(http.HandlerFunc(r.handlePutElicitationIntegrity)))
		mux.Handle("POST /v1/admin/tenants/{id}/compliance-profile/decommission",
			r.requireAdmin(http.HandlerFunc(r.handleDecommissionCompliance)))
		// §10.6 / §15.1 tenant RBAC config, gated on manage_rbac_config:
		// platform-admin or tenant-admin, scoped to the path tenant.
		rbacConfigAdmin := r.requirePermission(auth.PermManageRBACConfig)
		mux.Handle("GET /v1/admin/tenants/{id}/rbac-config",
			rbacConfigAdmin(http.HandlerFunc(r.handleGetRBACConfig)))
		mux.Handle("PUT /v1/admin/tenants/{id}/rbac-config",
			rbacConfigAdmin(http.HandlerFunc(r.handlePutRBACConfig)))
	}
	if r.runtimes != nil {
		// §10.2 / §15.1: runtime create and delete create or remove a
		// platform-global record and are reserved to platform-admin. The
		// §10.2 matrix grants `tenant-admin` "Manage runtimes (own
		// tenant)", which §15.1 scopes to updating an already-granted
		// record — so the PUT runs on the manage_runtimes permission gate
		// with §4 access-table scoping; create/delete keep requireAdmin.
		runtimeManage := r.requireResourceManage(auth.PermManageRuntimes, tenantaccessstore.KindRuntime)
		mux.Handle("POST /v1/admin/runtimes", r.requireAdmin(http.HandlerFunc(r.handleCreateRuntime)))
		mux.Handle("POST /v1/admin/runtimes/regenerate-cards",
			r.requireAdmin(http.HandlerFunc(r.handleRegenerateCards)))
		// §4: runtime reads are tenant-scoped — a tenant-admin sees the
		// runtimes granted to their tenant; the handlers filter.
		mux.Handle("GET /v1/admin/runtimes", r.requireTenantResourceAdmin(http.HandlerFunc(r.handleListRuntimes)))
		mux.Handle("GET /v1/admin/runtimes/{name}", r.requireTenantResourceAdmin(http.HandlerFunc(r.handleGetRuntime)))
		mux.Handle("PUT /v1/admin/runtimes/{name}", runtimeManage(http.HandlerFunc(r.handleUpdateRuntime)))
		mux.Handle("DELETE /v1/admin/runtimes/{name}", r.requireAdmin(http.HandlerFunc(r.handleDeleteRuntime)))
	}
	if r.recommendations != nil {
		mux.Handle("GET /v1/admin/recommendations",
			r.requireAdmin(http.HandlerFunc(r.handleRecommendations)))
	}
	if r.eventBuffer != nil {
		mux.Handle("GET /v1/admin/events/buffer",
			r.requireAdmin(http.HandlerFunc(r.handleEventBuffer)))
	}
	if r.users != nil {
		mux.Handle("POST /v1/admin/users", r.requireUserAdmin(http.HandlerFunc(r.handleCreateUser)))
		mux.Handle("GET /v1/admin/users", r.requireUserAdmin(http.HandlerFunc(r.handleListUsers)))
		mux.Handle("GET /v1/admin/users/{user_id}", r.requireUserAdmin(http.HandlerFunc(r.handleGetUser)))
		mux.Handle("PUT /v1/admin/users/{user_id}", r.requireUserAdmin(http.HandlerFunc(r.handleUpdateUser)))
		mux.Handle("POST /v1/admin/users/{user_id}/invalidate", r.requireUserAdmin(http.HandlerFunc(r.handleInvalidateUser)))
		mux.Handle("DELETE /v1/admin/users/{user_id}", r.requireUserAdmin(http.HandlerFunc(r.handleDeleteUser)))
		if r.erasureRunner != nil {
			// §12.8 GDPR user erasure.
			mux.Handle("POST /v1/admin/users/{user_id}/erase",
				r.requireUserAdmin(http.HandlerFunc(r.handleEraseUser)))
		}
	}
	if r.erasureJobs != nil {
		// §12.8 erasure-job status query.
		mux.Handle("GET /v1/admin/erasure-jobs/{job_id}",
			r.requireUserAdmin(http.HandlerFunc(r.handleGetErasureJob)))
	}
	if r.sessions != nil {
		// §12.8 legal hold set / clear.
		mux.Handle("POST /v1/admin/legal-hold",
			r.requireAdmin(http.HandlerFunc(r.handleSetLegalHold)))
	}
	if r.corrections != nil && r.billing != nil {
		// §11.2.1 operator-initiated billing corrections (Category 2).
		// Gated on the issue_billing_corrections permission, which the
		// §10.2 matrix grants to platform-admin only — tenant-admin and
		// user receive 403. The approve/reject endpoints carry the same
		// gate; the four-eyes rule (a second, distinct platform-admin)
		// is enforced inside the handler.
		billingCorrectionAdmin := r.requirePermission(auth.PermIssueBillingCorrections)
		mux.Handle("POST /v1/admin/billing-corrections",
			billingCorrectionAdmin(http.HandlerFunc(r.handleCreateBillingCorrection)))
		mux.Handle("GET /v1/admin/billing-corrections",
			billingCorrectionAdmin(http.HandlerFunc(r.handleListBillingCorrections)))
		mux.Handle("GET /v1/admin/billing-corrections/{id}",
			billingCorrectionAdmin(http.HandlerFunc(r.handleGetBillingCorrection)))
		mux.Handle("POST /v1/admin/billing-corrections/{id}/approve",
			billingCorrectionAdmin(http.HandlerFunc(r.handleApproveBillingCorrection)))
		mux.Handle("POST /v1/admin/billing-corrections/{id}/reject",
			billingCorrectionAdmin(http.HandlerFunc(r.handleRejectBillingCorrection)))
	}
	if r.experiments != nil {
		// §10.7 / §15.1 experiment admin CRUD.
		mux.Handle("POST /v1/admin/experiments",
			r.requireTenantResourceAdmin(http.HandlerFunc(r.handleCreateExperiment)))
		mux.Handle("GET /v1/admin/experiments",
			r.requireTenantResourceAdmin(http.HandlerFunc(r.handleListExperiments)))
		mux.Handle("GET /v1/admin/experiments/{name}",
			r.requireTenantResourceAdmin(http.HandlerFunc(r.handleGetExperiment)))
		mux.Handle("PUT /v1/admin/experiments/{name}",
			r.requireTenantResourceAdmin(http.HandlerFunc(r.handleUpdateExperiment)))
		mux.Handle("PATCH /v1/admin/experiments/{name}",
			r.requireTenantResourceAdmin(http.HandlerFunc(r.handlePatchExperiment)))
		mux.Handle("DELETE /v1/admin/experiments/{name}",
			r.requireTenantResourceAdmin(http.HandlerFunc(r.handleDeleteExperiment)))
		if r.evals != nil {
			// §10.7 / §15.1 experiment results aggregation.
			mux.Handle("GET /v1/admin/experiments/{name}/results",
				r.requireTenantResourceAdmin(http.HandlerFunc(r.handleExperimentResults)))
		}
	}
	if r.environments != nil {
		// §10.6 / §15.1 environment admin CRUD, gated on the §10.2
		// manage_environments permission.
		envAdmin := r.requirePermission(auth.PermManageEnvironments)
		mux.Handle("POST /v1/admin/environments",
			envAdmin(http.HandlerFunc(r.handleCreateEnvironment)))
		mux.Handle("GET /v1/admin/environments",
			envAdmin(http.HandlerFunc(r.handleListEnvironments)))
		mux.Handle("GET /v1/admin/environments/{name}",
			envAdmin(http.HandlerFunc(r.handleGetEnvironment)))
		mux.Handle("PUT /v1/admin/environments/{name}",
			envAdmin(http.HandlerFunc(r.handleUpdateEnvironment)))
		mux.Handle("DELETE /v1/admin/environments/{name}",
			envAdmin(http.HandlerFunc(r.handleDeleteEnvironment)))
		mux.Handle("GET /v1/admin/environments/{name}/runtime-exposure",
			envAdmin(http.HandlerFunc(r.handleEnvironmentRuntimeExposure)))
		mux.Handle("GET /v1/admin/tenants/{id}/access-report",
			envAdmin(http.HandlerFunc(r.handleTenantAccessReport)))
	}
	if r.pools != nil {
		// §10.2 / §15.1: pool create and delete are platform-admin-only
		// (they define or remove a platform-global record). The §10.2
		// matrix grants `tenant-admin` "Manage pools / scaling policies
		// (own tenant)", which §15.1 scopes to updating an already-granted
		// record — so the PUT runs on the manage_pools permission gate
		// with §4 access-table scoping; create/delete keep requireAdmin.
		poolManage := r.requireResourceManage(auth.PermManagePools, tenantaccessstore.KindPool)
		mux.Handle("POST /v1/admin/pools", r.requireAdmin(http.HandlerFunc(r.handleCreatePool)))
		// §4 / §15.1: pool reads are tenant-scoped — a tenant-admin sees
		// the pools granted to their tenant; the handlers filter.
		mux.Handle("GET /v1/admin/pools", r.requireTenantResourceAdmin(http.HandlerFunc(r.handleListPools)))
		mux.Handle("GET /v1/admin/pools/{name}", r.requireTenantResourceAdmin(http.HandlerFunc(r.handleGetPool)))
		mux.Handle("PUT /v1/admin/pools/{name}", poolManage(http.HandlerFunc(r.handleUpdatePool)))
		mux.Handle("DELETE /v1/admin/pools/{name}", r.requireAdmin(http.HandlerFunc(r.handleDeletePool)))
	}
	if r.connectors != nil {
		mux.Handle("POST /v1/admin/connectors", r.requireAdmin(http.HandlerFunc(r.handleCreateConnector)))
		mux.Handle("GET /v1/admin/connectors", r.requireAdmin(http.HandlerFunc(r.handleListConnectors)))
		mux.Handle("GET /v1/admin/connectors/{id}", r.requireAdmin(http.HandlerFunc(r.handleGetConnector)))
		mux.Handle("PUT /v1/admin/connectors/{id}", r.requireAdmin(http.HandlerFunc(r.handleUpdateConnector)))
		mux.Handle("DELETE /v1/admin/connectors/{id}", r.requireAdmin(http.HandlerFunc(r.handleDeleteConnector)))
		if r.connectorOAuth != nil {
			// §9.3 connector OAuth 2.1 authorization-code flow. The
			// initiation endpoint requires an authenticated caller — the
			// resulting credential is scoped to the caller's identity —
			// so it is gated on authentication inside the handler rather
			// than on platform-admin. The callback endpoint is a browser
			// redirect from the OAuth provider carrying no Bearer token;
			// it is intentionally not gated, and the signed `state`
			// parameter is its anti-CSRF control. The literal
			// `oauth/callback` segment takes precedence over the
			// `{id}` wildcard in Go's ServeMux.
			mux.Handle("GET /v1/admin/connectors/oauth/callback",
				http.HandlerFunc(r.handleConnectorOAuthCallback))
			mux.Handle("POST /v1/admin/connectors/{id}/oauth/authorize",
				http.HandlerFunc(r.handleAuthorizeConnector))
		}
	}
	if r.delegationPolicies != nil {
		// §10.2: the matrix grants `tenant-admin` "Manage delegation
		// policies (own tenant)", so the CRUD runs on the
		// manage_delegation_policies permission gate (also honoring a
		// custom role that holds it). A DelegationPolicy is a
		// platform-global record with no tenant_id and no tenant-access
		// join table — unlike runtimes and pools, §10.2 defines no
		// per-resource scoping mechanism — so the gate applies no
		// per-resource scoping (scopeKind is empty).
		delegationManage := r.requireResourceManage(auth.PermManageDelegationPolicies, "")
		mux.Handle("POST /v1/admin/delegation-policies", delegationManage(http.HandlerFunc(r.handleCreateDelegationPolicy)))
		mux.Handle("GET /v1/admin/delegation-policies", delegationManage(http.HandlerFunc(r.handleListDelegationPolicies)))
		mux.Handle("GET /v1/admin/delegation-policies/{name}", delegationManage(http.HandlerFunc(r.handleGetDelegationPolicy)))
		mux.Handle("PUT /v1/admin/delegation-policies/{name}", delegationManage(http.HandlerFunc(r.handleUpdateDelegationPolicy)))
		mux.Handle("DELETE /v1/admin/delegation-policies/{name}", delegationManage(http.HandlerFunc(r.handleDeleteDelegationPolicy)))
	}
	if r.customRoles != nil {
		// §10.2: custom roles are stored in the tenant RBAC config, so
		// the routes are gated on the manage_rbac_config permission.
		rbacAdmin := r.requirePermission(auth.PermManageRBACConfig)
		mux.Handle("POST /v1/admin/tenants/{id}/roles", rbacAdmin(http.HandlerFunc(r.handleCreateCustomRole)))
		mux.Handle("GET /v1/admin/tenants/{id}/roles", rbacAdmin(http.HandlerFunc(r.handleListCustomRoles)))
		mux.Handle("GET /v1/admin/tenants/{id}/roles/{name}", rbacAdmin(http.HandlerFunc(r.handleGetCustomRole)))
		mux.Handle("PUT /v1/admin/tenants/{id}/roles/{name}", rbacAdmin(http.HandlerFunc(r.handleUpdateCustomRole)))
		mux.Handle("DELETE /v1/admin/tenants/{id}/roles/{name}", rbacAdmin(http.HandlerFunc(r.handleDeleteCustomRole)))
	}
	if r.credentialPools != nil {
		// §15.1 credential-pool admin CRUD, gated on the §10.2
		// manage_credential_pools permission.
		credPoolAdmin := r.requirePermission(auth.PermManageCredentialPools)
		mux.Handle("POST /v1/admin/credential-pools", credPoolAdmin(http.HandlerFunc(r.handleCreateCredentialPool)))
		mux.Handle("GET /v1/admin/credential-pools", credPoolAdmin(http.HandlerFunc(r.handleListCredentialPools)))
		mux.Handle("GET /v1/admin/credential-pools/{name}", credPoolAdmin(http.HandlerFunc(r.handleGetCredentialPool)))
		mux.Handle("PUT /v1/admin/credential-pools/{name}", credPoolAdmin(http.HandlerFunc(r.handleUpdateCredentialPool)))
		mux.Handle("DELETE /v1/admin/credential-pools/{name}", credPoolAdmin(http.HandlerFunc(r.handleDeleteCredentialPool)))
	}
	if r.tenantAccess != nil {
		mux.Handle("POST /v1/admin/runtimes/{name}/tenant-access", r.requireAdmin(r.grantAccessHandler(tenantaccessstore.KindRuntime)))
		mux.Handle("GET /v1/admin/runtimes/{name}/tenant-access", r.requireAdmin(r.listAccessHandler(tenantaccessstore.KindRuntime)))
		mux.Handle("DELETE /v1/admin/runtimes/{name}/tenant-access/{tenantId}", r.requireAdmin(r.revokeAccessHandler(tenantaccessstore.KindRuntime)))
		mux.Handle("POST /v1/admin/pools/{name}/tenant-access", r.requireAdmin(r.grantAccessHandler(tenantaccessstore.KindPool)))
		mux.Handle("GET /v1/admin/pools/{name}/tenant-access", r.requireAdmin(r.listAccessHandler(tenantaccessstore.KindPool)))
		mux.Handle("DELETE /v1/admin/pools/{name}/tenant-access/{tenantId}", r.requireAdmin(r.revokeAccessHandler(tenantaccessstore.KindPool)))
	}
	if r.breakers != nil {
		mux.Handle("GET /v1/admin/circuit-breakers", r.requireAdmin(http.HandlerFunc(r.handleListBreakers)))
		mux.Handle("GET /v1/admin/circuit-breakers/{name}", r.requireAdmin(http.HandlerFunc(r.handleGetBreaker)))
		mux.Handle("POST /v1/admin/circuit-breakers/{name}/open", r.requireAdmin(http.HandlerFunc(r.handleOpenBreaker)))
		mux.Handle("POST /v1/admin/circuit-breakers/{name}/close", r.requireAdmin(http.HandlerFunc(r.handleCloseBreaker)))
	}
	if r.tenants != nil || r.runtimes != nil || r.users != nil {
		mux.Handle("POST /v1/admin/bootstrap", r.requireAdmin(http.HandlerFunc(r.handleBootstrap)))
	}
	if r.tokenRevoker != nil {
		// §13.3 operator-initiated token revocation.
		mux.Handle("POST /v1/admin/issued-tokens/{jti}/revoke",
			r.requireAdmin(http.HandlerFunc(r.handleRevokeToken)))
	}
	if r.auditLog != nil {
		// §25.9 Audit Log Query API. The verify route is registered
		// before the {seq} route so "verify" is not parsed as a seq.
		mux.Handle("GET /v1/admin/audit-events/verify", r.requireAuditReader(http.HandlerFunc(r.handleVerifyAuditChain)))
		mux.Handle("GET /v1/admin/audit-events", r.requireAuditReader(http.HandlerFunc(r.handleListAuditEvents)))
		mux.Handle("GET /v1/admin/audit-events/{seq}", r.requireAuditReader(http.HandlerFunc(r.handleGetAuditEvent)))
	}
	if r.platformWired {
		// §25.3 platform introspection. Version + config are
		// platform-admin gated (config can carry sensitive
		// operational detail even with secrets redacted).
		mux.Handle("GET /v1/admin/platform/version", r.requireAdmin(http.HandlerFunc(r.handlePlatformVersion)))
		mux.Handle("GET /v1/admin/platform/config", r.requireAdmin(http.HandlerFunc(r.handlePlatformConfig)))
	}
	// §25.4 self-introspection — available to any authenticated
	// caller, no role gate. Returns the calling principal's identity
	// + role grants so a freshly-onboarded admin agent can discover
	// what operations it is permitted to invoke.
	mux.HandleFunc("GET /v1/admin/me", r.handleMe)
	mux.HandleFunc("GET /v1/admin/me/authorized-tools", r.handleAuthorizedTools)
	return mux
}

// requireAuditReader gates the §25.9 audit-query endpoints on
// platform-admin or tenant-admin (the per-tenant scoping is applied
// inside auditTenant).
func (r *Router) requireAuditReader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		p, ok := authmw.FromContext(req.Context())
		if !ok {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "endpoint requires authentication", nil)
			return
		}
		if !p.HasRole(auth.RolePlatformAdmin) && !p.HasRole(auth.RoleTenantAdmin) {
			writeError(w, http.StatusForbidden, "FORBIDDEN",
				"audit query requires platform-admin or tenant-admin", nil)
			return
		}
		next.ServeHTTP(w, req)
	})
}

// requireAdmin gates a genuinely platform-scoped admin route on the
// §10.2 platform-admin role. Routes the §10.2 matrix also grants to
// `tenant-admin` (own tenant) use a permission-aware gate instead;
// requireAdmin is reserved for operations the matrix restricts to
// platform-admin (tenant CRUD, runtime / pool create and delete,
// connectors, circuit breakers, billing corrections, platform-wide
// settings).
func (r *Router) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		principal, ok := authmw.FromContext(req.Context())
		if !ok || !principal.HasRole(auth.RolePlatformAdmin) {
			writeError(w, http.StatusForbidden, "FORBIDDEN",
				"admin endpoint requires the platform-admin role", nil)
			return
		}
		next.ServeHTTP(w, req)
	})
}

// requirePermission gates an endpoint on a §10.2 permission. A
// principal passes when one of its roles — built-in or tenant custom —
// grants perm. platform-admin and tenant-admin pass through their
// permission-matrix rows; a tenant custom role passes when its
// permission set includes perm. For a tenant-resource permission
// (manage_environments, manage_credential_pools) the admitted built-in
// set is identical to requireTenantResourceAdmin; the gate additionally
// admits a custom role that holds the permission.
func (r *Router) requirePermission(perm auth.Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p, ok := authmw.FromContext(req.Context())
			if !ok {
				writeError(w, http.StatusForbidden, "FORBIDDEN",
					"endpoint requires authentication", nil)
				return
			}
			if !r.principalGrantsPermission(req.Context(), p, perm) {
				writeError(w, http.StatusForbidden, "FORBIDDEN",
					"this endpoint requires the "+string(perm)+" permission", nil)
				return
			}
			next.ServeHTTP(w, req)
		})
	}
}

// TenantPayload is the §15.1 admin-tenant request/response body.
type TenantPayload struct {
	ID                       string                      `json:"id"`
	DisplayName              string                      `json:"displayName,omitempty"`
	ComplianceProfile        string                      `json:"complianceProfile,omitempty"`
	DataResidencyRegion      string                      `json:"dataResidencyRegion,omitempty"`
	WorkspaceTier            string                      `json:"workspaceTier,omitempty"`
	MaxConcurrentSessions    int                         `json:"maxConcurrentSessions,omitempty"`
	StorageQuotaBytes        int64                       `json:"storageQuotaBytes,omitempty"`
	TokenQuotaPerWindow      int64                       `json:"tokenQuotaPerWindow,omitempty"`
	MinIsolationProfile      string                      `json:"minIsolationProfile,omitempty"`
	BillingErasurePolicy     string                      `json:"billingErasurePolicy,omitempty"`
	ExperimentTargeting      *experiment.TargetingConfig `json:"experimentTargeting,omitempty"`
	CreatedAt                string                      `json:"createdAt,omitempty"`
	UpdatedAt                string                      `json:"updatedAt,omitempty"`
	DeletedAt                string                      `json:"deletedAt,omitempty"`
	T4KmsLastProbeSuccessAt  string                      `json:"t4KmsLastProbeSuccessAt,omitempty"`
}

// fromTenant maps a stored row to the wire payload. If probe is
// non-nil and the tenant is at workspaceTier T4, the payload includes
// `t4KmsLastProbeSuccessAt` per §15.1.
func fromTenant(t tenantstore.Tenant) TenantPayload {
	return fromTenantWithProbe(t, nil)
}

func fromTenantWithProbe(t tenantstore.Tenant, probe KMSProbe) TenantPayload {
	p := TenantPayload{
		ID:                    t.ID,
		DisplayName:           t.DisplayName,
		ComplianceProfile:     t.ComplianceProfile,
		DataResidencyRegion:   t.DataResidencyRegion,
		WorkspaceTier:         t.WorkspaceTier,
		MaxConcurrentSessions: t.MaxConcurrentSessions,
		StorageQuotaBytes:     t.StorageQuotaBytes,
		TokenQuotaPerWindow:   t.TokenQuotaPerWindow,
		MinIsolationProfile:   t.MinIsolationProfile,
		BillingErasurePolicy:  t.BillingErasurePolicy,
		CreatedAt:             rfc3339Nano(t.CreatedAt),
		UpdatedAt:             rfc3339Nano(t.UpdatedAt),
		DeletedAt:             rfc3339Nano(t.DeletedAt),
	}
	if t.ExperimentTargeting.Configured() {
		et := t.ExperimentTargeting.Clone()
		p.ExperimentTargeting = &et
	}
	if probe != nil && t.WorkspaceTier == "T4" {
		if ts, ok := probe.LastProbeSuccess(t.ID); ok {
			p.T4KmsLastProbeSuccessAt = rfc3339Nano(ts)
		}
	}
	return p
}

// validBillingErasurePolicy reports whether s is an accepted §12.8
// billingErasurePolicy: empty (the pseudonymize default), pseudonymize,
// or exempt.
func validBillingErasurePolicy(s string) bool {
	return s == "" ||
		s == tenantstore.BillingErasurePseudonymize ||
		s == tenantstore.BillingErasureExempt
}

// regulatedComplianceProfiles are the §12.8 compliance profiles for
// which a billingErasurePolicy of exempt warrants an audit signal.
var regulatedComplianceProfiles = map[string]bool{
	"hipaa":   true,
	"fedramp": true,
	"soc2":    true,
}

// complianceProfileRank maps the §11.7 compliance ratchet ladder
// (none < soc2 < fedramp < hipaa) to an ordinal. An empty value is
// equivalent to none. A profile not on the ladder (for example gdpr)
// is absent from the map and is not subject to the ratchet.
var complianceProfileRank = map[string]int{
	"":        0,
	"none":    0,
	"soc2":    1,
	"fedramp": 2,
	"hipaa":   3,
}

// isComplianceDowngrade reports whether a transition from current to
// requested lowers the §11.7 compliance ratchet ordinal. The ratchet
// is evaluated only between ladder profiles; a transition that
// involves an off-ladder profile is not treated as a downgrade.
func isComplianceDowngrade(current, requested string) bool {
	cur, curOnLadder := complianceProfileRank[current]
	req, reqOnLadder := complianceProfileRank[requested]
	if !curOnLadder || !reqOnLadder {
		return false
	}
	return req < cur
}

// workspaceTierRank maps the §12.9 storage-classification tier to an
// ordinal. The tenant-settable workspaceTier is T3 or T4; an unset
// value defaults to T3, so the empty string ranks with T3. A value
// outside the ladder is absent from the map and is not ratcheted.
var workspaceTierRank = map[string]int{
	"":   1,
	"T3": 1,
	"T4": 2,
}

// isWorkspaceTierDowngrade reports whether a transition from current to
// requested lowers the §12.9 storage-classification tier. §15.1 states
// that workspaceTier is ratcheted stricter-only, exactly as the §11.7
// complianceProfile is. The ratchet is evaluated only between ladder
// tiers.
func isWorkspaceTierDowngrade(current, requested string) bool {
	cur, curOnLadder := workspaceTierRank[current]
	req, reqOnLadder := workspaceTierRank[requested]
	if !curOnLadder || !reqOnLadder {
		return false
	}
	return req < cur
}

// emitBillingErasureExemptRegulated emits the §12.8
// compliance.billing_erasure_exempt_regulated audit event when a tenant
// combines billingErasurePolicy=exempt with a regulated compliance
// profile. The combination is permitted (retaining identifiable billing
// records can be a legitimate operational need); the event makes the
// retention trade-off visible in the audit trail so compliance officers
// can confirm a documented legal basis.
func (r *Router) emitBillingErasureExemptRegulated(ctx context.Context, p authmw.Principal, t tenantstore.Tenant) {
	if t.BillingErasurePolicy != tenantstore.BillingErasureExempt {
		return
	}
	if !regulatedComplianceProfiles[t.ComplianceProfile] {
		return
	}
	r.emit(ctx, p, "compliance.billing_erasure_exempt_regulated", t.ID, map[string]any{
		"tenant_id":            t.ID,
		"complianceProfile":    t.ComplianceProfile,
		"billingErasurePolicy": tenantstore.BillingErasureExempt,
	})
}

// EmitBillingErasureExemptRegulatedStartup scans every active tenant
// and emits the §12.8 compliance.billing_erasure_exempt_regulated audit
// event for each that combines billingErasurePolicy=exempt with a
// regulated compliance profile. The gateway calls it once at startup so
// the posture is re-surfaced in the audit trail and cannot silently
// persist across redeployments. It is a no-op when no audit sink is
// wired.
func EmitBillingErasureExemptRegulatedStartup(ctx context.Context, tenants tenantstore.Store, sink AuditSink, clock func() time.Time) error {
	if sink == nil {
		return nil
	}
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	rows, err := tenants.List(ctx, tenantstore.ListFilter{})
	if err != nil {
		return err
	}
	for _, t := range rows {
		if t.BillingErasurePolicy != tenantstore.BillingErasureExempt {
			continue
		}
		if !regulatedComplianceProfiles[t.ComplianceProfile] {
			continue
		}
		sink.EmitAdminEvent(ctx, AuditEvent{
			Type:           "compliance.billing_erasure_exempt_regulated",
			ActorTenantID:  t.ID,
			TargetResource: t.ID,
			Detail: map[string]any{
				"tenant_id":            t.ID,
				"complianceProfile":    t.ComplianceProfile,
				"billingErasurePolicy": tenantstore.BillingErasureExempt,
			},
			At: clock(),
		})
	}
	return nil
}

// handleCreateTenant implements POST /v1/admin/tenants.
func (r *Router) handleCreateTenant(w http.ResponseWriter, req *http.Request) {
	var body TenantPayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	if body.ID == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id is required",
			map[string]any{"field": "id"})
		return
	}
	if err := auth.ValidateTenantID(body.ID); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(),
			map[string]any{"field": "id"})
		return
	}
	if body.MaxConcurrentSessions < 0 {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"maxConcurrentSessions must not be negative",
			map[string]any{"field": "maxConcurrentSessions"})
		return
	}
	if body.StorageQuotaBytes < 0 {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"storageQuotaBytes must not be negative",
			map[string]any{"field": "storageQuotaBytes"})
		return
	}
	if body.TokenQuotaPerWindow < 0 {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"tokenQuotaPerWindow must not be negative",
			map[string]any{"field": "tokenQuotaPerWindow"})
		return
	}
	if body.MinIsolationProfile != "" && !isolation.IsValid(isolation.Profile(body.MinIsolationProfile)) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"minIsolationProfile must be standard, sandboxed, or microvm",
			map[string]any{"field": "minIsolationProfile"})
		return
	}
	if !validBillingErasurePolicy(body.BillingErasurePolicy) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"billingErasurePolicy must be pseudonymize or exempt",
			map[string]any{"field": "billingErasurePolicy"})
		return
	}
	if body.ExperimentTargeting != nil {
		if err := body.ExperimentTargeting.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(),
				map[string]any{"field": "experimentTargeting"})
			return
		}
	}

	t := tenantstore.Tenant{
		ID:                    body.ID,
		DisplayName:           body.DisplayName,
		ComplianceProfile:     body.ComplianceProfile,
		DataResidencyRegion:   body.DataResidencyRegion,
		WorkspaceTier:         body.WorkspaceTier,
		MaxConcurrentSessions: body.MaxConcurrentSessions,
		StorageQuotaBytes:     body.StorageQuotaBytes,
		TokenQuotaPerWindow:   body.TokenQuotaPerWindow,
		MinIsolationProfile:   body.MinIsolationProfile,
		BillingErasurePolicy:  body.BillingErasurePolicy,
		CreatedAt:             r.clock(),
	}
	if body.ExperimentTargeting != nil {
		t.ExperimentTargeting = body.ExperimentTargeting.Clone()
	}
	t.UpdatedAt = t.CreatedAt
	if err := r.tenants.Create(req.Context(), t); err != nil {
		if errors.Is(err, tenantstore.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "RESOURCE_CONFLICT",
				"tenant with this id already exists",
				map[string]any{"id": body.ID})
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	row, _ := r.tenants.Get(req.Context(), body.ID)
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		// requireAdmin should have caught this; defensive assertion.
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	r.emit(req.Context(), principal, "admin.tenant.created", body.ID, map[string]any{
		"displayName":           row.DisplayName,
		"complianceProfile":     row.ComplianceProfile,
		"dataResidencyRegion":   row.DataResidencyRegion,
		"workspaceTier":         row.WorkspaceTier,
		"maxConcurrentSessions": row.MaxConcurrentSessions,
		"storageQuotaBytes":     row.StorageQuotaBytes,
	})
	r.emitBillingErasureExemptRegulated(req.Context(), principal, row)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(fromTenant(row))
}

// handleListTenants implements GET /v1/admin/tenants.
func (r *Router) handleListTenants(w http.ResponseWriter, req *http.Request) {
	filter := tenantstore.ListFilter{
		IncludeDeleted: req.URL.Query().Get("includeDeleted") == "true",
	}
	rows, err := r.tenants.List(req.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	out := make([]TenantPayload, 0, len(rows))
	for _, t := range rows {
		out = append(out, fromTenant(t))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"tenants": out})
}

// handleGetTenant implements GET /v1/admin/tenants/{id}.
func (r *Router) handleGetTenant(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("id")
	row, err := r.tenants.Get(req.Context(), id)
	if err != nil {
		if errors.Is(err, tenantstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "tenant not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromTenantWithProbe(row, r.kmsProbe))
}

// UpdateTenantRequest is the §15.1 admin-tenant update body. Only
// the fields explicitly present are mutated; omitting a field leaves
// the stored value untouched. Empty-string clears the field.
type UpdateTenantRequest struct {
	DisplayName           *string                     `json:"displayName,omitempty"`
	ComplianceProfile     *string                     `json:"complianceProfile,omitempty"`
	DataResidencyRegion   *string                     `json:"dataResidencyRegion,omitempty"`
	WorkspaceTier         *string                     `json:"workspaceTier,omitempty"`
	MaxConcurrentSessions *int                        `json:"maxConcurrentSessions,omitempty"`
	StorageQuotaBytes     *int64                      `json:"storageQuotaBytes,omitempty"`
	TokenQuotaPerWindow   *int64                      `json:"tokenQuotaPerWindow,omitempty"`
	MinIsolationProfile   *string                     `json:"minIsolationProfile,omitempty"`
	BillingErasurePolicy  *string                     `json:"billingErasurePolicy,omitempty"`
	ExperimentTargeting   *experiment.TargetingConfig `json:"experimentTargeting,omitempty"`
}

// handleUpdateTenant implements PUT /v1/admin/tenants/{id}.
func (r *Router) handleUpdateTenant(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("id")
	var body UpdateTenantRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	if body.MaxConcurrentSessions != nil && *body.MaxConcurrentSessions < 0 {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"maxConcurrentSessions must not be negative",
			map[string]any{"field": "maxConcurrentSessions"})
		return
	}
	if body.StorageQuotaBytes != nil && *body.StorageQuotaBytes < 0 {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"storageQuotaBytes must not be negative",
			map[string]any{"field": "storageQuotaBytes"})
		return
	}
	if body.TokenQuotaPerWindow != nil && *body.TokenQuotaPerWindow < 0 {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"tokenQuotaPerWindow must not be negative",
			map[string]any{"field": "tokenQuotaPerWindow"})
		return
	}
	if body.MinIsolationProfile != nil && *body.MinIsolationProfile != "" &&
		!isolation.IsValid(isolation.Profile(*body.MinIsolationProfile)) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"minIsolationProfile must be standard, sandboxed, or microvm",
			map[string]any{"field": "minIsolationProfile"})
		return
	}
	if body.BillingErasurePolicy != nil && !validBillingErasurePolicy(*body.BillingErasurePolicy) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"billingErasurePolicy must be pseudonymize or exempt",
			map[string]any{"field": "billingErasurePolicy"})
		return
	}
	if body.ExperimentTargeting != nil {
		if err := body.ExperimentTargeting.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(),
				map[string]any{"field": "experimentTargeting"})
			return
		}
	}
	// §11.7 / §12.9 ratchet checks: complianceProfile and workspaceTier
	// may be tightened in place but never lowered through the generic
	// update endpoint. Both compare against the tenant's current row.
	if body.ComplianceProfile != nil || body.WorkspaceTier != nil {
		current, err := r.tenants.Get(req.Context(), id)
		if err != nil {
			if errors.Is(err, tenantstore.ErrNotFound) {
				writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "tenant not found", nil)
				return
			}
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
		if body.ComplianceProfile != nil &&
			isComplianceDowngrade(current.ComplianceProfile, *body.ComplianceProfile) {
			writeError(w, http.StatusUnprocessableEntity, "COMPLIANCE_PROFILE_DOWNGRADE_PROHIBITED",
				"complianceProfile may be tightened in place but not lowered through this endpoint; "+
					"use POST /v1/admin/tenants/{id}/compliance-profile/decommission for a legitimate wind-down",
				map[string]any{
					"currentProfile":   current.ComplianceProfile,
					"requestedProfile": *body.ComplianceProfile,
				})
			return
		}
		// §12.9 workspaceTier ratchet. §15.1 names §12.9 as the authority
		// for the stricter-only rule but the §15.1 error-code table
		// defines no workspaceTier-specific code. The rejection therefore
		// reuses CLASSIFICATION_CONTROL_VIOLATION — the §12.9-owned
		// storage-classification control code, whose details.reason field
		// the spec leaves open — with reason tier_downgrade_prohibited.
		if body.WorkspaceTier != nil &&
			isWorkspaceTierDowngrade(current.WorkspaceTier, *body.WorkspaceTier) {
			writeError(w, http.StatusUnprocessableEntity, "CLASSIFICATION_CONTROL_VIOLATION",
				"workspaceTier may be tightened in place but not lowered; lowering a tenant's "+
					"storage classification tier would weaken its data-classification controls",
				map[string]any{
					"tenantId": id,
					"tier":     *body.WorkspaceTier,
					"reason":   "tier_downgrade_prohibited",
				})
			return
		}
		// §12.5 T4 KMS availability probe. A workspaceTier: T4
		// transition (whether a new T3 → T4 promotion or an
		// idempotent re-assert) runs a zero-byte encrypt/decrypt
		// round-trip against the tenant-scoped KMS key before
		// persisting. The pre-provisioning model is required: the
		// operator must provision the key before this call. On
		// probe failure the update is rejected with
		// CLASSIFICATION_CONTROL_VIOLATION and the tenant remains at
		// its prior tier — no row update has happened yet.
		if body.WorkspaceTier != nil && *body.WorkspaceTier == "T4" && r.kmsProbe != nil {
			if err := r.kmsProbe.ProbeAvailability(req.Context(), id, "T4"); err != nil {
				writeError(w, http.StatusUnprocessableEntity, "CLASSIFICATION_CONTROL_VIOLATION",
					"T4 KMS key availability probe failed; the tenant's per-tenant KMS key "+
						"must be reachable before the tenant can be marked workspaceTier T4",
					map[string]any{
						"tenantId": id,
						"tier":     "T4",
						"reason":   "kms_probe_failed",
					})
				return
			}
		}
	}
	updated, err := r.tenants.Update(req.Context(), id, func(t *tenantstore.Tenant) error {
		if body.DisplayName != nil {
			t.DisplayName = *body.DisplayName
		}
		if body.ComplianceProfile != nil {
			t.ComplianceProfile = *body.ComplianceProfile
		}
		if body.DataResidencyRegion != nil {
			t.DataResidencyRegion = *body.DataResidencyRegion
		}
		if body.WorkspaceTier != nil {
			t.WorkspaceTier = *body.WorkspaceTier
		}
		if body.MaxConcurrentSessions != nil {
			t.MaxConcurrentSessions = *body.MaxConcurrentSessions
		}
		if body.StorageQuotaBytes != nil {
			t.StorageQuotaBytes = *body.StorageQuotaBytes
		}
		if body.TokenQuotaPerWindow != nil {
			t.TokenQuotaPerWindow = *body.TokenQuotaPerWindow
		}
		if body.MinIsolationProfile != nil {
			t.MinIsolationProfile = *body.MinIsolationProfile
		}
		if body.BillingErasurePolicy != nil {
			t.BillingErasurePolicy = *body.BillingErasurePolicy
		}
		if body.ExperimentTargeting != nil {
			t.ExperimentTargeting = body.ExperimentTargeting.Clone()
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, tenantstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "tenant not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		// requireAdmin should have caught this; defensive assertion.
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	r.emit(req.Context(), principal, "admin.tenant.updated", id, map[string]any{
		"changedFields": changedFields(body),
	})
	r.emitBillingErasureExemptRegulated(req.Context(), principal, updated)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromTenantWithProbe(updated, r.kmsProbe))
}

// changedFields returns the list of body fields the caller set so the
// audit event records the intent without leaking the new value
// (values are still on the row response; the audit detail captures
// the change list for compact mutation history).
func changedFields(b UpdateTenantRequest) []string {
	var out []string
	if b.DisplayName != nil {
		out = append(out, "displayName")
	}
	if b.ComplianceProfile != nil {
		out = append(out, "complianceProfile")
	}
	if b.DataResidencyRegion != nil {
		out = append(out, "dataResidencyRegion")
	}
	if b.WorkspaceTier != nil {
		out = append(out, "workspaceTier")
	}
	if b.MaxConcurrentSessions != nil {
		out = append(out, "maxConcurrentSessions")
	}
	if b.StorageQuotaBytes != nil {
		out = append(out, "storageQuotaBytes")
	}
	if b.MinIsolationProfile != nil {
		out = append(out, "minIsolationProfile")
	}
	if b.BillingErasurePolicy != nil {
		out = append(out, "billingErasurePolicy")
	}
	return out
}

// DecommissionComplianceRequest is the §15.1 body for the attested
// compliance-profile wind-down. It is the sole path that may lower a
// tenant's complianceProfile — the generic PUT rejects downgrades per
// the §11.7 ratchet.
type DecommissionComplianceRequest struct {
	// PreviousProfile must equal the tenant's current profile — a
	// concurrency guard against a racing update.
	PreviousProfile string `json:"previousProfile"`
	// TargetProfile is the lower profile to ratchet down to.
	TargetProfile string `json:"targetProfile"`
	// AcknowledgeDataRemediation must be true.
	AcknowledgeDataRemediation bool `json:"acknowledgeDataRemediation"`
	// Justification is required free-text recorded in the audit event.
	Justification string `json:"justification"`
	// RemediationAttestations lists the remediation steps the operator
	// attests to; at least one entry is required.
	RemediationAttestations []string `json:"remediationAttestations"`
}

// handleDecommissionCompliance implements
// POST /v1/admin/tenants/{id}/compliance-profile/decommission per §11.7
// and §15.1: the attested, platform-admin-only path that lowers a
// regulated complianceProfile, which the generic PUT forbids.
func (r *Router) handleDecommissionCompliance(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("id")
	var body DecommissionComplianceRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	if !body.AcknowledgeDataRemediation {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"acknowledgeDataRemediation must be true",
			map[string]any{"field": "acknowledgeDataRemediation"})
		return
	}
	if body.Justification == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"justification is required",
			map[string]any{"field": "justification"})
		return
	}
	if len(body.RemediationAttestations) == 0 {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"at least one remediationAttestations entry is required",
			map[string]any{"field": "remediationAttestations"})
		return
	}

	current, err := r.tenants.Get(req.Context(), id)
	if err != nil {
		if errors.Is(err, tenantstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "tenant not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// Concurrency guard: previousProfile must match the live value.
	if body.PreviousProfile != current.ComplianceProfile {
		writeError(w, http.StatusConflict, "RESOURCE_CONFLICT",
			"previousProfile does not match the tenant's current complianceProfile",
			map[string]any{
				"currentProfile":  current.ComplianceProfile,
				"previousProfile": body.PreviousProfile,
			})
		return
	}
	// targetProfile must be a ladder profile strictly below the current
	// one. A target at or above the current profile is not a wind-down.
	curRank, curOnLadder := complianceProfileRank[current.ComplianceProfile]
	tgtRank, tgtOnLadder := complianceProfileRank[body.TargetProfile]
	if !curOnLadder || !tgtOnLadder {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"previousProfile and targetProfile must be ratchet-ladder profiles (none, soc2, fedramp, hipaa)",
			map[string]any{"field": "targetProfile"})
		return
	}
	if tgtRank >= curRank {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"targetProfile must be strictly lower than the current complianceProfile",
			map[string]any{"field": "targetProfile"})
		return
	}

	updated, err := r.tenants.Update(req.Context(), id, func(t *tenantstore.Tenant) error {
		t.ComplianceProfile = body.TargetProfile
		return nil
	})
	if err != nil {
		if errors.Is(err, tenantstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "tenant not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	r.emit(req.Context(), principal, "compliance.profile_decommissioned", id, map[string]any{
		"previous_profile":         body.PreviousProfile,
		"target_profile":           body.TargetProfile,
		"justification":            body.Justification,
		"remediation_attestations": body.RemediationAttestations,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromTenant(updated))
}

// handleDeleteTenant implements DELETE /v1/admin/tenants/{id}. The
// minimal implementation soft-deletes per §12.8; full hard-delete
// (the tenant-deletion controller) ships in Phase 13.
func (r *Router) handleDeleteTenant(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("id")
	if err := r.tenants.SoftDelete(req.Context(), id, r.clock()); err != nil {
		if errors.Is(err, tenantstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "tenant not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		// requireAdmin should have caught this; defensive assertion.
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	r.emit(req.Context(), principal, "admin.tenant.soft_deleted", id, nil)
	w.WriteHeader(http.StatusNoContent)
}

// writeError shares the §15.1 envelope shape with the rest of the
// gateway.
func writeError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": code, "message": message, "details": details},
	})
}
