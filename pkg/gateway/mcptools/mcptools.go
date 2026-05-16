// SPDX-License-Identifier: MIT

// Package mcptools wires the §8.5 platform MCP tool surface to the
// gateway's session + delegation services. It is the bridge between
// the transport-only pkg/gateway/mcp adapter and the concrete
// gateway operations, kept separate so the MCP adapter has no
// dependency on the session store.
//
// v1 registers the core §8.5 tools:
//
//   - `lenny/create_session`  — create a session.
//   - `lenny/send_message`    — deliver a message to a session.
//   - `lenny/get_task_tree`   — read the §8 delegation task tree.
//   - `lenny/cancel_child`    — cancel a child session and cascade.
//   - `lenny/delegate_task`   — spawn a child session (§8.2).
//
// Each handler runs the same validation as the equivalent REST
// endpoint so the REST and MCP surfaces stay in lockstep per the
// §15.2.1 consistency contract.
package mcptools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// Deps carries the gateway services the MCP tools dispatch to.
type Deps struct {
	// Store is the §4.2 session store.
	Store sessionstore.Store

	// Executor routes messages to runtimes.
	Executor executor.Executor

	// Delegation is the §8 delegation service. Optional — when nil,
	// the lenny/delegate_task tool is not registered.
	Delegation *delegation.Service

	// Clock + IDFunc match the session server's construction; pass
	// nil for production defaults.
	Clock  func() time.Time
	IDFunc func() string

	// TenantID is the tenant the MCP session operates within. The
	// MCP adapter is mounted per-tenant; v1 binds one tenant per
	// adapter instance.
	TenantID string
}

