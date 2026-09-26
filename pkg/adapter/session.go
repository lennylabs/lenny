// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	"github.com/lennylabs/lenny/pkg/observability/tracing"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// setupOptionsFromProto converts the §5.1 setupPolicy message into the
// workspace.SetupOptions bounding the setup phase. A nil policy yields
// a near-zero SetupOptions — no aggregate cap, shell mode (legacy
// `/bin/sh -c`). on_timeout "warn" proceeds past the cap; any other
// value, including empty, is the conservative "fail" default. Env is
// seeded with the §7.5 minimal whitelist (DefaultSetupEnv) so
// a setup command does not see the adapter's process environment.
// Shell mirrors the §7.5 setupCommandPolicy.shell flag the
// gateway sets per runtime (true→`/bin/sh -c`, false→exec argv). spec:
// §7.5 — F-7.5.2 / F-7.5.8.
func setupOptionsFromProto(p *adapterv1.SetupPolicy, workdir string) workspace.SetupOptions {
	opts := workspace.SetupOptions{
		Env:   workspace.DefaultSetupEnv(workdir),
		Shell: true,
	}
	if p == nil {
		return opts
	}
	opts.AggregateTimeout = time.Duration(p.GetTimeoutSeconds()) * time.Second
	opts.FailOnAggregateTimeout = p.GetOnTimeout() != "warn"
	opts.Shell = p.GetShell()
	return opts
}

// RuntimeProcess manages the pod's runtime process. The §4.7 adapter
// starts it at session start, forwards message envelopes to it,
// signals it on interrupt, and closes it at session teardown.
type RuntimeProcess interface {
	// Start spawns the runtime process for the session.
	Start(ctx context.Context, sessionID string) error
	// WriteEnvelope forwards a pre-encoded message envelope to the
	// runtime's stdin.
	WriteEnvelope(sessionID string, envelope []byte) error
	// Output streams the runtime's output envelopes. Each value is one
	// §28.5.3 JSONL frame the runtime wrote to stdout. The channel is
	// closed when the runtime's output ends; the context bounds the
	// reader so a stalled consumer does not leak it.
	Output(ctx context.Context, sessionID string) (<-chan []byte, error)
	// Interrupt signals the runtime process. A hard interrupt sends
	// SIGKILL; a clean interrupt sends SIGTERM so the runtime can pause
	// or checkpoint within the gateway's grace deadline.
	Interrupt(ctx context.Context, sessionID string, hard bool) error
	// Close tears the runtime process down.
	Close(ctx context.Context, sessionID string) error
}

