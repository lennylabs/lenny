//go:build chaos

// SPDX-License-Identifier: MIT

// Tier-8 chaos coverage for a live delegation tree whose Redis budget state
// is lost or unreachable. Each test runs gateway replicas in process (the
// session server, the delegation service, and the lenny/delegate_task MCP
// surface, wired as cmd/lenny-gateway wires them) over a real Postgres
// container and a real Redis container. The replicas reach Redis through a
// TCP fault proxy, so a test can make Redis unreachable and bring it back
// with its data intact; data loss is a FLUSHALL against the Redis container,
// which is what a Redis restart without persistence leaves.
//
// The data-loss test pins the §8.3 BUDGET_KEYS_EXPIRED detection on a
// reservation, the one-time tree cleanup, the §16.1 detection counter, and
// the cancelled children's terminal returns, which must not recreate any
// tree-wide key. The outage test pins that a budget return Redis cannot
// serve still completes the §7.1 terminal pipeline and leaves the child's
// reservation counted once Redis returns.
package tier8_chaos_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/delegation/lease"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/resultrollup"
	archivepg "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treearchive/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treebudget"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	interactionpg "github.com/lennylabs/lenny/pkg/gateway/session/interactionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionbudget"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionusage"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/redisconn/slotguard"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/orphanfixture"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	"github.com/lennylabs/lenny/tests/testinfra/tcpfault"
)

const (
	dlgLossTenant   = "acme"
	dlgLossUser     = "alice@acme.com"
	dlgLossRootPool = "pool-a"
	// dlgLossProbeTarget is the agent runtime lenny/delegate_task targets.
	dlgLossProbeTarget = "probe-agent"
	// dlgLossQuiescence is the usage quiescence timeout every replica runs
	// with, so a cancelled child's budget return runs one second after its
	// terminal transition.
	dlgLossQuiescence = time.Second
	// dlgLossKeysExpiredEvent is the CloudEvents type of the critical
	// delegation.budget_keys_expired operational event.
	dlgLossKeysExpiredEvent = "dev.lenny.delegation.budget_keys_expired"
)

// dlgLossEvents counts the delegation.budget_keys_expired operational events
// every replica emits into the shared event store.
type dlgLossEvents struct {
	mu sync.Mutex
	n  int
}

func (e *dlgLossEvents) Emit(_ context.Context, ev events.OperationalEvent) error {
	if ev.Type != dlgLossKeysExpiredEvent {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.n++
	return nil
}

func (e *dlgLossEvents) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.n
}

// dlgLossReturn is one budget return a replica's post-terminal hook issued,
// with the error Redis answered it with.
type dlgLossReturn struct {
	r   treebudget.Reservation
	err error
}

// dlgLossReturns records every budget return a replica issues on its way to
// the production reserver.
type dlgLossReturns struct {
	inner *treebudget.Reserver
	mu    sync.Mutex
	calls []dlgLossReturn
}

func (l *dlgLossReturns) Return(ctx context.Context, r treebudget.Reservation) error {
	err := l.inner.Return(ctx, r)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, dlgLossReturn{r: r, err: err})
	return err
}

func (l *dlgLossReturns) snapshot() []dlgLossReturn {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]dlgLossReturn(nil), l.calls...)
}

// dlgLossTerminator is the enforcer's budget terminator. The tests end
// sessions themselves, so it records nothing.
type dlgLossTerminator struct{}

func (dlgLossTerminator) TerminateSession(string, string) {}

// dlgLossReplica is one gateway replica: its own Redis client, reserver,
// session server, delegation service, MCP surface, enforcer, and metric
// registry, over the shared stores.
type dlgLossReplica struct {
	client   *redis.Client
	srv      *sessionserver.Server
	svc      *delegation.Service
	mcp      http.Handler
	metrics  *gatewaymetrics.Metrics
	enforcer *sessionbudget.Enforcer
	returns  *dlgLossReturns
}

// dlgLossStack is the shared Postgres and Redis, the fault proxy every
// replica reaches Redis through, and the replicas.
type dlgLossStack struct {
	pg           *containers.Postgres
	rd           *containers.Redis
	proxy        *tcpfault.Proxy
	sessions     *pgstore.Store
	interactions *interactionpg.Store
	events       *dlgLossEvents
	replicas     []*dlgLossReplica
}

