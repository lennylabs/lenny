// SPDX-License-Identifier: MIT

package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"

	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/observability/tracing"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// Attach opens the §4.7 bidirectional content stream for a session. The
// first AttachRequest binds the stream to the pod's session; from
// then on the gateway streams client-to-agent envelopes, which the
// adapter writes to the runtime's stdin, and the adapter streams the
// runtime's output envelopes back. The stream ends when the runtime's
// output closes, the gateway half-closes the client direction, or
// either side errors.
func (s *Server) Attach(stream grpc.BidiStreamingServer[adapterv1.AttachRequest, adapterv1.AttachResponse]) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	sessionID := first.GetSessionId().GetValue()
	if sessionID == "" {
		return status.Error(codes.InvalidArgument, "Attach requires a session id on the first message")
	}
	// spec: §5.2 — the stream binds to the session it names, whose slot
	// the registry holds on every pod, and its cwd is that slot's tree.
	slotID := sessionID
	if err := s.checkSessionBound(sessionID); err != nil {
		return err
	}
	rt := s.runtimeForSession(sessionID)
	if rt == nil {
		return status.Errorf(codes.FailedPrecondition,
			"session %s has no running runtime", sessionID)
	}
	wsRoot, err := s.workspaceRootForSession(sessionID)
	if err != nil {
		return err
	}
	if hung := s.entryHeartbeatHung(sessionID); hung != nil {
		select {
		case <-hung:
			return status.Error(codes.DeadlineExceeded, "runtime missed heartbeat ack deadline; session stream ended (§28.5.3)")
		default:
		}
	}
	if env := first.GetEnvelopeJson(); len(env) > 0 {
		if err := s.writeSessionEnvelope(rt, sessionID, env); err != nil {
			return envelopeWriteStatus("deliver message to runtime", err)
		}
	}

	ctx := stream.Context()
	rawOut, err := rt.Output(ctx, sessionID)
	if err != nil {
		return status.Errorf(codes.Internal, "open runtime output: %v", err)
	}
	// spec: §28.5.3 — the single pod-global runtime serves
	// every session over one connection, so its output stream interleaves
	// frames for every session, each tagged with the session it addresses.
	// Demultiplex on that address so this Attach stream sees only its own
	// session's frames, resolving a session-scoped frame that carries none
	// against the pod's slot count.
	out := demuxSessionOutput(ctx, rawOut, sessionID, s.slotCount)

	// spec: §28.5.3 — the session's heartbeat monitor runs from its
	// session_start, independent of this stream. When the runtime misses
	// the ack deadline the monitor closes hung and escalates once, and this
	// stream, and every later stream for the entry, ends. hbHung is nil when
	// the entry has no monitor.
	hbHung := s.entryHeartbeatHung(sessionID)
	// spec: §4.7.10 (Runtime supervisor, Crash) — a crash of the runtime
	// process that holds the session ends the stream with runtime_crash.
	crashed := s.entryRuntimeCrash(sessionID)

	// The receive loop forwards client envelopes to the runtime's
	// stdin; it runs concurrently with the send loop below. recvErr is
	// buffered so the loop's final send never blocks once the send
	// loop has stopped selecting on it.
	recvErr := make(chan error, 1)
	go func() {
		recvErr <- s.attachRecvLoop(stream, sessionID, rt)
	}()

	for {
		select {
		case line, ok := <-out:
			if !ok {
				// The runtime's output ended. A crash is handled before the
				// output closes, so a crashed session reports runtime_crash
				// and any other end is the session's clean end.
				if isClosed(crashed) {
					return errRuntimeCrash
				}
				return nil
			}
			// spec: §28.5.3 — heartbeat_ack is protocol-level
			// with no content payload; it answers the adapter's liveness
			// probe, which the activity tap feeds, and is never relayed to
			// the gateway, whether or not the entry has a monitor.
			if jsonlFrameType(line) == heartbeatAckFrameType {
				continue
			}
			// spec: §28.5.3 (CH-MSGSOCK, Outbound: session_started), rule
			// 5 — session_started acknowledges the adapter's own
			// session_start. The adapter consumes it and relays it to no
			// Attach stream, whether or not a start is waiting for it, so
			// a runtime that acknowledges every start reaches no client.
			if jsonlFrameType(line) == sessionStartedFrameType {
				continue
			}
			// spec: §28.5.3 — set_tracing_context is an outbound
			// protocol frame the adapter consumes (it registers the
			// tracing identifiers with the gateway for delegation
			// propagation) and never relays as content. Available at all
			// tiers, so even a Basic runtime with no MCP access reaches the
			// same gateway registration the lenny/set_tracing_context MCP
			// tool performs. The demultiplexer above has already resolved
			// the frame to this stream: one addressing another session is
			// withheld, and one carrying no address on a pod holding more
			// than one slot is rejected and counted before this branch is
			// reached. The handler applies §28.5.3's live-binding
			// confirmation alone, dropping a frame whose registry entry no
			// longer names this session on
			// lenny_adapter_set_tracing_context_dropped_total.
			if jsonlFrameType(line) == "set_tracing_context" {
				s.handleSetTracingContext(ctx, sessionID, slotID, line)
				continue
			}
			// §28.5.3: an adapter-local tool_call is answered by the
			// adapter itself and never relayed to the gateway. A relayed
			// platform/connector tool_call is traced gateway-side as
			// mcp.external_tool_call; the adapter-local dispatch below is
			// the pod-side tool invocation that §16.3 attributes to the Pod.
			if result, handled := HandleToolCall(line, wsRoot); handled {
				if err := s.emitLocalToolCall(ctx, sessionID, slotID, line, result, rt); err != nil {
					return err
				}
				continue
			}
			if err := stream.Send(&adapterv1.AttachResponse{EnvelopeJson: stripRuntimeFrom(line)}); err != nil {
				return err
			}
		case err := <-recvErr:
			if err == nil || errors.Is(err, io.EOF) {
				// The gateway half-closed the client direction; keep
				// streaming runtime output until it ends. A nil channel
				// disables this case for the rest of the stream.
				recvErr = nil
				continue
			}
			return err
		case <-hbHung:
			// spec: §28.5.3 — the runtime missed the heartbeat ack
			// deadline. The monitor already escalated through
			// Runtime.Interrupt once; the stream ends with DeadlineExceeded
			// so the gateway sees the unresponsive-agent escalation rather
			// than a clean close, and makes no second Interrupt call.
			// spec: §4.7.10 (Runtime process lifetime).
			return status.Error(codes.DeadlineExceeded, "runtime missed heartbeat ack deadline; session stream ended (§28.5.3)")
		case <-crashed:
			return errRuntimeCrash
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// isClosed reports whether ch is closed; a nil channel is not.
func isClosed(ch <-chan struct{}) bool {
	if ch == nil {
		return false
	}
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// emitLocalToolCall opens the §16.3 `session.tool_call` span for one
// adapter-local tool invocation, writes the tool_result back to the
// runtime's stdin, and ends the span.
// The span is per invocation per the §16.3 annotation. It carries the tool name as a descriptive
// attribute (a tool identifier, never arguments) and records an UPSTREAM
// error when the local tool reported isError, so a failing read of a
// missing file or a denied write surfaces on the trace. F-16.3.6.
func (s *Server) emitLocalToolCall(ctx context.Context, sessionID, slotID string, frame, result []byte, rt RuntimeProcess) error {
	// spec: §16.3 — `session.tool_call`, emitted by the Pod, one
	// span per tool invocation. NewTracer(nil) resolves the process-global
	// provider cmd/lenny-adapter installs; correlation fields auto-project.
	_, span := tracing.NewTracer(nil).Start(ctx, tracing.SpanSessionToolCall)
	defer span.End()
	if name := extractToolCallName(frame); name != "" {
		span.SetAttributes(attribute.String("tool.name", name))
	}
	if id := extractToolCallID(frame); id != "" {
		span.SetAttributes(attribute.String("tool.call_id", id))
	}
	// §16.3: a tool that returned isError is the UPSTREAM category — the
	// failure originates in the tool's execution, not in the adapter
	// transport. The tool_result is still delivered to the runtime.
	if toolResultIsError(result) {
		tracing.RecordError(span, tracing.CategorizeError(
			errors.New("adapter-local tool reported an error result"),
			tracing.CategoryUpstream,
		))
	}
	if err := s.writeSessionEnvelope(rt, sessionID, result); err != nil {
		tracing.RecordError(span, tracing.CategorizeError(err, tracing.CategoryTransient))
		return envelopeWriteStatus("deliver tool result to runtime", err)
	}
	return nil
}

// toolResultIsError reports whether a tool_result frame carries
// isError: true. A frame that does not parse leaves the result treated
// as a success so a parse hiccup does not flip a healthy tool to an
// error on the span.
func toolResultIsError(result []byte) bool {
	var probe struct {
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(result, &probe); err != nil {
		return false
	}
	return probe.IsError
}

// stripRuntimeFrom removes a runtime-set `from` field from an outbound
// JSONL frame before the adapter relays it to the gateway. Per
// schemas/lenny-adapter-jsonl.schema.json line 74 the `from` field is
// adapter-injected and runtimes MUST NOT set it; the gateway re-stamps
// the authoritative sender context. Stripping it here prevents a
// misbehaving runtime from spoofing a sender (e.g., appearing as a
// `client`). A frame without a top-level `from` object, or one that does
// not parse as a JSON object, is returned unchanged so non-envelope
// frames and malformed output pass through to the gateway's own
// validation untouched.
func stripRuntimeFrom(line []byte) []byte {
	if !bytes.Contains(line, []byte(`"from"`)) {
		return line
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(line, &obj); err != nil {
		return line
	}
	if _, ok := obj["from"]; !ok {
		return line
	}
	delete(obj, "from")
	sanitized, err := json.Marshal(obj)
	if err != nil {
		return line
	}
	return sanitized
}

// attachRecvLoop forwards each client envelope on the Attach stream to
// the runtime's stdin until the stream ends. Each envelope is stamped
// with the session's address so the shared runtime's dispatch loop routes
// it to that session's cwd.
func (s *Server) attachRecvLoop(stream grpc.BidiStreamingServer[adapterv1.AttachRequest, adapterv1.AttachResponse], sessionID string, rt RuntimeProcess) error {
	for {
		msg, err := stream.Recv()
		if err != nil {
			return err
		}
		if env := msg.GetEnvelopeJson(); len(env) > 0 {
			if err := s.writeSessionEnvelope(rt, sessionID, env); err != nil {
				return envelopeWriteStatus("deliver message to runtime", err)
			}
		}
	}
}

// writeSessionEnvelope stamps the session's address onto an outbound
// envelope and forwards it to the shared runtime over the single
// connection. Every session is bound to a slot on every pod and a
// session-mode slot's identifier is its session's identifier, so the
// stamp is unconditional and every inbound frame carries it.
// spec: §5.2; §6.4; §28.5.3.
//
// It is also the adapter's seal check and the activity stamp of each frame
// it writes (§4.7.1 **Session seal.**): in one hold of s.mu it refuses a
// message frame for a sealed session with errSessionSealed, writing and
// stamping nothing, and otherwise stamps the entry before the write; it
// stamps the same entry again when the write returns, whether or not the
// write succeeded. Every other frame type passes the seal.
// spec: §5.2; §6.4; §28.5.3; §4.7.1 (Role and Gateway RPC Contract);
// §28.5.1 (CH-ATTACH).
func (s *Server) writeSessionEnvelope(rt RuntimeProcess, sessionID string, envelope []byte) error {
	entry, err := s.admitSessionWrite(sessionID, jsonlFrameType(envelope) == "message")
	if err != nil {
		return err
	}
	stamped, err := stampSessionID(envelope, sessionID)
	if err != nil {
		return err
	}
	err = rt.WriteEnvelope(sessionID, stamped)
	s.stampEntry(entry)
	return err
}

// admitSessionWrite is the one critical section that admits a frame write
// for sessionID and stamps it as activity. It returns errSessionNotOpened
// for an entry whose session_start the open sequence has not written
// (§28.5.3 CH-MSGSOCK, Inbound: session_start, rule 1, so a frame never
// precedes the session's session_start), errSessionSealed for a message to
// a sealed session, and otherwise the entry it stamped.
// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start); §4.7.1 (Role and
// Gateway RPC Contract)
func (s *Server) admitSessionWrite(sessionID string, isMessage bool) (*slotState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.slotStateLocked(sessionID)
	if !ok || !st.sessionStartWritten {
		return nil, fmt.Errorf("session %s: %w", sessionID, errSessionNotOpened)
	}
	if isMessage && st.sealed {
		return nil, fmt.Errorf("session %s: %w", sessionID, errSessionSealed)
	}
	s.stampLocked(st)
	return st, nil
}

// errSessionSealed is the error of a message write for a session that
// SealSession sealed. The caller answers FailedPrecondition with a message
// beginning session_sealed:, which the gateway reads as a refused delivery
// rather than a stream failure. spec: §4.7.1 (Role and Gateway RPC
// Contract), §28.5.1 (CH-ATTACH).
var errSessionSealed = errors.New("session sealed against message delivery")

// sessionSealedPrefix opens the status message of a sealed refusal.
const sessionSealedPrefix = "session_sealed: "

// envelopeWriteStatus maps a writeSessionEnvelope error to the gRPC status
// its caller returns: FailedPrecondition with a message beginning
// session_sealed: for a message the seal refused, FailedPrecondition for a
// session whose session_start the adapter has not written, which the
// caller may retry once the start has opened the session, and Internal for
// a failed write.
// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start), rule 1; §28.5.1
// (CH-ATTACH).
func envelopeWriteStatus(what string, err error) error {
	if errors.Is(err, errSessionSealed) {
		return status.Errorf(codes.FailedPrecondition, "%s%s: %v", sessionSealedPrefix, what, err)
	}
	if errors.Is(err, errSessionNotOpened) {
		return status.Errorf(codes.FailedPrecondition, "%s: %v", what, err)
	}
	return status.Errorf(codes.Internal, "%s: %v", what, err)
}

// sessionScopedFrameTypes is the §28.5.3 session-scoped set: the frame
// types that address a session and are therefore relayed to that
// session's Attach stream alone. Every other frame type is
// protocol-level and passes through the demultiplexer unchanged. A
// heartbeat_ack reaches the session's heartbeat monitor through the
// activity tap rather than through any stream. spec: §28.5.3.
var sessionScopedFrameTypes = map[string]bool{
	"message":             true,
	"tool_call":           true,
	"tool_result":         true,
	"response":            true,
	"set_tracing_context": true,
	"status":              true,
}

// demuxSessionOutput filters the shared runtime's interleaved output
// stream down to the frames this Attach stream's session owns. The single
// pod-global runtime serves every session over one connection, so each
// Attach stream subscribes to the same fan-out.
//
// The predicate narrows by frame type. A frame whose type is outside the
// §28.5.3 session-scoped set is protocol-level (heartbeat and
// heartbeat_ack) and passes through; the Attach loop consumes a
// heartbeat_ack without relaying it. A session-scoped frame
// carrying this session's address is delivered and one carrying a
// co-tenant's is dropped. A session-scoped frame carrying no address
// resolves to this stream's own binding while the pod holds at most one
// slot, which is the only session it could name, and is rejected on a pod
// holding more than one slot, where nothing in the frame says which
// session it addresses. A rejection is relayed to no stream, counted on
// lenny_adapter_unaddressed_frame_rejected_total under the frame's own
// type, and logged with that type and the pod's slot count. The decision
// sits in each stream's demultiplexer, so one unaddressed frame on a pod
// holding two live Attach streams is evaluated, counted, and logged once
// per stream.
//
// slotCount reports the entries the adapter's slot registry holds, bound
// or registered-but-unbound, so the rule fails closed while a second
// session's workspace is being prepared. It is read per frame rather than
// once, because the pod's population changes over a stream's life. A
// count of zero falls in the same arm as a count of one: the ending
// session's stream drains the shared runtime's output after its entry was
// deleted, and the frame resolves to the one stream still open.
//
// ctx bounds the filter goroutine so a stalled consumer does not leak it.
// spec: §28.5.3.
func demuxSessionOutput(ctx context.Context, in <-chan []byte, sessionID string, slotCount func() int) <-chan []byte {
	out := make(chan []byte)
	go func() {
		defer close(out)
		for {
			select {
			case line, ok := <-in:
				if !ok {
					return
				}
				if !deliverToSession(line, sessionID, slotCount) {
					continue
				}
				select {
				case out <- line:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// deliverToSession applies §28.5.3's type-scoped resolve-or-reject rule to
// one runtime output frame on the Attach stream bound to sessionID, and
// reports whether the stream relays it. A rejected unaddressed frame is
// counted and logged here, which is the one site that decides it.
// spec: §28.5.3.
func deliverToSession(line []byte, sessionID string, slotCount func() int) bool {
	frameType := jsonlFrameType(line)
	if !sessionScopedFrameTypes[frameType] {
		return true
	}
	switch addr := frameSessionID(line); {
	case addr == sessionID:
		return true
	case addr != "":
		// A co-tenant's frame belongs to a sibling Attach stream.
		return false
	}
	if n := slotCount(); n > 1 {
		incUnaddressedFrameRejected(frameType)
		log.Printf("lenny-adapter: protocol error: %s frame carrying no session identifier rejected on the stream bound to session %s; the pod holds %d slots",
			frameType, sessionID, n)
		return false
	}
	return true
}
