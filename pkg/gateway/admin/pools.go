// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/lennylabs/lenny/pkg/elicitation"
	"github.com/lennylabs/lenny/pkg/gateway/billingfanout"
	"github.com/lennylabs/lenny/pkg/gateway/delegationpolicystore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/pagination"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantaccessstore"
	"github.com/lennylabs/lenny/pkg/observability/audit"
	"github.com/lennylabs/lenny/pkg/sandbox/egress"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// ReconciliationResumer clears the PoolScalingController's in-memory
// admission-denial backoff for one pool, implementing §4.6.2 item 3
// condition (c): an operator-initiated reset of a stuck pool's denial
// counter that does not require a Postgres configuration change. The
// PSC's Reconciler satisfies it through a namespace-binding adapter
// (the admin API addresses pools by name; the PSC keys them by agent
// namespace). The returned count is the number of CRD tuples cleared,
// zero when the pool was not stuck.
type ReconciliationResumer interface {
	ResumePoolReconciliation(ctx context.Context, poolName string) (cleared int, err error)
}

// PoolStatusReader reads a pool's live §5.2 bootstrap status from its
// Kubernetes CRD pair so the admin pool GET can surface poolCondition
// and idlePodCount (§5.2 line 629) without operators inspecting the CR
// status directly. condition is "PoolWarmingUp" while the pool is
// bootstrapping and the empty string otherwise; idlePodCount is the
// ready-pod count. found is false when no SandboxWarmPool exists for
// the pool yet, so the handler omits the live-status fields.
// The gateway satisfies it with a podsession.PoolStatusLookup.
type PoolStatusReader interface {
	PoolStatus(ctx context.Context, poolName string) (condition string, idlePodCount int, found bool, err error)
}

// PoolBootstrapStatusReader reads a pool's §17.8.2 cold-start
// convergence signals from its SandboxWarmPool CRD status and the
// PoolScalingController's demand window: how many hours of traffic data
// have accumulated and which scaling mode the controller currently
// operates the pool in (`bootstrap` or `formula`). The admin pool GET
// uses it to populate the bootstrapStatus object's hoursOfData and
// estimatedConvergenceAt. found is false when no SandboxWarmPool exists
// for the pool yet, so the handler reports the override-only view.
// spec: §17.8.2 step 3.
type PoolBootstrapStatusReader interface {
	PoolBootstrapStatus(ctx context.Context, poolName string) (hoursOfData float64, scalingMode string, found bool, err error)
}

// BootstrapStatusPayload is the §17.8.2 step-3 bootstrapStatus object on
// the admin pool GET. active reports whether the pool is operating under
// its bootstrapMinWarm override (the controller has not converged to
// formula-driven scaling); bootstrapMinWarm echoes the override value;
// hoursOfData and estimatedConvergenceAt are populated when a
// PoolBootstrapStatusReader is wired. spec: §17.8.2 step 3.
type BootstrapStatusPayload struct {
	Active                 bool    `json:"active"`
	BootstrapMinWarm       int     `json:"bootstrapMinWarm"`
	HoursOfData            float64 `json:"hoursOfData,omitempty"`
	EstimatedConvergenceAt string  `json:"estimatedConvergenceAt,omitempty"`
}

// CRDGenerationReader reports the pool's CRD-side pool_config_generation
// (read from the lenny.dev/config-generation annotation the
// PoolScalingController stamps in §4.6.2 line 558). It backs the
// §4.6.2 line 560 sync-status endpoint and the PUT response's
// syncStatus field. A nil reader leaves the CRD generation unknown,
// so the handler omits it and reports syncStatus="unknown". spec:
// spec/04_system-components.md lines 557-560.
type CRDGenerationReader interface {
	// CRDGeneration returns the generation observed on the pool's
	// SandboxTemplate annotation, the last successful reconciliation
	// instant, and ok = true when the CRD pair exists. ok = false
	// when no SandboxTemplate has been created for the pool yet.
	CRDGeneration(ctx context.Context, poolName string) (generation int64, lastReconciledAt time.Time, ok bool, err error)
}

