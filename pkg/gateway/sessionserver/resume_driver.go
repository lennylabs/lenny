// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordination"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/usercreds"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// This file is the §7.3 resume driver: the only code that writes
// resume_pending → resuming. The watchdog's sweepResumePending hands it every
// resume_pending row inside the row's window, and tree recovery hands it the
// resume_pending nodes of a tree. On the replica that holds the session's
// coordination lease it claims a replacement pod, writes the resuming
// incarnation from the binder's post-claim hook, restores the session, and
// commits it to running.

// resumeExit is how one resume attempt ended, for the
// lenny_session_resume_attempts_total counter. The zero value records nothing.
type resumeExit int

const (
	// resumeExitUncounted is an attempt that wrote no resuming incarnation,
	// or whose entry write it did not see take effect.
	resumeExitUncounted resumeExit = iota
	// resumeExitReported is a failure after the entry write, reported
	// through the failure funnel.
	resumeExitReported
	// resumeExitCommitLanded is a commit to running that landed, at or
	// above the attempt's generation, including one that later
	// relinquished.
	resumeExitCommitLanded
	// resumeExitCommitNotLanded is a commit that did not land.
	resumeExitCommitNotLanded
	// resumeExitAbandoned is an attempt the server-lifetime context ended
	// after its entry write, before its outcome was known.
	resumeExitAbandoned
)

// recordResumeAttempt records lenny_session_resume_attempts_total for one
// attempt: success for a commit that landed, failure for a reported failure
// or a commit that did not land, and nothing for any other exit. It is the
// only caller of incSessionResumeAttempt, and each attempt calls it once,
// deferred, so the counter counts each resuming incarnation whose entry
// write the attempt saw take effect at most once.
//
// spec: §16.1 (Metrics), §7.3 (Resume driver)
func (s *Server) recordResumeAttempt(pool string, exit resumeExit) {
	if s.incSessionResumeAttempt == nil {
		return
	}
	switch exit {
	case resumeExitCommitLanded:
		s.incSessionResumeAttempt(pool, "success")
	case resumeExitReported, resumeExitCommitNotLanded:
		s.incSessionResumeAttempt(pool, "failure")
	}
}

// DriveResume is the watchdog's ResumeDriver hook. For a resume_pending row,
// it starts one asynchronous restore attempt on the server-lifetime context
// and returns at once. It writes nothing and starts nothing when this replica
// does not hold the session's coordination lease (or the lease read fails),
// or when an attempt for the session is already in flight on this replica.
// The attempt reads the row from the primary and starts no restore when the
// row has left resume_pending or its active age exceeds the effective
// maxSessionAge cap.
//
// The attempt context is derived from the server-lifetime context, and its
// cancel function is registered in the attempt record before the goroutine
// starts, so a terminal write's CancelResumeAttempt reaches the attempt from
// its first read. The attempt removes its own entry when it returns. With no
// pod binder wired, the attempt commits the row to running with no claim.
//
// spec: §7.3 (Resume driver)
func (s *Server) DriveResume(ctx context.Context, row sessionstore.Session) {
	if !s.coordinatesSession(ctx, row.TenantID, row.ID) {
		return
	}
	actx, cancel := context.WithCancel(s.lifetime())
	if !s.resumeAttempts.begin(row.ID, cancel) {
		cancel()
		return
	}
	go func() {
		defer s.resumeAttempts.end(row.ID)
		defer cancel()
		if s.podBinder == nil {
			s.binderlessResume(actx, row)
			return
		}
		s.attemptResume(actx, cancel, row)
	}()
}

// CancelResumeAttempt cancels the context of the resume attempt in flight on
// this replica for the session, so its pre-commit restoration RPCs stop. It
// does nothing when no attempt is registered, reads no store, is idempotent,
// and is safe to call from inside the attempt. The post-terminal hook calls
// it after every terminal write that runs the hook, and the full_revoke
// write calls it through AfterRevokeWrite.
//
// spec: §7.2 (Interactive Session Model), §7.3 (Resume driver)
func (s *Server) CancelResumeAttempt(sessionID string) {
	s.resumeAttempts.cancel(sessionID)
}

