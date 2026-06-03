// SPDX-License-Identifier: MIT

// Package poolstore is the §5.2 SandboxWarmPool registry. It backs
// the pool admission used by session creation (`runtimeRef` →
// `SandboxWarmPool` lookup), the §15.1 admin pool CRUD endpoints,
// and the §4.6.2 PoolScalingController.
//
// Per §5.1 pools are platform-global (no tenant_id, no RLS). The
// §10.6 `runtime_tenant_access` / `pool_tenant_access` join tables
// enforce per-tenant visibility at the admin handler layer; this
// store is the source of truth for the pool record itself.
package poolstore

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/elicitation"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/sandbox/egress"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// Pool captures the §5.2 SandboxWarmPool CRD shape. v1 models the
// essential fields; extension fields (taskPolicy, credentialPolicy,
// env, image overrides) attach to the row but are not strictly
// validated by this store — the admin handler owns the cross-field
// validation per §5.2.
type Pool struct {
	// Name is the §5.2 pool identifier.
	Name string

	// RuntimeRef names the runtime this pool warms.
	RuntimeRef string

	// IsolationProfile overrides the runtime's default §5.3 profile.
	IsolationProfile isolation.Profile

	// ExecutionMode is the §5.2 mode (session, task, concurrent).
	ExecutionMode runtimestore.ExecutionMode

	// ConcurrencyStyle is the §5.2 concurrent-mode sub-variant
	// (`workspace`, `stateless`). It is meaningful only when
	// ExecutionMode is `concurrent`, where ValidateConcurrentConfig
	// requires it.
	ConcurrencyStyle ConcurrencyStyle

	// MaxConcurrent is the §5.2 per-pod slot bound for concurrent mode.
	// A concurrent-mode pod hosts at most this many slots
	// simultaneously. It must be >= 1 on a concurrent-mode pool.
	MaxConcurrent int

	// AcknowledgeProcessLevelIsolation records the §5.2 deployer
	// acknowledgment that concurrent-workspace slots share the pod
	// process namespace, /tmp, cgroup memory, network stack, and
	// credential group-read access. A concurrent-workspace pool is
	// rejected without it.
	AcknowledgeProcessLevelIsolation bool

	// CleanupTimeoutSeconds bounds per-slot cleanup on a
	// concurrent-workspace pool. The §5.2 rule requires
	// CleanupTimeoutSeconds >= MaxConcurrent * 5 so each slot's cleanup
	// budget clears the 5-second floor.
	CleanupTimeoutSeconds int

	// AllowCrossTenantReuse mirrors the §5.2 task-mode field. Concurrent
	// modes have no cross-tenant isolation boundary, so
	// ValidateConcurrentConfig rejects a concurrent-mode pool that sets
	// it.
	AllowCrossTenantReuse bool

	// ResourceClass is the §5.2 size bucket (`small`, `medium`,
	// `large`); free-form per pool admin.
	ResourceClass string

	// WarmCount is the §5.2 desired warm replica count. Mode-adjusted
	// per §4.6.2.
	WarmCount int

	// MaxSessionAgeSeconds is the §5.2 per-session lifetime cap.
	MaxSessionAgeSeconds int

	// AllowStandardIsolation gates §5.3 `standard` profile admission.
	// Pools whose IsolationProfile is `standard` require this flag
	// per §5.3 security note.
	AllowStandardIsolation bool

	// EgressProfile is the §13.2 per-pool egress profile (`restricted`,
	// `provider-direct`, `internet`). Empty resolves to the §13.2
	// default (`restricted`) at admission. The store rejects an
	// `internet` profile on a `standard` (runc) pool per the §13.2
	// cross-control.
	EgressProfile egress.Profile

	// CreatedAt / UpdatedAt / DeletedAt are the audit timestamps.
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt time.Time

	// Generation is the §4.6.2 pool_config_generation counter, bumped
	// on every admin-API write and stamped onto the pool's CRD pair as
	// the lenny.dev/config-generation annotation so the §4.6.2
	// PoolConfigDrift alert can compare Postgres-side and CRD-side
	// generations. spec: spec/04_system-components.md lines 558-560.
	Generation int64

	// DrainingSince records when the pool entered the §15.1 line 797
	// `draining` phase. A zero value means the pool is `active`. While
	// it is set the gateway stops admitting new sessions to the pool
	// (POST /v1/admin/pools/{name}/drain → GET reports `phase: draining`),
	// and session creation that would select the pool is rejected with
	// 503 POOL_DRAINING. spec: §15.1 line 797.
	DrainingSince time.Time

	// TaskPolicy is the §5.2 task-mode policy block (lines 398-413). It
	// is required when ExecutionMode is `task` and must be absent on
	// session or concurrent pools — `ValidateTaskPolicy` enforces both
	// directions. AllowCrossTenantReuse is intentionally not on this
	// struct: it lives on Pool as the legacy top-level field and the
	// CRD-side TaskPolicy.AllowCrossTenantReuse is populated from there
	// when PoolStoreSource maps the pool to its SandboxTemplate. spec:
	// §5.2 lines 398-475.
	TaskPolicy *TaskPolicy

	// ElicitationDepthPolicy is the §9.2 line 90-98 per-pool
	// `elicitationDepthPolicy` that governs whether agent-initiated
	// elicitations raised by a session in this pool are suppressed by
	// delegation depth (`allow_all`, `suppress_at_depth`, `block_all`).
	// Empty defaults to the §9.2 line 92 platform default
	// (`suppress_at_depth` at depth 3) when the dispatcher resolves it.
	// spec: §9.2 lines 90-98.
	ElicitationDepthPolicy elicitation.DepthPolicy

	// ElicitationSuppressAtDepth is the §9.2 line 92 threshold N for the
	// `suppress_at_depth` policy; ignored for the other policies. Zero
	// with a `suppress_at_depth` policy resolves to the platform default
	// (DefaultSuppressAtDepth=3) at the dispatcher.
	ElicitationSuppressAtDepth int

	// URLModeElicitation is the §9.2 line 86 per-pool agent-initiated
	// url-mode elicitation allowlist. The zero value (Enabled:false)
	// blocks every agent-initiated url-mode elicitation, the §9.2
	// default. Enabled:true with an empty DomainAllowlist is rejected at
	// store admission with URL_MODE_ELICITATION_DOMAIN_REQUIRED.
	// spec: §9.2 line 86.
	URLModeElicitation elicitation.URLModeAllowlist
}

