// SPDX-License-Identifier: MIT

package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

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
	if err := s.checkSession(sessionID); err != nil {
		return err
	}
	if env := first.GetEnvelopeJson(); len(env) > 0 {
		if err := s.Runtime.WriteEnvelope(sessionID, env); err != nil {
			return status.Errorf(codes.Internal, "deliver message to runtime: %v", err)
		}
	}

	ctx := stream.Context()
	out, err := s.Runtime.Output(ctx, sessionID)
	if err != nil {
		return status.Errorf(codes.Internal, "open runtime output: %v", err)
	}

	// The receive loop forwards client envelopes to the runtime's
	// stdin; it runs concurrently with the send loop below. recvErr is
	// buffered so the loop's final send never blocks once the send
	// loop has stopped selecting on it.
	recvErr := make(chan error, 1)
	go func() {
		recvErr <- s.attachRecvLoop(stream, sessionID)
	}()

	for {
		select {
		case line, ok := <-out:
			if !ok {
				// The runtime's output ended; the session is done.
				return nil
			}
			// §15.4.1: an adapter-local tool_call is answered by the
			// adapter itself and never relayed to the gateway. A relayed
			// platform/connector tool_call is traced gateway-side as
			// mcp.external_tool_call; the adapter-local dispatch below is
			// the pod-side tool invocation that §16.3 attributes to the Pod.
			if result, handled := HandleToolCall(line, s.WorkspaceRoot); handled {
				if err := s.emitLocalToolCall(ctx, sessionID, line, result); err != nil {
					return err
				}
				continue
			}
			// spec: §10.1 line 173 — record the most recent tool_call id
			// so a subsequent CheckpointBarrier ack can carry it for the
			// gateway's resume-deduplication path. Best-effort: a malformed
			// frame leaves the prior last_tool_call_id unchanged.
			if id := extractToolCallID(line); id != "" {
				s.SetLastToolCallID(id)
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
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// emitLocalToolCall opens the §16.3 `session.tool_call` span for one
// adapter-local tool invocation, records the §10.1 last_tool_call_id,
// writes the tool_result back to the runtime's stdin, and ends the span.
// The span is per invocation per the §16.3 line 343 "(per tool
// invocation)" annotation. It carries the tool name as a descriptive
// attribute (a tool identifier, never arguments) and records an UPSTREAM
// error when the local tool reported isError, so a failing read of a
// missing file or a denied write surfaces on the trace. F-16.3.6.
func (s *Server) emitLocalToolCall(ctx context.Context, sessionID string, frame, result []byte) error {
	// spec: §16.3 line 343 — `session.tool_call`, emitted by the Pod, one
	// span per tool invocation. NewTracer(nil) resolves the process-global
	// provider cmd/lenny-adapter installs; correlation fields auto-project.
	_, span := tracing.NewTracer(nil).Start(ctx, tracing.SpanSessionToolCall)
	defer span.End()
	if name := extractToolCallName(frame); name != "" {
		span.SetAttributes(attribute.String("tool.name", name))
	}
	if id := extractToolCallID(frame); id != "" {
		span.SetAttributes(attribute.String("tool.call_id", id))
		s.SetLastToolCallID(id)
	}
	// §16.3: a tool that returned isError is the UPSTREAM category — the
	// failure originates in the tool's execution, not in the adapter
	// transport. The tool_result is still delivered to the runtime.
	if toolResultIsError(result) {
		tracing.RecordError(span, tracing.CategorizeError(
			errors.New("adapter-local tool reported an error result"),
			tracing.CategoryUpstream))
	}
	if err := s.Runtime.WriteEnvelope(sessionID, result); err != nil {
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
// the runtime's stdin until the stream ends.
func (s *Server) attachRecvLoop(stream grpc.BidiStreamingServer[adapterv1.AttachRequest, adapterv1.AttachResponse], sessionID string) error {
	for {
		msg, err := stream.Recv()
		if err != nil {
			return err
		}
		if env := msg.GetEnvelopeJson(); len(env) > 0 {
			if err := s.Runtime.WriteEnvelope(sessionID, env); err != nil {
				return status.Errorf(codes.Internal, "deliver message to runtime: %v", err)
			}
		}
	}
}