// PoolPayload is the §15.1 admin-pool wire shape.
type PoolPayload struct {
	Name                   string `json:"name"`
	RuntimeRef             string `json:"runtimeRef,omitempty"`
	IsolationProfile       string `json:"isolationProfile,omitempty"`
	ExecutionMode          string `json:"executionMode,omitempty"`
	ResourceClass          string `json:"resourceClass,omitempty"`
	WarmCount              int    `json:"warmCount,omitempty"`
	MaxSessionAgeSeconds   int    `json:"maxSessionAgeSeconds,omitempty"`
	AllowStandardIsolation bool   `json:"allowStandardIsolation,omitempty"`

	// EgressProfile is the §13.2 per-pool egress profile (`restricted`,
	// `provider-direct`, `internet`). Empty on create resolves to the
	// §13.2 default (`restricted`).
	EgressProfile string `json:"egressProfile,omitempty"`

	// MaxConcurrent is the §5.2 service-mode per-pod request capacity. It
	// is meaningful only on a `service`-mode pool; the pool store's
	// ValidateServiceConfig rejects it on any other pool. The session-mode
	// concurrency bound lives on SessionPolicy.MaxConcurrentSessions.
	MaxConcurrent int `json:"maxConcurrent,omitempty"`

	// PoolCondition and IdlePodCount are the §5.2 line 629 live
	// bootstrap-status fields, populated on GET when the gateway is
	// wired with a PoolStatusReader and the pool's SandboxWarmPool
	// exists. PoolCondition is "PoolWarmingUp" during the bootstrap
	// window. They are pointers so a legitimate idlePodCount of 0 is
	// emitted while an unwired reader or an unreconciled pool omits both.
	PoolCondition *string `json:"poolCondition,omitempty"`
	IdlePodCount  *int    `json:"idlePodCount,omitempty"`

	// SyncStatus is the §4.6.2 line 559 reconciliation-status flag.
	// "synced" means the CRD generation matches the Postgres
	// generation; "pending" means the controller has not yet observed
	// the latest write; "unknown" means no CRD reader is wired.
	// spec: spec/04_system-components.md line 559.
	SyncStatus string `json:"syncStatus,omitempty"`

	// Phase is the §15.1 line 797 pool lifecycle phase: "active" or
	// "draining". A pool reports "draining" after
	// POST /v1/admin/pools/{name}/drain until its in-flight sessions
	// complete. spec: §15.1 line 797.
	Phase string `json:"phase,omitempty"`

	// ActiveSessions is the count of live (non-terminal) sessions bound
	// to the pool. It is populated on GET while the pool is draining so
	// operators can watch the drain converge; it is a pointer so a
	// legitimate count of 0 (drain complete) is emitted distinctly from
	// an active pool that omits the field. spec: §15.1 line 797.
	ActiveSessions *int `json:"activeSessions,omitempty"`

	// ETag is the §15.1 optimistic-concurrency entity tag — the quoted
	// decimal pool_config_generation. List and GET responses carry it so
	// a client can supply it as the If-Match header on a later PUT.
	// spec: §15.1 lines 1207-1209.
	ETag string `json:"etag,omitempty"`

	// BootstrapStatus is the §17.8.2 step-3 cold-start bootstrap status
	// object. It is populated on GET for a pool that carries a
	// bootstrapMinWarm override; pools without one omit it. spec: §17.8.2
	// step 3.
	BootstrapStatus *BootstrapStatusPayload `json:"bootstrapStatus,omitempty"`

	// SessionPolicy is the §5.2 session-mode sessionPolicy block: the
	// pod-reuse, concurrency, and workspace-cleanup configuration
	// (maxConcurrentSessions, recycle.*, the cleanup and exhaustion
	// knobs). It is meaningful only on a `session`-mode pool; the
	// gateway-side ValidateSessionPolicy and ValidateServiceConfig enforce
	// that direction. spec: §5.2 (sessionPolicy block).
	SessionPolicy *runtimestore.SessionPolicy `json:"sessionPolicy,omitempty"`

	// ElicitationDepthPolicy is the §9.2 line 90-98 per-pool depth
	// policy (`allow_all`, `suppress_at_depth`, `block_all`) for
	// agent-initiated elicitations raised by sessions in this pool. An
	// omitted value resolves to the §9.2 line 92 platform default
	// (suppress at depth 3). spec: §9.2 lines 90-98.
	ElicitationDepthPolicy string `json:"elicitationDepthPolicy,omitempty"`

	// ElicitationSuppressAtDepth is the §9.2 line 92 threshold N for the
	// `suppress_at_depth` policy; ignored for the other policies.
	ElicitationSuppressAtDepth int `json:"elicitationSuppressAtDepth,omitempty"`

	// URLModeElicitation is the §9.2 line 86 per-pool agent-initiated
	// url-mode elicitation allowlist. spec: §9.2 line 86.
	URLModeElicitation *URLModeElicitationPayload `json:"urlModeElicitation,omitempty"`

	// SDKWarm is the §6.1 SDK-warm circuit-breaker operability block:
	// the operator `circuitBreakerOverride` (line 63) and the
	// `acknowledgeHighDemotionRate` flag (line 48). It is populated on GET
	// when the pool carries either setting. The override is set via the
	// dedicated PUT /v1/admin/pools/{name}/circuit-breaker endpoint;
	// acknowledgeHighDemotionRate is set via the main PUT. spec: §6.1
	// lines 48, 63-65.
	SDKWarm *SDKWarmPayload `json:"sdkWarm,omitempty"`

	CreatedAt string `json:"createdAt,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
	DeletedAt string `json:"deletedAt,omitempty"`
}

// SDKWarmPayload is the §6.1 `sdkWarm` block on the admin wire:
// `{"circuitBreakerOverride": "enabled"|"disabled"|"auto",
// "acknowledgeHighDemotionRate": bool, "demotionRateThreshold": 0.6}`.
// spec: §6.1 lines 48, 63-65, §15.1 line 801.
type SDKWarmPayload struct {
	CircuitBreakerOverride      string   `json:"circuitBreakerOverride,omitempty"`
	AcknowledgeHighDemotionRate bool     `json:"acknowledgeHighDemotionRate,omitempty"`
	DemotionRateThreshold       *float64 `json:"demotionRateThreshold,omitempty"`
}

// URLModeElicitationPayload is the §9.2 line 86 per-pool
// `urlModeElicitation` block on the admin wire:
// `{"enabled": bool, "domainAllowlist": ["accounts.example.com"]}`.
// Setting enabled:true with an empty domainAllowlist is rejected with
// 400 URL_MODE_ELICITATION_DOMAIN_REQUIRED. spec: §9.2 line 86.
type URLModeElicitationPayload struct {
	Enabled         bool     `json:"enabled,omitempty"`
	DomainAllowlist []string `json:"domainAllowlist,omitempty"`
}

// PoolSyncStatus is the §15.1 GET /v1/admin/pools/{name}/sync-status
// payload. spec: spec/04_system-components.md line 560.
type PoolSyncStatus struct {
	Pool               string `json:"pool"`
	PostgresGeneration int64  `json:"postgresGeneration"`
	CRDGeneration      int64  `json:"crdGeneration"`
	LastReconciledAt   string `json:"lastReconciledAt,omitempty"`
	LagSeconds         int64  `json:"lagSeconds"`
	InSync             bool   `json:"inSync"`
}

// UpdatePoolRequest is the §15.1 PUT body.
type UpdatePoolRequest struct {
	RuntimeRef             *string                     `json:"runtimeRef,omitempty"`
	IsolationProfile       *string                     `json:"isolationProfile,omitempty"`
	ExecutionMode          *string                     `json:"executionMode,omitempty"`
	ResourceClass          *string                     `json:"resourceClass,omitempty"`
	WarmCount              *int                        `json:"warmCount,omitempty"`
	BootstrapMinWarm       *int                        `json:"bootstrapMinWarm,omitempty"`
	MaxSessionAgeSeconds   *int                        `json:"maxSessionAgeSeconds,omitempty"`
	AllowStandardIsolation *bool                       `json:"allowStandardIsolation,omitempty"`
	EgressProfile          *string                     `json:"egressProfile,omitempty"`
	MaxConcurrent          *int                        `json:"maxConcurrent,omitempty"`
	SessionPolicy          *runtimestore.SessionPolicy `json:"sessionPolicy,omitempty"`
	// ClearSessionPolicy, when true, removes the persisted sessionPolicy
	// block in the same PUT. A non-nil SessionPolicy with
	// ClearSessionPolicy set is rejected (the two operations are mutually
	// exclusive).
	ClearSessionPolicy bool `json:"clearSessionPolicy,omitempty"`

	// ElicitationDepthPolicy / ElicitationSuppressAtDepth / URLModeElicitation
	// are the §9.2 per-pool elicitation policy fields. spec: §9.2 lines 86,
	// 90-98.
	ElicitationDepthPolicy     *string                    `json:"elicitationDepthPolicy,omitempty"`
	ElicitationSuppressAtDepth *int                       `json:"elicitationSuppressAtDepth,omitempty"`
	URLModeElicitation         *URLModeElicitationPayload `json:"urlModeElicitation,omitempty"`

	// SDKWarm carries the §6.1 SDK-warm operability fields. The main PUT
	// honors `acknowledgeHighDemotionRate` (line 48); the
	// `circuitBreakerOverride` is set via the dedicated
	// PUT /v1/admin/pools/{name}/circuit-breaker endpoint (line 63) which
	// enforces the non-SDK-warm-pool 409 and emits the override audit
	// event, so the override field here is ignored by the main PUT.
	SDKWarm *SDKWarmPayload `json:"sdkWarm,omitempty"`
}

func fromPool(p poolstore.Pool) PoolPayload {
	out := PoolPayload{
		Name:                   p.Name,
		RuntimeRef:             p.RuntimeRef,
		IsolationProfile:       string(p.IsolationProfile),
		ExecutionMode:          string(p.ExecutionMode),
		ResourceClass:          p.ResourceClass,
		WarmCount:              p.WarmCount,
		MaxSessionAgeSeconds:   p.MaxSessionAgeSeconds,
		AllowStandardIsolation: p.AllowStandardIsolation,
		EgressProfile:          string(p.EgressProfile),
		MaxConcurrent:          p.MaxConcurrent,
		SessionPolicy:          p.SessionPolicy.Clone(),
		CreatedAt:              rfc3339Nano(p.CreatedAt),
		UpdatedAt:              rfc3339Nano(p.UpdatedAt),
		DeletedAt:              rfc3339Nano(p.DeletedAt),
		// spec: §15.1 line 1207 — the ETag is the quoted decimal
		// pool_config_generation (the per-resource version column).
		ETag: formatETag(p.Generation),
		// spec: §15.1 line 797 — GET surfaces the pool lifecycle phase so
		// a client can see a drain in progress.
		Phase: p.Phase(),
	}
	out.ElicitationDepthPolicy = string(p.ElicitationDepthPolicy)
	out.ElicitationSuppressAtDepth = p.ElicitationSuppressAtDepth
	if p.URLModeElicitation.Enabled || len(p.URLModeElicitation.DomainAllowlist) > 0 {
		out.URLModeElicitation = &URLModeElicitationPayload{
			Enabled:         p.URLModeElicitation.Enabled,
			DomainAllowlist: append([]string(nil), p.URLModeElicitation.DomainAllowlist...),
		}
	}
	if p.SDKWarmCircuitBreakerOverride != poolstore.SDKWarmOverrideUnset || p.AcknowledgeHighDemotionRate || p.DemotionRateThreshold != nil {
		out.SDKWarm = &SDKWarmPayload{
			CircuitBreakerOverride:      string(p.SDKWarmCircuitBreakerOverride),
			AcknowledgeHighDemotionRate: p.AcknowledgeHighDemotionRate,
			DemotionRateThreshold:       p.DemotionRateThreshold,
		}
	}
	return out
}

// urlModeFromWire maps the §9.2 url-mode allowlist payload to the store
// value. A nil payload reads as the zero value (agent-initiated url-mode
// blocked, the §9.2 default). spec: §9.2 line 86.
func urlModeFromWire(in *URLModeElicitationPayload) elicitation.URLModeAllowlist {
	if in == nil {
		return elicitation.URLModeAllowlist{}
	}
	return elicitation.URLModeAllowlist{
		Enabled:         in.Enabled,
		DomainAllowlist: append([]string(nil), in.DomainAllowlist...),
	}
}

// poolFromPayload builds a poolstore.Pool from the admin wire payload,
// applying the §13.2 egress default, the §5.3 line 677 dev-mode
// isolation default, and the §5.2 executionMode default. It is the
// shared create-side build used by the POST /v1/admin/pools handler and
// the §17.6 bootstrap pool seed so both paths resolve identical
// defaults. CreatedAt/UpdatedAt are stamped from the Router clock.
func (r *Router) poolFromPayload(body PoolPayload) poolstore.Pool {
	pl := poolstore.Pool{
		Name:                       body.Name,
		RuntimeRef:                 body.RuntimeRef,
		IsolationProfile:           isolation.Profile(body.IsolationProfile),
		ExecutionMode:              runtimestore.ExecutionMode(body.ExecutionMode),
		MaxConcurrent:              body.MaxConcurrent,
		SessionPolicy:              body.SessionPolicy.Clone(),
		ResourceClass:              body.ResourceClass,
		WarmCount:                  body.WarmCount,
		MaxSessionAgeSeconds:       body.MaxSessionAgeSeconds,
		AllowStandardIsolation:     body.AllowStandardIsolation,
		EgressProfile:              egress.Profile(body.EgressProfile),
		ElicitationDepthPolicy:     elicitation.DepthPolicy(body.ElicitationDepthPolicy),
		ElicitationSuppressAtDepth: body.ElicitationSuppressAtDepth,
		URLModeElicitation:         urlModeFromWire(body.URLModeElicitation),
		CreatedAt:                  r.clock(),
	}
	pl.UpdatedAt = pl.CreatedAt
	// spec: §6.1 lines 48, 63-65 — carry the SDK-warm operability block
	// onto a created pool so a bootstrap seed or POST can set the override,
	// the demotion-rate acknowledgment, and the demotion-rate threshold up
	// front.
	if body.SDKWarm != nil {
		pl.SDKWarmCircuitBreakerOverride = poolstore.SDKWarmCircuitBreakerOverride(body.SDKWarm.CircuitBreakerOverride)
		pl.AcknowledgeHighDemotionRate = body.SDKWarm.AcknowledgeHighDemotionRate
		pl.DemotionRateThreshold = body.SDKWarm.DemotionRateThreshold
	}
	if pl.EgressProfile == "" {
		// spec: §13.2 — an omitted egress profile resolves to the
		// narrowest egress (`restricted`), so the stored record reflects
		// the resolved profile rather than an ambiguous empty value.
		pl.EgressProfile = egress.Default()
	}
	if pl.IsolationProfile == "" {
		// spec: §5.3 line 677 — in dev mode the default isolation profile
		// falls back to `standard` (runc) so a developer can launch pods on
		// a cluster without gVisor. A standard-isolation pool requires the
		// explicit allowStandardIsolation opt-in; dev mode supplies it on
		// the operator's behalf for the default fallback (the warning is
		// logged once at gateway startup).
		pl.IsolationProfile = isolation.DefaultForMode(r.devMode)
		if r.devMode && pl.IsolationProfile == isolation.ProfileStandard {
			pl.AllowStandardIsolation = true
		}
	}
	if pl.ExecutionMode == "" {
		pl.ExecutionMode = runtimestore.ExecutionModeSession
	}
	return pl
}

// validatePoolForStore runs the §5.2 / §5.3 / §13.2 store-side pool
// invariants that poolstore.Create enforces, so the §15.1 dry-run create
// preview rejects exactly what a persisted create would. It mirrors the
// inline checks in poolstore.Memory.Create plus the exported validators.
func validatePoolForStore(p poolstore.Pool) error {
	if err := poolstore.ValidateName(p.Name); err != nil {
		return err
	}
	if p.WarmCount < 0 {
		return errors.New("poolstore: warmCount must be >= 0")
	}
	if p.MaxSessionAgeSeconds < 0 {
		return errors.New("poolstore: maxSessionAgeSeconds must be >= 0")
	}
	if p.IsolationProfile == isolation.ProfileStandard && !p.AllowStandardIsolation {
		return errors.New("poolstore: isolationProfile=standard requires allowStandardIsolation=true (§5.3)")
	}
	if err := poolstore.ValidateEgressIsolation(p); err != nil {
		return err
	}
	if err := poolstore.ValidateServiceConfig(p); err != nil {
		return err
	}
	if err := poolstore.ValidateSessionPolicy(p); err != nil {
		return err
	}
	return poolstore.ValidateElicitationPolicy(p)
}

// WithPools wires the §15.1 pool CRUD handlers onto the Router.
func (r *Router) WithPools(s poolstore.Store) *Router {
	r.pools = s
	return r
}

// WithReconciliationResumer wires an in-process §4.6.2 resumer onto the
// Router. It is an optional override for the single-process / test
// deployment, where the PoolScalingController's denial tracker is
// reachable directly and the resume can clear the counter synchronously.
// The POST /v1/admin/pools/{name}/resume-reconciliation route is
// registered regardless: in the production split deployment (no
// in-process resumer) the handler bumps the durable
// reconciliation_resume_epoch the PSC observes on its next tick.
func (r *Router) WithReconciliationResumer(rr ReconciliationResumer) *Router {
	r.reconciliationResumer = rr
	return r
}

// WithPoolStatusReader wires the §5.2 line 629 live pool-status lookup
// onto the Router. Without it the admin pool GET omits poolCondition
// and idlePodCount (the gateway has no Kubernetes client to read CRD
// status, e.g. the minimal Postgres-only dev posture).
func (r *Router) WithPoolStatusReader(rdr PoolStatusReader) *Router {
	r.poolStatus = rdr
	return r
}

// WithPoolBootstrapStatusReader wires the §17.8.2 step-3 cold-start
// status source onto the Router. Without it the admin pool GET reports
// the override-only bootstrapStatus view (active + bootstrapMinWarm)
// and omits hoursOfData / estimatedConvergenceAt.
func (r *Router) WithPoolBootstrapStatusReader(rdr PoolBootstrapStatusReader) *Router {
	r.poolBootstrap = rdr
	return r
}

// WithCRDGenerationReader wires the §4.6.2 line 559/560 sync-status
// data source. Without it the admin pool GET / PUT report
// syncStatus="unknown" and the sync-status endpoint reports
// crdGeneration=0 inSync=false — the §6.0 Postgres-only dev posture.
func (r *Router) WithCRDGenerationReader(rdr CRDGenerationReader) *Router {
	r.crdGenerations = rdr
	return r
}

func (p PoolPayload) validateEnums() error {
	if p.IsolationProfile != "" && !isolation.IsValid(isolation.Profile(p.IsolationProfile)) {
		return errors.New("isolationProfile is not a recognised §5.3 profile")
	}
	if p.ExecutionMode != "" && !runtimestore.ExecutionMode(p.ExecutionMode).IsValid() {
		return errors.New("executionMode is not a recognised mode")
	}
	if p.EgressProfile != "" && !egress.IsValid(egress.Profile(p.EgressProfile)) {
		return errors.New("egressProfile is not a recognised §13.2 profile (restricted, provider-direct, internet)")
	}
	if p.ElicitationDepthPolicy != "" && !elicitation.DepthPolicy(p.ElicitationDepthPolicy).IsValid() {
		return errors.New("elicitationDepthPolicy is not a recognised §9.2 policy (allow_all, suppress_at_depth, block_all)")
	}
	return nil
}

func (r *Router) handleCreatePool(w http.ResponseWriter, req *http.Request) {
	var body PoolPayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	if err := poolstore.ValidateName(body.Name); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(),
			map[string]any{"field": "name"})
		return
	}
	if err := body.validateEnums(); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	// Cross-resource consistency: if the runtimes store is wired,
	// reject pools that reference a non-existent runtime so
	// misconfigurations surface at admin time rather than at session
	// creation. Capture the runtime's §12.9 workspace tier for the §5.2
	// line 396 T4 cross-tenant-reuse check below.
	var runtimeTier runtimestore.WorkspaceTier
	var runtimePreConnect bool
	if r.runtimes != nil && body.RuntimeRef != "" {
		rt, err := r.runtimes.Get(req.Context(), body.RuntimeRef)
		if err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"runtimeRef does not resolve to a registered runtime",
				map[string]any{"runtimeRef": body.RuntimeRef})
			return
		}
		runtimeTier = rt.WorkspaceTier
		runtimePreConnect = poolstore.RuntimePreConnect(rt)
	}

	pl := r.poolFromPayload(body)
	// spec: §5.2 line 430, §6.1 lines 77-78 — reject an SDK-warm runtime
	// (preConnect: true) bound to a service-mode pool or a concurrent
	// session-mode pool (maxConcurrentSessions > 1) before storage;
	// SDK-warm assumes a single pre-connected agent process.
	if err := poolstore.ValidatePreConnectExecutionMode(runtimePreConnect, pl.ExecutionMode, pl.SessionPolicy); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(),
			map[string]any{"runtimeRef": body.RuntimeRef})
		return
	}
	// spec: §5.2 line 396 — reject cross-tenant reuse on a pool whose
	// runtime is T4 before storage, so a misconfigured pool never enters
	// the registry.
	if err := poolstore.ValidateCrossTenantReuseTier(pl, runtimeTier); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(),
			map[string]any{"runtimeRef": body.RuntimeRef, "workspaceTier": string(runtimeTier)})
		return
	}
	// spec: §15.1 line 1140 — ?dryRun=true validates without persisting or auditing.
	// The remaining store-side validations (the checks Create would run)
	// run here so the dry run is exhaustive; the preview carries the
	// generation a persisted create would stamp (the §15.1 line 1207 ETag).
	if req.URL.Query().Get("dryRun") == "true" {
		if perr := validatePoolForStore(pl); perr != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", perr.Error(), nil)
			return
		}
		if pl.Generation == 0 {
			pl.Generation = 1
		}
		writeDryRun(w, http.StatusCreated, fromPool(pl))
		return
	}
	if err := r.pools.Create(req.Context(), pl); err != nil {
		if errors.Is(err, poolstore.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "RESOURCE_CONFLICT",
				"pool with this name already exists", nil)
			return
		}
		if errors.Is(err, poolstore.ErrURLModeDomainRequired) {
			// spec: §9.2 line 86 — a pool that enables agent-initiated
			// url-mode elicitation must name the permitted domains.
			writeError(w, http.StatusBadRequest, "URL_MODE_ELICITATION_DOMAIN_REQUIRED", err.Error(),
				map[string]any{"field": "urlModeElicitation.domainAllowlist"})
			return
		}
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	stored, _ := r.pools.Get(req.Context(), body.Name)
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	r.emit(req.Context(), principal, "admin.pool.created", body.Name, map[string]any{
		"runtimeRef":       stored.RuntimeRef,
		"isolationProfile": string(stored.IsolationProfile),
		"executionMode":    string(stored.ExecutionMode),
	})
	r.maybeEmitWeakIsolation(req.Context(), principal, stored)
	r.dispatchPoolIsolationAudit(req.Context(), principal, stored.Name)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(fromPool(stored))
}

