// SPDX-License-Identifier: MIT

// Package checkpoint encodes the §4.4 checkpoint primitives that are
// pure: the Level / Trigger / Outcome / ResumeMode enums, the
// per-level consistency tag, the per-trigger retry budget, the
// workspace-size pre-check (§4.4 "Hard workspace size limit"), and
// the periodic-checkpoint freshness SLO (§4.4 "Checkpoint freshness
// SLO").
//
// The MinIO upload pipeline, the SIGSTOP/SIGCONT embedded-adapter
// path, the eviction Postgres minimal-state fallback, and the
// `lenny-drain-readiness` admission webhook all live in later phases
// that wire this package into the gateway and adapter binaries.
package checkpoint

import (
	"fmt"
	"time"
)

// Level reflects the runtime integration level from §15.4.3, used to
// dispatch the checkpoint quiescence strategy per §4.4.
type Level string

const (
	LevelBasic    Level = "basic"
	LevelStandard Level = "standard"
	LevelFull     Level = "full"
	LevelEmbedded Level = "embedded"
)

// AllLevels returns the closed §4.4 enum.
func AllLevels() []Level {
	return []Level{LevelBasic, LevelStandard, LevelFull, LevelEmbedded}
}

// IsValid reports whether l is one of the four §4.4 levels.
func (l Level) IsValid() bool {
	for _, v := range AllLevels() {
		if l == v {
			return true
		}
	}
	return false
}

// ConsistencyTag is the §4.4 `consistency` field stamped on every
// checkpoint record.
type ConsistencyTag string

const (
	// ConsistencyBestEffort: snapshot was captured without pausing
	// the runtime. Possible workspace inconsistencies on resume.
	ConsistencyBestEffort ConsistencyTag = "best-effort"
	// ConsistencyConsistent: snapshot was captured under a
	// cooperative quiescence handshake (Full level) or a frozen
	// process (embedded adapter), so workspace and session-file state
	// match.
	ConsistencyConsistent ConsistencyTag = "consistent"
)

// ConsistencyForLevel returns the §4.4 mandated consistency tag for a
// checkpoint produced under the given level. Basic and Standard are
// best-effort; Full and Embedded produce consistent checkpoints.
func ConsistencyForLevel(l Level) ConsistencyTag {
	switch l {
	case LevelFull, LevelEmbedded:
		return ConsistencyConsistent
	default:
		return ConsistencyBestEffort
	}
}

// Trigger is the §4.4 checkpoint-trigger enum. Each trigger has a
// distinct retry budget and post-failure behaviour.
type Trigger string

const (
	// TriggerPeriodic: scheduled periodic checkpoint
	// (`periodicCheckpointIntervalSeconds`).
	TriggerPeriodic Trigger = "periodic"
	// TriggerPreScaleDown: pre-scale-down checkpoint taken before a
	// PoolScalingController-initiated pod release.
	TriggerPreScaleDown Trigger = "pre_scale_down"
	// TriggerEviction: preStop-hook checkpoint at pod termination.
	TriggerEviction Trigger = "eviction"
)

// AllTriggers returns the closed §4.4 enum.
func AllTriggers() []Trigger {
	return []Trigger{TriggerPeriodic, TriggerPreScaleDown, TriggerEviction}
}

// IsValid reports whether t is one of the §4.4 triggers.
func (t Trigger) IsValid() bool {
	for _, v := range AllTriggers() {
		if t == v {
			return true
		}
	}
	return false
}

// IsEviction reports whether t belongs to the §4.4 eviction-retry-
// budget path. Eviction triggers get the longer 30-second budget;
// non-eviction triggers get ~5 seconds.
func (t Trigger) IsEviction() bool { return t == TriggerEviction }

// RetryBudget returns the §4.4 retry parameters for this trigger.
// Initial delay, the per-attempt max delay (cap), and the total
// budget across all retries.
type RetryBudget struct {
	Initial     time.Duration
	Cap         time.Duration
	TotalBudget time.Duration
}

