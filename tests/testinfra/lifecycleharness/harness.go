// SPDX-License-Identifier: MIT

package lifecycleharness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordination"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// Tenant is the tenant every harness session belongs to.
const Tenant = "acme"

// Timers the harness applies. Every timer a cell does not fire is longer
// than a cell and its checker run; the resume windows are short so the
// checker's horizon is reached in a few passes.
const (
	// SweepInterval is the coordination Sweeper and watchdog tick interval.
	SweepInterval = 5 * time.Second
	// LeaseTTL is the coordination lease TTL.
	LeaseTTL = 20 * time.Second
	// MaxResumePending is the resume_pending window.
	MaxResumePending = 60 * time.Second
	// MaxResuming is the resuming watchdog budget.
	MaxResuming = 30 * time.Second
	// MaxRetries is the retry budget.
	MaxRetries = 2
	// SealWindow is every replica's §7.1 seal-and-export window, the
	// deployment default.
	SealWindow = sessionserver.DefaultWorkspaceSealMaxDuration
	// ReleaseAllowance is every replica's release allowance
	// (--stream-failure-report-timeout), the deployment default.
	ReleaseAllowance = sessionserver.DefaultStreamFailureReportTimeout
)

// PostTerminalCap is the cap of the §7.1 post-terminal window: the terminal
// pipeline budget the replicas run with. Every session store the harness
// builds counts a terminal session as holding its pod under this cap, and the
// checker's pods-and-claims and slot-set invariants apply the same holder
// predicate on the harness clock.
//
// spec: §7.1 (Terminal pipeline context, Post-terminal window)
var PostTerminalCap = sessionserver.TerminalPipelineBudgetFor(SealWindow, ReleaseAllowance)

// Options configures a harness.
type Options struct {
	// Replicas is the number of gateway replicas; at least one.
	Replicas int
	// Store, when set, is the shared session store; otherwise an in-memory
	// store with the post-terminal cap PostTerminalCap is used. New refuses a
	// store that does not count the post-terminal window under
	// PostTerminalCap (CheckStoreCap), since the checker applies that cap.
	Store sessionstore.Store
	// NewStore, when set and Store is not, builds the shared session store
	// over the harness's stepped clock, so the store's row stamps and the
	// gateway's timers read one clock. It builds the store with the
	// post-terminal cap PostTerminalCap, as PostgresStore and PostgresStoreOn
	// do, and New refuses one that does not (CheckStoreCap).
	NewStore func(now func() time.Time) sessionstore.Store
	// Pods is the number of idle pods seeded before the scenario.
	Pods int
	// CredentialPool, when true, registers one proxy-mode credential pool
	// that every session leases from.
	CredentialPool bool
	// Events, when true, gives every replica a session event bus that
	// relays through the shared Redis, the cross-replica event relay.
	Events bool
	// DurableInbox, when true, keeps every session inbox in the shared
	// Redis rather than in memory.
	DurableInbox bool
	// Recycling, when true, makes the warm pool a recycling pool.
	Recycling bool
	// MaxCreatedSeconds is every replica's watchdog bound on a session
	// that stays in created; zero keeps the watchdog default.
	MaxCreatedSeconds int
	// MaxFinalizingSeconds is every replica's watchdog bound on a session
	// that stays in finalizing; zero keeps the watchdog default.
	MaxFinalizingSeconds int
	// MaxIdleSeconds is every replica's watchdog idle bound; zero keeps it
	// longer than any scenario, so no idle sweep fires.
	MaxIdleSeconds int
	// HeartbeatInterval, when set, is every pod adapter's heartbeat
	// interval, so a runtime's busy reports reach the adapter's activity
	// window within a scenario.
	HeartbeatInterval time.Duration
	// MaxConcurrentSessions, when above 1, makes the warm pool a
	// concurrent-workspace pool with that many slots per pod: every
	// replica's binder gets its own slot counter over the shared Redis, and
	// the gateway pool registry carries the pool's session policy.
	MaxConcurrentSessions int32
	// KeepRuntime, on a recycling harness with an exclusive pool, registers
	// the pool's session policy with recycle.maxSessionsPerPod above 1,
	// recycle.runtimeProcess: keep, and the process-level isolation
	// acknowledgment. The reuse limit makes a same-tenant acquisition rebind
	// a reserved pod rather than end its hold, and the keep setting is what
	// keeps a pod's runtime across the sessions it serves (§5.2 Deployer acknowledgment
	// (runtime process kept across sessions), §4.6.1 within-hold rebind).
	KeepRuntime bool
	// QueueAddr, when set, maps the shared Redis address to the address the
	// session DLQ and inbox client dials, so a scenario can put a
	// partitionable proxy on the held-message path alone.
	QueueAddr func(addr string) string
}

