// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioncallback"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/tests/testinfra/orphanfixture"
)

// recordingOpsEmitter records the operational events the server emits.
type recordingOpsEmitter struct {
	mu     sync.Mutex
	events []events.OperationalEvent
}

func (r *recordingOpsEmitter) Emit(_ context.Context, e events.OperationalEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return nil
}

// count returns the recorded events of the given CloudEvents type.
func (r *recordingOpsEmitter) count(ceType string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.events {
		if e.Type == ceType {
			n++
		}
	}
	return n
}

// keysExpiredCounter records each IncDelegationBudgetKeysExpired call by its
// pool and tenant labels.
type keysExpiredCounter struct {
	mu    sync.Mutex
	calls map[string]int
}

func newKeysExpiredCounter() *keysExpiredCounter {
	return &keysExpiredCounter{calls: map[string]int{}}
}

func (c *keysExpiredCounter) inc(pool, tenantID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls[pool+"/"+tenantID]++
}

func (c *keysExpiredCounter) get(pool, tenantID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[pool+"/"+tenantID]
}

func (c *keysExpiredCounter) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, v := range c.calls {
		n += v
	}
	return n
}

var keysExpiredEventType = eventDelegationBudgetKeysExpired.CloudEventsType()

// keysExpiredFixture is a server over an in-memory store with a recording
// operational-event emitter, a detection counter, a billing ledger, and a
// §14 callback dispatcher whose deliveries are counted.
type keysExpiredFixture struct {
	store     *orphanfixture.WriteCounter
	srv       *Server
	ops       *recordingOpsEmitter
	metric    *keysExpiredCounter
	billing   *billingstore.Memory
	delivered atomic.Int32
	settled   chan struct{}
	cbURL     string
	cbIP      string
}

func newKeysExpiredFixture(t *testing.T, inner sessionstore.Store) *keysExpiredFixture {
	t.Helper()
	f := &keysExpiredFixture{
		store:   orphanfixture.NewWriteCounter(inner),
		ops:     &recordingOpsEmitter{},
		metric:  newKeysExpiredCounter(),
		billing: billingstore.NewMemory(),
		settled: make(chan struct{}, 16),
	}
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.delivered.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(hook.Close)
	u, err := url.Parse(hook.URL)
	if err != nil {
		t.Fatalf("parse hook url: %v", err)
	}
	f.cbURL, f.cbIP = hook.URL, u.Hostname()
	dispatcher := sessioncallback.NewDispatcher(sessioncallback.Config{
		GatewayID: "gw-test1",
		Opener:    func(context.Context, string, []byte) ([]byte, error) { return []byte("secret"), nil },
		Finalizer: func(context.Context, string, string, *sessioncallback.DeliveryRecord) error {
			f.settled <- struct{}{}
			return nil
		},
		Sleep:      func(context.Context, time.Duration) bool { return true },
		Revalidate: func(netip.Addr) bool { return true },
		NewClient:  func(netip.Addr) *http.Client { return hook.Client() },
	})
	f.srv = New(f.store, Options{
		DevTenantHeader:                true,
		OpsEmitter:                     f.ops,
		Billing:                        f.billing,
		CallbackDispatcher:             dispatcher,
		IncDelegationBudgetKeysExpired: f.metric.inc,
	})
	t.Cleanup(f.srv.Close)
	return f
}

// seed creates a session row.
func (f *keysExpiredFixture) seed(t *testing.T, row sessionstore.Session) {
	t.Helper()
	now := time.Now().UTC()
	row.TenantID, row.RuntimeRef, row.CreatedAt, row.UpdatedAt = "acme", "echo", now, now
	if err := f.store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed %s: %v", row.ID, err)
	}
}

