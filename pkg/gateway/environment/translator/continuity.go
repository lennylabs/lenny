// SPDX-License-Identifier: MIT

package translator

import (
	"context"
	"errors"

	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// errContinuationNotFound marks an unknown or cross-tenant
// previous_response_id. The handler maps it to a native OpenAI 404 with no
// rehydration and no dispatch (fail-closed continuation resolution). A
// cross-tenant id is indistinguishable from an unknown id because the
// tenant-scoped sessionstore.Get returns ErrNotFound for both under the §4.2
// session-store isolation model.
// spec: §15 built-in adapter single-shot compute model; §4.2 session-store
// tenant isolation.
var errContinuationNotFound = errors.New("continuity: previous_response_id not found")

// continuity resolves an OpenResponsesAdapter previous_response_id to its
// prior conversation and records each response's own turn.
//
// Storage is chain-walk single-turn buckets: each response's per-session
// transcript bucket holds only that response's own turn (the inbound
// normalized input plus the assistant text output). On a continuation the
// helper walks the ContinuationParentID chain from the referenced response
// back to the chain root, reading each ancestor's single-turn bucket with one
// tenant-scoped transcriptstore.Get per hop, and assembles the turns in
// chronological order (root first). A turn is therefore stored exactly once
// (O(N) aggregate storage, O(1) write per turn, single source of truth),
// rather than copied forward into every descendant bucket.
//
// Because each response's bucket is an ordinary single-turn per-session
// transcript, the §12.8 erasure orchestrator already covers it by walking the
// user's sessions and calling DeleteBySession; erasing or redacting a turn
// touches the one bucket that owns it rather than every descendant copy, so no
// adapter-specific erasure handling is added.
// spec: §15 built-in adapter single-shot compute model; §15.1 session
// transcript; §12.8 erasure.
type continuity struct {
	sessions    sessionstore.Store
	transcripts transcriptstore.Store
}

// rehydrate resolves prevID fail-closed, then walks the ContinuationParentID
// chain from prevID back to the chain root and returns the prior conversation
// as leading executor.Message values in chronological order (root first).
//
// An unknown or cross-tenant prevID (sessionstore.ErrNotFound on the
// referenced id itself) returns errContinuationNotFound so the handler can
// fail closed. A missing mid-chain ancestor (sessionstore.ErrNotFound on a
// non-referenced hop, e.g. an erased ancestor) ends the walk at the gap,
// keeping the newer turns already collected. A hop whose transcript bucket is
// empty (transcriptstore.ErrNotFound) contributes no messages and the walk
// continues to its ancestors. A visited set guards against a pointer cycle.
// spec: §15 built-in adapter single-shot compute model; §4.2 session-store
// tenant isolation.
func (c *continuity) rehydrate(ctx context.Context, tenantID, prevID string) ([]executor.Message, error) {
	if c.transcripts == nil || c.sessions == nil {
		// No continuity backing (the in-memory and unit path): treat as empty
		// prior history rather than a fail-closed lookup.
		return nil, nil
	}
	// perTurn[i] holds the messages of one response, collected newest-first as
	// the walk climbs the chain toward the root.
	var perTurn [][]executor.Message
	visited := map[string]bool{}
	for id := prevID; id != ""; {
		if visited[id] {
			break // cycle guard: a self- or loop-referencing chain ends here.
		}
		visited[id] = true

		row, err := c.sessions.Get(ctx, tenantID, id)
		if err != nil {
			if errors.Is(err, sessionstore.ErrNotFound) {
				if id == prevID {
					// The referenced response is unknown or cross-tenant: fail
					// closed. spec: §15 (fail-closed continuation resolution).
					return nil, errContinuationNotFound
				}
				// A missing mid-chain ancestor (e.g. erased) ends the walk;
				// the newer turns already collected still rehydrate.
				break
			}
			return nil, err
		}

		entries, err := c.transcripts.Get(ctx, tenantID, id)
		if err != nil && !errors.Is(err, transcriptstore.ErrNotFound) {
			return nil, err
		}
		// transcriptstore.ErrNotFound leaves entries nil: an empty hop
		// contributes nothing and the walk continues to its ancestors.
		msgs := make([]executor.Message, 0, len(entries))
		for _, e := range entries {
			msgs = append(msgs, executor.Message{Role: e.Role, Content: e.Content})
		}
		perTurn = append(perTurn, msgs)

		id = row.ContinuationParentID
	}

	// Reverse to chronological order (root first) and flatten.
	var out []executor.Message
	for i := len(perTurn) - 1; i >= 0; i-- {
		out = append(out, perTurn[i]...)
	}
	return out, nil
}

// record writes this response's own turn on its own session id: the new
// inbound input by role, then the assistant text output. It never copies the
// prior conversation forward; the ancestors' turns stay in their own buckets.
// The write is best-effort, matching the canonical §15.1 transcript-write path
// so a transcript failure does not fail the response.
// spec: §15 built-in adapter single-shot compute model; §15.1 session
// transcript best-effort write.
func (c *continuity) record(ctx context.Context, tenantID, sessionID string,
	in []executor.Message, out []executor.MessagePart,
) {
	if c.transcripts == nil {
		return
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
		return
	}
	_ = c.transcripts.Append(ctx, tenantID, sessionID, entries...)
}