// maybeEmitWeakIsolation emits the §5.3 DirectModeWeakIsolation warning
// audit event when a pool runs under `standard` (runc) isolation via the
// explicit `allowStandardIsolation: true` opt-in. The opt-in is the
// deliberately-weak-isolation posture the §5.3 security note targets and
// §4.9 line 1489 requires a warning signal for; the standard
// `admin.pool.created` / `admin.pool.updated` event does not distinguish
// it from a sandboxed pool, so this companion event gives compliance
// teams an audit-trail signal that a runc pool was admitted. A pool that
// is not runc, or one rejected before storage, emits nothing.
func (r *Router) maybeEmitWeakIsolation(ctx context.Context, p authmw.Principal, pool poolstore.Pool) {
	if pool.IsolationProfile != isolation.ProfileStandard || !pool.AllowStandardIsolation {
		return
	}
	r.emit(ctx, p, "pool.direct_mode_weak_isolation", pool.Name, map[string]any{
		"isolationProfile":       string(pool.IsolationProfile),
		"allowStandardIsolation": true,
		"egressProfile":          string(pool.EgressProfile),
		"severity":               "warning",
		"reason":                 "pool admitted under standard (runc) isolation via explicit allowStandardIsolation opt-in (§5.3)",
	})
}

// dispatchPoolIsolationAudit runs the §8.3 line 350 proactive
// pool-registration isolation audit for a newly created or updated pool.
// Per §8.3 the audit is emitted asynchronously and does not block pool
// registration (the pool is persisted regardless), so it runs in a
// goroutine on a detached context. It is a no-op unless the pools,
// runtimes, and delegation-policy stores are all wired (the audit joins
// all three inventories).
func (r *Router) dispatchPoolIsolationAudit(ctx context.Context, p authmw.Principal, poolName string) {
	if r.pools == nil || r.runtimes == nil || r.delegationPolicies == nil {
		return
	}
	detached := context.WithoutCancel(ctx)
	go r.emitPoolIsolationWarnings(detached, p, poolName)
}

