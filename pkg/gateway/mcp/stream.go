// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
)

// AttachToolName is the §15.2 line 1289 `attach_session` MCP tool. The
// gateway registers it under the `lenny/` namespace like every other
// platform tool; the transport intercepts a `tools/call` for this name
// when the client requests `text/event-stream` and upgrades the
// response to the Streamable HTTP SSE channel. spec: §15.2 line 1289.
const AttachToolName = "lenny/attach_session"

// attachKeepAliveInterval is the §15.2 line 1333 fixed keepalive cadence:
// the adapter writes an SSE comment line whenever no SessionEvent frame
// has been written for 20 seconds. The interval is fixed by the protocol
// contract (not tunable per connection); it is a package var only so the
// streaming test can shorten it. spec: §15.2 line 1333.
var attachKeepAliveInterval = 20 * time.Second

// SessionEventSource is the read side of the §15.1 session event bus the
// §15.2 Streamable HTTP SSE channel streams from. *sessionevents.Bus
// satisfies it. Defining the interface here keeps the transport coupled
// only to the subscribe + eviction-cursor surface it needs.
type SessionEventSource interface {
	// SubscribeForTenant registers a tenant-bound subscriber, returning
	// the retained backlog (events with Seq > afterSeq) plus a live
	// channel. A tenant id that does not match the session's frozen
	// binding returns an error so a foreign caller cannot attach.
	SubscribeForTenant(tenantID, sessionID string, afterSeq uint64, bufferSize int) (*sessionevents.Subscription, error)
	// OldestRetainedSeq reports the smallest Seq still in the replay
	// buffer, used to detect the §15.2 line 1331 eviction case.
	OldestRetainedSeq(sessionID string) (uint64, bool)
}

// AttachConfig wires the §15.2 Streamable HTTP SSE channel into the MCP
// transport. A zero config (Events == nil) leaves attach streaming off,
// so a `lenny/attach_session` tools/call falls through to the registered
// snapshot handler on every transport.
type AttachConfig struct {
	// Events is the §15.1 session event bus the stream replays from and
	// tails. Required to enable streaming.
	Events SessionEventSource
	// TenantFromRequest resolves the caller's tenant from the upgraded
	// request so the bus enforces the §7.2 tenant binding. A nil resolver
	// streams under the empty tenant (the legacy untenanted bus path).
	TenantFromRequest func(*http.Request) string
	// Authorize, when set, gates the attach before any SSE byte is
	// written: it returns a *ToolError (e.g. RESOURCE_NOT_FOUND) when the
	// session does not exist or is not visible to the caller's tenant.
	// Production wires the §4.2 session-store Get. A nil Authorize relies
	// on the bus tenant binding alone. spec: §7.2 tenant isolation.
	Authorize func(ctx context.Context, tenantID, sessionID string) error
	// Now supplies the clock for event timestamps on the wire. A nil Now
	// defaults to time.Now.
	Now func() time.Time
}

func (c AttachConfig) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// SetAttach installs the §15.2 Streamable HTTP SSE channel. When wired, a
// `tools/call` for AttachToolName carrying `Accept: text/event-stream` is
// intercepted by the transport (Server.handleToolCall) and upgraded to
// the SSE stream; a non-SSE caller (a WebSocket frame or a plain JSON
// POST) falls through to the registered snapshot handler. spec: §15.2
// lines 1331-1333. F-15.2.2, F-9.1.7.
func (s *Server) SetAttach(cfg AttachConfig) {
	s.attach = cfg
}

// wantsEventStream reports whether the caller asked for the §15.2
// Streamable HTTP SSE channel via the Accept header.
func wantsEventStream(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream")
}

// lastEventID extracts the SSE-standard Last-Event-ID reconnect cursor —
// the §15.2 line 1331 "implicit resumeFromSeq on plain reconnects".
func lastEventID(r *http.Request) uint64 {
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			return n
		}
	}
	return 0
}

