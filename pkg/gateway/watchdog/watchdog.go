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

	// DefaultTickInterval is the §11.3 line 224 default sweep
	// cadence: 5s.
	DefaultTickInterval = 5 * time.Second
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
	TickInterval                   time.Duration
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

// Watchdog drives the periodic sweep.
type Watchdog struct {
	store    sessionstore.Store
	tenants  TenantLister
	cfg      Config
	clock    func() time.Time
	billing  billingstore.Store
	archive  treearchive.Store
	terminal TerminalHook
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

// Result captures the outcome of a sweep.
type Result struct {
	// ForcedFailures is the count of sessions transitioned to `failed`
	// by this sweep.
	ForcedFailures int
	// Expirations is the count of sessions transitioned to `expired`
	// by the §11.3 maxSessionAge and maxAwaitingClientAction sweeps.
	Expirations int
	// PerReason records the count per FailureReason for observability.
	PerReason map[string]int
}

// Tick runs a single sweep against every tenant returned by the
// configured TenantLister at the supplied now. Returns the count of
// sessions transitioned to `failed` and the per-reason breakdown.
//
// Tick is idempotent: a second sweep with the same `now` does not
// re-transition rows that the first sweep already moved to `failed`.
func (w *Watchdog) Tick(ctx context.Context, now time.Time) (Result, error) {
	res := Result{PerReason: map[string]int{}}
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
		if err := w.sweepMaxAge(ctx, tenant, now, &res); err != nil {
			return res, err
		}
		if err := w.sweepAwaitingClientAction(ctx, tenant, now, &res); err != nil {
			return res, err
		}
	}
	return res, nil
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
			w.recordCompleted(ctx, updated)
		}
	}
	return nil
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
		if now.Sub(row.CreatedAt) <= effectiveMaxSessionAge(row, platformCap) {
			continue
		}
		updated, err := w.store.Update(ctx, tenant, row.ID, func(r *sessionstore.Session) error {
			if session.IsTerminal(r.State) {
				// Concurrent transition — leave the new state alone.
				return nil
			}
			r.State = session.StateExpired
			return nil
		})
		if err != nil {
			return err
		}
		if updated.State == session.StateExpired {
			res.Expirations++
			w.recordCompleted(ctx, updated)
		}
	}
	return nil
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
	_, _ = w.billing.Append(ctx, billingstore.Event{
		TenantID:  sess.TenantID,
		UserID:    sess.UserID,
		SessionID: sess.ID,
		EventType: billingstore.EventSessionCompleted,
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