// emitPoolIsolationWarnings performs the §8.3 lines 349-352 proactive
// monotonicity audit: for the registered pool, it emits one
// pool.isolation_warning per active DelegationPolicy rule under which a
// more-restrictive parent pool could delegate to this weaker pool. Each
// warning lands on both the §11.7 audit chain (operator visibility) and
// the §11.2.1 billing stream (the affected tenant's cost-attribution /
// compliance record). All emissions are best-effort; the pool is already
// persisted. spec: §8.3 lines 349-352; §11.2.1. F-11.2.1.
func (r *Router) emitPoolIsolationWarnings(ctx context.Context, p authmw.Principal, poolName string) {
	pools, err := r.pools.List(ctx, poolstore.ListFilter{})
	if err != nil {
		return
	}
	runtimes, err := r.runtimes.List(ctx, runtimestore.ListFilter{})
	if err != nil {
		return
	}
	policies, err := r.delegationPolicies.List(ctx, delegationpolicystore.AllTenantsSentinel, delegationpolicystore.ListFilter{})
	if err != nil {
		return
	}
	rtByName := make(map[string]runtimestore.Runtime, len(runtimes))
	for _, rt := range runtimes {
		rtByName[rt.Name] = rt
	}
	candidates := make([]delegationpolicystore.PoolCandidate, 0, len(pools))
	for _, pl := range pools {
		if !pl.IsActive() {
			continue
		}
		rt := rtByName[pl.RuntimeRef]
		candidates = append(candidates, delegationpolicystore.PoolCandidate{
			PoolName:         pl.Name,
			IsolationProfile: string(pl.IsolationProfile),
			IsolationRank:    isolation.Rank(pl.IsolationProfile),
			Candidate: delegationpolicystore.Candidate{
				ID:     pl.RuntimeRef,
				Type:   string(rt.Type),
				Labels: rt.Labels,
			},
		})
	}
	active := make([]delegationpolicystore.DelegationPolicy, 0, len(policies))
	for _, pol := range policies {
		if pol.IsActive() {
			active = append(active, pol)
		}
	}
	for _, v := range delegationpolicystore.ProactiveWarningsForPool(active, candidates, poolName) {
		ruleRef := v.Policy + "#" + strconv.Itoa(v.RuleIndex)
		r.emit(ctx, p, string(audit.EventPoolIsolationWarning), poolName, map[string]any{
			"pool_name":             poolName,
			"pool_isolation":        v.TargetProfile,
			"matched_policy_rule":   ruleRef,
			"conflicting_pool_name": v.SourcePool,
			"conflicting_isolation": v.SourceProfile,
			"policy_tenant_id":      v.TenantID,
		})
		// spec: §11.2.1 — the warning lands on the affected tenant's billing
		// stream (the DelegationPolicy owner), the per-tenant compliance record.
		r.appendBilling(ctx, billingfanout.PoolIsolationWarning(
			v.TenantID, poolName, v.TargetProfile, ruleRef, v.SourcePool, v.SourceProfile,
		))
	}
}