// TaskPolicy mirrors the §5.2 taskPolicy block declared on
// SandboxTemplate.spec (`pkg/apis/lenny/v1/sandboxtemplate_types.go`)
// minus AllowCrossTenantReuse, which lives at the pool top level. Field
// semantics match the CRD verbatim so the admin REST surface, store
// row, and CRD all share interpretation. spec: §5.2 lines 398-475.
type TaskPolicy struct {
	// AcknowledgeBestEffortScrub records the §5.2 line 461 deployer
	// acknowledgment that workspace scrub is best-effort. A task-mode
	// pool is rejected without it (line 473).
	AcknowledgeBestEffortScrub bool

	// MicrovmScrubMode is the §5.2 line 442 cross-tenant scrub variant
	// for microvm pods: `restart` (default) boots a fresh guest between
	// tenants, `in-place` reuses the running guest with documented
	// guest-kernel residual state. Meaningful only when the pool's
	// AllowCrossTenantReuse is true and IsolationProfile is microvm.
	MicrovmScrubMode runtimestore.MicrovmScrubMode

	// AcknowledgeMicrovmResidualState records the §5.2 line 442
	// acknowledgment that guest-kernel residual state persists when
	// MicrovmScrubMode is `in-place`. Required in that mode.
	AcknowledgeMicrovmResidualState bool

	// CleanupCommands are the §5.2 line 404 deployer-defined commands
	// the adapter runs between tasks (after the credential purge in
	// step 0, before the §5.2 scrub steps 1-6).
	CleanupCommands []string

	// CleanupTimeoutSeconds bounds the cleanupCommands phase (§5.2 line
	// 407). 30s in the spec yaml example.
	CleanupTimeoutSeconds int

	// OnCleanupFailure is the §5.2 line 444 disposition for a cleanup
	// failure: `warn` returns the pod to the pool with a scrub_warning
	// annotation; `fail` retires the pod.
	OnCleanupFailure runtimestore.CleanupFailureDisposition

	// MaxScrubFailures is the §5.2 line 446 cumulative scrub-failure
	// retirement threshold. The spec default is 3.
	MaxScrubFailures int

	// MaxTasksPerPod is the §5.2 line 410 task-count retirement
	// threshold. Required (>= 1) on task-mode pools.
	MaxTasksPerPod int

	// MaxPodUptimeSeconds is the §5.2 line 411 uptime retirement
	// threshold; zero leaves retirement governed by MaxTasksPerPod and
	// MaxScrubFailures alone.
	MaxPodUptimeSeconds int

	// MaxTaskRetries is the §5.2 line 412 / §6.6 per-task crash retry
	// budget. A nil value takes the §6.6 default of 1; an explicit 0
	// disables retries.
	MaxTaskRetries *int
}

