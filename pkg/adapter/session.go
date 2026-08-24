// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"log"
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
	_, startMCP, err := s.claimSessionSlot(sessionID, s.isSDKWarm(), false)
	if err != nil {
		spanErr = err
		return nil, err
	}

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
		s.releaseSessionSlot(sessionID)
		// §16.3: a manifest-write failure is TRANSIENT (a retry on a fresh
		// pod can succeed; the §4.7 contract returns the pod to idle).
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
			s.releaseSessionSlot(sessionID)
			spanErr = tracing.CategorizeError(err, tracing.CategoryTransient)
			return nil, status.Errorf(codes.Internal, "start platform MCP server: %v", err)
		}
		// §9.3: open one intra-pod MCP server per permitted
		// connector, forwarding tools/list and tools/call to the gateway.
		// Best-effort per connector. F-9.1.2.
		s.startConnectorMCPServers(sessionID, nonce, connectors)
	}
	if err := s.Runtime.Start(ctx, sessionID); err != nil {
		s.releaseSessionSlot(sessionID)
		// §16.3: a runtime-start crash is TRANSIENT (pod crash → retry on a
		// fresh pod).
		spanErr = tracing.CategorizeError(err, tracing.CategoryTransient)
		return nil, status.Errorf(codes.Internal, "start runtime: %v", err)
	}
	s.noteRuntimeStarted(sessionID)
	return &adapterv1.StartSessionResponse{}, nil
}

// SendMessage delivers a content message to the pod's runtime (§4.7).
// The request carries a §28.5.3 message envelope already encoded by
// the gateway; the adapter writes it verbatim to the runtime's stdin.
// The runtime's response is surfaced asynchronously, so SendMessage
// returns once the envelope is delivered.
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
	if err := rt.WriteEnvelope(sessionID, req.GetEnvelopeJson()); err != nil {
		return nil, status.Errorf(codes.Internal, "deliver message to runtime: %v", err)
	}
	return &adapterv1.SendMessageResponse{}, nil
}

