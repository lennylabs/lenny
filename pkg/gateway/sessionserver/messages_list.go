// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/pagination"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/transcriptstore"
)

// messageFrom is the §15.4.1 MessageEnvelope `from` attribution surfaced
// on each MessageDAG node. The transcript store records the message
// `role`; the gateway derives the canonical `{kind, id}` object from it
// using the same conventions the executor stamps at delivery time
// (`client`/`client_{opaque}` for a client turn, `agent`/`sess_{id}` for
// an agent response, `system`/`lenny-gateway` for a platform message).
//
// spec: §15.4.1 lines 1696-1707 (`from` closed enum + id formats).
type messageFrom struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
}

// messageDelivery is the §15.4 delivery state surfaced on each MessageDAG
// node. Every row in the durable session_messages store was delivered
// before it was recorded (the §7.2 queued / dropped / expired paths never
// reach the transcript), so the recorded state is always `delivered`.
//
// spec: §15.1 line 692 — the list "Returns message history including
// delivery receipts and state."
type messageDelivery struct {
	Status      string `json:"status"`
	DeliveredAt string `json:"deliveredAt,omitempty"`
}

// messageNode is one §15.4.1 MessageDAG node returned by
// GET /v1/sessions/{id}/messages. In v1 there is one implicit thread per
// session (`threadId` absent on every row) and the transcript-write path
// records no parent edge, so `threadId` and `inReplyTo` are empty; the
// fields are carried so the DAG model is forward-compatible without a
// schema change.
//
// spec: §15.4.1 lines 1788-1798 (DAG conversation model).
type messageNode struct {
	ID            string          `json:"id"`
	Seq           uint64          `json:"seq"`
	From          messageFrom     `json:"from"`
	Role          string          `json:"role"`
	Content       string          `json:"content"`
	ThreadID      string          `json:"threadId,omitempty"`
	InReplyTo     string          `json:"inReplyTo,omitempty"`
	SchemaVersion int             `json:"schemaVersion"`
	CreatedAt     string          `json:"createdAt"`
	Delivery      messageDelivery `json:"delivery"`
}