// StartSession assigns a session to the pod and starts the runtime
// (§4.7, §6.1). It is the final RPC of the §4.7 session assignment
// sequence: the workspace is already materialized by FinalizeWorkspace
// and setup is already run by RunSetup, so StartSession claims the pod,
// writes the §15.4 adapter manifest, and starts the runtime process. It
// rejects the call with Unavailable when the pod already holds a
// session. A session-mode pod is one-session-only: the pod is
// terminated and replaced after the session ends rather than reused.
//
// On any failure after the session is tentatively claimed, the pod is
// returned to the idle state so a retry can land on a fresh pod.
func (s *Server) StartSession(ctx context.Context, req *adapterv1.StartSessionRequest) (*adapterv1.StartSessionResponse, error) {
	// spec: §16.3 — `session.start` is emitted by the Pod. This
	// is the Go-side adapter emitter that closes the F-16.3.6 gap: the
	// process-global OTLP provider cmd/lenny-adapter installs via
	// tracing.InitProvider backs NewTracer(nil), so the span exports under
	// the gateway-propagated trace context (correlation fields auto-project).
	ctx, span := tracing.NewTracer(nil).Start(ctx, tracing.SpanSessionStart)
	var spanErr error
	defer func() {
		tracing.RecordError(span, spanErr)
		span.End()
	}()

	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		// §16.3: invalid input is the PERMANENT category (a retry with the
		// same request cannot succeed).
		spanErr = tracing.CategorizeError(
			status.Error(codes.InvalidArgument, "StartSession requires a session id"),
			tracing.CategoryPermanent,
		)
		return nil, status.Error(codes.InvalidArgument, "StartSession requires a session id")
	}
	if s.Runtime == nil {
		spanErr = tracing.CategorizeError(
			status.Error(codes.FailedPrecondition, "adapter is not configured with a runtime"),
			tracing.CategoryPermanent,
		)
		return nil, status.Error(codes.FailedPrecondition,
			"adapter is not configured with a runtime")
	}

	// spec: §5.2 — every session is bound to a slot on every pod, so the
	// start claims this session's slot whatever the pool's concurrency.
	// The claim also decides the once-per-pod intra-pod MCP start.
	// StartSession carries no bind attempt token (§4.7.1), so it resolves
	// with no identity assertion and creates an untokened entry when none
	// stands.
	claim, err := s.claimSessionSlot(sessionID, slotResolve{allowCreate: true}, s.isSDKWarm(), false)
	if err != nil {
		spanErr = err
		return nil, err
	}
	startMCP := claim.startMCP

	// §9.3: resolve the connectors this session's effective
	// delegation policy permits so the manifest can list one per-connector
	// MCP server and the adapter can open each socket. Best-effort: a
	// resolution failure leaves the session with no connector servers
	// rather than failing the start.
	connectors := s.sessionConnectors(ctx, sessionID)
	// §15.4: write the adapter manifest the runtime reads at startup.
	nonce, err := s.writeSessionManifest(manifestInputs{
		sessionID:          sessionID,
		experimentContext:  req.GetExperimentContext(),
		tracingContext:     req.GetTracingContext(),
		agentInterface:     req.GetAgentInterface(),
		minPlatformVersion: req.GetMinPlatformVersion(),
		connectors:         connectors,
	})
	if err != nil {
		s.releaseClaimedSlot(ctx, sessionID, claim)
		// §16.3: a manifest-write failure is TRANSIENT (a retry on a fresh
		// pod can succeed; under the §4.7 contract the adapter releases the
		// slot).
		spanErr = tracing.CategorizeError(err, tracing.CategoryTransient)
		return nil, status.Errorf(codes.Internal, "write adapter manifest: %v", err)
	}
	// §4.7: start the platform MCP server the runtime connects to. A
	// type: mcp runtime is "oblivious to Lenny" (§5.1) and never connects
	// to the platform MCP server, so the adapter does not start one for
	// it — the adapter drives the type: mcp agent's own MCP server as a
	// client instead. The servers bind pod-wide sockets, so only the claim
	// that took the once-per-pod start arms them.
	if s.RuntimeKind != RuntimeKindMCP && startMCP {
		if err := s.startPlatformMCP(nonce); err != nil {
			s.releaseClaimedSlot(ctx, sessionID, claim)
			spanErr = tracing.CategorizeError(err, tracing.CategoryTransient)
			return nil, status.Errorf(codes.Internal, "start platform MCP server: %v", err)
		}
		// §9.3: open one intra-pod MCP server per permitted
		// connector, forwarding tools/list and tools/call to the gateway.
		// Best-effort per connector. F-9.1.2.
		s.startConnectorMCPServers(sessionID, nonce, connectors)
	}
	if err := s.Runtime.Start(ctx, sessionID); err != nil {
		s.releaseClaimedSlot(ctx, sessionID, claim)
		// §16.3: a runtime-start crash is TRANSIENT (pod crash → retry on a
		// fresh pod).
		spanErr = tracing.CategorizeError(err, tracing.CategoryTransient)
		return nil, status.Errorf(codes.Internal, "start runtime: %v", err)
	}
	// spec: §4.7.1 rule 8 — the start confirms the registry still holds
	// the entry its claim was admitted against before the runtime is
	// recorded as holding the session.
	if !s.noteRuntimeStarted(sessionID, claim.attempt) {
		rollbackErr := s.rollbackUnconfirmedStart(ctx, sessionID)
		// §16.3: a lost race with a reclaim is the TRANSIENT category.
		spanErr = tracing.CategorizeError(rollbackErr, tracing.CategoryTransient)
		return nil, rollbackErr
	}
	return &adapterv1.StartSessionResponse{}, nil
}