// attemptResume runs one restore attempt. It reads the row from the primary,
// bounds the attempt by the window left before the entry write, acquires the
// coordination lease, releases any earlier binding, and restores the session
// through the binder with the entry-write hook. A failure before the hook
// writes and records nothing; a hook error stops with nothing written; a
// failure after the hook is reported once at the attempt's generation G; a
// restore that returned without error is committed. A claimless restore, a
// service-mode session, runs the hook before it returns, so it takes the same
// exits as a restore that claimed a pod.
//
// spec: §7.3 (Resume driver; Retry and Resume)
func (s *Server) attemptResume(ctx context.Context, cancel context.CancelFunc, listed sessionstore.Session) {
	row, ok := s.readResumable(ctx, listed)
	if !ok {
		return
	}
	exit := resumeExitUncounted
	defer func() { s.recordResumeAttempt(row.PoolRef, exit) }()

	bound := newAttemptBound(s.afterFuncOrDefault(), cancel)
	defer bound.stop()
	bound.reset(s.windowLeft(row))

	if err := s.acquireCoordinationLease(ctx, row.TenantID, row.ID); err != nil {
		slog.Info("sessionserver: resume attempt could not acquire the coordination lease",
			"session_id", row.ID, "error", err)
		return
	}
	entry := s.newEntryHook(row, bound)
	gen := row.CoordinationGeneration + 1
	out, err := s.releaseThenResumeOnPod(ctx, row, resumeBind{onClaimed: entry.run, generation: gen})
	switch {
	case err == nil:
		exit = s.completeResume(row, gen, out)
	case !entry.ran():
		// A pre-claim failure writes nothing and records nothing; the next
		// watchdog tick attempts again until the window ends the retries.
		slog.Info("sessionserver: resume attempt failed before its claim",
			"session_id", row.ID, "error", err)
	case entry.failed():
		// The hook's entry write was superseded or failed; the binder has
		// already released the claim.
		slog.Info("sessionserver: resume attempt entry write did not apply",
			"session_id", row.ID, "error", entry.err())
	default:
		exit = s.reportResumeFailure(row, gen, err)
	}
}

// readResumable reads the row from the primary and reports whether the
// attempt may restore it: the row is resume_pending and its active age is at
// or under the effective cap. The received row comes from a read-pool listing
// and can show an earlier incarnation, so neither check uses it.
func (s *Server) readResumable(ctx context.Context, listed sessionstore.Session) (sessionstore.Session, bool) {
	row, err := s.readPrimary(ctx, listed.TenantID, listed.ID)
	if err != nil {
		slog.Warn("sessionserver: resume attempt could not read the session row from the primary",
			"session_id", listed.ID, "error", err)
		return sessionstore.Session{}, false
	}
	if row.State != session.StateResumePending {
		return sessionstore.Session{}, false
	}
	if sessionstore.ActiveAge(row, s.clock()) > s.effectiveAgeCap(ctx, row) {
		// spec: §6.2 (maxSessionAge timer behavior across states) — an
		// over-cap row is not driven into running.
		slog.Info("sessionserver: resume attempt refused for a session over its maxSessionAge cap",
			"session_id", row.ID)
		return sessionstore.Session{}, false
	}
	return row, true
}

// effectiveAgeCap resolves the row's maxSessionAge cap through the resolver
// the gateway shares with the watchdog, or the platform default.
func (s *Server) effectiveAgeCap(ctx context.Context, row sessionstore.Session) time.Duration {
	if s.ageCap != nil {
		return s.ageCap(ctx, row)
	}
	return watchdog.EffectiveAgeCap(ctx, nil, time.Duration(watchdog.DefaultMaxSessionAgeSeconds)*time.Second, row)
}

// windowLeft is the time left in the row's resume_pending window, measured
// from its StateEnteredAt as sweepResumePending measures it.
// spec: §6.2 (Pod State Machine); §7.3 (Resume driver).
func (s *Server) windowLeft(row sessionstore.Session) time.Duration {
	window := watchdog.EffectiveMaxResumeWindow(row, s.resumePendingCap())
	return row.StateEnteredAt.Add(window).Sub(s.clock())
}

func (s *Server) resumePendingCap() time.Duration {
	if s.maxResumePending > 0 {
		return s.maxResumePending
	}
	return time.Duration(watchdog.DefaultMaxResumePendingSeconds) * time.Second
}

