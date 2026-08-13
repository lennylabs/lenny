// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage for the adapter-side addressing
// decision the §28.5.3 `set_tracing_context` frame goes through. A runtime
// emits the frame in a loop on an open Attach stream while the binding that
// stream carries is released underneath it, with no ordering between the
// two goroutines, and a second runtime writes frames the stream under test
// does not address into the same fan-out.
//
// Four properties hold under every interleaving, and each case asserts all
// four:
//
//   - Accounting. Every frame the addressing decision reaches is either
//     forwarded to the gateway or counted on the drop counter, and never
//     both and never neither.
//   - Session binding. A forward carries the session id of the stream that
//     carried the frame, so a frame fanned out to a sibling stream cannot
//     register against that sibling's session.
//   - Address equality. A frame carrying an address the stream does not
//     hold is never forwarded, even while the release is in flight.
//   - Release ordering. A frame whose emission began after the Shutdown RPC
//     returned, which is after the binding was released, is never
//     forwarded. Each addressed frame carries its sequence number and the
//     release goroutine records the sequence the emitter had reached when
//     Shutdown returned, so the ordering is decidable from the recorded
//     forwards alone.
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

// tracingRaceFrames is the number of addressed set_tracing_context frames
// one run emits while the release goroutine runs, tracingRaceMisaddressed
// is how many frames carrying an address the stream under test does not
// hold are interleaved with them, and tracingRaceRuns is how many times the
// whole scenario repeats so the release lands at varying points in the
// emission loop. `lenny-test stress --test <Name> --runs <N>` is the
// flake-budget form for driving the window harder.
const (
	tracingRaceFrames       = 64
	tracingRaceMisaddressed = 16
	tracingRaceRuns         = 24
)

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
// can count the forwards and read back which frame each one carried.
type raceForwarder struct {
	mu       sync.Mutex
	recorded []raceForward
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
	return json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`), nil
}

// forwards returns the calls recorded so far.
func (f *raceForwarder) forwards() []raceForward {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]raceForward(nil), f.recorded...)
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

// openRaceAttach binds an Attach stream to (sessionID, slotID). An empty
// slotID binds the pod-global path.
func openRaceAttach(t *testing.T, client adapterv1.AdapterClient, sessionID, slotID string) adapterv1.Adapter_AttachClient {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stream, err := client.Attach(ctx)
	if err != nil {
		t.Fatalf("Attach(%s/%s): %v", sessionID, slotID, err)
	}
	req := &adapterv1.AttachRequest{SessionId: &adapterv1.SessionId{Value: sessionID}}
	if slotID != "" {
		req.SlotId = &adapterv1.SlotId{Value: slotID}
	}
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

// raceEmitter writes sequence-numbered frames on the address the stream
// under test holds and publishes how far it has got. The mark is stored
// before the frame is sent, so a mark read at some moment T bounds the
// emission: every frame numbered above the mark began its send after T.
// Reading the mark once the Shutdown RPC has returned therefore names a
// frame boundary after which the binding was certainly released.
type raceEmitter struct {
	progress atomic.Int64
}

func newRaceEmitter() *raceEmitter {
	e := &raceEmitter{}
	e.progress.Store(-1)
	return e
}

// emit writes n addressed frames on slotID, numbered from zero.
func (e *raceEmitter) emit(rt *raceRuntime, slotID string, n int) {
	for i := 0; i < n; i++ {
		e.progress.Store(int64(i))
		rt.output <- raceTracingFrame(slotID, strconv.Itoa(i))
	}
}

// mark returns the highest frame number whose send had begun, which is an
// upper bound on the frames emitted before the caller read it.
func (e *raceEmitter) mark() int64 {
	return e.progress.Load()
}

// emitMisaddressedFrames writes n frames carrying an address the stream
// under test does not hold. They reach the same fan-out, so they exercise
// the address-equality condition and, on a concurrent pod, the sibling
// stream's own handler.
func emitMisaddressedFrames(rt *raceRuntime, slotID string, n int) {
	for i := 0; i < n; i++ {
		rt.output <- raceTracingFrame(slotID, raceMisaddressedSeq)
	}
}

// checkTracingAccounting asserts the four properties the race establishes:
// every decision accounted for exactly once, no forward against a session
// the stream is not bound to, no forward of a frame the stream does not
// address, and no forward of a frame emitted after the release completed.
func checkTracingAccounting(t *testing.T, run int, sessionID string, decisions int, got []raceForward, drops float64, mark int64) {
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
		seq, err := strconv.Atoi(fw.seq)
		if err != nil {
			t.Fatalf("run %d: forward %d carried unreadable frame label %q", run, i, fw.seq)
		}
		if int64(seq) > mark {
			t.Fatalf("run %d: forward %d carried frame %d, whose emission began after the release "+
				"completed (last frame in flight at that point: %d): live-binding confirmation "+
				"admitted a frame whose binding was already released", run, i, seq, mark)
		}
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
//	address equality admitted what it must reject. A forward of a frame
//	emitted after the Shutdown RPC returned means live-binding confirmation
//	admitted a frame against a slot binding that was already gone. The
//	post-release phase failing means a released slot still registers
//	tracing identifiers at all.
func TestSetTracingContextSlotReleaseRaceDecidesEveryFrameOnce_spec_28_5_3(t *testing.T) {
	for run := 0; run < tracingRaceRuns; run++ {
		t.Run(fmt.Sprintf("run%02d", run), func(t *testing.T) {
			s, rt, fwd, client := raceAdapter(t)
			for _, slot := range []string{"slot-a", "slot-b"} {
				if _, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
					SessionId: &adapterv1.SessionId{Value: "sess-" + slot},
					Runtime:   "echo",
					SlotId:    &adapterv1.SlotId{Value: slot},
				}); err != nil {
					t.Fatalf("run %d: StartSession(%s): %v", run, slot, err)
				}
			}
			streamA := openRaceAttach(t, client, "sess-slot-a", "slot-a")
			streamB := openRaceAttach(t, client, "sess-slot-b", "slot-b")
			rt.waitForSubscribers(t, 2)

			before := tracingDropCount(t)
			emitter := newRaceEmitter()
			var mark int64
			var wg sync.WaitGroup
			wg.Add(3)
			// The emitter, the untagged writer, and the teardown run with
			// no ordering between them: the release lands somewhere inside
			// the emission loop.
			go func() {
				defer wg.Done()
				emitter.emit(rt, "slot-a", tracingRaceFrames)
			}()
			go func() {
				defer wg.Done()
				// An untagged frame is fanned out to every slot's stream,
				// so it reaches this stream's handler and the sibling's.
				emitMisaddressedFrames(rt, "", tracingRaceMisaddressed)
			}()
			go func() {
				defer wg.Done()
				time.Sleep(time.Duration(rand.Intn(2000)) * time.Microsecond)
				if _, err := client.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
					SessionId: &adapterv1.SessionId{Value: "sess-slot-a"},
					SlotId:    &adapterv1.SlotId{Value: "slot-a"},
				}); err != nil {
					t.Errorf("run %d: Shutdown(slot-a): %v", run, err)
				}
				// releaseSlot has certainly run by the time the RPC
				// returns, so every frame numbered above this mark is
				// emitted against a released binding.
				mark = emitter.mark()
			}()
			wg.Wait()
			// Both streams decide the untagged frames, so both must be
			// drained before the accounting is read.
			drainToStatus(t, rt, streamA, "slot-a")
			drainToStatus(t, rt, streamB, "slot-b")

			raced := fwd.forwards()
			// Every tagged frame is decided on this stream alone (the
			// demux drops it on the sibling); every untagged frame is
			// decided on both streams.
			decisions := tracingRaceFrames + 2*tracingRaceMisaddressed
			checkTracingAccounting(t, run, "sess-slot-a", decisions, raced,
				tracingDropCount(t)-before, mark)

			// The slot is gone for good, so every later frame on its
			// address fails live-binding confirmation.
			settled := tracingDropCount(t)
			newRaceEmitter().emit(rt, "slot-a", tracingRaceFrames)
			drainToStatus(t, rt, streamA, "slot-a")
			if after := fwd.forwards(); len(after) != len(raced) {
				t.Fatalf("run %d: %d frames forwarded after the slot was released, want none: "+
					"a released slot still registers tracing identifiers", run, len(after)-len(raced))
			}
			if got := tracingDropCount(t) - settled; got != float64(tracingRaceFrames) {
				t.Fatalf("run %d: drop counter moved by %v after the slot was released, want %d",
					run, got, tracingRaceFrames)
			}
			// The sibling slot is untouched by its neighbour's teardown.
			drainToStatus(t, rt, streamB, "slot-b")
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
//	slotless stream does not address. A forward of a frame emitted after
//	the Shutdown RPC returned means live-binding confirmation admitted a
//	frame against a session binding that was already cleared. The
//	post-release phase failing means a released session still registers
//	tracing identifiers at all.
func TestSetTracingContextSessionReleaseRaceDecidesEveryFrameOnce_spec_28_5_3(t *testing.T) {
	for run := 0; run < tracingRaceRuns; run++ {
		t.Run(fmt.Sprintf("run%02d", run), func(t *testing.T) {
			s, rt, fwd, client := raceAdapter(t)
			if _, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
				SessionId: &adapterv1.SessionId{Value: "sess-solo"},
				Runtime:   "echo",
			}); err != nil {
				t.Fatalf("run %d: StartSession: %v", run, err)
			}
			stream := openRaceAttach(t, client, "sess-solo", "")
			rt.waitForSubscribers(t, 1)

			before := tracingDropCount(t)
			emitter := newRaceEmitter()
			var mark int64
			var wg sync.WaitGroup
			wg.Add(3)
			go func() {
				defer wg.Done()
				emitter.emit(rt, "", tracingRaceFrames)
			}()
			go func() {
				defer wg.Done()
				// A slotless stream reads the fan-out unfiltered, so a
				// frame carrying a slot id reaches its handler and must be
				// rejected on address equality alone.
				emitMisaddressedFrames(rt, "slot-x", tracingRaceMisaddressed)
			}()
			go func() {
				defer wg.Done()
				time.Sleep(time.Duration(rand.Intn(2000)) * time.Microsecond)
				if _, err := client.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
					SessionId: &adapterv1.SessionId{Value: "sess-solo"},
				}); err != nil {
					t.Errorf("run %d: Shutdown(pod-global): %v", run, err)
				}
				mark = emitter.mark()
			}()
			wg.Wait()
			drainToStatus(t, rt, stream, "")

			raced := fwd.forwards()
			decisions := tracingRaceFrames + tracingRaceMisaddressed
			checkTracingAccounting(t, run, "sess-solo", decisions, raced,
				tracingDropCount(t)-before, mark)

			settled := tracingDropCount(t)
			newRaceEmitter().emit(rt, "", tracingRaceFrames)
			drainToStatus(t, rt, stream, "")
			if after := fwd.forwards(); len(after) != len(raced) {
				t.Fatalf("run %d: %d frames forwarded after the session was released, want none: "+
					"a released session still registers tracing identifiers", run, len(after)-len(raced))
			}
			if got := tracingDropCount(t) - settled; got != float64(tracingRaceFrames) {
				t.Fatalf("run %d: drop counter moved by %v after the session was released, want %d",
					run, got, tracingRaceFrames)
			}
		})
	}
}