// Clone returns a deep copy so the store never shares the
// cleanup-command slice or retry pointer with a caller. A nil receiver
// clones to nil.
func (p *TaskPolicy) Clone() *TaskPolicy {
	if p == nil {
		return nil
	}
	cp := *p
	cp.CleanupCommands = append([]string(nil), p.CleanupCommands...)
	if p.MaxTaskRetries != nil {
		n := *p.MaxTaskRetries
		cp.MaxTaskRetries = &n
	}
	return &cp
}

// IsActive reports whether the pool has not been soft-deleted.
func (p Pool) IsActive() bool { return p.DeletedAt.IsZero() }

// Phase reports the §15.1 line 797 pool lifecycle phase the admin GET
// surfaces: "draining" once DrainingSince is set, "active" otherwise.
// Soft-deleted pools still report their drain phase; IsActive gates
// deletion separately.
func (p Pool) Phase() string {
	if p.IsDraining() {
		return PhaseDraining
	}
	return PhaseActive
}

// IsDraining reports whether the pool has entered the §15.1 line 797
// draining phase and is therefore closed to new session admission.
func (p Pool) IsDraining() bool { return !p.DrainingSince.IsZero() }

// EstimatedDrainSeconds returns the §15.1 line 797 drain-completion
// estimate for a draining pool given the longest active session age in
// the pool (in seconds). The spec derives the estimate from the longest
// active session age, capped at the pool's maxSessionAgeSeconds because
// a session cannot outlive its lifetime cap. A pool with no lifetime cap
// (maxSessionAgeSeconds == 0) returns the uncapped age. The value feeds
// the Retry-After header on a POOL_DRAINING rejection; ages are whole
// seconds so the ceil() the spec names is already satisfied. spec:
// §15.1 line 797.
func EstimatedDrainSeconds(p Pool, longestAgeSeconds int) int {
	if longestAgeSeconds < 0 {
		longestAgeSeconds = 0
	}
	if p.MaxSessionAgeSeconds > 0 && longestAgeSeconds > p.MaxSessionAgeSeconds {
		return p.MaxSessionAgeSeconds
	}
	return longestAgeSeconds
}

// Pool lifecycle phases surfaced on the §15.1 admin GET. spec: §15.1
// line 797.
const (
	PhaseActive   = "active"
	PhaseDraining = "draining"
)

// ConcurrencyStyle is the §5.2 concurrent-mode sub-variant.
type ConcurrencyStyle string

const (
	// ConcurrencyStyleWorkspace is `concurrencyStyle: workspace` —
	// workspace-concurrent. Each slot gets its own per-slot workspace
	// tree and the pod's /workspace/shared/ is shared read-only across
	// the pod's slots (§6.4).
	ConcurrencyStyleWorkspace ConcurrencyStyle = "workspace"

	// ConcurrencyStyleStateless is `concurrencyStyle: stateless` —
	// stateless-concurrent. No workspace is materialized and the pod
	// holds no Lenny-managed per-slot session state (§5.2).
	ConcurrencyStyleStateless ConcurrencyStyle = "stateless"
)