func (s *Server) resumingCap() time.Duration {
	if s.maxResuming > 0 {
		return s.maxResuming
	}
	return time.Duration(watchdog.DefaultMaxResumingSeconds) * time.Second
}

func (s *Server) afterFuncOrDefault() func(time.Duration, func()) func() bool {
	if s.afterFunc != nil {
		return s.afterFunc
	}
	return func(d time.Duration, f func()) func() bool { return time.AfterFunc(d, f).Stop }
}

// attemptBound cancels the attempt context when the attempt's bound passes.
// Before the entry write the bound is the window left; after it, the resuming
// watchdog's MaxResumingSeconds. Correctness does not depend on it, because
// every exit is conditioned on the attempt's generation.
type attemptBound struct {
	mu       sync.Mutex
	after    func(time.Duration, func()) func() bool
	cancel   context.CancelFunc
	stopFunc func() bool
}

func newAttemptBound(after func(time.Duration, func()) func() bool, cancel context.CancelFunc) *attemptBound {
	return &attemptBound{after: after, cancel: cancel}
}

// reset replaces the bound with d from now. A d at or below zero cancels the
// attempt at once.
func (b *attemptBound) reset(d time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopFunc != nil {
		b.stopFunc()
		b.stopFunc = nil
	}
	if d <= 0 {
		b.cancel()
		return
	}
	b.stopFunc = b.after(d, b.cancel)
}

// stop disarms the bound.
func (b *attemptBound) stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopFunc != nil {
		b.stopFunc()
		b.stopFunc = nil
	}
}

// errResumeSuperseded is the entry write's not-applied sentinel: the locked
// row is no longer resume_pending at the generation the attempt read.
var errResumeSuperseded = errors.New("sessionserver: resume entry superseded on the locked row")

// entryHook is the binder's post-claim hook for one attempt: one store Update
// that writes resume_pending → resuming and increments
// coordination_generation, so the state resuming and the new value name one
// incarnation of the restore. A second call in the same attempt, such as a
// slot retry, is a no-op that returns nil.
type entryHook struct {
	s     *Server
	row   sessionstore.Session
	bound *attemptBound

	mu      sync.Mutex
	called  bool
	written bool
	lastErr error
}

func (s *Server) newEntryHook(row sessionstore.Session, bound *attemptBound) *entryHook {
	return &entryHook{s: s, row: row, bound: bound}
}

// run is the hook the binder calls after its claim and before any RPC reaches
// the pod. spec: §7.3 (Resume driver)
func (h *entryHook) run(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.written {
		return nil
	}
	h.called = true
	expected := h.row.CoordinationGeneration
	_, err := h.s.store.Update(ctx, h.row.TenantID, h.row.ID, func(r *sessionstore.Session) error {
		if r.State != session.StateResumePending || r.CoordinationGeneration != expected {
			return errResumeSuperseded
		}
		r.State = session.StateResuming
		r.CoordinationGeneration++
		return nil
	})
	if err != nil {
		h.lastErr = err
		return err
	}
	h.written = true
	// spec: §7.3 (Resume driver) — after the entry write the attempt is
	// bounded by the resuming watchdog's budget.
	h.bound.reset(h.s.resumingCap())
	return nil
}

func (h *entryHook) ran() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.called
}

func (h *entryHook) failed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastErr != nil && !h.written
}

func (h *entryHook) err() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastErr
}

// reportResumeFailure reports a failure after the entry write once, through
// the failure funnel, at the attempt's generation G, on the server-lifetime
// context so neither the attempt bound nor CancelResumeAttempt cuts it short.
// When the server-lifetime context has ended, no report is sent.
//
// spec: §7.3 (Resume driver; Credential leases on the replacement pod), §6.2
// (Pod State Machine)
func (s *Server) reportResumeFailure(row sessionstore.Session, gen int64, cause error) resumeExit {
	ctx := s.lifetime()
	if ctx.Err() != nil {
		return resumeExitAbandoned
	}
	reason := resumeFailureReason(cause)
	disp, err := s.ReportSessionFailure(ctx, FailureReport{
		TenantID:                       row.TenantID,
		SessionID:                      row.ID,
		Reason:                         reason,
		ExpectedCoordinationGeneration: gen,
	})
	if err != nil {
		slog.Warn("sessionserver: report resume failure", "session_id", row.ID, "reason", reason, "error", err)
	} else {
		slog.Info("sessionserver: reported resume failure", "session_id", row.ID, "reason", reason,
			"from", string(disp.From), "to", string(disp.To), "cause", cause)
	}
	return resumeExitReported
}