// Register installs the §8.5 tools onto the MCP server.
func Register(srv *mcp.Server, deps Deps) {
	clock := deps.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	idFn := deps.IDFunc
	if idFn == nil {
		idFn = randomSessionID
	}
	tenant := deps.TenantID
	if tenant == "" {
		tenant = "default"
	}

	srv.RegisterTool(mcp.Tool{
		Name:        "lenny/create_session",
		Description: "Create a new agent session against a runtime.",
		InputSchema: json.RawMessage(`{"type":"object","required":["runtimeRef"],"properties":{"runtimeRef":{"type":"string"},"userId":{"type":"string"}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		var in struct {
			RuntimeRef string `json:"runtimeRef"`
			UserID     string `json:"userId"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
		}
		if in.RuntimeRef == "" {
			return mcp.ToolResult{}, errors.New("runtimeRef is required")
		}
		now := clock()
		row := sessionstore.Session{
			ID:               idFn(),
			TenantID:         tenant,
			UserID:           in.UserID,
			RuntimeRef:       in.RuntimeRef,
			State:            session.StateRunning,
			IsolationProfile: isolation.Default(),
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		if err := deps.Store.Create(ctx, row); err != nil {
			return mcp.ToolResult{}, err
		}
		return textResult(fmt.Sprintf(`{"sessionId":%q,"state":%q}`, row.ID, row.State)), nil
	})

	srv.RegisterTool(mcp.Tool{
		Name:        "lenny/send_message",
		Description: "Deliver a message to a running session and return the response.",
		InputSchema: json.RawMessage(`{"type":"object","required":["sessionId","content"],"properties":{"sessionId":{"type":"string"},"content":{"type":"string"}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		var in struct {
			SessionID string `json:"sessionId"`
			Content   string `json:"content"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
		}
		row, err := deps.Store.Get(ctx, tenant, in.SessionID)
		if err != nil {
			return mcp.ToolResult{}, fmt.Errorf("session lookup: %w", err)
		}
		if session.IsTerminal(row.State) {
			return mcp.ToolResult{}, fmt.Errorf("session %s is terminal (%s)", in.SessionID, row.State)
		}
		if deps.Executor == nil {
			return mcp.ToolResult{}, errors.New("no executor configured")
		}
		out, err := deps.Executor.Send(ctx, row.ID, []executor.Message{
			{Role: "user", Content: in.Content},
		})
		if err != nil {
			return mcp.ToolResult{}, err
		}
		content := make([]mcp.ToolContent, 0, len(out))
		for _, p := range out {
			if p.Type == "text" {
				content = append(content, mcp.ToolContent{Type: "text", Text: p.Text})
			}
		}
		return mcp.ToolResult{Content: content}, nil
	})

	srv.RegisterTool(mcp.Tool{
		Name:        "lenny/get_task_tree",
		Description: "Return the §8 delegation task tree rooted at a session.",
		InputSchema: json.RawMessage(`{"type":"object","required":["sessionId"],"properties":{"sessionId":{"type":"string"}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		var in struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
		}
		root, err := deps.Store.Get(ctx, tenant, in.SessionID)
		if err != nil {
			return mcp.ToolResult{}, fmt.Errorf("session lookup: %w", err)
		}
		all, err := deps.Store.List(ctx, tenant, sessionstore.ListFilter{})
		if err != nil {
			return mcp.ToolResult{}, err
		}
		tree := buildTree(root, all)
		body, _ := json.Marshal(tree)
		return textResult(string(body)), nil
	})

	srv.RegisterTool(mcp.Tool{
		Name:        "lenny/cancel_child",
		Description: "Cancel a child session and cascade the cancellation to its descendants (§8.5).",
		InputSchema: json.RawMessage(`{"type":"object","required":["parentSessionId","childSessionId"],"properties":{"parentSessionId":{"type":"string"},"childSessionId":{"type":"string"}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		var in struct {
			ParentSessionID string `json:"parentSessionId"`
			ChildSessionID  string `json:"childSessionId"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
		}
		if in.ParentSessionID == "" || in.ChildSessionID == "" {
			return mcp.ToolResult{}, errors.New("parentSessionId and childSessionId are required")
		}
		if in.ParentSessionID == in.ChildSessionID {
			return mcp.ToolResult{}, errors.New("a session cannot cancel itself as its own child")
		}
		child, err := deps.Store.Get(ctx, tenant, in.ChildSessionID)
		if err != nil {
			return mcp.ToolResult{}, fmt.Errorf("child session lookup: %w", err)
		}
		all, err := deps.Store.List(ctx, tenant, sessionstore.ListFilter{})
		if err != nil {
			return mcp.ToolResult{}, err
		}
		// Authorization: the caller may cancel only sessions inside its
		// own §8 delegation subtree.
		if !isDescendant(child, in.ParentSessionID, all) {
			return mcp.ToolResult{}, fmt.Errorf("session %s is not a child of %s",
				in.ChildSessionID, in.ParentSessionID)
		}
		if session.IsTerminal(child.State) {
			return mcp.ToolResult{}, fmt.Errorf("child session %s is already terminal (%s)",
				in.ChildSessionID, child.State)
		}
		cancelled, err := cancelSubtree(ctx, deps.Store, tenant, child, all)
		if err != nil {
			return mcp.ToolResult{}, err
		}
		body, _ := json.Marshal(struct {
			Cancelled []string `json:"cancelled"`
		}{Cancelled: cancelled})
		return textResult(string(body)), nil
	})

	if deps.Delegation != nil {
		srv.RegisterTool(mcp.Tool{
			Name:        "lenny/delegate_task",
			Description: "Spawn a child session under a running parent (§8.2 recursive delegation).",
			InputSchema: json.RawMessage(`{"type":"object","required":["parentSessionId","runtimeRef"],"properties":{"parentSessionId":{"type":"string"},"runtimeRef":{"type":"string"},"poolRef":{"type":"string"},"maxDepth":{"type":"integer"}}}`),
		}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
			var in struct {
				ParentSessionID string `json:"parentSessionId"`
				RuntimeRef      string `json:"runtimeRef"`
				PoolRef         string `json:"poolRef"`
				MaxDepth        int    `json:"maxDepth"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return mcp.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
			}
			res, err := deps.Delegation.Delegate(ctx, tenant, delegation.Request{
				ParentSessionID: in.ParentSessionID,
				RuntimeRef:      in.RuntimeRef,
				PoolRef:         in.PoolRef,
				MaxDepth:        in.MaxDepth,
			})
			if err != nil {
				return mcp.ToolResult{}, err
			}
			return textResult(fmt.Sprintf(`{"childSessionId":%q,"depth":%d}`, res.Child.ID, res.Depth)), nil
		})
	}
}

// treeNode mirrors the §8 tree shape the get_task_tree tool returns.
type treeNode struct {
	SessionID string     `json:"sessionId"`
	State     string     `json:"state"`
	Children  []treeNode `json:"children"`
}

func buildTree(root sessionstore.Session, all []sessionstore.Session) treeNode {
	childrenByParent := map[string][]sessionstore.Session{}
	for _, s := range all {
		if s.ParentSessionID != "" {
			childrenByParent[s.ParentSessionID] = append(childrenByParent[s.ParentSessionID], s)
		}
	}
	return walk(root, childrenByParent, map[string]bool{})
}

func walk(s sessionstore.Session, byParent map[string][]sessionstore.Session, seen map[string]bool) treeNode {
	node := treeNode{SessionID: s.ID, State: string(s.State), Children: []treeNode{}}
	if seen[s.ID] {
		return node
	}
	seen[s.ID] = true
	for _, c := range byParent[s.ID] {
		node.Children = append(node.Children, walk(c, byParent, seen))
	}
	return node
}

// isDescendant reports whether child sits in the §8 delegation subtree
// rooted at parentID. It walks the ParentSessionID chain upward from
// child; the seen set guards against a malformed cyclic chain.
func isDescendant(child sessionstore.Session, parentID string, all []sessionstore.Session) bool {
	byID := make(map[string]sessionstore.Session, len(all))
	for _, s := range all {
		byID[s.ID] = s
	}
	seen := map[string]bool{}
	cur := child
	for cur.ParentSessionID != "" && !seen[cur.ID] {
		seen[cur.ID] = true
		if cur.ParentSessionID == parentID {
			return true
		}
		next, ok := byID[cur.ParentSessionID]
		if !ok {
			return false
		}
		cur = next
	}
	return false
}

// cancelSubtree transitions child and every non-terminal session in the
// subtree rooted at it to `cancelled` — the default §8 cascade policy.
// Already-terminal sessions are left untouched, but the traversal still
// descends through them to reach any non-terminal descendants. It
// returns the ids it cancelled, sorted for a deterministic result.
func cancelSubtree(ctx context.Context, store sessionstore.Store, tenant string,
	child sessionstore.Session, all []sessionstore.Session) ([]string, error) {

	byParent := map[string][]sessionstore.Session{}
	for _, s := range all {
		if s.ParentSessionID != "" {
			byParent[s.ParentSessionID] = append(byParent[s.ParentSessionID], s)
		}
	}
	var cancelled []string
	seen := map[string]bool{}
	queue := []sessionstore.Session{child}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if seen[cur.ID] {
			continue
		}
		seen[cur.ID] = true
		queue = append(queue, byParent[cur.ID]...)
		if session.IsTerminal(cur.State) {
			continue
		}
		if _, err := store.Update(ctx, tenant, cur.ID, func(row *sessionstore.Session) error {
			row.State = session.StateCancelled
			return nil
		}); err != nil {
			return cancelled, fmt.Errorf("cancel session %s: %w", cur.ID, err)
		}
		cancelled = append(cancelled, cur.ID)
	}
	sort.Strings(cancelled)
	return cancelled, nil
}

func textResult(s string) mcp.ToolResult {
	return mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: s}}}
}
