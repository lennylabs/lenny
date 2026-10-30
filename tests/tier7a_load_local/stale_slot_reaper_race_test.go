// SPDX-License-Identifier: MIT

//go:build load_local

package tier7a_load_local_test

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/slotreaper"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

// callerKey is the gRPC metadata key naming which replica sent a Shutdown.
const callerKey = "x-race-caller"

// Callers of the fake adapter's Shutdown.
const (
	callerReaper = "reaper"
	callerLate   = "late"
)

// teardownAdapter serializes the Shutdown of one session the way §4.7.1
// rule 11 requires of the adapter. The pod still runs the session's runtime
// when the round starts. The first Shutdown naming it holds the identifier
// while a teardown runs for a randomized delay and then answers the
// teardown's outcome. A Shutdown that arrives while the teardown is in
// flight answers an unclean exit at once, and one that arrives after it
// answers the outcome of the teardown that ran. Every other identifier is
// passed to the real adapter, which answers absent with a clean exit.
type teardownAdapter struct {
	session string
	delay   time.Duration
	fails   bool
	// stillCounted reports whether the session still holds a slot in the
	// pod's set, as its plain member or as the leaked member a release
	// swaps in when its Shutdown meets this teardown in flight (§6.2); it
	// is read when a teardown ends, before the answer.
	stillCounted func() bool

	mu       sync.Mutex
	present  bool
	inFlight bool
	unclean  bool
	started  chan struct{}
	answers  map[string][]bool
	// removedDuringTeardown records that the member left the set while
	// the teardown was still running.
	removedDuringTeardown bool
}

func newTeardownAdapter(sessionID string, delay time.Duration, fails bool, stillCounted func() bool) *teardownAdapter {
	return &teardownAdapter{
		session: sessionID, delay: delay, fails: fails, stillCounted: stillCounted,
		present: true, started: make(chan struct{}), answers: map[string][]bool{},
	}
}

// intercept is the unary server interceptor that applies the rule to the
// session's Shutdown.
func (a *teardownAdapter) intercept(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
	sr, ok := req.(*adapterv1.ShutdownRequest)
	if !ok || !strings.HasSuffix(info.FullMethod, "/Shutdown") || sr.GetSessionId().GetValue() != a.session {
		return next(ctx, req)
	}
	clean := a.shutdown()
	a.mu.Lock()
	caller := ""
	if md, ok := metadata.FromIncomingContext(ctx); ok && len(md.Get(callerKey)) > 0 {
		caller = md.Get(callerKey)[0]
	}
	a.answers[caller] = append(a.answers[caller], clean)
	a.mu.Unlock()
	return &adapterv1.ShutdownResponse{ExitedCleanly: clean}, nil
}

// shutdown runs one Shutdown of the session and reports its exit.
func (a *teardownAdapter) shutdown() bool {
	a.mu.Lock()
	switch {
	case a.inFlight:
		a.mu.Unlock()
		return false
	case !a.present:
		clean := !a.unclean
		a.mu.Unlock()
		return clean
	}
	a.present, a.inFlight = false, true
	close(a.started)
	a.mu.Unlock()
	time.Sleep(a.delay)
	counted := a.stillCounted()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.inFlight, a.unclean = false, a.fails
	a.removedDuringTeardown = a.removedDuringTeardown || !counted
	return !a.fails
}

// answer returns the exits the caller's Shutdowns received.
func (a *teardownAdapter) answer(caller string) []bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]bool(nil), a.answers[caller]...)
}

// reapRace is one round's pod: a slot set holding a stale terminal member
// and a live sibling, a fake adapter for the stale session, the reaper's
// binder, and another replica's binder that runs the late release.
type reapRace struct {
	*slotRaceEnv
	store   *memstore.Store
	adapter *teardownAdapter
	lis     *bufconn.Listener
	reaper  *podsession.Binder
	late    *podsession.Binder
}

func newReapRace(t *testing.T, stale string, delay time.Duration, fails bool) *reapRace {
	t.Helper()
	e := newSlotRaceEnv(t, "claimed")
	f := &reapRace{slotRaceEnv: e, store: memstore.New(), lis: bufconn.Listen(1 << 20)}
	f.adapter = newTeardownAdapter(stale, delay, fails, func() bool {
		set := f.members(t)
		return set[stale] || set[slotcounter.LeakedMember(stale)]
	})
	srv := adapter.New("reap-race")
	srv.WorkspaceBase = t.TempDir()
	gs := adapter.NewGRPCServer(srv, grpc.ChainUnaryInterceptor(f.adapter.intercept))
	go func() { _ = gs.Serve(f.lis) }()
	t.Cleanup(gs.Stop)
	f.reaper = f.binder(callerReaper)
	f.late = f.binder(callerLate)
	return f
}

// dial connects to the fake adapter, naming the caller on every RPC.
func (f *reapRace) dial(caller string) (*adapterclient.Client, error) {
	return adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return f.lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			return invoker(metadata.AppendToOutgoingContext(ctx, callerKey, caller), method, req, reply, cc, opts...)
		}))
}

// binder returns one replica's binder, with its own slot counter, whose
// adapter connections name the caller.
func (f *reapRace) binder(caller string) *podsession.Binder {
	return &podsession.Binder{
		Client: f.kube, Namespace: slotRaceNS, AdapterPort: 50051,
		AcceptedVersions: []string{adapter.ProtocolVersionV1}, SlotCounter: f.counter(),
		DialAdapter: func(string) (*adapterclient.Client, error) { return f.dial(caller) },
	}
}

