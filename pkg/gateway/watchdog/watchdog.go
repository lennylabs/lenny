// SPDX-License-Identifier: MIT

// Package watchdog implements the §6.2 / §11.3 pre-running state-
// lifetime watchdogs. Sessions stuck in `created`, `finalizing`,
// `ready`, or `starting` past their configured wall-clock budget are
// transitioned to `failed` so they cannot pin warm pods indefinitely.
//
// The watchdog is store-agnostic: it sweeps every session reachable
// via the SessionStore interface, computes the elapsed time in the
// current state from the row's UpdatedAt, and applies the §6.2
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
// and transition it to `failed`. The maxSessionAge cap bounds the
// total lifetime of any non-terminal session, measured from its
// creation, and transitions an over-age session to `expired`. The
// maxAwaitingClientAction deadline bounds the awaiting_client_action
// state and likewise transitions an over-deadline session to
// `expired`. The watchdog never operates on terminal sessions.
package watchdog

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/treearchive"
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
	// ExpiryReasonMaxIdleTime is the §6.2 `maxClientIdleSeconds` idle-clock
	// expiry: a clock-running session reclaimed for continuous client
	// inactivity. It shares the `expiryReason` vocabulary of the §14
	// `dev.lenny.session_expired` event. spec: §16.1.1; §6.2.
	ExpiryReasonMaxIdleTime = "max_idle_time"
	// ExpiryReasonMaxSessionAge is the §11.3 `maxSessionAge` age-cap expiry
	// (and the §7.3 `awaiting_client_action` wall-clock deadline, which shares
	// the age-cap series). spec: §16.1.1; §11.3 line 198.
	ExpiryReasonMaxSessionAge = "max_session_age"
)

// ResumeTimeoutOutcome enumerates the two §6.2 line 246-254 outcomes the
// watchdog records when a `resuming` row exceeds its 300s budget.
const (
	// ResumingRetryOutcome is the §6.2 line 249 path: retries remain, the
	// session falls back to resume_pending for another attempt.
	ResumingRetryOutcome = "retry"
	// ResumingExhaustedOutcome is the §6.2 line 249 path: retries are
	// exhausted, the session transitions to awaiting_client_action.
	ResumingExhaustedOutcome = "exhausted"
)