// RetryBudgetFor returns the §4.4 retry parameters for the given
// trigger. Non-eviction: 200ms initial, ~5s total. Eviction: 500ms
// initial, 5s cap, 30s total.
func RetryBudgetFor(t Trigger) RetryBudget {
	if t.IsEviction() {
		return RetryBudget{
			Initial:     500 * time.Millisecond,
			Cap:         5 * time.Second,
			TotalBudget: 30 * time.Second,
		}
	}
	return RetryBudget{
		Initial:     200 * time.Millisecond,
		Cap:         5 * time.Second,
		TotalBudget: 5 * time.Second,
	}
}

// RetryBudgetForFallback returns the §4.4 line 277 retry parameters for
// the eviction Postgres minimal-state fallback write. The budget covers
// the managed Postgres failover window (RDS Multi-AZ, Cloud SQL HA:
// typically 15–30 s) that can prevent the first write attempt from
// succeeding. The total budget is 60 s, fitting comfortably within
// terminationGracePeriodSeconds (240 s at Tier 1/2, 300 s at Tier 3)
// with ample headroom for stream drain after the Postgres write
// completes or the budget is exhausted. Per-attempt cap is 5 s; initial
// delay is 500 ms; growth factor is 2x (encoded by the caller's
// backoff helper).
//
// spec: §4.4 line 277 — "The Postgres write is retried with exponential
// backoff (initial 500ms, factor 2x, capped at 5s per attempt) for up
// to 60 seconds total."
func RetryBudgetForFallback() RetryBudget {
	return RetryBudget{
		Initial:     500 * time.Millisecond,
		Cap:         5 * time.Second,
		TotalBudget: 60 * time.Second,
	}
}

// CheckpointTimeout is the §4.4 60-second timeout for the
// quiescence-to-completion window. Applies to every checkpoint path.
const CheckpointTimeout = 60 * time.Second

// Outcome is the §4.4 outcome enum for completed checkpoint records.
type Outcome string

const (
	// OutcomeSuccess: snapshot + metadata committed atomically.
	OutcomeSuccess Outcome = "success"
	// OutcomeFailed: checkpoint did not produce a usable record.
	OutcomeFailed Outcome = "failed"
	// OutcomeAborted: checkpoint was aborted (watchdog, adapter
	// crash, or external cancellation). MinIO objects that were
	// uploaded are cleaned up per §4.4 "Checkpoint abort cleanup".
	OutcomeAborted Outcome = "aborted"
	// OutcomePartial: eviction-path partial-manifest record per §4.4
	// "Exception — preStop timeout partial manifest" and §10.1
	// "Partial manifest on checkpoint timeout".
	OutcomePartial Outcome = "partial"
)

// AllOutcomes returns the closed §4.4 enum.
func AllOutcomes() []Outcome {
	return []Outcome{OutcomeSuccess, OutcomeFailed, OutcomeAborted, OutcomePartial}
}

// IsValid reports whether o is one of the §4.4 outcomes.
func (o Outcome) IsValid() bool {
	for _, v := range AllOutcomes() {
		if o == v {
			return true
		}
	}
	return false
}

// IsTerminal reports whether o is a final, persisted outcome. Aborted
// and Failed are terminal but never produce a valid record; Success
// and Partial both persist (though Partial is a recovery-aid record,
// not a valid checkpoint).
func (o Outcome) IsTerminal() bool { return o.IsValid() }

// ResumeMode is the §4.4 / §7.2 mode the gateway selects when
// resuming a session from a checkpoint. Surfaced to clients on the
// `session.resumed` event.
type ResumeMode string

const (
	// ResumeFull: resumed from a successful full checkpoint with both
	// workspace and conversation state intact.
	ResumeFull ResumeMode = "full"
	// ResumePartialWorkspace: resumed from a partial-manifest record;
	// some workspace chunks were recovered, others were not.
	ResumePartialWorkspace ResumeMode = "partial_workspace"
	// ResumeConversationOnly: resumed from the Postgres minimal-state
	// fallback; workspace files are gone. Client receives
	// workspaceLost: true.
	ResumeConversationOnly ResumeMode = "conversation_only"
	// ResumeCoordinatorHandoff: resumed under a new coordinator
	// without losing the workspace (per §10.4 coordinator handoff).
	ResumeCoordinatorHandoff ResumeMode = "coordinator_handoff"
)

