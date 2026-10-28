// SPDX-License-Identifier: MIT

// Package watchdog implements the §6.2 / §11.3 pre-running state-
// lifetime watchdogs. Sessions stuck in `created`, `finalizing`,
// `ready`, or `starting` past their configured wall-clock budget are
// transitioned to `failed` so they cannot pin warm pods indefinitely.
//
// The watchdog is store-agnostic: it sweeps every session reachable
// via the SessionStore interface, computes the elapsed time in the
// current state from the row's StateEnteredAt (the §6.2 state_entered_at
// instant the store stamps on every state change), and applies the §6.2
// transition when the budget is exhausted. The transition is a
// regular sessionstore.Update so the row's UpdatedAt advances and
// downstream watchers see the change.
//
// The default budgets match §11.3:
//
//	created    → 300 s (gateway.maxCreatedStateTimeoutSeconds)
//	finalizing → 600 s (gateway.maxFinalizingTimeoutSeconds)
//	ready      → 300 s (gateway.maxReadyTimeoutSeconds)
//	starting   → 120 s (gateway.maxStartingTimeoutSeconds)
//
// The watchdog applies the §11.3 session-lifetime controls. The
// per-state budgets above bound a session stuck in a pre-running state
// and transition it to `failed`. The maxSessionAge cap bounds a
// session's active age: the time it has spent in `starting`, `running`,
// and `input_required`, accumulated across every pause. The timer is
// paused in `suspended` and in the recovery states (`resume_pending`,
// `resuming`, and `awaiting_client_action`), so time in those states
// does not count, and the watchdog never expires a `suspended` session
// on age. An over-age session in an accruing state transitions to
// `expired` (§6.2 maxSessionAge timer behavior across states). The
// maxAwaitingClientAction deadline bounds the awaiting_client_action
// state and likewise transitions an over-deadline session to
// `expired`. The watchdog never operates on terminal sessions.
package watchdog

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// FailureReason constants — §6.2 / §11.3 pre-running timeouts.
const (
	ReasonCreatedTimeout  = "CREATED_TIMEOUT"
	ReasonFinalizeTimeout = "FINALIZE_TIMEOUT"
	ReasonReadyTimeout    = "READY_TIMEOUT"
	ReasonStartingTimeout = "STARTING_TIMEOUT"
)

// Session-expiry reason values — the §16.1.1 vocabulary the
// `lenny_session_expiry_total{reason}` counter is labelled by. The watchdog is
// the single producer; it stamps one of these on every `→ expired` edge it
// drives and passes it to the SessionExpiryNotifier.
const (
	// ExpiryReasonMaxIdleTime is the reason of the two idle expiries: the
	// §6.2 `maxClientIdleSeconds` expiry of a `running` session that cannot
	// be idle-suspended, and the §6.2 suspended-session lifetime
	// (maxSuspendedSessionSeconds). It shares the `expiryReason` vocabulary
	// of the §14 `dev.lenny.session_expired` event. spec: §16.1.1; §6.2.
	ExpiryReasonMaxIdleTime = "max_idle_time"
	// ExpiryReasonMaxSessionAge is the §11.3 `maxSessionAge` age-cap expiry
	// (and the §7.3 `awaiting_client_action` wall-clock deadline, which shares
	// the age-cap series). spec: §16.1.1; §11.3.
	ExpiryReasonMaxSessionAge = "max_session_age"
)

// ResumeTimeoutOutcome enumerates the two §6.2 outcomes the
// watchdog records when a `resuming` row exceeds its 300s budget.
const (
	// ResumingRetryOutcome is the §6.2 path: retries remain, the
	// session falls back to resume_pending for another attempt.
	ResumingRetryOutcome = "retry"
	// ResumingExhaustedOutcome is the §6.2 path: retries are
	// exhausted, the session transitions to awaiting_client_action.
	ResumingExhaustedOutcome = "exhausted"
)

// Default budgets per the §11.3 timeout table. Each is operator-tunable
// through the matching Config field. The `runtime.maxSessionAgeSeconds`
// per-runtime override (§5.1) further caps a runtime's session lifetime
// below the platform default; the watchdog compares the row's active age
// against the effective deadline.
const (
	// DefaultMaxCreatedStateSeconds is the §11.3 default for
	// the `created` pre-running state: 300s.
	DefaultMaxCreatedStateSeconds = 300

	// DefaultMaxFinalizingStateSeconds is the §11.3 default
	// for the `finalizing` pre-running state: 600s. The
	// `maxFinalizingTimeoutSeconds ≥ setupTimeoutSeconds` invariant
	// (§6.2) is enforced at runtime admission; see
	// pkg/gateway/admin.validateSetupPolicy.
	DefaultMaxFinalizingStateSeconds = 600

	// DefaultMaxReadyStateSeconds is the §11.3 default for
	// the `ready` pre-running state: 300s.
	DefaultMaxReadyStateSeconds = 300

	// DefaultMaxStartingStateSeconds is the §11.3 default
	// for the `starting` pre-running state: 120s.
	DefaultMaxStartingStateSeconds = 120

	// DefaultMaxSessionAgeSeconds is the §11.3 / §6.2 platform-wide
	// active-time session age cap: 7200s (2h). Age counts only while the
	// session is in starting, running, or input_required, and is paused in
	// suspended and the recovery states (§6.2 maxSessionAge timer behavior
	// across states). The per-runtime `runtime.maxSessionAgeSeconds` (§5.1
	// limits) caps a runtime's session age below this default; when unset,
	// the platform default applies to every session of that runtime, so this
	// constant is the source-of-truth for the deployer-visible upper bound on
	// a session that does not opt into a tighter per-runtime limit.
	DefaultMaxSessionAgeSeconds = 7200

	// DefaultMaxAwaitingClientActionSeconds is the §11.3
	// deadline for the `awaiting_client_action` state: 900s. The
	// 900s budget resets on each re-entry into the state — see
	// `sweepAwaitingClientAction` below.
	DefaultMaxAwaitingClientActionSeconds = 900

	// DefaultMaxIdleSeconds is the §6.2 `maxClientIdleSeconds` platform
	// default: 900s. The idle sweep suspends a `running` session that had no
	// activity for its effective `maxClientIdleSeconds` and releases its pod
	// or slot (§6.2 Idle suspension.), or expires one that cannot be
	// idle-suspended. The per-pool `sessionPolicy.maxClientIdleSeconds` and
	// the §27.6 playground idle override tighten it through the
	// IdleResolver; with neither set this default applies. spec: §6.2
	// (maxClientIdleSeconds clock behavior across states).
	DefaultMaxIdleSeconds = 900

	// DefaultMaxSuspendedPodHoldSeconds is the §6.2 / §11.3 hold window of
	// an interrupt suspension: 900s. When a `suspended` session that holds
	// its pod has been suspended this long, the gateway checkpoints the
	// workspace and releases the pod, and the session stays `suspended`
	// with no pod. An idle suspension has a hold of zero. Only the
	// deploy-wide value exists. spec: §6.2 (Pod State Machine); §11.3.
	DefaultMaxSuspendedPodHoldSeconds = 900

	// DefaultMaxSuspendedSessionSeconds is the §6.2 suspended-session
	// lifetime, gateway.maxSuspendedSessionSeconds: 604800s (7 days). A
	// session that stays `suspended` this long after its entry to
	// `suspended` is expired with the `max_idle_time` expiry reason.
	// spec: §6.2 (Pod State Machine).
	DefaultMaxSuspendedSessionSeconds = 604800

	// DefaultMaxResumePendingSeconds is the §6.2 / §7.3 default for the `resume_pending` wall-clock cap: 900s. A
	// session that has waited this long for a pod to become available
	// is transitioned to `awaiting_client_action`. The deployer cap is
	// the same field as `awaiting_client_action` expiry
	// (maxResumeWindowSeconds in spec), and the per-session
	// retryPolicy.maxResumeWindowSeconds tightens it below the platform
	// cap. spec: §6.2; §7.3.
	DefaultMaxResumePendingSeconds = 900

	// DefaultMaxResumingSeconds is the §6.2 default for the
	// `resuming` 300-second watchdog: matches the setup-command total
	// timeout. On expiry the gateway branches on retry budget — see
	// sweepResuming below. spec: §6.2.
	DefaultMaxResumingSeconds = 300

	// DefaultMaxRetries is the §7.3 worked-example default for
	// retryPolicy.maxRetries: 2 (3 total attempts). Mirrors
	// policy.DefaultMaxRetries; the watchdog keeps the constant in this
	// package so the resuming sweep does not depend on the interceptor
	// package's retry evaluator. spec: §7.3.
	DefaultMaxRetries = 2

	// DefaultTickInterval is the §11.3 default sweep
	// cadence: 5s.
	DefaultTickInterval = 5 * time.Second

	// DefaultExpiryWarningSeconds is the §11.3 lead time for the
	// session_expiring_soon warning: the gateway warns the client and pod
	// 300s (5 minutes) before a session's maxSessionAge deadline so the
	// agent can checkpoint and the client can extend or wrap up. F-11.3.5.
	DefaultExpiryWarningSeconds = 300
)

