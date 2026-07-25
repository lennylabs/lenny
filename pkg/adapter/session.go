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
// seeded with the §7.5 line 479 minimal whitelist (DefaultSetupEnv) so
// a setup command does not see the adapter's process environment.
// Shell mirrors the §7.5 line 490 setupCommandPolicy.shell flag the
// gateway sets per runtime (true→`/bin/sh -c`, false→exec argv). spec:
// §7.5 line 490 — F-7.5.2 / F-7.5.8.
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
	// §15.4.1 JSONL frame the runtime wrote to stdout. The channel is
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
	// spec: §16.3 line 341 — `session.start` is emitted by the Pod. This
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
	// spec: §6.4 lines 385-405 — a slot-qualified StartSession claims one
	// of the pod's concurrent-workspace slots (its own per-slot tree and
	// runtime) rather than the whole pod. The single-session base path
	// (maxConcurrentSessions == 1, no slot id) below is taken unchanged.
	if slotID := req.GetSlotId().GetValue(); s.useSlot(slotID) {
		resp, err := s.startSessionSlot(ctx, req, slotID)
		spanErr = err
		return resp, err
	}
	if s.Runtime == nil {
		spanErr = tracing.CategorizeError(
			status.Error(codes.FailedPrecondition, "adapter is not configured with a runtime"),
			tracing.CategoryPermanent,
		)
		return nil, status.Error(codes.FailedPrecondition,
			"adapter is not configured with a runtime")
	}

	if err := s.claimSession(sessionID); err != nil {
		spanErr = err
		return nil, err
	}

	// §9.3 line 142: resolve the connectors this session's effective
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
		s.releaseSession()
		// §16.3: a manifest-write failure is TRANSIENT (a retry on a fresh
		// pod can succeed; the §4.7 contract returns the pod to idle).
		spanErr = tracing.CategorizeError(err, tracing.CategoryTransient)
		return nil, status.Errorf(codes.Internal, "write adapter manifest: %v", err)
	}
	// §4.7: start the platform MCP server the runtime connects to. A
	// type: mcp runtime is "oblivious to Lenny" (§5.1) and never connects
	// to the platform MCP server, so the adapter does not start one for
	// it — the adapter drives the type: mcp agent's own MCP server as a
	// client instead.
	if s.RuntimeKind != RuntimeKindMCP {
		if err := s.startPlatformMCP(nonce); err != nil {
			s.releaseSession()
			spanErr = tracing.CategorizeError(err, tracing.CategoryTransient)
			return nil, status.Errorf(codes.Internal, "start platform MCP server: %v", err)
		}
		// §9.3 lines 142-164: open one intra-pod MCP server per permitted
		// connector, forwarding tools/list and tools/call to the gateway.
		// Best-effort per connector. F-9.1.2.
		s.startConnectorMCPServers(sessionID, nonce, connectors)
	}
	if err := s.Runtime.Start(ctx, sessionID); err != nil {
		s.releaseSession()
		// §16.3: a runtime-start crash is TRANSIENT (pod crash → retry on a
		// fresh pod).
		spanErr = tracing.CategorizeError(err, tracing.CategoryTransient)
		return nil, status.Errorf(codes.Internal, "start runtime: %v", err)
	}
	return &adapterv1.StartSessionResponse{}, nil
}

// SendMessage delivers a content message to the pod's runtime (§4.7).
// The request carries a §15.4.1 message envelope already encoded by
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
	// spec: §6.4 lines 401-405 — a slot-qualified message is delivered to
	// the slot's own runtime so the dispatch lands on the slot's cwd.
	if slotID := req.GetSlotId().GetValue(); s.useSlot(slotID) {
		if err := s.checkSlotSession(sessionID, slotID); err != nil {
			return nil, err
		}
		rt := s.runtimeForSlot(slotID)
		if rt == nil {
			return nil, status.Errorf(codes.FailedPrecondition, "slot %s has no running runtime", slotID)
		}
		if err := rt.WriteEnvelope(sessionID, req.GetEnvelopeJson()); err != nil {
			return nil, status.Errorf(codes.Internal, "deliver message to slot runtime: %v", err)
		}
		return &adapterv1.SendMessageResponse{}, nil
	}
	if err := s.checkSession(sessionID); err != nil {
		return nil, err
	}
	if err := s.Runtime.WriteEnvelope(sessionID, req.GetEnvelopeJson()); err != nil {
		return nil, status.Errorf(codes.Internal, "deliver message to runtime: %v", err)
	}
	return &adapterv1.SendMessageResponse{}, nil
}

