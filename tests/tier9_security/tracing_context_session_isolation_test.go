// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 cross-session isolation for the §28.5.3 set_tracing_context
// addressing rule, exercised against the real adapter.Server over a live
// gRPC Attach stream and a gateway double that stores each session's
// tracing context the way the lenny/set_tracing_context platform tool does
// (tracing.Merge followed by tracing.Validate against the named session's
// row).
//
// One runtime process serves every slot on a concurrent pod and its output
// is fanned out to every Attach stream (§5.2, §13.1), so a frame that
// carries no slotId reaches every slot's stream. The isolation boundary
// under test is that such a frame writes no session's tracing context: the
// stream's own (session, slot) address is the frame's address, and a frame
// that does not match it is dropped.
//
// The write these cases forbid is permanent. pkg/delegation/tracing merges
// without overwriting and the tree exposes no delete path, so an identifier
// written onto a sibling session stays on that session for its lifetime and
// counts against the §8.3 32-entry bound; a session driven to that bound
// rejects every later registration on itself and on its delegated children.
//
// The cases run in-process rather than on the Kind e2e cluster because the
// addressing rule is transport-independent adapter logic evaluated against
// the pod's own slot registry.
package tier9_security_test

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/mcp"
	"github.com/lennylabs/lenny/pkg/delegation/tracing"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// tracingIsolationRuntime is the pod's single runtime process. Its output
// is broadcast to every Attach subscriber, mirroring
// SocketRuntimeProcess: one process per pod serves every slot and each
// Attach stream demultiplexes the shared stream by slotId.
type tracingIsolationRuntime struct {
	mu   sync.Mutex
	cond *sync.Cond
	subs []chan []byte
}

func newTracingIsolationRuntime() *tracingIsolationRuntime {
	rt := &tracingIsolationRuntime{}
	rt.cond = sync.NewCond(&rt.mu)
	return rt
}

func (r *tracingIsolationRuntime) Start(context.Context, string) error           { return nil }
func (r *tracingIsolationRuntime) WriteEnvelope(string, []byte) error            { return nil }
func (r *tracingIsolationRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (r *tracingIsolationRuntime) Close(context.Context, string) error           { return nil }

func (r *tracingIsolationRuntime) Output(ctx context.Context, _ string) (<-chan []byte, error) {
	sub := make(chan []byte, 8)
	r.mu.Lock()
	r.subs = append(r.subs, sub)
	r.cond.Broadcast()
	r.mu.Unlock()
	go func() {
		<-ctx.Done()
		r.mu.Lock()
		for i, s := range r.subs {
			if s == sub {
				r.subs = append(r.subs[:i], r.subs[i+1:]...)
				break
			}
		}
		r.mu.Unlock()
	}()
	return sub, nil
}

// emit broadcasts one frame to every current subscriber, which is what the
// pod's single runtime connection does with a frame written on its stdout.
func (r *tracingIsolationRuntime) emit(line []byte) {
	r.mu.Lock()
	subs := append([]chan []byte(nil), r.subs...)
	r.mu.Unlock()
	for _, s := range subs {
		s <- line
	}
}

// waitForSubscribers blocks until n Attach streams have subscribed. A frame
// emitted before a stream subscribes is not delivered to it, so a case that
// asserts a stream did not act on a frame must first know the stream would
// have received it.
func (r *tracingIsolationRuntime) waitForSubscribers(t *testing.T, n int) {
	t.Helper()
	r.mu.Lock()
	// A watchdog wakes the wait so a stream that never subscribes surfaces
	// as a failure instead of a deadlock.
	timedOut := false
	timer := time.AfterFunc(10*time.Second, func() {
		r.mu.Lock()
		timedOut = true
		r.cond.Broadcast()
		r.mu.Unlock()
	})
	defer timer.Stop()
	for len(r.subs) < n && !timedOut {
		r.cond.Wait()
	}
	got := len(r.subs)
	r.mu.Unlock()
	if got < n {
		t.Fatalf("only %d of %d Attach streams subscribed to the runtime output before timeout", got, n)
	}
}

// tracingGateway stands in for the gateway's lenny/set_tracing_context
// platform tool. It records each session's tracing context under the same
// §8.3 rules the tool applies: the submitted identifiers merge into the
// named session's recorded context without overwriting, and the merged
// result is validated. The session it writes is the one named in the call
// arguments, which is the session the adapter injected.
type tracingGateway struct {
	mu       sync.Mutex
	contexts map[string]map[string]string
}

func newTracingGateway() *tracingGateway {
	return &tracingGateway{contexts: map[string]map[string]string{}}
}

func (g *tracingGateway) ListPlatformTools(context.Context, string) ([]mcp.Tool, error) {
	return []mcp.Tool{{Name: "lenny/set_tracing_context", Description: "register tracing identifiers"}}, nil
}

func (g *tracingGateway) CallPlatformTool(_ context.Context, _, toolName string, args json.RawMessage) (json.RawMessage, error) {
	if toolName != "lenny/set_tracing_context" {
		return json.RawMessage(`{"content":[]}`), nil
	}
	var in struct {
		SessionID string            `json:"sessionId"`
		Context   map[string]string `json:"context"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	merged := tracing.Merge(g.contexts[in.SessionID], in.Context)
	if err := tracing.Validate(merged); err != nil {
		return nil, err
	}
	g.contexts[in.SessionID] = merged
	return json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`), nil
}

// seed records a session's inherited tracing context, so a case asserting
// the context is unchanged asserts against a populated row rather than an
// absent one.
func (g *tracingGateway) seed(sessionID string, tc map[string]string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.contexts[sessionID] = tc
}

func (g *tracingGateway) contextOf(sessionID string) map[string]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make(map[string]string, len(g.contexts[sessionID]))
	for k, v := range g.contexts[sessionID] {
		out[k] = v
	}
	return out
}