// Config holds the §11.3 budgets plus the sweep cadence. A zero value
// yields the defaults.
type Config struct {
	MaxCreatedSeconds    int
	MaxFinalizingSeconds int
	MaxReadySeconds      int
	MaxStartingSeconds   int
	// MaxSessionAgeSeconds is the §11.3 / §6.2 active-time session age
	// cap: a session whose active age (sessionstore.ActiveAge, the time spent
	// in starting, running, and input_required) exceeds this is expired. Time
	// in suspended and the recovery states does not count, and no age cap
	// expires a suspended session.
	MaxSessionAgeSeconds int
	// MaxAwaitingClientActionSeconds is the §11.3 deadline for the
	// awaiting_client_action state: a session that has waited this
	// long for client action is expired.
	MaxAwaitingClientActionSeconds int
	// MaxIdleSeconds is the §6.2 `maxClientIdleSeconds` platform default:
	// a `running` session with no activity for longer than this (or its
	// tighter per-pool or playground cap through the IdleResolver) is
	// idle-suspended, or expired when it cannot be idle-suspended. A zero
	// value falls through to DefaultMaxIdleSeconds.
	MaxIdleSeconds int
	// MaxSuspendedPodHoldSeconds is the §6.2 / §11.3 deploy-wide hold window
	// of an interrupt suspension: once a `suspended` session that holds its
	// pod has been suspended this long, the gateway checkpoints and releases
	// the pod and the session stays `suspended`. A zero value falls through
	// to DefaultMaxSuspendedPodHoldSeconds.
	MaxSuspendedPodHoldSeconds int
	// MaxSuspendedSessionSeconds is the §6.2 suspended-session lifetime: a
	// session suspended this long after its entry to `suspended` is
	// expired. A zero value falls through to
	// DefaultMaxSuspendedSessionSeconds.
	MaxSuspendedSessionSeconds int
	// MaxResumePendingSeconds is the §6.2 wall-clock cap on
	// resume_pending. On expiry the session transitions to
	// awaiting_client_action so the client can intervene. The
	// per-session retryPolicy.maxResumeWindowSeconds tightens it below
	// this platform cap; a zero/unset row-side value falls through.
	MaxResumePendingSeconds int
	// MaxResumingSeconds is the §6.2 watchdog on resuming. On
	// expiry, the watchdog branches: retries remain → resume_pending
	// and retry counter bumps; otherwise → awaiting_client_action.
	MaxResumingSeconds int
	// MaxRetries is the §7.3 default retry budget the resuming
	// sweep applies when a row has no per-session override. Zero falls
	// through to DefaultMaxRetries.
	MaxRetries   int
	TickInterval time.Duration
	// ExpiryWarningSeconds is the §11.3 lead time for the
	// session_expiring_soon warning: a non-terminal session whose effective
	// maxSessionAge deadline is within this many seconds is warned once. A
	// zero value falls through to DefaultExpiryWarningSeconds. F-11.3.5.
	ExpiryWarningSeconds int
}

// EffectiveMaxSuspendedSessionSeconds returns the suspended-session lifetime
// the watchdog enforces for a configured value: a non-positive value falls
// through to DefaultMaxSuspendedSessionSeconds. Startup validation compares
// the delegation budget key TTL against this resolved value rather than the
// raw flag, so a zero flag cannot pass a bound the watchdog then exceeds.
// spec: §6.2 (Pod State Machine), §8.3 (Delegation Policy and Lease)
func EffectiveMaxSuspendedSessionSeconds(configured int) int {
	if configured <= 0 {
		return DefaultMaxSuspendedSessionSeconds
	}
	return configured
}

// withDefaults returns a copy of c with unset fields filled from the
// §11.3 defaults.
func (c Config) withDefaults() Config {
	if c.MaxCreatedSeconds <= 0 {
		c.MaxCreatedSeconds = DefaultMaxCreatedStateSeconds
	}
	if c.MaxFinalizingSeconds <= 0 {
		c.MaxFinalizingSeconds = DefaultMaxFinalizingStateSeconds
	}
	if c.MaxReadySeconds <= 0 {
		c.MaxReadySeconds = DefaultMaxReadyStateSeconds
	}
	if c.MaxStartingSeconds <= 0 {
		c.MaxStartingSeconds = DefaultMaxStartingStateSeconds
	}
	if c.MaxSessionAgeSeconds <= 0 {
		c.MaxSessionAgeSeconds = DefaultMaxSessionAgeSeconds
	}
	if c.MaxAwaitingClientActionSeconds <= 0 {
		c.MaxAwaitingClientActionSeconds = DefaultMaxAwaitingClientActionSeconds
	}
	if c.MaxIdleSeconds <= 0 {
		c.MaxIdleSeconds = DefaultMaxIdleSeconds
	}
	if c.MaxSuspendedPodHoldSeconds <= 0 {
		c.MaxSuspendedPodHoldSeconds = DefaultMaxSuspendedPodHoldSeconds
	}
	c.MaxSuspendedSessionSeconds = EffectiveMaxSuspendedSessionSeconds(c.MaxSuspendedSessionSeconds)
	if c.MaxResumePendingSeconds <= 0 {
		c.MaxResumePendingSeconds = DefaultMaxResumePendingSeconds
	}
	if c.MaxResumingSeconds <= 0 {
		c.MaxResumingSeconds = DefaultMaxResumingSeconds
	}
	if c.MaxRetries <= 0 {
		c.MaxRetries = DefaultMaxRetries
	}
	if c.ExpiryWarningSeconds <= 0 {
		c.ExpiryWarningSeconds = DefaultExpiryWarningSeconds
	}
	// spec: §6.2; §11.3. The watchdog enforces the
	// gateway-side `maxFinalizingTimeoutSeconds` outer bound; the
	// runtime-side `setupTimeoutSeconds` inner-bound invariant
	// (maxFinalizingTimeoutSeconds ≥ setupTimeoutSeconds) is enforced
	// at admin registration in the §15.1 POST/PUT /v1/admin/runtimes
	// handlers (admin.validateSetupPolicy receives the gateway cap and
	// rejects the runtime when its setupPolicy.timeoutSeconds exceeds
	// it). A session that reaches `finalizing` is bounded by
	// MaxFinalizingSeconds regardless.
	if c.TickInterval <= 0 {
		c.TickInterval = DefaultTickInterval
	}
	return c
}

// TenantLister enumerates tenants the watchdog should sweep. The
// minimal gateway uses a fixed slice; production wires this to the
// tenant registry so newly-added tenants are picked up on the next
// sweep without a restart.
type TenantLister interface {
	ListTenants(ctx context.Context) ([]string, error)
}

// StaticTenants is a TenantLister backed by a fixed slice — useful in
// tests and the minimal gateway.
type StaticTenants []string

// ListTenants implements TenantLister.
func (s StaticTenants) ListTenants(_ context.Context) ([]string, error) { return s, nil }

// TerminalHook is invoked when the watchdog forces a session to a
// terminal state (failed or expired). It supersedes the watchdog's
// in-package billing/archive emission when wired, so terminal side
// effects — workspace seal, executor release (which drains the pod and
// for concurrent-mode sessions releases the slot per §5.2),
// §11.7 audit, §7.2 SSE, §11.2.1 billing, and §8.10 archive — run once
// through the same Server.recordSessionCompleted path the REST handlers
// use.
//
// spec: §5.2 — slot release on session end / expiry; §6.2 — executor release maps the terminal phase onto the Sandbox.
type TerminalHook interface {
	// BeginTerminal opens the writer's terminal claim for the session before
	// its terminal Update and returns the idempotent function that ends it.
	// The watchdog calls the end function after OnSessionTerminal returns
	// when its write applied, and at once on a not-applied sentinel or a
	// store error. spec: §7.2 (Interactive Session Model).
	BeginTerminal(sessionID string) (end func())
	// fromState is the session's pre-terminal state, read on the locked row
	// the watchdog forced to its terminal value. The terminal pod-release
	// path keys off it so a maxSessionAge-expired running session is released
	// through the §6.2 executor recycle path rather than reclaimed by name
	// (§4.6).
	OnSessionTerminal(ctx context.Context, fromState session.State, sess sessionstore.Session)
}

// ResumeDriver is an optional hook on TerminalHook implementations: the §7.3
// resume driver, the only code that writes resume_pending → resuming. The
// watchdog calls DriveResume for every resume_pending row it lists inside the
// row's maxResumeWindowSeconds window, so a session waiting for a replacement
// pod is attempted once per tick. DriveResume returns at once; it decides
// whether this replica attempts the restore. spec: §7.3 (Resume driver).
type ResumeDriver interface {
	DriveResume(ctx context.Context, row sessionstore.Session)
}

// AwaitingClientActionExpiryNotifier is an optional hook on TerminalHook
// implementations. The watchdog invokes it for the
// `awaiting_client_action → expired` edge BEFORE the generic
// OnSessionTerminal so the §11.7 / §16.7 session.expired_in_awaiting_action
// audit row carries the awaiting-action context (which the generic
// terminal hook cannot derive from the post-update row alone).
//
// spec: §7.3 — `awaiting_client_action → expired` entry path;
// §11.7 / §16.7 audit row. F-7.3.25.
type AwaitingClientActionExpiryNotifier interface {
	OnSessionExpiredFromAwaitingClientAction(ctx context.Context, sess sessionstore.Session)
}

// AwaitingClientActionEntryNotifier is an optional hook on TerminalHook
// implementations. The watchdog invokes it whenever it transitions a
// non-terminal row into `awaiting_client_action` — the §6.2
// resume_pending wall-clock cap and the §6.2 resuming
// retries-exhausted branch. The hook is the entry-point for the §7.3 webhook, the §16.6 operational
// event, and the §11.7 / §16.7 audit row that every entry into the
// state must emit.
//
// The notifier MUST NOT be confused with AwaitingClientActionExpiryNotifier
// (which fires on the terminal `awaiting → expired` edge). Entry and
// expiry are distinct events and emit distinct audit rows.
//
// spec: §6.2;
// §6.2;
// §7.3. F-6.2.14.
type AwaitingClientActionEntryNotifier interface {
	OnSessionEnteredAwaitingClientAction(ctx context.Context, sess sessionstore.Session)
}

// RetryAttemptNotifier is an optional hook on TerminalHook
// implementations. The watchdog invokes it whenever the resuming sweep
// drives a `resuming → resume_pending` retry, so the §16.1
// lenny_session_retry_total counter and the §11.7 / §16.7
// session.retry_attempted audit row count the watchdog's spend edge.
// The session server fires the same pair on the other spend edge, a
// reported failure into resume_pending (transitionToResumePending).
//
// spec: §6.2; §7.3 (Retry and Resume) "Retry accounting.";
// §16.1 lenny_session_retry_total; §11.7 / §16.7 session.retry_attempted.
// F-6.2.14.
type RetryAttemptNotifier interface {
	OnSessionRetryAttempt(ctx context.Context, sess sessionstore.Session)
}

