// SPDX-License-Identifier: MIT

//go:build load_local

package tier7a_load_local_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/tests/testinfra/gatewaypki"
	"github.com/lennylabs/lenny/tests/testinfra/inputwaitrig"
)

// Store failure modes the fault store injects into the next Update.
const (
	failNone int32 = iota
	failBeforeCommit
	failAfterCommit
)

var errInjectedWrite = errors.New("injected session store failure")

// faultStore is the session store every replica shares. It fails the next
// Update before or after committing it when armed, and runs an optional hook
// after each Update that reached the wrapped store.
type faultStore struct {
	sessionstore.Store
	next atomic.Int32
	mu   sync.Mutex
	hook func(id string, err error)
}

func (f *faultStore) Update(ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error) (sessionstore.Session, error) {
	switch f.next.Swap(failNone) {
	case failBeforeCommit:
		return sessionstore.Session{}, errInjectedWrite
	case failAfterCommit:
		if _, err := f.Store.Update(ctx, tenantID, id, mutate); err != nil {
			return sessionstore.Session{}, err
		}
		return sessionstore.Session{}, errInjectedWrite
	}
	row, err := f.Store.Update(ctx, tenantID, id, mutate)
	f.mu.Lock()
	hook := f.hook
	f.mu.Unlock()
	if hook != nil {
		hook(id, err)
	}
	return row, err
}

func (f *faultStore) setHook(h func(id string, err error)) {
	f.mu.Lock()
	f.hook = h
	f.mu.Unlock()
}

// recoveryPod is one pod process: its sessions and its current
// LNK-GWCONTROL connection.
type recoveryPod struct {
	name     string
	sessions []string
	conn     adapterv1.GatewayControlClient
	server   int
}

// recoveryWait is one pending lenny/request_input call.
type recoveryWait struct {
	server    *inputwaitrig.Server
	sessionID string
	requestID string
	waitID    string
	done      <-chan inputwaitrig.Result
}

// recoveryRig is the replicas, the pods, and the pending waits of one
// randomized run over one shared store.
type recoveryRig struct {
	t       *testing.T
	rng     *rand.Rand
	pki     *gatewaypki.Material
	store   *faultStore
	servers []*inputwaitrig.Server
	pods    []*recoveryPod
	waits   []*recoveryWait
}

