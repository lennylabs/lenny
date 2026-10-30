// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos testbed: a live multi-agent delegation tree whose nodes
// call a model through the §4.9 LLM reverse proxy, with a mid-scenario
// provider partition injected while the tree is in flight. This is the
// resilience half of the integrated-testbed theme — failure injection
// combined with a running multi-part scenario rather than a single
// component in isolation.
//
// The exercise follows the §12.8 six-step chaos pattern verbatim:
//
//  1. Bring the system to a known-good state — a depth-2 fan-2 delegation
//     tree exists with carved per-hop budgets, and a model call round-trips
//     through the proxy to a healthy upstream.
//  2. Inject the failure — the upstream provider returns 5xx for every
//     call (an agent-to-LLM-provider partition; the §4.9 forwarder treats
//     a 5xx or a transport failure identically as a breaker failure).
//  3. Assert the documented behavior — after the consecutive-failure
//     threshold the §4.9 circuit breaker trips open, the pod-facing
//     PROVIDER_UNAVAILABLE (HTTP 503) error reaches the client, and while
//     open the breaker stops dialing the doomed upstream entirely.
//  4. Resolve the failure — restore upstream health and let the breaker
//     cooldown elapse.
//  5. Assert recovery — the half-open probe succeeds, the breaker closes,
//     and model calls round-trip again.
//  6. Assert no data loss — the delegation tree's session rows and carved
//     token budgets are byte-for-byte what they were before the partition,
//     and a fresh delegation still admits, so the control plane was never
//     corrupted by the model-path partition.
//
// The model path (the proxy, the upstream, the circuit breaker) and the
// delegation control plane (the session store, the carved leases) are
// deliberately independent surfaces: this test proves a provider
// partition degrades the model path with a documented error and recovers
// cleanly, while leaving the in-flight delegation tree's budget and task
// structure untouched.
package tier8_chaos_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	dlease "github.com/lennylabs/lenny/pkg/delegation/lease"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/proxylease"
	"github.com/lennylabs/lenny/tests/testinfra/stubs/llmprovider"
)

const partitionTenant = "acme"

