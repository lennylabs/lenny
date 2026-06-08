// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"nhooyr.io/websocket"
)

// wsAttachBuffer is the per-subscriber live-event channel depth for an
// attach over the §4.1 WebSocket transport. It matches the §15.2 Streamable
// HTTP SSE channel's buffer (handleAttachStream) so the two legs apply the
// same backpressure to a slow client.
const wsAttachBuffer = 64

// wsAttachRequest reports whether a raw inbound WebSocket frame is a
// `tools/call` for the §15.2 line 1289 `attach_session` tool, returning the
// parsed request envelope and the tool arguments. Detecting it before the
// generic dispatch lets the WebSocket leg stream the per-session event bus
// (a long-lived push) instead of returning the single snapshot frame the
// registered handler produces. A non-attach or malformed frame returns
// ok=false and the caller falls through to the normal request/response
// dispatch. spec: §15.2 line 1289.
func wsAttachRequest(data []byte) (jsonRPCRequest, json.RawMessage, bool) {
	var req jsonRPCRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return req, nil, false
	}
	if req.JSONRPC != "2.0" || req.Method != "tools/call" {
		return req, nil, false
	}
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil || p.Name != AttachToolName {
		return req, nil, false
	}
	return req, p.Arguments, true
}

// startWSAttach serves an `attach_session` tools/call that arrived over the
// §4.1 MCP WebSocket transport. Whereas the Streamable HTTP leg upgrades the
// HTTP response to SSE (handleAttachStream), the WebSocket carries the same
// §15.1 session-event stream as server-pushed `notifications/lenny/sessionEvent`
// frames on the existing connection: the read loop keeps serving
// request/response frames (send_message, interrupt_session, ...) while the
// returned goroutine tails the bus. A §27.5 chat client therefore receives the
// agent's output, tool-use, and lifecycle events live over the one socket the
// chat-stream contract mandates, rather than only the snapshot the registered
// handler returns.
//
// The tools/call ack (or a structured error) is written synchronously so it
// precedes the backlog; the push goroutine then replays the retained backlog
// and tails live events. The returned cancel func stops the goroutine: the
// caller cancels a prior attach before starting a new one and on connection
// close. A nil return means the attach failed (the error was already written)
// and there is nothing to cancel. Every outbound frame passes through the same
// §27.9 redaction gate the request/response path applies. spec: §15.2 lines
// 1331, 1370; §27.5 R2; F-27.4.7.
func (s *Server) startWSAttach(parent context.Context, conn *websocket.Conn, r *http.Request, req jsonRPCRequest, arguments json.RawMessage, redact bool) context.CancelFunc {
	var in attachArgs
	if err := json.Unmarshal(arguments, &in); err != nil {
		s.writeWSError(parent, conn, req.ID, errInvalidParams, "VALIDATION_ERROR",
			"params.arguments is not a valid attach_session object", nil, redact)
		return nil
	}
	if in.SessionID == "" {
		s.writeWSError(parent, conn, req.ID, errInvalidParams, "VALIDATION_ERROR", "sessionId is required", nil, redact)
		return nil
	}

	tenant := ""
	if s.attach.TenantFromRequest != nil {
		tenant = s.attach.TenantFromRequest(r)
	}

	// Authorize before any event frame is written so a missing or foreign
	// session surfaces as a normal JSON-RPC error rather than a half-open
	// stream, the same contract the SSE leg enforces. spec: §7.2 isolation.
	if s.attach.Authorize != nil {
		if err := s.attach.Authorize(parent, tenant, in.SessionID); err != nil {
			code, msg, details := "RESOURCE_NOT_FOUND", err.Error(), map[string]any(nil)
			var te *ToolError
			if errors.As(err, &te) {
				if te.Code != "" {
					code = te.Code
				}
				msg = te.Msg
				details = te.Details
			}
			s.writeWSError(parent, conn, req.ID, errInvalidParams, code, msg, details, redact)
			return nil
		}
	}

	afterSeq := in.ResumeFromSeq
	// §7.2 defense-in-depth: the bus enforces the tenant binding even when
	// the Authorize precheck above is unwired.
	sub, err := s.attach.Events.SubscribeForTenant(tenant, in.SessionID, afterSeq, wsAttachBuffer)
	if err != nil {
		s.writeWSError(parent, conn, req.ID, errInvalidParams, "RESOURCE_NOT_FOUND", "session not found", nil, redact)
		return nil
	}

	// Ack the attach so the client knows the stream is open before any event
	// frame arrives; writing it synchronously here guarantees it precedes the
	// backlog the goroutine replays next.
	ack := marshalResult(req.ID, map[string]any{
		"attached":      true,
		"sessionId":     in.SessionID,
		"resumeFromSeq": afterSeq,
	})
	if !s.writeWSBytes(parent, conn, ack, redact) {
		sub.Close()
		return nil
	}

	ctx, cancel := context.WithCancel(parent)
	go func() {
		defer sub.Close()
		// §15.2 line 1331 — when the cursor fell below the oldest retained
		// event the client missed evicted events; emit one gap_detected frame
		// ahead of the backlog. Cursor 0 is a first attach and is never a gap.
		if afterSeq > 0 {
			if oldest, ok := s.attach.Events.OldestRetainedSeq(in.SessionID); ok && oldest > afterSeq+1 {
				if !s.writeWSBytes(ctx, conn, marshalMCPGapDetected(afterSeq, oldest), redact) {
					return
				}
			}
		}
		for _, ev := range sub.Backlog {
			if !s.writeWSBytes(ctx, conn, marshalMCPSessionEvent(ev), redact) {
				return
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case ev, open := <-sub.Events():
				if !open {
					return
				}
				if !s.writeWSBytes(ctx, conn, marshalMCPSessionEvent(ev), redact) {
					return
				}
			}
		}
	}()
	return cancel
}

// writeWSBytes writes one text frame to the WebSocket, applying the §27.9
// egress redaction when the connection is playground-origin. It bounds the
// write with wsWriteTimeout so a stalled client cannot block the push
// goroutine (or the read loop, which shares the connection write mutex)
// indefinitely. It reports whether the write succeeded; a failure (closed
// connection, timeout, cancelled context, or a nil frame from a marshal
// error) tells the caller to stop. spec: §27.9 line 251.
func (s *Server) writeWSBytes(ctx context.Context, conn *websocket.Conn, b []byte, redact bool) bool {
	if b == nil {
		return false
	}
	if redact {
		b = redactPlaygroundFrame(b)
	}
	writeCtx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageText, b) == nil
}

// writeWSError writes a §15.2.1 lenny error envelope frame on the WebSocket,
// the transport analogue of Server.WriteLennyError for the attach path.
func (s *Server) writeWSError(ctx context.Context, conn *websocket.Conn, id json.RawMessage, jsonRPCCode int, lennyCode, message string, details map[string]any, redact bool) {
	s.writeWSBytes(ctx, conn, marshalLennyError(id, jsonRPCCode, lennyCode, message, details), redact)
}
