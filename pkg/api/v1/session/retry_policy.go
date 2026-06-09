// SPDX-License-Identifier: MIT

package session

import (
	"fmt"
	"sort"
)

// RetryPolicy is the §7.3 client-supplied retry policy carried on
// CreateSession. Each field is independently optional: the gateway
// fills missing values from the deployer defaults at admission and
// clamps every populated value against the deployer caps so a client
// cannot grow its budget past the platform's published bounds.
//
// The §7.3 line 381 worked example:
//
//	{
//	  "mode": "auto_then_client",
//	  "maxRetries": 2,
//	  "retryableFailures": ["pod_evicted", "node_lost", "runtime_crash"],
//	  "nonRetryableFailures": ["workspace_validation_failed", "setup_command_failed"],
//	  "maxSessionAgeSeconds": 7200,
//	  "maxResumeWindowSeconds": 900
//	}
//
// spec: §7.3 lines 377-393.
type RetryPolicy struct {
	// Mode governs whether the gateway runs the auto-retry path before
	// surfacing the failure to the client. The §7.3 worked example pins
	// "auto_then_client" as the default; "client_only" disables the
	// auto-retry chain so every failure surfaces immediately as
	// awaiting_client_action.
	Mode RetryMode `json:"mode,omitempty"`

	// MaxRetries is the §7.3 auto-retry budget: the number of automatic
	// pod-recovery attempts the gateway runs before declaring the
	// session awaiting_client_action. Zero (the wire default) selects
	// the deployer's DefaultMaxRetries.
	MaxRetries int `json:"maxRetries,omitempty"`

	// RetryableFailures enumerates the §7.3 failure classes the auto-
	// retry path applies to. An empty slice selects the §7.3 line 384
	// platform default. Unknown values are preserved verbatim so a
	// later platform version can extend the catalog without a client-
	// side migration.
	RetryableFailures []string `json:"retryableFailures,omitempty"`

	// NonRetryableFailures enumerates the §7.3 failure classes that
	// bypass the auto-retry path and go straight to awaiting_client_action.
	// An empty slice selects the §7.3 line 385 platform default.
	NonRetryableFailures []string `json:"nonRetryableFailures,omitempty"`

	// MaxSessionAgeSeconds is the §7.3 per-session total-lifetime cap.
	// The gateway clamps the value against the deployer's matching cap
	// (typically the watchdog's MaxSessionAgeSeconds) so the platform
	// bound is the upper limit. Zero selects the deployer default.
	MaxSessionAgeSeconds int `json:"maxSessionAgeSeconds,omitempty"`

	// MaxResumeWindowSeconds is the §7.3 line 404 resume-window timer
	// that bounds how long a session may sit in resume_pending while
	// the gateway tries to allocate a fresh warm pod before falling
	// back to awaiting_client_action. Zero selects the deployer default.
	MaxResumeWindowSeconds int `json:"maxResumeWindowSeconds,omitempty"`
}

// RetryMode is the closed §7.3 enum on RetryPolicy.Mode.
type RetryMode string

const (
	// RetryModeAutoThenClient is the §7.3 line 382 default: the
	// gateway runs up to maxRetries automatic recovery attempts before
	// surfacing the failure to the client via awaiting_client_action.
	RetryModeAutoThenClient RetryMode = "auto_then_client"
	// RetryModeClientOnly disables auto-retry: every failure surfaces
	// to the client immediately as awaiting_client_action.
	RetryModeClientOnly RetryMode = "client_only"
)

// DefaultRetryMode is the §7.3 line 382 worked-example default.
const DefaultRetryMode = RetryModeAutoThenClient

// AllRetryModes returns the closed §7.3 mode enum in declaration order.
func AllRetryModes() []RetryMode {
	return []RetryMode{RetryModeAutoThenClient, RetryModeClientOnly}
}

// IsValid reports whether m is a known §7.3 retry mode.
func (m RetryMode) IsValid() bool {
	for _, v := range AllRetryModes() {
		if m == v {
			return true
		}
	}
	return false
}

// Resolve returns m when it is valid; otherwise the §7.3 default.
func (m RetryMode) Resolve() RetryMode {
	if m.IsValid() {
		return m
	}
	return DefaultRetryMode
}

