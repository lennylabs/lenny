// SPDX-License-Identifier: MIT

package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	if env := first.GetEnvelopeJson(); len(env) > 0 {
		if err := s.writeSessionEnvelope(rt, sessionID, env); err != nil {
			return status.Errorf(codes.Internal, "deliver message to runtime: %v", err)
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

	// spec: §28.5.3 — the adapter probes runtime liveness
	// with periodic heartbeats and SIGTERMs a process that misses the ack
	// deadline. Disabled (hbHung == nil) unless HeartbeatInterval is set.
	hb := s.startHeartbeat(ctx, sessionID, rt)
	var hbHung <-chan struct{}
	if hb != nil {
		hbHung = hb.hung
	}

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
				// The runtime's output ended; the session is done.
				return nil
			}
			// spec: §28.5.3 — heartbeat_ack is protocol-level
			// with no content payload; it answers the adapter's liveness
			// probe and is never relayed to the gateway.
			if hb != nil && jsonlFrameType(line) == "heartbeat_ack" {
				hb.ack()
				continue
			}
			// spec: §28.5.3 — set_tracing_context is an outbound
			// protocol frame the adapter consumes (it registers the
			// tracing identifiers with the gateway for delegation
			// propagation) and never relays as content. Available at all
			// tiers, so even a Basic runtime with no MCP access reaches the
			// same gateway registration the lenny/set_tracing_context MCP
			// tool performs. The handler resolves the frame against this
			// stream's (session, slot) address and drops one that addresses
			// another stream, so an untagged frame on a concurrent pod no
			// longer registers against every slot's session.
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
			// spec: §28.5.3 — the runtime missed the heartbeat
			// ack deadline. The adapter SIGTERMs the hung process and ends
			// the stream with DeadlineExceeded so the gateway sees the
			// unresponsive-agent escalation rather than a clean close.
			s.onHeartbeatHung(ctx, sessionID, rt)
			return status.Error(codes.DeadlineExceeded, "runtime missed heartbeat ack deadline; sent SIGTERM (§28.5.3)")
		case <-ctx.Done():
			return ctx.Err()
		}
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
		return status.Errorf(codes.Internal, "deliver tool result to runtime: %v", err)
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
				return status.Errorf(codes.Internal, "deliver message to runtime: %v", err)
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
func (s *Server) writeSessionEnvelope(rt RuntimeProcess, sessionID string, envelope []byte) error {
	stamped, err := stampSessionID(envelope, sessionID)
	if err != nil {
		return err
	}
	return rt.WriteEnvelope(sessionID, stamped)
}

// sessionScopedFrameTypes is the §28.5.3 session-scoped set: the frame
// types that address a session and are therefore relayed to that
// session's Attach stream alone. Every other frame type is
// protocol-level and passes through the demultiplexer unchanged, which is
// what keeps the per-stream heartbeat monitor answering on a pod-global
// runtime that acks unstamped. spec: §28.5.3.
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
// heartbeat_ack) and passes through, so each session's heartbeat monitor
// still sees its ack on an unaddressed frame. A session-scoped frame
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