// Default budgets per §11.3 line 191-205. Each is operator-tunable
// through the matching Config field. The `runtime.maxSessionAgeSeconds`
// per-runtime override (§5.1) further caps a runtime's session lifetime
// below the platform default; the watchdog applies the per-row UpdatedAt
// or createdAt comparison against the effective deadline.
const (
	// DefaultMaxCreatedStateSeconds is the §11.3 line 218 default for
	// the `created` pre-running state: 300s.
	DefaultMaxCreatedStateSeconds = 300

	// DefaultMaxFinalizingStateSeconds is the §11.3 line 219 default
	// for the `finalizing` pre-running state: 600s. The
	// `maxFinalizingTimeoutSeconds ≥ setupTimeoutSeconds` invariant
	// (§6.2 line 260) is enforced at runtime admission; see
	// pkg/gateway/admin.validateSetupPolicy.
	DefaultMaxFinalizingStateSeconds = 600

	// DefaultMaxReadyStateSeconds is the §11.3 line 220 default for
	// the `ready` pre-running state: 300s.
	DefaultMaxReadyStateSeconds = 300

	// DefaultMaxStartingStateSeconds is the §11.3 line 221 default
	// for the `starting` pre-running state: 120s.
	DefaultMaxStartingStateSeconds = 120

	// DefaultMaxSessionAgeSeconds is the §11.3 line 198 / §6.2 line
	// 240 platform-wide total session lifetime cap: 7200s (2h). The
	// per-runtime `runtime.maxSessionAgeSeconds` (§5.1 limits) caps a
	// runtime's session lifetime below this default; when unset, the
	// platform default applies to every session of that runtime, so
	// this constant is the source-of-truth for the deployer-visible
	// upper bound on a session that does not opt into a tighter
	// per-runtime limit.
	DefaultMaxSessionAgeSeconds = 7200

	// DefaultMaxAwaitingClientActionSeconds is the §11.3 line 199
	// deadline for the `awaiting_client_action` state: 900s. The
	// 900s budget resets on each re-entry into the state — see
	// `sweepAwaitingClientAction` below.
	DefaultMaxAwaitingClientActionSeconds = 900

	// DefaultMaxIdleSeconds is the §11.3 line 199 / §6.2
	// `maxClientIdleSeconds` platform default: the pool's effective
	// `maxSessionAgeSeconds`, so a session with no resolvable idle bound
	// is reclaimed at the age cap rather than at a fixed 600s (a deliberate
	// relaxation recorded in the §6.2 clock-default rewrite). This package
	// uses DefaultMaxSessionAgeSeconds as the fixed fallback because that is
	// the platform age default the IdleResolver returns 0 against. A session
	// with no qualifying agent activity (§6.2 qualifying events) for longer
	// than its effective cap is expired by the idle sweep. The
	// `sessionPolicy.maxClientIdleSeconds` knob and the §27.6 playground idle
	// override tighten it below this default via the IdleResolver. F-11.3.7.
	DefaultMaxIdleSeconds = DefaultMaxSessionAgeSeconds

	// DefaultMaxSuspendedPodHoldSeconds is the §11.3 line 233 wall-clock
	// cap on the suspended pod-hold window. A session that has been
	// `suspended` longer than this releases its pod (and transitions to
	// `expired` if no resume has begun). Both deploy-wide and per-tenant
	// caps apply; the more restrictive wins, per §11.3 line 233. The
	// per-tenant cap is read from tenant configuration when present and
	// otherwise falls back to this platform default. spec: §11.3 line 233.
	DefaultMaxSuspendedPodHoldSeconds = 900

	// DefaultMaxResumePendingSeconds is the §6.2 line 292 / §7.3 line
	// 404 default for the `resume_pending` wall-clock cap: 900s. A
	// session that has waited this long for a pod to become available
	// is transitioned to `awaiting_client_action`. The deployer cap is
	// the same field as `awaiting_client_action` expiry
	// (maxResumeWindowSeconds in spec), and the per-session
	// retryPolicy.maxResumeWindowSeconds tightens it below the platform
	// cap. spec: §6.2 line 292; §7.3 line 404.
	DefaultMaxResumePendingSeconds = 900

	// DefaultMaxResumingSeconds is the §6.2 line 249 default for the
	// `resuming` 300-second watchdog: matches the setup-command total
	// timeout. On expiry the gateway branches on retry budget — see
	// sweepResuming below. spec: §6.2 line 249.
	DefaultMaxResumingSeconds = 300

	// DefaultMaxRetries is the §7.3 worked-example default for
	// retryPolicy.maxRetries: 2 (3 total attempts). Mirrors
	// policy.DefaultMaxRetries; the watchdog keeps the constant in this
	// package so the resuming sweep does not depend on the interceptor
	// package's retry evaluator. spec: §7.3 line 382.
	DefaultMaxRetries = 2

	// DefaultTickInterval is the §11.3 line 224 default sweep
	// cadence: 5s.
	DefaultTickInterval = 5 * time.Second

	// DefaultExpiryWarningSeconds is the §11.3 line 240 lead time for the
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
	// MaxSessionAgeSeconds is the §11.3 total-lifetime cap: a
	// non-terminal session alive longer than this, measured from its
	// creation, is expired regardless of its current state.
	MaxSessionAgeSeconds int
	// MaxAwaitingClientActionSeconds is the §11.3 deadline for the
	// awaiting_client_action state: a session that has waited this
	// long for client action is expired.
	MaxAwaitingClientActionSeconds int
	// MaxIdleSeconds is the §11.3 line 199 `maxClientIdleSeconds`
	// platform default: a session in a clock-running state with no
	// qualifying agent activity for longer than this (or its tighter
	// per-runtime / per-pool / per-session cap via the IdleResolver) is
	// expired. A zero value falls through to DefaultMaxIdleSeconds (the
	// platform age default, per §6.2).
	MaxIdleSeconds int
	// MaxSuspendedPodHoldSeconds is the §11.3 line 233 deploy-wide cap
	// on the suspended pod-hold window. The per-tenant cap (when set)
	// further tightens this; the watchdog applies the more-restrictive
	// of the two for each row. A zero value falls through to
	// DefaultMaxSuspendedPodHoldSeconds.
	MaxSuspendedPodHoldSeconds int
	// MaxResumePendingSeconds is the §6.2 line 292 wall-clock cap on
	// resume_pending. On expiry the session transitions to
	// awaiting_client_action so the client can intervene. The
	// per-session retryPolicy.maxResumeWindowSeconds tightens it below
	// this platform cap; a zero/unset row-side value falls through.
	MaxResumePendingSeconds int
	// MaxResumingSeconds is the §6.2 line 249 watchdog on resuming. On
	// expiry, the watchdog branches: retries remain → resume_pending
	// and retry counter bumps; otherwise → awaiting_client_action.
	MaxResumingSeconds int
	// MaxRetries is the §7.3 line 382 default retry budget the resuming
	// sweep applies when a row has no per-session override. Zero falls
	// through to DefaultMaxRetries.
	MaxRetries   int
	TickInterval time.Duration
	// ExpiryWarningSeconds is the §11.3 line 240 lead time for the
	// session_expiring_soon warning: a non-terminal session whose effective
	// maxSessionAge deadline is within this many seconds is warned once. A
	// zero value falls through to DefaultExpiryWarningSeconds. F-11.3.5.
	ExpiryWarningSeconds int
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
	// spec: §6.2 line 260; §11.3 line 219. The watchdog enforces the
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
// for concurrent-mode sessions releases the slot per §5.2 line 519),
// §11.7 audit, §7.2 SSE, §11.2.1 billing, and §8.10 archive — run once
// through the same Server.recordSessionCompleted path the REST handlers
// use.
//
// spec: §5.2 line 519 — slot release on session end / expiry; §6.2 lines
// 105-117 — executor release maps the terminal phase onto the Sandbox.
type TerminalHook interface {
	OnSessionTerminal(ctx context.Context, sess sessionstore.Session)
}

// AwaitingClientActionExpiryNotifier is an optional hook on TerminalHook
// implementations. The watchdog invokes it for the
// `awaiting_client_action → expired` edge BEFORE the generic
// OnSessionTerminal so the §11.7 / §16.7 session.expired_in_awaiting_action
// audit row carries the awaiting-action context (which the generic
// terminal hook cannot derive from the post-update row alone).
//
// spec: §7.3 line 423 — `awaiting_client_action → expired` entry path;
// §11.7 / §16.7 audit row. F-7.3.25.
type AwaitingClientActionExpiryNotifier interface {
	OnSessionExpiredFromAwaitingClientAction(ctx context.Context, sess sessionstore.Session)
}

// AwaitingClientActionEntryNotifier is an optional hook on TerminalHook
// implementations. The watchdog invokes it whenever it transitions a
// non-terminal row into `awaiting_client_action` — the §6.2 line 292
// resume_pending wall-clock cap and the §6.2 line 249 resuming
// retries-exhausted branch. The hook is the entry-point for the §7.3
// line 427 `session.awaiting_action` webhook, the §16.6 operational
// event, and the §11.7 / §16.7 audit row that every entry into the
// state must emit.
//
// The notifier MUST NOT be confused with AwaitingClientActionExpiryNotifier
// (which fires on the terminal `awaiting → expired` edge). Entry and
// expiry are distinct events and emit distinct audit rows.
//
// spec: §6.2 line 292 (resume_pending → awaiting_client_action);
// §6.2 line 249 (resuming retries-exhausted → awaiting_client_action);
// §7.3 line 427 (session.awaiting_action webhook). F-6.2.14.
type AwaitingClientActionEntryNotifier interface {
	OnSessionEnteredAwaitingClientAction(ctx context.Context, sess sessionstore.Session)
}

// RetryAttemptNotifier is an optional hook on TerminalHook
// implementations. The watchdog invokes it whenever the resuming sweep
// drives a `resuming → resume_pending` retry, so the §16.1
// lenny_session_retry_total counter and the §11.7 / §16.7
// session.retry_attempted audit row see the watchdog-initiated retry
// in addition to the gateway-initiated retries the
// `bumpRecoveryGeneration` path already covers.
//
// spec: §6.2 line 249 (resuming → resume_pending retry path);
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
//   - `max_idle_time` on the §6.2 `maxClientIdleSeconds` idle-clock expiry, and
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
// vocabulary); §6.2 (maxClientIdleSeconds clock); §11.3 line 199 (max client
// idle row). F-11.3.7.
type SessionExpiryNotifier interface {
	OnSessionExpired(ctx context.Context, sess sessionstore.Session, reason string)
}

// ExpiryWarningNotifier is an optional hook on TerminalHook implementations.
// The watchdog invokes it once per session, when the session's effective
// maxSessionAge deadline first falls within the ExpiryWarningSeconds window
// (default 300s), so the gateway emits the §11.3 line 240 session_expiring_soon
// SSE event to the client and dispatches the DEADLINE_APPROACHING lifecycle-
// channel signal to the running pod. maxSessionAgeSeconds is the session's
// resolved deadline (for the SSE payload) and remainingSeconds is the
// wall-clock left before it.
//
// The notification is best-effort and fires at most once per session per
// gateway process: a watchdog restart inside the window may re-warn, which
// SSE clients tolerate as an advisory event.
//
// spec: §11.3 line 240 (session_expiring_soon + DEADLINE_APPROACHING);
// §15 line 2141 (Basic/Standard runtimes receive no advance notice). F-11.3.5.
type ExpiryWarningNotifier interface {
	OnSessionExpiringSoon(ctx context.Context, sess sessionstore.Session, maxSessionAgeSeconds, remainingSeconds int)
}

// DLQSweeper runs the §7.2 line 341 dead-letter-queue TTL sweep for one
// recovering session, emitting a `message_expired` event with `reason:
// "dlq_ttl_expired"` per expired entry and returning the count removed.
// *sessioninbox.Coordinator satisfies it. A nil sweeper disables the
// sweep. The §7.2 line 294 trimmer activates only while the session is in
// a recovering state (`resume_pending` or `awaiting_client_action`); the
// watchdog is the periodic background sweeper that enforces that scoping.
//
// spec: §7.2 lines 294, 341.
type DLQSweeper interface {
	SweepExpired(ctx context.Context, tenantID, sessionID string) (expired int, err error)
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
// spec: §11.3 line 198 — `maxSessionAge` is a deployer cap tuned per
// runtime; §5.1 limits.maxSessionAgeSeconds; §5.2 pool maxSessionAgeSeconds.
// F-11.3.3.
type SessionAgeResolver interface {
	EffectiveMaxSessionAgeSeconds(ctx context.Context, sess sessionstore.Session) int
}

// IdleResolver resolves the §11.3 line 199 per-runtime / per-pool /
// per-session `maxClientIdleSeconds` cap (in seconds) for a session row. A
// return of 0 means no configuration surface resolves a cap, so the platform
// default (w.cfg.MaxIdleSeconds) applies. The resolver is optional: a nil
// resolver leaves the idle sweep on the single platform default.
//
// spec: §11.3 line 199; §6.2 (maxClientIdleSeconds clock); §27.6 line 201.
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

	// warnedMu guards warned, the §11.3 line 240 expiry-warning dedup set.
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
// spec: §5.2 line 519 — slot release; §7.2 lines 137, 141 — SSE; §11.7 —
// audit; §6.2 — executor release.
func (w *Watchdog) WithTerminalHook(hook TerminalHook) *Watchdog {
	w.terminal = hook
	return w
}

// WithMessaging wires the §7.2 session-inbox coordinator so each sweep
// runs the dead-letter-queue TTL sweep over every recovering session
// (`resume_pending`, `awaiting_client_action`). The sweep emits a
// `message_expired(dlq_ttl_expired)` event per expired DLQ entry, the
// background-trimmer half of the §7.2 line 294 contract. A nil coordinator
// (the dev / no-Redis posture) leaves the DLQ sweep disabled.
//
// spec: §7.2 lines 294, 341.
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
// spec: §11.3 line 198 — deployer cap tuned per runtime. F-11.3.3.
func (w *Watchdog) WithSessionAgeResolver(r SessionAgeResolver) *Watchdog {
	w.ageResolver = r
	return w
}

// WithIdleResolver wires the §11.3 line 199 per-runtime / per-pool /
// per-session `maxClientIdleSeconds` resolver so the idle sweep honors a
// deployer's `sessionPolicy.maxClientIdleSeconds` and the §27.6 playground
// idle override instead of expiring every clock-running session at the
// single platform default. A nil resolver leaves the platform-default
// behaviour intact.
//
// spec: §11.3 line 199; §6.2 (maxClientIdleSeconds clock). F-11.3.7.
func (w *Watchdog) WithIdleResolver(r IdleResolver) *Watchdog {
	w.idleResolver = r
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
	// IdleExpirations is the count of `running` sessions transitioned to
	// `expired` by the §11.3 line 199 `maxIdleTime` sweep in this tick.
	IdleExpirations int
	// ResumePendingTimeouts is the count of sessions transitioned from
	// resume_pending to awaiting_client_action by the §6.2 line 292
	// wall-clock cap.
	ResumePendingTimeouts int
	// ResumingTimeouts is the count of sessions whose §6.2 line 249
	// resuming watchdog fired in this sweep, broken down by outcome
	// (ResumingRetryOutcome → resume_pending; ResumingExhaustedOutcome
	// → awaiting_client_action) in PerResumingOutcome.
	ResumingTimeouts int
	// PerReason records the count per FailureReason for observability.
	PerReason map[string]int
	// PerResumingOutcome counts the §6.2 line 249 resuming-watchdog
	// disposition.
	PerResumingOutcome map[string]int
	// DLQExpired is the count of dead-letter-queue entries removed by the
	// §7.2 line 341 TTL sweep across every recovering session in this
	// sweep. Each removed entry emits a `message_expired(dlq_ttl_expired)`
	// event on its sender's stream.
	DLQExpired int
	// ExpiryWarnings is the count of sessions newly warned by the §11.3
	// line 240 session_expiring_soon sweep in this tick (each fires the
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
				if !w.elapsed(row.UpdatedAt, st, now) {
					continue
				}
				reason := w.reasonForState(st)
				updated, err := w.store.Update(ctx, tenant, row.ID, func(r *sessionstore.Session) error {
					if r.State != st {
						// Concurrent transition — leave the new
						// state alone.
						return nil
					}
					r.State = session.StateFailed
					r.FailureClass = w.classForReason(reason)
					r.FailureReason = reason
					return nil
				})
				if err != nil {
					return res, err
				}
				if updated.State == session.StateFailed && updated.FailureReason == reason {
					res.ForcedFailures++
					res.PerReason[reason]++
					w.recordCompleted(ctx, updated)
				}
			}
		}
		// spec: §6.2 line 249 (resuming 300s) and §6.2 line 292
		// (resume_pending wall-clock cap). The resuming sweep runs
		// before resume_pending so a resuming → resume_pending retry
		// that lands inside the same Tick does not get re-swept by the
		// resume_pending sweep in the same call (UpdatedAt advances on
		// the resume_pending write, but defence-in-depth: ordering is
		// stable regardless).
		if err := w.sweepResuming(ctx, tenant, now, &res); err != nil {
			return res, err
		}
		if err := w.sweepResumePending(ctx, tenant, now, &res); err != nil {
			return res, err
		}
		// spec: §11.3 line 240 — warn the client and pod 5 minutes before
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
		if err := w.sweepDLQExpiry(ctx, tenant, &res); err != nil {
			return res, err
		}
	}
	return res, nil
}