// SendMessage delivers a content message to the pod's runtime (§4.7).
// The request carries a §28.5.3 message envelope already encoded by
// the gateway. The adapter stamps the session's address onto it and
// writes it to the shared runtime's stdin, through the same helper the
// Attach leg uses, because §28.5.3 makes the population of the
// per-session identifier an adapter-side obligation on every
// session-scoped frame on every pod. The runtime's response is surfaced
// asynchronously, so SendMessage returns once the envelope is delivered.
// spec: §28.5.3; §5.2.
func (s *Server) SendMessage(_ context.Context, req *adapterv1.SendMessageRequest) (*adapterv1.SendMessageResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "SendMessage requires a session id")
	}
	if len(req.GetEnvelopeJson()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "SendMessage requires a message envelope")
	}
	// spec: §5.2 — the message is delivered to the session the request
	// names, whose slot the registry holds on every pod.
	if err := s.checkSessionBound(sessionID); err != nil {
		return nil, err
	}
	rt := s.runtimeForSession(sessionID)
	if rt == nil {
		return nil, status.Errorf(codes.FailedPrecondition,
			"session %s has no running runtime", sessionID)
	}
	if err := s.writeSessionEnvelope(rt, sessionID, req.GetEnvelopeJson()); err != nil {
		return nil, status.Errorf(codes.Internal, "deliver message to runtime: %v", err)
	}
	return &adapterv1.SendMessageResponse{}, nil
}

// Shutdown answers a teardown request for one slot. It performs two
// teardowns under two preconditions, as the §4.7 Shutdown row states: it
// releases the slot and its per-slot tree for any registry entry the call
// removes, and it tears the runtime down only for a session that started.
// releaseSessionSlot, with its claim-fenced form releaseClaimedSlot, is the
// shipped statement of the unstarted branch's semantics, so the gateway's
// compensation and the adapter's own start rollbacks read as one rule.
//
// Rule 10 (the teardown-pairing rule) is the outermost branch. It is
// decided on the request's fields alone, which is why it sits above s.mu:
// a malformed request never reaches the registry and performs nothing,
// the whole-pod scrub included.
//
// Rules 11 through 15 are the comparison shutdownReclaimOutcome decides.
// Its arms map onto the rules in the cascade's order: no entry is rule 11
// and answers ABSENT; the unconditional form is rule 12 and answers
// RECLAIMED; an entry carrying no token and an entry carrying another
// token are rule 13's two arms and answer SUPERSEDED, which tells the
// gateway the entry belongs to another attempt and nothing was removed; a
// matching token is rule 14 and answers RECLAIMED. Rule 15 is the
// slot_reclaim field every answer carries. The removing arm's per-slot
// guard, reclaim hold and cleanup-outcome report follow §5.2's reclaim-hold
// paragraph and disposition table; the inline comments give the reasons.
//
// The runtime teardown must not run for an unstarted session. For a
// bound-but-unstarted entry the handler this replaced sent the §15.4.2
// drain whenever no other bound entry remained, which a
// registered-but-unbound co-tenant about to call StartSession does not
// hold off, and filed a ReportSessionScrub that advanced sessionsServed
// for a session the pod never ran. Runtime.Close is also not uniformly
// session-scoped: InProcessRuntime.Close and MCPRuntime.Close ignore the
// session identifier and tear the runtime down on any call, and
// SocketRuntimeProcess.Close tears down the shared connection, the child
// and the never-rebound listener whenever its active set is empty, which
// Interrupt of the last active session produces without clearing the
// connection.
//
// A non-positive deadline_ms leaves Runtime.Close on the inbound context;
// a positive one bounds it, so the runtime's SIGTERM/SIGKILL pivot honors
// the §11.4 graceful window.
//
// spec: §4.7; §4.7.1 (role and gateway RPC contract), rules 10 through 15;
// §5.2 (slot-identifier reclaim hold); §11.4; §15.4.2.
func (s *Server) Shutdown(ctx context.Context, req *adapterv1.ShutdownRequest) (*adapterv1.ShutdownResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "Shutdown requires a session id")
	}
	// spec: §4.7.1 (role and gateway RPC contract), rule 10 (the
	// teardown-pairing rule). Decided on the request's fields alone, so it
	// sits above s.mu and a malformed request never reaches the registry.
	attempt := req.GetBindAttempt()
	unconditional := req.GetUnconditionalTeardown()
	if (attempt == "") == !unconditional {
		return nil, status.Errorf(codes.InvalidArgument,
			"shutdown for session %s must carry exactly one of bind_attempt and unconditional_teardown",
			sessionID)
	}

	// The first decision runs under s.mu alone, with no slot guard taken.
	// An arm that removes nothing touches no path, and the per-slot guard
	// brackets sections that run as long as a materialization, a setup
	// command or a checkpoint restore. A reclaim answering ABSENT or
	// SUPERSEDED from behind one of those would spend its whole budget
	// waiting to report that it removed nothing, and the gateway would
	// record an RPC error and a leaked slot instead.
	s.mu.Lock()
	cur, ok := s.slotStateLocked(sessionID)
	outcome, remove, untokened := shutdownReclaimOutcome(cur, ok, unconditional, attempt)
	s.mu.Unlock()
	if !remove {
		return s.answerShutdown(req, outcome, true, untokened)
	}

	// The removing arm takes the guard through the raw form, which no
	// reclaim hold refuses and which gives up inside the request's own
	// context rather than blocking past it. An expired acquisition proceeds
	// unguarded and counts as a cleanup that did not complete (§5.2). The
	// unlock is deferred ahead of the hold release below, so the guard
	// outlives the hold: a FinalizeWorkspace, RunSetup or Resume admitted
	// before this reclaim opened its hold is excluded by the guard, which
	// the hold cannot reach once a section is past its resolve.
	unlockSlot, guarded := s.lockSlotGuard(ctx, sessionID)
	defer unlockSlot()
	if !guarded {
		warnSlotGuardNotAcquired(sessionID, "Shutdown")
	}

	// The second decision is required rather than defensive: s.mu was not
	// held while the guard was acquired, so the entry can have been removed
	// or replaced, and deregistering on the first decision would act on a
	// comparison the registry no longer supports. This decision is the one
	// the deregistration is atomic with, because deregisterSlotLocked
	// deletes unconditionally once it finds an entry.
	s.mu.Lock()
	cur, ok = s.slotStateLocked(sessionID)
	outcome, remove, untokened = shutdownReclaimOutcome(cur, ok, unconditional, attempt)
	if !remove {
		s.mu.Unlock()
		return s.answerShutdown(req, outcome, true, untokened)
	}
	st, _, boundRemains, release := s.reclaimSlotLocked(sessionID)
	r := reclaimedSlot{
		st: st,
		// st.started is set inside claimSessionSlotUnderLock before
		// Runtime.Start runs, so gating the runtime teardown on it fails
		// closed on a start still in flight, which is torn down rather than
		// skipped.
		started:      st.started,
		live:         s.runtimeHoldsLocked(sessionID),
		boundRemains: boundRemains,
		guarded:      guarded,
	}
	// spec: §5.2 (slot-identifier reclaim hold). The deregistration and the
	// hold are one critical section, so no bind is admitted between them.
	// The release is deferred rather than written at each return so that a
	// panic out of Runtime.Close or the tree removal is a cleanup that did
	// not complete rather than a silent release, and it is taken only when
	// the cleanup completed, because §5.2 keeps the identifier held for the
	// life of the pod when it does not.
	completed := false
	defer func() {
		if completed {
			release()
		}
	}()
	s.mu.Unlock()

	exitedCleanly, done := s.tearDownReclaimedSlot(ctx, req, r)
	completed = done
	return s.answerShutdown(req, outcome, exitedCleanly, false)
}

