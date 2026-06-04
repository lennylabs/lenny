// SPDX-License-Identifier: MIT

// Package deadlock implements the §8.8 subtree deadlock detector: the
// heuristic all-tasks-blocked sweep that identifies deadlocked
// delegation subtrees, surfaces a `deadlock_detected` event to the
// subtree root's lenny/await_children stream, and fails the deepest
// blocked tasks with DEADLOCK_TIMEOUT when the root does not break the
// deadlock within `maxDeadlockWaitSeconds`.
//
// The detector is a per-subtree heuristic, not a true cycle-detection
// algorithm. Cross-subtree circular `lenny/send_message` waits and
// children blocked on external resources are documented false negatives
// (spec: §8.8 line 981); they are bounded by `maxRequestInputWaitSeconds`
// rather than by this detector.
//
// spec: §8.8 lines 981-997. F-8.8.6.
package deadlock

import (
	"sort"
	"time"

	session "github.com/lennylabs/lenny/pkg/api/v1/session"
)

// EventType is the §8.8 line 987 deadlock event discriminator.
const EventType = "deadlock_detected"

// DefaultMaxWait is the §8.8 line 981 `maxDeadlockWaitSeconds` default
// (120s) used when a pool does not override it.
const DefaultMaxWait = 120 * time.Second

// PendingInput is one pending lenny/request_input round on a task: the
// §8.8 requestId the parent answers with `inReplyTo` and the wall-clock
// instant the request began blocking. spec: §8.8 line 990. F-8.8.6.
type PendingInput struct {
	RequestID    string
	BlockedSince time.Time
}

// Node is one session's state in a delegation-tree snapshot the
// detector reasons over. spec: §8.8 line 981. F-8.8.6.
type Node struct {
	SessionID string
	TenantID  string
	State     session.State
	// AwaitingChildIDs are the children this session is currently
	// blocking on inside a live lenny/await_children call. Empty when the
	// session is not awaiting children.
	AwaitingChildIDs []string
	// PendingInputs are the session's pending lenny/request_input rounds
	// (the input_required sub-state). Empty when the session is not
	// blocked on input.
	PendingInputs []PendingInput
}

// Snapshot is the set of delegation nodes the detector evaluates in one
// sweep, keyed by session id. A node absent from the map is treated as
// having made progress (terminal, running, or unknown) so its awaiting
// ancestor is not considered deadlocked on its account.
type Snapshot struct {
	Nodes map[string]Node
}

// BlockedRequest is one entry in a deadlock event's `blockedRequests`
// array. spec: §8.8 line 990.
type BlockedRequest struct {
	RequestID    string    `json:"requestId"`
	TaskID       string    `json:"taskId"`
	BlockedSince time.Time `json:"blockedSince"`
}

// Event is the §8.8 lines 985-994 deadlock_detected payload surfaced on
// the root task's lenny/await_children stream.
type Event struct {
	Type                  string           `json:"type"`
	DeadlockedSubtreeRoot string           `json:"deadlockedSubtreeRoot"`
	BlockedRequests       []BlockedRequest `json:"blockedRequests"`
	DetectedAt            time.Time        `json:"detectedAt"`
	WillTimeoutAt         time.Time        `json:"willTimeoutAt"`
}

// Subtree is one detected deadlocked subtree: its root, the blocked
// requests across the whole subtree, and the deepest blocked task ids
// (the §8.8 line 981 "deepest blocked tasks" the DEADLOCK_TIMEOUT edge
// fails when the deadlock is not resolved in time).
type Subtree struct {
	Root            string
	TenantID        string
	BlockedRequests []BlockedRequest
	DeepestTasks    []string
}

