// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage for the adapter-side addressing
// decision the §28.5.3 `set_tracing_context` frame goes through. A runtime
// emits the frame in a loop on an open Attach stream while the binding that
// stream carries is released underneath it, with no ordering between the
// two goroutines, and a second runtime writes frames the stream under test
// does not address into the same fan-out.
//
// Four properties hold under every interleaving, and each racing case
// asserts all four:
//
//   - Accounting. Every frame the addressing decision reaches is either
//     forwarded to the gateway or counted on the drop counter, and never
//     both and never neither.
//   - Session binding. A forward carries the session id of the stream that
//     carried the frame, so a frame fanned out to a sibling stream cannot
//     register against that sibling's session.
//   - Address equality. A frame carrying an address the stream does not
//     hold is never forwarded, even while the release is in flight.
//   - Release ordering. A frame the handler decides after the teardown has
//     returned is never forwarded. The bound is taken on the decision
//     rather than on the emission: once the teardown returns, the release
//     goroutine writes a sentinel status frame on the stream's address, and
//     the relay decides frames one at a time on a single goroutine, so
//     every forward recorded after that sentinel comes back was decided
//     against a binding that was already gone. Frames keep flowing across
//     the sentinel, so the window the bound covers is the window the
//     teardown opens.
//
// The frames the pipeline is holding when the teardown lands are the part
// of that window a racing case cannot place, because their decision order
// against the release is whatever the scheduler chose. The in-flight case
// removes the timing: it stops the relay inside its first forward, parks
// the rest of the frames behind it, runs the teardown to completion, and
// then lets the relay go, so every parked frame is known to be decided
// after the release and every one of them must drop.
//
// The registry the live-binding condition reads has two terms on the
// pod-global branch, and a pod holding slots and a pod-global session at
// once is the state in which they disagree. The coexisting case builds that
// state and releases both bindings, so both terms move while frames are
// being decided. Releasing the pod-global session before the slot leaves no
// instant at which both terms hold, so any forward at all in that ordering
// means the decision read the two terms at two different times. That case
// resolves a tear only when the reads are further apart than the interval
// between the two teardowns; the tier-1 case in `pkg/adapter` that toggles
// the registry underneath the decision resolves one of any width.
//
// Once the release has completed and been observed, every later frame
// drops.
//
// spec: §28.5.3 (set_tracing_context addressing, live-binding
// confirmation), §8.3 (tracing context, per-session scope), §6.4 (slot
// claim and release lifecycle).

package tier7a_load_local_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	adaptermcp "github.com/lennylabs/lenny/pkg/adapter/mcp"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// tracingRaceFrameCap bounds how many addressed frames one emitter writes
// and tracingRaceMisaddressedCap bounds the interleaved frames carrying an
// address the stream under test does not hold. Both emitters run until the
// release sentinel comes back rather than to their cap, so the caps only
// stop a run whose teardown never lands. tracingRaceRuns is how many times
// the whole scenario repeats so the release lands at varying points in the
// emission loop, and tracingRaceSettleFrames is how many frames the
// post-release phase writes. `lenny-test stress --test <Name> --runs <N>`
// is the flake-budget form for driving the window harder.
const (
	tracingRaceFrameCap        = 4096
	tracingRaceMisaddressedCap = 4096
	tracingRaceRuns            = 16
	tracingRaceSettleFrames    = 64
)

// tracingRaceInFlightFrames is how many frames the in-flight case parks in
// the pipeline behind a stalled relay, so they are all decided after the
// teardown has run to completion.
const tracingRaceInFlightFrames = 12

// tracingRaceJitter bounds the delay before the teardown goroutine issues
// its Shutdown, so the release lands at a different point of the emission
// loop on each run.
const tracingRaceJitter = 400 * time.Microsecond

// raceFrameSeqKey is the tracing-context key every emitted frame carries so
// a recorded forward can be traced back to the frame that produced it.
const raceFrameSeqKey = "lenny_race_seq"