// seedTree seeds the root in rootState with a callback registered, a
// cancel_all child with a running grandchild, and a detach child with a
// running grandchild of its own.
func (f *keysExpiredFixture) seedTree(t *testing.T, rootState session.State, podAssignment string) {
	t.Helper()
	f.seed(t, sessionstore.Session{
		ID: "root", State: rootState, PoolRef: "pool-a", PodAssignment: podAssignment,
		CallbackURL: f.cbURL, CallbackPinnedIP: f.cbIP, CallbackSecret: []byte("opaque-sealed"),
	})
	for _, n := range []struct {
		id, parent string
		policy     session.CascadePolicy
	}{
		{"child", "root", session.CascadeCancelAll},
		{"grandchild", "child", session.CascadeCancelAll},
		{"detached", "root", session.CascadeDetach},
		{"detached-child", "detached", session.CascadeCancelAll},
	} {
		f.seed(t, sessionstore.Session{
			ID: n.id, State: session.StateRunning, ParentSessionID: n.parent,
			RootSessionID: "root", CascadeOnFailure: n.policy,
		})
	}
}

func (f *keysExpiredFixture) row(t *testing.T, id string) sessionstore.Session {
	t.Helper()
	r, err := f.store.Get(context.Background(), "acme", id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return r
}

func (f *keysExpiredFixture) completedBillingEvents(t *testing.T, id string) int {
	t.Helper()
	evs, err := f.billing.Since(context.Background(), "acme", 0, 0)
	if err != nil {
		t.Fatalf("billing Since: %v", err)
	}
	n := 0
	for _, e := range evs {
		if e.SessionID == id && e.EventType == billingstore.EventSessionCompleted {
			n++
		}
	}
	return n
}

// waitCallbacks waits for want callback deliveries to settle, then confirms
// no further delivery arrives.
func (f *keysExpiredFixture) waitCallbacks(t *testing.T, want int) {
	t.Helper()
	for i := 0; i < want; i++ {
		select {
		case <-f.settled:
		case <-time.After(5 * time.Second):
			t.Fatalf("callback %d of %d was not delivered", i+1, want)
		}
	}
	select {
	case <-f.settled:
		t.Fatalf("more than %d callbacks were delivered", want)
	case <-time.After(50 * time.Millisecond):
	}
	if got := int(f.delivered.Load()); got != want {
		t.Errorf("callback deliveries = %d, want %d", got, want)
	}
}

func (f *keysExpiredFixture) rootClaimed() bool {
	f.srv.terminalClaims.mu.Lock()
	defer f.srv.terminalClaims.mu.Unlock()
	_, ok := f.srv.terminalClaims.entries["root"]
	return ok
}

func (f *keysExpiredFixture) assertDescendantsCancelled(t *testing.T) {
	t.Helper()
	for _, id := range []string{"child", "grandchild", "detached", "detached-child"} {
		if st := f.row(t, id).State; st != session.StateCancelled {
			t.Errorf("descendant %s state = %q, want cancelled", id, st)
		}
		if w := f.store.Writes(id); w != 1 {
			t.Errorf("descendant %s received %d terminal writes, want 1", id, w)
		}
	}
}

// TestBudgetKeysExpiredFailsAnAdmittedRootOnce_spec_8_3 asserts that a
// detection whose root is in running, input_required, or suspended (holding
// its pod or with the pod released) fails the root with the
// budget_keys_expired class and reason, runs its terminal pipeline once (one
// §14 callback, one session.completed billing event), emits one
// delegation.budget_keys_expired event, counts the detection under the root's
// pool, cancels every descendant whatever its cascade policy, and holds no
// root terminal claim once the handler returns.
//
// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup), §7.1, §7.2
// (Interactive Session Model), §8.10
func TestBudgetKeysExpiredFailsAnAdmittedRootOnce_spec_8_3(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state session.State
		pod   string
	}{
		{"running", session.StateRunning, "pod-a"},
		{"input_required", session.StateInputRequired, "pod-a"},
		{"suspended holding its pod", session.StateSuspended, "pod-a"},
		{"suspended with its pod released", session.StateSuspended, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newKeysExpiredFixture(t, memstore.New())
			f.seedTree(t, tc.state, tc.pod)
			ctx := context.Background()

			f.srv.HandleBudgetKeysExpired(ctx, "acme", "root")
			if f.rootClaimed() {
				t.Error("the root holds a terminal claim after the handler returned")
			}
			if err := f.srv.WaitTerminalWork(ctx); err != nil {
				t.Fatalf("WaitTerminalWork: %v", err)
			}

			root := f.row(t, "root")
			if root.State != session.StateFailed {
				t.Fatalf("root state = %q, want failed", root.State)
			}
			if root.FailureClass != session.FailureClassBudgetKeysExpired || root.FailureReason != reasonBudgetKeysExpired {
				t.Errorf("root failure = (%q, %q), want (%q, %q)", root.FailureClass, root.FailureReason,
					session.FailureClassBudgetKeysExpired, reasonBudgetKeysExpired)
			}
			if root.TerminatedAt.IsZero() {
				t.Error("the root write did not record the Terminated fact")
			}
			if w := f.store.Writes("root"); w != 1 {
				t.Errorf("root terminal writes = %d, want 1", w)
			}
			f.assertDescendantsCancelled(t)
			if n := f.ops.count(keysExpiredEventType); n != 1 {
				t.Errorf("delegation.budget_keys_expired events = %d, want 1", n)
			}
			if n := f.metric.get("pool-a", "acme"); n != 1 {
				t.Errorf("detections counted under pool-a = %d, want 1", n)
			}
			if n := f.completedBillingEvents(t, "root"); n != 1 {
				t.Errorf("root session.completed billing events = %d, want 1", n)
			}
			f.waitCallbacks(t, 1)
		})
	}
}

