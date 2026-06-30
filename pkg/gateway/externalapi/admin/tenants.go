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
	"strconv"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/billing/correctionstore"
	"github.com/lennylabs/lenny/pkg/gateway/breakerstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorcredstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/environment/customrolestore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/deploymentconfigstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantaccessstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/evalstore"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/errorclassify"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/pagination"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationpolicystore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/operability/recommendations"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor/interceptorstore"
	"github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/externaladapterstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimecapoverride"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
	corr "github.com/lennylabs/lenny/pkg/observability/correlation"
	"github.com/lennylabs/lenny/pkg/quota"
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

	// OperationID is the §11.7 lines 347-348 optional correlation
	// token, sourced from the X-Lenny-Operation-ID request header (via
	// the correlation context). The OCSF translator projects it onto
	// metadata.correlation_uid. Empty when the request carried no
	// header. spec: §11.7 line 347.
	OperationID string

	// CallerKind is the §11.7 lines 347-348 optional caller class,
	// sourced from the OIDC caller_type claim ("human" / "service" /
	// "agent"). The OCSF translator projects it onto actor.user.type /
	// type_id, which the §25.9 human-vs-agent reporting reads. Empty
	// when the token carried no claim. spec: §11.7 line 348.
	CallerKind string

	// AgentName is the §15.1 line 938 human-readable agent instance
	// identifier, sourced from the X-Lenny-Agent-Name request header
	// (via the correlation context). §15.1 requires it be propagated to
	// audit records so the §25.9 audit-query API can attribute a
	// remediation to the operator agent that issued it. Empty when the
	// request carried no header. spec: §15.1 line 938. F-15.1.10.
	AgentName string

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
	tenants    tenantstore.Store
	runtimes   runtimestore.Store
	users      userstore.Store
	pools      poolstore.Store
	breakers   breakerstore.Store
	connectors connectorstore.Store
	// externalAdapters / adapterValidator back the §15.1 / §24.8
	// external-protocol adapter registry CRUD and validate gate.
	externalAdapters externaladapterstore.Store
	adapterValidator AdapterValidator
	connectorOAuth   *ConnectorOAuth
	// connectorTester / connectorCreds / connectorTestLimiter back the
	// §15.1 `POST /v1/admin/connectors/{name}/test` live-connectivity
	// endpoint. A nil connectorTester leaves the route unregistered.
	connectorTester      ConnectorTester
	connectorCreds       connectorcredstore.Store
	connectorTestLimiter ratelimit.Counter
	// connectorRefresher / connectorRefreshLimiter back the §9.3 line 136
	// `POST /v1/admin/connectors/{name}/refresh` capability-inference
	// endpoint. A nil refresher leaves the route unregistered.
	connectorRefresher      ConnectorCapabilityRefresher
	connectorRefreshLimiter ratelimit.Counter
	delegationPolicies      delegationpolicystore.Store
	// interceptors backs the §4.8 / §15.1 external-interceptor registry
	// CRUD surface; interceptorCooldownSeconds is the cluster-scoped
	// `gateway.interceptorWeakeningCooldownSeconds` recorded on a
	// `fail-closed → fail-open` transition (§8.3 SEC-013). A nil store
	// leaves the routes unregistered. F-4.8.17.
	interceptors               interceptorstore.Store
	interceptorCooldownSeconds int
	credentialPools            credentialpoolstore.Store
	poolCredRevoker            PoolCredentialRevoker
	poolCredHealth             PoolCredentialHealthReader
	customRoles                customrolestore.Store
	tenantAccess               tenantaccessstore.Store
	// capOverrides backs the §5.1 line 49 per-tenant runtime capability
	// override CRUD surface. Nil leaves the routes unregistered. F-5.1.20.
	capOverrides runtimecapoverride.Store
	auditLog     AuditLog
	auditPruner  AuditPartitionDropper
	auditMetrics AuditQueryMetrics
	// auditScatter / scatterCache / scatterCacheEnabled back the §25.9
	// line 3668/3709 platform-admin cross-tenant audit scatter-gather and
	// its Redis result cache. A nil auditScatter leaves the platform-admin
	// no-tenantId query on the single-tenant (`platform`) read path.
	// F-25.9.11.
	auditScatter        auditScatterReader
	scatterCache        ScatterGatherCache
	scatterCacheEnabled bool
	tokenRevoker        IssuedTokenRevoker
	revocationCache     RevocationCache
	// adminToken provisions/rotates the §17.6 initial admin credential
	// (lenny-admin + lenny-admin-token Secret). Nil leaves the bootstrap
	// admin-token step and the rotate-token route inactive. F-17.6.3.
	adminToken     AdminTokenProvisioner
	userPods       UserPodTerminator
	userLeases     UserLeaseRevoker
	userTokens     UserTokenRevoker
	userPlayground UserPlaygroundRevoker
	erasureRunner  ErasureRunner
	erasureJobs    erasurejob.Store
	// impersonation drives the §13.3 platform-admin impersonation flow
	// (POST/DELETE/GET /v1/admin/impersonation). Nil leaves the routes
	// unregistered. F-16.7.1.
	impersonation ImpersonationService
	// saltRotator backs POST /v1/admin/tenants/{id}/rotate-erasure-salt
	// (§12.8 line 857). Nil leaves the route unregistered. F-12.8.5.
	saltRotator   ErasureSaltRotator
	artifactHolds ArtifactLegalHolder
	// escrowReleaser runs the §12.8 line 884 escrow-GC release when a legal
	// hold is cleared (hold: false): it deletes the escrow objects the hold
	// protected and emits legal_hold.escrow_released. Nil leaves the clear
	// path releasing nothing (a deployment with no force-delete escrow).
	escrowReleaser    EscrowReleaser
	billing           billingstore.Store
	corrections       correctionstore.Store
	dualControlThresh float64
	// approverNotifier delivers the §11.2.1 dual-control approval
	// notification to eligible approvers via the configured
	// billing.approverNotificationWebhook. Nil leaves the notification
	// step inactive (the workflow still records the pending request).
	// spec: §11.2.1 line 175. F-11.2.14.
	approverNotifier ApproverNotifier
	sessions         sessionstore.Store
	// sessionAdmin backs the §24.11 platform-admin session-investigation
	// endpoints (GET /v1/admin/sessions/{id}, force-terminate). Nil leaves
	// them unregistered. spec: §24.11 lines 135-136.
	sessionAdmin  SessionAdmin
	interactions  interactionstore.Store
	experiments   experimentstore.Store
	stickyFlusher StickyFlusher
	environments  environmentstore.Store
	evals         evalstore.Store
	evalMatview   bool
	clock         func() time.Time
	audit         AuditSink
	metrics       RBACConfigMetrics

	platformInfo   PlatformInfo
	platformConfig map[string]string
	platformWired  bool

	recommendations RecommendationService
	eventBuffer     EventBufferQuerier
	eventEmitter    events.EventEmitter

	kmsProbe KMSProbe
	// elicitationFloor returns the current §9.2 / §17.2 platform-wide
	// elicitation content-integrity floor. It is a function rather than a
	// static string so a floor change sourced from the phase-stamp
	// ConfigMap at runtime (§17.2 line 86) is observed by every read
	// without re-wiring the Router. A nil function reads as the empty
	// (no-floor) default.
	elicitationFloor func() string

	// deploymentConfig backs the §16.7 deployment-transition audit emitter
	// (POST /v1/admin/deployment/config-change). It holds the last-applied
	// Helm deployment-scope configuration baseline the endpoint diffs each
	// render against. A nil store leaves the route unregistered.
	// F-8.2.5, F-9.2.10, F-17.2.8.
	deploymentConfig deploymentconfigstore.Store

	// siemConfigured mirrors whether the platform has an
	// `audit.siem.endpoint` configured. The §11.7 compliance enforcement
	// gate rejects creating or updating a tenant to a regulated
	// complianceProfile (and creating an environment under one) with
	// COMPLIANCE_SIEM_REQUIRED when it is false. spec: §11.7 lines 445-451.
	siemConfigured bool

	// pgauditConfigured mirrors whether the platform has both
	// `audit.pgaudit.enabled: true` and `audit.pgaudit.sinkEndpoint`
	// configured. The §11.7 item-5 compliance gate rejects creating or
	// updating a tenant to a regulated complianceProfile (and creating an
	// environment under one) with COMPLIANCE_PGAUDIT_REQUIRED when it is
	// false. spec: §11.7 lines 374-379.
	pgauditConfigured bool

	reconciliationResumer ReconciliationResumer
	poolStatus            PoolStatusReader
	crdGenerations        CRDGenerationReader
	poolDrainMetrics      PoolDrainMetrics
	poolBootstrap         PoolBootstrapStatusReader

	credentialRekey CredentialRekeyer
	secretProber    SecretAccessProber

	// caRotation drives the §10.3 CA-rotation state machine. Nil leaves
	// the /v1/admin/ca-rotation routes unregistered (mTLS PKI disabled).
	// F-10.3.21.
	caRotation CARotationManager

	// runtimeUpgrade drives the §10.5 RuntimeUpgrade state machine. Nil
	// leaves the /v1/admin/pools/{name}/upgrade routes unregistered.
	// F-10.5.1.
	runtimeUpgrade RuntimeUpgradeManager

	// artifactReplication backs the §25.11 line 3898-3899
	// POST/GET /v1/admin/artifact-replication/{region}/{resume,status}
	// endpoints. The routes are registered unconditionally so an agent
	// always reaches a real endpoint; a nil controller (the default until
	// the gateway wires the live replication.Controller) answers 503
	// ARTIFACT_REPLICATION_UNAVAILABLE.
	artifactReplication ArtifactReplicationController

	// migrations backs the §15.1 / §24.13 schema-migration management
	// endpoints. Nil leaves them unregistered.
	migrations MigrationManager

	// preflighter backs the §15.1 line 890 `POST /v1/admin/preflight`
	// API-backed infrastructure-connectivity preflight. Nil leaves the
	// endpoint unregistered.
	preflighter InfraPreflighter

	// leaseDenials clears the §8.6 extension-denied flag, backing the
	// §15.1 line 868 DELETE …/extension-denial admin endpoint. Nil leaves
	// the endpoint unregistered.
	leaseDenials LeaseDenialClearer

	// tenantResolver maps a delegation tree's root session id to its
	// owning tenant. handleClearExtensionDenial uses it to confine a
	// non-platform-admin caller to its own tenant before the clear, so a
	// tenant-admin cannot clear another tenant's extension-denial row
	// given an opaque session UUID (§10.2 line 261). Nil fails closed: a
	// non-platform-admin caller is rejected.
	tenantResolver leasecontrol.TenantResolver

	// quotaReconciler backs the §15.1 line 879 POST
	// /v1/admin/quota/reconcile endpoint. The route is always registered
	// so the §24.6 CLI command reaches a real endpoint; a nil reconciler
	// (the default until F-11.2.4 wires the Postgres token-usage
	// checkpoint store) answers 503 QUOTA_RECONCILE_UNAVAILABLE.
	quotaReconciler QuotaReconciler

	// devMode mirrors Options.DevMode; it selects the §5.3 line 677
	// dev-mode isolation default for pools and runtimes that omit
	// isolationProfile.
	devMode bool

	// maxFinalizingTimeoutSeconds is the gateway-side outer bound
	// from §11.3 line 219. When > 0, runtime registration and bootstrap
	// reject a runtime whose `setupPolicy.timeoutSeconds` exceeds it,
	// honouring the §6.2 line 260 invariant
	// `maxFinalizingTimeoutSeconds ≥ setupTimeoutSeconds`. Zero
	// disables enforcement (used in tests that do not exercise the
	// finalizing-state watchdog).
	//
	// spec: §6.2 line 260; §11.3 line 219.
	maxFinalizingTimeoutSeconds int
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
	Query(since uint64, filter events.EventFilter, limit int) events.BufferedEventPage
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

	// DevMode is the platform global.devMode (LENNY_DEV_MODE=true). When
	// true, a pool or runtime that omits isolationProfile defaults to
	// `standard` (runc) per §5.3 line 677 so a developer can run on a
	// cluster without gVisor; pools defaulted this way also receive the
	// explicit allowStandardIsolation opt-in dev mode supplies on their
	// behalf. When false the default is the production `sandboxed`.
	DevMode bool
}