// shutdownReclaimOutcome decides what a Shutdown does with the entry it
// resolved, as a pure function of the request and the entry. It is the
// whole of §4.7.1's teardown comparison, and the handler evaluates it twice
// on the removing path: once under s.mu as the fast path that answers the
// outcomes removing nothing, and once under s.mu after the slot guard is
// held, which is the evaluation the deregistration is atomic with. Two calls
// of one function cannot diverge, where the same comparison written out
// twice can.
//
// It returns the outcome, whether the entry is removed, and whether the
// fail-closed arm answered, an entry carrying no attempt token at all, so
// the caller counts it on the arm that answers and a call counts it at most
// once.
//
// spec: §4.7.1 (role and gateway RPC contract), rules 11 through 14
func shutdownReclaimOutcome(cur *slotState, ok, unconditional bool, attempt string) (adapterv1.SlotReclaimOutcome, bool, bool) {
	switch {
	case !ok:
		// Rule 11, the no-entry rule.
		return adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_ABSENT, false, false
	case unconditional:
		// Rule 12, the unconditional-teardown rule.
		return adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED, true, false
	case cur.bindAttempt == "":
		// Rule 13, the entry carries no token.
		return adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_SUPERSEDED, false, true
	case cur.bindAttempt != attempt:
		// Rule 13, the entry carries another attempt's token.
		return adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_SUPERSEDED, false, false
	default:
		// Rule 14, the attempt-match rule.
		return adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED, true, false
	}
}