// AllConcurrencyStyles returns the closed §5.2 enum.
func AllConcurrencyStyles() []ConcurrencyStyle {
	return []ConcurrencyStyle{ConcurrencyStyleWorkspace, ConcurrencyStyleStateless}
}

// IsValid reports whether s is a known concurrency style.
func (s ConcurrencyStyle) IsValid() bool {
	for _, v := range AllConcurrencyStyles() {
		if s == v {
			return true
		}
	}
	return false
}

// ValidateEgressIsolation enforces the §13.2 cross-control that pairs a
// pool's egress profile with its isolation profile. An empty egress
// profile is treated as the §13.2 default (`restricted`), which is
// compatible with any isolation profile, so the check fires only when a
// pool explicitly opts into a broader egress profile. The current rule
// rejects the `internet` egress profile on a `standard` (runc) pool: a
// runc pod with broad internet egress is the high-blast-radius
// configuration the §5.3 security note targets and §13.2 forbids.
//
// A non-empty but unrecognised egress profile is rejected so a mistyped
// value fails closed rather than being silently ignored.
//
// spec: §13.2 — "the `internet` profile requires a sandboxed isolation
// profile (`sandboxed` or `microvm`) ... The warm pool controller
// rejects pool configurations that combine `standard` isolation with
// `internet` egress at validation time."
func ValidateEgressIsolation(p Pool) error {
	if p.EgressProfile == "" {
		return nil
	}
	if !egress.IsValid(p.EgressProfile) {
		return errors.New("poolstore: egressProfile is not a recognised §13.2 profile (restricted, provider-direct, internet)")
	}
	// Resolve the effective isolation profile the way the admission path
	// does: an empty profile defaults to the §5.3 production default,
	// which always satisfies the cross-control, so only an explicit
	// `standard` profile can trip it.
	iso := p.IsolationProfile
	if iso == "" {
		iso = isolation.Default()
	}
	if !egress.AllowsIsolation(p.EgressProfile, iso) {
		return errors.New("poolstore: egressProfile=internet requires isolationProfile sandboxed or microvm; standard (runc) is forbidden (§13.2)")
	}
	return nil
}

// ValidateConcurrentConfig enforces the §5.2 / §13.1 admission rules for
// a pool's concurrent-mode configuration. It is the pool-side half of
// the Phase 12c pod-level isolation enforcement: a `concurrent`-mode
// pool cannot be created without the §5.2 deployer acknowledgment, and
// it cannot weaken the cross-tenant boundary that §5.2 reserves to
// task-mode microvm pools.
//
// The rules:
//
//   - A non-concurrent pool must not set concurrent-only fields
//     (concurrencyStyle, maxConcurrent), so a stray field on a
//     session-mode or task-mode pool is rejected rather than silently
//     ignored.
//   - A concurrent pool must name a valid concurrencyStyle (`workspace`
//     or `stateless`).
//   - A concurrent pool must set maxConcurrent >= 1 — the §5.2 per-pod
//     slot bound.
//   - A concurrent pool must never set allowCrossTenantReuse: §5.2
//     gives simultaneous process-level co-tenancy no isolation boundary,
//     so cross-tenant slot sharing is categorically rejected (unlike
//     task mode's microvm option).
//   - A concurrent-workspace pool must set
//     acknowledgeProcessLevelIsolation: §5.2 requires the deployer to
//     accept the shared process namespace, /tmp, cgroup memory, network
//     stack, and credential group-read access between simultaneous
//     slots before the mode is enabled.
//   - A concurrent-workspace pool that sets cleanupTimeoutSeconds must
//     satisfy cleanupTimeoutSeconds >= maxConcurrent * 5 so each slot's
//     per-slot cleanup budget clears the §5.2 5-second floor.
//
// It returns nil for a session-mode or task-mode pool. Callers invoke
// it at the admin-API boundary and surface the error as a §15.1
// VALIDATION_ERROR.
func ValidateConcurrentConfig(p Pool) error {
	if p.ExecutionMode != runtimestore.ExecutionModeConcurrent {
		if p.ConcurrencyStyle != "" {
			return errors.New("poolstore: concurrencyStyle is valid only when executionMode is concurrent (§5.2)")
		}
		if p.MaxConcurrent != 0 {
			return errors.New("poolstore: maxConcurrent is valid only when executionMode is concurrent (§5.2)")
		}
		return nil
	}

	if !p.ConcurrencyStyle.IsValid() {
		return errors.New("poolstore: concurrent-mode pool requires concurrencyStyle to be workspace or stateless (§5.2)")
	}
	if p.MaxConcurrent < 1 {
		return errors.New("poolstore: concurrent-mode pool requires maxConcurrent >= 1 (§5.2)")
	}
	if p.AllowCrossTenantReuse {
		return errors.New("poolstore: allowCrossTenantReuse is not permitted for concurrent-mode pools; " +
			"cross-tenant slot sharing has no isolation boundary in concurrent mode (§5.2)")
	}
	if p.ConcurrencyStyle == ConcurrencyStyleWorkspace {
		if !p.AcknowledgeProcessLevelIsolation {
			return errors.New("poolstore: concurrent-workspace pool requires acknowledgeProcessLevelIsolation=true; " +
				"concurrent slots share the pod process namespace, /tmp, cgroup memory, network stack, " +
				"and credential group-read access (§5.2)")
		}
		if p.CleanupTimeoutSeconds != 0 && p.CleanupTimeoutSeconds < p.MaxConcurrent*5 {
			return errors.New("poolstore: cleanupTimeoutSeconds / maxConcurrent would produce a per-slot " +
				"cleanup timeout below the 5s minimum; set cleanupTimeoutSeconds >= maxConcurrent * 5 (§5.2)")
		}
	}
	return nil
}