// resumeFailureReason classifies a failure after the entry write: a
// setup-command FailedPrecondition is setup_command_failed, a credential
// rejection that recurs on every attempt is credential_rejected, and every
// other cause is runtime_crash.
func resumeFailureReason(err error) string {
	var setup *podsession.SetupCommandFailure
	if errors.As(err, &setup) && status.Code(setup.Cause) == codes.FailedPrecondition {
		return string(session.FailureSetupCommandFailed)
	}
	if isCredentialRejection(err) {
		return string(session.FailureCredentialRejected)
	}
	return string(session.FailureRuntimeCrash)
}

// isCredentialRejection reports whether a restore's credential assignment was
// refused for a reason that recurs on every attempt: the chain carries a
// *podsession.CredentialAssignmentError and names a missing pool, a missing
// user credential, an unconfigured LLM proxy, or a gRPC status of
// InvalidArgument, PermissionDenied, FailedPrecondition, or Unauthenticated.
// An exhausted pool, an unavailable Token Service, a context error, and any
// other cause are not rejections.
//
// spec: §7.3 (Resume driver; Credential leases on the replacement pod)
func isCredentialRejection(err error) bool {
	var assign *podsession.CredentialAssignmentError
	if !errors.As(err, &assign) {
		return false
	}
	if errors.Is(err, credassign.ErrPoolNotFound) ||
		errors.Is(err, credentialstore.ErrNotFound) ||
		errors.Is(err, usercreds.ErrProxyUnavailable) {
		return true
	}
	var withStatus interface{ GRPCStatus() *status.Status }
	if !errors.As(err, &withStatus) {
		return false
	}
	switch withStatus.GRPCStatus().Code() {
	case codes.InvalidArgument, codes.PermissionDenied, codes.FailedPrecondition, codes.Unauthenticated:
		return true
	default:
		return false
	}
}

// binderlessResume is the resume attempt with no pod binder wired, the
// single-gateway Compose deployment: it commits the row from resume_pending
// to running with no claim, as completeResume's binderless path.
func (s *Server) binderlessResume(ctx context.Context, listed sessionstore.Session) {
	if _, ok := s.readResumable(ctx, listed); !ok {
		return
	}
	// The baseline is the received row's RecoveryGeneration, and the
	// binderless commit writes no resuming incarnation, so it records no
	// resume-attempts count.
	s.completeResume(listed, 0, resumeOutcome{})
}

// commitObservation is what the commit's repeat loop saw: whether its last
// Update returned nil, whether any try returned a store error, whether any
// try returned ErrNotFound, and R, the row the last nil Update returned or
// the last row the mutator copied.
type commitObservation struct {
	landed   bool
	storeErr bool
	notFound bool
	row      sessionstore.Session
}

// commitBaseline is what the attempt knew before its commit: its
// coordination generation, the replacement pod's sandbox, and the
// RecoveryGeneration of the row it restored. binderless selects the
// classification the binderless path applies, which has no sandbox to match.
type commitBaseline struct {
	gen         int64
	sandbox     string
	recoveryGen int64
	binderless  bool
}

// commitOutcome names which exit a resume commit took: the commit did not
// land, it landed at the attempt's generation, or it landed and an adopter's
// handoff has since advanced the row past that generation. The exit decides
// what completeResume does after the commit.
type commitOutcome int

const (
	commitNotLanded commitOutcome = iota
	commitLanded
	commitAboveG
)