// TestBudgetKeysExpiredKeepsARefusedRootAndSweeps_spec_8_3 asserts that a
// detection whose root is in a state §7.2 gives no BUDGET_KEYS_EXPIRED
// transition from (resume_pending, resuming, or awaiting_client_action)
// keeps the root's state, emits no event, still counts the detection and
// cancels every descendant, and leaves the root with no terminal claim.
//
// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup), §7.2
// (Interactive Session Model), §8.10
func TestBudgetKeysExpiredKeepsARefusedRootAndSweeps_spec_8_3(t *testing.T) {
	for _, st := range []session.State{
		session.StateResumePending, session.StateResuming, session.StateAwaitingClientAction,
	} {
		t.Run(string(st), func(t *testing.T) {
			f := newKeysExpiredFixture(t, memstore.New())
			f.seedTree(t, st, "")
			ctx := context.Background()

			f.srv.HandleBudgetKeysExpired(ctx, "acme", "root")
			if f.rootClaimed() {
				t.Error("the root holds a terminal claim after the handler returned")
			}
			if err := f.srv.WaitTerminalWork(ctx); err != nil {
				t.Fatalf("WaitTerminalWork: %v", err)
			}
			root := f.row(t, "root")
			if root.State != st || root.FailureClass != "" {
				t.Errorf("root = (%q, %q), want (%q, no failure class)", root.State, root.FailureClass, st)
			}
			f.assertDescendantsCancelled(t)
			if n := f.ops.count(keysExpiredEventType); n != 0 {
				t.Errorf("delegation.budget_keys_expired events = %d, want 0", n)
			}
			if n := f.metric.get("pool-a", "acme"); n != 1 {
				t.Errorf("detections counted = %d, want 1", n)
			}
			if n := f.completedBillingEvents(t, "root"); n != 0 {
				t.Errorf("root session.completed billing events = %d, want 0", n)
			}
		})
	}
}

// TestBudgetKeysExpiredGuardAdmitsOnlyTheSpecStates_spec_7_2 asserts the
// root-write guard's admission set over every session state: only running,
// input_required, and suspended are admitted and stamped.
//
// spec: §7.2 (Interactive Session Model), §8.3
func TestBudgetKeysExpiredGuardAdmitsOnlyTheSpecStates_spec_7_2(t *testing.T) {
	admitted := map[session.State]bool{
		session.StateRunning: true, session.StateInputRequired: true, session.StateSuspended: true,
	}
	for _, st := range session.AllStates() {
		r := &sessionstore.Session{State: st}
		err := budgetKeysExpiredGuard(r)
		if admitted[st] {
			if err != nil || r.FailureClass != session.FailureClassBudgetKeysExpired || r.FailureReason != reasonBudgetKeysExpired {
				t.Errorf("%s: guard = (%v, %q, %q), want admitted and stamped", st, err, r.FailureClass, r.FailureReason)
			}
			continue
		}
		if !errors.Is(err, sessionstore.ErrSuperseded) || r.FailureClass != "" {
			t.Errorf("%s: guard = (%v, %q), want ErrSuperseded with no stamp", st, err, r.FailureClass)
		}
	}
}