// sweepDLQExpiry runs the §7.2 line 341 dead-letter-queue TTL sweep over
// every recovering session in tenant. The §7.2 line 294 trimmer is
// state-gated: it activates only while a session is in `resume_pending` or
// `awaiting_client_action`, so the sweep lists exactly those states and
// asks the coordinator to expire past-TTL DLQ entries (emitting a
// `message_expired(dlq_ttl_expired)` event per entry). A per-session sweep
// error is dropped so a transient Redis blip on one session does not stall
// the watchdog tick or the other sessions' sweeps; the next tick retries.
// No-op when messaging is not wired.
//
// spec: §7.2 lines 294, 341.
func (w *Watchdog) sweepDLQExpiry(ctx context.Context, tenant string, res *Result) error {
	if w.messaging == nil {
		return nil
	}
	for _, st := range []session.State{
		session.StateResumePending,
		session.StateAwaitingClientAction,
	} {
		rows, err := w.store.List(ctx, tenant, sessionstore.ListFilter{State: st})
		if err != nil {
			return err
		}
		for _, row := range rows {
			expired, serr := w.messaging.SweepExpired(ctx, tenant, row.ID)
			if serr != nil {
				continue
			}
			res.DLQExpired += expired
		}
	}
	return nil
}

// sweepResumePending implements the §6.2 line 292 wall-clock cap. A
// session that has waited in resume_pending longer than its effective
// maxResumeWindowSeconds transitions to awaiting_client_action so the
// client can intervene. The transition fires the §7.3 line 427
// session.awaiting_action webhook (delivered via the optional entry
// notifier on the terminal hook).
//
// Reset semantics: a session that traverses resume_pending → resuming →
// resume_pending (one retry round-trip) gets a fresh budget on
// re-entry because UpdatedAt advances on the resume_pending write. The
// platform-wide maxSessionAge cap bounds the cumulative case so a
// misbehaving recovery cannot indefinitely re-arm the inner budget.
func (w *Watchdog) sweepResumePending(ctx context.Context, tenant string, now time.Time, res *Result) error {
	rows, err := w.store.List(ctx, tenant,
		sessionstore.ListFilter{State: session.StateResumePending})
	if err != nil {
		return err
	}
	platformCap := time.Duration(w.cfg.MaxResumePendingSeconds) * time.Second
	for _, row := range rows {
		if now.Sub(row.UpdatedAt) <= effectiveMaxResumeWindow(row, platformCap) {
			continue
		}
		updated, err := w.store.Update(ctx, tenant, row.ID, func(r *sessionstore.Session) error {
			if r.State != session.StateResumePending {
				return nil
			}
			r.State = session.StateAwaitingClientAction
			return nil
		})
		if err != nil {
			return err
		}
		if updated.State == session.StateAwaitingClientAction {
			res.ResumePendingTimeouts++
			w.notifyAwaitingClientActionEntered(ctx, updated)
		}
	}
	return nil
}

