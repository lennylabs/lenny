// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local coverage for the closing guarantee of the CH-MSGSOCK
// Session frame writes table and for the CH-RUNTIMEOPS ordering that the
// session_started acknowledgement gives a session-scoped frame.
//
// A start's gateway context can expire while Runtime.Start runs, and its
// compensating Shutdown then reclaims the attempt's entry, either under the
// slot serialization or, when the open sequence holds it, after a guard
// acquisition that expired. A retry binds a fresh entry under the same
// identifier and starts it while a deadline signal for the session is in
// flight. Across those interleavings the runtime must read the session's
// frames as alternating session_start and session_end, the adapter's
// running record must match the last frame, and no deadline_approaching may
// reach the runtime before it has written the session_started that answers
// the retry's session_start.
//
// spec: §28.5.3 (CH-MSGSOCK, Session frame writes), §28.5.3 (CH-RUNTIMEOPS,
// Messages), §4.7.1 (role and gateway RPC contract), §5.2 (slot-identifier
// reclaim hold).
package tier7a_load_local_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/tests/testinfra/ackruntime"
	"github.com/lennylabs/lenny/tests/testinfra/rotationgate"
)

// pairedFrame is one session frame the runtime read, with its position in
// the case's global order.
type pairedFrame struct {
	typ, startID string
	seq          int64
}

// pairingRuntime records every session frame the adapter writes, answers
// each session_start with session_started after a random short delay
// unless the case withholds that start's answer, and parks the first
// Runtime.Start until the case releases it. Every recorded event takes a
// number from one counter shared with the CH-RUNTIMEOPS reader, so the
// case can order a session_started the runtime wrote against a
// deadline_approaching it read.
type pairingRuntime struct {
	*ackruntime.Runtime
	seq *atomic.Int64

	mu       sync.Mutex
	rng      *rand.Rand
	frames   []pairedFrame
	answered map[string]int64
	withhold map[int]bool
	starts   int

	park           bool
	parkOnce       sync.Once
	parked, unpark chan struct{}
}

func newPairingRuntime(t *testing.T, seq *atomic.Int64, seed int64, park bool) *pairingRuntime {
	rt := ackruntime.New(t)
	rt.SetReply(ackruntime.Withhold)
	return &pairingRuntime{
		Runtime:  rt,
		seq:      seq,
		rng:      rand.New(rand.NewSource(seed)),
		answered: map[string]int64{},
		withhold: map[int]bool{},
		park:     park,
		parked:   make(chan struct{}),
		unpark:   make(chan struct{}),
	}
}

// Start parks the first start until the case releases it, standing in for
// a Runtime.Start that is still making the runtime live when the gateway
// gives up on the attempt.
func (r *pairingRuntime) Start(context.Context, string) error {
	if r.park {
		r.parkOnce.Do(func() {
			close(r.parked)
			<-r.unpark
		})
	}
	return nil
}

func (r *pairingRuntime) WriteEnvelope(sessionID string, envelope []byte) error {
	var f struct {
		Type    string `json:"type"`
		StartID string `json:"startId"`
	}
	_ = json.Unmarshal(envelope, &f)
	r.mu.Lock()
	r.frames = append(r.frames, pairedFrame{typ: f.Type, startID: f.StartID, seq: r.seq.Add(1)})
	answer := false
	var delay time.Duration
	if f.Type == "session_start" {
		answer = !r.withhold[r.starts]
		r.starts++
		delay = time.Duration(r.rng.Intn(3000)) * time.Microsecond
	}
	r.mu.Unlock()
	if answer {
		go r.answer(sessionID, f.StartID, delay)
	}
	return r.Runtime.WriteEnvelope(sessionID, envelope)
}

// answer writes the session_started for startID after delay, recording its
// position before the adapter can read it.
func (r *pairingRuntime) answer(sessionID, startID string, delay time.Duration) {
	time.Sleep(delay)
	r.mu.Lock()
	r.answered[startID] = r.seq.Add(1)
	r.mu.Unlock()
	r.Emit(ackruntime.SessionStarted(sessionID, startID, ""))
}

func (r *pairingRuntime) Close(context.Context, string) error { return nil }

// withholdNext withholds the answer to the next session_start written.
func (r *pairingRuntime) withholdNext() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.withhold[r.starts] = true
}

func (r *pairingRuntime) startsWritten() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.starts
}

func (r *pairingRuntime) snapshot() ([]pairedFrame, map[string]int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	answered := make(map[string]int64, len(r.answered))
	for k, v := range r.answered {
		answered[k] = v
	}
	return append([]pairedFrame(nil), r.frames...), answered
}

// assignAttempt binds sessionID's entry to attempt through AssignCredentials.
func assignAttempt(s *adapter.Server, sessionID, attempt string) error {
	_, err := s.AssignCredentials(context.Background(), &adapterv1.AssignCredentialsRequest{
		BindAttempt: attempt,
		SessionId:   &adapterv1.SessionId{Value: sessionID},
		Leases: map[string]*adapterv1.CredentialLease{
			"anthropic": {LeaseId: "l-" + attempt, Provider: "anthropic", Payload: []byte("{}")},
		},
	})
	return err
}