// requireTracingContext fails when a session's recorded tracing context is
// not exactly want.
func requireTracingContext(t *testing.T, g *tracingGateway, sessionID string, want map[string]string) {
	t.Helper()
	got := g.contextOf(sessionID)
	if len(got) != len(want) {
		t.Fatalf("session %s tracingContext = %v, want %v", sessionID, got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("session %s tracingContext[%q] = %q, want %q (full context %v)", sessionID, k, got[k], v, got)
		}
	}
}

// tracingIsolationPod builds a concurrent-slot adapter server wired to the
// pod's single runtime and the gateway double, and returns an Attach
// client for it.
func tracingIsolationPod(t *testing.T) (*adapter.Server, *tracingIsolationRuntime, *tracingGateway, adapterv1.AdapterClient) {
	t.Helper()
	base := t.TempDir()
	s := adapter.New("test")
	s.WorkspaceBase = filepath.Join(base, "workspace")
	s.SessionsRoot = filepath.Join(base, "sessions")
	s.ArtifactsRoot = filepath.Join(base, "artifacts")
	s.CredentialsDir = filepath.Join(base, "run", "lenny")
	rt := newTracingIsolationRuntime()
	s.Runtime = rt
	gw := newTracingGateway()
	s.PlatformForwarder = gw

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
		t.Fatalf("dial adapter: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return s, rt, gw, adapterv1.NewAdapterClient(conn)
}

// startTracingSlot claims a §6.4 slot on the pod for sessionID.
func startTracingSlot(t *testing.T, s *adapter.Server, sessionID, slotID string) {
	t.Helper()
	if _, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Runtime:   "echo",
		SlotId:    &adapterv1.SlotId{Value: slotID},
	}); err != nil {
		t.Fatalf("StartSession(%s on slot %s): %v", sessionID, slotID, err)
	}
}

// attachTracingStream binds an Attach stream to (sessionID, slotID). An
// empty slotID binds the pod-global base path.
func attachTracingStream(t *testing.T, client adapterv1.AdapterClient, sessionID, slotID string) adapterv1.Adapter_AttachClient {
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
		t.Fatalf("bind Attach(%s/%s): %v", sessionID, slotID, err)
	}
	return stream
}

// tracingContextFrame builds a set_tracing_context frame carrying one
// identifier, tagged with slotID when slotID is non-empty.
func tracingContextFrame(slotID, runID string) []byte {
	if slotID == "" {
		return []byte(`{"type":"set_tracing_context","context":{"langsmith_run_id":"` + runID + `"}}`)
	}
	return []byte(`{"type":"set_tracing_context","slotId":"` + slotID +
		`","context":{"langsmith_run_id":"` + runID + `"}}`)
}

// tracingStatusFrame builds a status frame, which the Attach loop relays as
// content. The output relay handles one frame at a time, so receiving the
// status frame that follows a set_tracing_context frame proves the adapter
// finished handling that frame and the assertions below read settled state.
func tracingStatusFrame(slotID string) []byte {
	if slotID == "" {
		return []byte(`{"type":"status","state":"thinking"}`)
	}
	return []byte(`{"type":"status","slotId":"` + slotID + `","state":"thinking"}`)
}

// awaitTracingStatus receives the next relayed frame and requires it to be
// the status frame.
func awaitTracingStatus(t *testing.T, stream adapterv1.Adapter_AttachClient) {
	t.Helper()
	got, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	var frame struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(got.GetEnvelopeJson(), &frame); err != nil {
		t.Fatalf("decode relayed frame: %v", err)
	}
	if frame.Type != "status" {
		t.Fatalf("relayed frame type = %q, want status", frame.Type)
	}
}