// seedRow writes a per-slot session row naming the pod, terminal at
// terminatedAt when that is non-zero.
func (f *reapRace) seedRow(t *testing.T, id string, terminatedAt time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := f.store.Create(ctx, sessionstore.Session{
		ID: id, TenantID: "acme", State: session.StateRunning, PodAssignment: slotRacePod, ScrubPolicy: "best-effort-per-slot",
	}); err != nil {
		t.Fatal(err)
	}
	if terminatedAt.IsZero() {
		return
	}
	if _, err := f.store.Update(ctx, "acme", id, func(r *sessionstore.Session) error {
		r.State = session.StateCompleted
		sessionstore.MarkTerminated(r, session.StateRunning, terminatedAt)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// lateRelease is the normal release another replica runs for the session:
// the terminal binding release's binder call, which sends the session's
// Shutdown over the published binding's connection and then runs the keyed
// release, leaked when that Shutdown did not report a clean exit.
func (f *reapRace) lateRelease(t *testing.T, round int, stale string) {
	cl, err := f.dial(callerLate)
	if err != nil {
		t.Errorf("round %d: dial for the late release: %v", round, err)
		return
	}
	if err := f.late.ReleaseSlot(context.Background(), &podsession.BindResult{
		SessionID: stale, SandboxName: slotRacePod, Adapter: cl,
	}); err != nil {
		t.Errorf("round %d: late release: %v", round, err)
	}
}

// spec: §5.2 (Slot assignment atomicity), §6.2 (leaked slot semantics),
// §4.7.1 (role and gateway RPC contract), rule 11
// diagnosis: a stale-slot reaper pass raced a late normal release of the same
// terminal session on another replica, over an adapter that holds the
// session's identifier while a teardown runs. The reaper removed the member
// while the teardown was in flight (it acted on an unclean answer), the
// member was removed twice or left counted, the late release's leaked:
// member did not match the exit its Shutdown received, the live sibling's
// member was removed, or the claim left bound.
func TestStaleSlotReaperRacesALateRelease_spec_5_2(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	outcomes := map[string]int{}
	for round := 0; round < slotRounds/2; round++ {
		stale, live := fmt.Sprintf("00000000-0000-4000-8000-%012d", round), "00000000-0000-4000-8000-999999999999"
		// Every other round starts the reaper only after the late release's
		// teardown has begun, so its Shutdown always meets the teardown in
		// flight; the others interleave freely.
		inFlight := round%2 == 0
		delay := time.Duration(1+rng.Intn(5)) * time.Millisecond
		if inFlight {
			delay = time.Duration(30+rng.Intn(30)) * time.Millisecond
		}
		f := newReapRace(t, stale, delay, rng.Intn(3) == 0)
		f.seedBoundClaim(t, stale, live)
		f.seedRow(t, stale, time.Now().Add(-time.Hour))
		f.seedRow(t, live, time.Time{})
		reaper, err := slotreaper.New(slotreaper.Config{
			Pods: f.store, Members: f.counter(), Sessions: f.store, Releaser: f.reaper,
			Rate: 100, Interval: time.Second, TerminalAge: time.Minute,
		})
		if err != nil {
			t.Fatal(err)
		}
		if inFlight {
			done := make(chan struct{})
			go func() { defer close(done); f.lateRelease(t, round, stale) }()
			<-f.adapter.started
			reaper.Pass(context.Background())
			<-done
		} else {
			raceJittered(rng,
				func() { reaper.Pass(context.Background()) },
				func() { f.lateRelease(t, round, stale) })
		}
		outcomes[f.checkReapRound(t, round, stale, live, inFlight)]++
	}
	t.Logf("outcomes: %v", outcomes)
}

// checkReapRound asserts one round's outcome and names it.
func (f *reapRace) checkReapRound(t *testing.T, round int, stale, live string, inFlight bool) string {
	t.Helper()
	if f.adapter.removedDuringTeardown {
		t.Fatalf("round %d: the stale member left the set while a teardown of its session was in flight", round)
	}
	late, reaper := f.adapter.answer(callerLate), f.adapter.answer(callerReaper)
	if len(late) != 1 {
		t.Fatalf("round %d: the late release sent %d Shutdowns, want 1", round, len(late))
	}
	if inFlight && (len(reaper) != 1 || reaper[0]) {
		t.Fatalf("round %d: the reaper's Shutdown during the teardown answered %v, want one unclean exit", round, reaper)
	}
	set := f.members(t)
	switch {
	case set[stale] || !set[live]:
		t.Fatalf("round %d: set %v, want the stale member gone and the live sibling counted", round, set)
	case set[slotcounter.LeakedMember(stale)] == late[0]:
		t.Fatalf("round %d: the late release's Shutdown answered clean=%v, set %v; a leaked: member must follow an unclean exit and only one", round, late[0], set)
	case f.log.Removals(slotRacePod, stale) != 1:
		t.Fatalf("round %d: removals of the stale member = %d, want 1", round, f.log.Removals(slotRacePod, stale))
	}
	if got := f.claimPhase(t); got != string(claimstate.Bound) {
		t.Fatalf("round %d: claim = %q, want bound", round, got)
	}
	return fmt.Sprintf("late clean=%v reaper=%v", late[0], reaper)
}