// raceMisaddressedSeq labels a frame emitted on an address the stream under
// test does not hold. No correct addressing decision forwards one, so the
// label appearing in a recorded forward is itself the failure.
const raceMisaddressedSeq = "misaddressed"

// raceRuntime is the RuntimeProcess double for these cases. One runtime
// process per pod serves every slot, so a single output stream is fanned
// out to every Attach subscriber, which is what makes an untagged frame
// reach streams it does not address.
type raceRuntime struct {
	mu      sync.Mutex
	subs    []chan []byte
	subCond *sync.Cond
	output  chan []byte
	fanOnce sync.Once
}

func newRaceRuntime() *raceRuntime {
	return &raceRuntime{output: make(chan []byte, 8)}
}

func (r *raceRuntime) Start(context.Context, string) error           { return nil }
func (r *raceRuntime) WriteEnvelope(string, []byte) error            { return nil }
func (r *raceRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (r *raceRuntime) Close(context.Context, string) error           { return nil }

func (r *raceRuntime) Output(context.Context, string) (<-chan []byte, error) {
	sub := make(chan []byte, 8)
	r.mu.Lock()
	r.subs = append(r.subs, sub)
	if r.subCond == nil {
		r.subCond = sync.NewCond(&r.mu)
	}
	r.subCond.Broadcast()
	r.mu.Unlock()
	r.fanOnce.Do(func() {
		go func() {
			for line := range r.output {
				r.mu.Lock()
				subs := append([]chan []byte(nil), r.subs...)
				r.mu.Unlock()
				for _, s := range subs {
					s <- line
				}
			}
		}()
	})
	return sub, nil
}

// waitForSubscribers blocks until at least n Attach handlers have
// subscribed to the fan-out. A frame written before a stream subscribes is
// never delivered to it, which would break the accounting the cases below
// assert.
func (r *raceRuntime) waitForSubscribers(t *testing.T, n int) {
	t.Helper()
	r.mu.Lock()
	if r.subCond == nil {
		r.subCond = sync.NewCond(&r.mu)
	}
	timedOut := false
	timer := time.AfterFunc(10*time.Second, func() {
		r.mu.Lock()
		timedOut = true
		r.subCond.Broadcast()
		r.mu.Unlock()
	})
	defer timer.Stop()
	for len(r.subs) < n && !timedOut {
		r.subCond.Wait()
	}
	got := len(r.subs)
	r.mu.Unlock()
	if got < n {
		t.Fatalf("only %d of %d Attach output subscribers registered before timeout", got, n)
	}
}

// raceForward is one recorded CallPlatformTool: the session id the adapter
// injected and the sequence label of the frame that produced it.
type raceForward struct {
	session string
	seq     string
}

// raceForwarder records every CallPlatformTool the adapter makes so a case
// can count the forwards and read back which frame each one carried. The
// relay decides one frame at a time, so the recorded order is the order the
// addressing decision admitted the frames.
type raceForwarder struct {
	mu       sync.Mutex
	recorded []raceForward
	// gate, when non-nil, holds the relay inside its first forward until
	// the gate is closed. It is what lets a case stall the addressing loop
	// with frames already in flight, run a teardown to completion, and then
	// let the stalled loop decide the frames that were waiting behind it.
	gate     chan struct{}
	arrived  chan struct{}
	gateOnce sync.Once
}

func (f *raceForwarder) ListPlatformTools(context.Context, string) ([]adaptermcp.Tool, error) {
	return nil, nil
}

func (f *raceForwarder) CallPlatformTool(_ context.Context, sessionID, _ string, args json.RawMessage) (json.RawMessage, error) {
	seq := "unreadable"
	var call struct {
		Context map[string]string `json:"context"`
	}
	if err := json.Unmarshal(args, &call); err == nil {
		if v, ok := call.Context[raceFrameSeqKey]; ok {
			seq = v
		}
	}
	f.mu.Lock()
	f.recorded = append(f.recorded, raceForward{session: sessionID, seq: seq})
	f.mu.Unlock()
	if f.gate != nil {
		f.gateOnce.Do(func() { close(f.arrived) })
		<-f.gate
	}
	return json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`), nil
}

// stallOnFirstForward arms the gate so the relay stops inside its first
// forward, and returns the release function.
func (f *raceForwarder) stallOnFirstForward() func() {
	f.gate = make(chan struct{})
	f.arrived = make(chan struct{})
	var once sync.Once
	return func() { once.Do(func() { close(f.gate) }) }
}

// awaitStall blocks until the relay is stopped inside its first forward.
func (f *raceForwarder) awaitStall(t *testing.T) {
	t.Helper()
	select {
	case <-f.arrived:
	case <-time.After(10 * time.Second):
		t.Fatal("the relay never reached its first forward")
	}
}

// forwards returns the calls recorded so far.
func (f *raceForwarder) forwards() []raceForward {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]raceForward(nil), f.recorded...)
}

// count returns how many calls have been recorded so far.
func (f *raceForwarder) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.recorded)
}

// raceTracingFrame builds a set_tracing_context frame labelled with seq,
// stamping slotID when it is non-empty the way the concurrent dispatch loop
// does.
func raceTracingFrame(slotID, seq string) []byte {
	slot := ""
	if slotID != "" {
		slot = `"slotId":"` + slotID + `",`
	}
	return []byte(`{"type":"set_tracing_context",` + slot +
		`"context":{"langsmith_run_id":"run_race","` + raceFrameSeqKey + `":"` + seq + `"}}`)
}

// raceStatusFrame builds a status frame, which the Attach loop relays as
// content. The relay handles one frame at a time, so receiving the status
// frame proves every set_tracing_context frame written before it has been
// decided.
func raceStatusFrame(slotID string) []byte {
	if slotID == "" {
		return []byte(`{"type":"status","state":"thinking"}`)
	}
	return []byte(`{"type":"status","slotId":"` + slotID + `","state":"thinking"}`)
}

// tracingDropCount reads the §28.5.3 drop counter off the default registry,
// where the adapter registers it.
func tracingDropCount(t *testing.T) float64 {
	t.Helper()
	fams, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, f := range fams {
		if f.GetName() != "lenny_adapter_set_tracing_context_dropped_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			return m.GetCounter().GetValue()
		}
	}
	return 0
}

// raceAdapter builds an adapter Server over the fan-out runtime with a
// recording platform forwarder, serves it over an in-memory listener, and
// returns the pieces a case drives.
func raceAdapter(t *testing.T) (*adapter.Server, *raceRuntime, *raceForwarder, adapterv1.AdapterClient) {
	t.Helper()
	base := t.TempDir()
	s := adapter.New("test")
	s.WorkspaceRoot = filepath.Join(base, "workspace-root")
	s.WorkspaceBase = filepath.Join(base, "workspace")
	s.SessionsRoot = filepath.Join(base, "sessions")
	s.ArtifactsRoot = filepath.Join(base, "artifacts")
	s.CredentialsDir = filepath.Join(base, "run", "lenny")
	rt := newRaceRuntime()
	s.Runtime = rt
	fwd := &raceForwarder{}
	s.PlatformForwarder = fwd

	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(s)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial in-memory adapter: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return s, rt, fwd, adapterv1.NewAdapterClient(conn)
}

// startRaceSlot binds sessionID's slot on the pod, which is the one start
// path on a pod of either concurrency. spec: §5.2.
func startRaceSlot(t *testing.T, s *adapter.Server, sessionID string) {
	t.Helper()
	if _, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Runtime:   "echo",
	}); err != nil {
		t.Fatalf("StartSession(%s): %v", sessionID, err)
	}
}

// openRaceAttach binds an Attach stream to sessionID, which is the
// stream's whole address; slotID is the same identifier and is carried for
// the diagnostics below.
func openRaceAttach(t *testing.T, client adapterv1.AdapterClient, sessionID, slotID string) adapterv1.Adapter_AttachClient {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stream, err := client.Attach(ctx)
	if err != nil {
		t.Fatalf("Attach(%s/%s): %v", sessionID, slotID, err)
	}
	req := &adapterv1.AttachRequest{SessionId: &adapterv1.SessionId{Value: sessionID}}
	if err := stream.Send(req); err != nil {
		t.Fatalf("Send bind(%s/%s): %v", sessionID, slotID, err)
	}
	return stream
}

// drainToStatus emits a status frame on the stream's address and waits for
// the relay to hand it back, which happens only after every frame written
// before it has been decided.
func drainToStatus(t *testing.T, rt *raceRuntime, stream adapterv1.Adapter_AttachClient, slotID string) {
	t.Helper()
	rt.output <- raceStatusFrame(slotID)
	awaitStatusFrame(t, stream, slotID)
}

// awaitStatusFrame reads the stream until the relay hands back a status
// frame.
func awaitStatusFrame(t *testing.T, stream adapterv1.Adapter_AttachClient, slotID string) {
	t.Helper()
	for {
		got, err := stream.Recv()
		if err != nil {
			t.Fatalf("Recv on the stream bound to slot %q: %v", slotID, err)
		}
		if jsonlType(t, got.GetEnvelopeJson()) == "status" {
			return
		}
	}
}

// jsonlType reads the type field of a relayed frame.
func jsonlType(t *testing.T, envelope []byte) string {
	t.Helper()
	var frame struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(envelope, &frame); err != nil {
		t.Fatalf("decode relayed frame %s: %v", envelope, err)
	}
	return frame.Type
}

// raceEmitter writes frames on one address until it is stopped, so frames
// are still being decided when the release lands and when the sentinel that
// marks the release comes back. It reports how many frames it wrote, which
// is what the accounting balances against.
type raceEmitter struct {
	stop    atomic.Bool
	written int
}

// emit writes frames on slotID, labelling each with label(i), until the
// emitter is stopped or limit frames have been written.
func (e *raceEmitter) emit(rt *raceRuntime, slotID string, limit int, label func(int) string) {
	for i := 0; i < limit && !e.stop.Load(); i++ {
		rt.output <- raceTracingFrame(slotID, label(i))
		e.written = i + 1
	}
}

// emitAddressed writes sequence-numbered frames on the address the stream
// under test holds.
func (e *raceEmitter) emitAddressed(rt *raceRuntime, slotID string) {
	e.emit(rt, slotID, tracingRaceFrameCap, strconv.Itoa)
}

// emitMisaddressed writes frames carrying an address the stream under test
// does not hold. They reach the same fan-out, so they exercise the
// address-equality condition and, on a concurrent pod, the sibling stream's
// own handler.
func (e *raceEmitter) emitMisaddressed(rt *raceRuntime, slotID string) {
	e.emit(rt, slotID, tracingRaceMisaddressedCap, func(int) string { return raceMisaddressedSeq })
}

// awaitReleaseSentinel is what makes the release-ordering bound
// decision-relative rather than emission-relative. The teardown goroutine writes a status
// frame on the stream's address once Shutdown has returned; the reader waits
// for the relay to hand it back and records how many forwards existed at
// that point. The relay decides frames one at a time, so every forward
// recorded from that index onward was decided after the sentinel, which is
// after the binding was released.
func awaitReleaseSentinel(t *testing.T, stream adapterv1.Adapter_AttachClient, slotID string, fwd *raceForwarder, emitters ...*raceEmitter) int {
	t.Helper()
	awaitStatusFrame(t, stream, slotID)
	decided := fwd.count()
	for _, e := range emitters {
		e.stop.Store(true)
	}
	return decided
}

// shutdownAfterJitter issues req after a random delay and then writes the
// release sentinel on slotID, so the teardown lands at an unpredictable
// point of the emission loop and the frames decided after it are
// identifiable.
func shutdownAfterJitter(t *testing.T, client adapterv1.AdapterClient, rt *raceRuntime, slotID string, reqs ...*adapterv1.ShutdownRequest) {
	t.Helper()
	time.Sleep(time.Duration(rand.Intn(int(tracingRaceJitter))) * time.Nanosecond)
	for _, req := range reqs {
		if _, err := client.Shutdown(context.Background(), req); err != nil {
			t.Errorf("Shutdown(%s): %v", req.GetSessionId().GetValue(), err)
		}
	}
	rt.output <- raceStatusFrame(slotID)
}

// shutdownSessionRequest builds the Shutdown that releases the named
// session's slot, which is the one teardown on a pod of either
// concurrency. spec: §5.2.
func shutdownSessionRequest(sessionID string) *adapterv1.ShutdownRequest {
	return &adapterv1.ShutdownRequest{SessionId: &adapterv1.SessionId{Value: sessionID}}
}

// checkTracingAccounting asserts the four properties the race establishes:
// every decision accounted for exactly once, no forward against a session
// the stream is not bound to, no forward of a frame the stream does not
// address, and no forward of a frame the handler decided after the release
// completed. decidedAtRelease is the forward count the release sentinel
// observed; a forward at or after that index was decided against a released
// binding.
func checkTracingAccounting(t *testing.T, run int, sessionID string, decisions int, got []raceForward, drops float64, decidedAtRelease int) {
	t.Helper()
	if float64(len(got))+drops != float64(decisions) {
		t.Fatalf("run %d: %d frames reached the addressing decision but %d were forwarded and %v "+
			"counted as dropped; a frame was both forwarded and counted, or lost without either",
			run, decisions, len(got), drops)
	}
	for i, fw := range got {
		if fw.session != sessionID {
			t.Fatalf("run %d: forward %d carried session %q, want %q: the addressing decision "+
				"registered tracing identifiers against a session this stream is not bound to",
				run, i, fw.session, sessionID)
		}
		if fw.seq == raceMisaddressedSeq {
			t.Fatalf("run %d: forward %d carried a frame addressed to another stream: address "+
				"equality admitted a frame it must reject", run, i)
		}
		if i >= decidedAtRelease {
			t.Fatalf("run %d: forward %d carried frame %q, which the handler decided after the "+
				"teardown returned and its sentinel came back: live-binding confirmation admitted "+
				"a frame whose binding was already released", run, i, fw.seq)
		}
	}
}

// checkPostReleaseSilence emits settle frames on the released address and
// asserts that every one of them drops, so the release is final rather than
// merely observed once.
func checkPostReleaseSilence(t *testing.T, run int, rt *raceRuntime, stream adapterv1.Adapter_AttachClient, slotID string, fwd *raceForwarder, forwardsBefore int) {
	t.Helper()
	settled := tracingDropCount(t)
	e := &raceEmitter{}
	e.emit(rt, slotID, tracingRaceSettleFrames, strconv.Itoa)
	drainToStatus(t, rt, stream, slotID)
	if after := fwd.count(); after != forwardsBefore {
		t.Fatalf("run %d: %d frames forwarded after the binding was released, want none: "+
			"a released binding still registers tracing identifiers", run, after-forwardsBefore)
	}
	if got := tracingDropCount(t) - settled; got != float64(tracingRaceSettleFrames) {
		t.Fatalf("run %d: drop counter moved by %v after the binding was released, want %d",
			run, got, tracingRaceSettleFrames)
	}
}

// spec: 28.5.3 (set_tracing_context addressing, live-binding
// confirmation), 6.4 (slot claim and release lifecycle), 8.3 (tracing
// context, per-session scope)
//
// diagnosis: the adapter's addressing decision for a set_tracing_context
//
//	frame is wrong while a slot is being released underneath the stream
//	that carries it. A count mismatch means a frame was both forwarded and
//	counted as dropped, or neither. A forward naming the sibling slot's
//	session means an untagged frame fanned out to that stream registered
//	against it. A forward of a frame addressed to another stream means
//	address equality admitted what it must reject. A forward recorded after
//	the release sentinel means live-binding confirmation admitted a frame
//	the handler decided against a slot binding that was already gone. The
//	post-release phase failing means a released slot still registers
//	tracing identifiers at all.
func TestSetTracingContextSlotReleaseRaceDecidesEveryFrameOnce_spec_28_5_3(t *testing.T) {
	for run := 0; run < tracingRaceRuns; run++ {
		t.Run(fmt.Sprintf("run%02d", run), func(t *testing.T) {
			s, rt, fwd, client := raceAdapter(t)
			startRaceSlot(t, s, "sess-slot-a")
			startRaceSlot(t, s, "sess-slot-b")
			streamA := openRaceAttach(t, client, "sess-slot-a", "sess-slot-a")
			streamB := openRaceAttach(t, client, "sess-slot-b", "sess-slot-b")
			rt.waitForSubscribers(t, 2)

			before := tracingDropCount(t)
			addressed := &raceEmitter{}
			misaddressed := &raceEmitter{}
			var wg sync.WaitGroup
			wg.Add(3)
			// The emitter, the untagged writer, and the teardown run with
			// no ordering between them: the release lands somewhere inside
			// the emission loop, and both emitters keep writing until the
			// release sentinel has come back.
			go func() {
				defer wg.Done()
				addressed.emitAddressed(rt, "sess-slot-a")
			}()
			go func() {
				defer wg.Done()
				// An untagged frame is fanned out to every slot's stream,
				// so it reaches this stream's handler and the sibling's.
				misaddressed.emitMisaddressed(rt, "")
			}()
			go func() {
				defer wg.Done()
				shutdownAfterJitter(t, client, rt, "sess-slot-a", shutdownSessionRequest("sess-slot-a"))
			}()
			decidedAtRelease := awaitReleaseSentinel(t, streamA, "sess-slot-a", fwd, addressed, misaddressed)
			wg.Wait()
			// Both streams decide the untagged frames, so both must be
			// drained before the accounting is read.
			drainToStatus(t, rt, streamA, "sess-slot-a")
			drainToStatus(t, rt, streamB, "sess-slot-b")

			raced := fwd.forwards()
			// Every tagged frame is decided on this stream alone (the
			// demux drops it on the sibling); every untagged frame is
			// decided on both streams.
			decisions := addressed.written + 2*misaddressed.written
			checkTracingAccounting(t, run, "sess-slot-a", decisions, raced,
				tracingDropCount(t)-before, decidedAtRelease)

			// The slot is gone for good, so every later frame on its
			// address fails live-binding confirmation.
			checkPostReleaseSilence(t, run, rt, streamA, "sess-slot-a", fwd, len(raced))
			// The sibling slot is untouched by its neighbour's teardown.
			drainToStatus(t, rt, streamB, "sess-slot-b")
		})
	}
}

// spec: 28.5.3 (set_tracing_context addressing, live-binding
// confirmation), 8.3 (tracing context, per-session scope)
//
// diagnosis: the adapter's addressing decision for a set_tracing_context
//
//	frame is wrong while the pod-global session is being released
//	underneath the stream that carries it. A count mismatch means a frame
//	was both forwarded and counted as dropped, or neither. A forward of a
//	frame carrying a slot id means address equality admitted a frame the
//	slotless stream does not address. A forward recorded after the release
//	sentinel means live-binding confirmation admitted a frame the handler
//	decided against a session binding that was already cleared. The
//	post-release phase failing means a released session still registers
//	tracing identifiers at all.
func TestSetTracingContextSessionReleaseRaceDecidesEveryFrameOnce_spec_28_5_3(t *testing.T) {
	for run := 0; run < tracingRaceRuns; run++ {
		t.Run(fmt.Sprintf("run%02d", run), func(t *testing.T) {
			s, rt, fwd, client := raceAdapter(t)
			startRaceSlot(t, s, "sess-solo")
			stream := openRaceAttach(t, client, "sess-solo", "sess-solo")
			rt.waitForSubscribers(t, 1)

			before := tracingDropCount(t)
			addressed := &raceEmitter{}
			misaddressed := &raceEmitter{}
			var wg sync.WaitGroup
			wg.Add(3)
			go func() {
				defer wg.Done()
				addressed.emitAddressed(rt, "sess-solo")
			}()
			go func() {
				defer wg.Done()
				// A frame carrying no address reaches every stream through
				// the fan-out and must be rejected on address equality
				// alone, on a pod of either concurrency.
				misaddressed.emitMisaddressed(rt, "")
			}()
			go func() {
				defer wg.Done()
				shutdownAfterJitter(t, client, rt, "sess-solo", shutdownSessionRequest("sess-solo"))
			}()
			decidedAtRelease := awaitReleaseSentinel(t, stream, "sess-solo", fwd, addressed, misaddressed)
			wg.Wait()
			drainToStatus(t, rt, stream, "sess-solo")

			raced := fwd.forwards()
			decisions := addressed.written + misaddressed.written
			checkTracingAccounting(t, run, "sess-solo", decisions, raced,
				tracingDropCount(t)-before, decidedAtRelease)

			checkPostReleaseSilence(t, run, rt, stream, "sess-solo", fwd, len(raced))
		})
	}
}

// spec: 28.5.3 (set_tracing_context addressing, live-binding
// confirmation), 6.4 (slot claim and release lifecycle), 8.3 (tracing
// context, per-session scope)
//
// diagnosis: a frame that was already in flight when the binding was
//
//	released is decided against the binding it was written under rather
//	than against the registry as it stands when the handler reaches it. The
//	relay is stopped inside its first forward, the rest of the frames are
//	parked in the runtime's fan-out and the stream's own buffer behind it,
//	and the teardown runs to completion before the relay is let go, so
//	every parked frame is decided after the release with no timing left in
//	the case. A forward of one means the teardown window is open for
//	everything the pipeline was holding at the moment the binding went
//	away, which is the window the emission order of a racing case cannot
//	see.
func TestSetTracingContextFramesInFlightAcrossReleaseAreDropped_spec_28_5_3(t *testing.T) {
	cases := []struct {
		name      string
		sessionID string
		slotID    string
		start     func(t *testing.T, s *adapter.Server)
		shutdown  *adapterv1.ShutdownRequest
	}{
		{
			name:      "coTenantedPod",
			sessionID: "sess-slot-a",
			slotID:    "sess-slot-a",
			start:     func(t *testing.T, s *adapter.Server) { startRaceSlot(t, s, "sess-slot-a") },
			shutdown:  shutdownSessionRequest("sess-slot-a"),
		},
		{
			name:      "poolOfOne",
			sessionID: "sess-solo",
			slotID:    "sess-solo",
			start:     func(t *testing.T, s *adapter.Server) { startRaceSlot(t, s, "sess-solo") },
			shutdown:  shutdownSessionRequest("sess-solo"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, rt, fwd, client := raceAdapter(t)
			release := fwd.stallOnFirstForward()
			t.Cleanup(release)
			tc.start(t, s)
			stream := openRaceAttach(t, client, tc.sessionID, tc.slotID)
			rt.waitForSubscribers(t, 1)

			// The first frame is forwarded while the binding is live and
			// leaves the relay stopped inside the forward.
			rt.output <- raceTracingFrame(tc.slotID, "0")
			fwd.awaitStall(t)

			before := tracingDropCount(t)
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 1; i <= tracingRaceInFlightFrames; i++ {
					rt.output <- raceTracingFrame(tc.slotID, strconv.Itoa(i))
				}
			}()
			if _, err := client.Shutdown(context.Background(), tc.shutdown); err != nil {
				t.Fatalf("Shutdown(%s/%s): %v", tc.sessionID, tc.slotID, err)
			}
			// The teardown has returned, so every frame the relay has not
			// reached yet is decided against a binding that is already gone.
			release()
			wg.Wait()
			drainToStatus(t, rt, stream, tc.slotID)

			got := fwd.forwards()
			if len(got) != 1 || got[0].seq != "0" {
				t.Fatalf("%d frames forwarded, want only the frame decided before the teardown: %v: "+
					"a frame the pipeline was holding when the binding was released still registered "+
					"tracing identifiers", len(got), got)
			}
			if drops := tracingDropCount(t) - before; drops != float64(tracingRaceInFlightFrames) {
				t.Fatalf("drop counter moved by %v across the teardown, want %d: the frames in flight "+
					"were neither forwarded nor counted", drops, tracingRaceInFlightFrames)
			}
		})
	}
}