// Shutdown tears the named session down and, on the occupancy-zero
// recycle boundary, runs the §5.2 whole-pod scrub. It is where a
// session's whole teardown lands, on every pod: the final usage flush,
// the §15.4.2 drain signal, the runtime close, the per-slot tree removal,
// and the cleanup-outcome report are one sequence whatever the pool's
// concurrency.
//
// The handler is three ordered clauses over one message. It refuses an
// empty session_id. It then runs the locked cancel-deregister step for
// the named session and runs the teardown when that step removed a bound
// entry, skipping it otherwise. It then runs the whole-pod scrub when the
// request carries the recycle disposition.
//
// Clause two is conditional rather than guarded, which is what makes the
// handler idempotent: the §11.4 full revoke and the concurrent
// occupancy-zero edge each send a second request for a session already
// released, and returning an error there would make every revoked session
// hold its slot for the life of the pod.
//
// The §4.7 ShutdownRequest carries `deadline_ms` — the §11.4 step-3
// graceful window the gateway pinned at full_revoke (10s by default).
// Close runs under a context bounded by that deadline so the runtime
// adapter's SIGTERM/SIGKILL pivot honors the spec window instead of an
// internal default. A non-positive `deadline_ms` falls through to the
// inbound RPC context.
//
// spec: §4.7; §5.2; §11.4; §15.4.2.
func (s *Server) Shutdown(ctx context.Context, req *adapterv1.ShutdownRequest) (*adapterv1.ShutdownResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "Shutdown requires a session id")
	}

	// Clause two. The deregistration and the drain decision are one
	// critical section: an occupancy read taken before the deregistration
	// would let two co-tenants ending at once each observe the other and
	// send no drain at all.
	s.mu.Lock()
	st, removed, boundRemains := s.deregisterSlotLocked(sessionID)
	bound := removed && st.sessionID != ""
	s.mu.Unlock()

	// spec: §5.2 — the recycle disposition says the gateway is holding
	// this pod for its next session: the adapter closes the ending
	// session's runtime, keeps the pod's process alive across the recycle
	// boundary, and the pod "keeps the process alive and reuses it for the
	// next session". The pod-global drain signal and the shared runtime
	// transport are the pod's own end rather than the ending session's, so
	// neither is torn down on this request. Tearing them down leaves a
	// recycled pod that cannot serve anything: the §4.7 sidecar runtime
	// runs in a container the pod never restarts, so its process exits on
	// the closed transport and the next session's start has nothing to
	// talk to.
	recycling := req.GetRecycle() != nil

	closeErr := error(nil)
	if bound {
		// §4.7: flush a final usage report onto the gateway control stream
		// before the stream closes, so the gateway can run
		// budget_return.lua (§8.3) with the session's complete token
		// totals. It reads the usage meter alone, so it is indifferent to
		// the entry being gone by the time it runs.
		s.emitFinalUsage(ctx, sessionID)
		// spec: §15.4.2 / §15.4.3 — a Full-level runtime drains through
		// CH-RUNTIMEOPS (the DRAINING state) before the hard runtime
		// close. The signal is pod-global and names no session, so it goes
		// out only when the deregistration left the registry holding no
		// bound entry; sending it while a co-tenant is still bound would
		// signal the shared runtime to terminate while it is still serving
		// that session. It precedes the close because the last session's
		// close tears the shared runtime down and a terminate frame sent
		// afterwards reaches a dead runtime.
		if !boundRemains && !recycling {
			s.drainViaLifecycle(req.GetDeadlineMs(), req.GetReason())
		}
		if s.Runtime != nil {
			closeCtx, cancel := contextWithGraceDeadline(ctx, time.Duration(req.GetDeadlineMs())*time.Millisecond)
			closeErr = s.endRuntimeUse(closeCtx, sessionID, recycling)
			cancel()
		}
		s.noteRuntimeClosed(sessionID)
		// The second release step. It follows the drain and the close so
		// the agent process is not reading a credential file the teardown
		// has already removed inside the §15.4.2 grace window.
		_ = removeSlotTree(st)
		s.cancelPodMCPIfRuntimeIdle()
		// spec: §5.2 (per-session cleanup outcome), §4.7
		// (ReportSessionScrub). Report the cleanup outcome so the gateway
		// advances sessions_served (feeding the maxSessionsPerPod
		// retirement) and, on a leaked outcome, feeds the
		// unhealthy-threshold ledger. A clean close is `released`; a close
		// failure or grace-deadline overrun is `leaked`.
		s.reportSessionScrub(ctx, sessionID, closeErr)
	}

	// Clause three. The gateway populates recycle only at occupancy zero,
	// and on that call clause two has already deregistered the ending
	// session and removed its tree ahead of the scrub. The Shutdown
	// response does not wait for the scrub; the gateway bounds it with the
	// missing-report timeout it armed before sending this request.
	// spec: §5.2 recycle lifecycle; §4.7 Shutdown recycle disposition.
	if rc := req.GetRecycle(); rc != nil {
		s.startPodScrub(rc)
	}
	return &adapterv1.ShutdownResponse{ExitedCleanly: closeErr == nil}, nil
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

// endRuntimeUse ends sessionID's use of the pod's runtime. Off the
// recycle boundary it is the runtime's Close, which tears the shared
// transport down once the last session releases it. On the recycle
// boundary the pod is kept for its next session, so a runtime that can
// separate the two (the §4.7 sidecar transport, which is bound per pod
// rather than per session) releases the session and leaves its transport
// up. A runtime whose transport ends with its last session falls back to
// Close. spec: §5.2 (recycle lifecycle); §4.7.
func (s *Server) endRuntimeUse(ctx context.Context, sessionID string, recycling bool) error {
	if recycling {
		if r, ok := s.Runtime.(sessionReleaser); ok {
			return r.ReleaseSession(ctx, sessionID)
		}
	}
	return s.Runtime.Close(ctx, sessionID)
}

// sessionReleaser is the optional half of RuntimeProcess a runtime
// implements when its transport outlives the sessions carried on it: the
// pod's next session reuses that transport after the §5.2 recycle
// boundary. spec: §5.2.
type sessionReleaser interface {
	// ReleaseSession ends one session's use of the runtime and leaves the
	// pod's transport up for the next session.
	ReleaseSession(ctx context.Context, sessionID string) error
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
