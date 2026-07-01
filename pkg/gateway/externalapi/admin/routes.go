// SPDX-License-Identifier: MIT

package admin

import (
	"net/http"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantaccessstore"
)

// Handler builds the §15.1 admin mux. It constructs the ServeMux,
// registers each admin domain's routes through its registrar, and
// wraps the §25.1 per-route scope gate around the assembled mux. Each
// registerXRoutes helper owns one admin domain and registers only when
// its backing store or seam is wired, mirroring the per-domain file
// layout under pkg/gateway/externalapi/admin/.
//
// spec: §15.1 (admin API surface); §25.1 line 94 (scope enforcement point).
func (r *Router) Handler() http.Handler {
	mux := http.NewServeMux()
	r.registerTenantRoutes(mux)
	r.registerRuntimeRoutes(mux)
	r.registerRecommendationRoutes(mux)
	r.registerMigrationRoutes(mux)
	r.registerEventBufferRoutes(mux)
	r.registerSessionAdminRoutes(mux)
	r.registerQuotaRoutes(mux)
	r.registerArtifactReplicationRoutes(mux)
	r.registerUserRoutes(mux)
	r.registerErasureJobRoutes(mux)
	r.registerLegalHoldRoutes(mux)
	r.registerImpersonationRoutes(mux)
	r.registerBillingCorrectionRoutes(mux)
	r.registerExperimentRoutes(mux)
	r.registerEnvironmentRoutes(mux)
	r.registerPoolRoutes(mux)
	r.registerConnectorRoutes(mux)
	r.registerExternalAdapterRoutes(mux)
	r.registerDelegationPolicyRoutes(mux)
	r.registerInterceptorRoutes(mux)
	r.registerCustomRoleRoutes(mux)
	r.registerCredentialPoolRoutes(mux)
	r.registerCredentialRekeyRoutes(mux)
	r.registerTenantAccessRoutes(mux)
	r.registerBreakerRoutes(mux)
	r.registerCARotationRoutes(mux)
	r.registerRuntimeUpgradeRoutes(mux)
	r.registerLeaseDenialRoutes(mux)
	r.registerBootstrapRoutes(mux)
	r.registerAdminTokenRoutes(mux)
	r.registerTokenRevokerRoutes(mux)
	r.registerAuditRoutes(mux)
	r.registerPlatformRoutes(mux)
	r.registerPreflightRoutes(mux)
	r.registerMeRoutes(mux)
	// §25.1 enforcement point 1: the per-route scope gate runs before
	// the mux dispatches any handler, so a scope-narrowed token is
	// rejected with 403 SCOPE_FORBIDDEN before it reaches a destructive
	// admin handler at its full role ceiling. An absent scope claim, or
	// a route the document declares no scope for, defers to the role
	// gate on the matched handler. spec: §15.1 line 914,920; §25.1 line 94.
	return r.enforceScopes(mux)
}

