// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration testbed: several agents delegating in a tree while
// each node calls a model through the §4.9 LLM reverse proxy and invokes
// an external §9.3 MCP tool, asserting the §8.2 per-hop invariants hold
// across the whole tree and cannot be bypassed by cooperating runtimes.
//
// The spec sentence under test (§8.2, "Design Philosophy"): "The gateway
// enforces three invariants on every delegation hop: the child's token
// budget is carved from the parent's remaining budget, the child's scope
// is a subset of the parent's scope, and the child's isolation profile is
// no weaker than the parent's. A runtime cannot bypass these by
// cooperating with another runtime — enforcement sits in the gateway and
// the Token Service, not in the agent code."
//
// This composes the real production surfaces the theme's building blocks
// exposed: the delegation Service (the single gateway entry that turns a
// delegate request into a child session), the connector bridge against a
// real external MCP server stub (the §9.3 scope boundary), and the LLM
// reverse proxy against a stubbed upstream provider (the §4.9 model call).
// It drives a depth-3, fan-2 tree (root → two children → two grandchildren
// each), exercises the model call and the external tool call on every
// node, and asserts each of the three invariants per hop, including the
// anti-collusion property: a child cannot exceed the parent's budget,
// reach a connector its own scope denies, or weaken the parent's isolation,
// regardless of what the agent code requests.
//
// Tier 5 (full chart on Kind, with delegation-echo built into the agent
// workload and the same tree driven through live pods) is the promotion
// target for the same behaviour; this integration-tier test pins the
// invariants deterministically first.
//
// The mcpserver / connector helpers (policyResolver, allowConnectors,
// countMethod, sawMethod) live in external_mcp_tool_test.go (same package).
package tier4_integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/delegation/cycle"
	dlease "github.com/lennylabs/lenny/pkg/delegation/lease"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorauthz"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorinvoke"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectortools"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/proxylease"
	"github.com/lennylabs/lenny/tests/testinfra/stubs/llmprovider"
	"github.com/lennylabs/lenny/tests/testinfra/stubs/mcpserver"
)

const testbedTenant = "acme"

// testbedNode is one agent in the delegation tree: the session id the
// delegation Service minted for it, the runtime/pool identity the hop
// resolved to, and the token budget carved onto its lease.
type testbedNode struct {
	id          string
	runtimeRef  string
	poolRef     string
	tokenBudget int64
}

