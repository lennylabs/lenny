// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/sessionrecord"
)

// buildTaskRecord projects the §8.8 TaskRecord envelope for a session
// from the session row plus its recorded transcript. The §4.2 line 157
// "task records and parent/child lineage" responsibility is realised in
// v1 as a projection over the session row and transcript rather than a
// separate persisted table: the durable substrate (the session row and
// the §15.1 transcript) already carries everything the envelope needs,
// so on-read projection avoids a second source of truth that could drift
// from the row.
//
// The transcript's §7.2 user/assistant/system roles map onto the §8.8
// caller/agent message roles (user → caller; assistant and system →
// agent). The terminal task state lands on the final agent turn per the
// §8.8 example. When a TaskUsage Builder is wired, usage carries the
// task's own consumption (available at any state) and treeUsage the
// subtree rollup (null until every descendant settles per §8.8 line 917).
//
// spec: §8.8 lines 806-823, 897-917; §4.2 line 157. F-8.8.3.
func (s *Server) buildTaskRecord(ctx context.Context, row sessionstore.Session) *sessionrecord.Record {
	if s.transcripts == nil {
		return nil
	}
	rec := &sessionrecord.Record{
		SchemaVersion: sessionrecord.SchemaVersion,
		TaskID:        row.ID,
		SessionID:     row.ID,
		State:         mcpStateForSession(row.State),
		Messages:      []sessionrecord.Message{},
	}
	entries, err := s.transcripts.Get(ctx, row.TenantID, row.ID)
	if err != nil {
		return rec
	}
	lastAgent := -1
	for _, e := range entries {
		role := sessionrecord.RoleAgent
		if e.Role == "user" {
			role = sessionrecord.RoleCaller
		}
		rec.Messages = append(rec.Messages, sessionrecord.Message{
			Role:  role,
			Parts: []sessionrecord.MessagePart{sessionrecord.TextPart(e.Content)},
		})
		if role == sessionrecord.RoleAgent {
			lastAgent = len(rec.Messages) - 1
		}
	}
	// spec: §8.8 line 815 — the agent message carries the task state; the
	// terminal state belongs on the final agent turn.
	if session.IsTerminal(row.State) && lastAgent >= 0 {
		rec.Messages[lastAgent].State = rec.State
	}
	// spec: §8.8 lines 897-917 — stamp the per-task usage and the subtree
	// rollup (null until every descendant settles) when the Builder is
	// wired. F-8.8.3.
	if s.taskUsage != nil {
		rec.Usage = s.taskUsage.Usage(ctx, row)
		rec.TreeUsage = s.taskUsage.TreeUsage(ctx, row, rec.Usage)
	}
	return rec
}