// RetryPolicyCaps is the deployer-supplied set of upper bounds the
// gateway applies to a client-supplied RetryPolicy. Each field is the
// hard ceiling: a client value above the cap is clamped down; a
// zero/unset client value falls through to the cap as the effective
// value.
//
// spec: §7.3 line 377 — "Retry policy is set per session by the
// client, bounded by deployer caps".
type RetryPolicyCaps struct {
	// MaxRetries is the deployer's upper bound on RetryPolicy.MaxRetries.
	MaxRetries int
	// MaxSessionAgeSeconds is the deployer's upper bound on
	// RetryPolicy.MaxSessionAgeSeconds (mirrors the watchdog's same-named
	// platform-wide cap).
	MaxSessionAgeSeconds int
	// MaxResumeWindowSeconds is the deployer's upper bound on
	// RetryPolicy.MaxResumeWindowSeconds.
	MaxResumeWindowSeconds int
}

// ValidateRetryPolicy is the gateway-side admission check. It rejects
// negative values with a structured error citing the §7.3 field; the
// caller maps the error to its 400 envelope. Modes are restricted to
// the closed enum; unknown values reject.
//
// spec: §7.3 lines 377-393.
func ValidateRetryPolicy(p *RetryPolicy) error {
	if p == nil {
		return nil
	}
	if p.Mode != "" && !p.Mode.IsValid() {
		return &RetryPolicyValidationError{Field: "mode", Reason: fmt.Sprintf("unknown mode %q; allowed: %v", p.Mode, modeStrings())}
	}
	if p.MaxRetries < 0 {
		return &RetryPolicyValidationError{Field: "maxRetries", Reason: "must be non-negative"}
	}
	if p.MaxSessionAgeSeconds < 0 {
		return &RetryPolicyValidationError{Field: "maxSessionAgeSeconds", Reason: "must be non-negative"}
	}
	if p.MaxResumeWindowSeconds < 0 {
		return &RetryPolicyValidationError{Field: "maxResumeWindowSeconds", Reason: "must be non-negative"}
	}
	return nil
}

// ClampRetryPolicy returns the effective per-session policy after
// applying caps. A nil input yields the deployer default policy (the
// cap values themselves act as the effective values when the client
// supplied nothing). A zero cap field skips that clamp so deployer
// "unlimited" semantics survive.
//
// The mode is normalised to DefaultRetryMode when unset or unknown,
// matching RetryMode.Resolve().
//
// Failure lists are not clamped — they are advisory category lists the
// auto-retry path consults; the deployer admission gates entries via
// the runtime-registry policy at registration time, not here.
//
// spec: §7.3 lines 377-393.
func ClampRetryPolicy(p *RetryPolicy, caps RetryPolicyCaps) RetryPolicy {
	out := RetryPolicy{Mode: DefaultRetryMode}
	if p != nil {
		out = *p
		out.RetryableFailures = append([]string(nil), p.RetryableFailures...)
		out.NonRetryableFailures = append([]string(nil), p.NonRetryableFailures...)
	}
	out.Mode = out.Mode.Resolve()
	if caps.MaxRetries > 0 {
		if out.MaxRetries <= 0 || out.MaxRetries > caps.MaxRetries {
			out.MaxRetries = caps.MaxRetries
		}
	}
	if caps.MaxSessionAgeSeconds > 0 {
		if out.MaxSessionAgeSeconds <= 0 || out.MaxSessionAgeSeconds > caps.MaxSessionAgeSeconds {
			out.MaxSessionAgeSeconds = caps.MaxSessionAgeSeconds
		}
	}
	if caps.MaxResumeWindowSeconds > 0 {
		if out.MaxResumeWindowSeconds <= 0 || out.MaxResumeWindowSeconds > caps.MaxResumeWindowSeconds {
			out.MaxResumeWindowSeconds = caps.MaxResumeWindowSeconds
		}
	}
	return out
}

// RetryPolicyValidationError is returned by ValidateRetryPolicy. It
// carries the offending field so the §15.1 error envelope can surface
// details.field per the §7.3 admission contract.
type RetryPolicyValidationError struct {
	Field  string
	Reason string
}

func (e *RetryPolicyValidationError) Error() string {
	return fmt.Sprintf("retryPolicy.%s: %s", e.Field, e.Reason)
}

func modeStrings() []string {
	out := make([]string, 0, len(AllRetryModes()))
	for _, m := range AllRetryModes() {
		out = append(out, string(m))
	}
	sort.Strings(out)
	return out
}