// handleMessagesList implements GET /v1/sessions/{id}/messages per §15.1
// line 692: the §15.4.1 MessageDAG over the durable session_messages
// store, listing the messages sent to and from a session with their
// delivery state. The view shares the session_messages backing with
// GET /v1/sessions/{id}/transcript (the transcript is the linearized
// role/content projection; this is the message-node projection with the
// stable id, derived `from` attribution, and delivery state).
//
// It supports the canonical §15.1 `{items, cursor, hasMore}` envelope
// with opaque cursors, the spec-named `?since=` (coordinator-local seq)
// and `?threadId=` filters (§15.4.1 line 1792), and [1, 200] limit
// clamping. A `derive_failure` audit row, a missing/cross-tenant
// session, or a gateway with no transcript store wired returns
// 404 RESOURCE_NOT_FOUND per §15.1 line 661.
func (s *Server) handleMessagesList(w http.ResponseWriter, r *http.Request) {
	tenantID := s.resolveTenant(r)
	id := r.PathValue("id")

	// The session must exist (and belong to the tenant) before we
	// surface its messages — a foreign session must not leak.
	row, err := s.store.Get(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// spec: §15.1 line 661 — a derive_failure audit row never delivered
	// a message, so the message list 404s.
	if isDeriveFailureRow(row) {
		s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session has no messages", nil)
		return
	}
	if s.transcripts == nil {
		s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND",
			"session has no recorded messages", nil)
		return
	}

	params, ferr := pagination.ParseRequest(r,
		[]string{transcriptSortField}, transcriptDefaultSort, s.clock())
	if ferr != nil {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", ferr.Message, ferr.Details())
		return
	}

	// Resolve the start cursor: the opaque cursor tiebreak wins (canonical
	// form); the spec-named `?since=` seq is the convenience filter for
	// clients that track the coordinator-local sequence directly. spec:
	// §15.4.1 lines 1792-1793.
	afterSeq := uint64(0)
	if params.Cursor.Tiebreak != "" {
		if n, err := strconv.ParseUint(params.Cursor.Tiebreak, 10, 64); err == nil {
			afterSeq = n
		}
	} else if v := r.URL.Query().Get("since"); v != "" {
		n, perr := strconv.ParseUint(v, 10, 64)
		if perr != nil {
			s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"`since` must be a non-negative integer sequence number",
				map[string]any{"field": "since", "value": v})
			return
		}
		afterSeq = n
	}

	// spec: §15.4.1 lines 1791, 1796 — v1 has one implicit unlabeled
	// thread per session, so every recorded node carries an empty
	// threadId. A `?threadId=` filter naming a concrete thread therefore
	// matches nothing; an absent/empty filter returns the implicit thread.
	threadID := r.URL.Query().Get("threadId")
	if threadID != "" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(pagination.Envelope[messageNode]{Items: []messageNode{}})
		return
	}

	// Fetch one extra entry so we can detect whether further pages exist
	// without re-querying the store.
	entries, err := s.transcripts.Page(r.Context(), tenantID, id, afterSeq, params.Limit+1)
	if err != nil && !errors.Is(err, transcriptstore.ErrNotFound) {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	hasMore := false
	if len(entries) > params.Limit {
		entries = entries[:params.Limit]
		hasMore = true
	}

	items := make([]messageNode, 0, len(entries))
	for _, e := range entries {
		items = append(items, toMessageNode(e, row.ID))
	}
	envelope := pagination.Envelope[messageNode]{
		Items:   items,
		HasMore: hasMore,
	}
	if hasMore && len(entries) > 0 {
		last := entries[len(entries)-1]
		seqStr := strconv.FormatUint(last.Seq, 10)
		envelope.Cursor = pagination.MintCursor(params.Sort, seqStr, seqStr, s.clock())
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(envelope)
}

// toMessageNode projects a recorded transcript entry onto a §15.4.1
// MessageDAG node. The `from` attribution is derived from the role; the
// delivery state is `delivered` (a recorded message was delivered before
// it was transcribed). spec: §15.4.1 lines 1696-1707, 1788-1798.
func toMessageNode(e transcriptstore.Entry, sessionID string) messageNode {
	node := messageNode{
		ID:            e.ID,
		Seq:           e.Seq,
		From:          deriveMessageFrom(e.Role, sessionID),
		Role:          e.Role,
		Content:       e.Content,
		SchemaVersion: e.SchemaVersion,
		Delivery:      messageDelivery{Status: "delivered"},
	}
	if !e.Timestamp.IsZero() {
		ts := e.Timestamp.UTC().Format(time.RFC3339Nano)
		node.CreatedAt = ts
		node.Delivery.DeliveredAt = ts
	}
	return node
}

// deriveMessageFrom maps a §7.2 transcript role to the §15.4.1 `from`
// object, mirroring the identities the executor stamps at delivery time.
// An assistant response is attributed to this session's agent; a system
// message to the gateway; a user turn to the gateway client.
//
// The id formats follow the closed `from.kind` table: an `agent` id is
// `sess_{session_id}`, a `system` id is the literal `lenny-gateway`, and
// a `client` id is `client_{opaque}` (the transcript-reconstruction path
// has no per-message client identifier, so it uses a stable sentinel).
//
// spec: §15.4.1 lines 1700-1705 (`from.id` format per `kind`).
func deriveMessageFrom(role, sessionID string) messageFrom {
	switch role {
	case "assistant":
		return messageFrom{Kind: "agent", ID: "sess_" + sessionID}
	case "system":
		return messageFrom{Kind: "system", ID: "lenny-gateway"}
	default:
		return messageFrom{Kind: "client", ID: "client_gateway"}
	}
}