// ErrURLModeDomainRequired is the §9.2 line 86 admission rejection
// returned when a pool sets urlModeElicitation.enabled:true with an
// empty or absent domainAllowlist. The admin handler maps it to the
// 400 URL_MODE_ELICITATION_DOMAIN_REQUIRED error code.
var ErrURLModeDomainRequired = errors.New("poolstore: urlModeElicitation.enabled requires a non-empty domainAllowlist (§9.2 URL_MODE_ELICITATION_DOMAIN_REQUIRED)")

// ValidateElicitationPolicy enforces the §9.2 per-pool elicitation
// configuration invariants at store admission. It is checked in
// Create and Update so a misconfigured pool fails at the admin API and
// at the §17.6 bootstrap seed rather than at the first elicitation:
//
//   - urlModeElicitation.enabled:true requires a non-empty
//     domainAllowlist (§9.2 line 86). A pool that enables agent-
//     initiated url-mode elicitation without naming the permitted
//     domains is rejected with ErrURLModeDomainRequired, which the
//     admin handler surfaces as 400 URL_MODE_ELICITATION_DOMAIN_REQUIRED.
//
//   - elicitationDepthPolicy, when set, must be one of the §9.2 line
//     94-96 enum values (`allow_all`, `suppress_at_depth`, `block_all`).
//     An empty value is permitted and resolves to the §9.2 line 92
//     platform default at the dispatcher.
//
// spec: §9.2 lines 86, 90-98.
func ValidateElicitationPolicy(p Pool) error {
	if err := p.URLModeElicitation.Validate(); err != nil {
		return ErrURLModeDomainRequired
	}
	if p.ElicitationDepthPolicy != "" && !p.ElicitationDepthPolicy.IsValid() {
		return errors.New("poolstore: elicitationDepthPolicy is not a recognised §9.2 policy (allow_all, suppress_at_depth, block_all)")
	}
	return nil
}