// attachArgs is the §15.2 line 1289 `attach_session` argument object.
type attachArgs struct {
	SessionID     string `json:"sessionId"`
	ResumeFromSeq uint64 `json:"resumeFromSeq"`
}

// handleAttachStream serves the §15.2 Streamable HTTP SSE channel for an
// `attach_session` tools/call. It mirrors the REST
// GET /v1/sessions/{id}/events transport (the two share the §15.1 event
// bus and the §15.2 line 1331 resume contract) and projects each
// SessionEvent onto the MCP wire as a `notifications/lenny/sessionEvent`
// JSON-RPC notification carrying the event's SeqNum as the SSE `id:`
// line. The per-kind MCP method projection (notifications/tasks/
// statusUpdate, elicitation/create, MCP Tasks final-state) is the
// follow-on tracked under F-15.2.13; this method delivers the transport
// the finding flags as absent: the SSE channel itself, resumeFromSeq /
// Last-Event-ID replay, the gap_detected stream-control frame, and the
// 20s keepalive. spec: §15.2 lines 1331-1333. F-15.2.2, F-9.1.7.
func (s *Server) handleAttachStream(w http.ResponseWriter, r *http.Request, req jsonRPCRequest, arguments json.RawMessage) {
	var in attachArgs
	if err := json.Unmarshal(arguments, &in); err != nil {
		s.WriteLennyError(w, req.ID, errInvalidParams, "VALIDATION_ERROR",
			"params.arguments is not a valid attach_session object", nil)
		return
	}
	if in.SessionID == "" {
		s.WriteLennyError(w, req.ID, errInvalidParams, "VALIDATION_ERROR", "sessionId is required", nil)
		return
	}

	tenant := ""
	if s.attach.TenantFromRequest != nil {
		tenant = s.attach.TenantFromRequest(r)
	}

	// Authorize before any SSE byte is written so a missing or foreign
	// session surfaces as a normal JSON-RPC error (the client never sees
	// a half-open event stream). spec: §7.2 tenant isolation.
	if s.attach.Authorize != nil {
		if err := s.attach.Authorize(r.Context(), tenant, in.SessionID); err != nil {
			code, msg, details := "RESOURCE_NOT_FOUND", err.Error(), map[string]any(nil)
			var te *ToolError
			if errors.As(err, &te) {
				if te.Code != "" {
					code = te.Code
				}
				msg = te.Msg
				details = te.Details
			}
			s.WriteLennyError(w, req.ID, errInvalidParams, code, msg, details)
			return
		}
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		s.WriteLennyError(w, req.ID, errInternal, "INTERNAL_ERROR",
			"response writer does not support streaming", nil)
		return
	}

	// resumeFromSeq takes precedence; a plain reconnect that omitted it
	// falls back to the SSE Last-Event-ID header.
	afterSeq := in.ResumeFromSeq
	if afterSeq == 0 {
		afterSeq = lastEventID(r)
	}

	// §7.2 defense-in-depth: the bus enforces the tenant binding even if
	// the Authorize precheck above is unwired.
	sub, err := s.attach.Events.SubscribeForTenant(tenant, in.SessionID, afterSeq, 64)
	if err != nil {
		s.WriteLennyError(w, req.ID, errInvalidParams, "RESOURCE_NOT_FOUND", "session not found", nil)
		return
	}
	defer sub.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// spec: §15.2 line 1331 — when the requested cursor falls below the
	// oldest retained sequence the client missed evicted events; emit a
	// single protocol-level gap_detected frame ahead of the backlog. The
	// frame is a stream-control signal, not a SessionEvent, so it carries
	// no `id:` line. Cursor 0 is a first attach and is never a gap.
	if afterSeq > 0 {
		if oldest, ok := s.attach.Events.OldestRetainedSeq(in.SessionID); ok && oldest > afterSeq+1 {
			writeMCPGapDetected(w, afterSeq, oldest)
		}
	}

	// Replay the backlog (events missed while disconnected) before live
	// delivery; each frame carries its SeqNum as the SSE id: line so a
	// later reconnect resumes verbatim.
	for _, ev := range sub.Backlog {
		writeMCPSessionEvent(w, ev)
	}
	flusher.Flush()

	keepalive := time.NewTicker(attachKeepAliveInterval)
	defer keepalive.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, open := <-sub.Events():
			if !open {
				return
			}
			writeMCPSessionEvent(w, ev)
			flusher.Flush()
			// Reset the idle timer: keepalive fires only after 20s with
			// no SessionEvent frame written. spec: §15.2 line 1333.
			keepalive.Reset(attachKeepAliveInterval)
		case <-keepalive.C:
			// spec: §15.2 line 1333 — `:keepalive\n\n` SSE comment line.
			// Carries no id:, so it does not affect SeqNum / Last-Event-ID
			// tracking or the gap_detected contract.
			fmt.Fprint(w, ":keepalive\n\n")
			flusher.Flush()
		}
	}
}