func (r *Router) handleListPools(w http.ResponseWriter, req *http.Request) {
	filter := poolstore.ListFilter{
		IncludeDeleted: req.URL.Query().Get("includeDeleted") == "true",
		RuntimeRef:     req.URL.Query().Get("runtimeRef"),
	}
	rows, err := r.pools.List(req.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// §4: a tenant-admin sees only pools granted to their tenant
	// through the pool_tenant_access table; platform-admin is
	// unfiltered.
	if tenantID, filtered := tenantScopeFilter(req); filtered {
		allowed := r.accessibleSet(req.Context(), tenantaccessstore.KindPool, tenantID)
		kept := rows[:0]
		for _, p := range rows {
			if allowed[p.Name] {
				kept = append(kept, p)
			}
		}
		rows = kept
	}
	out := make([]PoolPayload, 0, len(rows))
	for _, p := range rows {
		out = append(out, fromPool(p))
	}
	// spec: §15.1 lines 1228-1253 — canonical cursor-paginated envelope.
	writePaginatedList(w, req, r.clock(), out, adminTimestampSortFields, adminListDefaultSort,
		func(p PoolPayload, s pagination.Sort) (string, string) {
			switch s.Field {
			case "name":
				return p.Name, p.Name
			case "updated_at":
				return p.UpdatedAt, p.Name
			default:
				return p.CreatedAt, p.Name
			}
		})
}

func (r *Router) handleGetPool(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	row, err := r.pools.Get(req.Context(), name)
	if err != nil {
		if errors.Is(err, poolstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "pool not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// §4 / §15.1: a tenant-admin may read a pool only when their
	// tenant holds an access grant for it; otherwise it reads as 404.
	if tenantID, filtered := tenantScopeFilter(req); filtered {
		if !r.accessibleSet(req.Context(), tenantaccessstore.KindPool, tenantID)[row.Name] {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "pool not found", nil)
			return
		}
	}
	payload := fromPool(row)
	r.attachPoolStatus(req.Context(), &payload)
	// spec: §17.8.2 step 3 — surface the cold-start bootstrapStatus
	// object for a pool that carries a bootstrapMinWarm override.
	r.attachBootstrapStatus(req.Context(), row, &payload)
	payload.SyncStatus = r.resolveSyncStatus(req.Context(), row)
	// spec: §15.1 line 797 — while a pool is draining, GET surfaces the
	// live in-flight session count so operators can watch the drain
	// converge, and the gauge is refreshed off the same read.
	if row.IsDraining() {
		active, _ := r.poolDrainStats(req.Context(), row)
		payload.ActiveSessions = &active
		if r.poolDrainMetrics != nil {
			r.poolDrainMetrics.SetPoolDrainingSessions(row.Name, active)
		}
	}
	// spec: §15.1 line 1209 — GET responses for an admin resource carry
	// the ETag header so the client can use it as the next PUT's If-Match.
	w.Header().Set("ETag", formatETag(row.Generation))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

// attachPoolStatus populates the §5.2 line 629 live poolCondition and
// idlePodCount on a pool payload when a PoolStatusReader is wired and
// the pool has a reconciled SandboxWarmPool. A reader error is swallowed
// so a transient Kubernetes lookup failure never fails the admin GET;
// the live-status fields are simply omitted. poolCondition is set only
// while the pool is in the PoolWarmingUp bootstrap window.
// spec: §5.2 line 629 ("Operator visibility").
func (r *Router) attachPoolStatus(ctx context.Context, p *PoolPayload) {
	if r.poolStatus == nil {
		return
	}
	condition, idle, found, err := r.poolStatus.PoolStatus(ctx, p.Name)
	if err != nil || !found {
		return
	}
	idleCopy := idle
	p.IdlePodCount = &idleCopy
	if condition != "" {
		condCopy := condition
		p.PoolCondition = &condCopy
	}
}

// attachBootstrapStatus populates the §17.8.2 step-3 bootstrapStatus
// object on a pool payload that carries a bootstrapMinWarm override.
// active and bootstrapMinWarm come from the override column, which is
// always available. hoursOfData and estimatedConvergenceAt are populated
// only when a PoolBootstrapStatusReader is wired and the pool has a
// reconciled SandboxWarmPool; a reader error is swallowed so a transient
// Kubernetes lookup never fails the admin GET. A pool with no override
// omits the object entirely. spec: §17.8.2 step 3.
func (r *Router) attachBootstrapStatus(ctx context.Context, row poolstore.Pool, p *PoolPayload) {
	if row.BootstrapMinWarm == nil {
		return
	}
	status := &BootstrapStatusPayload{
		// Without a status reader the override's mere presence is the
		// authoritative "operating under a bootstrap override" signal.
		Active:           true,
		BootstrapMinWarm: *row.BootstrapMinWarm,
	}
	if r.poolBootstrap != nil {
		hoursOfData, scalingMode, found, err := r.poolBootstrap.PoolBootstrapStatus(ctx, row.Name)
		if err == nil && found {
			status.HoursOfData = hoursOfData
			// The controller reports scalingMode: formula once it has
			// converged even while the override column lingers, so the
			// CRD status is authoritative for active vs converged.
			status.Active = scalingMode != scalingModeFormulaStatus
			if status.Active {
				remaining := bootstrapMinHoursOfData - hoursOfData
				if remaining > 0 {
					at := r.clock().Add(time.Duration(remaining * float64(time.Hour)))
					status.EstimatedConvergenceAt = rfc3339Nano(at.UTC())
				}
			}
		}
	}
	p.BootstrapStatus = status
}

// scalingModeFormulaStatus mirrors the §17.8.2 status.scalingMode value
// the PoolScalingController writes once a pool converges. The admin GET
// reads it to decide bootstrapStatus.active.
const scalingModeFormulaStatus = "formula"

// bootstrapMinHoursOfData is the §17.8.2 step-4 traffic-data window
// (48h) the admin GET projects estimatedConvergenceAt against.
const bootstrapMinHoursOfData = 48.0

// handleSyncStatus implements the §4.6.2 line 560
// GET /v1/admin/pools/{name}/sync-status endpoint. It compares the
// pool's Postgres generation to the CRD-side generation stamped by the
// PoolScalingController and reports the lag in seconds.
func (r *Router) handleSyncStatus(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	row, err := r.pools.Get(req.Context(), name)
	if err != nil {
		if errors.Is(err, poolstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "pool not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if tenantID, filtered := tenantScopeFilter(req); filtered {
		if !r.accessibleSet(req.Context(), tenantaccessstore.KindPool, tenantID)[row.Name] {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "pool not found", nil)
			return
		}
	}
	payload := r.buildSyncStatus(req.Context(), row)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

// buildSyncStatus assembles the §4.6.2 line 560 sync-status response
// from a pool row, looking up the CRD-side generation when a reader is
// wired. spec: spec/04_system-components.md lines 557-560.
func (r *Router) buildSyncStatus(ctx context.Context, row poolstore.Pool) PoolSyncStatus {
	out := PoolSyncStatus{
		Pool:               row.Name,
		PostgresGeneration: row.Generation,
	}
	if r.crdGenerations == nil {
		// Without a CRD reader the gateway can only report the
		// Postgres-side state; leave crdGeneration / inSync at their
		// zero values so the operator sees that no CRD comparison ran.
		return out
	}
	crdGen, lastAt, ok, err := r.crdGenerations.CRDGeneration(ctx, row.Name)
	if err != nil || !ok {
		// A missing SandboxTemplate is the not-yet-reconciled case; the
		// pool exists in Postgres but the controller has yet to write
		// it to the CRD. Leave crdGeneration=0 inSync=false.
		return out
	}
	out.CRDGeneration = crdGen
	out.InSync = crdGen == row.Generation
	if !lastAt.IsZero() {
		out.LastReconciledAt = rfc3339Nano(lastAt)
		lag := int64(r.clock().Sub(lastAt).Seconds())
		if lag < 0 {
			lag = 0
		}
		out.LagSeconds = lag
	}
	return out
}

// resolveSyncStatus reports the §4.6.2 line 559 syncStatus flag for one
// pool. "synced" means the CRD generation matches Postgres; "pending"
// means a write has not been observed on the CRD yet; "unknown" means
// no CRD reader is wired.
func (r *Router) resolveSyncStatus(ctx context.Context, row poolstore.Pool) string {
	if r.crdGenerations == nil {
		return "unknown"
	}
	crdGen, _, ok, err := r.crdGenerations.CRDGeneration(ctx, row.Name)
	if err != nil || !ok {
		return "pending"
	}
	if crdGen == row.Generation {
		return "synced"
	}
	return "pending"
}

// handleResumeReconciliation implements §4.6.2 item 3 condition (c):
// an operator resets a stuck pool's admission-denial backoff so the
// PoolScalingController retries the pool on its next tick without
// requiring a Postgres configuration change. The pool must exist. With
// an in-process resumer wired the denial counter is cleared
// synchronously and the response reports how many CRD tuples were
// cleared; in the split deployment the handler bumps the durable
// reconciliation_resume_epoch and the response reports the new epoch the
// controller acts on (async=true).
func (r *Router) handleResumeReconciliation(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	if _, err := r.pools.Get(req.Context(), name); err != nil {
		if errors.Is(err, poolstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "pool not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// Two resolution paths per the deployment topology. A wired
	// in-process resumer (single-process dev / tests) clears the PSC's
	// denial counter synchronously and reports the cleared-tuple count.
	// In the production split deployment the gateway has no live
	// Reconciler to call, so it bumps the durable
	// reconciliation_resume_epoch the PoolScalingController reads from
	// this same Postgres store on its next reconcile tick (§4.6.2 item 3
	// condition (c)).
	var (
		cleared int
		epoch   int64
		async   bool
	)
	if r.reconciliationResumer != nil {
		c, err := r.reconciliationResumer.ResumePoolReconciliation(req.Context(), name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
		cleared = c
	} else {
		e, err := r.pools.BumpResumeEpoch(req.Context(), name)
		if err != nil {
			if errors.Is(err, poolstore.ErrNotFound) {
				writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "pool not found", nil)
				return
			}
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
		epoch = e
		async = true
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	detail := map[string]any{
		"clearedTuples": cleared,
		"wasStuck":      cleared > 0,
		"async":         async,
	}
	if async {
		detail["resumeEpoch"] = epoch
	}
	r.emit(req.Context(), principal, "admin.pool.reconciliation_resumed", name, detail)
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"pool":          name,
		"clearedTuples": cleared,
		"wasStuck":      cleared > 0,
		"async":         async,
	}
	if async {
		// In the split deployment the resume is recorded durably and
		// honored on the controller's next reconcile tick; the gateway
		// cannot observe the cleared-tuple count, so it reports the new
		// epoch the PSC acts on instead.
		resp["resumeEpoch"] = epoch
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// applyPoolUpdateMerge merges a §15.1 UpdatePoolRequest onto a pool in
// place, applying the partial-update rule that an omitted (nil) field
// leaves the stored value unchanged. It is the single merge implementation
// shared by the real store Update closure and the dry-run preview.
func applyPoolUpdateMerge(p *poolstore.Pool, body UpdatePoolRequest) {
	if body.RuntimeRef != nil {
		p.RuntimeRef = *body.RuntimeRef
	}
	if body.IsolationProfile != nil {
		p.IsolationProfile = isolation.Profile(*body.IsolationProfile)
	}
	if body.ExecutionMode != nil {
		p.ExecutionMode = runtimestore.ExecutionMode(*body.ExecutionMode)
	}
	if body.ResourceClass != nil {
		p.ResourceClass = *body.ResourceClass
	}
	if body.WarmCount != nil {
		p.WarmCount = *body.WarmCount
	}
	// spec: §17.8.2 step 3 — a non-nil bootstrapMinWarm sets or updates
	// the cold-start override. Clearing is the dedicated
	// DELETE /v1/admin/pools/{name}/bootstrap-override route, so PUT
	// never clears the override.
	if body.BootstrapMinWarm != nil {
		v := *body.BootstrapMinWarm
		p.BootstrapMinWarm = &v
	}
	if body.MaxSessionAgeSeconds != nil {
		p.MaxSessionAgeSeconds = *body.MaxSessionAgeSeconds
	}
	if body.AllowStandardIsolation != nil {
		p.AllowStandardIsolation = *body.AllowStandardIsolation
	}
	if body.EgressProfile != nil {
		p.EgressProfile = egress.Profile(*body.EgressProfile)
	}
	if body.MaxConcurrent != nil {
		p.MaxConcurrent = *body.MaxConcurrent
	}
	if body.ClearSessionPolicy {
		p.SessionPolicy = nil
	} else if body.SessionPolicy != nil {
		p.SessionPolicy = body.SessionPolicy.Clone()
	}
	if body.ElicitationDepthPolicy != nil {
		p.ElicitationDepthPolicy = elicitation.DepthPolicy(*body.ElicitationDepthPolicy)
	}
	if body.ElicitationSuppressAtDepth != nil {
		p.ElicitationSuppressAtDepth = *body.ElicitationSuppressAtDepth
	}
	if body.URLModeElicitation != nil {
		p.URLModeElicitation = urlModeFromWire(body.URLModeElicitation)
	}
	// spec: §6.1 line 48 — the main PUT honors acknowledgeHighDemotionRate
	// and the deployer-configurable demotionRateThreshold. The
	// circuitBreakerOverride is left to the dedicated /circuit-breaker
	// endpoint (which enforces the non-SDK-warm 409 and the override audit
	// event), so it is not merged here.
	if body.SDKWarm != nil {
		p.AcknowledgeHighDemotionRate = body.SDKWarm.AcknowledgeHighDemotionRate
		p.DemotionRateThreshold = body.SDKWarm.DemotionRateThreshold
	}
}

func (r *Router) handleUpdatePool(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	var body UpdatePoolRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	// spec: §15.1 lines 1207-1211 — every admin PUT requires If-Match.
	// The pool's entity tag is its pool_config_generation; read the
	// current row and enforce the optimistic-concurrency precondition
	// before applying the mutation. A missing pool reads as 404 ahead of
	// the precondition so a stale-handle client cannot probe existence.
	current, err := r.pools.Get(req.Context(), name)
	if err != nil {
		if errors.Is(err, poolstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "pool not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if !enforceIfMatch(w, req, current.Generation) {
		return
	}
	// spec: §15.1 line 1140 — ?dryRun=true validates without persisting or auditing.
	// The dry-run flag is resolved here, after the If-Match precondition, so
	// dryRun=true combined with a stale If-Match still returns 412 above.
	r.applyPoolUpdate(w, req, name, body, req.URL.Query().Get("dryRun") == "true")
}

// WarmCountRequest is the §25.17 PUT /v1/admin/pools/{name}/warm-count
// body. The agent-operability worked example and the warm-pool-exhaustion
// runbook address the pool's warm count by the field name `minWarm` and
// gate the mutation behind a confirm flag, distinct from the §15.1
// UpdatePoolRequest `warmCount` field name. spec: §25.17 lines 5232-5239.
type WarmCountRequest struct {
	MinWarm *int `json:"minWarm"`
	Confirm bool `json:"confirm"`
}

// handleUpdatePoolWarmCount serves the §25.17 PUT
// /v1/admin/pools/{name}/warm-count sub-route the diagnostic
// suggestedAction and the runbook point at. It maps the operability
// `minWarm` field onto the §15.1 warm-count update and delegates to the
// shared pool-update path so the same validation, audit emission, and
// sync-status resolution apply. spec: §25.17 lines 5232-5239.
func (r *Router) handleUpdatePoolWarmCount(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	var body WarmCountRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	if body.MinWarm == nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "minWarm is required", nil)
		return
	}
	if *body.MinWarm < 0 {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "minWarm must not be negative", nil)
		return
	}
	// spec: §25.17 line 5238 — the scale request carries confirm:true; the
	// §25.2 dry-run/confirm convention returns a preview without confirm so
	// a retried watchdog does not scale on an exploratory call.
	if !body.Confirm {
		writeJSON(w, http.StatusOK, map[string]any{
			"dryRun": true,
			"preview": map[string]any{
				"pool":          name,
				"field":         "minWarm",
				"proposedValue": *body.MinWarm,
				"warnings": []string{
					"This sets the warm count for pool " + name +
						". Re-run with confirm:true to apply.",
				},
			},
		})
		return
	}
	warmCount := *body.MinWarm
	// spec: §25.17 — the warm-count sub-route does not support the §15.1
	// ?dryRun=true query convention; it has its own confirm-flag preview.
	r.applyPoolUpdate(w, req, name, UpdatePoolRequest{WarmCount: &warmCount}, false)
}

// CircuitBreakerOverrideRequest is the §15.1 line 801
// PUT /v1/admin/pools/{name}/circuit-breaker body:
// `{"sdkWarm": {"circuitBreakerOverride": "enabled"|"disabled"|"auto"}}`.
// spec: §6.1 lines 63-65, §15.1 line 801.
type CircuitBreakerOverrideRequest struct {
	SDKWarm *SDKWarmOverridePayload `json:"sdkWarm,omitempty"`
}

// SDKWarmOverridePayload carries the circuitBreakerOverride value of the
// §15.1 line 801 request body.
type SDKWarmOverridePayload struct {
	CircuitBreakerOverride string `json:"circuitBreakerOverride,omitempty"`
}

// handleUpdatePoolCircuitBreaker serves the §15.1 line 801
// PUT /v1/admin/pools/{name}/circuit-breaker endpoint. It overrides the
// §6.1 SDK-warm circuit-breaker state: `enabled` clears a tripped breaker
// ahead of its grace window, `disabled` forces SDK-warm off, and `auto`
// restores automatic control. The PoolScalingController reads the stored
// override via the §4.6.2 PoolStoreSource and applies it on its next
// reconcile. The endpoint requires If-Match, returns 409
// INVALID_STATE_TRANSITION for a non-SDK-warm pool, and emits the
// pool.sdk_warm_circuit_breaker_override audit event. spec: §6.1 lines
// 63-65, §15.1 line 801.
func (r *Router) handleUpdatePoolCircuitBreaker(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	var body CircuitBreakerOverrideRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	if body.SDKWarm == nil || body.SDKWarm.CircuitBreakerOverride == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"sdkWarm.circuitBreakerOverride is required", nil)
		return
	}
	override := poolstore.SDKWarmCircuitBreakerOverride(body.SDKWarm.CircuitBreakerOverride)
	// spec: §15.1 line 801 — the request value must be one of the explicit
	// override actions; the unset/empty value is not an accepted request.
	if override == poolstore.SDKWarmOverrideUnset || !override.IsValid() {
		writeError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR",
			"sdkWarm.circuitBreakerOverride must be one of enabled, disabled, auto",
			map[string]any{"field": "sdkWarm.circuitBreakerOverride"})
		return
	}
	current, err := r.pools.Get(req.Context(), name)
	if err != nil {
		if errors.Is(err, poolstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "pool not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// spec: §15.1 lines 1207-1211 — the override is a PUT and requires
	// If-Match against the pool_config_generation.
	if !enforceIfMatch(w, req, current.Generation) {
		return
	}
	// spec: §15.1 line 801 — circuit-breaker override has no effect on a
	// non-SDK-warm pool; reject with 409 INVALID_STATE_TRANSITION.
	if !r.poolIsSDKWarm(req.Context(), current) {
		writeError(w, http.StatusConflict, "INVALID_STATE_TRANSITION",
			"circuit-breaker override has no effect on a pool whose runtime is not SDK-warm (capabilities.preConnect)",
			map[string]any{"pool": name})
		return
	}
	previous := current.SDKWarmCircuitBreakerOverride
	updated, err := r.pools.Update(req.Context(), name, func(p *poolstore.Pool) error {
		p.SDKWarmCircuitBreakerOverride = override
		return nil
	})
	if err != nil {
		if errors.Is(err, poolstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "pool not found", nil)
			return
		}
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	// spec: §15.1 line 801 — record operator identity (carried by emit),
	// the previous override state, and the new value.
	prevWire := string(previous)
	if previous == poolstore.SDKWarmOverrideUnset {
		prevWire = string(poolstore.SDKWarmOverrideAuto)
	}
	r.emit(req.Context(), principal, "pool.sdk_warm_circuit_breaker_override", name, map[string]any{
		"previousOverride": prevWire,
		"newOverride":      string(override),
	})
	w.Header().Set("ETag", formatETag(updated.Generation))
	w.Header().Set("Content-Type", "application/json")
	payload := fromPool(updated)
	payload.SyncStatus = r.resolveSyncStatus(req.Context(), updated)
	_ = json.NewEncoder(w).Encode(payload)
}

// poolIsSDKWarm reports whether the pool's runtime declares the §6.1
// preConnect capability, i.e. whether the pool warms SDK-warm pods. The
// §15.1 circuit-breaker override is only meaningful on such a pool. When
// no runtime store is wired the check is skipped (the override is
// permitted); a runtime that does not resolve is treated as not SDK-warm.
func (r *Router) poolIsSDKWarm(ctx context.Context, p poolstore.Pool) bool {
	if r.runtimes == nil {
		return true
	}
	rt, err := r.runtimes.Get(ctx, p.RuntimeRef)
	if err != nil {
		return false
	}
	return poolstore.RuntimePreConnect(rt)
}

// applyPoolUpdate validates the §15.1 pool-update body, applies it, emits
// the audit event, and writes the §4.6.2 sync-status response. It is the
// shared core of the PUT /v1/admin/pools/{name} handler and the §25.17
// warm-count sub-route. When dryRun is true the full validation runs but
// the merged pool is previewed without persisting or auditing (§15.1 line
// 1140); the warm-count sub-route always passes dryRun=false because it
// does not support the query-parameter dry run.
func (r *Router) applyPoolUpdate(w http.ResponseWriter, req *http.Request, name string, body UpdatePoolRequest, dryRun bool) {
	if body.IsolationProfile != nil && *body.IsolationProfile != "" &&
		!isolation.IsValid(isolation.Profile(*body.IsolationProfile)) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"isolationProfile is not a recognised §5.3 profile", nil)
		return
	}
	if body.ExecutionMode != nil && *body.ExecutionMode != "" &&
		!runtimestore.ExecutionMode(*body.ExecutionMode).IsValid() {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"executionMode is not a recognised mode", nil)
		return
	}
	if body.EgressProfile != nil && *body.EgressProfile != "" &&
		!egress.IsValid(egress.Profile(*body.EgressProfile)) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"egressProfile is not a recognised §13.2 profile (restricted, provider-direct, internet)", nil)
		return
	}
	if body.ClearSessionPolicy && body.SessionPolicy != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"clearSessionPolicy and sessionPolicy are mutually exclusive in one PUT", nil)
		return
	}
	if body.ElicitationDepthPolicy != nil && *body.ElicitationDepthPolicy != "" &&
		!elicitation.DepthPolicy(*body.ElicitationDepthPolicy).IsValid() {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"elicitationDepthPolicy is not a recognised §9.2 policy (allow_all, suppress_at_depth, block_all)", nil)
		return
	}
	// runtimeRef cross-check.
	if body.RuntimeRef != nil && *body.RuntimeRef != "" && r.runtimes != nil {
		if _, err := r.runtimes.Get(req.Context(), *body.RuntimeRef); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"runtimeRef does not resolve to a registered runtime", nil)
			return
		}
	}
	// spec: §5.2 line 396 — reject a PUT that would leave the pool with
	// cross-tenant reuse enabled while its runtime is T4. The effective
	// post-update runtimeRef and allowCrossTenantReuse are resolved from
	// the body (when set) or the stored pool, so newly setting either
	// field is caught. The runtime tier is only consulted when reuse
	// would be on.
	if r.runtimes != nil {
		current, gerr := r.pools.Get(req.Context(), name)
		if errors.Is(gerr, poolstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "pool not found", nil)
			return
		} else if gerr != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", gerr.Error(), nil)
			return
		}
		// effSession is the post-update sessionPolicy: the body's block when
		// set (or cleared), otherwise the stored block. The cross-tenant and
		// preConnect checks key off it after the relocation of
		// allowCrossTenantReuse onto recycle.
		effSession := current.SessionPolicy
		if body.ClearSessionPolicy {
			effSession = nil
		} else if body.SessionPolicy != nil {
			effSession = body.SessionPolicy
		}
		effPool := poolstore.Pool{SessionPolicy: effSession}
		if effPool.RequestsCrossTenantReuse() {
			effRef := current.RuntimeRef
			if body.RuntimeRef != nil {
				effRef = *body.RuntimeRef
			}
			var tier runtimestore.WorkspaceTier
			if effRef != "" {
				rt, rerr := r.runtimes.Get(req.Context(), effRef)
				if rerr != nil {
					writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
						"runtimeRef does not resolve to a registered runtime", nil)
					return
				}
				tier = rt.WorkspaceTier
			}
			if err := poolstore.ValidateCrossTenantReuseTier(effPool, tier); err != nil {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(),
					map[string]any{"runtimeRef": effRef, "workspaceTier": string(tier)})
				return
			}
		}
		// spec: §5.2 line 430, §6.1 lines 77-78 — reject a PUT that would
		// leave the pool bound to a preConnect runtime in service mode or
		// with maxConcurrentSessions > 1. The effective post-update
		// executionMode, sessionPolicy, and runtimeRef are resolved from the
		// body (when set) or the stored pool.
		effMode := current.ExecutionMode
		if body.ExecutionMode != nil {
			effMode = runtimestore.ExecutionMode(*body.ExecutionMode)
		}
		effRef := current.RuntimeRef
		if body.RuntimeRef != nil {
			effRef = *body.RuntimeRef
		}
		if effRef != "" {
			rt, rerr := r.runtimes.Get(req.Context(), effRef)
			if rerr != nil {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
					"runtimeRef does not resolve to a registered runtime", nil)
				return
			}
			if err := poolstore.ValidatePreConnectExecutionMode(
				poolstore.RuntimePreConnect(rt), effMode, effSession,
			); err != nil {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(),
					map[string]any{"runtimeRef": effRef})
				return
			}
		}
	}
	// spec: §15.1 line 1140 — ?dryRun=true validates without persisting or auditing.
	// The dry-run branch runs after the full validation above (and, in
	// handleUpdatePool, after the If-Match precondition) so a stale If-Match
	// combined with dryRun=true still returns 412 before this point. The
	// preview reflects applying the body onto the current pool with the
	// generation a persisted update would stamp (the §15.1 line 1210 ETag).
	if dryRun {
		current, gerr := r.pools.Get(req.Context(), name)
		if gerr != nil {
			if errors.Is(gerr, poolstore.ErrNotFound) {
				writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "pool not found", nil)
				return
			}
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", gerr.Error(), nil)
			return
		}
		preview := current
		applyPoolUpdateMerge(&preview, body)
		// Run the same store-side validations a persisted update would, so
		// the dry run rejects exactly what a real PUT would reject.
		if perr := validatePoolForStore(preview); perr != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", perr.Error(), nil)
			return
		}
		// A persisted update bumps pool_config_generation; the preview
		// carries the bumped value so the ETag matches a real success.
		preview.Generation = current.Generation + 1
		w.Header().Set("ETag", formatETag(preview.Generation))
		payload := fromPool(preview)
		payload.SyncStatus = r.resolveSyncStatus(req.Context(), preview)
		writeDryRun(w, http.StatusOK, payload)
		return
	}
	updated, err := r.pools.Update(req.Context(), name, func(p *poolstore.Pool) error {
		applyPoolUpdateMerge(p, body)
		return nil
	})
	if err != nil {
		if errors.Is(err, poolstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "pool not found", nil)
			return
		}
		if errors.Is(err, poolstore.ErrURLModeDomainRequired) {
			// spec: §9.2 line 86 — enabling agent-initiated url-mode
			// elicitation requires a non-empty domainAllowlist.
			writeError(w, http.StatusBadRequest, "URL_MODE_ELICITATION_DOMAIN_REQUIRED", err.Error(),
				map[string]any{"field": "urlModeElicitation.domainAllowlist"})
			return
		}
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	r.emit(req.Context(), principal, "admin.pool.updated", name, map[string]any{
		"changedFields": changedPoolFields(body),
	})
	r.maybeEmitWeakIsolation(req.Context(), principal, updated)
	r.dispatchPoolIsolationAudit(req.Context(), principal, updated.Name)
	// spec: §15.1 line 1210 — on a successful PUT the response carries
	// the new ETag reflecting the incremented version.
	w.Header().Set("ETag", formatETag(updated.Generation))
	w.Header().Set("Content-Type", "application/json")
	payload := fromPool(updated)
	// spec: §4.6.2 line 559 — a PUT immediately bumps the Postgres
	// generation; the CRD has not yet reconciled, so syncStatus is
	// "pending" until the controller catches up.
	payload.SyncStatus = r.resolveSyncStatus(req.Context(), updated)
	_ = json.NewEncoder(w).Encode(payload)
}