// SessionExpiryNotifier is an optional hook on TerminalHook implementations.
// The watchdog invokes it on every platform expiry-clock transition it drives
// (`→ expired`), so the §16.1 `lenny_session_expiry_total{reason}` counter sees
// the termination. The watchdog resolves the §16.1.1 `reason` vocabulary from
// the expiry edge that fired:
//
//   - `max_idle_time` on the §6.2 `maxClientIdleSeconds` expiry of a session
//     that cannot be idle-suspended and on the §6.2 suspended-session
//     lifetime, and
//   - `max_session_age` on the §11.3 `maxSessionAge` age-cap expiry (and the
//     §7.3 `awaiting_client_action` wall-clock deadline, whose reason shares the
//     age-cap series per the §14 `expiryReason` vocabulary).
//
// pool is the session's §5.2 PoolRef (the metric's other label), empty for a
// session whose pool was never resolved. The notification is best-effort: a nil
// hook or one that does not implement the optional notifier silently no-ops, and
// it never rolls back the watchdog's state transition.
//
// spec: §16.1 (lenny_session_expiry_total{reason}); §16.1.1 (reason
// vocabulary); §6.2 (maxClientIdleSeconds clock); §11.3. F-11.3.7.
type SessionExpiryNotifier interface {
	OnSessionExpired(ctx context.Context, sess sessionstore.Session, reason string)
}

// ExpiryWarningNotifier is an optional hook on TerminalHook implementations.
// The watchdog invokes it once per session, when the session's effective
// maxSessionAge deadline first falls within the ExpiryWarningSeconds window
// (default 300s), so the gateway emits the §11.3 session_expiring_soon
// SSE event to the client and dispatches the DEADLINE_APPROACHING CH-RUNTIMEOPS signal to the running pod. maxSessionAgeSeconds is the session's
// resolved deadline (for the SSE payload) and remainingSeconds is the
// active time left before it, the cap minus the session's active age.
//
// The notification is best-effort and fires at most once per session per
// gateway process: a watchdog restart inside the window may re-warn, which
// SSE clients tolerate as an advisory event.
//
// spec: §11.3;
// §15. F-11.3.5.
type ExpiryWarningNotifier interface {
	OnSessionExpiringSoon(ctx context.Context, sess sessionstore.Session, maxSessionAgeSeconds, remainingSeconds int)
}

// DLQSweeper runs the §7.2 dead-letter-queue TTL sweep for one session in a
// recovering state, emitting a `message_expired` event with `reason:
// "dlq_ttl_expired"` per expired entry and returning the count removed.
// *sessioninbox.Coordinator satisfies it. A nil sweeper disables the sweep.
// The watchdog calls it only for a session in `resume_pending`, `resuming`,
// or `awaiting_client_action` whose state entry is at least the DLQ TTL old,
// never for a `suspended` session, whose held messages wait for its resume.
//
// spec: §7.2 (Interactive Session Model).
type DLQSweeper interface {
	SweepExpired(ctx context.Context, tenantID, sessionID string) (expired int, err error)
}

// SuspendedPodReleaser releases the pod of an interrupt suspension whose
// hold window ended (§6.2 Pod State Machine): it checkpoints, clears the pod
// from the row, and releases the pod while the session stays `suspended`.
// It returns at once and runs the release off the caller. The session server
// implements it. spec: §6.2 (Pod State Machine).
type SuspendedPodReleaser interface {
	ReleaseSuspendedPod(ctx context.Context, row sessionstore.Session)
}

// IdleSuspender suspends an idle `running` session and releases its pod or
// slot in one sequence (§6.2 Idle suspension.). idleSeconds is the session's
// effective maxClientIdleSeconds, the window the runtime adapter's seal
// requires without activity. It returns at once and runs the suspension off
// the caller. The session server implements it, and the gateway wires it
// only when a pod binder is wired. spec: §6.2 (maxClientIdleSeconds clock
// behavior across states).
type IdleSuspender interface {
	SuspendIdle(ctx context.Context, row sessionstore.Session, idleSeconds int)
}

// BacklogResumer resumes a `suspended` session whose held messages arrived
// while it was suspended (§7.2 delivery path 6 Backlog resume.). HoldsPod
// reports whether the row holds its pod, which the watchdog's suspended
// sweep reads to leave a podless row to the gateway leader's re-check.
// ResumeBacklog reports true when it started a resume or could not read the
// backlog, in which case the sweep releases nothing this tick. The session
// server implements it. spec: §7.2 (Message delivery routing — seven paths,
// path 6).
type BacklogResumer interface {
	HoldsPod(row sessionstore.Session) bool
	ResumeBacklog(ctx context.Context, row sessionstore.Session) bool
}

// IdleSuspendability is the optional half of the IdleResolver that decides
// whether a session can be idle-suspended. A service-mode session and a
// `type: mcp` runtime's session cannot, and keep the idle expiry. An error
// skips the row this tick. spec: §6.2 (maxClientIdleSeconds clock behavior
// across states).
type IdleSuspendability interface {
	IdleSuspendable(ctx context.Context, sess sessionstore.Session) (bool, error)
}

// SessionAgeResolver resolves the §5.1 per-runtime / §5.2 per-pool
// maxSessionAge cap (in seconds) for a session row so the maxSessionAge
// sweep applies a deployer's per-runtime tuning instead of the single
// platform default. A return of 0 means neither the runtime's `limits:`
// block nor the assigned pool declares a cap, so the platform default (and
// any tighter per-session retryPolicy value) applies. The resolver is
// optional: a nil resolver preserves the platform-default behaviour and
// the per-session retryPolicy clamp.
//
// spec: §11.3 — `maxSessionAge` is a deployer cap tuned per
// runtime; §5.1 limits.maxSessionAgeSeconds; §5.2 pool maxSessionAgeSeconds.
// F-11.3.3.
type SessionAgeResolver interface {
	EffectiveMaxSessionAgeSeconds(ctx context.Context, sess sessionstore.Session) int
}

// IdleResolver resolves the §11.3 per-runtime / per-pool /
// per-session `maxClientIdleSeconds` cap (in seconds) for a session row. A
// return of 0 means no configuration surface resolves a cap, so the platform
// default (w.cfg.MaxIdleSeconds) applies. The resolver is optional: a nil
// resolver leaves the idle sweep on the single platform default.
//
// spec: §11.3; §6.2 (maxClientIdleSeconds clock); §27.6.
// F-11.3.7.
type IdleResolver interface {
	EffectiveMaxIdleSeconds(ctx context.Context, sess sessionstore.Session) int
}

// Watchdog drives the periodic sweep.
type Watchdog struct {
	store        sessionstore.Store
	tenants      TenantLister
	cfg          Config
	clock        func() time.Time
	billing      billingstore.Store
	archive      treearchive.Store
	terminal     TerminalHook
	messaging    DLQSweeper
	ageResolver  SessionAgeResolver
	idleResolver IdleResolver
	releaser     SuspendedPodReleaser
	suspender    IdleSuspender
	backlog      BacklogResumer

	// warnedMu guards warned, the §11.3 expiry-warning dedup set.
	warnedMu sync.Mutex
	// warned holds the session ids the expiry-warning sweep has already
	// signalled, so a session is warned at most once. Entries are pruned
	// when the sweep observes the session reach a terminal state. F-11.3.5.
	warned map[string]struct{}
}

// New returns a Watchdog. The clock argument is optional; pass nil to
// default to time.Now.
func New(store sessionstore.Store, tenants TenantLister, cfg Config, clock func() time.Time) *Watchdog {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Watchdog{
		store:   store,
		tenants: tenants,
		cfg:     cfg.withDefaults(),
		clock:   clock,
	}
}

// WithBilling wires the §11.2.1 billing ledger so the watchdog emits a
// session.completed event for every session it forces to `failed`.
func (w *Watchdog) WithBilling(b billingstore.Store) *Watchdog {
	w.billing = b
	return w
}

// WithTreeArchive wires the §8.10 session_tree_archive so the watchdog
// archives a child session it forces to a terminal state.
func (w *Watchdog) WithTreeArchive(a treearchive.Store) *Watchdog {
	w.archive = a
	return w
}

// WithTerminalHook wires the gateway-side terminal-side-effects pipeline
// (workspace seal, executor release, audit, SSE, billing, archive). When
// set, the watchdog delegates the full terminal pipeline to hook and
// SKIPS its own billing/archive emission, so a session forced terminal
// by background sweep emits the same signals exactly once as a session
// terminated by REST. Without a hook the watchdog falls back to its own
// best-effort billing + child-archive (preserving the pre-hook behaviour)
// — but in that mode concurrent-mode slot releases stay leaked until
// pod termination per F-5.2.26.
//
// spec: §5.2 — slot release; §7.2 — SSE; §11.7 —
// audit; §6.2 — executor release.
func (w *Watchdog) WithTerminalHook(hook TerminalHook) *Watchdog {
	w.terminal = hook
	return w
}

// WithMessaging wires the §7.2 session-inbox coordinator so each sweep
// runs the dead-letter-queue TTL sweep over every recovering session
// (`resume_pending`, `resuming`, `awaiting_client_action`). The sweep emits a
// `message_expired(dlq_ttl_expired)` event per expired DLQ entry, the
// background-trimmer half of the §7.2 contract. A nil coordinator
// (the dev / no-Redis posture) leaves the DLQ sweep disabled.
//
// spec: §7.2.
func (w *Watchdog) WithMessaging(s DLQSweeper) *Watchdog {
	w.messaging = s
	return w
}