// rootUpdateFailingStore fails every Update of one session.
type rootUpdateFailingStore struct {
	sessionstore.Store
	id string
}

func (s *rootUpdateFailingStore) Update(ctx context.Context, tenantID, id string, fn func(*sessionstore.Session) error) (sessionstore.Session, error) {
	if id == s.id {
		return sessionstore.Session{}, errors.New("store unavailable")
	}
	return s.Store.Update(ctx, tenantID, id, fn)
}

// TestBudgetKeysExpiredRootUpdateErrorEndsItsClaim_spec_8_3 asserts that a
// root write the store fails runs no hook, emits no event, ends the root's
// terminal claim before the handler returns, and still sweeps the tree.
//
// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup), §7.1
func TestBudgetKeysExpiredRootUpdateErrorEndsItsClaim_spec_8_3(t *testing.T) {
	inner := memstore.New()
	f := newKeysExpiredFixture(t, &rootUpdateFailingStore{Store: inner, id: "root"})
	f.seedTree(t, session.StateRunning, "pod-a")
	ctx := context.Background()

	f.srv.HandleBudgetKeysExpired(ctx, "acme", "root")
	if f.rootClaimed() {
		t.Error("the root holds a terminal claim after the handler returned")
	}
	if err := f.srv.WaitTerminalWork(ctx); err != nil {
		t.Fatalf("WaitTerminalWork: %v", err)
	}
	if n := f.srv.OpenTerminalWork(); n != 0 {
		t.Errorf("OpenTerminalWork = %d, want 0", n)
	}
	if st := f.row(t, "root").State; st != session.StateRunning {
		t.Errorf("root state = %q, want running", st)
	}
	if n := f.ops.count(keysExpiredEventType); n != 0 {
		t.Errorf("delegation.budget_keys_expired events = %d, want 0", n)
	}
	f.assertDescendantsCancelled(t)
}

// TestBudgetKeysExpiredTerminalRootCancelsAnOrphan_spec_8_3 asserts that a
// detection whose root is already terminal counts the detection, cancels a
// live orphan under that root, and emits no event.
//
// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup), §8.10 (Orphan
// cleanup)
func TestBudgetKeysExpiredTerminalRootCancelsAnOrphan_spec_8_3(t *testing.T) {
	f := newKeysExpiredFixture(t, memstore.New())
	f.seed(t, sessionstore.Session{ID: "root", State: session.StateCompleted, PoolRef: "pool-a"})
	f.seed(t, sessionstore.Session{
		ID: "orphan", State: session.StateRunning, ParentSessionID: "root",
		RootSessionID: "root", CascadeOnFailure: session.CascadeDetach,
	})
	ctx := context.Background()

	f.srv.HandleBudgetKeysExpired(ctx, "acme", "root")
	if err := f.srv.WaitTerminalWork(ctx); err != nil {
		t.Fatalf("WaitTerminalWork: %v", err)
	}
	if st := f.row(t, "orphan").State; st != session.StateCancelled {
		t.Errorf("orphan state = %q, want cancelled", st)
	}
	if st := f.row(t, "root").State; st != session.StateCompleted {
		t.Errorf("root state = %q, want completed", st)
	}
	if n := f.metric.get("pool-a", "acme"); n != 1 {
		t.Errorf("detections counted = %d, want 1", n)
	}
	if n := f.ops.count(keysExpiredEventType); n != 0 {
		t.Errorf("delegation.budget_keys_expired events = %d, want 0", n)
	}
}

// TestBudgetKeysExpiredMissingRootCountsUnderNoPool_spec_16_1 asserts that a
// detection whose root row cannot be read is counted under an empty pool and
// emits no event.
//
// spec: §16.1, §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup)
func TestBudgetKeysExpiredMissingRootCountsUnderNoPool_spec_16_1(t *testing.T) {
	f := newKeysExpiredFixture(t, memstore.New())
	f.srv.HandleBudgetKeysExpired(context.Background(), "acme", "absent")
	if n := f.metric.get("", "acme"); n != 1 {
		t.Errorf("detections counted under an empty pool = %d, want 1", n)
	}
	if n := f.ops.count(keysExpiredEventType); n != 0 {
		t.Errorf("delegation.budget_keys_expired events = %d, want 0", n)
	}
}

