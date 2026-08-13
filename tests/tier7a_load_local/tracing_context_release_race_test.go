// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage for the adapter-side addressing
// decision the §28.5.3 `set_tracing_context` frame goes through. The
// decision is computed from one sampling of the adapter's registry under a
// single lock hold, so a teardown landing between what would otherwise be
// two reads cannot admit a frame the live-binding condition exists to
// reject. A runtime emits the frame in a loop on an open Attach stream
// while the binding that stream carries is released underneath it, with no
// ordering between the two goroutines.
//
// The invariant that holds under either interleaving is an accounting one:
// every frame the runtime writes is either forwarded to the gateway with
// the session id the stream is bound to, or counted on the drop counter,
// and never both and never neither. Once the release has completed and
// been observed, every later frame drops.
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
	"sync"
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

// tracingRaceFrames is the number of set_tracing_context frames one run
// emits while the release goroutine runs, and tracingRaceRuns is how many
// times the whole scenario repeats so the release lands at varying points
// in the emission loop. `lenny-test stress --test <Name> --runs <N>` is the
// flake-budget form for driving the window harder.
const (
	tracingRaceFrames = 64
	tracingRaceRuns   = 24
)

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

// raceForwarder records every CallPlatformTool the adapter makes so a case
// can count the forwards and read back the session id each one carried.
type raceForwarder struct {
	mu       sync.Mutex
	sessions []string
}

func (f *raceForwarder) ListPlatformTools(context.Context, string) ([]adaptermcp.Tool, error) {
	return nil, nil
}

func (f *raceForwarder) CallPlatformTool(_ context.Context, sessionID, _ string, _ json.RawMessage) (json.RawMessage, error) {
	f.mu.Lock()
	f.sessions = append(f.sessions, sessionID)
	f.mu.Unlock()
	return json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`), nil
}

// forwards returns the number of recorded calls and the session id of each.
func (f *raceForwarder) forwards() (int, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sessions), append([]string(nil), f.sessions...)
}

// raceTracingFrame builds a set_tracing_context frame, stamping slotID when
// it is non-empty the way the concurrent dispatch loop does.
func raceTracingFrame(slotID string) []byte {
	if slotID == "" {
		return []byte(`{"type":"set_tracing_context","context":{"langsmith_run_id":"run_race"}}`)
	}
	return []byte(`{"type":"set_tracing_context","slotId":"` + slotID +
		`","context":{"langsmith_run_id":"run_race"}}`)
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

// emitTracingFrames writes n set_tracing_context frames on one address,
// yielding between frames so the release goroutine interleaves at a
// different point on each run.
func emitTracingFrames(rt *raceRuntime, slotID string, n int) {
	for i := 0; i < n; i++ {
		rt.output <- raceTracingFrame(slotID)
	}
}

// checkTracingAccounting asserts the addressing decision accounted for
// every emitted frame exactly once and that no forward named a session
// other than the stream's own.
func checkTracingAccounting(t *testing.T, run int, sessionID string, emitted int, forwards int, sessions []string, drops float64) {
	t.Helper()
	if float64(forwards)+drops != float64(emitted) {
		t.Fatalf("run %d: %d frames emitted but %d forwarded and %v dropped; a frame was both "+
			"forwarded and counted, or lost without either, so the addressing decision did not "+
			"read the registry once", run, emitted, forwards, drops)
	}
	for i, got := range sessions {
		if got != sessionID {
			t.Fatalf("run %d: forward %d carried session %q, want %q: the addressing decision "+
				"registered tracing identifiers against a session this stream is not bound to",
				run, i, got, sessionID)
		}
	}
}

// spec: 28.5.3 (set_tracing_context addressing, live-binding
// confirmation), 6.4 (slot claim and release lifecycle), 8.3 (tracing
// context, per-session scope)
//
// diagnosis: the addressing helper reads the adapter's registry more than
//
//	once per frame, so a slot teardown landing between the reads lets a
//	frame through that live-binding confirmation exists to reject, and the
//	adapter registers tracing identifiers against a slot session that has
//	already been released. A count mismatch means a frame was both
//	forwarded and counted as dropped, or neither.
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
			var wg sync.WaitGroup
			wg.Add(2)
			// The emitter and the teardown run with no ordering between
			// them: the release lands somewhere inside the emission loop.
			go func() {
				defer wg.Done()
				emitTracingFrames(rt, "slot-a", tracingRaceFrames)
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
			}()
			wg.Wait()
			drainToStatus(t, rt, streamA, "slot-a")

			forwards, sessions := fwd.forwards()
			checkTracingAccounting(t, run, "sess-slot-a", tracingRaceFrames, forwards, sessions,
				tracingDropCount(t)-before)

			// The slot is gone for good, so every later frame on its
			// address fails live-binding confirmation.
			settled := tracingDropCount(t)
			emitTracingFrames(rt, "slot-a", tracingRaceFrames)
			drainToStatus(t, rt, streamA, "slot-a")
			if after, _ := fwd.forwards(); after != forwards {
				t.Fatalf("run %d: %d frames forwarded after the slot was released, want none: "+
					"a released slot still registers tracing identifiers", run, after-forwards)
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
// diagnosis: the addressing helper reads the adapter's registry more than
//
//	once per frame on the pod-global branch, so a session release landing
//	between the reads lets an untagged frame through and the adapter
//	registers tracing identifiers against a session the pod no longer
//	holds. A count mismatch means a frame was both forwarded and counted
//	as dropped, or neither.
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
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				emitTracingFrames(rt, "", tracingRaceFrames)
			}()
			go func() {
				defer wg.Done()
				time.Sleep(time.Duration(rand.Intn(2000)) * time.Microsecond)
				if _, err := client.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
					SessionId: &adapterv1.SessionId{Value: "sess-solo"},
				}); err != nil {
					t.Errorf("run %d: Shutdown(pod-global): %v", run, err)
				}
			}()
			wg.Wait()
			drainToStatus(t, rt, stream, "")

			forwards, sessions := fwd.forwards()
			checkTracingAccounting(t, run, "sess-solo", tracingRaceFrames, forwards, sessions,
				tracingDropCount(t)-before)

			settled := tracingDropCount(t)
			emitTracingFrames(rt, "", tracingRaceFrames)
			drainToStatus(t, rt, stream, "")
			if after, _ := fwd.forwards(); after != forwards {
				t.Fatalf("run %d: %d frames forwarded after the session was released, want none: "+
					"a released session still registers tracing identifiers", run, after-forwards)
			}
			if got := tracingDropCount(t) - settled; got != float64(tracingRaceFrames) {
				t.Fatalf("run %d: drop counter moved by %v after the session was released, want %d",
					run, got, tracingRaceFrames)
			}
		})
	}
}
