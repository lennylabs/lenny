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
// flight, beside an Attach stream bound to the session. After a Shutdown
// whose guard acquisition expired, the retry runs across the stale open
// sequence's session_end and is refused for the life of the pod, because
// that cleanup did not complete. Across those interleavings the runtime
// must read the session's frames as alternating session_start and
// session_end, and the adapter's running record must match the last frame. No deadline_approaching may reach the runtime before it
// has written the session_started that answers the retry's session_start,
// and no session_started may reach the Attach stream.
//
// spec: §28.5.3 (CH-MSGSOCK, Session frame writes), §28.5.3 (CH-RUNTIMEOPS,
// Messages), §4.7.1 (role and gateway RPC contract), §5.2 (slot-identifier
// reclaim hold).
package tier7a_load_local_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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
//	remainingMs bound; or the Attach stream bound to the session relayed a
//	session_started or ended during the iteration; or, after a Shutdown
//	whose guard acquisition expired, a retry's bind, StartSession, or Attach
//	was admitted, or the reclaim hold cleared when the stale open sequence
//	ended. A stale attempt's
//	session_end then releases the retry's context on a runtime that lives
//	as long as the pod, a multi-session runtime receives a CH-RUNTIMEOPS
//	frame for a session it has not opened, or a client receives the
//	adapter's own acknowledgement frame as content.
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
// expires. Both arms then drive attempt 2's bind and StartSession
// concurrently with a SignalDeadline and an Attach for the session. An even
// iteration's retry is admitted, and every third one withholds the answer
// to the retry's session_start past the wait. An odd iteration's retry runs
// while attempt 1's open sequence is still waiting and keeps retrying
// across that sequence's stale session_end, and §5.2 refuses every one of
// its requests, because the expired-guard cleanup did not complete.
func runPairingIteration(t *testing.T, i int) {
	const sessionID = "sess-pairing"
	expiredGuard := i%2 == 1
	seq := &atomic.Int64{}
	s, sock, _ := rotationgate.NewPodAdapter(t, "pairing-pool")
	rt := newPairingRuntime(t, seq, int64(i), !expiredGuard)
	s.Runtime = rt
	s.SessionStartAckTimeout = 300 * time.Millisecond
	peer := rotationgate.DialPeer(t, sock)
	client := holdRaceClient(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if !s.Lifecycle.WaitHandshake(ctx, 5*time.Second) {
		t.Fatal("CH-RUNTIMEOPS capability handshake did not complete")
	}
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

	var probe retryOutcome
	if expiredGuard {
		probe = retryAgainstHeldIdentifier(t, s, client, peer, seq, sessionID, first)
	} else {
		probe = retryWithDeadlineSignal(t, s, rt, client, peer, seq, sessionID, i%3 == 2)
		close(rt.unpark)
		<-first
	}
	if err := waitStarted(probe); err != nil {
		t.Fatal(err)
	}
	assertFramesPaired(t, s, rt, sessionID, probe.signal)
	if expiredGuard {
		assertOnlyFirstAttemptFrames(t, rt)
	}
	if probe.attach != nil {
		probe.attach.assertNoSessionStarted(t)
	}
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

// retryOutcome is attempt 2's start, its deadline signal, and, in an
// iteration whose retry is admitted, the Attach stream bound to the
// session. attach is nil in a guard-expired iteration, whose Attach the
// adapter refuses because the identifier has no bound entry.
type retryOutcome struct {
	started chan error
	signal  <-chan deadlineProbe
	attach  *attachWatch
}

// retryWithDeadlineSignal binds attempt 2 after a guarded compensating
// Shutdown. That cleanup completed and released the identifier's reclaim
// hold, so the bind must be admitted; asserting it rather than accepting
// either outcome keeps the deadline-ordering, the SignalDeadline bound, and
// the Attach checks from being skipped silently. The case then opens an
// Attach stream bound to the session and runs attempt 2's StartSession and
// a SignalDeadline for the session concurrently beside it.
// spec: §5.2 (slot-identifier reclaim hold), §15.4 (gRPC status codes).
func retryWithDeadlineSignal(t *testing.T, s *adapter.Server, rt *pairingRuntime, client adapterv1.AdapterClient, peer *rotationgate.Peer, seq *atomic.Int64, sessionID string, withhold bool) retryOutcome {
	t.Helper()
	out := retryOutcome{started: make(chan error, 1)}
	if err := assignAttempt(s, sessionID, "attempt-2"); err != nil {
		t.Fatalf("attempt 2's bind after a guarded compensating Shutdown: %v; want admitted, since the completed cleanup released the reclaim hold", err)
	}
	out.attach = watchAttach(t, client, rt, sessionID)
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

// heldRetryWindow is how long an expired-guard iteration keeps retrying
// attempt 2's bind after attempt 1's open sequence has returned, so a hold
// released by that sequence's end, or by any cleanup after it, is observed.
const heldRetryWindow = 500 * time.Millisecond

// retryAgainstHeldIdentifier drives attempt 2 after a compensating Shutdown
// whose guard acquisition expired while attempt 1's open sequence held the
// slot serialization and waited for a session_started the runtime
// withholds. The Shutdown's acts ran without the slot serialization, so its
// cleanup did not complete and §5.2 holds the identifier for the life of
// the pod. The case binds attempt 2 at once, then, while the open sequence
// is still waiting, runs attempt 2's StartSession, a SignalDeadline, and an
// Attach for the session concurrently, and keeps retrying attempt 2's bind
// across the open sequence's stale session_end and for heldRetryWindow
// after attempt 1's StartSession returns. Every bind and the StartSession
// must be refused with slot_reclaim_in_progress, and the Attach must be
// refused before relaying any frame, because no entry is bound under the
// identifier. A hold that cleared once the open sequence ended would admit
// a retry whose session_start could follow the stale session_end's
// decision, which the open sequence alone does not fence.
// spec: §5.2 (slot-identifier reclaim hold), §15.4 (gRPC status codes),
// §28.5.3 (CH-MSGSOCK, Session frame writes).
func retryAgainstHeldIdentifier(t *testing.T, s *adapter.Server, client adapterv1.AdapterClient, peer *rotationgate.Peer, seq *atomic.Int64, sessionID string, first <-chan error) retryOutcome {
	t.Helper()
	requireReclaimHoldRefusal(t, "attempt 2's immediate bind", assignAttempt(s, sessionID, "attempt-2"))
	out := retryOutcome{started: make(chan error, 1)}
	out.signal = signalDeadlineRace(s, peer, seq, sessionID, 1000)
	startErr := make(chan error, 1)
	go func() {
		_, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
			SessionId: &adapterv1.SessionId{Value: sessionID},
		})
		startErr <- err
	}()
	attachErr := make(chan error, 1)
	go func() { attachErr <- attachRefusal(client, sessionID) }()
	firstDone := make(chan struct{})
	pollErr := make(chan error, 1)
	go func() { pollErr <- pollHeldBinds(s, sessionID, firstDone, heldRetryWindow) }()

	select {
	case <-first:
	case <-time.After(10 * time.Second):
		t.Fatal("attempt 1's StartSession did not return after its acknowledgement wait")
	}
	close(firstDone)
	if err := <-pollErr; err != nil {
		t.Fatal(err)
	}
	requireReclaimHoldRefusal(t, "attempt 2's StartSession", <-startErr)
	if err := <-attachErr; err != nil {
		t.Fatal(err)
	}
	out.started <- nil
	return out
}

// pollHeldBinds retries attempt 2's bind until stop is closed and for
// window after, and returns an error naming the first bind that was not
// the reclaim-hold refusal.
func pollHeldBinds(s *adapter.Server, sessionID string, stop <-chan struct{}, window time.Duration) error {
	var until time.Time
	for n := 0; ; n++ {
		if err := assignAttempt(s, sessionID, "attempt-2"); !isReclaimHoldRefusal(err) {
			return fmt.Errorf("attempt 2's bind retry %d after an expired-guard Shutdown = %v; want ABORTED slot_reclaim_in_progress for the life of the pod", n, err)
		}
		if until.IsZero() {
			select {
			case <-stop:
				until = time.Now().Add(window)
			default:
			}
		} else if time.Now().After(until) {
			return nil
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// attachRefusal opens an Attach stream for sessionID and returns nil when
// the adapter ends it with FAILED_PRECONDITION before relaying any frame,
// which is its answer to a session with no bound entry.
// spec: §5.2; §28.5.3 (CH-MSGSOCK, Outbound: session_started).
func attachRefusal(client adapterv1.AdapterClient, sessionID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := client.Attach(ctx)
	if err != nil {
		return fmt.Errorf("open Attach(%s): %w", sessionID, err)
	}
	if err := stream.Send(&adapterv1.AttachRequest{SessionId: &adapterv1.SessionId{Value: sessionID}}); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("Attach bind(%s): %w", sessionID, err)
	}
	got, err := stream.Recv()
	if err == nil {
		return fmt.Errorf("Attach(%s) relayed %s while the identifier is held; want the stream refused", sessionID, got.GetEnvelopeJson())
	}
	if status.Code(err) != codes.FailedPrecondition {
		return fmt.Errorf("Attach(%s) ended with %v; want FAILED_PRECONDITION, since no entry is bound under the held identifier", sessionID, err)
	}
	return nil
}

// assertOnlyFirstAttemptFrames requires that an expired-guard iteration's
// runtime read exactly attempt 1's session_start and the session_end its
// open sequence wrote when its acknowledgement wait ended, and no frame for
// the refused retry.
func assertOnlyFirstAttemptFrames(t *testing.T, rt *pairingRuntime) {
	t.Helper()
	frames, _ := rt.snapshot()
	if len(frames) != 2 || frames[0].typ != "session_start" || frames[1].typ != "session_end" {
		t.Fatalf("expired-guard iteration's runtime read %+v; want attempt 1's session_start and session_end only", frames)
	}
}

// requireReclaimHoldRefusal requires err, the outcome of the request what
// names, to be the §5.2 reclaim-hold refusal.
// spec: §5.2 (slot-identifier reclaim hold), §15.4 (gRPC status codes).
func requireReclaimHoldRefusal(t *testing.T, what string, err error) {
	t.Helper()
	if !isReclaimHoldRefusal(err) {
		t.Fatalf("%s after a compensating Shutdown whose guard acquisition expired = %v; want ABORTED slot_reclaim_in_progress, since the incomplete cleanup holds the identifier", what, err)
	}
}

// isReclaimHoldRefusal reports whether err is the §5.2 reclaim-hold
// refusal, which §15.4 answers with ABORTED and the
// slot_reclaim_in_progress reason.
func isReclaimHoldRefusal(err error) bool {
	st, ok := status.FromError(err)
	return err != nil && ok && st.Code() == codes.Aborted && strings.Contains(st.Message(), "slot_reclaim_in_progress")
}

// attachWatch is an Attach stream bound to the session, read for the rest
// of the iteration. It records every session_started frame the stream
// relays and the error that ended the stream before the case closed it.
type attachWatch struct {
	cancel context.CancelFunc
	done   chan struct{}

	mu      sync.Mutex
	started []string
	endErr  error
}

// watchAttach opens an Attach stream for sessionID through the adapter's
// gRPC surface, which runs the stream's session-bound and runtime
// admission, and confirms the stream is subscribed to the runtime output
// by writing a status frame for the session and reading it back. A frame
// the runtime writes before the subscription would reach the stream's
// filter never, so without the confirmation the absence of
// session_started would prove nothing.
func watchAttach(t *testing.T, client adapterv1.AdapterClient, rt *pairingRuntime, sessionID string) *attachWatch {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stream, err := client.Attach(ctx)
	if err != nil {
		t.Fatalf("Attach(%s): %v", sessionID, err)
	}
	if err := stream.Send(&adapterv1.AttachRequest{SessionId: &adapterv1.SessionId{Value: sessionID}}); err != nil {
		t.Fatalf("Attach bind(%s): %v", sessionID, err)
	}
	rt.Emit([]byte(`{"type":"status","sessionId":"` + sessionID + `","state":"thinking"}`))
	for {
		got, err := stream.Recv()
		if err != nil {
			t.Fatalf("Attach(%s) ended before relaying the subscription probe: %v", sessionID, err)
		}
		if relayedType(got.GetEnvelopeJson()) == "status" {
			break
		}
	}
	w := &attachWatch{cancel: cancel, done: make(chan struct{})}
	go w.read(ctx, stream)
	return w
}

func (w *attachWatch) read(ctx context.Context, stream adapterv1.Adapter_AttachClient) {
	defer close(w.done)
	for {
		got, err := stream.Recv()
		if err != nil {
			if ctx.Err() == nil {
				w.mu.Lock()
				w.endErr = err
				w.mu.Unlock()
			}
			return
		}
		if relayedType(got.GetEnvelopeJson()) == "session_started" {
			w.mu.Lock()
			w.started = append(w.started, string(got.GetEnvelopeJson()))
			w.mu.Unlock()
		}
	}
}

// assertNoSessionStarted closes the stream and requires that it stayed open
// for the whole iteration and relayed no session_started: the adapter
// consumes the acknowledgement of its own session_start and relays it to no
// Attach stream. spec: §28.5.3 (CH-MSGSOCK, Outbound: session_started).
func (w *attachWatch) assertNoSessionStarted(t *testing.T) {
	t.Helper()
	w.cancel()
	select {
	case <-w.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the Attach stream did not end after the case closed it")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.endErr != nil {
		t.Errorf("the Attach stream bound to the session ended during the iteration: %v", w.endErr)
	}
	if len(w.started) != 0 {
		t.Errorf("the Attach stream relayed session_started %v, want every acknowledgement consumed by the adapter", w.started)
	}
}

// relayedType reads the type of a relayed frame, or "" for one that does
// not decode.
func relayedType(envelope []byte) string {
	var f struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(envelope, &f)
	return f.Type
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