// TestBudgetKeysExpiredConcurrentDetectionsFailTheRootOnce_spec_8_3 runs
// randomized concurrent detections over randomized trees and asserts one
// applied root write with the budget_keys_expired class, one event, one
// metric increment per detection under the root's pool, one terminal write
// and at most one post-terminal hook per member, and every member terminal.
//
// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup), §7.1, §8.10
func TestBudgetKeysExpiredConcurrentDetectionsFailTheRootOnce_spec_8_3(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	for round := 0; round < 20; round++ {
		runBudgetKeysExpiredRound(t, rng, round, 2+rng.Intn(6))
	}
}

// runBudgetKeysExpiredRound seeds a random tree under a running root, runs
// detections concurrent HandleBudgetKeysExpired calls with randomized start
// offsets, and checks the once-per-root and once-per-member outcomes.
func runBudgetKeysExpiredRound(t *testing.T, rng *rand.Rand, round, detections int) {
	t.Helper()
	n := 0
	newID := func() string { n++; return fmt.Sprintf("r%d_s%02d", round, n) }
	nodes := orphanfixture.RandomTree(rng, newID, 3, 3)
	writes := orphanfixture.NewWriteCounter(memstore.New())
	archive := orphanfixture.NewArchiveCounter()
	ops := &recordingOpsEmitter{}
	metric := newKeysExpiredCounter()
	srv := New(writes, Options{
		DevTenantHeader: true, TreeArchive: archive, OpsEmitter: ops,
		IncDelegationBudgetKeysExpired: metric.inc,
	})
	defer srv.Close()
	orphanfixture.Seed(t, writes, sessionstore.Session{TenantID: "acme", RuntimeRef: "echo", PoolRef: "pool-a"}, nodes)
	rootID := nodes[0].ID
	ctx := context.Background()

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < detections; i++ {
		delay := time.Duration(rng.Intn(200)) * time.Microsecond
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			time.Sleep(delay)
			srv.HandleBudgetKeysExpired(ctx, "acme", rootID)
		}()
	}
	close(start)
	wg.Wait()
	if err := srv.WaitTerminalWork(ctx); err != nil {
		t.Fatalf("round %d: WaitTerminalWork: %v", round, err)
	}

	root, err := writes.Get(ctx, "acme", rootID)
	if err != nil {
		t.Fatalf("round %d: get root: %v", round, err)
	}
	if root.State != session.StateFailed || root.FailureClass != session.FailureClassBudgetKeysExpired {
		t.Errorf("round %d: root = (%q, %q), want (failed, budget_keys_expired)", round, root.State, root.FailureClass)
	}
	if w := writes.Writes(rootID); w != 1 {
		t.Errorf("round %d: root terminal writes = %d, want 1", round, w)
	}
	if got := ops.count(keysExpiredEventType); got != 1 {
		t.Errorf("round %d: delegation.budget_keys_expired events = %d, want 1", round, got)
	}
	if got := metric.get("pool-a", "acme"); got != detections || metric.total() != detections {
		t.Errorf("round %d: detections counted = %d (total %d), want %d", round, got, metric.total(), detections)
	}
	exact := map[string]bool{}
	for _, node := range nodes[1:] {
		exact[node.ID] = true
		row, err := writes.Get(ctx, "acme", node.ID)
		if err != nil {
			t.Fatalf("round %d: get %s: %v", round, node.ID, err)
		}
		if !session.IsTerminal(row.State) {
			t.Errorf("round %d: member %s is %q after the cleanup, want terminal", round, node.ID, row.State)
		}
	}
	orphanfixture.CheckOnce(t, nodes, writes, archive.Hooks, exact)
	if open := srv.OpenTerminalWork(); open != 0 {
		t.Errorf("round %d: OpenTerminalWork = %d after quiescence, want 0", round, open)
	}
}