// WithSessionAgeResolver wires the §5.1 / §5.2 per-runtime / per-pool
// maxSessionAge resolver so the maxSessionAge sweep honours a deployer's
// per-runtime `limits.maxSessionAgeSeconds` (or per-pool
// `maxSessionAgeSeconds`) instead of expiring every session at the single
// platform default. Most-restrictive-wins: the resolver's cap clamps below
// the platform default, and the per-session retryPolicy value clamps below
// that. A nil resolver leaves the platform-default behaviour intact.
//
// spec: §11.3 — deployer cap tuned per runtime. F-11.3.3.
func (w *Watchdog) WithSessionAgeResolver(r SessionAgeResolver) *Watchdog {
	w.ageResolver = r
	return w
}

// WithIdleResolver wires the §11.3 per-runtime / per-pool /
// per-session `maxClientIdleSeconds` resolver so the idle sweep honors a
// deployer's `sessionPolicy.maxClientIdleSeconds` and the §27.6 playground
// idle override instead of expiring every clock-running session at the
// single platform default. A nil resolver leaves the platform-default
// behaviour intact.
//
// spec: §11.3; §6.2 (maxClientIdleSeconds clock). F-11.3.7.
func (w *Watchdog) WithIdleResolver(r IdleResolver) *Watchdog {
	w.idleResolver = r
	return w
}

// WithSuspendedPodReleaser wires the release of an interrupt suspension's
// pod at the end of its hold window. A nil releaser leaves the pod held.
// spec: §6.2 (Pod State Machine).
func (w *Watchdog) WithSuspendedPodReleaser(r SuspendedPodReleaser) *Watchdog {
	w.releaser = r
	return w
}

// WithIdleSuspender wires the idle suspension of a `running` session. With
// no suspender the idle sweep expires every idle session, as a gateway with
// no pod binder does. spec: §6.2 (maxClientIdleSeconds clock behavior across
// states).
func (w *Watchdog) WithIdleSuspender(s IdleSuspender) *Watchdog {
	w.suspender = s
	return w
}

// WithBacklogResumer wires the backlog resume of a `suspended` session that
// holds its pod. spec: §7.2 (Message delivery routing — seven paths, path 6).
func (w *Watchdog) WithBacklogResumer(b BacklogResumer) *Watchdog {
	w.backlog = b
	return w
}

// Result captures the outcome of a sweep.
type Result struct {
	// ForcedFailures is the count of sessions transitioned to `failed`
	// by this sweep.
	ForcedFailures int
	// Expirations is the count of sessions transitioned to `expired`
	// by the §11.3 maxSessionAge and maxAwaitingClientAction sweeps.
	Expirations int
	// IdleExpirations is the count of `running` sessions that cannot be
	// idle-suspended and were transitioned to `expired` by the §6.2 idle
	// sweep in this tick.
	IdleExpirations int
	// IdleSuspensions is the count of idle `running` sessions handed to the
	// idle suspender in this tick.
	IdleSuspensions int
	// SuspendedExpirations is the count of `suspended` sessions expired by
	// the §6.2 suspended-session lifetime in this tick.
	SuspendedExpirations int
	// ResumePendingTimeouts is the count of sessions transitioned from
	// resume_pending to awaiting_client_action by the §6.2
	// wall-clock cap.
	ResumePendingTimeouts int
	// ResumingTimeouts is the count of sessions whose §6.2
	// resuming watchdog fired in this sweep, broken down by outcome
	// (ResumingRetryOutcome → resume_pending; ResumingExhaustedOutcome
	// → awaiting_client_action) in PerResumingOutcome.
	ResumingTimeouts int
	// PerReason records the count per FailureReason for observability.
	PerReason map[string]int
	// PerResumingOutcome counts the §6.2 resuming-watchdog
	// disposition.
	PerResumingOutcome map[string]int
	// DLQExpired is the count of dead-letter-queue entries removed by the
	// §7.2 TTL sweep across every recovering session in this
	// sweep. Each removed entry emits a `message_expired(dlq_ttl_expired)`
	// event on its sender's stream.
	DLQExpired int
	// ExpiryWarnings is the count of sessions newly warned by the §11.3 session_expiring_soon sweep in this tick (each fires the
	// SSE event and the DEADLINE_APPROACHING adapter signal once). F-11.3.5.
	ExpiryWarnings int
}

// Tick runs a single sweep against every tenant returned by the
// configured TenantLister at the supplied now. Returns the count of
// sessions transitioned to `failed` and the per-reason breakdown.
//
// Tick is idempotent: a second sweep with the same `now` does not
// re-transition rows that the first sweep already moved to `failed`.
func (w *Watchdog) Tick(ctx context.Context, now time.Time) (Result, error) {
	res := Result{PerReason: map[string]int{}, PerResumingOutcome: map[string]int{}}
	tenants, err := w.tenants.ListTenants(ctx)
	if err != nil {
		return res, err
	}
	for _, tenant := range tenants {
		// We scan every state separately so the store can use a
		// state-indexed query in production. The minimal in-memory
		// store does the filter in-process; the contract is the same.
		for _, st := range []session.State{
			session.StateCreated,
			session.StateFinalizing,
			session.StateReady,
			session.StateStarting,
		} {
			rows, err := w.store.List(ctx, tenant, sessionstore.ListFilter{State: st})
			if err != nil {
				return res, err
			}
			for _, row := range rows {
				if !w.elapsed(row.StateEnteredAt, st, now) {
					continue
				}
				reason := w.reasonForState(st)
				end := w.beginTerminal(row.ID)
				updated, err := w.store.Update(ctx, tenant, row.ID, func(r *sessionstore.Session) error {
					if r.State != st || !w.elapsed(r.StateEnteredAt, st, now) {
						// spec: §10.1.1 (Primary:) — the locked row left the
						// listed state, or re-entered it inside the budget;
						// leave it alone.
						return sessionstore.ErrSuperseded
					}
					r.State = session.StateFailed
					r.FailureClass = w.classForReason(reason)
					r.FailureReason = reason
					// spec: §5.2 (Slot assignment atomicity) — the stamp reads the clock
					// inside this row's Update, because an earlier row's post-terminal
					// hook, seal included, ran after the pass's now was read.
					sessionstore.MarkTerminated(r, st, w.clock())
					return nil
				})
				if errors.Is(err, sessionstore.ErrSuperseded) {
					end()
					continue
				}
				if err != nil {
					end()
					return res, err
				}
				res.ForcedFailures++
				res.PerReason[reason]++
				// st is the pre-running state the sweep timed out
				// (created/finalizing/ready/starting), the §4.6 pre-terminal
				// state the reclaim path keys off.
				w.recordCompleted(ctx, st, updated)
				end()
			}
		}
		// spec: §6.2 and §6.2
		// (resume_pending wall-clock cap). The resuming sweep runs
		// before resume_pending so a resuming → resume_pending retry
		// that lands inside the same Tick does not get re-swept by the
		// resume_pending sweep in the same call (StateEnteredAt moves on
		// the resume_pending write, but defence-in-depth: ordering is
		// stable regardless).
		if err := w.sweepResuming(ctx, tenant, now, &res); err != nil {
			return res, err
		}
		if err := w.sweepResumePending(ctx, tenant, now, &res); err != nil {
			return res, err
		}
		// spec: §11.3 — warn the client and pod 5 minutes before
		// maxSessionAge. Runs before sweepMaxAge so the warning fires while
		// the session is still non-terminal (sweepMaxAge expires it on a
		// later tick once the deadline passes). F-11.3.5.
		if err := w.sweepExpiryWarning(ctx, tenant, now, &res); err != nil {
			return res, err
		}
		if err := w.sweepMaxAge(ctx, tenant, now, &res); err != nil {
			return res, err
		}
		if err := w.sweepAwaitingClientAction(ctx, tenant, now, &res); err != nil {
			return res, err
		}
		if err := w.sweepIdle(ctx, tenant, now, &res); err != nil {
			return res, err
		}
		if err := w.sweepSuspended(ctx, tenant, now, &res); err != nil {
			return res, err
		}
		if err := w.sweepDLQExpiry(ctx, tenant, now, &res); err != nil {
			return res, err
		}
	}
	return res, nil
}

// sweepDLQExpiry runs the §7.2 dead-letter-queue TTL sweep over every
// session in a recovering state of tenant: `resume_pending`, `resuming`, and
// `awaiting_client_action`. It does not list `suspended`, so it expires no
// held message of a `suspended` session; those are delivered after the
// session's next resume (§7.2 delivery path 6 Held messages. and Backlog
// resume.), and the terminal drain removes what remains when the
// suspended-session lifetime ends the session. A row whose StateEnteredAt is
// less than DefaultDLQTTL before now is skipped, so an entry expires at the later
// of its score and the row's entry to its state plus the TTL (§7.2 delivery
// path 6 Delivery of held messages.). The coordinator emits a
// `message_expired(dlq_ttl_expired)` event per expired entry. A per-session
// sweep error is dropped so a transient Redis blip on one session does not
// stall the tick; the next tick retries. No-op when messaging is not wired.
//
// spec: §7.2 (Interactive Session Model).
func (w *Watchdog) sweepDLQExpiry(ctx context.Context, tenant string, now time.Time, res *Result) error {
	if w.messaging == nil {
		return nil
	}
	for _, st := range []session.State{
		session.StateResumePending,
		session.StateResuming,
		session.StateAwaitingClientAction,
	} {
		rows, err := w.store.List(ctx, tenant, sessionstore.ListFilter{State: st})
		if err != nil {
			return err
		}
		for _, row := range rows {
			if now.Sub(row.StateEnteredAt) < sessioninbox.DefaultDLQTTL {
				continue
			}
			expired, serr := w.messaging.SweepExpired(ctx, tenant, row.ID)
			if serr != nil {
				continue
			}
			res.DLQExpired += expired
		}
	}
	return nil
}