// NewRouter returns a Router. Pass nil for opts to use the defaults.
func NewRouter(tenants tenantstore.Store, opts Options) *Router {
	clock := opts.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Router{tenants: tenants, clock: clock, audit: opts.Audit, metrics: opts.Metrics, devMode: opts.DevMode}
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

// WithMaxFinalizingTimeoutSeconds wires the §11.3 line 219 gateway
// outer bound onto the Router so the §15.1 runtime POST/PUT and
// bootstrap handlers can reject a runtime whose
// `setupPolicy.timeoutSeconds` exceeds it. A non-positive value
// disables enforcement; production should pass the same value the
// finalizing watchdog uses so the §6.2 line 260 invariant
// `maxFinalizingTimeoutSeconds ≥ setupTimeoutSeconds` is enforced at
// every admission path.
//
// spec: §6.2 line 260; §11.3 line 219.
func (r *Router) WithMaxFinalizingTimeoutSeconds(seconds int) *Router {
	r.maxFinalizingTimeoutSeconds = seconds
	return r
}

// WithElicitationFloor wires the §9.2 platform-wide elicitation
// content-integrity floor onto the Router. The
// /v1/admin/tenants/{id}/elicitation-content-integrity GET handler
// returns both `storedMode` (the tenant's persisted value) and
// `effectiveMode` (the resolved `max(platformFloor, storedMode)`).
// The PUT handler rejects a write whose stored mode is strictly
// weaker than the floor with the
// ELICITATION_INTEGRITY_BELOW_PLATFORM_FLOOR error. Without a wired
// floor every floor is treated as `off` and the resolver is the
// identity function, matching the §9.2 default.
func (r *Router) WithElicitationFloor(mode string) *Router {
	r.elicitationFloor = func() string { return mode }
	return r
}

// WithElicitationFloorProvider wires a dynamic §9.2 / §17.2 floor source
// onto the Router. The provider is read on every floor-dependent request
// (the GET effective-mode resolution, the PUT below-floor guard, and the
// audit `platform_floor_at_change` field) so a floor change sourced from
// the phase-stamp ConfigMap at runtime (§17.2 line 86) takes effect
// without a gateway restart. A nil provider reads as the empty (no-floor)
// default.
func (r *Router) WithElicitationFloorProvider(fn func() string) *Router {
	r.elicitationFloor = fn
	return r
}

// elicitationFloorValue reads the current platform floor, defaulting to
// the empty (no-floor) string when no provider is wired.
func (r *Router) elicitationFloorValue() string {
	if r.elicitationFloor == nil {
		return ""
	}
	return r.elicitationFloor()
}

// WithSIEMConfigured records whether the platform has an
// `audit.siem.endpoint` configured. When false, the §11.7 compliance
// enforcement gate rejects creating or updating a tenant to a regulated
// complianceProfile (soc2, fedramp, hipaa), and creating an environment
// under such a tenant, with COMPLIANCE_SIEM_REQUIRED (HTTP 422). The
// production gateway passes `audit.siem.endpoint != ""`.
// spec: §11.7 lines 445-451.
func (r *Router) WithSIEMConfigured(configured bool) *Router {
	r.siemConfigured = configured
	return r
}

// complianceSIEMRequiredMessage is the §11.7 line 448 verbatim 422 body
// for the SIEM hard-requirement gate.
func complianceSIEMRequiredMessage(profile string) string {
	return "tenant.complianceProfile '" + profile + "' requires audit.siem.endpoint to be configured. " +
		"A database superuser can bypass INSERT-only grants; an independent SIEM copy is mandatory for compliance-grade audit integrity."
}

// requireSIEMForProfile reports whether the §11.7 gate must reject an
// operation that lands a tenant on the given complianceProfile: the
// profile is regulated (soc2, fedramp, hipaa) and no SIEM endpoint is
// configured. spec: §11.7 lines 445-451.
func (r *Router) requireSIEMForProfile(profile string) bool {
	return regulatedComplianceProfiles[profile] && !r.siemConfigured
}

// WithPgauditConfigured records whether the platform has both
// `audit.pgaudit.enabled: true` and `audit.pgaudit.sinkEndpoint`
// configured. When false, the §11.7 item-5 compliance gate rejects
// creating or updating a tenant to a regulated complianceProfile (soc2,
// fedramp, hipaa), and creating an environment under such a tenant, with
// COMPLIANCE_PGAUDIT_REQUIRED (HTTP 422). The production gateway passes
// `audit.pgaudit.enabled && audit.pgaudit.sinkEndpoint != ""`.
// spec: §11.7 lines 374-379.
func (r *Router) WithPgauditConfigured(configured bool) *Router {
	r.pgauditConfigured = configured
	return r
}

// compliancePgauditRequiredMessage is the §11.7 line 377 422 body for the
// pgaudit hard-requirement gate.
func compliancePgauditRequiredMessage(profile string) string {
	return "tenant.complianceProfile '" + profile + "' requires audit.pgaudit.enabled to be true and " +
		"audit.pgaudit.sinkEndpoint to be configured. pgaudit DDL/ROLE capture to an external append-only " +
		"sink closes the residual tamper window between periodic grant checks; it is mandatory for a " +
		"regulated compliance posture."
}

// requirePgauditForProfile reports whether the §11.7 item-5 gate must
// reject an operation that lands a tenant on the given complianceProfile:
// the profile is regulated (soc2, fedramp, hipaa) and pgaudit is not
// fully configured. spec: §11.7 lines 374-379.
func (r *Router) requirePgauditForProfile(profile string) bool {
	return regulatedComplianceProfiles[profile] && !r.pgauditConfigured
}

// emit fires an audit event when an AuditSink is wired. Never
// blocks the caller — sinks must do their own async delivery.
func (r *Router) emit(ctx context.Context, p authmw.Principal, eventType, resource string, detail map[string]any) {
	if r.audit == nil {
		return
	}
	// spec: §11.7 lines 347-348 / §15.1 line 938 — carry operation_id
	// (from the correlation context populated off X-Lenny-Operation-ID),
	// agent_name (off X-Lenny-Agent-Name), and caller_kind (from the
	// OIDC caller_type claim) when available so the OCSF translator can
	// project them onto metadata.correlation_uid, the agent attribution,
	// and actor.user.type.
	r.audit.EmitAdminEvent(ctx, AuditEvent{
		Type:           eventType,
		ActorSubject:   p.Subject,
		ActorTenantID:  p.TenantID,
		TargetResource: resource,
		OperationID:    corr.From(ctx).OperationID,
		AgentName:      corr.From(ctx).AgentName,
		CallerKind:     p.CallerType,
		Detail:         detail,
		At:             r.clock(),
	})
}

// appendBilling tees a §11.2.1 billing event into the per-tenant billing
// ledger. The §11.2.1 closed event set places the interceptor failPolicy,
// export-scan, credential-revocation, and pool-isolation config-change
// events in the cost-attribution stream alongside the §11.7 audit chain;
// the admin Router is their producer, so it dual-emits. The write is
// best-effort: a nil ledger (the no-billing minimal gateway) or a
// transient store fault never fails the admin mutation, which is already
// committed by the time emit/appendBilling runs. spec: §11.2.1. F-11.2.1.
func (r *Router) appendBilling(ctx context.Context, ev billingstore.Event) {
	if r.billing == nil || ev.TenantID == "" {
		return
	}
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = r.clock()
	}
	_, _ = r.billing.Append(ctx, ev)
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
		// §24.10 row 4 — platform-admin force-delete with the Phase 3.5
		// legal-hold escrow override. F-12.8.2, F-24.10.2.
		mux.Handle("POST /v1/admin/tenants/{id}/force-delete", r.requireAdmin(http.HandlerFunc(r.handleForceDeleteTenant)))
		if r.saltRotator != nil {
			// §12.8 line 857: platform-admin compromise-response salt rotation.
			mux.Handle("POST /v1/admin/tenants/{id}/rotate-erasure-salt",
				r.requireAdmin(http.HandlerFunc(r.handleRotateErasureSalt)))
		}
		// §15.1 line 823,824: GET/PUT elicitation-content-integrity admit
		// platform-admin or tenant-admin. The role gate widens to
		// requireTenantResourceAdmin; each handler confines a non-platform
		// caller to its own {id} via authorizeTenantPath. ADM-3.
		mux.Handle("GET /v1/admin/tenants/{id}/elicitation-content-integrity",
			r.requireTenantResourceAdmin(http.HandlerFunc(r.handleGetElicitationIntegrity)))
		mux.Handle("PUT /v1/admin/tenants/{id}/elicitation-content-integrity",
			r.requireTenantResourceAdmin(http.HandlerFunc(r.handlePutElicitationIntegrity)))
		mux.Handle("POST /v1/admin/tenants/{id}/compliance-profile/decommission",
			r.requireAdmin(http.HandlerFunc(r.handleDecommissionCompliance)))
		if r.deploymentConfig != nil {
			// §16.7 deployment-transition audit emitter: the post-upgrade
			// hook reconciles the rendered Helm deployment config against the
			// persisted baseline and emits the gateway.*/platform.*/deployment.*
			// transition events under the operator's identity. F-8.2.5,
			// F-9.2.10, F-17.2.8.
			mux.Handle("POST /v1/admin/deployment/config-change",
				r.requireAdmin(http.HandlerFunc(r.handleDeploymentConfigChange)))
		}
		// §15.1 lines 818-819: platform-admin tenant suspend/resume. Suspend
		// rejects new session creation and message injection with
		// TENANT_SUSPENDED and drains the tenant's active sessions; resume
		// restores normal operation without un-terminating those sessions.
		mux.Handle("POST /v1/admin/tenants/{id}/suspend",
			r.requireAdmin(http.HandlerFunc(r.handleSuspendTenant)))
		mux.Handle("POST /v1/admin/tenants/{id}/resume",
			r.requireAdmin(http.HandlerFunc(r.handleResumeTenant)))
		// §10.6 / §15.1 tenant RBAC config, gated on manage_rbac_config:
		// platform-admin or tenant-admin, scoped to the path tenant.
		rbacConfigAdmin := r.requirePermission(auth.PermManageRBACConfig)
		mux.Handle("GET /v1/admin/tenants/{id}/rbac-config",
			rbacConfigAdmin(http.HandlerFunc(r.handleGetRBACConfig)))
		mux.Handle("PUT /v1/admin/tenants/{id}/rbac-config",
			rbacConfigAdmin(http.HandlerFunc(r.handlePutRBACConfig)))
		// §5.1 line 49: per-tenant runtime capability customization. A
		// tenant-scoped config sub-resource gated, like rbac-config, on
		// manage_rbac_config (platform-admin or tenant-admin scoped to the
		// path tenant). F-5.1.20.
		if r.capOverrides != nil {
			capOverrideAdmin := r.requirePermission(auth.PermManageRBACConfig)
			mux.Handle("GET /v1/admin/tenants/{id}/runtime-capability-overrides",
				capOverrideAdmin(http.HandlerFunc(r.handleListRuntimeCapabilityOverrides)))
			mux.Handle("GET /v1/admin/tenants/{id}/runtime-capability-overrides/{runtime}",
				capOverrideAdmin(http.HandlerFunc(r.handleGetRuntimeCapabilityOverride)))
			mux.Handle("PUT /v1/admin/tenants/{id}/runtime-capability-overrides/{runtime}",
				capOverrideAdmin(http.HandlerFunc(r.handlePutRuntimeCapabilityOverride)))
			mux.Handle("DELETE /v1/admin/tenants/{id}/runtime-capability-overrides/{runtime}",
				capOverrideAdmin(http.HandlerFunc(r.handleDeleteRuntimeCapabilityOverride)))
		}
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
	if r.migrations != nil {
		// §15.1 lines 891-892 / §24.13 lines 150-151: schema-migration
		// status read and last-resort down-migration. Both require
		// platform-admin.
		mux.Handle("GET /v1/admin/schema/migrations/status",
			r.requireAdmin(http.HandlerFunc(r.handleMigrationStatus)))
		mux.Handle("POST /v1/admin/schema/migrations/{version}/down",
			r.requireAdmin(http.HandlerFunc(r.handleMigrationDown)))
	}
	if r.eventBuffer != nil {
		mux.Handle("GET /v1/admin/events/buffer",
			r.requireAdmin(http.HandlerFunc(r.handleEventBuffer)))
	}
	if r.sessionAdmin != nil {
		// §24.11 platform-admin session investigation: read-through and
		// the operator-driven forced terminal transition. Both resolve a
		// session by its global id and are platform-admin-only.
		mux.Handle("GET /v1/admin/sessions/{id}",
			r.requireAdmin(http.HandlerFunc(r.handleGetSession)))
		mux.Handle("POST /v1/admin/sessions/{id}/force-terminate",
			r.requireAdmin(http.HandlerFunc(r.handleForceTerminateSession)))
	}
	// §15.1 line 879 / §24.6 line 99 — quota-counter re-aggregation. The
	// route is registered unconditionally so the `lenny-ctl admin quota
	// reconcile` command always reaches a real endpoint; the handler
	// answers 503 QUOTA_RECONCILE_UNAVAILABLE when the reconciler seam is
	// unwired (the default until the F-11.2.4 Postgres checkpoint lands).
	mux.Handle("POST /v1/admin/quota/reconcile",
		r.requireAdmin(http.HandlerFunc(r.handleQuotaReconcile)))
	// §25.11 lines 3898-3899 — ArtifactStore replication resume and
	// status. Registered unconditionally so the agent always reaches a
	// real endpoint; the handlers answer 503 when the replication
	// controller is unwired (the Tier-1 dev default). Both are
	// platform-admin-only (§25.11 line 3898 narrows resume to
	// platform-admin; status follows for symmetry).
	mux.Handle("POST /v1/admin/artifact-replication/{region}/resume",
		r.requireAdmin(http.HandlerFunc(r.handleResumeArtifactReplication)))
	mux.Handle("GET /v1/admin/artifact-replication/{region}/status",
		r.requireAdmin(http.HandlerFunc(r.handleArtifactReplicationStatus)))
	if r.users != nil {
		mux.Handle("POST /v1/admin/users", r.requireUserAdmin(http.HandlerFunc(r.handleCreateUser)))
		mux.Handle("GET /v1/admin/users", r.requireUserAdmin(http.HandlerFunc(r.handleListUsers)))
		// spec: §15.1 path-parameter casing — camelCase route templates.
		mux.Handle("GET /v1/admin/users/{userId}", r.requireUserAdmin(http.HandlerFunc(r.handleGetUser)))
		mux.Handle("PUT /v1/admin/users/{userId}", r.requireUserAdmin(http.HandlerFunc(r.handleUpdateUser)))
		mux.Handle("POST /v1/admin/users/{userId}/invalidate", r.requireUserAdmin(http.HandlerFunc(r.handleInvalidateUser)))
		mux.Handle("DELETE /v1/admin/users/{userId}", r.requireUserAdmin(http.HandlerFunc(r.handleDeleteUser)))
		// §15.1 lines 826-828 — tenant-scoped user listing and the
		// platform-managed role assignment surface. manage_users gate
		// (platform-admin or tenant-admin); the handler scopes a
		// tenant-admin to the path tenant.
		mux.Handle("GET /v1/admin/tenants/{id}/users", r.requireUserAdmin(http.HandlerFunc(r.handleListTenantUsers)))
		mux.Handle("PUT /v1/admin/tenants/{id}/users/{userId}/role", r.requireUserAdmin(http.HandlerFunc(r.handlePutTenantUserRole)))
		mux.Handle("DELETE /v1/admin/tenants/{id}/users/{userId}/role", r.requireUserAdmin(http.HandlerFunc(r.handleDeleteTenantUserRole)))
		if r.erasureRunner != nil {
			// §12.8 GDPR user erasure.
			mux.Handle("POST /v1/admin/users/{userId}/erase",
				r.requireUserAdmin(http.HandlerFunc(r.handleEraseUser)))
		}
	}
	if r.erasureJobs != nil {
		// §12.8 erasure-job status query.
		mux.Handle("GET /v1/admin/erasure-jobs/{jobId}",
			r.requireUserAdmin(http.HandlerFunc(r.handleGetErasureJob)))
		// §24.12 line 143 / §12.8 line 766 — operator retry of a failed
		// erasure job. platform-admin only.
		if r.erasureRunner != nil && r.users != nil {
			mux.Handle("POST /v1/admin/erasure-jobs/{jobId}/retry",
				r.requireAdmin(http.HandlerFunc(r.handleRetryErasureJob)))
		}
		// §24.12 line 144 / §12.8 line 764 — manual clear of the GDPR
		// Article 18 processing restriction after a failed job.
		// platform-admin only.
		if r.users != nil {
			mux.Handle("POST /v1/admin/erasure-jobs/{jobId}/clear-processing-restriction",
				r.requireAdmin(http.HandlerFunc(r.handleClearErasureRestriction)))
		}
	}
	if r.sessions != nil {
		// §15.1 line 865 / §10.2 line 280 — legal hold set / clear is
		// platform-admin or tenant-admin; a tenant-admin is confined to its
		// own tenant by the body-tenant binding in handleSetLegalHold.
		mux.Handle("POST /v1/admin/legal-hold",
			r.requireTenantResourceAdmin(http.HandlerFunc(r.handleSetLegalHold)))
		// §15.1 line 865 active-hold listing. platform-admin or
		// tenant-admin; a tenant-admin is auto-scoped to its own tenant.
		mux.Handle("GET /v1/admin/legal-holds",
			r.requireTenantResourceAdmin(http.HandlerFunc(r.handleListLegalHolds)))
	}
	if r.impersonation != nil {
		// §13.3 / §16.7 platform-admin impersonation flow. The handlers
		// enforce platform-admin-only inside (a cross-tenant impersonation
		// is never available to a tenant-admin); requireAdmin is the outer
		// admin gate.
		mux.Handle("POST /v1/admin/impersonation",
			r.requireAdmin(http.HandlerFunc(r.handleStartImpersonation)))
		mux.Handle("GET /v1/admin/impersonation",
			r.requireAdmin(http.HandlerFunc(r.handleListImpersonation)))
		mux.Handle("DELETE /v1/admin/impersonation/{id}",
			r.requireAdmin(http.HandlerFunc(r.handleEndImpersonation)))
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
		// §15.1 line 840: environment billing rollup. Only mounted when a
		// billing ledger is wired so the route is absent rather than
		// silently returning zero usage on a billing-less deployment.
		if r.billing != nil {
			mux.Handle("GET /v1/admin/environments/{name}/usage",
				envAdmin(http.HandlerFunc(r.handleEnvironmentUsage)))
		}
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
		// §25.17 lines 5232-5239: the agent-operability worked example and
		// the warm-pool-exhaustion runbook scale a pool through the dedicated
		// warm-count sub-route with the `minWarm` field. It runs on the same
		// manage_pools gate as the §15.1 PUT it delegates to.
		mux.Handle("PUT /v1/admin/pools/{name}/warm-count", poolManage(http.HandlerFunc(r.handleUpdatePoolWarmCount)))
		// §6.1 line 63, §15.1 line 801: override the SDK-warm circuit-breaker
		// state for a pool (enabled | disabled | auto). Runs on the same
		// manage_pools gate as the §15.1 PUT it shares the resource with.
		mux.Handle("PUT /v1/admin/pools/{name}/circuit-breaker", poolManage(http.HandlerFunc(r.handleUpdatePoolCircuitBreaker)))
		// §15.1 line 797: drain a pool. Transitions the pool to `draining`
		// so the gateway stops admitting new sessions to it and reports the
		// in-flight count + estimated drain seconds. Runs on the same
		// manage_pools gate as the PUT it shares the resource with.
		mux.Handle("POST /v1/admin/pools/{name}/drain", poolManage(http.HandlerFunc(r.handleDrainPool)))
		// §17.8.2 step 3: clear a pool's bootstrapMinWarm override so the
		// PoolScalingController switches to formula-driven scaling
		// immediately. Runs on the same manage_pools gate as the PUT it
		// shares the resource with.
		mux.Handle("DELETE /v1/admin/pools/{name}/bootstrap-override", poolManage(http.HandlerFunc(r.handleClearBootstrapOverride)))
		mux.Handle("DELETE /v1/admin/pools/{name}", r.requireAdmin(http.HandlerFunc(r.handleDeletePool)))
		// §4.6.2 item 3 (c): operator-initiated reset of a stuck pool's
		// admission-denial backoff. Registered unconditionally: when an
		// in-process PoolScalingController denial tracker is wired the
		// handler clears the counter synchronously; otherwise (the
		// production split deployment, where gateway and PSC are separate
		// binaries) it bumps the durable reconciliation_resume_epoch the
		// PSC observes on its next reconcile tick.
		mux.Handle("POST /v1/admin/pools/{name}/resume-reconciliation",
			poolManage(http.HandlerFunc(r.handleResumeReconciliation)))
		// §4.6.2 item 4: GET /v1/admin/pools/{name}/sync-status reports
		// the Postgres vs CRD generation comparison. spec:
		// spec/04_system-components.md line 560. The route is
		// registered unconditionally; without a CRD reader wired the
		// handler reports the Postgres-only generation and leaves
		// crdGeneration / inSync at their zero values so operators can
		// see Postgres is moving even on the §6.0 Postgres-only dev
		// posture.
		mux.Handle("GET /v1/admin/pools/{name}/sync-status",
			r.requireTenantResourceAdmin(http.HandlerFunc(r.handleSyncStatus)))
	}
	if r.connectors != nil {
		mux.Handle("POST /v1/admin/connectors", r.requireAdmin(http.HandlerFunc(r.handleCreateConnector)))
		mux.Handle("GET /v1/admin/connectors", r.requireAdmin(http.HandlerFunc(r.handleListConnectors)))
		// spec: §15.1 line 767 — admin CRUD resources use `{name}` as the
		// path identifier; connectors are keyed by their registry name
		// (F-15.1.12).
		mux.Handle("GET /v1/admin/connectors/{name}", r.requireAdmin(http.HandlerFunc(r.handleGetConnector)))
		mux.Handle("PUT /v1/admin/connectors/{name}", r.requireAdmin(http.HandlerFunc(r.handleUpdateConnector)))
		mux.Handle("DELETE /v1/admin/connectors/{name}", r.requireAdmin(http.HandlerFunc(r.handleDeleteConnector)))
		if r.connectorTester != nil {
			// §15.1 line 791 live-connectivity test. The §15.1 line 1163
			// contract grants this to platform-admin and tenant-admin.
			mux.Handle("POST /v1/admin/connectors/{name}/test",
				r.requireTenantResourceAdmin(http.HandlerFunc(r.handleTestConnector)))
		}
		if r.connectorRefresher != nil {
			// §9.3 line 136 capability inference. Like the test endpoint it
			// dials the external endpoint on the sanctioned outbound path,
			// so it is granted to platform-admin and tenant-admin and shares
			// the §15.1 line 1180 per-connector rate limit.
			mux.Handle("POST /v1/admin/connectors/{name}/refresh",
				r.requireTenantResourceAdmin(http.HandlerFunc(r.handleRefreshConnectorCapabilities)))
		}
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
			// `{name}` wildcard in Go's ServeMux.
			mux.Handle("GET /v1/admin/connectors/oauth/callback",
				http.HandlerFunc(r.handleConnectorOAuthCallback))
			mux.Handle("POST /v1/admin/connectors/{name}/oauth/authorize",
				http.HandlerFunc(r.handleAuthorizeConnector))
		}
	}
	if r.externalAdapters != nil {
		// spec: §15.1 lines 850-855 — external-protocol adapter CRUD plus
		// the §24.8 line 113 validate gate. All require platform-admin
		// (§24.8 grants `platform-admin`). The literal `validate` segment
		// takes precedence over the `{name}` wildcard in Go's ServeMux.
		mux.Handle("POST /v1/admin/external-adapters", r.requireAdmin(http.HandlerFunc(r.handleCreateExternalAdapter)))
		mux.Handle("GET /v1/admin/external-adapters", r.requireAdmin(http.HandlerFunc(r.handleListExternalAdapters)))
		mux.Handle("GET /v1/admin/external-adapters/{name}", r.requireAdmin(http.HandlerFunc(r.handleGetExternalAdapter)))
		mux.Handle("PUT /v1/admin/external-adapters/{name}", r.requireAdmin(http.HandlerFunc(r.handleUpdateExternalAdapter)))
		mux.Handle("DELETE /v1/admin/external-adapters/{name}", r.requireAdmin(http.HandlerFunc(r.handleDeleteExternalAdapter)))
		mux.Handle("POST /v1/admin/external-adapters/{name}/validate", r.requireAdmin(http.HandlerFunc(r.handleValidateExternalAdapter)))
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
	if r.interceptors != nil {
		// §4.8 / §15.1 external-interceptor registry. Interceptors are
		// platform-scoped cluster infrastructure, so the CRUD surface is
		// gated on the platform-admin role (requireAdmin), the distinct
		// credential domain §8.3 SEC-013 separates from cluster-config
		// access. F-4.8.17.
		mux.Handle("POST /v1/admin/interceptors", r.requireAdmin(http.HandlerFunc(r.handleCreateInterceptor)))
		mux.Handle("GET /v1/admin/interceptors", r.requireAdmin(http.HandlerFunc(r.handleListInterceptors)))
		mux.Handle("GET /v1/admin/interceptors/{name}", r.requireAdmin(http.HandlerFunc(r.handleGetInterceptor)))
		mux.Handle("PUT /v1/admin/interceptors/{name}", r.requireAdmin(http.HandlerFunc(r.handleUpdateInterceptor)))
		mux.Handle("DELETE /v1/admin/interceptors/{name}", r.requireAdmin(http.HandlerFunc(r.handleDeleteInterceptor)))
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
		// §15.1 lines 876-878 / §24.5 rows 3-5: per-credential subresource
		// CRUD (add / update / remove a single credential in a pool).
		mux.Handle("POST /v1/admin/credential-pools/{name}/credentials",
			credPoolAdmin(http.HandlerFunc(r.handleAddCredential)))
		mux.Handle("PUT /v1/admin/credential-pools/{name}/credentials/{credId}",
			credPoolAdmin(http.HandlerFunc(r.handleUpdateCredentialEntry)))
		mux.Handle("DELETE /v1/admin/credential-pools/{name}/credentials/{credId}",
			credPoolAdmin(http.HandlerFunc(r.handleRemoveCredential)))
		// §4.9 Emergency Credential Revocation: single-credential revoke,
		// pool-wide force-rotate, and the re-enable path. Same
		// manage_credential_pools gate as the pool CRUD.
		mux.Handle("POST /v1/admin/credential-pools/{name}/credentials/{credId}/revoke",
			credPoolAdmin(http.HandlerFunc(r.handleRevokeCredential)))
		mux.Handle("POST /v1/admin/credential-pools/{name}/credentials/{credId}/re-enable",
			credPoolAdmin(http.HandlerFunc(r.handleReEnableCredential)))
		mux.Handle("POST /v1/admin/credential-pools/{name}/revoke",
			credPoolAdmin(http.HandlerFunc(r.handleRevokePool)))
	}
	if r.credentialRekey != nil {
		// §4.9.1 KMS-key-rotation re-encryption job. KEK rotation is a
		// platform-security operation, so both routes are platform-admin
		// gated; the path tenant identifies the per-tenant KEK to
		// re-key. POST runs the re-encryption loop; GET runs the
		// verification query the operator checks before disabling the
		// old KEK version.
		mux.Handle("POST /v1/admin/tenants/{id}/credential-rekey",
			r.requireAdmin(http.HandlerFunc(r.handleCredentialRekey)))
		mux.Handle("GET /v1/admin/tenants/{id}/credential-rekey",
			r.requireAdmin(http.HandlerFunc(r.handleCredentialRekeyStatus)))
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
	if r.caRotation != nil {
		// §10.3 lines 344-350 — operator-driven cluster-internal CA
		// rotation. The procedure is platform-global, so every route is
		// platform-admin-only. F-10.3.21.
		mux.Handle("GET /v1/admin/ca-rotation", r.requireAdmin(http.HandlerFunc(r.handleGetCARotation)))
		mux.Handle("POST /v1/admin/ca-rotation/begin", r.requireAdmin(http.HandlerFunc(r.handleBeginCARotation)))
		mux.Handle("POST /v1/admin/ca-rotation/promote", r.requireAdmin(http.HandlerFunc(r.handlePromoteCARotation)))
		mux.Handle("POST /v1/admin/ca-rotation/retire", r.requireAdmin(http.HandlerFunc(r.handleRetireCARotation)))
	}
	if r.runtimeUpgrade != nil {
		// §10.5 lines 466-540 / §15.1 lines 869-874 — operator-driven
		// runtime image rollout for a pool. Every route is platform-admin
		// only (§24 lines 65-71). F-10.5.1.
		mux.Handle("POST /v1/admin/pools/{name}/upgrade/start", r.requireAdmin(http.HandlerFunc(r.handleUpgradeStart)))
		mux.Handle("POST /v1/admin/pools/{name}/upgrade/proceed", r.requireAdmin(http.HandlerFunc(r.handleUpgradeProceed)))
		mux.Handle("POST /v1/admin/pools/{name}/upgrade/pause", r.requireAdmin(http.HandlerFunc(r.handleUpgradePause)))
		mux.Handle("POST /v1/admin/pools/{name}/upgrade/resume", r.requireAdmin(http.HandlerFunc(r.handleUpgradeResume)))
		mux.Handle("POST /v1/admin/pools/{name}/upgrade/rollback", r.requireAdmin(http.HandlerFunc(r.handleUpgradeRollback)))
		mux.Handle("GET /v1/admin/pools/{name}/upgrade-status", r.requireAdmin(http.HandlerFunc(r.handleUpgradeStatus)))
	}
	if r.leaseDenials != nil {
		// §15.1 line 868 — clear the §8.6 extension-denied flag on a
		// subtree, bypassing the rejection cool-off window. Requires
		// platform-admin or tenant-admin.
		mux.Handle("DELETE /v1/admin/trees/{rootSessionId}/subtrees/{sessionId}/extension-denial",
			r.requireTenantResourceAdmin(http.HandlerFunc(r.handleClearExtensionDenial)))
	}
	if r.tenants != nil || r.runtimes != nil || r.users != nil {
		mux.Handle("POST /v1/admin/bootstrap", r.requireAdmin(http.HandlerFunc(r.handleBootstrap)))
	}
	if r.adminToken != nil {
		// §17.6 line 472 — rotate the initial admin token. platform-admin
		// only (requireAdmin). F-17.6.3.
		mux.Handle("POST /v1/admin/users/{user}/rotate-token",
			r.requireAdmin(http.HandlerFunc(r.handleRotateToken)))
	}
	if r.tokenRevoker != nil {
		// §13.3 operator-initiated token revocation.
		mux.Handle("POST /v1/admin/issued-tokens/{jti}/revoke",
			r.requireAdmin(http.HandlerFunc(r.handleRevokeToken)))
	}
	if r.auditLog != nil {
		// §25.9 Audit Log Query API. The summary route is registered
		// before the {seq} route so the literal path segment is not parsed
		// as a sequence number. Chain integrity is carried by the list
		// response's chainIntegrityReport envelope (§25.9 line 3653); §25.9
		// defines no standalone verify route. F-25.9.10.
		// §25.9 line 3661 aggregate counts by type/actor/resource.
		mux.Handle("GET /v1/admin/audit-events/summary", r.requireAuditReader(http.HandlerFunc(r.handleAuditSummary)))
		mux.Handle("GET /v1/admin/audit-events", r.requireAuditReader(http.HandlerFunc(r.handleListAuditEvents)))
		mux.Handle("GET /v1/admin/audit-events/{seq}", r.requireAuditReader(http.HandlerFunc(r.handleGetAuditEvent)))
		// §25.9 line 3662 audit-recovery: re-queue a row for OCSF
		// translation after a translator-version bump. Scope-gated on
		// audit:retranslate inside the handler.
		mux.Handle("POST /v1/admin/audit-events/{seq}/retranslate",
			r.requireAuditReader(http.HandlerFunc(r.handleRetranslateAuditEvent)))
		// §25.9 line 3663 audit-recovery: re-queue a terminally-failed
		// audit row for §12.6 CloudEvents re-publication. Scope-gated on
		// audit:republish inside the handler.
		mux.Handle("POST /v1/admin/audit-events/{seq}/republish",
			r.requireAuditReader(http.HandlerFunc(r.handleRepublishAuditEvent)))
		// §16.4 line 378 / §25.9 — operator force-drop of audit rows the
		// SIEM delivery guard is holding past their retention TTL, after
		// an explicit data-loss acknowledgement. Platform-admin gated;
		// only registered when a durable pruner is wired.
		if r.auditPruner != nil {
			mux.Handle("POST /v1/admin/audit-partitions/{partition}/drop",
				r.requireAdmin(http.HandlerFunc(r.handleForceDropAuditPartition)))
		}
	}
	if r.platformWired {
		// §25.3 platform introspection. Version + config are
		// platform-admin gated (config can carry sensitive
		// operational detail even with secrets redacted).
		mux.Handle("GET /v1/admin/platform/version", r.requireAdmin(http.HandlerFunc(r.handlePlatformVersion)))
		mux.Handle("GET /v1/admin/platform/config", r.requireAdmin(http.HandlerFunc(r.handlePlatformConfig)))
	}
	if r.preflighter != nil {
		// §15.1 line 890 / §24.2 — API-backed mode of `lenny-ctl
		// preflight`: active outbound Postgres/Redis/MinIO connectivity
		// and schema-version probes against the gateway's configured
		// backends. POST because it performs side-effecting outbound
		// dials. Reserved to platform-admin.
		mux.Handle("POST /v1/admin/preflight", r.requireAdmin(http.HandlerFunc(r.handlePreflight)))
	}
	// §25.4 self-introspection — available to any authenticated
	// caller, no role gate. Returns the calling principal's identity
	// + role grants so a freshly-onboarded admin agent can discover
	// what operations it is permitted to invoke.
	mux.HandleFunc("GET /v1/admin/me", r.handleMe)
	mux.HandleFunc("GET /v1/admin/me/authorized-tools", r.handleAuthorizedTools)
	// §25.1 enforcement point 1: the per-route scope gate runs before
	// the mux dispatches any handler, so a scope-narrowed token is
	// rejected with 403 SCOPE_FORBIDDEN before it reaches a destructive
	// admin handler at its full role ceiling. An absent scope claim, or
	// a route the document declares no scope for, defers to the role
	// gate on the matched handler. spec: §15.1 line 914,920; §25.1 line 94.
	return r.enforceScopes(mux)
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
	ID                      string                       `json:"id"`
	DisplayName             string                       `json:"displayName,omitempty"`
	ComplianceProfile       string                       `json:"complianceProfile,omitempty"`
	DataResidencyRegion     string                       `json:"dataResidencyRegion,omitempty"`
	WorkspaceTier           string                       `json:"workspaceTier,omitempty"`
	State                   string                       `json:"state,omitempty"`
	MaxConcurrentSessions   int                          `json:"maxConcurrentSessions,omitempty"`
	StorageQuotaBytes       int64                        `json:"storageQuotaBytes,omitempty"`
	TokenQuotaPerWindow     int64                        `json:"tokenQuotaPerWindow,omitempty"`
	QuotaResetPeriod        string                       `json:"quotaResetPeriod,omitempty"`
	GCPriority              string                       `json:"gcPriority,omitempty"`
	MinIsolationProfile     string                       `json:"minIsolationProfile,omitempty"`
	BillingErasurePolicy    string                       `json:"billingErasurePolicy,omitempty"`
	ExperimentTargeting     *experiment.TargetingConfig  `json:"experimentTargeting,omitempty"`
	CredentialPolicy        *credential.CredentialPolicy `json:"credentialPolicy,omitempty"`
	CreatedAt               string                       `json:"createdAt,omitempty"`
	UpdatedAt               string                       `json:"updatedAt,omitempty"`
	DeletedAt               string                       `json:"deletedAt,omitempty"`
	T4KmsLastProbeSuccessAt string                       `json:"t4KmsLastProbeSuccessAt,omitempty"`

	// ETag is the §15.1 optimistic-concurrency entity tag — the quoted
	// decimal version. A list consumer reads it per item to supply
	// If-Match on a subsequent PUT without a per-item GET. spec: §15.1
	// line 1209.
	ETag string `json:"etag,omitempty"`
}

// fromTenant maps a stored row to the wire payload. If probe is
// non-nil and the tenant is at workspaceTier T4, the payload includes
// `t4KmsLastProbeSuccessAt` per §15.1.
func fromTenant(t tenantstore.Tenant) TenantPayload {
	return fromTenantWithProbe(t, nil)
}

// tenantStateOrActive reports the §12.8 TenantState for the admin API,
// mapping the empty pre-lifecycle value to `active` so the response
// always carries a concrete state. spec: §12.8 line 865.
func tenantStateOrActive(state string) string {
	if state == "" {
		return tenantstore.TenantStateActive
	}
	return state
}

func fromTenantWithProbe(t tenantstore.Tenant, probe KMSProbe) TenantPayload {
	p := TenantPayload{
		ID:                    t.ID,
		DisplayName:           t.DisplayName,
		ComplianceProfile:     t.ComplianceProfile,
		DataResidencyRegion:   t.DataResidencyRegion,
		WorkspaceTier:         t.WorkspaceTier,
		State:                 tenantStateOrActive(t.State),
		MaxConcurrentSessions: t.MaxConcurrentSessions,
		StorageQuotaBytes:     t.StorageQuotaBytes,
		TokenQuotaPerWindow:   t.TokenQuotaPerWindow,
		QuotaResetPeriod:      t.QuotaResetPeriod,
		GCPriority:            gcPriorityOrNormal(t.GCPriority),
		MinIsolationProfile:   t.MinIsolationProfile,
		BillingErasurePolicy:  t.BillingErasurePolicy,
		CreatedAt:             rfc3339Nano(t.CreatedAt),
		UpdatedAt:             rfc3339Nano(t.UpdatedAt),
		DeletedAt:             rfc3339Nano(t.DeletedAt),
		// spec: §15.1 line 1207 — the ETag is the quoted decimal version.
		ETag: formatETag(t.Version),
	}
	if t.ExperimentTargeting.Configured() {
		et := t.ExperimentTargeting.Clone()
		p.ExperimentTargeting = &et
	}
	if t.CredentialPolicy.Configured() {
		cp := t.CredentialPolicy.Clone()
		p.CredentialPolicy = &cp
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

// validQuotaResetPeriod reports whether s is an accepted §11.2 line 31
// per-tenant quota reset period: empty (inherit the platform default),
// hourly, daily, monthly, or rolling.
func validQuotaResetPeriod(s string) bool {
	return s == "" || quota.ResetPeriod(s).IsValid()
}

// gcPriorityOrNormal maps the empty §12.5 GCPriority to the `normal`
// default so the admin response always carries a concrete value.
// spec: §12.5 line 317.
func gcPriorityOrNormal(s string) string {
	if s == "" {
		return tenantstore.GCPriorityNormal
	}
	return s
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

// validWorkspaceTier reports whether s is a §12.9 tenant-settable
// data-classification tier (empty, T3, or T4). It delegates to the
// canonical tenantstore validator so the admin API, the bootstrap
// upsert path, and the §10.6 environment override agree on the closed
// enum. spec: §12.9 line 1048; §15.1 line 816.
func validWorkspaceTier(s string) bool { return tenantstore.ValidWorkspaceTier(s) }

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

// SIEMStartupFatalMessage is the §11.7 line 450 verbatim fatal message
// the gateway logs when it refuses to start with a regulated tenant and
// no configured SIEM.
const SIEMStartupFatalMessage = "FATAL: one or more tenants have a regulated complianceProfile but audit.siem.endpoint is not configured. Configure SIEM or downgrade tenant complianceProfile to 'none' via POST /v1/admin/tenants/{id}/compliance-profile/decommission."

// ValidateSIEMForRegulatedTenants enforces the §11.7 line 450 startup
// gate: when any active tenant carries a regulated complianceProfile
// (soc2, fedramp, hipaa) and no SIEM endpoint is configured, it returns
// an error carrying SIEMStartupFatalMessage so the production gateway
// refuses to start. When siemConfigured is true the scan is skipped.
// spec: §11.7 lines 445-451.
func ValidateSIEMForRegulatedTenants(ctx context.Context, tenants tenantstore.Store, siemConfigured bool) error {
	if siemConfigured {
		return nil
	}
	rows, err := tenants.List(ctx, tenantstore.ListFilter{})
	if err != nil {
		return err
	}
	for _, t := range rows {
		if regulatedComplianceProfiles[t.ComplianceProfile] {
			return errors.New(SIEMStartupFatalMessage)
		}
	}
	return nil
}

// PgauditStartupFatalMessage is the §11.7 line 377 fatal message the
// gateway logs when it refuses to start with a regulated tenant and
// pgaudit not fully configured (`audit.pgaudit.enabled` false or
// `audit.pgaudit.sinkEndpoint` unset).
const PgauditStartupFatalMessage = "FATAL: one or more tenants have a regulated complianceProfile but audit.pgaudit.enabled is not true with audit.pgaudit.sinkEndpoint configured. Enable pgaudit DDL/ROLE capture to an external sink or downgrade tenant complianceProfile to 'none' via POST /v1/admin/tenants/{id}/compliance-profile/decommission."

// ValidatePgauditForRegulatedTenants enforces the §11.7 line 377 startup
// gate: when any active tenant carries a regulated complianceProfile
// (soc2, fedramp, hipaa) and pgaudit is not fully configured, it returns
// an error carrying PgauditStartupFatalMessage so the production gateway
// refuses to start. When pgauditConfigured is true the scan is skipped.
// spec: §11.7 lines 374-379.
func ValidatePgauditForRegulatedTenants(ctx context.Context, tenants tenantstore.Store, pgauditConfigured bool) error {
	if pgauditConfigured {
		return nil
	}
	rows, err := tenants.List(ctx, tenantstore.ListFilter{})
	if err != nil {
		return err
	}
	for _, t := range rows {
		if regulatedComplianceProfiles[t.ComplianceProfile] {
			return errors.New(PgauditStartupFatalMessage)
		}
	}
	return nil
}

// handleCreateTenant implements POST /v1/admin/tenants.
func (r *Router) handleCreateTenant(w http.ResponseWriter, req *http.Request) {
	var body TenantPayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	if body.ID == "" {
		// spec: §10.2 line 210 — the admin API rejects any tenant_id
		// that does not match the format with `400 INVALID_TENANT_ID`.
		// A missing id is a format violation (the regex requires at
		// least one character), so it routes through the same code.
		writeError(w, http.StatusBadRequest, "INVALID_TENANT_ID", "id is required",
			map[string]any{"field": "id"})
		return
	}
	if err := auth.ValidateTenantID(body.ID); err != nil {
		// spec: §10.2 line 210.
		writeError(w, http.StatusBadRequest, "INVALID_TENANT_ID", err.Error(),
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
	if !validQuotaResetPeriod(body.QuotaResetPeriod) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"quotaResetPeriod must be hourly, daily, monthly, or rolling",
			map[string]any{"field": "quotaResetPeriod"})
		return
	}
	if !tenantstore.ValidGCPriority(body.GCPriority) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"gcPriority must be normal or high",
			map[string]any{"field": "gcPriority"})
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
	// spec: §12.9 line 1048; §15.1 line 816 — workspaceTier is a closed
	// enum (tenant-settable T3 default or T4). An arbitrary string such as
	// "T2", "T5", or "prod" would be silently treated as "not T4" by every
	// downstream consumer (KMS probe skipped, t4-node-isolation predicate
	// skipped), so reject it at the registration boundary.
	if !validWorkspaceTier(body.WorkspaceTier) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"workspaceTier must be T3 or T4",
			map[string]any{"field": "workspaceTier"})
		return
	}
	if body.ExperimentTargeting != nil {
		if err := body.ExperimentTargeting.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(),
				map[string]any{"field": "experimentTargeting"})
			return
		}
	}
	if body.CredentialPolicy != nil {
		if err := body.CredentialPolicy.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(),
				map[string]any{"field": "credentialPolicy"})
			return
		}
	}
	// spec: §11.7 line 446 — creating a tenant with a regulated
	// complianceProfile when no audit.siem.endpoint is configured is
	// rejected with COMPLIANCE_SIEM_REQUIRED. The gate is unbypassable;
	// the deployer must configure SIEM before enabling a regulated tenant.
	if r.requireSIEMForProfile(body.ComplianceProfile) {
		writeError(w, http.StatusUnprocessableEntity, "COMPLIANCE_SIEM_REQUIRED",
			complianceSIEMRequiredMessage(body.ComplianceProfile),
			map[string]any{"field": "complianceProfile", "complianceProfile": body.ComplianceProfile})
		return
	}
	// spec: §11.7 line 377 — a regulated complianceProfile additionally
	// requires pgaudit DDL/ROLE capture to an external sink. The gate is
	// unbypassable, symmetric with the SIEM requirement above.
	if r.requirePgauditForProfile(body.ComplianceProfile) {
		writeError(w, http.StatusUnprocessableEntity, "COMPLIANCE_PGAUDIT_REQUIRED",
			compliancePgauditRequiredMessage(body.ComplianceProfile),
			map[string]any{"field": "complianceProfile", "complianceProfile": body.ComplianceProfile})
		return
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
		QuotaResetPeriod:      body.QuotaResetPeriod,
		GCPriority:            body.GCPriority,
		MinIsolationProfile:   body.MinIsolationProfile,
		BillingErasurePolicy:  body.BillingErasurePolicy,
		CreatedAt:             r.clock(),
	}
	if body.ExperimentTargeting != nil {
		t.ExperimentTargeting = body.ExperimentTargeting.Clone()
	}
	if body.CredentialPolicy != nil {
		t.CredentialPolicy = body.CredentialPolicy.Clone()
	}
	t.UpdatedAt = t.CreatedAt
	if err := r.tenants.Create(req.Context(), t); err != nil {
		if errors.Is(err, tenantstore.ErrAlreadyExists) {
			// spec: §15.1 line 983 — duplicate identifier is RESOURCE_ALREADY_EXISTS.
			writeError(w, http.StatusConflict, "RESOURCE_ALREADY_EXISTS",
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
	// spec: §15.1 lines 1228-1253 — canonical cursor-paginated envelope.
	// The id is the §15.1 line 1236 `name` sort field and the tiebreaker.
	writePaginatedList(w, req, r.clock(), out, adminTimestampSortFields, adminListDefaultSort,
		func(t TenantPayload, s pagination.Sort) (string, string) {
			switch s.Field {
			case "name":
				return t.ID, t.ID
			case "updated_at":
				return t.UpdatedAt, t.ID
			default:
				return t.CreatedAt, t.ID
			}
		})
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
	// spec: §12.8 line 873 — a `deleted`-state tenant is a tombstone
	// retained to prevent id reuse; GET returns 410 Gone with the
	// deletion timestamp rather than the (nulled) row. Tenants in
	// `disabling`/`deleting` are still resolvable (200) so an operator
	// can poll the in-progress lifecycle state.
	if !row.IsActive() {
		writeError(w, http.StatusGone, "TENANT_DELETED",
			"tenant has been deleted and its id is retained as a tombstone",
			map[string]any{"tenantId": id, "state": tenantStateOrActive(row.State), "deletedAt": rfc3339Nano(row.DeletedAt)})
		return
	}
	// spec: §15.1 line 1209 — GET carries the ETag header so the client can
	// use it as the next PUT's If-Match.
	w.Header().Set("ETag", formatETag(row.Version))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromTenantWithProbe(row, r.kmsProbe))
}

// UpdateTenantRequest is the §15.1 admin-tenant update body. Only
// the fields explicitly present are mutated; omitting a field leaves
// the stored value untouched. Empty-string clears the field.
type UpdateTenantRequest struct {
	DisplayName           *string                      `json:"displayName,omitempty"`
	ComplianceProfile     *string                      `json:"complianceProfile,omitempty"`
	DataResidencyRegion   *string                      `json:"dataResidencyRegion,omitempty"`
	WorkspaceTier         *string                      `json:"workspaceTier,omitempty"`
	MaxConcurrentSessions *int                         `json:"maxConcurrentSessions,omitempty"`
	StorageQuotaBytes     *int64                       `json:"storageQuotaBytes,omitempty"`
	TokenQuotaPerWindow   *int64                       `json:"tokenQuotaPerWindow,omitempty"`
	QuotaResetPeriod      *string                      `json:"quotaResetPeriod,omitempty"`
	GCPriority            *string                      `json:"gcPriority,omitempty"`
	MinIsolationProfile   *string                      `json:"minIsolationProfile,omitempty"`
	BillingErasurePolicy  *string                      `json:"billingErasurePolicy,omitempty"`
	ExperimentTargeting   *experiment.TargetingConfig  `json:"experimentTargeting,omitempty"`
	CredentialPolicy      *credential.CredentialPolicy `json:"credentialPolicy,omitempty"`
}

// handleUpdateTenant implements PUT /v1/admin/tenants/{id}.
func (r *Router) handleUpdateTenant(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("id")
	var body UpdateTenantRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
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
	if body.QuotaResetPeriod != nil && !validQuotaResetPeriod(*body.QuotaResetPeriod) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"quotaResetPeriod must be hourly, daily, monthly, or rolling",
			map[string]any{"field": "quotaResetPeriod"})
		return
	}
	if body.GCPriority != nil && !tenantstore.ValidGCPriority(*body.GCPriority) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"gcPriority must be normal or high",
			map[string]any{"field": "gcPriority"})
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
	// spec: §12.9 line 1048; §15.1 line 816 — reject an out-of-enum
	// workspaceTier before the ratchet check below, which only recognizes
	// the closed T3/T4 ladder. Without this gate a value like "T2" slips
	// past isWorkspaceTierDowngrade (off-ladder, so not a downgrade) and
	// persists as a tier every downstream consumer reads as "not T4".
	if body.WorkspaceTier != nil && !validWorkspaceTier(*body.WorkspaceTier) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"workspaceTier must be T3 or T4",
			map[string]any{"field": "workspaceTier"})
		return
	}
	if body.ExperimentTargeting != nil {
		if err := body.ExperimentTargeting.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(),
				map[string]any{"field": "experimentTargeting"})
			return
		}
	}
	if body.CredentialPolicy != nil {
		if err := body.CredentialPolicy.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(),
				map[string]any{"field": "credentialPolicy"})
			return
		}
	}
	// spec: §15.1 lines 1207-1211 — every admin PUT requires If-Match.
	// Resolve the current tenant so the entity tag (its version) is known
	// before applying the mutation; a missing tenant 404s ahead of the
	// precondition. The same row backs the §11.7 / §12.9 ratchet checks
	// below.
	current, err := r.tenants.Get(req.Context(), id)
	if err != nil {
		if errors.Is(err, tenantstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "tenant not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if !enforceIfMatch(w, req, current.Version) {
		return
	}
	// §11.7 / §12.9 ratchet checks: complianceProfile and workspaceTier
	// may be tightened in place but never lowered through the generic
	// update endpoint. Both compare against the tenant's current row.
	if body.ComplianceProfile != nil || body.WorkspaceTier != nil {
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
		// spec: §11.7 line 446 — updating a tenant to a regulated
		// complianceProfile when no audit.siem.endpoint is configured is
		// rejected with COMPLIANCE_SIEM_REQUIRED, symmetric with create.
		// The downgrade ratchet above runs first so a regulated→lower
		// transition still routes through COMPLIANCE_PROFILE_DOWNGRADE_PROHIBITED.
		if body.ComplianceProfile != nil && r.requireSIEMForProfile(*body.ComplianceProfile) {
			writeError(w, http.StatusUnprocessableEntity, "COMPLIANCE_SIEM_REQUIRED",
				complianceSIEMRequiredMessage(*body.ComplianceProfile),
				map[string]any{"field": "complianceProfile", "complianceProfile": *body.ComplianceProfile})
			return
		}
		// spec: §11.7 line 377 — updating to a regulated complianceProfile
		// with pgaudit not fully configured is rejected with
		// COMPLIANCE_PGAUDIT_REQUIRED, symmetric with create.
		if body.ComplianceProfile != nil && r.requirePgauditForProfile(*body.ComplianceProfile) {
			writeError(w, http.StatusUnprocessableEntity, "COMPLIANCE_PGAUDIT_REQUIRED",
				compliancePgauditRequiredMessage(*body.ComplianceProfile),
				map[string]any{"field": "complianceProfile", "complianceProfile": *body.ComplianceProfile})
			return
		}
		// §12.9 workspaceTier ratchet. §15.1 names §12.9 as the authority
		// for the stricter-only rule but the §15.1 error-code table
		// defines no workspaceTier-specific code. The rejection therefore
		// reuses CLASSIFICATION_CONTROL_VIOLATION — the §12.9-owned
		// storage-classification control code, whose details.reason field
		// the spec leaves open — with reason tier_downgrade_prohibited.
		if body.WorkspaceTier != nil &&
			tenantstore.IsWorkspaceTierDowngrade(current.WorkspaceTier, *body.WorkspaceTier) {
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
		if body.QuotaResetPeriod != nil {
			t.QuotaResetPeriod = *body.QuotaResetPeriod
		}
		if body.GCPriority != nil {
			t.GCPriority = *body.GCPriority
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
		if body.CredentialPolicy != nil {
			t.CredentialPolicy = body.CredentialPolicy.Clone()
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
	// spec: §15.1 line 1210 — a successful PUT carries the bumped ETag so
	// the client can chain a subsequent write without a refresh GET.
	w.Header().Set("ETag", formatETag(updated.Version))
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
	if b.GCPriority != nil {
		out = append(out, "gcPriority")
	}
	if b.MinIsolationProfile != nil {
		out = append(out, "minIsolationProfile")
	}
	if b.BillingErasurePolicy != nil {
		out = append(out, "billingErasurePolicy")
	}
	if b.ExperimentTargeting != nil {
		out = append(out, "experimentTargeting")
	}
	if b.CredentialPolicy != nil {
		out = append(out, "credentialPolicy")
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
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
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
		// spec: §15.1 line 981 — a state conflict is INVALID_STATE_TRANSITION.
		writeError(w, http.StatusConflict, "INVALID_STATE_TRANSITION",
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

// handleDeleteTenant implements DELETE /v1/admin/tenants/{id}. It
// initiates the §12.8 / §24.10 row-3 tenant-deletion lifecycle by
// transitioning the tenant from active into `disabling`; the background
// tenant-deletion controller (wired in the gateway) then advances the
// tenant through `disabling → deleting → deleted` asynchronously,
// running the §12.8 phases (soft-disable, terminate sessions, revoke
// credentials, legal-hold segregation, DeleteByTenant, KMS-key destroy,
// CRD cleanup, erasure receipt). The handler returns 202 immediately;
// operators monitor progress via GET /v1/admin/tenants/{id}.
//
// The transition is idempotent: a tenant already mid-lifecycle
// (`disabling`/`deleting`) is re-accepted at 202 with its current state,
// and a deletion request never restarts a lifecycle in flight. A
// tombstoned (`deleted`) tenant reads as not-found.
//
// spec: §12.8 line 865; §24.10 row 3 ("Initiate tenant deletion
// lifecycle ... runs asynchronously"). F-12.8.1, F-24.10.3.
func (r *Router) handleDeleteTenant(w http.ResponseWriter, req *http.Request) {
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
	// A tombstoned tenant is already deleted; the lifecycle is terminal.
	if tenantStateOrActive(row.State) == tenantstore.TenantStateDeleted || !row.DeletedAt.IsZero() {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "tenant not found", nil)
		return
	}
	// spec: §15.1 line 1213 — DELETE honours If-Match when present.
	if !enforceIfMatchIfPresent(w, req, row.Version) {
		return
	}
	updated, err := r.tenants.Update(req.Context(), id, func(t *tenantstore.Tenant) error {
		// Only an active tenant transitions into the lifecycle; a tenant
		// already disabling/deleting is left at its current phase so a
		// repeated DELETE does not rewind it.
		if t.State == "" || t.State == tenantstore.TenantStateActive {
			t.State = tenantstore.TenantStateDisabling
			t.UpdatedAt = r.clock()
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
	r.emit(req.Context(), principal, "admin.tenant.deletion_initiated", id,
		map[string]any{"state": tenantStateOrActive(updated.State)})
	writeJSON(w, http.StatusAccepted, map[string]any{
		"id":    id,
		"state": tenantStateOrActive(updated.State),
	})
}

// writeError writes the §15.1 / §25.2 canonical admin error envelope:
// `error.{code, category, message, retryable, suggestedRetryAfter?,
// details?}`. The `category` and `retryable` fields are populated from
// the shared §15.2.1 errorclassify table so the admin surface reports
// the same pair for a given code as the REST and MCP transports
// (spec: §15.1 line 944, §25.2 lines 302-329). A retryable failure at a
// 429 or 5xx status also advertises a backoff via `suggestedRetryAfter`
// and the matching `Retry-After` header (spec: §25.2 line 329 — the two
// agree when both are present).
func writeError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	writeErrorRetryAfter(w, status, code, message, details, 0)
}

// adminRetryAfterDefault is the §25.2 advisory backoff stamped on a
// retryable admin error that does not carry a more precise value. It
// matches the §4.3 token-service and rate-limit precedent of a 5-second
// floor for transient retries.
const adminRetryAfterDefault = 5 * time.Second

// writeErrorRetryAfter is writeError with an explicit backoff hint. A
// zero retryAfter defers to the category-derived default for retryable
// 429/5xx failures and omits the hint otherwise. spec: §25.2 line 329.
func writeErrorRetryAfter(w http.ResponseWriter, status int, code, message string, details map[string]any, retryAfter time.Duration) {
	cat, retryable := errorclassify.ClassifyStatus(code, status)
	body := map[string]any{
		"code":      code,
		"category":  string(cat),
		"message":   message,
		"retryable": retryable,
	}
	if details != nil {
		body["details"] = details
	}
	if retryable {
		if retryAfter == 0 && (status >= 500 || status == http.StatusTooManyRequests) {
			retryAfter = adminRetryAfterDefault
		}
		if retryAfter > 0 {
			secs := int(retryAfter.Seconds())
			body["suggestedRetryAfter"] = strconv.Itoa(secs) + "s"
			if w.Header().Get("Retry-After") == "" {
				w.Header().Set("Retry-After", strconv.Itoa(secs))
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": body})
}

// writeJSON writes v as a JSON body with the given status code. It is the
// success-path counterpart to writeError for the admin handlers.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