// Harness is the shared world the replicas run in.
type Harness struct {
	T          testing.TB
	Clock      *Clock
	Trace      *Trace
	Store      *TracedStore
	Inner      sessionstore.Store
	Redis      *miniredis.Miniredis
	Leases     leasestore.LeaseStore
	CredLeases *credleasestore.Store
	Creds      *credassign.Service
	Messaging  *sessioninbox.Coordinator
	DLQ        *sessioninbox.DLQ
	Inbox      sessioninbox.Inbox
	Cluster    *Cluster
	Replicas   []*Replica
	// Checkpoints is the suspension checkpointer every replica uses.
	Checkpoints *Checkpointer
	// Interactions is the interaction store every replica shares.
	Interactions *interactionstore.Memory
	// TreeMetrics records every replica's delegation-tree recovery
	// outcomes.
	TreeMetrics *TreeMetrics
	// Claims records every SandboxClaim any replica's binder created.
	Claims *ClaimLog
	// RelaxOrphanClaims applies the crash relaxation of the pods-and-claims
	// invariant: a bound claim whose pod no session holds (the §7.1 holder
	// predicate) and no surviving replica binds is accepted, because the claim-orphan GC
	// reclaims it (§4.6.1 Orphaned SandboxClaim detection, §7.3).
	RelaxOrphanClaims bool
	// Slots records every slot-set reservation and removal the replicas'
	// slot counters made.
	Slots *SlotLog
	// RelaxStaleSlotMembers applies the slot relaxation that matches
	// RelaxOrphanClaims: a plain slot-set member whose session is terminal
	// and past its post-terminal window is accepted, because a release no
	// replica performs leaves it counted until the stale-slot reaper or the
	// orphan GC (§5.2 Slot assignment atomicity). A member in its window is
	// accepted without the relaxation.
	RelaxStaleSlotMembers bool
	// RelaxEmptySetClaims accepts a bound claim on a pod whose slot set is
	// empty and that no session holds, which the claim-orphan GC
	// reclaims (§4.6.1 Orphaned SandboxClaim detection).
	RelaxEmptySetClaims bool

	mu          sync.Mutex
	sessions    map[string]bool
	rc          *redis.Client
	events      bool
	recycling   bool
	idleSeconds int
	// createdSeconds and finalizingSeconds are the watchdog's created- and
	// finalizing-state bounds.
	createdSeconds    int
	finalizingSeconds int
	// slotClient is the Redis client every replica's slot counter uses;
	// its hook feeds Slots.
	slotClient *redis.Client
	// maxConcurrent is the pool's maxConcurrentSessions, and pools the
	// gateway pool registry that carries it; pools is nil on an exclusive
	// pool.
	maxConcurrent int32
	pools         *poolstore.Memory
}

// Replica is one gateway replica: a session server with its own pod
// registry, executor, binder, watchdog, and coordination Sweeper.
type Replica struct {
	ID       string
	Server   *sessionserver.Server
	Registry *podsession.Registry
	Executor *executor.PodExecutor
	Binder   *podsession.Binder
	Watchdog *watchdog.Watchdog
	Sweeper  *coordination.Sweeper
	// SlotCounter is the replica's own slot counter on a concurrent pool,
	// and nil on an exclusive pool.
	SlotCounter *slotcounter.Counter
	// InputWaits is the replica's lenny/request_input registry, shared by
	// its REST and MCP message paths as the gateway wires it.
	InputWaits *inputwait.Registry

	mu      sync.Mutex
	crashed bool
	// k8sDown fails the binder's Kubernetes writes once the replica
	// crashed (crashableClient).
	k8sDown atomic.Bool
	// claimFault is the fault SetClaimFault arms on the binder's
	// SandboxClaim writes.
	claimFault atomic.Pointer[ClaimFault]
}