// spec: 28.5.3 (set_tracing_context addressing), 8.3 (tracing context,
// per-session scope), 13.1 (concurrent-slot isolation boundaries)
//
// diagnosis: a failure means one runtime wrote tracing identifiers onto a
// sibling session on the same concurrent pod. The pod's single runtime
// process serves every slot and its output reaches every slot's Attach
// stream, so an untagged set_tracing_context frame handled by each stream
// merges one slot's identifiers into every other slot's session. The write
// is permanent: pkg/delegation/tracing merges without overwriting and the
// tree has no delete path, so the entries stay on the sibling session and
// count against the 32-entry bound, and a session driven to that bound
// rejects every later registration on itself and on its delegated children.
func TestUntaggedTracingFrameWritesNoSiblingSlotSession_spec_28_5_3(t *testing.T) {
	s, rt, gw, client := tracingIsolationPod(t)
	startTracingSlot(t, s, "sess-slot-a", "slot-a")
	startTracingSlot(t, s, "sess-slot-b", "slot-b")
	inheritedA := map[string]string{"traceparent": "00-aaaa-aaaa-01"}
	inheritedB := map[string]string{"traceparent": "00-bbbb-bbbb-01"}
	gw.seed("sess-slot-a", map[string]string{"traceparent": "00-aaaa-aaaa-01"})
	gw.seed("sess-slot-b", map[string]string{"traceparent": "00-bbbb-bbbb-01"})

	streamA := attachTracingStream(t, client, "sess-slot-a", "slot-a")
	streamB := attachTracingStream(t, client, "sess-slot-b", "slot-b")
	rt.waitForSubscribers(t, 2)

	// The runtime writes a frame carrying no slotId. It reaches both slots'
	// streams and addresses neither.
	rt.emit(tracingContextFrame("", "run_untagged"))
	rt.emit(tracingStatusFrame("slot-a"))
	rt.emit(tracingStatusFrame("slot-b"))
	awaitTracingStatus(t, streamA)
	awaitTracingStatus(t, streamB)

	requireTracingContext(t, gw, "sess-slot-a", inheritedA)
	requireTracingContext(t, gw, "sess-slot-b", inheritedB)

	// A correctly addressed frame still registers, and only on the session
	// that owns the slot it names, so the isolation asserted above is the
	// addressing rule rather than a broken registration path.
	rt.emit(tracingContextFrame("slot-a", "run_a"))
	rt.emit(tracingStatusFrame("slot-a"))
	rt.emit(tracingStatusFrame("slot-b"))
	awaitTracingStatus(t, streamA)
	awaitTracingStatus(t, streamB)

	requireTracingContext(t, gw, "sess-slot-a", map[string]string{
		"traceparent": "00-aaaa-aaaa-01", "langsmith_run_id": "run_a",
	})
	requireTracingContext(t, gw, "sess-slot-b", inheritedB)
}

// spec: 28.5.3 (set_tracing_context addressing), 8.3 (tracing context,
// per-session scope), 13.1 (concurrent-slot isolation boundaries)
//
// diagnosis: a failure means one runtime wrote tracing identifiers onto a
// sibling session, here the pod-global session of a pod that also holds an
// occupied slot. The adapter imposes no guard against that coexistence, and
// an untagged frame from the slot's runtime reaches the slotless stream and
// satisfies address equality, so only the empty-registry term keeps it off
// the pod-global session. The write is permanent: pkg/delegation/tracing
// merges without overwriting and the tree has no delete path, so the
// entries stay on the pod-global session and count against the 32-entry
// bound, and a session driven to that bound rejects every later
// registration on itself and on its delegated children.
func TestUntaggedTracingFrameWritesNoPodGlobalSessionBesideASlot_spec_28_5_3(t *testing.T) {
	s, rt, gw, client := tracingIsolationPod(t)
	// The pod holds an occupied slot and a pod-global session at once: the
	// slot is claimed through a slot-qualified StartSession and the
	// pod-global session through a slotless one, which the adapter's
	// separate registries permit.
	startTracingSlot(t, s, "sess-slot-a", "slot-a")
	if _, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-pod"},
		Runtime:   "echo",
	}); err != nil {
		t.Fatalf("StartSession(pod-global): %v", err)
	}
	inherited := map[string]string{"traceparent": "00-cccc-cccc-01"}
	gw.seed("sess-pod", map[string]string{"traceparent": "00-cccc-cccc-01"})

	podStream := attachTracingStream(t, client, "sess-pod", "")
	rt.waitForSubscribers(t, 1)

	// The frame is the sibling slot's runtime output, carrying no slotId.
	rt.emit(tracingContextFrame("", "run_sibling"))
	rt.emit(tracingStatusFrame(""))
	awaitTracingStatus(t, podStream)

	requireTracingContext(t, gw, "sess-pod", inherited)
	if got := gw.contextOf("sess-slot-a"); len(got) != 0 {
		t.Fatalf("slot session tracingContext = %v, want no entries", got)
	}
}
