// SPDX-License-Identifier: MIT

package translator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// errContinuationNotFound marks a response id that names no completed
// response of a session in the caller's tenant: an unparsable id, an unknown
// or cross-tenant session, an ordinal past the session's newest response, or
// a response that has not completed. The handler maps it to a native OpenAI
// 404 with no rehydration and no dispatch. A cross-tenant id is
// indistinguishable from an unknown id because the tenant-scoped
// sessionstore.Get returns ErrNotFound for both.
// spec: §15 (Open Responses conversation sessions); §4.2 (session-store
// tenant isolation).
var errContinuationNotFound = errors.New("continuity: response not found")

// continuity resolves Open Responses response ids, assembles a response's
// conversation context, and records each turn.
//
// A response id is `<sessionId>.<n>`. Each session row carries one record per
// response in Session.Responses, and record n's EndSeq is the highest
// transcript sequence number assigned to turn n's entries. Every turn of a
// session appends to that session's own transcript, so a response's
// conversation context is the session's transcript up to and including the
// response's EndSeq, preceded by the context of the response the session's
// first turn continued from (ContinuationParentID). Bounding each hop by
// EndSeq rather than by an entry count keeps entries that a REST delivery
// appended between two turns inside the later turn's prefix and out of the
// earlier one's.
//
// The response records live on the session row and the entries in the
// session's transcript, so the §12.8 erasure of the session covers both and
// no adapter-specific erasure handling exists.
// spec: §15 (Open Responses conversation sessions); §15.1 session
// transcript; §12.8 erasure.
type continuity struct {
	sessions    sessionstore.Store
	transcripts transcriptstore.Store
	// primaryReadTimeout bounds the primary read of the head row; zero
	// selects sessionstore.DefaultPrimaryReadTimeout.
	primaryReadTimeout time.Duration
}

// sessionRead reads one tenant-scoped session row.
type sessionRead func(ctx context.Context, tenantID, id string) (sessionstore.Session, error)

// readPrimary reads the row from the Postgres primary. A response the gateway
// returned has its completion write on the primary already, while the §12.3
// read pool may still lag that write, so the head of a resolution reads here.
// spec: §15 (Open Responses conversation sessions); §12.3 (Postgres HA
// Requirements).
func (c *continuity) readPrimary(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	return sessionstore.ReadPrimary(ctx, c.sessions, c.primaryReadTimeout, tenantID, id)
}

// resolve reads the session a response id names from the primary and
// returns the row with the response's ordinal. It returns
// errContinuationNotFound for an unparsable id, a session the tenant cannot
// read, an ordinal past the newest response, or a response that has not
// completed. Any other store error is returned wrapped. It is the first hop of
// rehydrate, the resolution a continuation performs, and the read behind
// GET /v1/responses/{id}. It reads the primary because each of these callers
// turns a miss into the fail-closed 404, and a client that names a response
// the gateway has just returned must not meet a §12.3 read replica that has
// not yet applied the response's completion write.
// spec: §15 (Open Responses conversation sessions); §4.2 (session-store
// tenant isolation); §12.3 (Postgres HA Requirements).
func (c *continuity) resolve(ctx context.Context, tenantID, responseID string) (sessionstore.Session, int, error) {
	return c.resolveWith(ctx, c.readPrimary, tenantID, responseID)
}

// resolveAncestor resolves a lineage hop past the head through the store's
// Get, which may read the §12.3 read pool. The replica applies the WAL in
// order, so an ancestor's completion write is visible wherever its
// descendant's head row was.
// spec: §15 (Open Responses conversation sessions); §12.3 (Postgres HA
// Requirements).
func (c *continuity) resolveAncestor(ctx context.Context, tenantID, responseID string) (sessionstore.Session, int, error) {
	return c.resolveWith(ctx, c.sessions.Get, tenantID, responseID)
}

// resolveWith parses responseID, reads its session through read, and checks
// that the ordinal names a completed response.
// spec: §15 (Open Responses conversation sessions); §4.2 (session-store
// tenant isolation).
func (c *continuity) resolveWith(ctx context.Context, read sessionRead, tenantID, responseID string) (sessionstore.Session, int, error) {
	sessionID, n, ok := parseResponseID(responseID)
	if !ok {
		return sessionstore.Session{}, 0, errContinuationNotFound
	}
	row, err := read(ctx, tenantID, sessionID)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			return sessionstore.Session{}, 0, errContinuationNotFound
		}
		return sessionstore.Session{}, 0, fmt.Errorf("resolve response %s: %w", responseID, err)
	}
	if n > len(row.Responses) || !row.Responses[n-1].Completed {
		return sessionstore.Session{}, 0, errContinuationNotFound
	}
	return row, n, nil
}

