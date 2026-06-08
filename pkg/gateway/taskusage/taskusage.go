// SPDX-License-Identifier: MIT

// Package taskusage assembles the §8.8 TaskResult.usage and
// TaskResult.treeUsage rollups from the per-session token accumulator
// (pkg/gateway/sessionusage), the session row, and the §8.10 tree archive
// (pkg/gateway/treearchive).
//
// usage is the settling task's own consumption: token counts from the
// session's accumulator, plus wallClockSeconds / podMinutes /
// credentialLeaseMinutes derived from the session row. In v1 (session
// execution mode) a pod and a credential lease are each bound to a session
// for its whole lifetime, so podMinutes and credentialLeaseMinutes derive
// from the session's create-to-terminal wall-clock span; the dedicated
// timers land with the task/concurrent execution modes.
//
// treeUsage is the sum of a task's own usage plus every descendant's. Per
// §8.8 line 917 it is null until every descendant has settled, so the
// builder walks the subtree (live rows ∪ archived nodes) and returns nil
// when any descendant is still non-terminal.
//
// spec: §8.8 lines 897-917.
package taskusage

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionusage"
	"github.com/lennylabs/lenny/pkg/gateway/treearchive"
	"github.com/lennylabs/lenny/pkg/task"
)

// Builder assembles §8.8 usage / treeUsage. A nil *Builder is valid: its
// methods return nil rollups, so a gateway wired without the per-session
// accumulator surfaces the pre-metering behaviour (usage / treeUsage
// absent) rather than panicking.
type Builder struct {
	sessions sessionstore.Store
	tokens   sessionusage.Store
	archive  treearchive.Store
	now      func() time.Time
}

// New returns a Builder. Any nil dependency disables the corresponding
// rollup: without a token store usage carries zero tokens; without a
// session store or archive treeUsage is not computed (nil). now selects
// the wall-clock source for a not-yet-terminal session; a nil now uses
// time.Now.
func New(sessions sessionstore.Store, tokens sessionusage.Store, archive treearchive.Store, now func() time.Time) *Builder {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Builder{sessions: sessions, tokens: tokens, archive: archive, now: now}
}

// Usage builds the §8.8 per-task usage block for sess. It reads the
// session's accumulated tokens and derives the time dimensions from the
// row. A nil Builder or a nil token store yields a usage with zero tokens
// and the derived time dimensions.
// spec: §8.8 lines 897-903.
func (b *Builder) Usage(ctx context.Context, sess sessionstore.Session) *task.Usage {
	if b == nil {
		return nil
	}
	var tok sessionusage.Tokens
	if b.tokens != nil {
		tok, _ = b.tokens.Get(ctx, sess.TenantID, sess.ID)
	}
	return b.usageFrom(sess, tok)
}

// usageFrom assembles a usage block from a session row and its token
// totals. wallClockSeconds is the create-to-terminal span (or
// create-to-now for a not-yet-terminal session); podMinutes and
// credentialLeaseMinutes derive from it when a pod was bound, since in
// session mode the pod and credential lease span the session lifetime.
func (b *Builder) usageFrom(sess sessionstore.Session, tok sessionusage.Tokens) *task.Usage {
	wall := b.wallClockSeconds(sess)
	u := &task.Usage{
		InputTokens:      tok.Input,
		OutputTokens:     tok.Output,
		WallClockSeconds: wall,
	}
	if sess.PodAssignment != "" {
		u.PodMinutes = wall / 60.0
		u.CredentialLeaseMinutes = wall / 60.0
	}
	return u
}

// wallClockSeconds is the session's create-to-terminal span. UpdatedAt is
// the terminal-transition timestamp for a terminal row; a not-yet-terminal
// row measures to now. A non-positive span (clock skew, or a row whose
// UpdatedAt predates CreatedAt) yields 0.
func (b *Builder) wallClockSeconds(sess sessionstore.Session) float64 {
	if sess.CreatedAt.IsZero() {
		return 0
	}
	end := sess.UpdatedAt
	if !session.IsTerminal(sess.State) || end.Before(sess.CreatedAt) {
		end = b.now()
	}
	secs := end.Sub(sess.CreatedAt).Seconds()
	if secs < 0 {
		return 0
	}
	return secs
}