// SetClaimFault arms f on every SandboxClaim create, status write, and
// delete the replica's binder makes, so a scenario can fail one of them or
// hold it against another actor; nil disarms it. The fault runs on the
// binder's goroutine.
func (r *Replica) SetClaimFault(f ClaimFault) {
	if f == nil {
		r.claimFault.Store(nil)
		return
	}
	r.claimFault.Store(&f)
}

// Crashed reports whether the replica crashed.
func (r *Replica) Crashed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.crashed
}

// New builds the harness. It needs envtest; a test that cannot start it is
// skipped by envtest.Start.
func New(t testing.TB, opts Options) *Harness {
	t.Helper()
	if opts.Replicas < 1 {
		opts.Replicas = 1
	}
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	h := &Harness{
		T:                 t,
		Clock:             NewClock(time.Now().UTC(), mr),
		Trace:             NewTrace(),
		Redis:             mr,
		Leases:            leasestore.New(rc),
		Cluster:           NewClusterWith(t, ClusterOptions{Recycling: opts.Recycling, HeartbeatInterval: opts.HeartbeatInterval}),
		Checkpoints:       &Checkpointer{},
		Interactions:      interactionstore.NewMemory(),
		TreeMetrics:       &TreeMetrics{},
		Claims:            &ClaimLog{},
		sessions:          map[string]bool{},
		rc:                rc,
		events:            opts.Events,
		recycling:         opts.Recycling,
		idleSeconds:       opts.MaxIdleSeconds,
		createdSeconds:    opts.MaxCreatedSeconds,
		finalizingSeconds: opts.MaxFinalizingSeconds,
	}
	h.Slots = NewSlotLog()
	h.slotClient = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	h.slotClient.AddHook(h.Slots)
	t.Cleanup(func() { _ = h.slotClient.Close() })
	if opts.KeepRuntime && !opts.Recycling && opts.MaxConcurrentSessions <= 1 {
		t.Fatal("lifecycleharness: KeepRuntime needs a recycling pool or maxConcurrentSessions above 1")
	}
	if opts.MaxConcurrentSessions > 1 || opts.KeepRuntime {
		mcs := max(opts.MaxConcurrentSessions, 1)
		pools, err := poolPolicyStore(mcs, opts.Recycling)
		if err != nil {
			t.Fatal(err)
		}
		h.maxConcurrent, h.pools = mcs, pools
	}
	h.Inner = opts.Store
	switch {
	case h.Inner != nil:
	case opts.NewStore != nil:
		h.Inner = opts.NewStore(h.Clock.Now)
	default:
		h.Inner = NewMemStore(h.Clock.Now)
	}
	if err := CheckStoreCap(h.Inner); err != nil {
		t.Fatalf("lifecycleharness: %v", err)
	}
	h.Store = NewTracedStore(h.Inner, h.Trace)
	h.CredLeases = credleasestore.New()
	h.Creds = credassign.New(h.CredLeases, credcache.New())
	if opts.CredentialPool {
		h.Creds.RegisterPool(ProxyPool())
	}
	qc := rc
	if opts.QueueAddr != nil {
		qc = redis.NewClient(&redis.Options{Addr: opts.QueueAddr(mr.Addr())})
		t.Cleanup(func() { _ = qc.Close() })
	}
	h.DLQ = sessioninbox.NewDLQ(qc, 100)
	h.Inbox = sessioninbox.NewMemoryInbox(100)
	if opts.DurableInbox {
		h.Inbox = sessioninbox.NewRedisInbox(qc, 100)
	}
	h.Messaging = sessioninbox.NewCoordinator(sessioninbox.Config{Inbox: h.Inbox, DLQ: h.DLQ, Now: h.Clock.Now, Durable: opts.DurableInbox})
	for i := 0; i < opts.Pods; i++ {
		h.Cluster.AddIdlePod(t)
	}
	for i := 0; i < opts.Replicas; i++ {
		h.AddReplica(fmt.Sprintf("rep-%d", i+1))
	}
	t.Cleanup(h.Close)
	return h
}

// NewMemStore returns the harness's default session store: an in-memory
// store on now whose slot queries count a session in its post-terminal
// window under PostTerminalCap, as the gateway's production store does.
//
// spec: §7.1 (Post-terminal window), §5.2 (Post-recovery rehydration
// atomicity)
func NewMemStore(now func() time.Time) *memstore.Store {
	return memstore.NewWithClock(now, memstore.WithPostTerminalCap(PostTerminalCap))
}