// reclaimedSlot is what the removing arm of Shutdown captured inside the
// critical section that deregistered the entry. The predicates are taken
// once, under s.mu, and never re-read afterwards.
type reclaimedSlot struct {
	// st is the deregistered entry.
	st *slotState
	// started gates the runtime teardown: the merged claim ran for this
	// session, whether or not Runtime.Start has returned.
	started bool
	// live gates the cleanup-outcome report: the shared runtime process
	// holds the session, which noteRuntimeStarted records only after
	// Runtime.Start returned, so the slot reached §6.2's running.
	live bool
	// boundRemains reports that another bound entry survives the
	// deregistration, which withholds the pod-global §15.4.2 drain.
	boundRemains bool
	// guarded reports that the section holds the slot's guard.
	guarded bool
}

// tearDownReclaimedSlot runs the two teardowns for an entry Shutdown's
// removing arm deregistered, with s.mu released. It returns the response's
// exited_cleanly and whether the cleanup completed, which is the predicate
// that ends the §5.2 reclaim hold.
//
// started over-approximates toward closing and live under-approximates
// toward not counting, so every interleaving of a reclaim with a start
// files zero or one cleanup-outcome report for the session: a reclaim while
// the start is in flight closes the runtime and reports nothing, and a
// reclaim after noteRuntimeStarted recorded the session reports exactly
// once. The gateway advances the pod's served-session count on every report
// with no per-session dedup, so the one-report rule has to hold here.
//
// spec: §4.7.1 (role and gateway RPC contract), rules 12 and 14; §5.2 (pool
// configuration and execution modes); §15.4.2.
func (s *Server) tearDownReclaimedSlot(ctx context.Context, req *adapterv1.ShutdownRequest, r reclaimedSlot) (exitedCleanly, completed bool) {
	sessionID := req.GetSessionId().GetValue()
	closeErr := error(nil)
	if r.started {
		// §4.7: flush a final usage report onto the gateway control stream
		// so the gateway can run budget_return.lua (§8.3) with the
		// session's complete token totals.
		s.emitFinalUsage(ctx, sessionID)
		// spec: §15.4.2 / §15.4.3. The drain signal is pod-global and names
		// no session, so it goes out only when the deregistration left no
		// bound entry; a bound co-tenant is still being served. It precedes
		// the close because the last session's close tears the shared
		// runtime down and a terminate frame sent afterwards reaches a dead
		// runtime.
		if !r.boundRemains {
			s.drainViaLifecycle(req.GetDeadlineMs(), req.GetReason())
		}
		if s.Runtime != nil {
			closeCtx, cancel := contextWithGraceDeadline(ctx, time.Duration(req.GetDeadlineMs())*time.Millisecond)
			closeErr = s.Runtime.Close(closeCtx, sessionID)
			cancel()
		}
		s.noteRuntimeClosed(sessionID)
	}

	// The slot release runs for any entry the call removed, bound or not,
	// which reclaims the tree and the empty credential directory the
	// workspace-preparation RPCs created for a registered-but-unbound
	// entry. It follows the drain and the close so the agent process is not
	// reading a credential file the teardown already removed inside the
	// §15.4.2 grace window. The armed §4.9 expiry timers were cancelled by
	// deregisterSlotLocked. cancelPodMCPIfRuntimeIdle is safe here for an
	// unbound entry: it cancels nothing while the shared runtime process
	// serves a session or the arming session still holds a slot.
	treeErr := s.removeSlotTreeVia(r.st)
	s.cancelPodMCPIfRuntimeIdle()
	if treeErr != nil {
		slog.Warn("slot_tree_removal_failed", "slot_id", sessionID, "error", treeErr)
	}

	// The cleanup-outcome report is §6.2 occupancy accounting and stays
	// keyed on closeErr alone. On the running arm the gateway frees the
	// slot's occupancy on a clean exit, so a leaked report keyed on the
	// tree removal as well would book a leak against occupancy the same
	// answer frees; that residue is reclaimed at the occupancy-zero
	// whole-pod scrub instead.
	if r.live {
		s.reportSessionScrub(ctx, sessionID, closeErr)
	}
	// exited_cleanly implements the §5.2 table's clean-exit column. A slot
	// that reached running answers on the runtime close alone; one that did
	// not also carries the tree removal and the guard, because the gateway
	// keys its leaked disposition on this answer.
	exitedCleanly = closeErr == nil && (r.live || (r.guarded && treeErr == nil))
	completed = r.guarded && closeErr == nil && treeErr == nil
	return exitedCleanly, completed
}