// registerTenantRoutes registers the §15.1 tenant CRUD surface and the
// tenant-scoped sub-resources (force-delete, salt rotation, elicitation
// integrity, compliance decommission, deployment-config audit, suspend/
// resume, RBAC config, and runtime-capability overrides).
func (r *Router) registerTenantRoutes(mux *http.ServeMux) {
	if r.tenants == nil {
		return
	}
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

// registerRuntimeRoutes registers the §10.2 / §15.1 runtime CRUD surface.
func (r *Router) registerRuntimeRoutes(mux *http.ServeMux) {
	if r.runtimes == nil {
		return
	}
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

// registerRecommendationRoutes registers the §25.3 capacity-recommendation read.
func (r *Router) registerRecommendationRoutes(mux *http.ServeMux) {
	if r.recommendations == nil {
		return
	}
	mux.Handle("GET /v1/admin/recommendations",
		r.requireAdmin(http.HandlerFunc(r.handleRecommendations)))
}

// registerMigrationRoutes registers the §15.1 / §24.13 schema-migration
// management endpoints.
func (r *Router) registerMigrationRoutes(mux *http.ServeMux) {
	if r.migrations == nil {
		return
	}
	// §15.1 lines 891-892 / §24.13 lines 150-151: schema-migration
	// status read and last-resort down-migration. Both require
	// platform-admin.
	mux.Handle("GET /v1/admin/schema/migrations/status",
		r.requireAdmin(http.HandlerFunc(r.handleMigrationStatus)))
	mux.Handle("POST /v1/admin/schema/migrations/{version}/down",
		r.requireAdmin(http.HandlerFunc(r.handleMigrationDown)))
}

// registerEventBufferRoutes registers the §25.3 gateway-side event-buffer read.
func (r *Router) registerEventBufferRoutes(mux *http.ServeMux) {
	if r.eventBuffer == nil {
		return
	}
	mux.Handle("GET /v1/admin/events/buffer",
		r.requireAdmin(http.HandlerFunc(r.handleEventBuffer)))
}

// registerSessionAdminRoutes registers the §24.11 platform-admin
// session-investigation endpoints.
func (r *Router) registerSessionAdminRoutes(mux *http.ServeMux) {
	if r.sessionAdmin == nil {
		return
	}
	// §24.11 platform-admin session investigation: read-through and
	// the operator-driven forced terminal transition. Both resolve a
	// session by its global id and are platform-admin-only.
	mux.Handle("GET /v1/admin/sessions/{id}",
		r.requireAdmin(http.HandlerFunc(r.handleGetSession)))
	mux.Handle("POST /v1/admin/sessions/{id}/force-terminate",
		r.requireAdmin(http.HandlerFunc(r.handleForceTerminateSession)))
}

// registerQuotaRoutes registers the §15.1 / §24.6 quota-reconcile endpoint
// unconditionally so the `lenny-ctl admin quota reconcile` command always
// reaches a real endpoint.
func (r *Router) registerQuotaRoutes(mux *http.ServeMux) {
	// §15.1 line 879 / §24.6 line 99 — quota-counter re-aggregation. The
	// route is registered unconditionally so the `lenny-ctl admin quota
	// reconcile` command always reaches a real endpoint; the handler
	// answers 503 QUOTA_RECONCILE_UNAVAILABLE when the reconciler seam is
	// unwired (the default until the F-11.2.4 Postgres checkpoint lands).
	mux.Handle("POST /v1/admin/quota/reconcile",
		r.requireAdmin(http.HandlerFunc(r.handleQuotaReconcile)))
}

// registerArtifactReplicationRoutes registers the §25.11 ArtifactStore
// replication resume and status endpoints unconditionally.
func (r *Router) registerArtifactReplicationRoutes(mux *http.ServeMux) {
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
}

// registerUserRoutes registers the §15.1 user CRUD surface, the tenant-scoped
// user-listing and role-assignment routes, and the §12.8 GDPR user-erase route.
func (r *Router) registerUserRoutes(mux *http.ServeMux) {
	if r.users == nil {
		return
	}
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

// registerErasureJobRoutes registers the §12.8 / §24.12 erasure-job status,
// retry, and processing-restriction-clear endpoints.
func (r *Router) registerErasureJobRoutes(mux *http.ServeMux) {
	if r.erasureJobs == nil {
		return
	}
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

// registerLegalHoldRoutes registers the §15.1 / §10.2 legal-hold set/clear
// and active-hold listing endpoints.
func (r *Router) registerLegalHoldRoutes(mux *http.ServeMux) {
	if r.sessions == nil {
		return
	}
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

// registerImpersonationRoutes registers the §13.3 / §16.7 platform-admin
// impersonation flow.
func (r *Router) registerImpersonationRoutes(mux *http.ServeMux) {
	if r.impersonation == nil {
		return
	}
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

// registerBillingCorrectionRoutes registers the §11.2.1 operator-initiated
// billing-correction (Category 2) endpoints.
func (r *Router) registerBillingCorrectionRoutes(mux *http.ServeMux) {
	if r.corrections == nil || r.billing == nil {
		return
	}
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

// registerExperimentRoutes registers the §10.7 / §15.1 experiment admin CRUD
// and results-aggregation endpoints.
func (r *Router) registerExperimentRoutes(mux *http.ServeMux) {
	if r.experiments == nil {
		return
	}
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

// registerEnvironmentRoutes registers the §10.6 / §15.1 environment admin CRUD,
// runtime-exposure, usage-rollup, and tenant access-report endpoints.
func (r *Router) registerEnvironmentRoutes(mux *http.ServeMux) {
	if r.environments == nil {
		return
	}
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

// registerPoolRoutes registers the §10.2 / §15.1 pool CRUD surface and the
// §25.17 warm-count, circuit-breaker, drain, bootstrap-override, reconciliation,
// and sync-status sub-routes.
func (r *Router) registerPoolRoutes(mux *http.ServeMux) {
	if r.pools == nil {
		return
	}
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

// registerConnectorRoutes registers the §15.1 connector CRUD surface and the
// §9.3 test, refresh, and OAuth endpoints.
func (r *Router) registerConnectorRoutes(mux *http.ServeMux) {
	if r.connectors == nil {
		return
	}
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

// registerExternalAdapterRoutes registers the §15.1 / §24.8 external-protocol
// adapter CRUD and validate endpoints.
func (r *Router) registerExternalAdapterRoutes(mux *http.ServeMux) {
	if r.externalAdapters == nil {
		return
	}
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

// registerDelegationPolicyRoutes registers the §10.2 / §15.1 delegation-policy
// CRUD surface.
func (r *Router) registerDelegationPolicyRoutes(mux *http.ServeMux) {
	if r.delegationPolicies == nil {
		return
	}
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

// registerInterceptorRoutes registers the §4.8 / §15.1 external-interceptor
// registry CRUD surface.
func (r *Router) registerInterceptorRoutes(mux *http.ServeMux) {
	if r.interceptors == nil {
		return
	}
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

// registerCustomRoleRoutes registers the §10.2 tenant custom-role CRUD surface.
func (r *Router) registerCustomRoleRoutes(mux *http.ServeMux) {
	if r.customRoles == nil {
		return
	}
	// §10.2: custom roles are stored in the tenant RBAC config, so
	// the routes are gated on the manage_rbac_config permission.
	rbacAdmin := r.requirePermission(auth.PermManageRBACConfig)
	mux.Handle("POST /v1/admin/tenants/{id}/roles", rbacAdmin(http.HandlerFunc(r.handleCreateCustomRole)))
	mux.Handle("GET /v1/admin/tenants/{id}/roles", rbacAdmin(http.HandlerFunc(r.handleListCustomRoles)))
	mux.Handle("GET /v1/admin/tenants/{id}/roles/{name}", rbacAdmin(http.HandlerFunc(r.handleGetCustomRole)))
	mux.Handle("PUT /v1/admin/tenants/{id}/roles/{name}", rbacAdmin(http.HandlerFunc(r.handleUpdateCustomRole)))
	mux.Handle("DELETE /v1/admin/tenants/{id}/roles/{name}", rbacAdmin(http.HandlerFunc(r.handleDeleteCustomRole)))
}

// registerCredentialPoolRoutes registers the §15.1 credential-pool admin CRUD,
// the §24.5 per-credential subresource CRUD, and the §4.9 emergency-revocation
// endpoints.
func (r *Router) registerCredentialPoolRoutes(mux *http.ServeMux) {
	if r.credentialPools == nil {
		return
	}
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

// registerCredentialRekeyRoutes registers the §4.9.1 KMS-key-rotation
// re-encryption endpoints.
func (r *Router) registerCredentialRekeyRoutes(mux *http.ServeMux) {
	if r.credentialRekey == nil {
		return
	}
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

// registerTenantAccessRoutes registers the runtime and pool tenant-access
// grant/list/revoke endpoints.
func (r *Router) registerTenantAccessRoutes(mux *http.ServeMux) {
	if r.tenantAccess == nil {
		return
	}
	mux.Handle("POST /v1/admin/runtimes/{name}/tenant-access", r.requireAdmin(r.grantAccessHandler(tenantaccessstore.KindRuntime)))
	mux.Handle("GET /v1/admin/runtimes/{name}/tenant-access", r.requireAdmin(r.listAccessHandler(tenantaccessstore.KindRuntime)))
	mux.Handle("DELETE /v1/admin/runtimes/{name}/tenant-access/{tenantId}", r.requireAdmin(r.revokeAccessHandler(tenantaccessstore.KindRuntime)))
	mux.Handle("POST /v1/admin/pools/{name}/tenant-access", r.requireAdmin(r.grantAccessHandler(tenantaccessstore.KindPool)))
	mux.Handle("GET /v1/admin/pools/{name}/tenant-access", r.requireAdmin(r.listAccessHandler(tenantaccessstore.KindPool)))
	mux.Handle("DELETE /v1/admin/pools/{name}/tenant-access/{tenantId}", r.requireAdmin(r.revokeAccessHandler(tenantaccessstore.KindPool)))
}

// registerBreakerRoutes registers the §15.1 circuit-breaker inspection and
// manual-override endpoints.
func (r *Router) registerBreakerRoutes(mux *http.ServeMux) {
	if r.breakers == nil {
		return
	}
	mux.Handle("GET /v1/admin/circuit-breakers", r.requireAdmin(http.HandlerFunc(r.handleListBreakers)))
	mux.Handle("GET /v1/admin/circuit-breakers/{name}", r.requireAdmin(http.HandlerFunc(r.handleGetBreaker)))
	mux.Handle("POST /v1/admin/circuit-breakers/{name}/open", r.requireAdmin(http.HandlerFunc(r.handleOpenBreaker)))
	mux.Handle("POST /v1/admin/circuit-breakers/{name}/close", r.requireAdmin(http.HandlerFunc(r.handleCloseBreaker)))
}

// registerCARotationRoutes registers the §10.3 cluster-internal CA-rotation
// state-machine endpoints.
func (r *Router) registerCARotationRoutes(mux *http.ServeMux) {
	if r.caRotation == nil {
		return
	}
	// §10.3 lines 344-350 — operator-driven cluster-internal CA
	// rotation. The procedure is platform-global, so every route is
	// platform-admin-only. F-10.3.21.
	mux.Handle("GET /v1/admin/ca-rotation", r.requireAdmin(http.HandlerFunc(r.handleGetCARotation)))
	mux.Handle("POST /v1/admin/ca-rotation/begin", r.requireAdmin(http.HandlerFunc(r.handleBeginCARotation)))
	mux.Handle("POST /v1/admin/ca-rotation/promote", r.requireAdmin(http.HandlerFunc(r.handlePromoteCARotation)))
	mux.Handle("POST /v1/admin/ca-rotation/retire", r.requireAdmin(http.HandlerFunc(r.handleRetireCARotation)))
}

// registerRuntimeUpgradeRoutes registers the §10.5 / §15.1 pool runtime-image
// rollout state-machine endpoints.
func (r *Router) registerRuntimeUpgradeRoutes(mux *http.ServeMux) {
	if r.runtimeUpgrade == nil {
		return
	}
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

// registerLeaseDenialRoutes registers the §15.1 / §8.6 extension-denial-clear
// endpoint.
func (r *Router) registerLeaseDenialRoutes(mux *http.ServeMux) {
	if r.leaseDenials == nil {
		return
	}
	// §15.1 line 868 — clear the §8.6 extension-denied flag on a
	// subtree, bypassing the rejection cool-off window. Requires
	// platform-admin or tenant-admin.
	mux.Handle("DELETE /v1/admin/trees/{rootSessionId}/subtrees/{sessionId}/extension-denial",
		r.requireTenantResourceAdmin(http.HandlerFunc(r.handleClearExtensionDenial)))
}

// registerBootstrapRoutes registers the §17.6 platform-bootstrap endpoint when
// any of its backing stores is wired.
func (r *Router) registerBootstrapRoutes(mux *http.ServeMux) {
	if r.tenants == nil && r.runtimes == nil && r.users == nil {
		return
	}
	mux.Handle("POST /v1/admin/bootstrap", r.requireAdmin(http.HandlerFunc(r.handleBootstrap)))
}

// registerAdminTokenRoutes registers the §17.6 initial-admin-token rotation
// endpoint.
func (r *Router) registerAdminTokenRoutes(mux *http.ServeMux) {
	if r.adminToken == nil {
		return
	}
	// §17.6 — rotate the initial admin token. platform-admin
	// only (requireAdmin). F-17.6.3.
	mux.Handle("POST /v1/admin/users/{user}/rotate-token",
		r.requireAdmin(http.HandlerFunc(r.handleRotateToken)))
}

// registerTokenRevokerRoutes registers the §13.3 operator-initiated
// token-revocation endpoint.
func (r *Router) registerTokenRevokerRoutes(mux *http.ServeMux) {
	if r.tokenRevoker == nil {
		return
	}
	// §13.3 operator-initiated token revocation.
	mux.Handle("POST /v1/admin/issued-tokens/{jti}/revoke",
		r.requireAdmin(http.HandlerFunc(r.handleRevokeToken)))
}

// registerAuditRoutes registers the §25.9 Audit Log Query API, including the
// summary, list, get, retranslate, republish, and partition-drop endpoints.
func (r *Router) registerAuditRoutes(mux *http.ServeMux) {
	if r.auditLog == nil {
		return
	}
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

// registerPlatformRoutes registers the §25.3 platform-introspection endpoints.
func (r *Router) registerPlatformRoutes(mux *http.ServeMux) {
	if !r.platformWired {
		return
	}
	// §25.3 platform introspection. Version + config are
	// platform-admin gated (config can carry sensitive
	// operational detail even with secrets redacted).
	mux.Handle("GET /v1/admin/platform/version", r.requireAdmin(http.HandlerFunc(r.handlePlatformVersion)))
	mux.Handle("GET /v1/admin/platform/config", r.requireAdmin(http.HandlerFunc(r.handlePlatformConfig)))
}

// registerPreflightRoutes registers the §15.1 / §24.2 API-backed preflight
// endpoint.
func (r *Router) registerPreflightRoutes(mux *http.ServeMux) {
	if r.preflighter == nil {
		return
	}
	// §15.1 line 890 / §24.2 — API-backed mode of `lenny-ctl
	// preflight`: active outbound Postgres/Redis/MinIO connectivity
	// and schema-version probes against the gateway's configured
	// backends. POST because it performs side-effecting outbound
	// dials. Reserved to platform-admin.
	mux.Handle("POST /v1/admin/preflight", r.requireAdmin(http.HandlerFunc(r.handlePreflight)))
}

// registerMeRoutes registers the §25.4 self-introspection endpoints, available
// to any authenticated caller with no role gate.
func (r *Router) registerMeRoutes(mux *http.ServeMux) {
	// §25.4 self-introspection — available to any authenticated
	// caller, no role gate. Returns the calling principal's identity
	// + role grants so a freshly-onboarded admin agent can discover
	// what operations it is permitted to invoke.
	mux.HandleFunc("GET /v1/admin/me", r.handleMe)
	mux.HandleFunc("GET /v1/admin/me/authorized-tools", r.handleAuthorizedTools)
}