// writeMCPSessionEvent projects one SessionEvent onto the §15.2 Streamable
// HTTP wire. The SSE `id:` line carries the SeqNum (so resumeFromSeq and
// Last-Event-ID replay the frame verbatim); the `data:` line carries the
// §15.2.1 per-kind JSON-RPC frame (notifications/tasks/statusUpdate,
// elicitation/create, notifications/lenny/toolCall, notifications/lenny/
// error, or the MCP Tasks final-state frame), falling back to the generic
// notifications/lenny/sessionEvent frame for bus event types outside the
// closed SessionEventKind enum. spec: §15.2 lines 1331, 1356-1374.
func writeMCPSessionEvent(w http.ResponseWriter, ev sessionevents.Event) {
	b := marshalMCPSessionEvent(ev)
	if b == nil {
		return
	}
	fmt.Fprintf(w, "id: %d\n", ev.Seq)
	fmt.Fprintf(w, "data: %s\n\n", b)
}

// marshalMCPSessionEvent renders one SessionEvent as the §15.2.1 per-kind
// MCP wire frame, shared by the §15.2 Streamable HTTP SSE channel
// (writeMCPSessionEvent) and the §4.1 WebSocket push (startWSAttach) so a
// client classifies an event the same way regardless of transport. The
// per-kind projection itself lives in projection.go. A marshal failure
// returns nil and the caller drops the frame. spec: §15.2 lines 1331,
// 1356-1374. F-15.2.13, F-15.2.14.
func marshalMCPSessionEvent(ev sessionevents.Event) []byte {
	return projectMCPSessionEvent(ev)
}

// writeMCPGapDetected writes the §15.2 line 1331 gap_detected
// stream-control frame: a `notifications/lenny/gapDetected` JSON-RPC
// notification carrying {lastSeenSeq, nextSeq}. It carries no `id:` line
// because it is not a SessionEvent and not part of the SessionEventKind
// closed enum. spec: §15.2 line 1331.
func writeMCPGapDetected(w http.ResponseWriter, lastSeen, next uint64) {
	b := marshalMCPGapDetected(lastSeen, next)
	if b == nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", b)
}

// marshalMCPGapDetected renders the §15.2 line 1331 gap_detected
// stream-control notification bytes shared by the SSE and WebSocket legs.
func marshalMCPGapDetected(lastSeen, next uint64) []byte {
	b, err := json.Marshal(jsonRPCNotification{
		JSONRPC: "2.0",
		Method:  "notifications/lenny/gapDetected",
		Params: map[string]any{
			"lastSeenSeq": lastSeen,
			"nextSeq":     next,
		},
	})
	if err != nil {
		return nil
	}
	return b
}

// jsonRPCNotification is a JSON-RPC 2.0 notification (a request with no
// id), the wire form of every server-pushed frame on the §15.2 SSE
// channel.
type jsonRPCNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}