// AllResumeModes returns the closed enum.
func AllResumeModes() []ResumeMode {
	return []ResumeMode{ResumeFull, ResumePartialWorkspace, ResumeConversationOnly, ResumeCoordinatorHandoff}
}

// IsValid reports whether m is one of the §4.4 / §7.2 resume modes.
func (m ResumeMode) IsValid() bool {
	for _, v := range AllResumeModes() {
		if m == v {
			return true
		}
	}
	return false
}

// WorkspaceLost reports whether this resume mode implies workspace
// data was lost. Used by the gateway to compute the `workspaceLost`
// field on the §7.2 session.resumed event.
func (m ResumeMode) WorkspaceLost() bool { return m == ResumeConversationOnly }

// WorkspaceSizePreCheck implements the §4.4 "Hard workspace size
// limit" pre-checkpoint probe. Returns nil when the workspace is
// within the limit and *WorkspaceSizeExceededError otherwise. The
// adapter calls this immediately before quiescing the runtime; on
// rejection the checkpoint is aborted and a checkpoint.skipped event
// is emitted with reason workspace_size_limit.
func WorkspaceSizePreCheck(workspaceBytes, limitBytes int64) error {
	if workspaceBytes < 0 || limitBytes <= 0 {
		return nil // unconfigured limit; the kubelet emptyDir guard is the backstop
	}
	if workspaceBytes > limitBytes {
		return &WorkspaceSizeExceededError{
			WorkspaceBytes: workspaceBytes,
			LimitBytes:     limitBytes,
		}
	}
	return nil
}

// WorkspaceSizeExceededError carries the §4.4 workspace_size_limit
// abort details. The adapter surfaces this on the checkpoint.skipped
// event.
type WorkspaceSizeExceededError struct {
	WorkspaceBytes int64
	LimitBytes     int64
}

func (e *WorkspaceSizeExceededError) Error() string {
	return fmt.Sprintf("checkpoint: workspace bytes %d exceeds limit %d (§4.4 hard workspace size limit)", e.WorkspaceBytes, e.LimitBytes)
}

// FreshnessCheck implements the §4.4 "Checkpoint freshness SLO": a
// session is fresh when its last successful checkpoint age is at most
// periodicCheckpointIntervalSeconds. Returns nil when fresh and
// *StaleCheckpointError otherwise (used by the gateway to bump the
// lenny_checkpoint_stale_sessions gauge and trigger the
// CheckpointStale §16.5 alert).
func FreshnessCheck(now, lastCheckpoint time.Time, interval time.Duration) error {
	if lastCheckpoint.IsZero() {
		// The session has never had a successful checkpoint; treat as
		// stale unless interval is zero (unbounded freshness, used in
		// tests / dev mode).
		if interval <= 0 {
			return nil
		}
		return &StaleCheckpointError{AgeSeconds: -1, IntervalSeconds: int(interval / time.Second)}
	}
	if interval <= 0 {
		return nil
	}
	age := now.Sub(lastCheckpoint)
	if age > interval {
		return &StaleCheckpointError{
			AgeSeconds:      int(age / time.Second),
			IntervalSeconds: int(interval / time.Second),
		}
	}
	return nil
}

// StaleCheckpointError captures the §4.4 freshness-SLO violation. The
// gateway uses Age and Interval to populate the per-pool/per-level
// `lenny_checkpoint_stale_sessions` gauge labels.
type StaleCheckpointError struct {
	AgeSeconds      int
	IntervalSeconds int
}

func (e *StaleCheckpointError) Error() string {
	if e.AgeSeconds < 0 {
		return fmt.Sprintf("checkpoint: session has never been checkpointed; interval %ds (§4.4 freshness SLO)", e.IntervalSeconds)
	}
	return fmt.Sprintf("checkpoint: last checkpoint is %ds old, exceeds interval %ds (§4.4 freshness SLO)", e.AgeSeconds, e.IntervalSeconds)
}