// ValidateTaskPolicy enforces the §5.2 task-mode taskPolicy invariants
// at admin admission time. The same invariants are re-checked at the CRD
// layer by `lenny-pool-config-validator`; running them here makes a
// misconfigured pool fail at the admin API rather than after the CRD
// write, and it covers the Postgres-only dev posture where the
// admission webhook is not deployed.
//
// The rules (verbatim from §5.2 lines 398-475):
//
//   - A non-task pool must not carry a TaskPolicy (a stray policy on a
//     session-mode or concurrent pool is rejected rather than silently
//     ignored).
//
//   - A task pool must carry a TaskPolicy with
//     AcknowledgeBestEffortScrub: true (§5.2 line 473 "the pool
//     controller rejects the pool definition at validation time").
//
//   - A task pool's TaskPolicy must set MaxTasksPerPod >= 1 (§5.2 line
//     473 "maxTasksPerPod is required with no default — the deployer
//     must make an explicit choice").
//
//   - A task pool's AllowCrossTenantReuse is permitted only when
//     IsolationProfile is microvm (§5.2 line 387). The further §5.2
//     line 396 T4 prohibition is enforced by `ValidateCrossTenantReuseTier`.
//
//   - A task pool's MicrovmScrubMode `in-place` requires
//     AcknowledgeMicrovmResidualState: true (§5.2 line 442).
//
//   - A task pool's MicrovmScrubMode and OnCleanupFailure values, if
//     set, must be on the §5.2 closed enums.
//
// spec: §5.2 lines 398-475.
func ValidateTaskPolicy(p Pool) error {
	if p.ExecutionMode != runtimestore.ExecutionModeTask {
		if p.TaskPolicy != nil {
			return errors.New("poolstore: taskPolicy is valid only when executionMode is task (§5.2)")
		}
		return nil
	}
	tp := p.TaskPolicy
	if tp == nil {
		return errors.New("poolstore: task-mode pool requires taskPolicy with acknowledgeBestEffortScrub: true and maxTasksPerPod set (§5.2 line 473)")
	}
	if !tp.AcknowledgeBestEffortScrub {
		return errors.New("poolstore: task-mode pool requires taskPolicy.acknowledgeBestEffortScrub: true; " +
			"the between-task workspace scrub is best-effort and is not a tenant isolation boundary (§5.2 line 473)")
	}
	if tp.MaxTasksPerPod < 1 {
		return errors.New("poolstore: task-mode pool requires taskPolicy.maxTasksPerPod >= 1; " +
			"it is required with no default so the deployer makes an explicit reuse-limit choice (§5.2 line 473)")
	}
	if p.AllowCrossTenantReuse && p.IsolationProfile != isolation.ProfileMicrovm {
		return errors.New("poolstore: allowCrossTenantReuse is permitted only when isolationProfile is microvm (§5.2 line 387)")
	}
	if tp.MicrovmScrubMode != "" && !tp.MicrovmScrubMode.IsValid() {
		return errors.New("poolstore: taskPolicy.microvmScrubMode is not a recognised §5.2 mode (restart, in-place)")
	}
	if tp.MicrovmScrubMode == runtimestore.MicrovmScrubInPlace && !tp.AcknowledgeMicrovmResidualState {
		return errors.New("poolstore: taskPolicy.microvmScrubMode \"in-place\" requires taskPolicy.acknowledgeMicrovmResidualState: true; " +
			"in-place scrub leaves guest-kernel residual state across tenants (§5.2 line 442)")
	}
	if tp.OnCleanupFailure != "" && !tp.OnCleanupFailure.IsValid() {
		return errors.New("poolstore: taskPolicy.onCleanupFailure is not a recognised §5.2 disposition (warn, fail)")
	}
	if tp.MaxScrubFailures < 0 {
		return errors.New("poolstore: taskPolicy.maxScrubFailures must be >= 0 (§5.2)")
	}
	if tp.MaxPodUptimeSeconds < 0 {
		return errors.New("poolstore: taskPolicy.maxPodUptimeSeconds must be >= 0 (§5.2)")
	}
	if tp.MaxTaskRetries != nil && *tp.MaxTaskRetries < 0 {
		return errors.New("poolstore: taskPolicy.maxTaskRetries must be >= 0 (§5.2 line 412)")
	}
	if tp.CleanupTimeoutSeconds < 0 {
		return errors.New("poolstore: taskPolicy.cleanupTimeoutSeconds must be >= 0 (§5.2)")
	}
	return nil
}