// AddReplica starts one more replica.
func (h *Harness) AddReplica(id string) *Replica {
	h.T.Helper()
	reg := podsession.NewRegistry()
	r := &Replica{ID: id, Registry: reg, InputWaits: inputwait.NewRegistry()}
	binder := &podsession.Binder{
		Client:           crashableClient{Client: h.Cluster.Client, crashed: &r.k8sDown, claims: h.Claims, retire: h.Cluster.retireByName, fault: &r.claimFault},
		Namespace:        Namespace,
		AdapterPort:      50051,
		AcceptedVersions: []string{adapter.ProtocolVersionV1},
		DialAdapter:      h.Cluster.Dial,
		Credentials:      h.Creds,
	}
	if h.Concurrent() {
		r.SlotCounter = h.newSlotCounter()
		binder.SlotCounter = r.SlotCounter
	}
	exec := executor.NewPodExecutor(reg, binder)
	r.Executor, r.Binder = exec, binder
	var bus *sessionevents.Bus
	if h.events {
		bus = sessionevents.NewBus(256).WithRedisRelay(sessionevents.NewRedisRelay(h.rc))
	}
	r.Server = sessionserver.New(h.Store, sessionserver.Options{
		DevTenantHeader:            true,
		Events:                     bus,
		IDFunc:                     newIDFunc(id),
		DefaultIsolationProfile:    isolation.ProfileSandboxed,
		PodBinder:                  binder,
		PodRegistry:                reg,
		Executor:                   exec,
		AgentNamespace:             Namespace,
		CoordinationLeaseStore:     h.Leases,
		CoordinationLeaseTTL:       LeaseTTL,
		ReplicaID:                  id,
		Messaging:                  h.Messaging,
		Clock:                      h.Clock.Now,
		MaxResumePending:           MaxResumePending,
		MaxResuming:                MaxResuming,
		SuspensionCheckpointer:     h.Checkpoints,
		Interactions:               h.Interactions,
		TreeRecoveryMetrics:        h.TreeMetrics,
		InputWaits:                 r.InputWaits,
		Pools:                      h.poolRegistry(),
		WorkspaceSealMaxDuration:   SealWindow,
		StreamFailureReportTimeout: ReleaseAllowance,
	})
	// spec: §28.5.1 (Gateway-to-pod) — a failed CH-ATTACH stream is
	// reported to the replica's session server, as the gateway wires it.
	exec.SetStreamFailureHandler(r.Server.ReportAttachStreamFailure)
	r.Watchdog = watchdog.New(h.Store, watchdog.StaticTenants{Tenant}, watchdog.Config{
		MaxResumePendingSeconds:        int(MaxResumePending / time.Second),
		MaxResumingSeconds:             int(MaxResuming / time.Second),
		MaxRetries:                     MaxRetries,
		MaxIdleSeconds:                 h.watchdogIdleSeconds(),
		MaxCreatedSeconds:              h.createdSeconds,
		MaxFinalizingSeconds:           h.finalizingSeconds,
		MaxSessionAgeSeconds:           100000,
		MaxAwaitingClientActionSeconds: 100000,
	}, h.Clock.Now).WithTerminalHook(r.Server).
		WithSuspendedPodReleaser(r.Server).
		WithBacklogResumer(r.Server).
		WithIdleSuspender(r.Server).
		WithIdleResolver(idleResolver{}).
		WithMessaging(h.Messaging)
	r.Sweeper = coordination.NewSweeper(watchdog.StaticTenants{Tenant}, h.Store, h.Leases, coordination.Options{
		ReplicaID:        id,
		TTL:              LeaseTTL,
		Interval:         SweepInterval,
		Bindings:         registryBindings{reg: reg, exec: exec},
		HeldDeliverer:    r.Server,
		TerminalReleaser: r.Server,
		Readopter:        readopter{h: h, r: r},
		Clock:            h.Clock.Now,
	})
	h.mu.Lock()
	h.Replicas = append(h.Replicas, r)
	h.mu.Unlock()
	return r
}

// Crash ends the replica's server-lifetime context, drops its pod registry
// and stream cache, fails every later Kubernetes write of its binder, and
// leaves its coordination leases to lapse.
func (h *Harness) Crash(r *Replica) {
	r.mu.Lock()
	if r.crashed {
		r.mu.Unlock()
		return
	}
	r.crashed = true
	r.k8sDown.Store(true)
	r.mu.Unlock()
	r.Server.Close()
	for _, b := range r.Registry.Snapshot() {
		r.Registry.Remove(b.SessionID)
		r.Executor.EvictStream(b.SessionID)
		if b.Adapter != nil {
			b.Adapter.Close()
		}
	}
}