// stepClock is a controllable clock the circuit breaker reads so the test
// can advance past the open-state cooldown deterministically rather than
// sleeping. It is mutex-guarded because the breaker calls now() from the
// httptest server goroutine while the test goroutine advances it.
type stepClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *stepClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// spec: 12.8 (six-step chaos pattern: known-good, inject, assert
//
//	documented degraded behavior, resolve, assert recovery, assert no
//	data loss — TESTING.md "Every chaos test follows the same shape"),
//	12.8 (network failures: agent-to-LLM-provider partition), 4.9 (LLM
//	Proxy circuit breaker: consecutive upstream failures trip it open, an
//	open breaker returns PROVIDER_UNAVAILABLE without dialing, and a
//	half-open probe recovers it), 8.2 (delegation carving invariants
//	survive an unrelated model-path fault)
//
// diagnosis: the integrated model-path partition scenario regressed. While
//
//	a live delegation tree ran with model traffic and the upstream
//	provider was partitioned, one of the following failed: the §4.9
//	circuit breaker did not trip open under sustained upstream failure,
//	the documented PROVIDER_UNAVAILABLE error did not reach the pod, the
//	open breaker kept dialing the doomed upstream, the breaker did not
//	recover after the partition healed and the cooldown elapsed, or the
//	provider partition corrupted the in-flight delegation tree's session
//	rows or carved token budgets. A regression here means a provider
//	outage either hangs the proxy on a doomed upstream, hides the outage
//	from the agent, or bleeds into the delegation control plane.
func TestProviderPartitionMidDelegationRecovers(t *testing.T) {
	ctx := context.Background()
	fixedNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := func() time.Time { return fixedNow }

	// ---- build the live delegation tree: root + two carved children ----
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	idc := &partitionIDs{}
	svc := delegation.NewService(store, delegation.Options{
		Clock:    clk,
		IDFunc:   idc.next,
		Runtimes: runtimes,
	})
	for _, rt := range []string{"orchestrator", "worker-a", "worker-b"} {
		if err := runtimes.Create(ctx, runtimestore.Runtime{Name: rt, Type: runtimestore.TypeAgent}); err != nil {
			t.Fatalf("register runtime %s: %v", rt, err)
		}
	}

	const rootBudget = 100000
	const childBudget = 30000 // 2 × 30000 = 60000 ≤ 100000 root
	if err := store.Create(ctx, sessionstore.Session{
		ID: "root", TenantID: partitionTenant, UserID: "alice@acme.com",
		RuntimeRef: "orchestrator", PoolRef: "pool-root",
		State:            session.StateRunning,
		IsolationProfile: isolation.ProfileSandboxed,
		DelegationLease:  &sessionstore.DelegationLease{MaxTokenBudget: rootBudget, MaxChildrenTotal: 8},
		CreatedAt:        fixedNow, UpdatedAt: fixedNow,
	}); err != nil {
		t.Fatalf("seed root: %v", err)
	}
	delegate := func(runtimeRef, poolRef string, budget int64) string {
		res, err := svc.Delegate(ctx, partitionTenant, delegation.Request{
			ParentSessionID: "root",
			RuntimeRef:      runtimeRef,
			PoolRef:         poolRef,
			UserID:          "alice@acme.com",
			LeaseSlice:      dlease.LeaseSlice{MaxTokenBudget: budget},
		})
		if err != nil {
			t.Fatalf("delegate %s: %v", runtimeRef, err)
		}
		if res.Child.DelegationLease == nil || res.Child.DelegationLease.MaxTokenBudget != budget {
			t.Fatalf("child %s budget was not carved: %+v", res.Child.ID, res.Child.DelegationLease)
		}
		return res.Child.ID
	}
	childA := delegate("worker-a", "pool-a", childBudget)
	childB := delegate("worker-b", "pool-b", childBudget)

	// The pre-partition snapshot of the whole tree. Step 6 asserts the
	// tree is byte-for-byte identical to this after the partition heals.
	before := snapshotTree(t, store)
	if len(before) != 3 {
		t.Fatalf("expected a 3-node tree (root + 2 children), got %d", len(before))
	}

	// ---- stand up the §4.9 proxy with a breaker on a controllable clock ----
	clock := &stepClock{now: fixedNow}
	breaker := &llmproxy.CircuitBreaker{
		FailureThreshold: 3,
		Cooldown:         30 * time.Second,
		Now:              clock.Now,
	}
	upstream := llmprovider.New(t)
	fx := proxylease.Start(t, proxylease.Options{
		UpstreamBaseURL: upstream.URL(),
		UpstreamKey:     "sk-ant-upstream-real-secret",
		TenantID:        partitionTenant,
		SessionID:       "root",
		Breaker:         breaker,
	})

	// ---- STEP 1: known-good state — a model call round-trips ----
	if status, body := callModel(t, fx); status != http.StatusOK {
		t.Fatalf("step 1 known-good: model call status %d, body %s", status, body)
	}
	if breaker.State() != llmproxy.CircuitClosed {
		t.Fatalf("step 1: breaker state = %v, want closed", breaker.State())
	}

	// ---- STEP 2: inject the partition — every upstream call returns 5xx ----
	upstream.SetResponseOverride(func(llmprovider.Request) (int, string, map[string]string) {
		return http.StatusInternalServerError, `{"error":"upstream partitioned"}`, nil
	})

	// ---- STEP 3: assert the documented degraded behavior ----
	// Each failing call surfaces PROVIDER_UNAVAILABLE (503) to the pod and
	// records a breaker failure; after FailureThreshold consecutive
	// failures the breaker trips open.
	for i := 0; i < breaker.FailureThreshold; i++ {
		status, code, _ := callModelError(t, fx)
		if status != http.StatusServiceUnavailable || code != "PROVIDER_UNAVAILABLE" {
			t.Fatalf("step 3 failing call %d: status %d code %q, want 503 PROVIDER_UNAVAILABLE", i, status, code)
		}
	}
	if breaker.State() != llmproxy.CircuitOpen {
		t.Fatalf("step 3: breaker state = %v after %d failures, want open", breaker.State(), breaker.FailureThreshold)
	}

	// While the breaker is open it rejects the call without dialing the
	// doomed upstream, and still returns the documented PROVIDER_UNAVAILABLE.
	reqsBeforeOpenCall := len(upstream.Requests())
	status, code, msg := callModelError(t, fx)
	if status != http.StatusServiceUnavailable || code != "PROVIDER_UNAVAILABLE" {
		t.Fatalf("step 3 open-breaker call: status %d code %q, want 503 PROVIDER_UNAVAILABLE", status, code)
	}
	if !strings.Contains(msg, "circuit breaker") {
		t.Errorf("step 3 open-breaker error message = %q, want it to name the open circuit breaker", msg)
	}
	if got := len(upstream.Requests()); got != reqsBeforeOpenCall {
		t.Errorf("step 3: open breaker dialed the partitioned upstream: request count %d → %d, want unchanged",
			reqsBeforeOpenCall, got)
	}

	// The provider partition must not have touched the delegation control
	// plane while it was active.
	if during := snapshotTree(t, store); !treesEqual(before, during) {
		t.Errorf("step 3: delegation tree mutated during the partition: before=%v during=%v", before, during)
	}

	// ---- STEP 4: resolve — restore upstream health, let cooldown elapse ----
	upstream.SetResponseOverride(nil)
	clock.advance(31 * time.Second)

	// ---- STEP 5: assert recovery — the half-open probe closes the breaker ----
	if status, body := callModel(t, fx); status != http.StatusOK {
		t.Fatalf("step 5 recovery probe: model call status %d, body %s", status, body)
	}
	if breaker.State() != llmproxy.CircuitClosed {
		t.Fatalf("step 5: breaker state = %v after recovery, want closed", breaker.State())
	}
	// A second post-recovery call confirms steady-state health, not just a
	// one-shot probe.
	if status, body := callModel(t, fx); status != http.StatusOK {
		t.Fatalf("step 5 steady-state: model call status %d, body %s", status, body)
	}

	// ---- STEP 6: assert no data loss — tree is unchanged and still live ----
	after := snapshotTree(t, store)
	if !treesEqual(before, after) {
		t.Errorf("step 6: delegation tree corrupted by the partition:\n before=%v\n after =%v", before, after)
	}
	for _, id := range []string{"root", childA, childB} {
		if _, ok := after[id]; !ok {
			t.Errorf("step 6: node %s missing from the tree after recovery", id)
		}
	}
	// The control plane still admits a fresh delegation: root's remaining
	// budget (100000 − 2×30000 = 40000) still carves a child, proving the
	// partition left no budget-accounting corruption.
	freshID := delegate("worker-a", "pool-fresh", 20000)
	if _, err := store.Get(ctx, partitionTenant, freshID); err != nil {
		t.Errorf("step 6: post-recovery delegation did not commit a child: %v", err)
	}
}