// classifyCommit classifies a commit's observation against its baseline.
// With a pod binder, only this attempt's commit writes its sandbox at G and
// advances RecoveryGeneration. A guard miss after a store error therefore
// counts as landed when the observed row names the sandbox at the attempt's
// generation with an advanced recovery generation and a held pod or a
// terminal state, as landed-then-adopted when an adopter's handoff advanced
// that row past the attempt's generation, and as not landed otherwise. A
// guard miss with no earlier store error did not land: a concurrent pool's
// PodAssignment names the
// shared pod, so only the error history separates this attempt's write from
// a later incarnation's. On the binderless path, an advanced recovery
// generation means a commit landed since the received row.
//
// spec: §7.3 (Resume driver)
func classifyCommit(o commitObservation, b commitBaseline) commitOutcome {
	switch {
	case o.landed:
		return commitLanded
	case o.notFound, !o.storeErr:
		return commitNotLanded
	case b.binderless:
		if o.row.RecoveryGeneration > b.recoveryGen {
			return commitLanded
		}
		return commitNotLanded
	}
	r := o.row
	ours := r.PodAssignment == b.sandbox && r.RecoveryGeneration > b.recoveryGen && podHeldOrTerminal(r)
	switch {
	case ours && r.CoordinationGeneration == b.gen:
		return commitLanded
	case ours && r.CoordinationGeneration > b.gen:
		return commitAboveG
	default:
		return commitNotLanded
	}
}

// errResumeCommitMissed is the commit mutator's not-applied sentinel.
var errResumeCommitMissed = errors.New("sessionserver: resume commit missed on the locked row")

// commitResume runs the commit's guarded Update under the guarded-write
// repeat rule: it repeats the same Update until it returns nil, the
// sentinel, or ErrNotFound, or the context ends. It reports false when the
// context ended before the outcome was known.
func (s *Server) commitResume(ctx context.Context, row sessionstore.Session, b commitBaseline) (commitObservation, bool) {
	var obs commitObservation
	backoff := 50 * time.Millisecond
	// Captured once, so every try of the repeat writes the same inputs.
	now := s.clock()
	for {
		var seen sessionstore.Session
		updated, err := s.store.Update(ctx, row.TenantID, row.ID, func(r *sessionstore.Session) error {
			seen = *r
			if !commitGuard(*r, b) {
				return errResumeCommitMissed
			}
			transitionResume(r)
			// spec: §6.2 (maxClientIdleSeconds clock behavior across
			// states) — the idle clock starts fresh on entry to running.
			r.LastAgentActivityAt = now
			r.RecoveryGeneration++
			if !b.binderless && b.sandbox != "" {
				r.PodAssignment = b.sandbox
			}
			return nil
		})
		switch {
		case err == nil:
			obs.landed, obs.row = true, updated
			return obs, true
		case errors.Is(err, errResumeCommitMissed):
			obs.row = seen
			return obs, true
		case errors.Is(err, sessionstore.ErrNotFound):
			obs.notFound, obs.row = true, seen
			return obs, true
		}
		obs.storeErr = true
		obs.row = seen
		if ctx.Err() != nil {
			return obs, false
		}
		slog.Warn("sessionserver: resume commit store error; repeating", "session_id", row.ID, "error", err)
		select {
		case <-ctx.Done():
			return obs, false
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 2*time.Second)
	}
}

// commitGuard is the commit's locked-row guard: resuming at G with a pod
// binder, resume_pending on the binderless path.
func commitGuard(r sessionstore.Session, b commitBaseline) bool {
	if b.binderless {
		return r.State == session.StateResumePending
	}
	return r.State == session.StateResuming && r.CoordinationGeneration == b.gen
}

// completeResume commits a restore that reached its replacement pod and then
// finishes the exit the commit took: landed at the attempt's generation,
// landed and adopted past it, or not landed. It runs on the
// server-lifetime context, so the attempt bound does not reach it, and holds
// the session's delivery lock from before its commit until its tail ends.
//
// spec: §7.2 (Interactive Session Model), §7.3 (Resume driver), §8.10
// (Delegation Tree Recovery)
func (s *Server) completeResume(row sessionstore.Session, gen int64, out resumeOutcome) resumeExit {
	ctx := s.lifetime()
	unlock, err := s.deliveryLocks.Lock(ctx, row.ID)
	if err != nil {
		return resumeExitAbandoned
	}
	defer unlock()
	b := commitBaseline{gen: gen, recoveryGen: row.RecoveryGeneration, binderless: s.podBinder == nil}
	if out.result != nil {
		b.sandbox = out.result.SandboxName
	}
	obs, ok := s.commitResume(ctx, row, b)
	if !ok {
		// Context ended: the adopter of a landed row fences, publishes, and
		// delivers; otherwise sweepResuming and the claim-orphan GC reclaim.
		return resumeExitAbandoned
	}
	t := resumeTail{s: s, ctx: ctx, row: row, gen: gen, out: out, r: obs.row}
	switch classifyCommit(obs, b) {
	case commitLanded:
		t.landed()
		return resumeExitCommitLanded
	case commitAboveG:
		t.adopted(false)
		return resumeExitCommitLanded
	default:
		t.notLanded(obs.storeErr)
		return resumeExitCommitNotLanded
	}
}