func (r *Router) handleDeletePool(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	if err := r.pools.SoftDelete(req.Context(), name, r.clock()); err != nil {
		if errors.Is(err, poolstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "pool not found", nil)
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
	r.emit(req.Context(), principal, "admin.pool.soft_deleted", name, nil)
	w.WriteHeader(http.StatusNoContent)
}

// handleClearBootstrapOverride serves the §17.8.2 step-3
// DELETE /v1/admin/pools/{name}/bootstrap-override route. It clears the
// pool's bootstrapMinWarm override so the PoolScalingController switches
// to formula-driven scaling immediately, regardless of the 48-hour
// window. A present If-Match is honoured (§15.1 line 1213). A pool with
// no override in force is a no-op success so the operation is
// idempotent. The response carries the updated pool payload (now without
// the bootstrapStatus object) and the bumped ETag. spec: §17.8.2 step 3.
func (r *Router) handleClearBootstrapOverride(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	current, err := r.pools.Get(req.Context(), name)
	if err != nil {
		if errors.Is(err, poolstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "pool not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// §4 / §15.1: a tenant-admin may address a pool only when their
	// tenant holds an access grant for it; otherwise it reads as 404.
	if tenantID, filtered := tenantScopeFilter(req); filtered {
		if !r.accessibleSet(req.Context(), tenantaccessstore.KindPool, tenantID)[current.Name] {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "pool not found", nil)
			return
		}
	}
	// spec: §15.1 line 1213 — DELETE honours If-Match when present.
	if !enforceIfMatchIfPresent(w, req, current.Generation) {
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	// A pool with no override is already formula-driven; report success
	// without churning the generation so the operation is idempotent.
	if current.BootstrapMinWarm == nil {
		w.Header().Set("ETag", formatETag(current.Generation))
		w.Header().Set("Content-Type", "application/json")
		payload := fromPool(current)
		payload.SyncStatus = r.resolveSyncStatus(req.Context(), current)
		_ = json.NewEncoder(w).Encode(payload)
		return
	}
	updated, err := r.pools.Update(req.Context(), name, func(p *poolstore.Pool) error {
		p.BootstrapMinWarm = nil
		return nil
	})
	if err != nil {
		if errors.Is(err, poolstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "pool not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	r.emit(req.Context(), principal, "admin.pool.bootstrap_override_cleared", name, nil)
	w.Header().Set("ETag", formatETag(updated.Generation))
	w.Header().Set("Content-Type", "application/json")
	payload := fromPool(updated)
	payload.SyncStatus = r.resolveSyncStatus(req.Context(), updated)
	_ = json.NewEncoder(w).Encode(payload)
}

func changedPoolFields(b UpdatePoolRequest) []string {
	var out []string
	if b.RuntimeRef != nil {
		out = append(out, "runtimeRef")
	}
	if b.IsolationProfile != nil {
		out = append(out, "isolationProfile")
	}
	if b.ExecutionMode != nil {
		out = append(out, "executionMode")
	}
	if b.ResourceClass != nil {
		out = append(out, "resourceClass")
	}
	if b.WarmCount != nil {
		out = append(out, "warmCount")
	}
	if b.BootstrapMinWarm != nil {
		out = append(out, "bootstrapMinWarm")
	}
	if b.MaxSessionAgeSeconds != nil {
		out = append(out, "maxSessionAgeSeconds")
	}
	if b.AllowStandardIsolation != nil {
		out = append(out, "allowStandardIsolation")
	}
	if b.EgressProfile != nil {
		out = append(out, "egressProfile")
	}
	if b.MaxConcurrent != nil {
		out = append(out, "maxConcurrent")
	}
	if b.SessionPolicy != nil {
		out = append(out, "sessionPolicy")
	}
	if b.ClearSessionPolicy {
		out = append(out, "sessionPolicy.cleared")
	}
	if b.SDKWarm != nil {
		out = append(out, "sdkWarm.acknowledgeHighDemotionRate")
	}
	return out
}