// Shutdown terminates the pod's runtime and releases the session
// (§4.7). It closes the runtime process and returns the pod toward
// termination; a session-mode pod is replaced rather than reused.
//
// The §4.7 ShutdownRequest carries `deadline_ms` — the §11.4 step-3
// graceful window the gateway pinned at full_revoke (10s by default).
// Close runs under a context bounded by that deadline so the runtime
// adapter's SIGTERM/SIGKILL pivot honors the spec window instead of an
// internal default. A non-positive `deadline_ms` falls through to the
// inbound RPC context, preserving the previous behavior. spec: §11.4
// line 258; §4.7 ShutdownRequest.deadline_ms.
func (s *Server) Shutdown(ctx context.Context, req *adapterv1.ShutdownRequest) (*adapterv1.ShutdownResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "Shutdown requires a session id")
	}
	// spec: §6.4 lines 401-405 — a slot-qualified Shutdown tears down only
	// the named slot (its runtime + per-slot tree), leaving sibling slots
	// on the pod running.
	if slotID := req.GetSlotId().GetValue(); s.useSlot(slotID) {
		return s.shutdownSlot(ctx, sessionID, slotID, req.GetDeadlineMs())
	}
	// spec: §5.2 (whole-pod scrub trigger, uniform across session modes); §4.7
	// (Shutdown recycle disposition). A concurrent-session pod reaches occupancy
	// zero when its last slot drains cleanly: the gateway sends a slot-less
	// recycle Shutdown carrying the last-released slot's session id (so the
	// non-empty session_id guard above admits it) and the RecycleScrub
	// disposition, but never sets the pod-global s.sessionID (a concurrent pod
	// sets only the per-slot st.sessionID, so checkSession below can never pass
	// for it). Dispatch the whole-pod scrub on the empty pod-global session id:
	// run startPodScrub and return, replacing the gateway missing-report timeout
	// with a deliberate scrub-and-report. A base-mode session (which sets
	// s.sessionID via claimSession) does not match this branch and takes the
	// checkSession path unchanged, where its own recycle handling runs below.
	// Ordered before checkSession because a concurrent pod fails that gate.
	// F-5.2.31.
	if rc := req.GetRecycle(); rc != nil && s.currentSession() == "" {
		s.startPodScrub(rc)
		return &adapterv1.ShutdownResponse{}, nil
	}
	if err := s.checkSession(sessionID); err != nil {
		return nil, err
	}
	// §4.7 lines 661-662: flush a final usage report onto the gateway
	// control stream before the stream closes, so the gateway can run
	// budget_return.lua (§8.3) with the session's complete token totals.
	s.emitFinalUsage(ctx, sessionID)
	// spec: §15.4.2 / §15.4.3 — a Full-level runtime drains through the
	// lifecycle channel (the DRAINING state) before the hard runtime
	// close: the adapter sends `terminate` so the runtime finishes the
	// current exchange and exits within the gateway's grace window rather
	// than only observing the stdin/socket EOF. Basic/Standard runtimes
	// have no lifecycle channel (Lifecycle == nil) and a not-yet-connected
	// runtime is a no-op; the drain is best-effort and never fails the
	// shutdown.
	s.drainViaLifecycle(req.GetDeadlineMs(), req.GetReason())
	closeCtx, cancel := contextWithGraceDeadline(ctx, time.Duration(req.GetDeadlineMs())*time.Millisecond)
	closeErr := s.Runtime.Close(closeCtx, sessionID)
	cancel()
	s.releaseSession()
	// spec: §5.2 recycle lifecycle; §4.7 Shutdown recycle disposition. On the
	// occupancy-zero recycle boundary the pod process stays alive: after the
	// ending session's runtime is closed, run the whole-pod scrub and report
	// its binary outcome asynchronously via ReportPodScrub. The Shutdown
	// response does not wait for the scrub; the gateway bounds it with the
	// missing-report timeout it armed before sending this Shutdown. On the
	// terminate path (recycle unset) the pod is replaced and no scrub runs.
	if rc := req.GetRecycle(); rc != nil {
		// spec: §5.2 (ReportSessionScrub, maxSessionsPerPod both modes). A base
		// (maxConcurrentSessions == 1) recycling pod has no slot, so it advances
		// sessions_served on its own recycle boundary: emit ReportSessionScrub
		// with an empty slot id for the ending session before the whole-pod
		// scrub, so advanceScrubCounters reads back a non-zero count and the
		// maxSessionsPerPod retirement becomes functional in base mode too.
		// The outcome derives from the same closeErr that set ExitedCleanly.
		// This runs only on the recycle boundary; the terminate path replaces
		// the pod and needs no session-scrub report. F-5.2.31.
		s.reportSessionScrub(ctx, sessionID, "", closeErr)
		s.startPodScrub(rc)
	}
	return &adapterv1.ShutdownResponse{ExitedCleanly: closeErr == nil}, nil
}