// sweepResumePending implements the §6.2 wall-clock cap. A
// session that has waited in resume_pending longer than its effective
// maxResumeWindowSeconds transitions to awaiting_client_action so the
// client can intervene. The transition fires the §7.3
// session.awaiting_action webhook (delivered via the optional entry
// notifier on the terminal hook).
//
// Reset semantics: a session that traverses resume_pending → resuming →
// resume_pending (one retry round-trip) gets a fresh budget on
// re-entry because the store moves StateEnteredAt on the resume_pending
// write. A write that leaves the row in resume_pending does not restart the
// window. The
// maxSessionAge cap does not bound the cumulative case, because its timer is
// paused in resume_pending and resuming (§6.2 maxSessionAge timer behavior
// across states); the retry budget does, since each re-entry from resuming
// spends a retry and an exhausted budget moves the session to
// awaiting_client_action.
//
// Inside the window the sweep hands each listed row to the resume driver
// (DriveResume), which attempts the restore on the replica that holds the
// session's coordination lease. The driver applies the active-age check, so
// the watchdog and tree recovery apply one check. The window-expiry write
// takes effect only when the locked row is still resume_pending and its
// StateEnteredAt is past the window; otherwise the mutator returns
// errResumePendingSuperseded and the sweep counts no timeout and sends no
// notification.
//
// spec: §6.2 (Pod State Machine); §7.3 (Resume driver).
func (w *Watchdog) sweepResumePending(ctx context.Context, tenant string, now time.Time, res *Result) error {
	rows, err := w.store.List(ctx, tenant,
		sessionstore.ListFilter{State: session.StateResumePending})
	if err != nil {
		return err
	}
	platformCap := time.Duration(w.cfg.MaxResumePendingSeconds) * time.Second
	driver, _ := w.terminal.(ResumeDriver)
	for _, row := range rows {
		if now.Sub(row.StateEnteredAt) <= EffectiveMaxResumeWindow(row, platformCap) {
			if driver != nil {
				driver.DriveResume(ctx, row)
			}
			continue
		}
		updated, err := w.store.Update(ctx, tenant, row.ID, func(r *sessionstore.Session) error {
			if r.State != session.StateResumePending || now.Sub(r.StateEnteredAt) <= EffectiveMaxResumeWindow(*r, platformCap) {
				return errResumePendingSuperseded
			}
			r.State = session.StateAwaitingClientAction
			return nil
		})
		if errors.Is(err, errResumePendingSuperseded) {
			continue
		}
		if err != nil {
			return err
		}
		res.ResumePendingTimeouts++
		w.notifyAwaitingClientActionEntered(ctx, updated)
	}
	return nil
}

// errResumePendingSuperseded is the window-expiry mutator's not-applied
// sentinel: the locked row left resume_pending, or left and re-entered it,
// which restarts its window. spec: §6.2 (Pod State Machine).
var errResumePendingSuperseded = errors.New("watchdog: resume_pending window expiry superseded on the locked row")

// errResumingSuperseded is the resuming watchdog's not-applied sentinel: the
// locked row left resuming, or is a later resuming incarnation than the one
// the sweep listed. spec: §6.2 (Pod State Machine), §7.3 (Retry and Resume).
var errResumingSuperseded = errors.New("watchdog: resuming exit superseded on the locked row")

// sweepResuming implements the §6.2 resuming watchdog. A session that
// entered resuming (StateEnteredAt) more than MaxResumingSeconds ago without
// the resume completing branches on retry budget:
//
//   - RetryCount < effective maxRetries: transition to resume_pending
//     and increment RetryCount; fire the §16.1 retry-attempt metric and
//     §11.7 audit row via the optional RetryAttemptNotifier on the
//     terminal hook.
//   - RetryCount >= effective maxRetries: transition to
//     awaiting_client_action and fire the §7.3 entry notifier
//     so the client receives a session.awaiting_action webhook.
//
// Every replica's watchdog evaluates the rows it lists, so the exit is
// guarded on the incarnation the sweep listed: it takes effect only when the
// locked row is resuming at the listed row's coordination_generation, and the
// retry branch is computed inside the mutator from the locked row's
// RetryCount. A row a failure report already moved, or that re-entered
// resuming at a higher generation, is left unchanged with no notification,
// so two watchdogs exit one incarnation at most once and RetryCount never
// exceeds maxRetries. The exit writes no coordination_generation.
//
// The §6.2 non-retryable branch is independent of the watchdog: the resume
// driver reports a non-retryable failure through the failure funnel. The
// watchdog handles only the timeout.
//
// spec: §6.2 (Pod State Machine), §7.3 (Retry and Resume).
func (w *Watchdog) sweepResuming(ctx context.Context, tenant string, now time.Time, res *Result) error {
	rows, err := w.store.List(ctx, tenant,
		sessionstore.ListFilter{State: session.StateResuming})
	if err != nil {
		return err
	}
	budget := time.Duration(w.cfg.MaxResumingSeconds) * time.Second
	for _, row := range rows {
		if now.Sub(row.StateEnteredAt) <= budget {
			continue
		}
		updated, err := w.store.Update(ctx, tenant, row.ID, func(r *sessionstore.Session) error {
			if r.State != session.StateResuming || r.CoordinationGeneration != row.CoordinationGeneration {
				return errResumingSuperseded
			}
			if r.RetryCount < int64(effectiveMaxRetries(*r, w.cfg.MaxRetries)) {
				r.State = session.StateResumePending
				r.RetryCount++
			} else {
				r.State = session.StateAwaitingClientAction
			}
			return nil
		})
		if errors.Is(err, errResumingSuperseded) {
			continue
		}
		if err != nil {
			return err
		}
		res.ResumingTimeouts++
		if updated.State == session.StateResumePending {
			res.PerResumingOutcome[ResumingRetryOutcome]++
			w.notifyRetryAttempt(ctx, updated)
			continue
		}
		res.PerResumingOutcome[ResumingExhaustedOutcome]++
		w.notifyAwaitingClientActionEntered(ctx, updated)
	}
	return nil
}

// notifyAwaitingClientActionEntered fires the AwaitingClientActionEntryNotifier
// on the terminal hook when one is wired. Best-effort: a nil hook or a
// hook that does not implement the optional notifier silently no-ops.
// The §6.2 transitions both flow through here so the
// §7.3 webhook + §11.7 audit row fire exactly once per entry.
func (w *Watchdog) notifyAwaitingClientActionEntered(ctx context.Context, sess sessionstore.Session) {
	if w.terminal == nil {
		return
	}
	if n, ok := w.terminal.(AwaitingClientActionEntryNotifier); ok {
		n.OnSessionEnteredAwaitingClientAction(ctx, sess)
	}
}

// notifyRetryAttempt fires the RetryAttemptNotifier on the terminal hook
// when one is wired. Best-effort: a nil hook or a hook that does not
// implement the optional notifier silently no-ops. The watchdog's
// resuming → resume_pending retry path flows through here so the §16.1
// lenny_session_retry_total counter and §11.7 audit row count the
// watchdog's spend edge. The session server's transitionToResumePending
// fires the same pair on the reported-failure spend edge.
// spec: §7.3 (Retry and Resume) "Retry accounting.".
func (w *Watchdog) notifyRetryAttempt(ctx context.Context, sess sessionstore.Session) {
	if w.terminal == nil {
		return
	}
	if n, ok := w.terminal.(RetryAttemptNotifier); ok {
		n.OnSessionRetryAttempt(ctx, sess)
	}
}

// notifySessionExpiry fires the SessionExpiryNotifier on the terminal hook when
// one is wired, so the §16.1 `lenny_session_expiry_total{reason}` counter sees
// the watchdog-driven `→ expired` transition. reason is the §16.1.1 vocabulary
// value the caller resolved from the expiry edge (ExpiryReasonMaxIdleTime on the
// idle expiry of a session that cannot be idle-suspended and on the
// suspended-session lifetime, ExpiryReasonMaxSessionAge on the age-cap and
// awaiting-action wall-clock sweeps). Best-effort: a nil hook or one that does
// not implement the optional notifier silently no-ops.
//
// spec: §16.1 (lenny_session_expiry_total{reason}); §16.1.1 (reason
// vocabulary). F-11.3.7.
func (w *Watchdog) notifySessionExpiry(ctx context.Context, sess sessionstore.Session, reason string) {
	if w.terminal == nil {
		return
	}
	if n, ok := w.terminal.(SessionExpiryNotifier); ok {
		n.OnSessionExpired(ctx, sess, reason)
	}
}

// EffectiveMaxResumeWindow resolves the §7.3 per-session
// retryPolicy.maxResumeWindowSeconds override against the deployer cap. The
// resume driver bounds an attempt by the same window, so the two agree.
// A nil policy or zero value falls through to the platform cap; a
// positive override clamps to the smaller so the per-session value can
// never exceed the platform bound regardless of how the row was
// persisted. Mirrors effectiveMaxSessionAge in shape.
//
// spec: §7.3; §6.2 platform cap. F-6.2.14.
func EffectiveMaxResumeWindow(row sessionstore.Session, platformCap time.Duration) time.Duration {
	if row.RetryPolicy == nil || row.RetryPolicy.MaxResumeWindowSeconds <= 0 {
		return platformCap
	}
	override := time.Duration(row.RetryPolicy.MaxResumeWindowSeconds) * time.Second
	if platformCap > 0 && override > platformCap {
		return platformCap
	}
	return override
}

// effectiveMaxRetries resolves the §7.3 per-session
// retryPolicy.maxRetries against the deployer default. A nil policy or
// zero value falls through to the deployer default. spec: §7.3. F-6.2.14.
func effectiveMaxRetries(row sessionstore.Session, deployerDefault int) int {
	if row.RetryPolicy != nil && row.RetryPolicy.MaxRetries > 0 {
		return row.RetryPolicy.MaxRetries
	}
	if deployerDefault <= 0 {
		return DefaultMaxRetries
	}
	return deployerDefault
}