// sweepResuming implements the §6.2 line 249 resuming watchdog. A
// session whose UpdatedAt + MaxResumingSeconds has passed without the
// resume completing branches on retry budget:
//
//   - RetryCount < effective maxRetries: transition to resume_pending
//     and increment RetryCount; fire the §16.1 retry-attempt metric and
//     §11.7 audit row via the optional RetryAttemptNotifier on the
//     terminal hook.
//   - RetryCount >= effective maxRetries: transition to
//     awaiting_client_action and fire the §7.3 line 427 entry notifier
//     so the client receives a session.awaiting_action webhook.
//
// The §6.2 line 250 non-retryable branch is independent of the
// watchdog — the resume path collapses straight to
// awaiting_client_action when it detects a non-retryable failure class.
// The watchdog handles only the wall-clock timeout. Spec: §6.2 lines
// 246-254.
func (w *Watchdog) sweepResuming(ctx context.Context, tenant string, now time.Time, res *Result) error {
	rows, err := w.store.List(ctx, tenant,
		sessionstore.ListFilter{State: session.StateResuming})
	if err != nil {
		return err
	}
	budget := time.Duration(w.cfg.MaxResumingSeconds) * time.Second
	if budget <= 0 {
		return nil
	}
	for _, row := range rows {
		if now.Sub(row.UpdatedAt) <= budget {
			continue
		}
		maxRetries := effectiveMaxRetries(row, w.cfg.MaxRetries)
		retriesRemain := row.RetryCount < int64(maxRetries)
		updated, err := w.store.Update(ctx, tenant, row.ID, func(r *sessionstore.Session) error {
			if r.State != session.StateResuming {
				return nil
			}
			if retriesRemain {
				r.State = session.StateResumePending
				r.RetryCount++
			} else {
				r.State = session.StateAwaitingClientAction
			}
			return nil
		})
		if err != nil {
			return err
		}
		switch updated.State {
		case session.StateResumePending:
			res.ResumingTimeouts++
			res.PerResumingOutcome[ResumingRetryOutcome]++
			w.notifyRetryAttempt(ctx, updated)
		case session.StateAwaitingClientAction:
			res.ResumingTimeouts++
			res.PerResumingOutcome[ResumingExhaustedOutcome]++
			w.notifyAwaitingClientActionEntered(ctx, updated)
		}
	}
	return nil
}

