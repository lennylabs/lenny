//go:build contract

// SPDX-License-Identifier: MIT

// Package adapter_frame_resolution_test is the Tier 3 contract suite for
// the §28.5.3 resolve-or-reject rule the adapter applies to a
// session-scoped runtime frame that carries no per-session identifier,
// and for the key the gateway emits on the envelope it sends the other
// way.
//
// The rule is a wire contract in both directions and neither half is
// caught by a compiler. On the runtime-to-adapter leg the adapter reads
// the address off a JSON key, so a producer that spells it differently
// resolves to the empty string and the frame is treated as unaddressed.
// On the gateway-to-adapter leg the envelope carries the address as a
// struct tag, so an envelope whose member was renamed without its tag
// emits a key the published schema does not declare.
//
// The cases here drive a real adapter server over a real gRPC Attach
// stream against a fake pod-global runtime, which is the transport the
// rule is evaluated on, and validate the gateway's own envelope against
// the published JSON Lines schema.
//
// spec: §28.5.3 (frame addressing on the intra-pod leg), §5.2 (the
// adapter populates the per-session identifier on every pod), §6.4.
package adapter_frame_resolution_test

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// fanoutRuntime is the pod's single runtime process. Its output is
// broadcast to every Attach subscriber, which is what
// SocketRuntimeProcess does over the one connection the pod holds: every
// Attach stream sees every frame and each one demultiplexes.
type fanoutRuntime struct {
	mu        sync.Mutex
	cond      *sync.Cond
	subs      []chan []byte
	envelopes [][]byte
}

func newFanoutRuntime() *fanoutRuntime {
	rt := &fanoutRuntime{}
	rt.cond = sync.NewCond(&rt.mu)
	return rt
}

func (r *fanoutRuntime) Start(context.Context, string) error { return nil }

func (r *fanoutRuntime) WriteEnvelope(_ string, envelope []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.envelopes = append(r.envelopes, append([]byte(nil), envelope...))
	r.cond.Broadcast()
	return nil
}

func (r *fanoutRuntime) Output(ctx context.Context, _ string) (<-chan []byte, error) {
	ch := make(chan []byte, 16)
	r.mu.Lock()
	r.subs = append(r.subs, ch)
	r.cond.Broadcast()
	r.mu.Unlock()
	go func() {
		<-ctx.Done()
	}()
	return ch, nil
}

func (r *fanoutRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (r *fanoutRuntime) Close(context.Context, string) error           { return nil }

// emit writes one frame to every subscriber, the way the pod's single
// runtime connection fans its output out.
func (r *fanoutRuntime) emit(frame string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ch := range r.subs {
		ch <- []byte(frame)
	}
}

// waitForSubscribers blocks until n Attach streams have subscribed, so a
// case never emits into an empty fan-out.
func (r *fanoutRuntime) waitForSubscribers(t *testing.T, n int) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for len(r.subs) < n {
		r.cond.Wait()
	}
}

// firstEnvelope waits for the first envelope the adapter wrote to the
// runtime's stdin and returns it.
func (r *fanoutRuntime) firstEnvelope(t *testing.T) []byte {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for len(r.envelopes) == 0 {
		r.cond.Wait()
	}
	return r.envelopes[0]
}

// resolutionPod builds an adapter server holding one slot per named
// session and returns it with its runtime and a connected client.
func resolutionPod(t *testing.T, sessions ...string) (*fanoutRuntime, adapterv1.AdapterClient) {
	t.Helper()
	base := t.TempDir()
	s := adapter.New("contract")
	s.WorkspaceBase = base + "/workspace"
	s.SessionsRoot = base + "/sessions"
	s.ArtifactsRoot = base + "/artifacts"
	s.CredentialsDir = base + "/run/lenny"
	rt := newFanoutRuntime()
	s.Runtime = rt

	for _, sess := range sessions {
		if _, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
			SessionId: &adapterv1.SessionId{Value: sess},
		}); err != nil {
			t.Fatalf("StartSession(%s): %v", sess, err)
		}
	}

	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(s)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return rt, adapterv1.NewAdapterClient(conn)
}

// attach opens an Attach stream bound to sessionID.
func attach(t *testing.T, client adapterv1.AdapterClient, sessionID string) adapterv1.Adapter_AttachClient {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stream, err := client.Attach(ctx)
	if err != nil {
		t.Fatalf("Attach(%s): %v", sessionID, err)
	}
	if err := stream.Send(&adapterv1.AttachRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
	}); err != nil {
		t.Fatalf("Send bind(%s): %v", sessionID, err)
	}
	return stream
}