// drainViaLifecycle sends the §15.4.2 DRAINING-state graceful-shutdown
// signal on the lifecycle channel before the hard runtime close. It is a
// no-op when the runtime has no lifecycle channel (Basic/Standard level)
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
// context's deadline to size their SIGTERM/SIGKILL pivot. spec: §11.4
// line 258.
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
	s.EmitFinalUsageReport(u)
}

// boundSession identifies one live session bound to this pod for the
// eviction drive. slotID is empty on the single-session base pod
// (maxConcurrentSessions == 1) and carries the §6.4 slot id on a
// concurrent-workspace pod, matching the AdapterEvicting field contract.
type boundSession struct {
	sessionID string
	slotID    string
}

// liveBoundSessions enumerates the sessions currently bound to this pod so
// the kubelet-path SIGTERM handler can emit one AdapterEvicting per session
// and wait for each session's coordinator-driven eviction checkpoint. A
// base-mode pod records its one session in the pod-global sessionID with no
// slot; a concurrent-workspace pod records each session on its own slot and
// leaves the pod-global sessionID empty, so a slot entry carries the slot
// id. The two are mutually exclusive in practice; enumerating both is
// defensive. An idle pod returns nil, so the handler emits nothing and
// tears down as before. spec: §4.6.1 (agent-pod disruption protection),
// §4.7 (AdapterEvicting per session), §6.4 (per-slot sessions).
func (s *Server) liveBoundSessions() []boundSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []boundSession
	if s.sessionID != "" {
		out = append(out, boundSession{sessionID: s.sessionID})
	}
	for slotID, st := range s.slots {
		if st.sessionID != "" {
			out = append(out, boundSession{sessionID: st.sessionID, slotID: slotID})
		}
	}
	return out
}

// setEvicting records that this pod's own SIGTERM/eviction handler has
// engaged. The kubelet-path handler calls it before emitting
// AdapterEvicting so a concurrent Checkpoint RPC can take the best-effort
// eviction snapshot when the runtime lifecycle connection has already
// dropped. spec: §4.6.1 (agent-pod disruption protection).
func (s *Server) setEvicting() {
	s.evicting.Store(true)
}

// isEvicting reports whether this pod is itself terminating under the
// kubelet SIGTERM. The Checkpoint RPC reads it as the sole gate for the
// best-effort eviction snapshot; a still-running pod (including one driven
// through the §10.1 gateway-drain barrier, which also carries
// TriggerEviction) leaves it false and keeps the cooperative handshake
// fail-closed. spec: §4.4 (best-effort eviction snapshot), §4.6.1.
func (s *Server) isEvicting() bool {
	return s.evicting.Load()
}

// checkSession confirms sessionID is the session currently assigned to
// the pod.
func (s *Server) checkSession(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessionID == "" {
		return status.Error(codes.FailedPrecondition, "pod has no assigned session")
	}
	if s.sessionID != sessionID {
		return status.Errorf(codes.NotFound, "session %s is not assigned to this pod", sessionID)
	}
	return nil
}

// claimSession marks the pod as holding sessionID, returning a gRPC
// Unavailable error when the pod is not idle. The §4.7 StartSession
// contract specifies Unavailable for a pod that is not idle.
func (s *Server) claimSession(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessionID != "" {
		return status.Errorf(codes.Unavailable,
			"pod is not idle: session %s is already assigned", s.sessionID)
	}
	s.sessionID = sessionID
	return nil
}

// releaseSession returns the pod to the idle state and stops the
// session's platform MCP server, if one was started.
//
// credSessionID is INTENTIONALLY left set: the §6.1 lines 5/16/24
// invariant ("After a session completes or fails in `executionMode:
// session`, the pod is terminated and replaced — never recycled for a
// different session") is primarily enforced by the gateway-side
// teardown loop (binder.Release → drain → terminated → replaced); the
// sticky credSessionID is the adapter-side defense-in-depth that
// rejects an AssignCredentials for a different session if a pod
// somehow survives termination. Clearing it here would weaken that
// backstop. F-6.1.12.
func (s *Server) releaseSession() {
	s.mu.Lock()
	s.sessionID = ""
	cancel := s.mcpCancel
	s.mcpCancel = nil
	// spec: §5.1 — the next session's runtime must reconnect to the
	// platform MCP server to be observed at Standard; clear the prior
	// session's signal. F-5.1.11.
	s.mcpHandshakeSeen = false
	// §9.3: stop every per-connector MCP server started for the session so
	// the connector sockets are released alongside the platform socket.
	// F-9.1.2.
	connectorCancels := s.connectorCancels
	s.connectorCancels = nil
	// §4.9 line 1149: drop the direct-mode expiry timers so a stale lease
	// cannot fire AUTH_EXPIRED against a session that has already ended.
	s.cancelAllExpiryTimers()
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	for _, c := range connectorCancels {
		c()
	}
}