// ValidateCrossTenantReuseTier enforces the §5.2 line 396 T4 cross-tenant
// reuse prohibition: a pool whose associated Runtime is configured with
// workspaceTier: T4 may not set allowCrossTenantReuse, because T4
// workloads require dedicated node pools for per-tenant key isolation
// (§6.4) and cross-tenant pod reuse — even with microvm isolation —
// co-locates two tenants' Restricted data on a shared microvm host.
//
// runtimeTier is the resolved workspace tier of the pool's runtime; the
// caller looks it up (the pure poolstore record carries no runtime).
// The check is a no-op when the pool does not request cross-tenant reuse
// or the runtime is not T4. The error string is verbatim from §5.2 line
// 396 so operators see the exact spec language.
//
// spec: §5.2 line 396 — "The pool controller additionally rejects
// allowCrossTenantReuse: true on any pool whose associated Runtime is
// configured with workspaceTier: T4".
func ValidateCrossTenantReuseTier(p Pool, runtimeTier runtimestore.WorkspaceTier) error {
	if p.AllowCrossTenantReuse && runtimeTier.IsT4() {
		return errors.New("allowCrossTenantReuse: true is not permitted for T4-tier pools " +
			"(workspaceTier: T4); T4 workloads require dedicated node pools (Section 6.4)")
	}
	return nil
}

// ValidatePreConnectExecutionMode enforces the §6.1 lines 77-78
// preConnect/execution-mode compatibility matrix. A runtime that declares
// capabilities.preConnect: true pre-connects a single agent SDK process
// waiting for a single first prompt; concurrent-workspace mode multiplexes
// independent per-slot workspaces onto one pod and concurrent-stateless mode
// has no Lenny-managed agent lifecycle, so neither can host an SDK-warm
// runtime. The check runs at pool admission with the effective post-update
// executionMode and concurrencyStyle; preConnect reports the referenced
// runtime's resolved capabilities.preConnect. An empty concurrencyStyle on a
// concurrent pool resolves to the §5.2 default (workspace).
// spec: §6.1 lines 77-78.
func ValidatePreConnectExecutionMode(preConnect bool, mode runtimestore.ExecutionMode, style ConcurrencyStyle) error {
	if !preConnect || mode != runtimestore.ExecutionModeConcurrent {
		return nil
	}
	if style == ConcurrencyStyleStateless {
		return errors.New("preConnect: true is not supported with executionMode: concurrent, " +
			"concurrencyStyle: stateless; stateless mode has no Lenny-managed agent lifecycle")
	}
	return errors.New("preConnect: true is not supported with executionMode: concurrent, " +
		"concurrencyStyle: workspace; concurrent-workspace mode requires independent per-slot agent initialization")
}

// RuntimePreConnect reports whether rt declares the §5.1 / §6.1
// capabilities.preConnect SDK-warm flag.
func RuntimePreConnect(rt runtimestore.Runtime) bool {
	return rt.Capabilities != nil && rt.Capabilities.PreConnect
}

// Store is the §5.2 pool registry contract.
type Store interface {
	Create(ctx context.Context, p Pool) error
	Get(ctx context.Context, name string) (Pool, error)
	Update(ctx context.Context, name string, mutate func(*Pool) error) (Pool, error)
	List(ctx context.Context, filter ListFilter) ([]Pool, error)
	SoftDelete(ctx context.Context, name string, at time.Time) error
}

// ListFilter narrows the List result.
type ListFilter struct {
	IncludeDeleted bool
	RuntimeRef     string
}

// Sentinel errors.
var (
	ErrNotFound      = errors.New("poolstore: pool not found")
	ErrAlreadyExists = errors.New("poolstore: pool already exists")
)

// namePattern follows the §5.2 pool-name shape — same as
// runtimestore.ValidateName.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,127}$`)

// ValidateName reports whether name satisfies the §5.2 pattern.
func ValidateName(name string) error {
	if name == "" {
		return errors.New("poolstore: name is required")
	}
	if !namePattern.MatchString(name) {
		return errors.New(`poolstore: name must match ^[a-z0-9][a-z0-9_-]{0,127}$`)
	}
	return nil
}