func newDlgLossStack(t *testing.T, replicas int) *dlgLossStack {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	if _, err := pg.Pool.Exec(context.Background(),
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2)`, dlgLossTenant, []byte{0x01}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	rd := containers.StartRedis(t, containers.RedisOptions{})
	st := &dlgLossStack{
		pg: pg, rd: rd, proxy: tcpfault.Start(t, rd.Addr),
		sessions:     pgstore.New(pg.Pool),
		interactions: interactionpg.New(pg.Pool),
		events:       &dlgLossEvents{},
	}
	for i := 0; i < replicas; i++ {
		st.replicas = append(st.replicas, st.newReplica(t))
	}
	return st
}

// newReplica wires one replica as cmd/lenny-gateway does: the session server
// is the delegation service's BUDGET_KEYS_EXPIRED handler, the reserver is
// the post-terminal hook's budget returner, and the enforcer's Forget is the
// hook's budget forget.
func (st *dlgLossStack) newReplica(t *testing.T) *dlgLossReplica {
	t.Helper()
	client := slotguard.Install(redis.NewClient(&redis.Options{
		Addr:        st.proxy.Addr(),
		DialTimeout: time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second,
		MaxRetries: -1,
	}))
	t.Cleanup(func() { _ = client.Close() })
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("gatewaymetrics.New: %v", err)
	}
	reserver := treebudget.New(client, time.Hour)
	returns := &dlgLossReturns{inner: reserver}
	enforcer := sessionbudget.New(dlgLossTerminator{}, nil, nil)
	archive := archivepg.New(st.pg.Pool, nil)
	srv := sessionserver.New(st.sessions, sessionserver.Options{
		TreeArchive:                    archive,
		Interactions:                   st.interactions,
		OpsEmitter:                     st.events,
		IncDelegationBudgetKeysExpired: m.IncDelegationBudgetKeysExpired,
		TreeBudgetReturner:             returns,
		TaskUsage:                      resultrollup.New(st.sessions, sessionusage.NewMemory(), archive, nil),
		UsageQuiescenceTimeout:         dlgLossQuiescence,
		BudgetForget:                   enforcer.Forget,
	})
	t.Cleanup(srv.Close)
	svc := delegation.NewService(st.sessions, delegation.Options{
		TreeBudgetReserver: reserver,
		BudgetKeysExpired:  srv,
	})
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{Name: dlgLossProbeTarget, Type: runtimestore.TypeAgent}); err != nil {
		t.Fatalf("register %s: %v", dlgLossProbeTarget, err)
	}
	surface := mcp.NewServer()
	mcptools.Register(surface, mcptools.Deps{
		SessionService:    ownerprincipal.SessionAccess{},
		DevTenantFallback: true,
		Store:             st.sessions,
		Executor:          executor.NewEchoExecutor(),
		Runtimes:          runtimes,
		Delegation:        svc,
		TenantID:          dlgLossTenant,
	})
	return &dlgLossReplica{
		client: client, srv: srv, svc: svc, mcp: ownerprincipal.Wrap(surface.Handler(), dlgLossUser),
		metrics: m, enforcer: enforcer, returns: returns,
	}
}

// seedRoot creates a running root session with a finite lease.
func (st *dlgLossStack) seedRoot(t *testing.T) string {
	t.Helper()
	id := newChaosUUID(t)
	if err := st.sessions.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: dlgLossTenant, UserID: dlgLossUser, RuntimeRef: "echo",
		PoolRef: dlgLossRootPool, IsolationProfile: isolation.ProfileSandboxed,
		State: session.StateRunning,
		DelegationLease: &sessionstore.DelegationLease{
			MaxTokenBudget: 1_000_000, MaxTreeSize: 50, MaxChildrenTotal: 50, MaxParallelChildren: 10,
		},
	}); err != nil {
		t.Fatalf("seed root: %v", err)
	}
	return id
}

// delegate creates a child of parent through r's delegation service, which
// reserves the child's budget on the real Redis, and marks the child running
// as its start would. slice is the child's granted token budget.
func (st *dlgLossStack) delegate(t *testing.T, r *dlgLossReplica, parent, runtime string, slice int64) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := r.svc.Delegate(ctx, dlgLossTenant, delegation.Request{
		ParentSessionID: parent, RuntimeRef: runtime, PoolRef: "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
		LeaseSlice:       lease.LeaseSlice{MaxTokenBudget: slice},
	})
	if err != nil {
		t.Fatalf("delegate from %s: %v", parent, err)
	}
	if _, err := st.sessions.Update(ctx, dlgLossTenant, res.Child.ID, func(s *sessionstore.Session) error {
		s.State = session.StateRunning
		return nil
	}); err != nil {
		t.Fatalf("start child %s: %v", res.Child.ID, err)
	}
	return res.Child.ID
}

// dlgLossTree is a root with two delegated children and two delegated
// grandchildren, and the token slice each delegated node holds. Every slice
// is distinct, so a budget return identifies its node by its token delta.
type dlgLossTree struct {
	root     string
	children []string
	grand    []string
	slices   map[string]int64
}

func (tr dlgLossTree) nodes() []string {
	return append(append([]string(nil), tr.children...), tr.grand...)
}

// buildTree delegates the tree through r. The tree-wide token pool is checked
// against the delegating parent's maxTokenBudget, so a parent whose own slice
// is already counted in that pool can admit no child with a token slice. The
// first child therefore declares no token slice (no tokens-axis limit) and
// delegates both grandchildren; the second child holds a finite slice. Every
// delegated node still holds tree_size, tree_memory, and per-parent
// reservations.
//
// spec: §8.2, §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup)
func (st *dlgLossStack) buildTree(t *testing.T, r *dlgLossReplica) dlgLossTree {
	t.Helper()
	tr := dlgLossTree{root: st.seedRoot(t), slices: map[string]int64{}}
	for _, slice := range []int64{0, 5000} {
		c := st.delegate(t, r, tr.root, "worker-1", slice)
		tr.children = append(tr.children, c)
		tr.slices[c] = slice
	}
	for _, slice := range []int64{3000, 3001} {
		g := st.delegate(t, r, tr.children[0], "worker-2", slice)
		tr.grand = append(tr.grand, g)
		tr.slices[g] = slice
	}
	return tr
}

// budgetKeys reads every {root}:dlg:* key and its value straight from the
// Redis container, bypassing the proxy.
func (st *dlgLossStack) budgetKeys(t *testing.T, root string) map[string]string {
	t.Helper()
	ctx := context.Background()
	keys, err := st.rd.Client.Keys(ctx, "{"+root+"}:dlg:*").Result()
	if err != nil {
		t.Fatalf("redis keys: %v", err)
	}
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		v, err := st.rd.Client.Get(ctx, k).Result()
		if err != nil {
			t.Fatalf("redis get %s: %v", k, err)
		}
		out[k] = v
	}
	return out
}

// flushAll loses every key the Redis container holds.
func (st *dlgLossStack) flushAll(t *testing.T) {
	t.Helper()
	if err := st.rd.Client.FlushAll(context.Background()).Err(); err != nil {
		t.Fatalf("FLUSHALL: %v", err)
	}
}

// keysExpired is r's lenny_delegation_budget_keys_expired_total under the
// root pool and the tenant, read from its /metrics exposition.
func (r *dlgLossReplica) keysExpired(t *testing.T) float64 {
	t.Helper()
	rr := httptest.NewRecorder()
	ownerprincipal.Wrap(r.metrics.Handler(), ownerprincipal.Alice).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	series := `lenny_delegation_budget_keys_expired_total{pool="` + dlgLossRootPool + `",tenant_id="` + dlgLossTenant + `"} `
	sc := bufio.NewScanner(rr.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), series); ok {
			n, err := strconv.ParseFloat(v, 64)
			if err != nil {
				t.Fatalf("parse %q: %v", sc.Text(), err)
			}
			return n
		}
	}
	return 0
}

// keysExpiredTotal sums the detection counter over every replica, as a
// Prometheus sum over the gateway's scrape targets does.
func (st *dlgLossStack) keysExpiredTotal(t *testing.T) float64 {
	t.Helper()
	var sum float64
	for _, r := range st.replicas {
		sum += r.keysExpired(t)
	}
	return sum
}

// waitHealthy blocks until every replica's Redis client answers PING through
// the proxy and Postgres answers, so a later chaos test runs on a healthy
// stack.
func (st *dlgLossStack) waitHealthy(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for _, r := range st.replicas {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			err := r.client.Ping(ctx).Err()
			if err == nil {
				err = st.pg.Pool.Ping(ctx)
			}
			cancel()
			if err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("stack did not become healthy: %v", err)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
}

// waitRedisUnreachable blocks until r's Redis client fails, so the outage is
// in effect before the test ends a session.
func (st *dlgLossStack) waitRedisUnreachable(t *testing.T, r *dlgLossReplica) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := r.client.Ping(ctx).Err()
		cancel()
		if err != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("Redis stayed reachable through the cut proxy")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// waitTerminalWork waits for every replica's post-terminal hooks, budget
// returns included.
func (st *dlgLossStack) waitTerminalWork(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, r := range st.replicas {
		if err := r.srv.WaitTerminalWork(ctx); err != nil {
			t.Fatalf("WaitTerminalWork: %v", err)
		}
	}
}

func (st *dlgLossStack) row(t *testing.T, id string) sessionstore.Session {
	t.Helper()
	r, err := st.sessions.Get(context.Background(), dlgLossTenant, id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return r
}

// delegateTask calls lenny/delegate_task on the replica's MCP surface from
// parent and returns the tool result.
func delegateTask(t *testing.T, r *dlgLossReplica, parent string) map[string]any {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lenny/delegate_task","arguments":` +
		`{"parentSessionId":"` + parent + `","target":"` + dlgLossProbeTarget + `","poolRef":"pool-c"}}}`
	rr := httptest.NewRecorder()
	r.mcp.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(body))))
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode delegate_task response: %v; body=%s", err, rr.Body.String())
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("delegate_task response carries no result: %v", resp)
	}
	return result
}