// sweepAwaitingClientAction expires every session for the tenant that
// has waited in awaiting_client_action longer than the §11.3
// maxAwaitingClientAction deadline, measured from the row's StateEnteredAt
// (the instant it entered the state). §7.3 governs the transition:
// awaiting_client_action → expired when the deadline is exhausted. The
// expiry write re-checks the state and the deadline on the locked row, so a
// row that left and re-entered the state after the listing is left alone,
// and it stamps `expired:deadline` over any reason an earlier recovery left.
//
// Reset semantics: a session that traverses awaiting_client_action →
// resume_pending → running and later re-enters awaiting_client_action
// gets a fresh 900s budget because the store moves StateEnteredAt on the
// re-entering write. A write that leaves the row in the state does not
// restart the budget. The §11.3 spec text is silent on whether the
// deadline is per-entry or cumulative; the gateway implements the
// per-entry interpretation because §7.3 frames the deadline as the
// inactivity timeout on a single client-action cycle rather than as a
// total-time budget. The maxSessionAge cap does not bound the cumulative
// wait, because its timer is paused in awaiting_client_action (§6.2
// maxSessionAge timer behavior across states). Each re-entry requires the
// client to resume the session out of the state, and only the active time
// the session then spends in running between entries accrues toward
// maxSessionAge.
func (w *Watchdog) sweepAwaitingClientAction(ctx context.Context, tenant string, now time.Time, res *Result) error {
	rows, err := w.store.List(ctx, tenant,
		sessionstore.ListFilter{State: session.StateAwaitingClientAction})
	if err != nil {
		return err
	}
	deadline := time.Duration(w.cfg.MaxAwaitingClientActionSeconds) * time.Second
	for _, row := range rows {
		if now.Sub(row.StateEnteredAt) <= deadline {
			continue
		}
		end := w.beginTerminal(row.ID)
		updated, err := w.store.Update(ctx, tenant, row.ID, func(r *sessionstore.Session) error {
			if r.State != session.StateAwaitingClientAction || now.Sub(r.StateEnteredAt) <= deadline {
				// spec: §10.1.1 (Primary:) — the locked row left the state,
				// or re-entered it inside the deadline; leave it.
				return sessionstore.ErrSuperseded
			}
			r.State = session.StateExpired
			// spec: §8.8 — stamp the `expired:deadline` prefix
			// on the row so the MCP adapter surfaces `failed` with the
			// `expired:*` error code. The awaiting-client-action sweep
			// is the §7.3 wall-clock deadline, and its own reason replaces
			// any reason an earlier recovery left on the row. F-8.8.8.
			r.FailureReason = string(session.FailureExpiredDeadline)
			// spec: §5.2 (Slot assignment atomicity) — the stamp reads the clock
			// inside this row's Update, because an earlier row's post-terminal
			// hook, seal included, ran after the pass's now was read.
			sessionstore.MarkTerminated(r, session.StateAwaitingClientAction, w.clock())
			return nil
		})
		if errors.Is(err, sessionstore.ErrSuperseded) {
			end()
			continue
		}
		if err != nil {
			end()
			return err
		}
		// spec: §7.3 — fire the awaiting-action-specific
		// audit hook BEFORE the generic terminal hook so the
		// `session.expired_in_awaiting_action` row precedes the
		// `session.expired` row in the §11.7 chain. F-7.3.25.
		if notifier, ok := w.terminal.(AwaitingClientActionExpiryNotifier); ok {
			notifier.OnSessionExpiredFromAwaitingClientAction(ctx, updated)
		}
		res.Expirations++
		// spec: §16.1.1 — the §7.3 awaiting_client_action wall-clock
		// deadline is a `maxResumeWindowSeconds` reclamation; its
		// `expired:deadline` failure reason shares the age-cap series, so
		// the session_expiry counter stamps `max_session_age`. F-7.3.25.
		w.notifySessionExpiry(ctx, updated, ExpiryReasonMaxSessionAge)
		w.recordCompleted(ctx, session.StateAwaitingClientAction, updated)
		end()
	}
	return nil
}

// idleClockRunningStates is the set of states in which the §6.2
// `maxClientIdleSeconds` clock is evaluated: `running` alone. The clock is
// not evaluated in `input_required`, where the runtime waits on the client,
// and is paused in `awaiting_client_action`, `suspended`, the recovery
// states, and the pre-running and terminal states, which this list omits.
//
// spec: §6.2 (maxClientIdleSeconds clock behavior across states).
var idleClockRunningStates = []session.State{
	session.StateRunning,
}

// sweepIdle implements the §6.2 `maxClientIdleSeconds` control for the
// tenant's `running` sessions. A session with no qualifying activity (the
// §6.2 qualifying events the activity stamper records on
// `last_agent_activity_at`) for longer than its effective
// `maxClientIdleSeconds` is idle-suspended when it can be (§6.2 Idle
// suspension.): the sweep hands it to the IdleSuspender, which returns at
// once, when one is wired and the IdleResolver answers that the session can
// be idle-suspended. A session that cannot be idle-suspended, and every idle
// session on a gateway with no suspender, is transitioned to `expired` with
// reason `expired:idle`, as before. A resolver error skips the row this
// tick.
//
// The idle anchor is `LastAgentActivityAt`; when it is zero (no qualifying
// event recorded yet) the sweep falls back to StateEnteredAt, the instant the
// session entered the current state. The sweep reads the anchor; it sets
// nothing on it.
//
// spec: §6.2 (maxClientIdleSeconds clock behavior across states); §11.3.
func (w *Watchdog) sweepIdle(ctx context.Context, tenant string, now time.Time, res *Result) error {
	platformCap := w.cfg.MaxIdleSeconds
	rows, err := w.store.List(ctx, tenant, sessionstore.ListFilter{State: session.StateRunning})
	if err != nil {
		return err
	}
	for _, row := range rows {
		capSeconds := platformCap
		if w.idleResolver != nil {
			if secs := w.idleResolver.EffectiveMaxIdleSeconds(ctx, row); secs > 0 {
				capSeconds = secs
			}
		}
		if capSeconds <= 0 {
			continue
		}
		idleCap := time.Duration(capSeconds) * time.Second
		if !idleElapsed(row, now, idleCap) {
			continue
		}
		suspend, ok := w.idleSuspendable(ctx, row)
		if !ok {
			continue
		}
		if suspend {
			w.suspender.SuspendIdle(ctx, row, capSeconds)
			res.IdleSuspensions++
			continue
		}
		if err := w.expireIdle(ctx, tenant, row, now, idleCap, res); err != nil {
			return err
		}
	}
	return nil
}

// idleSuspendable reports whether an idle row goes to the idle suspender.
// ok is false when the suspendability check failed, so the sweep skips the
// row this tick. With no suspender, or an IdleResolver that cannot answer,
// the row is expired.
// spec: §6.2 (maxClientIdleSeconds clock behavior across states).
func (w *Watchdog) idleSuspendable(ctx context.Context, row sessionstore.Session) (suspend, ok bool) {
	if w.suspender == nil {
		return false, true
	}
	check, can := w.idleResolver.(IdleSuspendability)
	if !can {
		return false, true
	}
	suspendable, err := check.IdleSuspendable(ctx, row)
	if err != nil {
		slog.Warn("watchdog: decide whether an idle session can be idle-suspended", "session_id", row.ID, "error", err)
		return false, false
	}
	return suspendable, true
}

// idleAnchor is the instant the row's §6.2 `maxClientIdleSeconds` clock
// runs from: its last qualifying activity, or, when none is recorded, the
// instant it entered its current state.
// spec: §6.2 (maxClientIdleSeconds clock behavior across states).
func idleAnchor(row sessionstore.Session) time.Time {
	if row.LastAgentActivityAt.IsZero() {
		return row.StateEnteredAt
	}
	return row.LastAgentActivityAt
}

// idleElapsed reports whether row has been idle longer than idleCap at now.
// spec: §6.2 (maxClientIdleSeconds clock behavior across states).
func idleElapsed(row sessionstore.Session, now time.Time, idleCap time.Duration) bool {
	return now.Sub(idleAnchor(row)) > idleCap
}

// expireIdle writes the idle expiry of a `running` session that cannot be
// idle-suspended. The write is a terminal writer on the funnel path, guarded
// on the locked row still being `running` and still idle past idleCap at now,
// so an activity stamp or a re-entry to `running` that lands after the
// listing keeps the row. A guard miss changes nothing. The write stamps
// `expired:idle` over any reason an earlier recovery left on the row.
// spec: §6.2 (maxClientIdleSeconds clock behavior across states); §10.1.1 (Primary:).
func (w *Watchdog) expireIdle(ctx context.Context, tenant string, row sessionstore.Session, now time.Time, idleCap time.Duration, res *Result) error {
	var from session.State
	end := w.beginTerminal(row.ID)
	defer end()
	updated, err := w.store.Update(ctx, tenant, row.ID, func(r *sessionstore.Session) error {
		if !isIdleClockRunning(r.State) || !idleElapsed(*r, now, idleCap) {
			// spec: §10.1.1 (Primary:) — the locked row left `running`, or
			// is inside the idle cap; leave it.
			return sessionstore.ErrSuperseded
		}
		from = r.State
		r.State = session.StateExpired
		// spec: §8.8 — the open-ended `expired:*` prefix distinguishes
		// idle reclamation from the active-time `maxSessionAge` cap
		// (`expired:deadline`). F-11.3.7.
		r.FailureReason = string(session.FailureExpiredIdle)
		// spec: §5.2 (Slot assignment atomicity) — the stamp reads the clock
		// inside this row's Update, because an earlier row's post-terminal
		// hook, seal included, ran after the pass's now was read.
		sessionstore.MarkTerminated(r, from, w.clock())
		return nil
	})
	if errors.Is(err, sessionstore.ErrSuperseded) {
		return nil
	}
	if err != nil {
		return err
	}
	res.IdleExpirations++
	res.Expirations++
	// spec: §16.1.1 — the idle expiry stamps the `max_idle_time` reason on
	// the session_expiry counter. F-11.3.7.
	w.notifySessionExpiry(ctx, updated, ExpiryReasonMaxIdleTime)
	w.recordCompleted(ctx, from, updated)
	return nil
}

// isIdleClockRunning reports whether st is the state in which the §6.2
// `maxClientIdleSeconds` clock is evaluated. The transition guard uses it so
// a row that leaves `running` between the list and the update is left alone.
// spec: §6.2 (maxClientIdleSeconds clock behavior across states). F-11.3.7.
func isIdleClockRunning(st session.State) bool {
	for _, s := range idleClockRunningStates {
		if st == s {
			return true
		}
	}
	return false
}