// Surviving returns the replicas that have not crashed.
func (h *Harness) Surviving() []*Replica {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []*Replica
	for _, r := range h.Replicas {
		if !r.Crashed() {
			out = append(out, r)
		}
	}
	return out
}

// Close closes every replica.
func (h *Harness) Close() {
	for _, r := range h.Replicas {
		r.Server.Close()
	}
}

// Track records a session the scenario created, so the checker asserts its
// invariants.
func (h *Harness) Track(id string) {
	h.mu.Lock()
	h.sessions[id] = true
	h.mu.Unlock()
}

// Sessions returns the tracked session IDs.
func (h *Harness) Sessions() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.sessions))
	for id := range h.sessions {
		out = append(out, id)
	}
	return out
}

// Row reads a session row without recording a trace entry.
func (h *Harness) Row(id string) sessionstore.Session {
	h.T.Helper()
	row, err := h.Inner.Get(context.Background(), Tenant, id)
	if err != nil {
		h.T.Fatalf("read %s: %v", id, err)
	}
	return row
}

// Do sends an HTTP request to the replica's session API and returns the
// status and the decoded body.
func (h *Harness) Do(r *Replica, method, path string, body any) (int, map[string]any) {
	h.T.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("X-Lenny-Tenant-ID", Tenant)
	rr := httptest.NewRecorder()
	r.Server.Handler().ServeHTTP(rr, req)
	out := map[string]any{}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out
}