// toolErrorEnvelope returns the lenny/error envelope of an isError result.
func toolErrorEnvelope(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	if result["isError"] != true {
		t.Fatalf("delegate_task succeeded, want an error: %v", result)
	}
	content, _ := result["content"].([]any)
	for _, raw := range content {
		block, _ := raw.(map[string]any)
		if block["type"] != "lenny/error" {
			continue
		}
		var env map[string]any
		if err := json.Unmarshal([]byte(block["text"].(string)), &env); err != nil {
			t.Fatalf("decode error envelope: %v", err)
		}
		return env
	}
	t.Fatalf("no lenny/error block in %v", content)
	return nil
}

// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup), §7.1, §8.10,
// §16.1, §12.4
//
// diagnosis: a FLUSHALL against the Redis container lost a live tree's
// budget keys and the gateway did not end the tree once. Either the next
// lenny/delegate_task from a live node was admitted (a script recreated the
// keys), returned a code other than the permanent INVALID_STATE_TRANSITION,
// the root did not read back failed with failure_class budget_keys_expired
// and reason BUDGET_KEYS_EXPIRED, a descendant stayed live, the
// delegation.budget_keys_expired event did not fire exactly once, the
// detection counter did not rise by exactly one under the root's pool and
// tenant, or a second delegate_task on another replica ran a second cleanup.
// Afterwards, when the cancelled children's terminal returns ran past the
// usage quiescence timeout, a return recreated {root}:dlg:tokens or another
// tree-wide key, a child returned more or less than once, or a return that
// met the absent sentinel was treated as a detection.
func TestRedisDataLossOnALiveDelegationTree_spec_8_3(t *testing.T) {
	st := newDlgLossStack(t, 2)
	a, b := st.replicas[0], st.replicas[1]
	tr := st.buildTree(t, a)
	sentinel := "{" + tr.root + "}:dlg:tokens"
	if keys := st.budgetKeys(t, tr.root); keys[sentinel] == "" || keys["{"+tr.root+"}:dlg:tree_size"] != "4" {
		t.Fatalf("budget keys before the loss = %v, want the sentinel and tree_size 4", keys)
	}
	before := st.keysExpiredTotal(t)
	st.flushAll(t)
	if keys := st.budgetKeys(t, tr.root); len(keys) != 0 {
		t.Fatalf("budget keys after FLUSHALL = %v, want none", keys)
	}

	if !t.Run("detection and cleanup", func(t *testing.T) {
		env := toolErrorEnvelope(t, delegateTask(t, a, tr.grand[0]))
		if env["code"] != "INVALID_STATE_TRANSITION" || env["retryable"] != false {
			t.Fatalf("delegate_task after the loss = %v, want INVALID_STATE_TRANSITION with retryable false", env)
		}
		root := st.row(t, tr.root)
		if root.State != session.StateFailed || root.FailureClass != session.FailureClassBudgetKeysExpired ||
			root.FailureReason != "BUDGET_KEYS_EXPIRED" {
			t.Fatalf("root = (%q, %q, %q), want (failed, budget_keys_expired, BUDGET_KEYS_EXPIRED)",
				root.State, root.FailureClass, root.FailureReason)
		}
		for _, id := range tr.nodes() {
			if s := st.row(t, id).State; s != session.StateCancelled {
				t.Errorf("descendant %s = %q after the cleanup, want cancelled", id, s)
			}
		}
		if n := st.events.count(); n != 1 {
			t.Errorf("delegation.budget_keys_expired events = %d, want 1", n)
		}
		if d := a.keysExpired(t) - before; d != 1 {
			t.Errorf("detection counter under %s/%s rose by %v on the detecting replica, want 1", dlgLossRootPool, dlgLossTenant, d)
		}

		toolErrorEnvelope(t, delegateTask(t, b, tr.grand[1]))
		if n := st.events.count(); n != 1 {
			t.Errorf("delegation.budget_keys_expired events after a second delegate_task = %d, want 1", n)
		}
		if d := st.keysExpiredTotal(t) - before; d != 1 {
			t.Errorf("detection counter rose by %v after a second delegate_task, want 1", d)
		}
		if keys := st.budgetKeys(t, tr.root); len(keys) != 0 {
			t.Errorf("budget keys after the detection = %v, want none", keys)
		}
		st.waitHealthy(t)
	}) {
		return
	}

	t.Run("returns under the absent sentinel", func(t *testing.T) {
		st.waitTerminalWork(t)
		bySlice := map[int64][]error{}
		total := 0
		for _, r := range st.replicas {
			for _, c := range r.returns.snapshot() {
				if c.r.RootSessionID != tr.root {
					t.Errorf("a return addressed root %s, want %s", c.r.RootSessionID, tr.root)
				}
				bySlice[c.r.TokenDelta] = append(bySlice[c.r.TokenDelta], c.err)
				total++
			}
		}
		for _, id := range tr.nodes() {
			errs := bySlice[tr.slices[id]]
			if len(errs) != 1 {
				t.Errorf("descendant %s issued %d budget returns, want 1", id, len(errs))
				continue
			}
			if !errors.Is(errs[0], treebudget.ErrBudgetKeysExpired) {
				t.Errorf("descendant %s return err = %v, want ErrBudgetKeysExpired", id, errs[0])
			}
		}
		if want := len(tr.nodes()); total != want {
			t.Errorf("budget returns = %d, want %d", total, want)
		}
		if keys := st.budgetKeys(t, tr.root); len(keys) != 0 {
			t.Errorf("budget keys after the terminal returns = %v, want none", keys)
		}
		if n := st.events.count(); n != 1 {
			t.Errorf("delegation.budget_keys_expired events after the returns = %d, want 1", n)
		}
		if d := st.keysExpiredTotal(t) - before; d != 1 {
			t.Errorf("detection counter rose by %v after the returns, want 1", d)
		}
		if root := st.row(t, tr.root); root.State != session.StateFailed {
			t.Errorf("root = %q after the returns, want failed", root.State)
		}

		st.waitHealthy(t)
		fresh := st.seedRoot(t)
		st.delegate(t, b, fresh, "worker-1", 1000)
		if keys := st.budgetKeys(t, fresh); keys["{"+fresh+"}:dlg:tokens"] == "" {
			t.Errorf("a fresh tree's first delegation after the loss created no sentinel: %v", keys)
		}
	})
}