// errMaxAgeSuperseded is the sentinel the sweepMaxAge mutator returns when the
// locked row no longer qualifies for expiry under either leg: it is terminal,
// or its active age recomputed on the locked row is at or under the cap (or
// the row is suspended, which no age cap expires) and it is not past its
// perChildMaxAge. Returning an error aborts the Update, so the row is not
// rewritten, its UpdatedAt does not advance, and the sweep runs no expiry side
// effect for it.
// spec: §6.2 (maxSessionAge timer behavior across states); §8.2.
var errMaxAgeSuperseded = errors.New("watchdog: maxSessionAge expiry superseded on the locked row")

// sweepMaxAge expires every non-terminal session for the tenant that is past
// either of two wall-clock bounds.
//
// The age leg expires a session whose active session age exceeds its
// effective maxSessionAge cap. Active age is sessionstore.ActiveAge: the
// accumulated_session_age_seconds the store accrues on every write, plus the
// time since the last write while the row is in a counting state (starting,
// running, input_required). Time in suspended, resume_pending, resuming, and
// awaiting_client_action does not count, so a session older than the cap by
// wall clock and under it by active time survives. The cap is
// EffectiveAgeCap: the platform cap (w.cfg.MaxSessionAgeSeconds), tightened by
// the per-runtime / per-pool resolver and by the per-session
// retryPolicy.maxSessionAgeSeconds (F-7.3.24). A suspended row is never
// expired by the age leg, because its timer is paused and its own suspended
// lifetime bounds it.
//
// An over-cap starting, resume_pending, resuming, or awaiting_client_action
// row is still expired by the age leg. Its age stopped accruing when it left a
// counting state, so it is over the cap only when it left that state already
// past it, and §7.2 lists a maxSessionAge expiry edge from each of those
// states (resume_pending → expired and awaiting_client_action → expired among
// them). Leaving an over-cap resume_pending row would not let it recover:
// DriveResume's readResumable refuses an over-cap row, so the row would only
// wait out maxResumeWindowSeconds.
//
// The lease leg expires a delegated child past its perChildMaxAge: the slice
// its parent granted, stored on the child's own row, counted in wall-clock
// time from the child's creation in every non-terminal state, suspended
// included (leaseDue). Reading the child's own stored slice keeps a later
// lease extension of the parent from moving an existing child's bound.
//
// The listed row comes from a read-pool listing and only selects the row: the
// mutator recomputes both legs on the locked row with the sweep's now and
// returns errMaxAgeSuperseded when neither holds. On that sentinel the sweep
// writes nothing, runs no side effect, and goes on to the next row. Any other
// Update error ends the tick. The write stamps `expired:lease` when only the
// lease leg holds and `expired:deadline` otherwise, replacing any reason an
// earlier recovery left, and the §16.1.1 max_session_age expiry notification
// fires only when the age leg holds.
//
// spec: §6.2 (maxSessionAge timer behavior across states); §7.2; §8.2; §10.1.1
// (Primary:); §11.3.
func (w *Watchdog) sweepMaxAge(ctx context.Context, tenant string, now time.Time, res *Result) error {
	rows, err := w.store.List(ctx, tenant, sessionstore.ListFilter{})
	if err != nil {
		return err
	}
	platformCap := time.Duration(w.cfg.MaxSessionAgeSeconds) * time.Second
	for _, row := range rows {
		if session.IsTerminal(row.State) {
			continue
		}
		ageCap := EffectiveAgeCap(ctx, w.ageResolver, platformCap, row)
		if !maxAgeDue(row, ageCap, now) && !perChildMaxAgeDue(row, now) {
			continue
		}
		var from session.State
		var ageDue bool
		end := w.beginTerminal(row.ID)
		updated, err := w.store.Update(ctx, tenant, row.ID, func(r *sessionstore.Session) error {
			if session.IsTerminal(r.State) {
				return errMaxAgeSuperseded
			}
			ageDue = maxAgeDue(*r, ageCap, now)
			if !ageDue && !perChildMaxAgeDue(*r, now) {
				return errMaxAgeSuperseded
			}
			from = r.State
			r.State = session.StateExpired
			// spec: §8.8, §8.2 — stamp the `expired:*` prefix the MCP adapter
			// surfaces: `expired:lease` when only the perChildMaxAge leg holds,
			// `expired:deadline` for the §11.3 maxSessionAge cap otherwise.
			// The stamp is unconditional so an earlier recovery's reason does
			// not survive the expiry.
			r.FailureReason = string(session.FailureExpiredDeadline)
			if !ageDue {
				r.FailureReason = string(session.FailureExpiredLease)
			}
			// spec: §5.2 (Slot assignment atomicity) — the stamp reads the clock
			// inside this row's Update, because an earlier row's post-terminal
			// hook, seal included, ran after the pass's now was read.
			sessionstore.MarkTerminated(r, from, w.clock())
			return nil
		})
		if errors.Is(err, errMaxAgeSuperseded) {
			end()
			continue
		}
		if err != nil {
			end()
			return err
		}
		res.Expirations++
		if ageDue {
			// spec: §16.1.1 — the maxSessionAge age-cap expiry stamps the
			// `max_session_age` reason on the session_expiry counter. A
			// perChildMaxAge expiry is not a platform clock and fires none.
			w.notifySessionExpiry(ctx, updated, ExpiryReasonMaxSessionAge)
		}
		// spec: §4.6 — from is the locked row's pre-terminal state.
		// sweepMaxAge can force a running/resuming session to expired;
		// passing the true pre-terminal state keeps its teardown on the
		// §6.2 executor recycle path rather than the pre-running by-name
		// reclaim.
		w.recordCompleted(ctx, from, updated)
		end()
	}
	return nil
}

// maxAgeDue reports whether row is past its maxSessionAge cap by active age
// in a state the age leg may expire.
// spec: §6.2 (maxSessionAge timer behavior across states).
func maxAgeDue(row sessionstore.Session, ageCap time.Duration, now time.Time) bool {
	return maxAgeExpirable(row.State) && sessionstore.ActiveAge(row, now) > ageCap
}

// perChildMaxAgeDue reports whether row is a delegated child that has lived
// longer than the perChildMaxAge its stored slice grants, counted in wall-clock
// time from its creation. A root session, a child with no stored slice, and a
// child whose slice carries no perChildMaxAge are never due.
// spec: §8.2.
func perChildMaxAgeDue(row sessionstore.Session, now time.Time) bool {
	if row.ParentSessionID == "" || row.DelegationLease == nil || row.DelegationLease.PerChildMaxAge <= 0 {
		return false
	}
	return now.Sub(row.CreatedAt) > time.Duration(row.DelegationLease.PerChildMaxAge)*time.Second
}

// maxAgeExpirable reports whether sweepMaxAge may expire a row in st: any
// non-terminal state other than suspended, which no age cap expires.
// spec: §6.2 (maxSessionAge timer behavior across states).
func maxAgeExpirable(st session.State) bool {
	return !session.IsTerminal(st) && st != session.StateSuspended
}

// sweepExpiryWarning implements the §11.3 session-expiry warning:
// the gateway sends a `session_expiring_soon` event to the client and a
// `DEADLINE_APPROACHING` CH-RUNTIMEOPS signal to the pod five minutes
// (ExpiryWarningSeconds) before a session's effective maxSessionAge deadline
// so the agent can checkpoint and the client can extend or wrap up.
//
// The sweep is a no-op unless the wired TerminalHook also implements
// ExpiryWarningNotifier (the gateway sessionserver, which owns the SSE bus
// and the per-session adapter binding). Each session is warned at most once:
// the warned set dedups across ticks and is pruned when a session is observed
// terminal, so the map stays bounded by the live session count.
//
// The warning fires while the session is still non-terminal; the actual
// expiry is the later sweepMaxAge transition. The deadline is the same
// most-restrictive-wins EffectiveAgeCap the maxSessionAge sweep applies,
// compared with the same active age (sessionstore.ActiveAge), so a
// per-runtime / per-session tighter cap warns at the right moment and time
// spent paused does not move the warning earlier.
//
// Only a row whose age timer is running (sessionstore.SessionAgeCounts) is
// warned; a paused row reaches the band only after it re-enters a counting
// state.
//
// spec: §11.3; §6.2 (maxSessionAge timer behavior across states); §15. F-11.3.5.
func (w *Watchdog) sweepExpiryWarning(ctx context.Context, tenant string, now time.Time, res *Result) error {
	notifier, ok := w.terminal.(ExpiryWarningNotifier)
	if !ok {
		return nil
	}
	rows, err := w.store.List(ctx, tenant, sessionstore.ListFilter{})
	if err != nil {
		return err
	}
	window := time.Duration(w.cfg.ExpiryWarningSeconds) * time.Second
	platformCap := time.Duration(w.cfg.MaxSessionAgeSeconds) * time.Second
	for _, row := range rows {
		if session.IsTerminal(row.State) {
			// Prune so the dedup set tracks only live sessions.
			w.forgetWarned(row.ID)
			continue
		}
		if !sessionstore.SessionAgeCounts(row.State) {
			// The age timer is paused or not yet started (suspended, the
			// recovery states, and the pre-running states), so the deadline is
			// not approaching: no age cap expires a suspended row, and a paused
			// row's remaining time does not count down. Skipping it here keeps
			// the single dedup entry for the warning that precedes the real
			// expiry once the row re-enters a counting state.
			continue
		}
		cap := EffectiveAgeCap(ctx, w.ageResolver, platformCap, row)
		remaining := cap - sessionstore.ActiveAge(row, now)
		// Only warn inside the [deadline-window, deadline) band. A session
		// already past its deadline is left for sweepMaxAge; one further than
		// the window away is not yet eligible.
		if remaining <= 0 || remaining > window {
			continue
		}
		if !w.markWarned(row.ID) {
			continue
		}
		notifier.OnSessionExpiringSoon(ctx, row, int(cap.Seconds()), int(remaining.Seconds()))
		res.ExpiryWarnings++
	}
	return nil
}