// answerShutdown is the handler's only exit after the two-field
// precondition, so every outcome is built in one place. It counts the
// fail-closed arm, runs clause three and builds the response.
//
// Clause three, the whole-pod recycle scrub, runs on every outcome, because
// the two gateway callers that carry the recycle disposition reach
// different arms: Binder.ReleaseSlot sends it after a separate
// unconditional Shutdown already tore the last slot down, so its request
// answers ABSENT, and Binder.Release's recycle branch sends it as the
// session's only teardown, so its request answers RECLAIMED on the removing
// arm. An arm that returned before the scrub would hand the pod to the next
// tenant unscrubbed with no ReportPodScrub for the gateway's armed
// missing-report timeout to receive. On the removing arm Runtime.Close and
// the tree removal have both returned before the scrub goroutine starts,
// which is the order §5.2 states for the whole-pod boundary. The response
// does not wait for the scrub; the gateway bounds it with that timeout.
//
// spec: §4.7.1 (role and gateway RPC contract), rules 13 and 15; §5.2
// recycle lifecycle; §4.7 Shutdown recycle disposition; §16.1.
func (s *Server) answerShutdown(req *adapterv1.ShutdownRequest, outcome adapterv1.SlotReclaimOutcome, exitedCleanly, untokened bool) (*adapterv1.ShutdownResponse, error) {
	if untokened {
		incSlotShutdownUntokenedEntry()
	}
	if rc := req.GetRecycle(); rc != nil {
		s.startPodScrub(rc)
	}
	return &adapterv1.ShutdownResponse{
		ExitedCleanly: exitedCleanly,
		SlotReclaim:   outcome,
	}, nil
}

// drainViaLifecycle sends the §15.4.2 DRAINING-state graceful-shutdown
// signal on the CH-RUNTIMEOPS before the hard runtime close. It is a
// no-op when the runtime has no CH-RUNTIMEOPS (Basic/Standard level)
// or has not yet connected; any other send error is logged rather than
// surfaced so a drain hiccup never blocks termination.
func (s *Server) drainViaLifecycle(deadlineMs int32, reason string) {
	if s.Lifecycle == nil {
		return
	}
	if err := s.Lifecycle.Terminate(deadlineMs, drainReason(reason)); err != nil &&
		!errors.Is(err, errLifecycleNotConnected) && !errors.Is(err, errLifecycleClosed) {
		log.Printf("lenny-adapter: lifecycle drain signal: %v", err)
	}
}

// drainReason maps a §4.7 ShutdownRequest reason to the lifecycle
// `terminate` frame's reason enum (session_complete, budget_exhausted,
// eviction, operator), defaulting an empty or unrecognized value to
// session_complete so the wire frame always carries a valid reason.
// spec: §15.4.2 — terminate reason enum.
func drainReason(reason string) string {
	switch reason {
	case "session_complete", "budget_exhausted", "eviction", "operator":
		return reason
	default:
		return "session_complete"
	}
}

// contextWithGraceDeadline derives a context bounded by `grace` from
// `parent`, returning a no-op cancel when `grace` is non-positive. The
// adapter's RuntimeProcess.Close implementations read the derived
// context's deadline to size their SIGTERM/SIGKILL pivot. spec: §11.4.
func contextWithGraceDeadline(parent context.Context, grace time.Duration) (context.Context, context.CancelFunc) {
	if grace <= 0 {
		return parent, func() {}
	}
	return context.WithTimeout(parent, grace)
}

// emitFinalUsage reads the session's accumulated usage and pushes a
// FINAL_USAGE_REPORT control event. It is best-effort: a nil usage meter
// or a read error leaves the gateway to fall back to stream-close, which
// the §8.3 contract already tolerates.
func (s *Server) emitFinalUsage(ctx context.Context, sessionID string) {
	if s.Usage == nil {
		return
	}
	u, err := s.Usage.Usage(ctx, sessionID)
	if err != nil {
		return
	}
	s.EmitFinalUsageReport(sessionID, u)
}