func newRecoveryRig(t *testing.T, rng *rand.Rand, servers int) *recoveryRig {
	t.Helper()
	r := &recoveryRig{t: t, rng: rng, pki: gatewaypki.Mint(t), store: &faultStore{Store: memstore.New()}}
	for i := 0; i < servers; i++ {
		r.servers = append(r.servers, r.startServer())
	}
	now := time.Now().UTC()
	for _, name := range []string{"pod-a", "pod-b"} {
		p := &recoveryPod{name: name}
		for i := 1; i <= 2; i++ {
			id := fmt.Sprintf("00000000-0000-4000-8000-%s%07d", name[len(name)-1:]+"0000", i)
			if err := r.store.Create(context.Background(), sessionstore.Session{
				ID: id, TenantID: "acme", UserID: "alice@acme.com", RuntimeRef: "echo",
				State: session.StateRunning, PodAssignment: name, CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				t.Fatalf("seed %s: %v", id, err)
			}
			p.sessions = append(p.sessions, id)
		}
		r.pods = append(r.pods, p)
		r.redial(p, rng.Intn(servers))
	}
	return r
}

func (r *recoveryRig) startServer() *inputwaitrig.Server {
	return inputwaitrig.Start(r.t, inputwaitrig.Options{Store: r.store, PKI: r.pki})
}

// redial opens the pod's next connection to server i and sends a call
// carrying a session ID on it.
func (r *recoveryRig) redial(p *recoveryPod, i int) {
	p.server = i
	p.conn = r.servers[i].Dial(r.t, inputwaitrig.AgentURI(p.name))
	r.ping(p)
}

// ping sends a call carrying one of the pod's session IDs on its current
// connection, which runs the recovery when the connection is not recovered.
func (r *recoveryRig) ping(p *recoveryPod) {
	r.t.Helper()
	if err := inputwaitrig.Ping(context.Background(), p.conn, p.sessions[0]); err != nil {
		r.t.Fatalf("ping %s: %v", p.name, err)
	}
}

// enter parks a new wait on a session of a random pod, over the pod's
// current connection, and waits until its entry write names it.
func (r *recoveryRig) enter() {
	p := r.pods[r.rng.Intn(len(r.pods))]
	srv := r.servers[p.server]
	sessionID := p.sessions[r.rng.Intn(len(p.sessions))]
	before := srv.Waits.PendingForSession(sessionID)
	done := inputwaitrig.RequestInput(context.Background(), p.conn, sessionID)
	reqID := srv.WaitPending(r.t, sessionID, before...)
	var waitID string
	for _, pd := range srv.Waits.PendingDetailsForSession(sessionID) {
		if pd.RequestID == reqID {
			waitID = pd.WaitID
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for r.row(sessionID).InputWaitID != waitID {
		if time.Now().After(deadline) {
			r.t.Fatalf("the entry write of %s/%s never named %q", sessionID, reqID, waitID)
		}
		time.Sleep(time.Millisecond)
	}
	r.waits = append(r.waits, &recoveryWait{server: srv, sessionID: sessionID, requestID: reqID, waitID: waitID, done: done})
}

// takeWait removes and returns a random pending wait.
func (r *recoveryRig) takeWait() *recoveryWait {
	i := r.rng.Intn(len(r.waits))
	w := r.waits[i]
	r.waits = append(r.waits[:i], r.waits[i+1:]...)
	return w
}

// exit ends w by an answer or a cancellation, with the store armed to fail
// the next write as mode says, and returns once the call has returned,
// which is after its exit write.
func (r *recoveryRig) exit(w *recoveryWait, mode int32) {
	r.store.next.Store(mode)
	endWait(w, r.rng.Intn(2) == 0)
	inputwaitrig.Await(r.t, w.done)
	r.store.next.Store(failNone)
}

// endWait answers w when resolve is set and cancels it otherwise.
func endWait(w *recoveryWait, resolve bool) {
	if resolve {
		_ = w.server.Waits.Resolve(w.sessionID, w.requestID, "yes")
		return
	}
	w.server.Waits.Cancel(w.sessionID, w.requestID)
}

// redialDuringFailedExit opens the next connection of a waiting session's pod
// and sends its first call while that wait's exit write fails, so the
// replica re-arms while the new connection's recovery run is in flight. When
// the new connection is on the wait's own replica and the row names the wait,
// the run's superseded write on that session holds the run until the failed
// exit write has returned, which leaves the row naming a wait no registry
// holds after a run that started before the re-arm. Otherwise the exit and
// the run race freely.
func (r *recoveryRig) redialDuringFailedExit() {
	w := r.takeWait()
	p := r.podOf(w.sessionID)
	i := r.rng.Intn(len(r.servers))
	mode := failBeforeCommit + int32(r.rng.Intn(2))
	resolve := r.rng.Intn(2) == 0
	failExit := func() {
		r.store.next.Store(mode)
		endWait(w, resolve)
		<-w.done
		r.store.next.Store(failNone)
	}
	var wg sync.WaitGroup
	if r.servers[i] == w.server && r.row(w.sessionID).InputWaitID == w.waitID {
		var held atomic.Bool
		r.store.setHook(func(id string, err error) {
			if id == w.sessionID && errors.Is(err, sessionstore.ErrSuperseded) && held.CompareAndSwap(false, true) {
				failExit()
			}
		})
		defer r.store.setHook(nil)
		defer func() {
			if !held.Load() {
				failExit()
			}
		}()
	} else {
		wg.Add(1)
		go func() {
			defer wg.Done()
			failExit()
		}()
	}
	p.server = i
	p.conn = r.servers[i].Dial(r.t, inputwaitrig.AgentURI(p.name))
	_ = inputwaitrig.Ping(context.Background(), p.conn, p.sessions[0])
	wg.Wait()
}

// restart replaces replica i, by a process kill that loses its waits or by
// a graceful stop that drains them, and moves its pods to a live replica.
func (r *recoveryRig) restart(i int) {
	old := r.servers[i]
	kept := r.waits[:0]
	var ended []*recoveryWait
	for _, w := range r.waits {
		if w.server == old {
			ended = append(ended, w)
		} else {
			kept = append(kept, w)
		}
	}
	r.waits = kept
	if r.rng.Intn(2) == 0 {
		old.Kill()
	} else {
		old.Stop()
	}
	for _, w := range ended {
		inputwaitrig.Await(r.t, w.done)
	}
	r.servers[i] = r.startServer()
	for _, p := range r.pods {
		if p.server == i {
			r.redial(p, r.rng.Intn(len(r.servers)))
		}
	}
}

func (r *recoveryRig) podOf(sessionID string) *recoveryPod {
	for _, p := range r.pods {
		for _, s := range p.sessions {
			if s == sessionID {
				return p
			}
		}
	}
	r.t.Fatalf("no pod holds %s", sessionID)
	return nil
}

func (r *recoveryRig) row(id string) sessionstore.Session {
	r.t.Helper()
	row, err := r.store.Get(context.Background(), "acme", id)
	if err != nil {
		r.t.Fatalf("get %s: %v", id, err)
	}
	return row
}

// quiesce brings every pod's current connection through a completed
// recovery run since the last re-arm. No exit write or recovery run is in
// flight when it returns.
func (r *recoveryRig) quiesce() {
	for _, p := range r.pods {
		r.ping(p)
	}
}

// checkInvariant asserts that no row is input_required unless its
// InputWaitID names a wait pending in a live replica's registry.
func (r *recoveryRig) checkInvariant(step int, op string) {
	r.t.Helper()
	for _, p := range r.pods {
		for _, id := range p.sessions {
			row := r.row(id)
			if row.State != session.StateInputRequired {
				continue
			}
			if !r.pendingAnywhere(id, row.InputWaitID) {
				r.t.Fatalf("step %d (%s): %s is input_required naming %q, which no registry holds", step, op, id, row.InputWaitID)
			}
		}
	}
}

func (r *recoveryRig) pendingAnywhere(sessionID, waitID string) bool {
	for _, srv := range r.servers {
		for _, pd := range srv.Waits.PendingDetailsForSession(sessionID) {
			if pd.WaitID == waitID {
				return true
			}
		}
	}
	return false
}

// step runs one random event and returns its name.
func (r *recoveryRig) step() string {
	if len(r.waits) == 0 {
		r.enter()
		return "enter"
	}
	switch r.rng.Intn(7) {
	case 0, 1:
		if len(r.waits) < 6 {
			r.enter()
			return "enter"
		}
		r.exit(r.takeWait(), failNone)
		return "exit"
	case 2:
		r.exit(r.takeWait(), failNone)
		return "exit"
	case 3:
		mode := failBeforeCommit + int32(r.rng.Intn(2))
		r.exit(r.takeWait(), mode)
		return fmt.Sprintf("failed exit write (mode %d)", mode)
	case 4:
		p := r.pods[r.rng.Intn(len(r.pods))]
		r.redial(p, r.rng.Intn(len(r.servers)))
		return "redial " + p.name
	case 5:
		r.redialDuringFailedExit()
		return "redial during a failed exit write"
	default:
		i := r.rng.Intn(len(r.servers))
		r.restart(i)
		return fmt.Sprintf("restart replica %d", i)
	}
}

// spec: 7.2 (Session state machine), 28.3 (Link register)
// diagnosis: after a quiescent step in which every pod's current
// LNK-GWCONTROL connection had completed a recovery run since its replica's
// last re-arm, a session row was input_required naming a wait no registry
// held. A failed exit write did not re-arm the replica, a recovery run that a
// re-arm overtook still marked its connection recovered, a recovery left a
// row naming a wait pending only on another replica, or a killed or drained
// replica's lost wait was not restored on the pod's next connection.
// Re-run with the logged seed to reproduce the interleaving.
func TestInputWaitRecoveryKeepsRowsOnLiveWaits_spec_28_3(t *testing.T) {
	for _, servers := range []int{1, 2} {
		t.Run(fmt.Sprintf("%d replicas", servers), func(t *testing.T) {
			seed := time.Now().UnixNano()
			t.Logf("seed %d", seed)
			rng := rand.New(rand.NewSource(seed))
			r := newRecoveryRig(t, rng, servers)
			for step := 0; step < 150; step++ {
				op := r.step()
				r.quiesce()
				r.checkInvariant(step, op)
			}
			for len(r.waits) > 0 {
				r.exit(r.takeWait(), failNone)
			}
			r.quiesce()
			for _, p := range r.pods {
				for _, id := range p.sessions {
					if row := r.row(id); row.State != session.StateRunning {
						t.Errorf("after every wait ended %s is %s/%q, want running", id, row.State, row.InputWaitID)
					}
				}
			}
		})
	}
}