// TreeUsage builds the §8.8 treeUsage rollup for the subtree rooted at
// sess: the sum of sess's usage plus every descendant's, with totalTasks
// the node count. It returns nil when sess is non-terminal, when any
// descendant is non-terminal (§8.8 line 917), or when the Builder lacks
// the session store needed to enumerate the subtree.
//
// rootUsage is sess's own freshly-built usage (the same value the caller
// puts on TaskResult.usage); passing it avoids a second token lookup and
// keeps the leaf node's usage and treeUsage consistent.
// spec: §8.8 lines 904-917.
func (b *Builder) TreeUsage(ctx context.Context, sess sessionstore.Session, rootUsage *task.Usage) *task.TreeUsage {
	if b == nil || b.sessions == nil || rootUsage == nil {
		return nil
	}
	if !session.IsTerminal(sess.State) {
		return nil
	}
	rootID := sess.RootSessionID
	if rootID == "" {
		rootID = sess.ID
	}

	live, err := b.sessions.ListByRoot(ctx, sess.TenantID, rootID)
	if err != nil {
		return nil
	}
	var archived []treearchive.ArchivedNode
	if b.archive != nil {
		archived, _ = b.archive.Replay(ctx, sess.TenantID, rootID)
	}

	liveByID := make(map[string]sessionstore.Session, len(live))
	for _, s := range live {
		liveByID[s.ID] = s
	}
	archByID := make(map[string]treearchive.ArchivedNode, len(archived))
	for _, n := range archived {
		archByID[n.NodeSessionID] = n
	}

	// children[parentID] holds every node id whose parent is parentID,
	// drawn from the union of live rows and archived nodes so a settled,
	// reclaimed descendant (gone from live rows) still appears.
	children := map[string][]string{}
	addEdge := func(child, parent string) {
		if parent == "" || child == "" {
			return
		}
		for _, existing := range children[parent] {
			if existing == child {
				return
			}
		}
		children[parent] = append(children[parent], child)
	}
	for _, s := range live {
		addEdge(s.ID, s.ParentSessionID)
	}
	for _, n := range archived {
		addEdge(n.NodeSessionID, n.ParentSessionID)
	}

	// DFS the subtree rooted at sess, accumulating each node's own usage.
	// A non-terminal descendant collapses the whole rollup to null.
	var sum task.TreeUsage
	seen := map[string]bool{}
	var walk func(id string, isRoot bool) bool
	walk = func(id string, isRoot bool) bool {
		if seen[id] {
			return true
		}
		seen[id] = true

		u, settled := b.nodeUsage(ctx, id, isRoot, sess, rootUsage, liveByID, archByID)
		if !settled {
			return false
		}
		sum.InputTokens += u.InputTokens
		sum.OutputTokens += u.OutputTokens
		sum.WallClockSeconds += u.WallClockSeconds
		sum.PodMinutes += u.PodMinutes
		sum.CredentialLeaseMinutes += u.CredentialLeaseMinutes
		sum.TotalTasks++

		for _, child := range children[id] {
			if !walk(child, false) {
				return false
			}
		}
		return true
	}
	if !walk(sess.ID, true) {
		return nil
	}
	return &sum
}

// nodeUsage resolves one subtree node's own usage and whether it has
// settled. The root node uses the caller's freshly-built rootUsage (it is
// terminal by TreeUsage's precondition). A non-root node prefers its live
// row (rebuilt usage) and falls back to its archived TaskResult.usage when
// the row was reclaimed; a non-root node that is neither archived nor a
// terminal live row is unsettled, which collapses the rollup.
func (b *Builder) nodeUsage(
	ctx context.Context,
	id string,
	isRoot bool,
	root sessionstore.Session,
	rootUsage *task.Usage,
	liveByID map[string]sessionstore.Session,
	archByID map[string]treearchive.ArchivedNode,
) (task.Usage, bool) {
	if isRoot {
		return *rootUsage, true
	}
	if row, ok := liveByID[id]; ok {
		if !session.IsTerminal(row.State) {
			return task.Usage{}, false
		}
		var tok sessionusage.Tokens
		if b.tokens != nil {
			tok, _ = b.tokens.Get(ctx, root.TenantID, id)
		}
		return *b.usageFrom(row, tok), true
	}
	if n, ok := archByID[id]; ok {
		return archivedUsage(n), true
	}
	return task.Usage{}, false
}

// archivedUsage extracts the usage block a node baked into its archived
// §8.8 TaskResult when it settled. A node archived before per-session
// metering existed (or with a malformed body) contributes zero usage but
// still counts as settled, so the rollup degrades to a token undercount
// rather than a perpetual null.
func archivedUsage(n treearchive.ArchivedNode) task.Usage {
	if n.Result == "" {
		return task.Usage{}
	}
	var res task.Result
	if err := json.Unmarshal([]byte(n.Result), &res); err != nil || res.Usage == nil {
		return task.Usage{}
	}
	return *res.Usage
}