// rehydrate returns the conversation context of responseID as leading
// executor.Message values in chronological order (first session first).
//
// The first hop reads the primary and fails closed: a response id resolve
// rejects returns errContinuationNotFound. Later hops resolve through
// resolveAncestor. Each hop reads its session's transcript entries
// with Seq at or below the response's EndSeq, then continues at the
// session's ContinuationParentID. An empty ContinuationParentID ends the
// walk. A later hop that resolve rejects (an erased session, an unparsable
// parent id, an out-of-range or not-completed ordinal) stops the walk at
// that gap and keeps what was collected. A hop whose transcript is empty or
// erased contributes nothing and the walk continues. The visited guard is
// keyed by session id, so a lineage that returns to a session already walked
// ends there.
// spec: §15 (Open Responses conversation sessions); §4.2 (session-store
// tenant isolation).
func (c *continuity) rehydrate(ctx context.Context, tenantID, responseID string) ([]executor.Message, error) {
	row, n, err := c.resolve(ctx, tenantID, responseID)
	if err != nil {
		return nil, err
	}
	// perHop[i] holds one session's prefix, collected newest-first.
	var perHop [][]executor.Message
	visited := map[string]bool{}
	for !visited[row.ID] {
		visited[row.ID] = true
		msgs, err := c.prefix(ctx, tenantID, row.ID, row.Responses[n-1].EndSeq)
		if err != nil {
			return nil, err
		}
		perHop = append(perHop, msgs)
		if row.ContinuationParentID == "" {
			break
		}
		row, n, err = c.resolveAncestor(ctx, tenantID, row.ContinuationParentID)
		if errors.Is(err, errContinuationNotFound) {
			break // a gap in the lineage ends the walk.
		}
		if err != nil {
			return nil, err
		}
	}
	var out []executor.Message
	for i := len(perHop) - 1; i >= 0; i-- {
		out = append(out, perHop[i]...)
	}
	return out, nil
}

// prefix returns sessionID's transcript entries with Seq at or below endSeq.
// A session with no transcript, or a handler built without a transcript
// store, contributes no messages.
// spec: §15 (Open Responses conversation sessions).
func (c *continuity) prefix(ctx context.Context, tenantID, sessionID string, endSeq uint64) ([]executor.Message, error) {
	if c.transcripts == nil {
		return nil, nil
	}
	entries, err := c.transcripts.Get(ctx, tenantID, sessionID)
	if err != nil {
		if errors.Is(err, transcriptstore.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("read transcript %s: %w", sessionID, err)
	}
	msgs := make([]executor.Message, 0, len(entries))
	for _, e := range entries {
		if e.Seq > endSeq {
			break
		}
		msgs = append(msgs, executor.Message{Role: e.Role, Content: e.Content})
	}
	return msgs, nil
}

// record appends this turn to its own session's transcript, the new inbound
// input by role followed by the assistant text output, and returns the Seq
// Append assigned to the last entry: the response's end sequence. It never
// copies the conversation context forward. The record is required: an error
// is returned so the turn fails rather than completing a response with no
// transcript entries, which would break every later cold continuation
// through it. A turn with no entries, or a handler built without a
// transcript store, records nothing and returns 0.
// spec: §15 (Open Responses conversation sessions); §15.1 session
// transcript.
func (c *continuity) record(ctx context.Context, tenantID, sessionID string,
	in []executor.Message, out []executor.MessagePart,
) (uint64, error) {
	if c.transcripts == nil {
		return 0, nil
	}
	entries := make([]transcriptstore.Entry, 0, len(in)+len(out))
	for _, m := range in {
		entries = append(entries, transcriptstore.Entry{Role: m.Role, Content: m.Content})
	}
	for _, p := range out {
		if p.Type == "text" {
			entries = append(entries, transcriptstore.Entry{Role: "assistant", Content: p.Text})
		}
	}
	if len(entries) == 0 {
		return 0, nil
	}
	endSeq, err := c.transcripts.Append(ctx, tenantID, sessionID, entries...)
	if err != nil {
		return 0, fmt.Errorf("record turn on session %s: %w", sessionID, err)
	}
	return endSeq, nil
}
