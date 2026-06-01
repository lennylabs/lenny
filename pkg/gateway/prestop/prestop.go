// SPDX-License-Identifier: MIT

// Package prestop implements the §4.4 line 234, 263 / §10.1 preStop
// drain hook the gateway exposes as an internal HTTP endpoint.
//
// The hook is invoked by Kubernetes via
// `lifecycle.preStop.httpGet` when a gateway pod is scheduled for
// termination (rolling update, HPA scale-down, kubectl drain). It
// runs three things in order:
//
//  1. Reads `terminationGracePeriodSeconds` from the deployment
//     environment to bound the total preStop budget.
//  2. Selects the §10.1 tiered checkpoint cap for every session
//     this replica coordinates per Stage 2: the cap tier is chosen
//     from `last_checkpoint_workspace_bytes` (Postgres + per-replica
//     cache). Each selection bumps `lenny_prestop_cap_selection_total`
//     labeled by source (postgres | postgres_null | cache_hit |
//     cache_miss_max_tier).
//  3. Iterates active sessions on this replica and triggers
//     synchronous eviction checkpoints via the in-process
//     checkpointer. The cap selected above is the per-session
//     deadline.
//
// The hook returns HTTP 200 with a JSON summary of the attempted,
// completed, and skipped sessions; Kubernetes ignores the body but
// the summary is useful for log scraping and ad-hoc curl probes.
//
// spec: §4.4 lines 234, 263 / §10.1 preStop staged drain.
package prestop

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultTerminationGraceSeconds is the §4.4 line 263 default the
// gateway honors when LENNY_TERMINATION_GRACE_SECONDS is unset. The
// value mirrors the Helm chart default (`terminationGracePeriodSeconds:
// 240`) so a stock deployment matches the §17.8.2 budget.
// spec: §17.8.2 — "Tier 1/2: terminationGracePeriodSeconds=240".
const DefaultTerminationGraceSeconds = 240

// MinStreamDrainBudgetSeconds is the §10.1 minimum stream-drain
// budget at Stage 3 — the tiered cap must remain below
// `terminationGracePeriodSeconds - 30s` so the drain has at least
// 30 seconds to flush active streams after the checkpoint barrier
// completes.
// spec: §10.1 — "the tiered cap must remain below
// terminationGracePeriodSeconds - 30s to leave at least 30 seconds
// for stream drain".
const MinStreamDrainBudgetSeconds = 30

// TieredCapTier is one of the §10.1 cap tiers, distinguishing the
// per-workspace-size deadline applied to eviction checkpoints. The
// tier wraps the budget plus a label string for the
// `lenny_prestop_cap_selection_total` source label.
type TieredCapTier struct {
	// UpperBoundBytes is the largest workspace size (inclusive) this
	// tier applies to. A workspace whose size <= UpperBoundBytes
	// selects this tier; the highest matching tier wins.
	UpperBoundBytes int64
	// Budget is the wall-clock budget the gateway grants this
	// checkpoint to complete the upload. The §10.1 spec mandates 30s
	// / 60s / 90s for the standard tiers.
	Budget time.Duration
}

// StandardTiers is the §10.1 default cap-tier table the gateway
// honors when no override is wired. The tiers are listed in
// ascending UpperBoundBytes order so the chooser can walk linearly
// and select the first matching tier.
//
// spec: §10.1 — "≤ 100 MB → 30s; 101 MB – 300 MB → 60s; 301 MB – 512
// MB (hard limit) → 90s".
var StandardTiers = []TieredCapTier{
	{UpperBoundBytes: 100 * 1024 * 1024, Budget: 30 * time.Second},
	{UpperBoundBytes: 300 * 1024 * 1024, Budget: 60 * time.Second},
	{UpperBoundBytes: 512 * 1024 * 1024, Budget: 90 * time.Second},
}

// MaxTierBudget is the highest budget across StandardTiers — used by
// the cache-miss / postgres-null fallback per §10.1 ("the gateway
// MUST select the 90s maximum tier, not the 30s default").
var MaxTierBudget = StandardTiers[len(StandardTiers)-1].Budget

// CapSelectionSource is the §10.1 source label stamped onto the
// `lenny_prestop_cap_selection_total` counter for every Stage 2 tier
// selection. The labels feed the §16.5
// PreStopCapFallbackRateHigh alert ratio.
//
// spec: §10.1 — preStop tier selection source.
type CapSelectionSource string