// notifyAwaitingClientActionEntered fires the AwaitingClientActionEntryNotifier
// on the terminal hook when one is wired. Best-effort: a nil hook or a
// hook that does not implement the optional notifier silently no-ops.
// The §6.2 line 249 / 292 transitions both flow through here so the
// §7.3 line 427 webhook + §11.7 audit row fire exactly once per entry.
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
// lenny_session_retry_total counter and §11.7 audit row track the
// watchdog-initiated retry alongside the gateway-initiated retries the
// bumpRecoveryGeneration path already covers.
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
// idle-clock sweep, ExpiryReasonMaxSessionAge on the age-cap and
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

// effectiveMaxResumeWindow resolves the §7.3 per-session
// retryPolicy.maxResumeWindowSeconds override against the deployer cap.
// A nil policy or zero value falls through to the platform cap; a
// positive override clamps to the smaller so the per-session value can
// never exceed the platform bound regardless of how the row was
// persisted. Mirrors effectiveMaxSessionAge in shape.
//
// spec: §7.3 line 390 (retryPolicy.maxResumeWindowSeconds); §6.2 line
// 292 platform cap. F-6.2.14.
func effectiveMaxResumeWindow(row sessionstore.Session, platformCap time.Duration) time.Duration {
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
// zero value falls through to the deployer default. spec: §7.3 line
// 382 (retryPolicy.maxRetries). F-6.2.14.
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
// maxAwaitingClientAction deadline, measured from the row's UpdatedAt
// (the instant it entered the state). §7.3 governs the transition:
// awaiting_client_action → expired when the deadline is exhausted.
//
// Reset semantics: a session that traverses awaiting_client_action →
// resume_pending → running and later re-enters awaiting_client_action
// gets a fresh 900s budget because UpdatedAt advances on the
// re-entering write. The §11.3 spec text is silent on whether the
// deadline is per-entry or cumulative; the gateway implements the
// per-entry interpretation because §7.3 frames the deadline as the
// inactivity timeout on a single client-action cycle rather than as a
// total-time budget. The platform-wide maxSessionAge cap (§11.3 line
// 198, default 7200s) bounds the cumulative case from above and runs
// in the same sweep, so a misbehaving client cannot indefinitely
// re-arm the inner budget.
func (w *Watchdog) sweepAwaitingClientAction(ctx context.Context, tenant string, now time.Time, res *Result) error {
	rows, err := w.store.List(ctx, tenant,
		sessionstore.ListFilter{State: session.StateAwaitingClientAction})
	if err != nil {
		return err
	}
	deadline := time.Duration(w.cfg.MaxAwaitingClientActionSeconds) * time.Second
	for _, row := range rows {
		if now.Sub(row.UpdatedAt) <= deadline {
			continue
		}
		updated, err := w.store.Update(ctx, tenant, row.ID, func(r *sessionstore.Session) error {
			if r.State != session.StateAwaitingClientAction {
				// Concurrent transition — leave the new state alone.
				return nil
			}
			r.State = session.StateExpired
			// spec: §8.8 line 867 — stamp the `expired:deadline` prefix
			// on the row so the MCP adapter surfaces `failed` with the
			// `expired:*` error code. The awaiting-client-action sweep
			// is the §7.3 line 423 wall-clock deadline. F-8.8.8.
			if r.FailureReason == "" {
				r.FailureReason = string(session.FailureExpiredDeadline)
			}
			return nil
		})
		if err != nil {
			return err
		}
		if updated.State == session.StateExpired {
			// spec: §7.3 line 423 — fire the awaiting-action-specific
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
			w.recordCompleted(ctx, updated)
		}
	}
	return nil
}

// idleClockRunningStates is the §6.2 `maxClientIdleSeconds` clock's own
// pause table, expressed as the states in which the clock runs. The single
// idle bound terminates a session after continuous client inactivity, and
// waiting on an absent client is the condition it exists to reclaim, so the
// clock runs in `running`, `input_required`, and `awaiting_client_action`.
// An elicitation wait keeps the session in `running` (§9.2 — the elicitation
// response is client activity and resets the clock when it arrives), so it is
// covered by the `running` entry and needs no separate predicate. The clock
// is paused in `suspended`, `resume_pending`, `resuming`, `finalizing`, and
// terminal states, which this list omits.
//
// spec: §6.2 (maxClientIdleSeconds clock pause table); §9.2
// (elicitation-wait idle clock). F-11.3.7 / F-9.2.15.
var idleClockRunningStates = []session.State{
	session.StateRunning,
	session.StateInputRequired,
	session.StateAwaitingClientAction,
}

// sweepIdle implements the §11.3 line 199 / §6.2 `maxClientIdleSeconds`
// control. A session in a clock-running state (idleClockRunningStates) that
// has had no qualifying agent activity (the §6.2 qualifying events the
// activity stamper records on `last_agent_activity_at`) for longer than its
// effective `maxClientIdleSeconds` is transitioned to `expired` with reason
// `expired:idle`.
//
// The clock has its own pause table, distinct from the `maxSessionAge` pause
// table: `maxSessionAge` is paused in `awaiting_client_action` while this
// clock runs there, so the default-equal idle bound is the reclaimer for an
// abandoned `awaiting_client_action` wait. The sweep enforces the pause table
// by listing only the running states; the paused states (`suspended`,
// `resume_pending`, `resuming`, `finalizing`, terminal) are never listed.
//
// The idle anchor is `LastAgentActivityAt`; when it is zero (no qualifying
// event recorded yet) the sweep falls back to UpdatedAt — the instant the
// session entered the current state — so a freshly-running session that never
// does anything is reaped one `maxClientIdleSeconds` after it started running,
// not after its creation.
//
// spec: §6.2 (maxClientIdleSeconds clock); §11.3 line 199; §9.2
// (elicitation-wait idle clock). F-11.3.7 / F-9.2.15.
func (w *Watchdog) sweepIdle(ctx context.Context, tenant string, now time.Time, res *Result) error {
	platformCap := w.cfg.MaxIdleSeconds
	for _, st := range idleClockRunningStates {
		rows, err := w.store.List(ctx, tenant, sessionstore.ListFilter{State: st})
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
			anchor := row.LastAgentActivityAt
			if anchor.IsZero() {
				anchor = row.UpdatedAt
			}
			if now.Sub(anchor) <= time.Duration(capSeconds)*time.Second {
				continue
			}
			updated, err := w.store.Update(ctx, tenant, row.ID, func(r *sessionstore.Session) error {
				if !isIdleClockRunning(r.State) {
					// Concurrent transition into a paused state — leave it.
					return nil
				}
				r.State = session.StateExpired
				// spec: §8.8 line 867 — the open-ended `expired:*` prefix
				// distinguishes idle reclamation from the wall-clock
				// `maxSessionAge` deadline (`expired:deadline`). F-11.3.7.
				if r.FailureReason == "" {
					r.FailureReason = string(session.FailureExpiredIdle)
				}
				return nil
			})
			if err != nil {
				return err
			}
			if updated.State == session.StateExpired && updated.FailureReason == string(session.FailureExpiredIdle) {
				res.IdleExpirations++
				res.Expirations++
				// spec: §16.1.1 — the idle-clock expiry stamps the
				// `max_idle_time` reason on the session_expiry counter. F-11.3.7.
				w.notifySessionExpiry(ctx, updated, ExpiryReasonMaxIdleTime)
				w.recordCompleted(ctx, updated)
			}
		}
	}
	return nil
}