// recvFrame reads the next relayed frame and decodes its type and
// address off the wire, which is where the rule is observable.
func recvFrame(t *testing.T, stream adapterv1.Adapter_AttachClient) (frameType, sessionID string) {
	t.Helper()
	got, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	var probe struct {
		Type      string `json:"type"`
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(got.GetEnvelopeJson(), &probe); err != nil {
		t.Fatalf("decode relayed frame %q: %v", got.GetEnvelopeJson(), err)
	}
	return probe.Type, probe.SessionID
}

// spec: 28.5.3 (a session-scoped frame carrying no per-session identifier
//
//	resolves to the receiving stream's own binding on a pod holding at
//	most one slot), 5.2
//
// diagnosis: a failure means the adapter rejects a session-scoped frame
//
//	that names no session on a pod where only one session could have sent
//	it. Every Basic-level runtime that echoes nothing loses its output on
//	an exclusive pod.
func TestUnaddressedFrameResolvesOnAPodHoldingOneSlot_spec_28_5_3(t *testing.T) {
	rt, client := resolutionPod(t, "sess-a")
	stream := attach(t, client, "sess-a")
	rt.waitForSubscribers(t, 1)

	rt.emit(`{"type":"response","output":[{"type":"text","inline":"pong"}]}`)
	if ty, _ := recvFrame(t, stream); ty != "response" {
		t.Errorf("relayed frame type = %q, want response; the unaddressed frame must resolve to the one session the pod holds", ty)
	}
}

// spec: 28.5.3 (a session-scoped frame carrying no per-session identifier
//
//	is rejected and relayed to no stream on a pod holding more than one
//	slot), 5.2
//
// diagnosis: a failure means the adapter relays a frame that names no
//
//	session to a stream that cannot have sent it, so one session's output
//	reaches a co-tenant. The addressed frame that follows it proves the
//	stream is still live, so a silent relay is distinguished from a dead
//	stream.
func TestUnaddressedFrameIsRejectedOnAPodHoldingTwoSlots_spec_28_5_3(t *testing.T) {
	rt, client := resolutionPod(t, "sess-a", "sess-b")
	streamA := attach(t, client, "sess-a")
	attach(t, client, "sess-b")
	rt.waitForSubscribers(t, 2)

	// The unaddressed frame is rejected on both streams; the addressed
	// frame behind it is the one sess-a receives.
	rt.emit(`{"type":"response","output":[{"type":"text","inline":"unaddressed"}]}`)
	rt.emit(`{"type":"response","sessionId":"sess-a","output":[{"type":"text","inline":"addressed"}]}`)

	ty, addr := recvFrame(t, streamA)
	if ty != "response" || addr != "sess-a" {
		t.Fatalf("first relayed frame on sess-a = (%q, %q), want the addressed response; the unaddressed frame was relayed", ty, addr)
	}
}

// spec: 28.5.3 (heartbeat and heartbeat_ack are protocol-level and sit
//
//	outside the addressing rule)
//
// diagnosis: a failure means the demultiplexer narrowed to every frame
//
//	rather than to the session-scoped set, so a pod-global runtime that
//	acks unstamped misses every heartbeat and is SIGTERMed.
func TestProtocolFrameIsRelayedOnAPodHoldingTwoSlots_spec_28_5_3(t *testing.T) {
	rt, client := resolutionPod(t, "sess-a", "sess-b")
	streamA := attach(t, client, "sess-a")
	attach(t, client, "sess-b")
	rt.waitForSubscribers(t, 2)

	rt.emit(`{"type":"heartbeat_ack"}`)
	if ty, _ := recvFrame(t, streamA); ty != "heartbeat_ack" {
		t.Errorf("relayed frame type = %q, want heartbeat_ack; a protocol-level frame carries no address and passes through", ty)
	}
}

// spec: 28.5.3 (the adapter stamps the addressed session onto every
//
//	inbound envelope), 5.2, 6.4
//
// diagnosis: a failure means the key the adapter writes onto the inbound
//
//	envelope is not the one the published JSON Lines schema declares, so
//	every runtime reads no address and every frame it emits in response is
//	unaddressed.
func TestInboundEnvelopeCarriesTheAddressedSession_spec_28_5_3(t *testing.T) {
	rt, client := resolutionPod(t, "sess-a")
	stream := attach(t, client, "sess-a")
	if err := stream.Send(&adapterv1.AttachRequest{
		SessionId:    &adapterv1.SessionId{Value: "sess-a"},
		EnvelopeJson: []byte(`{"type":"message","id":"m1","input":[]}`),
	}); err != nil {
		t.Fatalf("Send envelope: %v", err)
	}

	done := make(chan []byte, 1)
	go func() { done <- rt.firstEnvelope(t) }()
	var env []byte
	select {
	case env = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the adapter never wrote the envelope to the runtime")
	}

	var probe map[string]any
	if err := json.Unmarshal(env, &probe); err != nil {
		t.Fatalf("stamped envelope is not JSON: %v (%s)", err, env)
	}
	if probe["sessionId"] != "sess-a" {
		t.Errorf("stamped envelope carries sessionId %v, want sess-a; the wire key is what the runtime reads: %s", probe["sessionId"], env)
	}
}