// Events reads the session's event list from the replica's JSON events
// view and returns the decoded items.
func (h *Harness) Events(r *Replica, id string) (int, []any) {
	h.T.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+id+"/events", nil)
	req.Header.Set("X-Lenny-Tenant-ID", Tenant)
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()
	r.Server.Handler().ServeHTTP(rr, req)
	var out struct {
		Items []any `json:"items"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out.Items
}

// StartSession creates and starts a running session on the replica and
// tracks it.
func (h *Harness) StartSession(r *Replica) string {
	h.T.Helper()
	id, code, body := h.TryStartSession(r)
	if code != http.StatusCreated {
		h.T.Fatalf("create-and-start on %s: status %d body %v", r.ID, code, body)
	}
	if st := h.Row(id).State; st != session.StateRunning {
		h.T.Fatalf("session %s state = %q after start, want running", id, st)
	}
	return id
}

// TryStartSession creates and starts a session on the replica and returns
// its ID (empty when the create failed), the answer's status, and its body.
// A created session is tracked.
func (h *Harness) TryStartSession(r *Replica) (string, int, map[string]any) {
	h.T.Helper()
	code, body := h.Do(r, http.MethodPost, "/v1/sessions/start", sessionserver.CreateAndStartRequest{
		RuntimeRef: RuntimeRef, UserID: "alice@acme.com",
	})
	id, _ := body["id"].(string)
	if id != "" {
		h.Track(id)
	}
	return id, code, body
}

// CreateSession creates a session on the replica without starting it, as
// the two-step create path does, and tracks it. The session's row stays
// in a pre-running state (created, or ready once finalized), and on a
// concurrent pool the create already reserved the session's slot.
func (h *Harness) CreateSession(r *Replica) string {
	h.T.Helper()
	code, body := h.Do(r, http.MethodPost, "/v1/sessions", sessionserver.CreateSessionRequest{
		RuntimeRef: RuntimeRef, UserID: "alice@acme.com",
	})
	id, _ := body["id"].(string)
	if code != http.StatusCreated || id == "" {
		h.T.Fatalf("create on %s: status %d body %v", r.ID, code, body)
	}
	h.Track(id)
	return id
}

// registryBindings is the Sweeper's view of one replica's pod registry.
type registryBindings struct {
	reg  *podsession.Registry
	exec *executor.PodExecutor
}

func (b registryBindings) Bound(id string) bool {
	_, ok := b.reg.Get(id)
	return ok
}

func (b registryBindings) ConnAlive(id string) bool {
	bind, ok := b.reg.Get(id)
	return ok && bind.Adapter != nil && bind.Adapter.Alive()
}

func (b registryBindings) EvictBinding(id string) {
	b.reg.Remove(id)
	b.exec.EvictStream(id)
}

func (b registryBindings) BindingGeneration(id string) int64 {
	if bind, ok := b.reg.Get(id); ok {
		return bind.CoordinationGeneration
	}
	return 0
}

var (
	idMu  sync.Mutex
	idSeq int
)

// newIDFunc returns UUID-shaped session IDs unique across replicas.
func newIDFunc(replica string) func() string {
	return func() string {
		idMu.Lock()
		defer idMu.Unlock()
		idSeq++
		return fmt.Sprintf("00000000-0000-4000-8000-%012d", idSeq)
	}
}

// TakeOver hands the session to a surviving replica other than from, as a
// peer's coordination takeover does: the lease moves to the peer and the
// row's coordination generation advances.
func (h *Harness) TakeOver(sessionID string, from *Replica) {
	var peer *Replica
	for _, r := range h.Surviving() {
		if r != from {
			peer = r
			break
		}
	}
	if peer == nil {
		return
	}
	ctx := context.Background()
	_ = h.Leases.Release(ctx, Tenant, sessionID, from.ID)
	if _, err := h.Leases.Acquire(ctx, Tenant, sessionID, peer.ID, LeaseTTL); err != nil {
		return
	}
	_, _ = h.Inner.Update(ctx, Tenant, sessionID, func(r *sessionstore.Session) error {
		r.CoordinationGeneration++
		return nil
	})
}

// poolRegistry is the gateway pool registry the session servers read: the
// concurrent pool's registry, or none on an exclusive pool, so a nil
// *poolstore.Memory never reaches the interface-typed option.
func (h *Harness) poolRegistry() poolstore.Store {
	if h.pools == nil {
		return nil
	}
	return h.pools
}

// watchdogIdleSeconds is the watchdog idle bound: Options.MaxIdleSeconds,
// or a bound longer than any scenario.
func (h *Harness) watchdogIdleSeconds() int {
	if h.idleSeconds > 0 {
		return h.idleSeconds
	}
	return 100000
}

// releaseMarkerSite is the gateway call site of the §7.1 release-marker
// write, as the trace names it.
const releaseMarkerSite = "recordPodRelease"

// WatchReleaseMarker restarts trace recording so AwaitReleaseMarkerWrite
// observes the next release-marker write. With fail set it also arms a store
// error on that write, so the release runs and its marker is not recorded,
// which leaves the session in its post-terminal window until the cap.
//
// spec: §7.1 (Post-terminal window)
func (h *Harness) WatchReleaseMarker(fail bool) {
	if fail {
		h.Trace.ArmMatching("Update", releaseMarkerSite, FaultStoreError, nil)
		return
	}
	h.Trace.Start()
}

// AwaitReleaseMarkerWrite waits until a replica has made the release-marker
// write for the session since WatchReleaseMarker, whether the write applied
// or failed, and fails the test after ten seconds.
//
// When the terminal write landed on a replica other than the one that binds
// the session, the binding replica releases the pod from its coordination
// Sweeper's terminal binding release and makes the marker write there. The
// harness runs no Sweeper loop, so the wait runs a sweep on every surviving
// replica between polls. The sweep leaves the harness clock where it is, so
// the wait does not move the session toward its post-terminal cap.
//
// spec: §7.1 (Post-terminal window), §10.1.1 (Stateless Replicas and
// Per-Session Coordination)
func (h *Harness) AwaitReleaseMarkerWrite(t testing.TB, id string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if h.releaseMarkerWritten(id) {
			h.Settle()
			return
		}
		h.sweepSurviving(context.Background())
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no replica made the release-marker write for %s", id)
}

// releaseMarkerWritten reports whether the trace holds a release-marker
// write for the session.
func (h *Harness) releaseMarkerWritten(id string) bool {
	for _, e := range h.Trace.Entries() {
		if e.Call == "Update" && e.SessionID == id && strings.Contains(e.Site, releaseMarkerSite) {
			return true
		}
	}
	return false
}

// sweepSurviving runs one coordination sweep on every surviving replica
// without advancing the harness clock.
func (h *Harness) sweepSurviving(ctx context.Context) {
	for _, r := range h.Surviving() {
		_, _ = r.Sweeper.Sweep(ctx)
	}
}