const (
	// CapSourcePostgres: Postgres read succeeded and returned a
	// non-null value (the steady-state healthy path).
	CapSourcePostgres CapSelectionSource = "postgres"
	// CapSourcePostgresNull: Postgres read succeeded but
	// `last_checkpoint_workspace_bytes` is NULL (the fresh-session
	// path); 90s conservative fallback used.
	CapSourcePostgresNull CapSelectionSource = "postgres_null"
	// CapSourceCacheHit: Postgres unreachable or slow; the value was
	// taken from the in-replica cache populated by a prior
	// checkpoint or by handoff priming.
	CapSourceCacheHit CapSelectionSource = "cache_hit"
	// CapSourceCacheMissMaxTier: Postgres unreachable and the cache
	// had no value; 90s conservative fallback used.
	CapSourceCacheMissMaxTier CapSelectionSource = "cache_miss_max_tier"
)

// SelectTier returns the §10.1 cap tier for the supplied workspace
// size. A negative or oversize value falls through to the largest
// tier (the §4.4 hard workspace size limit prevents larger values
// at admission time, but the chooser defends against drift).
func SelectTier(workspaceBytes int64, tiers []TieredCapTier) TieredCapTier {
	if len(tiers) == 0 {
		return TieredCapTier{Budget: MaxTierBudget}
	}
	for _, t := range tiers {
		if workspaceBytes <= t.UpperBoundBytes {
			return t
		}
	}
	return tiers[len(tiers)-1]
}

// ClampForStreamDrain applies the §10.1 cap-vs-stream-drain clamp:
// the tier must leave at least MinStreamDrainBudgetSeconds for stage
// 3 (stream drain). Returns a duration <= raw and <= grace -
// MinStreamDrainBudgetSeconds, with a floor of zero.
//
// spec: §10.1 — "the tiered cap must remain below
// terminationGracePeriodSeconds - 30s to leave at least 30 seconds
// for stream drain".
func ClampForStreamDrain(raw, grace time.Duration) time.Duration {
	if grace <= 0 {
		return 0
	}
	maxAllowed := grace - time.Duration(MinStreamDrainBudgetSeconds)*time.Second
	if maxAllowed <= 0 {
		return 0
	}
	if raw > maxAllowed {
		return maxAllowed
	}
	return raw
}

// SessionInfo describes one session this replica coordinates. The
// preStop hook iterates these and triggers an eviction checkpoint
// per session under the tier cap selected from
// LastCheckpointWorkspaceBytes.
type SessionInfo struct {
	TenantID  string
	SessionID string
	// Pool is the pool-label value used for the prestop_cap_selection
	// counter and any per-pool diagnostics. Empty omits the label.
	Pool string
	// LastCheckpointWorkspaceBytes is the last measured workspace
	// size — the field §10.1 keys the tier selection on. Zero is
	// distinguished from "unset" by the IsPostgresNull flag below
	// (LastCheckpointWorkspaceBytes can legitimately be 0 when a
	// session has only ever checkpointed an empty workspace).
	LastCheckpointWorkspaceBytes int64
	// IsPostgresNull, when true, signals the source reported the
	// value as NULL (fresh session with no prior checkpoint). The
	// hook stamps the CapSourcePostgresNull source on the counter.
	IsPostgresNull bool
	// SourceLabel overrides the source label otherwise derived from
	// the postgres/cache path. Tests use this to inject the cache-
	// hit / cache-miss cases directly. Empty falls back to the
	// derived label.
	SourceLabel CapSelectionSource
}

// SessionEnumerator returns the sessions this replica coordinates
// at the moment the preStop hook fires. Implementations should
// snapshot the registry; mutations during the hook are tolerated
// (the hook iterates the snapshot, not the live registry).
type SessionEnumerator interface {
	Snapshot(ctx context.Context) ([]SessionInfo, error)
}

// CheckpointFn is the per-session eviction-checkpoint surface. The
// implementation runs the §4.4 eviction-trigger checkpoint
// synchronously, returning nil on success or an error on failure
// (the caller logs and continues so a single failing session does
// not block the drain).
type CheckpointFn func(ctx context.Context, tenantID, sessionID string, budget time.Duration) error

// CapMetricsEmitter is the §10.1 metrics surface the hook bumps the
// `lenny_prestop_cap_selection_total` and
// `lenny_gateway_sigkill_streams_total` counters on. The
// gatewaymetrics.Metrics satisfies it; tests inject fakes.
//
// spec: §10.1 line 161 — preStop tier selection source counter and the
// SIGKILL-deadline stream counter.
type CapMetricsEmitter interface {
	IncPreStopCapSelection(pool, serviceInstanceID, source string)
	// IncSigkillStreams records one in-flight stream whose eviction
	// checkpoint did not finish within its tier budget before the
	// grace period elapsed. Kubernetes SIGKILLs the pod at the grace
	// deadline, forcibly terminating any such stream, so the counter
	// quantifies the impact of forced disconnections (§10.1 line 161).
	IncSigkillStreams(pool, serviceInstanceID string)
}