// isIdleClockRunning reports whether st is one of the §6.2 states in which the
// `maxClientIdleSeconds` clock runs. The transition guard uses it so a row
// that races into a paused state between the list and the update is left
// alone. spec: §6.2 (maxClientIdleSeconds clock pause table). F-11.3.7.
func isIdleClockRunning(st session.State) bool {
	for _, s := range idleClockRunningStates {
		if st == s {
			return true
		}
	}
	return false
}

// sweepMaxAge expires every non-terminal session for the tenant whose
// total lifetime, measured from CreatedAt, exceeds the effective
// maxSessionAge cap. The §11.3 platform-wide cap (w.cfg.MaxSessionAgeSeconds)
// is the upper bound; a session whose §7.3 retryPolicy.maxSessionAgeSeconds
// is tighter expires earlier (the clamp at gateway admission already
// ensures the per-session value can never exceed the platform cap, so
// this sweep applies the per-session value when it is smaller — F-7.3.24).
//
// The pre-running per-state budgets are tighter and run first, so this
// sweep effectively bounds the long-lived states (running, suspended,
// resume_pending, awaiting_client_action).
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
		if now.Sub(row.CreatedAt) <= w.effectiveAgeCap(ctx, row, platformCap) {
			continue
		}
		updated, err := w.store.Update(ctx, tenant, row.ID, func(r *sessionstore.Session) error {
			if session.IsTerminal(r.State) {
				// Concurrent transition — leave the new state alone.
				return nil
			}
			r.State = session.StateExpired
			// spec: §8.8 line 867 — stamp the `expired:deadline` prefix
			// for the §11.3 maxSessionAge cap so the MCP adapter
			// surfaces `failed` with the `expired:*` error code. F-8.8.8.
			if r.FailureReason == "" {
				r.FailureReason = string(session.FailureExpiredDeadline)
			}
			return nil
		})
		if err != nil {
			return err
		}
		if updated.State == session.StateExpired {
			res.Expirations++
			// spec: §16.1.1 — the maxSessionAge age-cap expiry stamps the
			// `max_session_age` reason on the session_expiry counter.
			w.notifySessionExpiry(ctx, updated, ExpiryReasonMaxSessionAge)
			w.recordCompleted(ctx, updated)
		}
	}
	return nil
}