// Memory is the in-memory Store implementation.
type Memory struct {
	mu    sync.RWMutex
	pools map[string]Pool
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory { return &Memory{pools: map[string]Pool{}} }

// Create implements Store.
func (m *Memory) Create(_ context.Context, p Pool) error {
	if err := ValidateName(p.Name); err != nil {
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
	if err := ValidateEgressIsolation(p); err != nil {
		return err
	}
	if err := ValidateConcurrentConfig(p); err != nil {
		return err
	}
	if err := ValidateTaskPolicy(p); err != nil {
		return err
	}
	if err := ValidateElicitationPolicy(p); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.pools[p.Name]; exists {
		return ErrAlreadyExists
	}
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = p.CreatedAt
	}
	if p.Generation == 0 {
		p.Generation = 1
	}
	p.TaskPolicy = p.TaskPolicy.Clone()
	p.URLModeElicitation.DomainAllowlist = cloneAllowlist(p.URLModeElicitation.DomainAllowlist)
	m.pools[p.Name] = p
	return nil
}

// cloneAllowlist returns a copy of the §9.2 url-mode domain allowlist so
// the Memory store never shares the backing slice with a caller.
func cloneAllowlist(in []string) []string {
	if in == nil {
		return nil
	}
	return append([]string(nil), in...)
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, name string) (Pool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	row, ok := m.pools[name]
	if !ok {
		return Pool{}, ErrNotFound
	}
	row.URLModeElicitation.DomainAllowlist = cloneAllowlist(row.URLModeElicitation.DomainAllowlist)
	row.TaskPolicy = row.TaskPolicy.Clone()
	return row, nil
}

// Update implements Store.
func (m *Memory) Update(_ context.Context, name string, mutate func(*Pool) error) (Pool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.pools[name]
	if !ok {
		return Pool{}, ErrNotFound
	}
	prev := row.UpdatedAt
	if err := mutate(&row); err != nil {
		return Pool{}, err
	}
	if row.WarmCount < 0 {
		return Pool{}, errors.New("poolstore: warmCount must be >= 0")
	}
	if row.MaxSessionAgeSeconds < 0 {
		return Pool{}, errors.New("poolstore: maxSessionAgeSeconds must be >= 0")
	}
	if row.IsolationProfile == isolation.ProfileStandard && !row.AllowStandardIsolation {
		return Pool{}, errors.New("poolstore: isolationProfile=standard requires allowStandardIsolation=true (§5.3)")
	}
	if err := ValidateEgressIsolation(row); err != nil {
		return Pool{}, err
	}
	if err := ValidateConcurrentConfig(row); err != nil {
		return Pool{}, err
	}
	if err := ValidateTaskPolicy(row); err != nil {
		return Pool{}, err
	}
	if err := ValidateElicitationPolicy(row); err != nil {
		return Pool{}, err
	}
	now := time.Now().UTC()
	if !now.After(prev) {
		now = prev.Add(time.Nanosecond)
	}
	row.UpdatedAt = now
	row.Generation++
	row.TaskPolicy = row.TaskPolicy.Clone()
	row.URLModeElicitation.DomainAllowlist = cloneAllowlist(row.URLModeElicitation.DomainAllowlist)
	m.pools[name] = row
	out := row
	out.TaskPolicy = row.TaskPolicy.Clone()
	out.URLModeElicitation.DomainAllowlist = cloneAllowlist(row.URLModeElicitation.DomainAllowlist)
	return out, nil
}

// List implements Store.
func (m *Memory) List(_ context.Context, filter ListFilter) ([]Pool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Pool, 0, len(m.pools))
	for _, row := range m.pools {
		if !filter.IncludeDeleted && !row.IsActive() {
			continue
		}
		if filter.RuntimeRef != "" && row.RuntimeRef != filter.RuntimeRef {
			continue
		}
		row.TaskPolicy = row.TaskPolicy.Clone()
		row.URLModeElicitation.DomainAllowlist = cloneAllowlist(row.URLModeElicitation.DomainAllowlist)
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// SoftDelete implements Store.
func (m *Memory) SoftDelete(_ context.Context, name string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.pools[name]
	if !ok {
		return ErrNotFound
	}
	if !row.DeletedAt.IsZero() {
		return nil
	}
	row.DeletedAt = at
	row.UpdatedAt = at
	row.Generation++
	m.pools[name] = row
	return nil
}