// Detect applies the §8.8 line 981 heuristic to snap and returns one
// Subtree per maximal deadlocked subtree.
//
// A node is "blocked" when it is in input_required (has a pending
// request_input) or when it is awaiting children where every non-terminal
// child is itself blocked and at least one is. Terminal (settled) awaited
// children are satisfied and skipped — they do not prevent a deadlock,
// and an actively-running non-blocked child does (it can still settle on
// its own, so the parent may yet make progress). A deadlocked subtree
// root is an awaiting node that is blocked and is not itself an awaited
// child of another blocked awaiting node, so nested deadlocks report only
// the topmost root. A subtree with no pending request_input anywhere is
// not actionable and is dropped — the root has nothing to resolve.
//
// spec: §8.8 line 981. F-8.8.6.
func Detect(snap Snapshot) []Subtree {
	nodes := snap.Nodes
	memo := map[string]bool{}
	visiting := map[string]bool{}
	var blocked func(id string) bool
	blocked = func(id string) bool {
		if v, ok := memo[id]; ok {
			return v
		}
		if visiting[id] {
			// A cycle in the await graph (out of scope per §8.8); treat as
			// not blocked so the DFS terminates rather than recursing
			// forever on a cross-subtree circular wait.
			return false
		}
		n, ok := nodes[id]
		if !ok {
			// Unknown node — treat as progress is possible (not blocked).
			return false
		}
		visiting[id] = true
		res := false
		switch {
		case len(n.PendingInputs) > 0:
			res = true
		case len(n.AwaitingChildIDs) > 0:
			anyBlocked := false
			obstructed := false
			for _, c := range n.AwaitingChildIDs {
				if isTerminal(nodes, c) {
					continue // settled child — satisfied, not an obstacle
				}
				if blocked(c) {
					anyBlocked = true
					continue
				}
				// A non-terminal, non-blocked child is actively making
				// progress; the parent may yet settle on it.
				obstructed = true
				break
			}
			res = anyBlocked && !obstructed
		}
		visiting[id] = false
		memo[id] = res
		return res
	}

	// A blocked awaiting node contained in another blocked awaiting node's
	// subtree is not a separate root.
	containedByBlocked := map[string]bool{}
	for id, n := range nodes {
		if len(n.AwaitingChildIDs) == 0 || !blocked(id) {
			continue
		}
		for _, c := range n.AwaitingChildIDs {
			if blocked(c) {
				containedByBlocked[c] = true
			}
		}
	}

	var roots []string
	for id, n := range nodes {
		if len(n.AwaitingChildIDs) == 0 {
			continue
		}
		if blocked(id) && !containedByBlocked[id] {
			roots = append(roots, id)
		}
	}
	sort.Strings(roots)

	var out []Subtree
	for _, root := range roots {
		st := buildSubtree(nodes, root, blocked)
		if len(st.BlockedRequests) == 0 {
			continue
		}
		out = append(out, st)
	}
	return out
}

// isTerminal reports whether the awaited child c has settled. A child
// absent from the snapshot is treated as terminal: the await handler
// authorized it as a real child, so a missing live row means it settled
// and was reclaimed. A settled child is satisfied and does not keep its
// parent deadlocked.
func isTerminal(nodes map[string]Node, c string) bool {
	n, ok := nodes[c]
	if !ok {
		return true
	}
	return session.IsTerminal(n.State)
}

// buildSubtree walks the deadlocked subtree rooted at root over the
// awaited-children edges (only following blocked children), collecting
// every pending request_input and the deepest blocked task ids.
func buildSubtree(nodes map[string]Node, root string, blocked func(string) bool) Subtree {
	st := Subtree{Root: root, TenantID: nodes[root].TenantID}
	depth := map[string]int{root: 0}
	order := []string{root}
	seen := map[string]bool{root: true}
	type reqAt struct {
		req   BlockedRequest
		depth int
	}
	var reqs []reqAt
	maxInputDepth := -1
	for i := 0; i < len(order); i++ {
		id := order[i]
		n := nodes[id]
		d := depth[id]
		for _, pi := range n.PendingInputs {
			reqs = append(reqs, reqAt{
				req:   BlockedRequest{RequestID: pi.RequestID, TaskID: id, BlockedSince: pi.BlockedSince},
				depth: d,
			})
			if d > maxInputDepth {
				maxInputDepth = d
			}
		}
		for _, c := range n.AwaitingChildIDs {
			if seen[c] || isTerminal(nodes, c) || !blocked(c) {
				continue
			}
			seen[c] = true
			depth[c] = d + 1
			order = append(order, c)
		}
	}
	for _, r := range reqs {
		st.BlockedRequests = append(st.BlockedRequests, r.req)
	}
	sort.Slice(st.BlockedRequests, func(i, j int) bool {
		if st.BlockedRequests[i].TaskID != st.BlockedRequests[j].TaskID {
			return st.BlockedRequests[i].TaskID < st.BlockedRequests[j].TaskID
		}
		return st.BlockedRequests[i].RequestID < st.BlockedRequests[j].RequestID
	})
	deepSet := map[string]bool{}
	for _, r := range reqs {
		if r.depth == maxInputDepth {
			deepSet[r.req.TaskID] = true
		}
	}
	for id := range deepSet {
		st.DeepestTasks = append(st.DeepestTasks, id)
	}
	sort.Strings(st.DeepestTasks)
	return st
}