// sweepExpiryWarning implements the §11.3 line 240 session-expiry warning:
// the gateway sends a `session_expiring_soon` event to the client and a
// `DEADLINE_APPROACHING` lifecycle-channel signal to the pod five minutes
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
// most-restrictive-wins effectiveAgeCap the maxSessionAge sweep applies, so a
// per-runtime / per-session tighter cap warns at the right moment.
//
// spec: §11.3 line 240; §15 line 2141 (Basic/Standard runtimes get no advance
// notice — the adapter reports delivered=false). F-11.3.5.
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
		cap := w.effectiveAgeCap(ctx, row, platformCap)
		remaining := cap - now.Sub(row.CreatedAt)
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

// effectiveAgeCap resolves the §11.3 maxSessionAge deadline for one row,
// applying the most-restrictive-wins precedence: the platform default
// (w.cfg.MaxSessionAgeSeconds) is the outer bound; the §5.1 per-runtime
// `limits.maxSessionAgeSeconds` / §5.2 per-pool `maxSessionAgeSeconds`
// (via the SessionAgeResolver) clamps below it; and the §7.3 per-session
// `retryPolicy.maxSessionAgeSeconds` clamps below that. A nil resolver or a
// zero resolver result leaves the platform cap in place, so the prior
// single-default behaviour is preserved when no per-runtime/pool tuning is
// configured.
//
// spec: §11.3 line 198 — deployer cap tuned per runtime; §5.1 limits;
// §5.2 pool. F-11.3.3.
func (w *Watchdog) effectiveAgeCap(ctx context.Context, row sessionstore.Session, platformCap time.Duration) time.Duration {
	cap := platformCap
	if w.ageResolver != nil {
		if secs := w.ageResolver.EffectiveMaxSessionAgeSeconds(ctx, row); secs > 0 {
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
// spec: §7.3 lines 389-390 (retryPolicy.maxSessionAgeSeconds);
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

// recordCompleted runs the side effects of a session the watchdog
// forced to a terminal state. When a TerminalHook is wired (production
// gateway path), it delegates the full terminal pipeline — workspace
// seal, executor release (which releases concurrent-mode slots per §5.2
// line 519 and drains pods per §6.2), audit, SSE, billing, archive — to
// the hook so the watchdog-driven path emits the same signals exactly
// once as the REST-driven terminal path. Without a hook the watchdog
// falls back to its own §11.2.1 billing event and §8.10 child archive.
// Best-effort either way: a failure must not abort the sweep.
//
// spec: §5.2 line 519 — concurrent-mode slot release on session end;
// §11.2.1 — billing; §8.10 — child archive.
func (w *Watchdog) recordCompleted(ctx context.Context, sess sessionstore.Session) {
	if w.terminal != nil {
		w.terminal.OnSessionTerminal(ctx, sess)
		return
	}
	w.archiveChild(ctx, sess)
	if w.billing == nil {
		return
	}
	// spec: §10.6 line 663 — watchdog-driven terminal also stamps env.
	// F-10.6.9. spec: §11.2 lines 87-88 — experiment/variant
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
		// spec: §14 line 106 — the watchdog-forced terminal event also
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

// elapsed reports whether row (currently in state st) has been in
// that state longer than the configured budget. The state-entry
// timestamp is approximated by the row's UpdatedAt — the store
// updates this on every state transition, so the first sweep after
// the transition correctly observes the elapsed time.
func (w *Watchdog) elapsed(updatedAt time.Time, st session.State, now time.Time) bool {
	budget := w.budgetFor(st)
	if budget == 0 {
		return false
	}
	return now.Sub(updatedAt) >= time.Duration(budget)*time.Second
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
