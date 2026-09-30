// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/pagination"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// transcriptSortField is the only sort key valid on the §15.1
// transcript: entries are written in monotonic `seq` order and that is
// the only meaningful ordering. spec: §15.1.
const transcriptSortField = "seq"

// transcriptDefaultSort sorts by per-session monotonic sequence in
// ascending order so the §15.1 transcript reads chronologically.
var transcriptDefaultSort = pagination.Sort{Field: transcriptSortField, Direction: pagination.DirectionAsc}

// handleTranscript implements GET /v1/sessions/{id}/transcript per
// §15.1. Supports the canonical `{items, cursor, hasMore}` envelope
// (spec §15.1) with opaque cursors, [1, 200] limit
// clamping, and 24-hour cursor TTL. The legacy `?afterSeq=` parameter
// is still honoured for backwards-compatible clients that have not
// switched to opaque cursors yet — the cursor is the canonical form.
// Returns 404 RESOURCE_NOT_FOUND when the session does not exist or
// has no recorded transcript.
func (s *Server) handleTranscript(w http.ResponseWriter, r *http.Request) {
	tenantID := s.resolveTenant(r)
	id := r.PathValue("id")

	// The session must exist (and belong to the tenant) before we
	// surface a transcript — a transcript for a foreign session must
	// not leak.
	if _, err := s.store.Get(r.Context(), tenantID, id); err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if s.transcripts == nil {
		s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND",
			"session has no recorded transcript", nil)
		return
	}

	params, ferr := pagination.ParseRequest(r,
		[]string{transcriptSortField}, transcriptDefaultSort, s.clock())
	if ferr != nil {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", ferr.Message, ferr.Details())
		return
	}
	afterSeq := uint64(0)
	if params.Cursor.Tiebreak != "" {
		if n, err := strconv.ParseUint(params.Cursor.Tiebreak, 10, 64); err == nil {
			afterSeq = n
		}
	} else if v := r.URL.Query().Get("afterSeq"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			afterSeq = n
		}
	}

	// Fetch one extra entry so we can detect whether further pages
	// exist without re-querying the store.
	entries, err := s.transcripts.Page(r.Context(), tenantID, id, afterSeq, params.Limit+1)
	if err != nil && !errors.Is(err, transcriptstore.ErrNotFound) {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if entries == nil {
		entries = []transcriptstore.Entry{}
	}
	hasMore := false
	if len(entries) > params.Limit {
		entries = entries[:params.Limit]
		hasMore = true
	}
	envelope := pagination.Envelope[transcriptstore.Entry]{
		Items:   entries,
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