// treeNode is the subset of a session row the partition must not disturb:
// its state and its carved delegation budget.
type treeNode struct {
	state  session.State
	budget int64
}

// snapshotTree captures every session row for the partition tenant so the
// test can prove the delegation tree is unchanged across the fault.
func snapshotTree(t *testing.T, store *memstore.Store) map[string]treeNode {
	t.Helper()
	rows, err := store.List(context.Background(), partitionTenant, sessionstore.ListFilter{})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	out := make(map[string]treeNode, len(rows))
	for _, s := range rows {
		var budget int64 = -1
		if s.DelegationLease != nil {
			budget = s.DelegationLease.MaxTokenBudget
		}
		out[s.ID] = treeNode{state: s.State, budget: budget}
	}
	return out
}

// treesEqual reports whether two tree snapshots are identical.
func treesEqual(a, b map[string]treeNode) bool {
	if len(a) != len(b) {
		return false
	}
	for id, na := range a {
		nb, ok := b[id]
		if !ok || na != nb {
			return false
		}
	}
	return true
}

// callModel drives one §4.9 model round-trip through the proxy with the
// fixture's lease token and returns the status and body. It is the
// happy-path leg used for the known-good and recovered states.
func callModel(t *testing.T, fx *proxylease.Fixture) (int, string) {
	t.Helper()
	body := `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"ping"}]}`
	req, err := http.NewRequest(http.MethodPost, fx.ProxyMessagesURL, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build proxy request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", fx.LeaseToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxy request: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// callModelError drives one model round-trip and decodes the pod-facing
// error envelope, returning the status, the error code, and the message.
// It is the leg used while the upstream is partitioned.
func callModelError(t *testing.T, fx *proxylease.Fixture) (status int, code, message string) {
	t.Helper()
	s, body := callModel(t, fx)
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("decode proxy error envelope (status %d): %v; body %s", s, err, body)
	}
	return s, env.Error.Code, env.Error.Message
}

// partitionIDs mints deterministic child session ids for the delegation
// Service so the fan-out does not collide on a fixed id.
type partitionIDs struct{ n int }

func (c *partitionIDs) next() string {
	c.n++
	return "sess_partition_child_" + string(rune('a'+c.n-1))
}