// markWarned records sessionID in the expiry-warning dedup set and reports
// true when the session had not been warned before (so the caller fires the
// warning exactly once). F-11.3.5.
func (w *Watchdog) markWarned(sessionID string) bool {
	w.warnedMu.Lock()
	defer w.warnedMu.Unlock()
	if w.warned == nil {
		w.warned = map[string]struct{}{}
	}
	if _, seen := w.warned[sessionID]; seen {
		return false
	}
	w.warned[sessionID] = struct{}{}
	return true
}

// forgetWarned drops sessionID from the dedup set once the sweep observes the
// session reach a terminal state, keeping the set bounded by live sessions.
func (w *Watchdog) forgetWarned(sessionID string) {
	w.warnedMu.Lock()
	defer w.warnedMu.Unlock()
	delete(w.warned, sessionID)
}

// EffectiveAgeCap resolves the §11.3 maxSessionAge cap for one row, applying
// the most-restrictive-wins precedence: platformCap (the gateway's
// --max-session-age-seconds) is the outer bound; the §5.1 per-runtime
// `limits.maxSessionAgeSeconds` / §5.2 per-pool `maxSessionAgeSeconds` (via
// resolver) clamps below it; and the §7.3 per-session
// `retryPolicy.maxSessionAgeSeconds` clamps below that. A nil resolver or a
// zero resolver result leaves platformCap in place.
//
// The cap bounds the row's active age (sessionstore.ActiveAge). It is the one
// resolver of the min-wins logic: the maxSessionAge and expiry-warning sweeps
// call it, and so does any other site that evaluates the timer, such as the
// check on entry to running, so every site compares against the same value
// for the same row.
//
// spec: §11.3 — deployer cap tuned per runtime; §5.1 limits; §5.2 pool;
// §6.2 (maxSessionAge timer behavior across states). F-11.3.3.
func EffectiveAgeCap(ctx context.Context, resolver SessionAgeResolver, platformCap time.Duration, row sessionstore.Session) time.Duration {
	cap := platformCap
	if resolver != nil {
		if secs := resolver.EffectiveMaxSessionAgeSeconds(ctx, row); secs > 0 {
			d := time.Duration(secs) * time.Second
			if cap <= 0 || d < cap {
				cap = d
			}
		}
	}
	return effectiveMaxSessionAge(row, cap)
}

// effectiveMaxSessionAge resolves the §7.3 per-session retryPolicy
// override against the deployer cap. A nil policy or a zero value
// falls through to the platform cap; a positive override clamps to
// the smaller of the two so the per-session value can never exceed
// the platform bound regardless of how the row was persisted.
//
// spec: §7.3;
// §11.3 maxSessionAge cap. F-7.3.24.
func effectiveMaxSessionAge(row sessionstore.Session, platformCap time.Duration) time.Duration {
	if row.RetryPolicy == nil || row.RetryPolicy.MaxSessionAgeSeconds <= 0 {
		return platformCap
	}
	override := time.Duration(row.RetryPolicy.MaxSessionAgeSeconds) * time.Second
	if platformCap > 0 && override > platformCap {
		return platformCap
	}
	return override
}

// Run drives the watchdog with a time.Ticker until ctx is cancelled.
// The supplied callback (if non-nil) receives the per-tick result so
// tests can observe sweeps without polling state.
func (w *Watchdog) Run(ctx context.Context, onTick func(Result, error)) {
	t := time.NewTicker(w.cfg.TickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			res, err := w.Tick(ctx, w.clock())
			if onTick != nil {
				onTick(res, err)
			}
		}
	}
}

// beginTerminal opens the terminal hook's writer claim for the session, or
// returns a no-op end when no hook is wired. spec: §7.2 (Interactive Session
// Model).
func (w *Watchdog) beginTerminal(sessionID string) func() {
	if w.terminal == nil {
		return func() {}
	}
	return w.terminal.BeginTerminal(sessionID)
}

// AgeCap returns the effective maxSessionAge cap of row, the value the
// maxSessionAge sweep applies. spec: §6.2 (maxSessionAge timer behavior
// across states).
func (w *Watchdog) AgeCap(ctx context.Context, row sessionstore.Session) time.Duration {
	return EffectiveAgeCap(ctx, w.ageResolver, time.Duration(w.cfg.MaxSessionAgeSeconds)*time.Second, row)
}

// recordCompleted runs the side effects of a session the watchdog
// forced to a terminal state. When a TerminalHook is wired (production
// gateway path), it delegates the full terminal pipeline — workspace
// seal, executor release (which releases concurrent-mode slots per §5.2 and drains pods per §6.2), audit, SSE, billing, archive — to
// the hook so the watchdog-driven path emits the same signals exactly
// once as the REST-driven terminal path. Without a hook the watchdog
// falls back to its own §11.2.1 billing event and §8.10 child archive.
// Best-effort either way: a failure must not abort the sweep.
//
// fromState is the session's pre-terminal state, which each sweep captures
// before forcing the row terminal, so the hook's pod-release path can
// distinguish a pre-running claimed session (created/finalizing/ready) from a
// maxSessionAge-expired running/resuming one (§4.6).
//
// spec: §5.2 — concurrent-mode slot release on session end;
// §11.2.1 — billing; §8.10 — child archive; §4.6 — durable binding.
func (w *Watchdog) recordCompleted(ctx context.Context, fromState session.State, sess sessionstore.Session) {
	if w.terminal != nil {
		w.terminal.OnSessionTerminal(ctx, fromState, sess)
		return
	}
	w.archiveChild(ctx, sess)
	if w.billing == nil {
		return
	}
	// spec: §10.6 — watchdog-driven terminal also stamps env.
	// F-10.6.9. spec: §11.2 — experiment/variant
	// auto-population on the watchdog-forced terminal event. F-11.2.13.
	expID, varID := sess.ExperimentContext.Enrollment()
	_, _ = w.billing.Append(ctx, billingstore.Event{
		TenantID:      sess.TenantID,
		UserID:        sess.UserID,
		SessionID:     sess.ID,
		EventType:     billingstore.EventSessionCompleted,
		EnvironmentID: sess.Environment,
		ExperimentID:  expID,
		VariantID:     varID,
		// spec: §14 — the watchdog-forced terminal event also
		// carries the session's labels so the metering stream stays
		// label-filterable. F-14.1.13.
		Labels: sess.Labels,
	})
}

// archiveChild records a child session the watchdog forced terminal in
// the §8.10 session_tree_archive, keyed by the delegation tree's root.
// A session with no parent is the tree root and is not archived.
func (w *Watchdog) archiveChild(ctx context.Context, sess sessionstore.Session) {
	if w.archive == nil || sess.ParentSessionID == "" {
		return
	}
	result, _ := json.Marshal(map[string]any{
		"schemaVersion": 1,
		"taskId":        sess.ID,
		"state":         string(sess.State),
		"error": map[string]any{
			"code":    "CHILD_" + strings.ToUpper(string(sess.State)),
			"message": "child session ended in state " + string(sess.State),
		},
	})
	_ = w.archive.Archive(ctx, treearchive.ArchivedNode{
		TenantID:        sess.TenantID,
		RootSessionID:   w.treeRoot(ctx, sess),
		NodeSessionID:   sess.ID,
		ParentSessionID: sess.ParentSessionID,
		State:           string(sess.State),
		Result:          string(result),
		SettledAt:       w.clock(),
	})
}

// treeRoot returns the delegation tree's root by walking the
// ParentSessionID chain up from sess. The visited set guards against a
// malformed cyclic chain.
func (w *Watchdog) treeRoot(ctx context.Context, sess sessionstore.Session) string {
	cur := sess
	visited := map[string]bool{}
	for cur.ParentSessionID != "" && !visited[cur.ID] {
		visited[cur.ID] = true
		parent, err := w.store.Get(ctx, cur.TenantID, cur.ParentSessionID)
		if err != nil {
			break
		}
		cur = parent
	}
	return cur.ID
}

// elapsed reports whether a row that entered state st at enteredAt (its
// StateEnteredAt) has been in that state at least the configured budget.
// spec: §6.2 (Pod State Machine).
func (w *Watchdog) elapsed(enteredAt time.Time, st session.State, now time.Time) bool {
	budget := w.budgetFor(st)
	if budget == 0 {
		return false
	}
	return now.Sub(enteredAt) >= time.Duration(budget)*time.Second
}

// budgetFor returns the configured budget (in seconds) for the given
// state. Returns 0 for states the watchdog does not bound.
func (w *Watchdog) budgetFor(st session.State) int {
	switch st {
	case session.StateCreated:
		return w.cfg.MaxCreatedSeconds
	case session.StateFinalizing:
		return w.cfg.MaxFinalizingSeconds
	case session.StateReady:
		return w.cfg.MaxReadySeconds
	case session.StateStarting:
		return w.cfg.MaxStartingSeconds
	default:
		return 0
	}
}

// reasonForState returns the §6.2 / §11.3 reason code for a session
// timed out in the given state.
func (w *Watchdog) reasonForState(st session.State) string {
	switch st {
	case session.StateCreated:
		return ReasonCreatedTimeout
	case session.StateFinalizing:
		return ReasonFinalizeTimeout
	case session.StateReady:
		return ReasonReadyTimeout
	case session.StateStarting:
		return ReasonStartingTimeout
	default:
		return ""
	}
}

// classForReason maps a reason to a §7.1 FailureClass. Only
// STARTING_TIMEOUT has a dedicated FailureClass; the others map to
// the closest catch-all (`runtime_failure`) until §7.1 grows
// dedicated classes for them.
func (w *Watchdog) classForReason(reason string) session.FailureClass {
	switch reason {
	case ReasonStartingTimeout:
		return session.FailureClassStartingTimeout
	default:
		return session.FailureClassRuntime
	}
}