// FailureReason is one of the §7.3 line 384/385 failure labels the
// classifier matches against retryPolicy.retryableFailures /
// nonRetryableFailures. The values are not a closed enum — any string
// may appear on a session-failure report — but the worked-example
// platform defaults below are normative.
//
// spec: §7.3 lines 384-388.
type FailureReason string

const (
	// FailurePodEvicted is the §7.3 line 384 default-retryable label for
	// a pod that lost its node (preempted, node-pressure eviction, etc.).
	FailurePodEvicted FailureReason = "pod_evicted"
	// FailureNodeLost is the §7.3 line 384 default-retryable label for a
	// node that has become unreachable or has been removed from the
	// cluster.
	FailureNodeLost FailureReason = "node_lost"
	// FailureRuntimeCrash is the §7.3 line 384 default-retryable label
	// for an in-pod runtime/adapter crash.
	FailureRuntimeCrash FailureReason = "runtime_crash"
	// FailureWorkspaceValidationFailed is the §7.3 line 385 default-non-
	// retryable label for an extraction/validation failure on workspace
	// restoration.
	FailureWorkspaceValidationFailed FailureReason = "workspace_validation_failed"
	// FailureSetupCommandFailed is the §7.3 line 385 default-non-
	// retryable label for a non-zero exit from a §7.5 setup command.
	FailureSetupCommandFailed FailureReason = "setup_command_failed"

	// FailureExpiredDeadline marks a session driven to `expired` by a
	// §11.3 / §7.3 deadline sweep — the platform-wide `maxSessionAge`
	// cap, the `awaiting_client_action` inactivity deadline, the orphan
	// cleanup window, and any other watchdog edge whose proximate cause
	// is wall-clock elapsed time. The §8.8 MCP adapter surfaces this as
	// `failed` with error code `expired:deadline` so external clients
	// can distinguish time-driven expiry from budget/lease expiry.
	// spec: §8.8 line 867.
	FailureExpiredDeadline FailureReason = "expired:deadline"

	// FailureExpiredBudget marks a session driven to `expired` because
	// a §8.1 token / cost / call budget was exhausted. The §8.8 MCP
	// adapter surfaces this as `failed` with error code
	// `expired:budget`. spec: §8.8 line 867.
	FailureExpiredBudget FailureReason = "expired:budget"

	// FailureExpiredLease marks a session driven to `expired` because a
	// §4.9 credential lease or §8.7 delegation lease expired. The §8.8
	// MCP adapter surfaces this as `failed` with error code
	// `expired:lease`. spec: §8.8 line 867.
	FailureExpiredLease FailureReason = "expired:lease"

	// FailureExpiredIdle marks a session driven to `expired` by the §11.3
	// line 199 `maxIdleTime` watchdog — a `running` session that has had
	// no qualifying agent activity (§6.2 lines 273-278) for longer than
	// its effective `maxIdleTimeSeconds`. The §8.8 MCP adapter surfaces
	// this as `failed` with error code `expired:idle` (the open-ended
	// `expired:*` prefix per §8.8 line 867) so external clients can
	// distinguish idle reclamation from the wall-clock `maxSessionAge`
	// deadline (`expired:deadline`). spec: §6.2 lines 273-300; §11.3 line
	// 199; §8.8 line 867.
	FailureExpiredIdle FailureReason = "expired:idle"

	// FailureOrphanPodTerminated marks a non-terminal session whose
	// bound pod reached the §6.2 `terminated` phase without the pod
	// writing a terminal event back to Postgres — the coordinator was
	// lost and never reconnected. The §10.1 orphan-session reconciler
	// forcibly transitions such a session to `failed` with this reason
	// so it stops holding quota indefinitely.
	// spec: §10.1 line 51 — "forcibly transitioned to `failed` with
	// reason `orphan_pod_terminated`".
	FailureOrphanPodTerminated FailureReason = "orphan_pod_terminated"

	// FailureDeadlockTimeout marks a blocked task the §8.8 subtree
	// deadlock detector failed because the deadlocked subtree was not
	// resolved within `maxDeadlockWaitSeconds` (default 120). The
	// detector fails the deepest blocked tasks in the subtree with this
	// reason so the §8.8 `DEADLOCK_TIMEOUT` contract fires; the MCP
	// adapter surfaces it as `failed` with error code `DEADLOCK_TIMEOUT`.
	// spec: §8.8 line 981. F-8.8.6.
	FailureDeadlockTimeout FailureReason = "deadlock_timeout"
)