// deadlineProbe is the outcome of one SignalDeadline and of the peer's read
// of the frame it may have written.
type deadlineProbe struct {
	delivered bool
	elapsed   time.Duration
	frame     rotationgate.Frame
	read      bool
	readSeq   int64
}

// signalDeadlineRace issues SignalDeadline for sessionID and reads the
// peer's next frame concurrently, stamping the read with the shared
// counter the moment it completes.
func signalDeadlineRace(s *adapter.Server, peer *rotationgate.Peer, seq *atomic.Int64, sessionID string, remainingMs int32) <-chan deadlineProbe {
	out := make(chan deadlineProbe, 1)
	go func() {
		var p deadlineProbe
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.frame, p.read = peer.ReadWithin(time.Duration(remainingMs)*time.Millisecond + 2*time.Second)
			p.readSeq = seq.Add(1)
		}()
		began := time.Now()
		resp, err := s.SignalDeadline(context.Background(), &adapterv1.SignalDeadlineRequest{
			SessionId: &adapterv1.SessionId{Value: sessionID}, RemainingMs: remainingMs,
		})
		p.elapsed = time.Since(began)
		p.delivered = err == nil && resp.GetDelivered()
		wg.Wait()
		out <- p
	}()
	return out
}

// spec: 28.5.3 (CH-MSGSOCK session frame writes), 28.5.3 (CH-RUNTIMEOPS Messages), 4.7.1 (role and gateway RPC contract), 5.2 (slot-identifier reclaim hold)
// diagnosis: across a start parked in Runtime.Start, its compensating
//
//	Shutdown (guarded, or after a guard acquisition that expired while the
//	open sequence waited for session_started), and a retry's bind and
//	start, the runtime read two session_start frames for the session with
//	no session_end between them, a session_end with none open, or a final
//	frame that disagrees with the adapter's running record; or a
//	deadline_approaching reached the runtime before the session_started
//	answering the retry's session_start, or a SignalDeadline outlived its
//	remainingMs bound. A stale attempt's session_end then releases the
//	retry's context on a runtime that lives as long as the pod, or a
//	multi-session runtime receives a CH-RUNTIMEOPS frame for a session it
//	has not opened.
func TestSessionFramesStayPairedAcrossReclaimAndRetry_spec_28_5_3(t *testing.T) {
	const iterations = 24
	for i := 0; i < iterations; i++ {
		t.Run(fmt.Sprintf("iteration-%d", i), func(t *testing.T) {
			runPairingIteration(t, i)
		})
	}
}

// runPairingIteration runs one interleaving on a fresh pod. Even
// iterations reclaim attempt 1 while it is parked in Runtime.Start, under
// the slot serialization. Odd iterations let attempt 1 write its
// session_start, withhold the answer so its open sequence keeps the slot
// serialization, and reclaim it with a Shutdown whose guard acquisition
// expires. Every third iteration withholds the answer to the retry's
// session_start past the wait.
func runPairingIteration(t *testing.T, i int) {
	const sessionID = "sess-pairing"
	expiredGuard := i%2 == 1
	seq := &atomic.Int64{}
	s, sock, _ := rotationgate.NewPodAdapter(t, "pairing-pool")
	rt := newPairingRuntime(t, seq, int64(i), !expiredGuard)
	s.Runtime = rt
	s.SessionStartAckTimeout = 300 * time.Millisecond
	peer := rotationgate.DialPeer(t, sock)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if !s.Lifecycle.WaitHandshake(ctx, 5*time.Second) {
		t.Fatal("CH-RUNTIMEOPS capability handshake did not complete")
	}
	// The Attach stream's subscription to the runtime output, which every
	// frame the runtime writes is fanned out to beside the open sequence's
	// own subscription.
	attach, err := s.Runtime.Output(ctx, sessionID)
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	go func() {
		for range attach {
		}
	}()

	if err := assignAttempt(s, sessionID, "attempt-1"); err != nil {
		t.Fatalf("assign attempt 1: %v", err)
	}
	if expiredGuard {
		rt.withholdNext()
	}
	first := make(chan error, 1)
	go func() {
		_, err := s.StartSession(ctx, &adapterv1.StartSessionRequest{SessionId: &adapterv1.SessionId{Value: sessionID}})
		first <- err
	}()
	compensate(t, s, rt, sessionID, expiredGuard)

	probe := retryWithDeadlineSignal(t, s, rt, peer, seq, sessionID, i%3 == 2)
	close(rt.unpark)
	<-first
	if err := waitStarted(probe); err != nil {
		t.Fatal(err)
	}
	assertFramesPaired(t, s, rt, sessionID, probe.signal)
}

