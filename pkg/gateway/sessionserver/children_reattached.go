// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// reattachedChild is the §7.1 ReattachedChild schema carried in the
// children_reattached event.
type reattachedChild struct {
	SessionID         string          `json:"session_id"`
	State             string          `json:"state"`
	PendingRequestID  string          `json:"pending_request_id,omitempty"`
	Result            json.RawMessage `json:"result,omitempty"`
	DelegationLeaseID string          `json:"delegation_lease_id"`
}

// emitChildrenReattached publishes the §7.1 / §8.10 children_reattached
// event on a resumed parent's event stream. Per §7.1 the event fires
// only when the parent has one or more active (non-terminal) children;
// it is a no-op when the parent has no children or every child has
// already settled. The children array carries every child — a settled
// child includes its §8.8 result. Best-effort: a failure to enumerate
// or publish never fails the resume.
//
// A non-terminal child carries `pending_request_id` when an outstanding
// `lenny/request_input` or §6/§9.2 pending interaction exists for the
// child — the parent needs the id to answer via `lenny/send_message`
// (inReplyTo) or the §15.1 interaction endpoints. spec: §7.2
// (ReattachedChild.pending_request_id). F-7.2.16.
func (s *Server) emitChildrenReattached(ctx context.Context, tenantID, parentID string) {
	if s.events == nil {
		return
	}
	all, err := s.store.List(ctx, tenantID, sessionstore.ListFilter{})
	if err != nil {
		return
	}
	// Collect this parent's direct children and locate the parent row so
	// the §8.10 archive can be replayed under the tree root.
	childRows := make([]sessionstore.Session, 0)
	var parent sessionstore.Session
	parentFound := false
	for _, row := range all {
		if row.ID == parentID {
			parent = row
			parentFound = true
		}
		if row.ParentSessionID == parentID {
			childRows = append(childRows, row)
		}
	}

	// spec: §8.10 — the resumed parent sees already-settled
	// children "in original-settlement order". The archive's Replay
	// returns nodes sorted by (settled_at, completion_seq), so a settled
	// child's position in the reattach payload reflects when it actually
	// reached a terminal state, not the store's row order. F-8.10.4.
	children := make([]reattachedChild, 0, len(childRows))
	emitted := make(map[string]bool, len(childRows))
	if s.treeArchive != nil && parentFound {
		root := s.treeRoot(ctx, parent)
		if nodes, rerr := s.treeArchive.Replay(ctx, tenantID, root); rerr == nil {
			for _, n := range nodes {
				if n.ParentSessionID != parentID || emitted[n.NodeSessionID] {
					continue
				}
				emitted[n.NodeSessionID] = true
				children = append(children, reattachedChild{
					SessionID: n.NodeSessionID,
					State:     n.State,
					// spec: §8.8 — replay the same §8.8
					// TaskResult body the archive captured at settle time so
					// the reattach payload matches the archived result. F-8.8.2.
					Result:            json.RawMessage(n.Result),
					DelegationLeaseID: n.NodeSessionID,
				})
			}
		}
	}

	// Append children the archive did not carry, in a deterministic
	// (session-id) order: terminal children when archiving is disabled or
	// a node was not yet written, then the still-active children that the
	// resumed parent re-awaits via lenny/await_children.
	sort.Slice(childRows, func(i, j int) bool { return childRows[i].ID < childRows[j].ID })
	anyActive := false
	for _, row := range childRows {
		if emitted[row.ID] {
			continue
		}
		child := reattachedChild{
			SessionID: row.ID,
			State:     string(row.State),
			// v1 has no separate delegation-lease id; the child session
			// id is the parent's correlation handle.
			DelegationLeaseID: row.ID,
		}
		if session.IsTerminal(row.State) {
			child.Result, _ = json.Marshal(s.materializeTaskResult(ctx, row, 0))
		} else {
			anyActive = true
			// spec: §7.2 — populate the pending_request_id when
			// the child has an outstanding request directed at the
			// parent. lenny/request_input wins over the interaction-store
			// entries because it carries a structured reply contract; an
			// interaction (tool-use / elicitation) is the fallback when
			// the child raised an approval. F-7.2.16.
			child.PendingRequestID = s.lookupPendingRequest(ctx, tenantID, row.ID)
		}
		children = append(children, child)
	}
	if !anyActive {
		return
	}
	data, _ := json.Marshal(struct {
		Children []reattachedChild `json:"children"`
	}{Children: children})
	s.events.PublishForTenant(tenantID, parentID, "children_reattached", string(data), s.clock())
}