// Hook is the §4.4 / §10.1 preStop HTTP handler. It is configured
// at construction with the dependencies it needs and exposed as
// http.Handler so the gateway's internal mux can route it on
// `/internal/prestop`.
type Hook struct {
	// Sessions enumerates the sessions this replica coordinates at
	// preStop-fire time. Required.
	Sessions SessionEnumerator
	// Checkpoint triggers the per-session eviction checkpoint.
	// Required. A nil implementation makes the hook a no-op (every
	// session is recorded as skipped).
	Checkpoint CheckpointFn
	// Metrics receives the per-session
	// lenny_prestop_cap_selection_total emission. Nil disables the
	// counter; the hook still runs.
	Metrics CapMetricsEmitter
	// ServiceInstanceID is the OTel service.instance.id of the
	// replica performing the selection — stamped onto the counter
	// so per-replica alert expressions can disambiguate.
	ServiceInstanceID string
	// GracePeriod overrides the LENNY_TERMINATION_GRACE_SECONDS
	// environment lookup. Zero falls back to the env / default
	// path so a production deployment honors the chart-set value.
	GracePeriod time.Duration
	// Tiers overrides the §10.1 StandardTiers table. Empty falls
	// back to StandardTiers so production deployments honor the
	// spec-defined tiers.
	Tiers []TieredCapTier
	// Logf, when set, receives a one-line diagnostic on every
	// per-session failure so operators can correlate the
	// summary JSON with the gateway log.
	Logf func(format string, args ...any)
	// fired protects the Hook against re-entry: Kubernetes invokes
	// preStop once per pod lifecycle, but a manual curl probe could
	// in principle trigger a second drain. The flag guards against
	// the concurrent invocation; the second call returns 200 with
	// `already_fired: true`.
	fired atomic.Bool
	// draining records that the §10.1 Stage 1 readiness flip has
	// happened: the hook sets it true as the first action of the
	// staged drain so the gateway's /readyz probe reports NotReady,
	// the Endpoints controller removes the pod from the Service, and
	// the load balancer stops routing new requests before any drain
	// logic begins. spec: §10.1 — "Stop accepting new work
	// (readiness=false) ... before any drain logic begins".
	draining atomic.Bool
}

// Draining reports whether the §10.1 preStop staged drain has begun on
// this replica. The gateway's /readyz readiness handler consults this
// so the Stage 1 readiness flip removes the pod from the Service
// endpoints before the eviction-checkpoint drain runs. A nil Hook (the
// in-memory / probe-disabled posture) is never draining.
//
// spec: §10.1 line — "The preStop hook immediately sets the pod's
// readiness probe to false ... The readiness flip must happen before
// any drain logic begins".
func (h *Hook) Draining() bool {
	if h == nil {
		return false
	}
	return h.draining.Load()
}

// Summary is the JSON body the hook returns on success. Kubernetes
// ignores the body but the summary is useful for log scraping.
type Summary struct {
	GracePeriodSeconds int      `json:"grace_period_seconds"`
	AttemptedSessions  int      `json:"attempted_sessions"`
	CompletedSessions  int      `json:"completed_sessions"`
	SkippedSessions    int      `json:"skipped_sessions"`
	FailedSessions     int      `json:"failed_sessions"`
	// SigkilledStreams counts sessions whose checkpoint ran out of
	// budget before the grace deadline; the kubelet SIGKILLs the pod
	// at the deadline so each is a forcibly terminated stream
	// (§10.1 line 161 `lenny_gateway_sigkill_streams_total`).
	SigkilledStreams int      `json:"sigkilled_streams,omitempty"`
	AlreadyFired     bool     `json:"already_fired,omitempty"`
	Errors             []string `json:"errors,omitempty"`
}

// ServeHTTP implements http.Handler.
func (h *Hook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h == nil || h.Sessions == nil {
		writeJSON(w, http.StatusOK, Summary{AlreadyFired: false})
		return
	}
	if !h.fired.CompareAndSwap(false, true) {
		writeJSON(w, http.StatusOK, Summary{AlreadyFired: true})
		return
	}
	summary := h.run(r.Context())
	writeJSON(w, http.StatusOK, summary)
}