// resumeTail carries what an attempt needs to finish after its commit. Each
// method finishes one exit, running its steps in the order the spec gives.
//
// spec: §7.3 (Resume driver)
type resumeTail struct {
	s   *Server
	ctx context.Context
	row sessionstore.Session
	gen int64
	out resumeOutcome
	r   sessionstore.Session
}

// landed finishes a commit that landed at the attempt's generation. It fences
// the pod at that generation, and a relinquished fence hands the pod to the
// adopter through adopted. It then publishes the binding and re-checks the row
// for a terminal state; a terminal row hands the binding to the terminal
// binding release and stops there. Otherwise it emits the resume events, runs
// the partial-manifest cleanup, recovers the delegation tree, and delivers the
// held messages.
//
// spec: §7.2 (Interactive Session Model), §7.3 (Resume driver), §8.10
// (Delegation Tree Recovery)
func (t resumeTail) landed() {
	res := t.out.result
	if res != nil && !t.s.binderless() {
		stampBindingGeneration(res, t.gen)
		if ferr := t.s.fenceResumedPod(t.ctx, res.Adapter, t.row.TenantID, t.row.ID, t.gen); ferr != nil {
			slog.Warn("sessionserver: resumed pod fence relinquished; the adopter owns the pod",
				"session_id", t.row.ID, "error", ferr)
			t.adopted(true)
			return
		}
		t.publish(res)
		if fresh, err := t.s.readPrimary(t.ctx, t.row.TenantID, t.row.ID); err != nil {
			slog.Warn("sessionserver: resume terminal re-check read failed", "session_id", t.row.ID, "error", err)
		} else if session.IsTerminal(fresh.State) {
			// spec: §7.2 — a terminal write landed after the commit; the
			// terminal binding release reclaims the published binding.
			t.s.ReleaseTerminalBinding(fresh)
			return
		}
	}
	t.events()
	if t.r.State == session.StateRunning {
		if err := t.s.deliverBuffered(t.ctx, t.r); err != nil {
			slog.Warn("sessionserver: deliver held messages after resume", "session_id", t.row.ID, "error", err)
		}
	}
}

// publish places the committed binding: the rebuild branch through
// publishBinding, which makes no second lease acquire, and the checkpoint
// branch into the pod registry.
func (t resumeTail) publish(res *podsession.BindResult) {
	if t.out.rebuild {
		t.s.publishBinding(t.ctx, res)
		return
	}
	if t.s.podRegistry != nil {
		t.s.podRegistry.Put(res)
	}
}

// adopted finishes a commit whose pod an adopter owns: either the fence at the
// attempt's generation was relinquished (relinquished true) or an adopter's
// handoff advanced the row past that generation (false). It publishes
// nothing and closes the attempt's adapter connection. When the row is not
// terminal it emits the resume events, runs the partial-manifest cleanup, and
// recovers the delegation tree; a terminal row skips those. The adopter
// delivers the held messages.
//
// spec: §7.3 (Resume driver), §8.10 (Delegation Tree Recovery)
func (t resumeTail) adopted(relinquished bool) {
	terminal := false
	if fresh, err := t.s.readPrimary(t.ctx, t.row.TenantID, t.row.ID); err != nil {
		slog.Warn("sessionserver: resume terminal re-check read failed", "session_id", t.row.ID, "error", err)
	} else {
		terminal = session.IsTerminal(fresh.State)
	}
	if res := t.out.result; res != nil && res.Adapter != nil {
		res.Adapter.Close()
	}
	slog.Info("sessionserver: resume commit landed; adopter owns the pod",
		"session_id", t.row.ID, "relinquished", relinquished, "terminal", terminal)
	if terminal {
		return
	}
	t.events()
}