// lookupPendingRequest returns the pending request id for a child
// session in `input_required`-equivalent state, or "" when none is
// outstanding. It prefers `lenny/request_input` registrations (the
// §8.5 structured-reply contract) and falls back to a §6/§9.2 pending
// interaction (tool-use approval / elicitation). When both sources are
// unwired the function is a no-op. spec: §7.2. F-7.2.16.
func (s *Server) lookupPendingRequest(ctx context.Context, tenantID, sessionID string) string {
	if s.inputWaits != nil {
		if ids := s.inputWaits.PendingForSession(sessionID); len(ids) > 0 {
			sort.Strings(ids)
			return ids[0]
		}
	}
	if s.interactions != nil {
		pending, err := s.interactions.ListPending(ctx, tenantID, sessionID)
		if err == nil && len(pending) > 0 {
			return pending[0].ID
		}
	}
	return ""
}

// buildHandoffChildrenReattached builds the §10.4
// synthesized `children_reattached` payload for a coordinator-handoff
// reattach. The §10.4 predicate fires the frame "if the session is a
// parent with archived children whose completion_seq is greater than the
// client's resumeFromSeq" — the child completions the client missed
// while the handoff was in flight. Those archived nodes are streamed in
// §8.10 original-settlement order, and the parent's still-active children
// are appended so the resumed parent re-establishes its await set (the
// §7.2 STR-007 symmetry with the single-coordinator resume path). Returns
// ok=false when the session is not a parent, has no missed completions,
// and has no active children. spec: §10.4; §7.2.
// F-7.2.13, F-10.4.2.
func (s *Server) buildHandoffChildrenReattached(ctx context.Context, tenantID, parentID string, afterSeq uint64) ([]byte, bool) {
	all, err := s.store.List(ctx, tenantID, sessionstore.ListFilter{})
	if err != nil {
		return nil, false
	}
	childRows := make([]sessionstore.Session, 0)
	var parent sessionstore.Session
	parentFound := false
	for _, row := range all {
		if row.ID == parentID {
			parent = row
			parentFound = true
		}
		if row.ParentSessionID == parentID {
			childRows = append(childRows, row)
		}
	}

	children := make([]reattachedChild, 0, len(childRows))
	emitted := make(map[string]bool, len(childRows))
	missedCompletion := false
	if s.treeArchive != nil && parentFound {
		root := s.treeRoot(ctx, parent)
		if nodes, rerr := s.treeArchive.Replay(ctx, tenantID, root); rerr == nil {
			for _, n := range nodes {
				if n.ParentSessionID != parentID || emitted[n.NodeSessionID] {
					continue
				}
				// spec: §10.4 — only archived children whose
				// completion the client has not yet observed (CompletionSeq
				// strictly above the resume cursor). A v1 archive writer
				// that does not stamp a per-session sequence leaves
				// CompletionSeq at 0, which never exceeds a non-zero cursor,
				// so those nodes fall to the active-children pass below.
				if n.CompletionSeq <= 0 || uint64(n.CompletionSeq) <= afterSeq {
					continue
				}
				emitted[n.NodeSessionID] = true
				missedCompletion = true
				children = append(children, reattachedChild{
					SessionID:         n.NodeSessionID,
					State:             n.State,
					Result:            json.RawMessage(n.Result),
					DelegationLeaseID: n.NodeSessionID,
				})
			}
		}
	}

	sort.Slice(childRows, func(i, j int) bool { return childRows[i].ID < childRows[j].ID })
	anyActive := false
	for _, row := range childRows {
		if emitted[row.ID] || session.IsTerminal(row.State) {
			continue
		}
		anyActive = true
		children = append(children, reattachedChild{
			SessionID:         row.ID,
			State:             string(row.State),
			DelegationLeaseID: row.ID,
			// spec: §7.2 — surface the pending request id so the
			// resumed parent can answer a child blocked on
			// lenny/request_input or a §6/§9.2 interaction.
			PendingRequestID: s.lookupPendingRequest(ctx, tenantID, row.ID),
		})
	}
	if !missedCompletion && !anyActive {
		return nil, false
	}
	data, err := json.Marshal(struct {
		Children []reattachedChild `json:"children"`
	}{Children: children})
	if err != nil {
		return nil, false
	}
	return data, true
}