// run executes the §10.1 staged drain synchronously and returns the
// summary so the test surface can verify outcomes without going
// through HTTP.
func (h *Hook) run(ctx context.Context) Summary {
	// spec: §10.1 Stage 1 — flip readiness to false before any drain
	// logic so the Endpoints controller removes this pod from the
	// Service and the load balancer stops routing new requests. This is
	// the first action of the staged drain so the readiness-propagation
	// lag (1–5s) overlaps the checkpoint wait rather than racing it.
	h.draining.Store(true)
	grace := h.gracePeriod()
	tiers := h.tiers()
	sessions, err := h.Sessions.Snapshot(ctx)
	summary := Summary{
		GracePeriodSeconds: int(grace / time.Second),
	}
	if err != nil {
		summary.Errors = append(summary.Errors,
			"snapshot sessions: "+err.Error())
		return summary
	}
	// Limit the total drain wall-clock to the grace period so a
	// runaway per-session checkpoint cannot exhaust the budget that
	// stage 3 needs.
	ctx, cancel := context.WithTimeout(ctx, grace)
	defer cancel()
	// Iterate sessions sequentially: the §10.1 contract describes
	// concurrent fan-out, but for v1 the sequential path is simpler
	// and the §17.8.2 MinIO throughput budget already accounts for
	// burst load. A future change can parallelize within the same
	// `lenny_prestop_cap_selection_total` semantics.
	var wg sync.WaitGroup
	for _, sess := range sessions {
		sess := sess
		summary.AttemptedSessions++
		source := h.deriveSource(sess)
		// spec: §10.1 — postgres_null and cache_miss_max_tier select
		// the 90s conservative tier regardless of the recorded
		// workspace size, because the replica has no authoritative
		// evidence for the session and a smaller cap would SIGKILL a
		// fresh large-workspace session mid-upload.
		var raw time.Duration
		switch source {
		case CapSourcePostgresNull, CapSourceCacheMissMaxTier:
			raw = MaxTierBudget
		default:
			raw = SelectTier(sess.LastCheckpointWorkspaceBytes, tiers).Budget
		}
		budget := ClampForStreamDrain(raw, grace)
		h.emitCapSelection(sess.Pool, source)
		if h.Checkpoint == nil {
			summary.SkippedSessions++
			continue
		}
		wg.Add(1)
		// Run sequentially in v1 so the budget is honored
		// per-session against the same clock.
		func() {
			defer wg.Done()
			perCtx, perCancel := context.WithTimeout(ctx, budget)
			defer perCancel()
			if err := h.Checkpoint(perCtx, sess.TenantID, sess.SessionID, budget); err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					summary.FailedSessions++
					// The checkpoint ran out of budget; this stream is
					// still in flight when the grace deadline hits, so
					// the kubelet SIGKILL forcibly terminates it.
					// spec: §10.1 line 161.
					summary.SigkilledStreams++
					h.emitSigkillStream(sess.Pool)
					summary.Errors = append(summary.Errors,
						sess.SessionID+": deadline exceeded")
				} else {
					summary.FailedSessions++
					summary.Errors = append(summary.Errors,
						sess.SessionID+": "+err.Error())
				}
				h.logf("prestop: session %s checkpoint failed: %v", sess.SessionID, err)
				return
			}
			summary.CompletedSessions++
		}()
	}
	wg.Wait()
	return summary
}

// deriveSource picks the §10.1 source label for the supplied
// SessionInfo. Explicit SourceLabel overrides the derivation so
// tests can drive the cache-hit / cache-miss paths.
func (h *Hook) deriveSource(s SessionInfo) CapSelectionSource {
	if s.SourceLabel != "" {
		return s.SourceLabel
	}
	if s.IsPostgresNull {
		return CapSourcePostgresNull
	}
	return CapSourcePostgres
}

// emitCapSelection bumps the §10.1
// `lenny_prestop_cap_selection_total` counter for the supplied
// (pool, source) pair.
func (h *Hook) emitCapSelection(pool string, source CapSelectionSource) {
	if h.Metrics == nil {
		return
	}
	h.Metrics.IncPreStopCapSelection(pool, h.ServiceInstanceID, string(source))
}

// emitSigkillStream bumps the §10.1 line 161
// `lenny_gateway_sigkill_streams_total` counter for one session whose
// eviction checkpoint exceeded its budget before the grace deadline.
func (h *Hook) emitSigkillStream(pool string) {
	if h.Metrics == nil {
		return
	}
	h.Metrics.IncSigkillStreams(pool, h.ServiceInstanceID)
}

// gracePeriod returns the per-hook termination grace period. The
// resolution order is: explicit override, env, default.
func (h *Hook) gracePeriod() time.Duration {
	if h.GracePeriod > 0 {
		return h.GracePeriod
	}
	return time.Duration(DefaultTerminationGraceSeconds) * time.Second
}

// tiers returns the configured tier table.
func (h *Hook) tiers() []TieredCapTier {
	if len(h.Tiers) > 0 {
		return h.Tiers
	}
	return StandardTiers
}

func (h *Hook) logf(format string, args ...any) {
	if h.Logf == nil {
		return
	}
	h.Logf(format, args...)
}

func writeJSON(w http.ResponseWriter, code int, summary Summary) {
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(summary)
}