// spec: 8.2 (per-hop invariants: budget carving, scope subsetting, isolation
//
//	monotonicity; enforcement sits in the gateway not the agent code),
//	9.3 (connector access is scoped per delegation level), 4.9 (LLM
//	reverse proxy model call)
//
// diagnosis: the §8.2 anti-collusion contract broke somewhere in the
//
//	integrated path. Across a live depth-3 delegation tree whose nodes
//	each call a model through the reverse proxy and an external MCP tool,
//	one of the three per-hop invariants failed to hold: a child's carved
//	token budget was allowed to exceed the parent's remaining budget, a
//	scoped child reached a connector its effective delegation policy
//	denies while a sibling was admitted, or a child weakened the parent's
//	isolation profile. A regression here means a cooperating pair of
//	runtimes could escalate budget, scope, or isolation beyond what the
//	gateway granted the delegation subtree.
func TestMultiAgentDelegationTestbedPerHopInvariants(t *testing.T) {
	ctx := context.Background()
	clk := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

	// ---- external third-party MCP tool every node may call (§9.3) ----
	extResult := json.RawMessage(`{"content":[{"type":"text","text":"issue opened"}]}`)
	ext := mcpserver.New(
		t,
		mcpserver.WithTool(mcpserver.Tool{
			Name:        "create_issue",
			Description: "open an issue in the external tracker",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}}}`),
		}),
		mcpserver.WithToolResult("create_issue", extResult),
	)

	// ---- upstream model provider stub behind the §4.9 reverse proxy ----
	upstream := llmprovider.New(t)

	// ---- the delegation harness: real Service over the session store ----
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	idc := &idCounter{}
	svc := delegation.NewService(store, delegation.Options{
		Clock:     clk,
		IDFunc:    idc.next,
		Runtimes:  runtimes,
		CycleMode: cycle.ModeEnforce,
	})

	// ---- the connector bridge sharing the same session store (§9.3) ----
	connectors := connectorstore.NewMemory()
	const connectorID = "acme-tracker"
	if err := connectors.Create(ctx, connectorstore.Connector{
		TenantID: testbedTenant, ID: connectorID, DisplayName: "Acme Tracker",
		MCPServerURL: ext.URL(), Transport: "streamable_http", Visibility: "tenant",
		CreatedAt: clk(), UpdatedAt: clk(),
	}); err != nil {
		t.Fatalf("register connector: %v", err)
	}
	// The per-session effective delegation policy is the §9.3 scope every
	// node is admitted against. The map is mutated per node as the tree is
	// built so a scoped grandchild carries a strictly narrower scope.
	resolver := &policyResolver{effective: map[string]delegationpolicystore.DelegationPolicy{}}
	authz := connectorauthz.New(resolver, store, nil)
	invoker := connectorinvoke.NewInvoker(connectors, nil, connectorinvoke.New(ext.Client()), nil, authz)
	bridge := connectortools.New(store, connectors, authz, invoker)

	// ---- register one agent runtime per node so each hop is a distinct
	// (runtime, pool) identity and the §8.2 cycle gate never fires ----
	for _, rt := range []string{
		"orchestrator",
		"worker-1a", "worker-1b",
		"worker-2aa", "worker-2ab", "worker-2ba", "worker-2bb",
	} {
		if err := runtimes.Create(ctx, runtimestore.Runtime{Name: rt, Type: runtimestore.TypeAgent}); err != nil {
			t.Fatalf("register runtime %s: %v", rt, err)
		}
	}

	// ---- root: a running orchestrator with a sandboxed isolation profile
	// and a 100000-token delegation lease (§8.2 granted budget) ----
	const rootBudget = 100000
	root := testbedNode{id: "root", runtimeRef: "orchestrator", poolRef: "pool-root", tokenBudget: rootBudget}
	if err := store.Create(ctx, sessionstore.Session{
		ID: root.id, TenantID: testbedTenant, UserID: "alice@acme.com",
		RuntimeRef: root.runtimeRef, PoolRef: root.poolRef,
		State:            session.StateRunning,
		IsolationProfile: isolation.ProfileSandboxed,
		DelegationLease:  &sessionstore.DelegationLease{MaxTokenBudget: rootBudget, MaxChildrenTotal: 8},
		CreatedAt:        clk(), UpdatedAt: clk(),
	}); err != nil {
		t.Fatalf("seed root: %v", err)
	}
	resolver.effective[root.id] = allowConnectors(connectorID)

	// delegateHop drives one §8.2 hop through the real delegation Service,
	// asserting the budget-carving invariant on admission, then patches the
	// admitted child to running so it can itself delegate, and registers its
	// effective §9.3 scope for the connector bridge.
	delegateHop := func(t *testing.T, parent testbedNode, runtimeRef, poolRef string, budget int64, scopeConnectors ...string) testbedNode {
		t.Helper()
		res, err := svc.Delegate(ctx, testbedTenant, delegation.Request{
			ParentSessionID: parent.id,
			RuntimeRef:      runtimeRef,
			PoolRef:         poolRef,
			UserID:          "alice@acme.com",
			LeaseSlice:      dlease.LeaseSlice{MaxTokenBudget: budget},
		})
		if err != nil {
			t.Fatalf("delegate %s under %s: %v", runtimeRef, parent.id, err)
		}
		child := res.Child
		// §8.2 budget carving: the carved child lease never exceeds the
		// parent's granted budget, and the resolved slice is stamped onto
		// the child row so the child's own descendants validate against it.
		if child.DelegationLease == nil {
			t.Fatalf("child %s carries no delegation lease; budget was not carved", child.ID)
		}
		if child.DelegationLease.MaxTokenBudget != budget {
			t.Errorf("child %s carved budget = %d, want %d", child.ID, child.DelegationLease.MaxTokenBudget, budget)
		}
		if child.DelegationLease.MaxTokenBudget > parent.tokenBudget {
			t.Errorf("child %s budget %d exceeds parent %s budget %d (§8.2 carving violated)",
				child.ID, child.DelegationLease.MaxTokenBudget, parent.id, parent.tokenBudget)
		}
		// §8.3 SEC-001 isolation monotonicity: the child inherits a profile
		// no weaker than the parent's.
		if !isolation.AtLeastAsRestrictive(child.IsolationProfile, isolation.Profile(parentIsolation(t, store, parent.id))) {
			t.Errorf("child %s isolation %q is weaker than parent %s (§8.2 monotonicity violated)",
				child.ID, child.IsolationProfile, parent.id)
		}
		// A delegated child lands in `created`; bring it to `running` so it
		// can delegate in turn.
		if _, err := store.Update(ctx, testbedTenant, child.ID, func(s *sessionstore.Session) error {
			s.State = session.StateRunning
			return nil
		}); err != nil {
			t.Fatalf("mark child %s running: %v", child.ID, err)
		}
		scope := scopeConnectors
		if scope == nil {
			scope = []string{connectorID}
		}
		resolver.effective[child.ID] = allowConnectors(scope...)
		return testbedNode{id: child.ID, runtimeRef: runtimeRef, poolRef: poolRef, tokenBudget: budget}
	}

	// ---- build the depth-3, fan-2 tree ----
	const d1Budget = 40000 // 2 × 40000 = 80000 ≤ 100000 root
	const d2Budget = 15000 // 2 × 15000 = 30000 ≤ 40000 parent
	d1a := delegateHop(t, root, "worker-1a", "pool-1a", d1Budget)
	d1b := delegateHop(t, root, "worker-1b", "pool-1b", d1Budget)
	d2aa := delegateHop(t, d1a, "worker-2aa", "pool-2aa", d2Budget)
	d2ab := delegateHop(t, d1a, "worker-2ab", "pool-2ab", d2Budget)
	d2ba := delegateHop(t, d1b, "worker-2ba", "pool-2ba", d2Budget)
	// d2bb is scoped to a *different* connector than its ancestors: the
	// §9.3 scope-subsetting probe. Its sibling d2ba shares the tracker.
	d2bb := delegateHop(t, d1b, "worker-2bb", "pool-2bb", d2Budget, "some-other-connector")

	tree := []testbedNode{root, d1a, d1b, d2aa, d2ab, d2ba, d2bb}
	if len(tree) != 7 {
		t.Fatalf("expected a 7-node depth-3 fan-2 tree, got %d nodes", len(tree))
	}

	// ---- every in-scope node calls a model through the §4.9 proxy and the
	// external §9.3 MCP tool; the whole tree is a live multi-agent workload ----
	for _, n := range tree {
		callModelThroughProxy(t, upstream, n.id)
	}
	// All nodes except the deliberately-rescoped d2bb are authorized for the
	// tracker connector; each invokes it, and the external server records it.
	inScope := []testbedNode{root, d1a, d1b, d2aa, d2ab, d2ba}
	for _, n := range inScope {
		raw, isErr, err := bridge.CallConnectorTool(ctx, n.id, connectorID, "create_issue", json.RawMessage(`{"title":"bug"}`))
		if err != nil {
			t.Fatalf("node %s CallConnectorTool: %v", n.id, err)
		}
		if isErr {
			t.Errorf("node %s external tool call reported isError, want success", n.id)
		}
		if string(raw) != string(extResult) {
			t.Errorf("node %s external tool result = %s, want %s", n.id, raw, extResult)
		}
	}
	if got := countMethod(ext.Requests(), "tools/call"); got != len(inScope) {
		t.Errorf("external server saw %d tools/call, want %d (one per in-scope node)", got, len(inScope))
	}

	// ---- §8.2 scope subsetting, anti-collusion: d2bb shares a parent with
	// d2ba but its scope denies the tracker, so the gateway refuses the same
	// connector call its sibling was admitted for — cooperation up the tree
	// does not widen the child's scope ----
	if _, err := bridge.ListConnectorTools(ctx, d2bb.id, connectorID); !errors.Is(err, leasecontrol.ErrConnectorNotPermitted) {
		t.Errorf("scoped node %s ListConnectorTools err = %v, want ErrConnectorNotPermitted", d2bb.id, err)
	}
	if _, _, err := bridge.CallConnectorTool(ctx, d2bb.id, connectorID, "create_issue", json.RawMessage(`{}`)); !errors.Is(err, leasecontrol.ErrConnectorNotPermitted) {
		t.Errorf("scoped node %s CallConnectorTool err = %v, want ErrConnectorNotPermitted", d2bb.id, err)
	}
	// The denied call never reached the external server: the tools/call
	// count is unchanged from the in-scope total.
	if got := countMethod(ext.Requests(), "tools/call"); got != len(inScope) {
		t.Errorf("scoped node's denied call leaked to the external server: tools/call = %d, want %d", got, len(inScope))
	}

	// ---- §8.2 budget carving, anti-collusion: a mid-tree node cannot carve
	// a child budget larger than its own carved budget, no matter what the
	// agent requests. d1a holds 40000; a 50000-token child is rejected and
	// commits no session ----
	beforeCount := countSessions(t, store)
	_, err := svc.Delegate(ctx, testbedTenant, delegation.Request{
		ParentSessionID: d1a.id,
		RuntimeRef:      "worker-2ab", // fresh identity — not a cycle
		PoolRef:         "pool-overbudget",
		UserID:          "alice@acme.com",
		LeaseSlice:      dlease.LeaseSlice{MaxTokenBudget: 50000},
	})
	var budgetErr *dlease.BudgetExceededError
	if !errors.As(err, &budgetErr) {
		t.Errorf("over-budget delegation err = %v, want *BudgetExceededError (§8.2 carving)", err)
	}
	if after := countSessions(t, store); after != beforeCount {
		t.Errorf("over-budget delegation committed a child: session count %d → %d", beforeCount, after)
	}

	// ---- §8.2 isolation monotonicity, anti-collusion: the root cannot
	// spawn a child with a weaker isolation profile than its own sandboxed
	// one; requesting `standard` (rank below sandboxed) is rejected ----
	beforeCount = countSessions(t, store)
	_, err = svc.Delegate(ctx, testbedTenant, delegation.Request{
		ParentSessionID:  root.id,
		RuntimeRef:       "worker-2bb", // fresh identity under root — not a cycle
		PoolRef:          "pool-weakiso",
		UserID:           "alice@acme.com",
		IsolationProfile: isolation.ProfileStandard,
		LeaseSlice:       dlease.LeaseSlice{MaxTokenBudget: 1000},
	})
	var isoErr *delegation.IsolationViolationError
	if !errors.As(err, &isoErr) {
		t.Errorf("isolation-weakening delegation err = %v, want *IsolationViolationError (§8.2 monotonicity)", err)
	}
	if after := countSessions(t, store); after != beforeCount {
		t.Errorf("isolation-weakening delegation committed a child: session count %d → %d", beforeCount, after)
	}
}

// callModelThroughProxy drives one §4.9 reverse-proxy model call for the
// named session: it mints a proxy lease bound to the session, points a
// standard Anthropic-dialect request at the proxy with the opaque lease
// token, and asserts the translated upstream response returns without the
// real upstream key leaking to the caller. This is the "each node calls a
// model through the proxy" leg of the integrated workload.
func callModelThroughProxy(t *testing.T, upstream *llmprovider.Stub, sessionID string) {
	t.Helper()
	fx := proxylease.Start(t, proxylease.Options{
		UpstreamBaseURL: upstream.URL(),
		UpstreamKey:     "sk-ant-upstream-real-secret",
		TenantID:        testbedTenant,
		SessionID:       sessionID,
	})
	body := `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"ping-` + sessionID + `"}]}`
	req, err := http.NewRequest(http.MethodPost, fx.ProxyMessagesURL, strings.NewReader(body))
	if err != nil {
		t.Fatalf("node %s build proxy request: %v", sessionID, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", fx.LeaseToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("node %s proxy request: %v", sessionID, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("node %s proxy request status %d: %s", sessionID, resp.StatusCode, raw)
	}
	if strings.Contains(string(raw), fx.UpstreamKey) {
		t.Fatalf("node %s: the real upstream key leaked to the agent in the proxy response", sessionID)
	}
	var msg struct {
		Type    string `json:"type"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("node %s decode proxy response: %v; body %s", sessionID, err, raw)
	}
	if msg.Type != "message" || len(msg.Content) == 0 || msg.Content[0].Text != "ping-"+sessionID {
		t.Fatalf("node %s proxy did not return the translated upstream response; got %s", sessionID, raw)
	}
}

// parentIsolation reads a session's stored isolation profile so the
// per-hop monotonicity assertion compares against the live parent row
// rather than a value the test remembered.
func parentIsolation(t *testing.T, store *memstore.Store, sessionID string) string {
	t.Helper()
	s, err := store.Get(context.Background(), testbedTenant, sessionID)
	if err != nil {
		t.Fatalf("read parent %s isolation: %v", sessionID, err)
	}
	return string(s.IsolationProfile)
}

// countSessions returns the number of sessions currently in the store for
// the testbed tenant, so a rejected delegation can be shown to commit no
// child row.
func countSessions(t *testing.T, store *memstore.Store) int {
	t.Helper()
	rows, err := store.List(context.Background(), testbedTenant, sessionstore.ListFilter{})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	return len(rows)
}

// idCounter mints deterministic, unique child session ids for the
// delegation Service so a fan-out tree does not collide on a fixed id.
type idCounter struct{ n int }

func (c *idCounter) next() string {
	c.n++
	return "sess_child_" + itoa(c.n)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
