// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage for the closing gate the terminal
// seal sets on a session's binding.
//
// When the seal starts, the gateway marks the session's binding closing and
// evicts its held Attach stream, so no turn runs while the export reads the
// workspace. In production that seal start races the session's concurrent
// Sends, the coordination sweep's stream eviction, and the binding release.
// These cases drive those actors together in random orders under the race
// detector and assert the invariants a race would break: no message whose
// Send began after the mark reaches the runtime, a Send after the mark is
// refused with nothing delivered, the binding is released exactly once
// although two releases race, and no stream goroutine outlives the release.
//
// spec: §7.1 (Delivery and checkpoints during the seal), §28.5.1
// (Gateway-to-pod).
package tier7a_load_local_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// closingRecordAdapter answers every message envelope with a response and
// records the text of every message that reached it and every Shutdown it
// received, keyed by session.
type closingRecordAdapter struct {
	adapterv1.UnimplementedAdapterServer

	mu        sync.Mutex
	received  map[string]bool
	shutdowns map[string]int
}

func newClosingRecordAdapter() *closingRecordAdapter {
	return &closingRecordAdapter{received: map[string]bool{}, shutdowns: map[string]int{}}
}

func (a *closingRecordAdapter) Shutdown(_ context.Context, req *adapterv1.ShutdownRequest) (*adapterv1.ShutdownResponse, error) {
	a.mu.Lock()
	a.shutdowns[req.GetSessionId().GetValue()]++
	a.mu.Unlock()
	return &adapterv1.ShutdownResponse{ExitedCleanly: true}, nil
}

func (a *closingRecordAdapter) Attach(stream grpc.BidiStreamingServer[adapterv1.AttachRequest, adapterv1.AttachResponse]) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	for {
		req, err := stream.Recv()
		if err != nil {
			return err
		}
		var in heldInbound
		if json.Unmarshal(req.GetEnvelopeJson(), &in) != nil || in.Type != "message" || len(in.Input) == 0 {
			continue
		}
		a.mu.Lock()
		a.received[in.Input[0].Inline] = true
		a.mu.Unlock()
		b, _ := json.Marshal(map[string]any{"type": "response", "text": "echo:" + in.Input[0].Inline})
		if err := stream.Send(&adapterv1.AttachResponse{EnvelopeJson: b}); err != nil {
			return err
		}
	}
}

func (a *closingRecordAdapter) reached(text string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.received[text]
}

func (a *closingRecordAdapter) shutdownsFor(sessionID string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.shutdowns[sessionID]
}

// closingRun is one session's actors and the record the invariants are
// checked against.
type closingRun struct {
	id   string
	e    *executor.PodExecutor
	reg  *podsession.Registry
	rng  *rand.Rand
	mu   sync.Mutex
	gate []string // texts of Sends that began after the mark or a release

	marked   atomic.Bool
	released atomic.Bool
}

// recordGated notes a Send that began after the mark or a release.
func (r *closingRun) recordGated(text string) {
	r.mu.Lock()
	r.gate = append(r.gate, text)
	r.mu.Unlock()
}

// send runs n Sends and checks that each Send that began after the mark is
// refused with nothing delivered.
func (r *closingRun) send(t *testing.T, sender, n int) {
	for i := 0; i < n; i++ {
		text := fmt.Sprintf("%s/%d/%d", r.id, sender, i)
		afterMark := r.marked.Load()
		gated := afterMark || r.released.Load()
		ctx, cancel := context.WithTimeout(context.Background(), heldWait)
		_, err := r.e.Send(ctx, r.id, []executor.Message{{Role: "user", Content: text}})
		cancel()
		if gated {
			r.recordGated(text)
		}
		if afterMark && !r.released.Load() {
			var sealed *executor.SealedError
			if errors.As(err, &sealed) && sealed.Delivered != 0 {
				t.Errorf("Send %s after the mark reported %d delivered", text, sealed.Delivered)
			}
			if err == nil {
				t.Errorf("Send %s began after the mark and succeeded", text)
			}
		}
	}
}

// sealStart is the seal step's start: with the binding still published it
// marks the binding closing and evicts the session's stream.
func (r *closingRun) sealStart() {
	bind, ok := r.reg.Get(r.id)
	if !ok {
		return
	}
	bind.MarkClosing()
	r.marked.Store(true)
	r.e.EvictStream(r.id)
}

// release is the hook's release, or the coordination sweep's, racing the
// other.
func (r *closingRun) release(t *testing.T) {
	if err := r.e.Release(context.Background(), r.id, executor.DispositionCompleted); err != nil {
		t.Errorf("Release %s: %v", r.id, err)
	}
	r.released.Store(true)
}

// actors returns the session's actors, each a function run on its own
// goroutine.
func (r *closingRun) actors(t *testing.T) []func() {
	acts := []func(){
		r.sealStart,
		func() { r.e.EvictStream(r.id) },
		func() { r.release(t) },
		func() { r.release(t) },
	}
	for s := 0; s < 3; s++ {
		s := s
		acts = append(acts, func() { r.send(t, s, 8) })
	}
	return acts
}

// runClosingInterleaving starts the session's actors in a random order,
// each after a random delay, and waits for every one.
func runClosingInterleaving(t *testing.T, r *closingRun) {
	acts := r.actors(t)
	r.rng.Shuffle(len(acts), func(i, j int) { acts[i], acts[j] = acts[j], acts[i] })
	var wg sync.WaitGroup
	for _, act := range acts {
		delay := time.Duration(r.rng.Intn(3000)) * time.Microsecond
		wg.Add(1)
		go func(act func()) {
			defer wg.Done()
			time.Sleep(delay)
			act()
		}(act)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(6 * heldWait):
		t.Fatalf("%s: actors did not finish; a Send, the seal start, or a release deadlocked", r.id)
	}
}

// spec: 7.1 (Normal Flow), 28.5.1 (Gateway-to-pod)
// diagnosis: the terminal seal's closing gate raced the session's Sends, a
// stream eviction, and the binding release. A race-detector report means the
// mark or the stream cache is read without the lock that orders them; a
// message from a Send that began after the mark reaching the runtime means a
// turn ran during the export, the output loss the gate exists to prevent; a
// second Shutdown means the release ran twice; a leftover reader or open
// goroutine means an evicted or released stream kept its goroutine.
func TestSealClosingGateRacesSendsEvictionAndRelease_spec_7_1(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	adapterSrv := newClosingRecordAdapter()
	dial := heldServe(t, adapterSrv)

	const iterations = 30
	for i := 0; i < iterations; i++ {
		id := fmt.Sprintf("seal-%d", i)
		reg := podsession.NewRegistry()
		heldBindOn(reg, dial(), id)
		r := &closingRun{id: id, e: heldExecutor(t, reg, id), reg: reg, rng: rand.New(rand.NewSource(rng.Int63()))}
		runClosingInterleaving(t, r)

		for _, text := range r.gate {
			if adapterSrv.reached(text) {
				t.Errorf("%s: message %s began after the mark or the release and reached the runtime", id, text)
			}
		}
		if n := adapterSrv.shutdownsFor(id); n != 1 {
			t.Errorf("%s: adapter Shutdown received %d times, want one release", id, n)
		}
		if _, bound := reg.Get(id); bound {
			t.Errorf("%s: binding still published after both releases", id)
		}
	}
	heldAssertNoStreamGoroutines(t)
}