// compensate runs attempt 1's compensating Shutdown. Without expiredGuard
// it runs once attempt 1 is parked in Runtime.Start, when the guard is
// free. With expiredGuard it runs once attempt 1's session_start is
// written, while the open sequence holds the guard and waits, on a
// deadline its guard acquisition outlives.
func compensate(t *testing.T, s *adapter.Server, rt *pairingRuntime, sessionID string, expiredGuard bool) {
	t.Helper()
	sctx := context.Background()
	if expiredGuard {
		deadline := time.Now().Add(5 * time.Second)
		for rt.startsWritten() == 0 {
			if time.Now().After(deadline) {
				t.Fatal("attempt 1 wrote no session_start")
			}
			time.Sleep(100 * time.Microsecond)
		}
		var cancel context.CancelFunc
		sctx, cancel = context.WithTimeout(sctx, 20*time.Millisecond)
		defer cancel()
	} else {
		<-rt.parked
	}
	if _, err := s.Shutdown(sctx, &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID}, BindAttempt: "attempt-1",
	}); err != nil {
		t.Fatalf("compensating Shutdown: %v", err)
	}
}

// retryOutcome is attempt 2's start and its deadline signal, when the bind
// was admitted.
type retryOutcome struct {
	started chan error
	signal  <-chan deadlineProbe
}

// retryWithDeadlineSignal binds attempt 2 and, when the bind is admitted,
// runs its StartSession and a SignalDeadline for the session concurrently.
// A bind refused by the reclaim hold of a cleanup that did not complete is
// an expected outcome of the guard-expired interleaving.
func retryWithDeadlineSignal(t *testing.T, s *adapter.Server, rt *pairingRuntime, peer *rotationgate.Peer, seq *atomic.Int64, sessionID string, withhold bool) retryOutcome {
	t.Helper()
	out := retryOutcome{started: make(chan error, 1)}
	if assignAttempt(s, sessionID, "attempt-2") != nil {
		out.started <- nil
		return out
	}
	if withhold {
		rt.withholdNext()
	}
	out.signal = signalDeadlineRace(s, peer, seq, sessionID, 1000)
	go func() {
		// A start whose answer was withheld fails; its frames are what the
		// case asserts.
		_, _ = s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
			SessionId: &adapterv1.SessionId{Value: sessionID},
		})
		out.started <- nil
	}()
	return out
}

func waitStarted(o retryOutcome) error {
	select {
	case <-o.started:
		return nil
	case <-time.After(20 * time.Second):
		return fmt.Errorf("attempt 2's StartSession did not return")
	}
}

// assertFramesPaired checks the frame alternation, the running record
// against the last frame, and the deadline signal's order and bound.
func assertFramesPaired(t *testing.T, s *adapter.Server, rt *pairingRuntime, sessionID string, signal <-chan deadlineProbe) {
	t.Helper()
	var probe *deadlineProbe
	if signal != nil {
		p := <-signal
		probe = &p
	}
	frames, answered := rt.snapshot()
	open := false
	for k, f := range frames {
		switch f.typ {
		case "session_start":
			if open {
				t.Fatalf("frame %d: a second session_start with no session_end between: %+v", k, frames)
			}
		case "session_end":
			if !open {
				t.Fatalf("frame %d: a session_end with no session_start open: %+v", k, frames)
			}
		default:
			t.Fatalf("frame %d: unexpected frame type %q", k, f.typ)
		}
		open = f.typ == "session_start"
	}
	// One fresh pod: at most one start took the record, so the pod's sole
	// session names the session exactly when the runtime holds it.
	if running := s.SoleSessionID() == sessionID; running != open {
		t.Fatalf("adapter records running=%v while the runtime's last frame leaves the session open=%v: %+v", running, open, frames)
	}
	if probe != nil {
		assertDeadlineAfterAnswer(t, *probe, frames, answered)
	}
}

// assertDeadlineAfterAnswer requires a delivered deadline_approaching to
// follow the session_started answering the latest session_start written
// before it, and the SignalDeadline to return within its bound.
func assertDeadlineAfterAnswer(t *testing.T, p deadlineProbe, frames []pairedFrame, answered map[string]int64) {
	t.Helper()
	if p.elapsed > 1000*time.Millisecond+time.Second {
		t.Errorf("SignalDeadline returned after %s, want within its 1000ms remainingMs bound", p.elapsed)
	}
	if !p.read {
		if p.delivered {
			t.Error("SignalDeadline reported delivered but the runtime read no frame")
		}
		return
	}
	if p.frame.Type != "deadline_approaching" {
		t.Fatalf("runtime read %+v on CH-RUNTIMEOPS, want only deadline_approaching", p.frame)
	}
	latest := ""
	for _, f := range frames {
		if f.typ == "session_start" && f.seq < p.readSeq {
			latest = f.startID
		}
	}
	at, ok := answered[latest]
	if !ok || at > p.readSeq {
		t.Fatalf("deadline_approaching reached the runtime (order %d) before the session_started answering startId %q (answered %v at %d)",
			p.readSeq, latest, ok, at)
	}
}