// DefaultRetryableFailures is the §7.3 line 384 platform default the
// classifier consults when the per-session retryPolicy supplies an
// empty RetryableFailures list. Returned as a fresh slice so callers
// can mutate freely.
func DefaultRetryableFailures() []string {
	return []string{
		string(FailurePodEvicted),
		string(FailureNodeLost),
		string(FailureRuntimeCrash),
	}
}

// DefaultNonRetryableFailures is the §7.3 line 385 platform default the
// classifier consults when the per-session retryPolicy supplies an
// empty NonRetryableFailures list.
func DefaultNonRetryableFailures() []string {
	return []string{
		string(FailureWorkspaceValidationFailed),
		string(FailureSetupCommandFailed),
	}
}

// FailureClassification is the closed enum produced by ClassifyFailure.
// The §7.3 line 402 "Classify failure" step branches on it: Retryable
// flows into resume_pending and the auto-retry chain, NonRetryable
// short-circuits to awaiting_client_action, and Unknown also short-
// circuits to awaiting_client_action so an unclassified cause cannot
// silently consume retry budget.
type FailureClassification int

const (
	// FailureUnclassified is the zero value reserved for an empty reason
	// string. Callers MUST NOT branch on it as "retryable"; the resume
	// path treats it as non-retryable (no retry budget consumed).
	FailureUnclassified FailureClassification = iota
	// FailureRetryable matches RetryableFailures (per-session or the
	// platform default). §7.3 line 403 admits the auto-retry chain only
	// for this disposition.
	FailureRetryable
	// FailureNonRetryable matches NonRetryableFailures or, when the
	// retry mode is "client_only", every classifiable cause.
	FailureNonRetryable
	// FailureUnknown is the disposition when the reason is non-empty but
	// matches neither list. §7.3 frames the platform-default lists as
	// the closed set the gateway recognises; an unknown label degrades
	// to non-retryable (no retry budget consumed) so a deployer that
	// adds a custom cause must enumerate it explicitly.
	FailureUnknown
)

// String returns the canonical lower_snake label so log lines and
// audit rows surface the disposition uniformly.
func (c FailureClassification) String() string {
	switch c {
	case FailureRetryable:
		return "retryable"
	case FailureNonRetryable:
		return "non_retryable"
	case FailureUnknown:
		return "unknown"
	default:
		return "unclassified"
	}
}

// ClassifyFailure resolves reason against the §7.3 retryable /
// non-retryable lists on p. Match semantics:
//
//   - A nil or empty p uses the platform defaults — §7.3 line 384/385.
//   - A non-empty per-session list replaces the corresponding platform
//     default; the spec frames the per-session lists as overrides, not
//     additions. A retryPolicy that names only retryableFailures keeps
//     the platform-default nonRetryableFailures and vice versa.
//   - Reason matching is exact and case-sensitive. Whitespace is not
//     trimmed because the cause labels are normalised at the call site.
//   - retryPolicy.Mode == "client_only" forces every classifiable
//     reason to NonRetryable per §7.3 line 382 — the mode is "no auto
//     retry; every failure surfaces to the client immediately".
//
// An empty reason returns FailureUnclassified — the caller MUST treat
// this as non-retryable (no retry budget consumed).
//
// spec: §7.3 lines 377-393 (retry policy and lists);
// §6.2 line 250 (non-retryable surfaces to awaiting_client_action).
func ClassifyFailure(reason string, p *RetryPolicy) FailureClassification {
	if reason == "" {
		return FailureUnclassified
	}
	retryable := DefaultRetryableFailures()
	nonRetryable := DefaultNonRetryableFailures()
	if p != nil {
		if len(p.RetryableFailures) > 0 {
			retryable = p.RetryableFailures
		}
		if len(p.NonRetryableFailures) > 0 {
			nonRetryable = p.NonRetryableFailures
		}
	}
	if p != nil && p.Mode == RetryModeClientOnly {
		// spec: §7.3 line 382 — client_only suppresses the auto-retry
		// chain; every classifiable cause behaves as non-retryable. An
		// unknown label still degrades to FailureUnknown so the caller
		// can log the gap rather than treat it as an explicit choice.
		if containsString(retryable, reason) || containsString(nonRetryable, reason) {
			return FailureNonRetryable
		}
		return FailureUnknown
	}
	if containsString(retryable, reason) {
		return FailureRetryable
	}
	if containsString(nonRetryable, reason) {
		return FailureNonRetryable
	}
	return FailureUnknown
}

func containsString(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