// spec: §7.1 (Terminal pipeline context), §8.3 (Return on completion), §8.3
// (BUDGET_KEYS_EXPIRED detection and tree cleanup), §8.10, §16.1, §12.4
//
// diagnosis: a delegated child ended while Redis was unreachable, and its
// budget return failed. Either the failed return cut the rest of the terminal
// pipeline (the enforcer still tracks the child, so its deny stays set, or
// the elicitation the child raised stayed pending), the return was not
// issued once on the recorded root, or, once Redis came back with its data,
// the child's reservation was no longer counted in the tree-wide keys (the
// return was replayed, applied silently, or replayed as a create), the
// outage was treated as a BUDGET_KEYS_EXPIRED detection, or the live parent
// could no longer delegate.
func TestRedisOutageDuringABudgetReturn_spec_8_3(t *testing.T) {
	st := newDlgLossStack(t, 1)
	a := st.replicas[0]
	ctx := context.Background()
	root := st.seedRoot(t)
	// The child declares no token slice so it can delegate a grandchild that
	// holds one: the tree-wide token pool is checked against the delegating
	// parent's maxTokenBudget (see buildTree).
	child := st.delegate(t, a, root, "worker-1", 0)
	grand := st.delegate(t, a, child, "worker-2", 3000)
	before := st.budgetKeys(t, root)
	if before["{"+root+"}:dlg:tree_size"] != "2" || before["{"+root+"}:dlg:tokens"] != "3000" {
		t.Fatalf("budget keys before the outage = %v, want tree_size 2 and tokens 3000", before)
	}

	a.enforcer.Record(ctx, ctx, dlgLossTenant, grand, 100, 100)
	if a.enforcer.Allow(grand) {
		t.Fatal("the enforcer allows a session that exhausted its budget")
	}
	if err := st.interactions.Put(ctx, interactionstore.Interaction{
		ID: newChaosUUID(t), Kind: interactionstore.KindElicitation, SessionID: grand,
		TenantID: dlgLossTenant, UserID: dlgLossUser, Phase: interactionstore.PhasePending,
		Detail: map[string]any{interactionstore.DetailOriginPod: grand},
	}); err != nil {
		t.Fatalf("seed elicitation: %v", err)
	}
	if p, err := st.interactions.ListPending(ctx, dlgLossTenant, grand); err != nil || len(p) != 1 {
		t.Fatalf("pending elicitations before the end = %d, %v; want 1", len(p), err)
	}

	st.proxy.Cut()
	st.waitRedisUnreachable(t, a)
	end := a.srv.BeginTerminal(grand)
	from, row, err := orphanfixture.Cancel(ctx, st.sessions, dlgLossTenant, grand, time.Now())
	if err != nil {
		end()
		t.Fatalf("end the child: %v", err)
	}
	a.srv.OnSessionTerminal(ctx, from, row)
	end()
	st.waitTerminalWork(t)

	calls := a.returns.snapshot()
	if len(calls) != 1 {
		t.Fatalf("budget returns = %d, want 1", len(calls))
	}
	c := calls[0]
	if !errors.Is(c.err, treebudget.ErrBudgetUnavailable) || errors.Is(c.err, treebudget.ErrBudgetKeysExpired) {
		t.Errorf("return during the outage err = %v, want ErrBudgetUnavailable", c.err)
	}
	if c.r.RootSessionID != root || c.r.ParentSessionID != child || c.r.TokenDelta != 3000 {
		t.Errorf("return = %+v, want root %s, parent %s, token delta 3000", c.r, root, child)
	}
	if !a.enforcer.Allow(grand) {
		t.Error("the enforcer still tracks the ended child; the budget forget did not run")
	}
	if p, err := st.interactions.ListPending(ctx, dlgLossTenant, grand); err != nil || len(p) != 0 {
		t.Errorf("pending elicitations after the end = %d, %v; want 0", len(p), err)
	}

	st.proxy.Restore(t)
	st.waitHealthy(t)
	if after := st.budgetKeys(t, root); !reflect.DeepEqual(after, before) {
		t.Errorf("budget keys after Redis returned = %v, want the pre-outage set %v", after, before)
	}
	if n := st.events.count(); n != 0 {
		t.Errorf("delegation.budget_keys_expired events = %d, want 0", n)
	}
	if n := st.keysExpiredTotal(t); n != 0 {
		t.Errorf("detection counter = %v, want 0", n)
	}
	for _, id := range []string{root, child} {
		if s := st.row(t, id).State; s != session.StateRunning {
			t.Errorf("session %s = %q after the outage, want running", id, s)
		}
	}
	st.delegate(t, a, child, "worker-3", 2000)
	if v := st.budgetKeys(t, root)["{"+root+"}:dlg:tree_size"]; v != "3" {
		t.Errorf("tree_size after a delegation on the recovered keys = %q, want 3", v)
	}
	if n := st.keysExpiredTotal(t); n != 0 {
		t.Errorf("detection counter after the recovered delegation = %v, want 0", n)
	}
}