// notLanded finishes a commit that did not land: the attempt releases only
// the uncommitted pod and the leases it minted, and publishes and writes
// nothing. After a store error it releases only on a confirmed reclaim.
//
// spec: §7.3 (Resume driver)
func (t resumeTail) notLanded(storeErr bool) {
	if t.s.binderless() || t.out.result == nil {
		return
	}
	if err := t.s.podBinder.ReleaseUncommitted(t.ctx, t.out.result, storeErr); err != nil {
		slog.Warn("sessionserver: release uncommitted restore", "session_id", t.row.ID, "error", err)
	}
}

// events emits the resume audit event, the status change, and the resumed
// event, runs the partial-manifest cleanup, emits the children-reattached
// event, and recovers the delegation tree, in that order. The resume mode is
// classified before the partial-manifest cleanup soft-deletes the manifest
// the classification reads.
func (t resumeTail) events() {
	s, r := t.s, t.r
	if s.lifecycleAudit != nil {
		s.lifecycleAudit.EmitSessionLifecycle(t.ctx, SessionLifecycleEvent{
			EventType:  auditSessionResumed,
			TenantID:   r.TenantID,
			SessionID:  r.ID,
			UserID:     r.UserID,
			RuntimeRef: r.RuntimeRef,
			State:      string(r.State),
			At:         s.clock(),
		})
	}
	if r.State == session.StateRunning {
		s.emitStatusChange(r.TenantID, r.ID, r.State)
	}
	mode := s.classifyResumeWithAdapter(t.ctx, r, t.out.mode)
	s.emitResumedEvent(t.ctx, r, mode)
	if s.partialManifestCleaner != nil {
		// spec: §10.1.7 (Cleanup.) — scope the cleanup by the committed
		// resume row's generation, so a live seal or checkpoint intent row
		// written by this incarnation survives.
		if cerr := s.partialManifestCleaner.CleanupAfterResume(t.ctx, r.TenantID, r.ID, r.CoordinationGeneration); cerr != nil {
			slog.Warn("sessionserver: partial-manifest cleanup after resume", "session_id", r.ID, "error", cerr)
		}
	}
	s.emitChildrenReattached(t.ctx, r.TenantID, r.ID)
	s.recoverDelegationTree(t.ctx, r.TenantID, s.treeRoot(t.ctx, r))
}

// binderless reports whether the server runs without a pod binder.
func (s *Server) binderless() bool { return s.podBinder == nil }

// DeliverHeldAfterTakeover is the coordination Sweeper's held-delivery hook.
// After a takeover publishes the session's binding, it returns at once and
// delivers the session's held messages on one goroutine on the
// server-lifetime context: it takes the delivery lock, reads the row from the
// primary, and delivers only when the row is running at generation, the
// takeover's compare-and-swap generation. A read or delivery error is logged.
// It takes no slot-accounting lock.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6), §10.1.2
// (Coordinator Handoff Protocol)
func (s *Server) DeliverHeldAfterTakeover(tenantID, sessionID string, generation int64) {
	ctx := s.lifetime()
	go func() {
		unlock, err := s.deliveryLocks.Lock(ctx, sessionID)
		if err != nil {
			return
		}
		defer unlock()
		row, err := s.readPrimary(ctx, tenantID, sessionID)
		if err != nil {
			slog.Warn("sessionserver: takeover delivery could not read the session row",
				"session_id", sessionID, "error", err)
			return
		}
		if row.State != session.StateRunning || row.CoordinationGeneration != generation {
			return
		}
		if err := s.deliverBuffered(ctx, row); err != nil {
			slog.Warn("sessionserver: takeover delivery of held messages", "session_id", sessionID, "error", err)
		}
	}()
}

// podHeldOrTerminal reports whether R holds a pod (running, input_required,
// or suspended with a pod) or is terminal.
func podHeldOrTerminal(r sessionstore.Session) bool {
	return session.IsTerminal(r.State) || coordination.PodHeld(r)
}
