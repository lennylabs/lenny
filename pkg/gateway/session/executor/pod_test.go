// SPDX-License-Identifier: MIT

package executor_test

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// respondingRuntime is an adapter.RuntimeProcess that replies to every
// written envelope with a §28.5.3 response frame on its output stream.
type respondingRuntime struct {
	out chan []byte
}

func (r *respondingRuntime) Start(context.Context, string) error { return nil }
func (r *respondingRuntime) WriteEnvelope(string, []byte) error {
	r.out <- []byte(`{"type":"response","text":"ack"}`)
	return nil
}

func (r *respondingRuntime) Output(context.Context, string) (<-chan []byte, error) {
	return r.out, nil
}
func (r *respondingRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (r *respondingRuntime) Close(context.Context, string) error           { return nil }

// dialPodAdapter serves an adapter backed by rt over bufconn, starts a
// session on it, and returns a connected adapterclient.Client.
func dialPodAdapter(t *testing.T, rt adapter.RuntimeProcess) *adapterclient.Client {
	t.Helper()
	srv := adapter.New("podexec-test")
	srv.WorkspaceBase = t.TempDir()
	srv.Runtime = rt
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	if err := cl.StartSession(context.Background(), adapterclient.StartSessionParams{
		SessionID: "sess-pod", Runtime: "echo",
	}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	return cl
}

func TestPodExecutorSendStreamsResponse(t *testing.T) {
	cl := dialPodAdapter(t, &respondingRuntime{out: make(chan []byte, 8)})
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{SessionID: "sess-pod", SandboxName: "sbx-1", Adapter: cl})

	pe := executor.NewPodExecutor(reg, nil)
	out, err := pe.Send(context.Background(), "sess-pod", []executor.Message{
		{Role: "user", Content: "hello"},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(out.Parts) != 1 || out.Parts[0].Type != "text" || out.Parts[0].Text != "ack" {
		t.Errorf("Send output = %+v, want one text part \"ack\"", out)
	}
}

func TestPodExecutorSendReusesTheStreamAcrossMessages(t *testing.T) {
	cl := dialPodAdapter(t, &respondingRuntime{out: make(chan []byte, 8)})
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{SessionID: "sess-pod", Adapter: cl})

	pe := executor.NewPodExecutor(reg, nil)
	for i := 0; i < 3; i++ {
		out, err := pe.Send(context.Background(), "sess-pod", []executor.Message{
			{Role: "user", Content: "msg"},
		})
		if err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
		if len(out.Parts) != 1 || out.Parts[0].Text != "ack" {
			t.Errorf("Send %d output = %+v, want one \"ack\" part", i, out)
		}
	}
}

func TestPodExecutorSendUnboundSession(t *testing.T) {
	pe := executor.NewPodExecutor(podsession.NewRegistry(), nil)
	if _, err := pe.Send(context.Background(), "sess-absent", []executor.Message{{Content: "x"}}); err == nil {
		t.Error("Send for an unbound session succeeded, want a failure")
	}
}

func TestPodExecutorCloseUnboundSession(t *testing.T) {
	pe := executor.NewPodExecutor(podsession.NewRegistry(), nil)
	if err := pe.Close(context.Background(), "sess-absent"); err != nil {
		t.Errorf("Close of an unbound session = %v, want nil", err)
	}
}

// TestPodExecutorEvictStreamReopensOverAFreshBinding pins the co-located
// coordination seam: after EvictStream drops a session's cached Attach stream,
// the next Send must Attach over the currently published binding rather than
// keep serving over the evicted stream. This is what lets a same-replica
// re-adopt (which republishes a fresh BindResult after a dead-connection
// eviction) actually reach the pod: streamFor consults e.streams before the
// registry, so a stale cached stream would otherwise shadow the re-adopt. The
// test would fail against code whose EvictStream did not delete the cached
// stream, because the second Send would reuse the first adapter's stream and
// the second adapter would never receive the envelope.
// spec: 4.7 (single content consumer per session / Attach content stream), 4.6.1 (coordinating replica holds the lease)
func TestPodExecutorEvictStreamReopensOverAFreshBinding(t *testing.T) {
	first := newSlotCapturingAdapter()
	firstCl := dialSlotCapturingAdapter(t, first)
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{SessionID: "sess-pod", Adapter: firstCl})

	pe := executor.NewPodExecutor(reg, nil)
	if _, err := pe.Send(context.Background(), "sess-pod", []executor.Message{
		{Role: "user", Content: "hello"},
	}); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	if _, _, gotEnvelope := first.snapshot(); !gotEnvelope {
		t.Fatal("first adapter never received the initial envelope")
	}

	// Evict the cached stream and republish the binding over a second adapter,
	// mirroring a dead-connection eviction followed by a same-replica re-adopt.
	pe.EvictStream("sess-pod")
	second := newSlotCapturingAdapter()
	secondCl := dialSlotCapturingAdapter(t, second)
	reg.Put(&podsession.BindResult{SessionID: "sess-pod", Adapter: secondCl})

	if _, err := pe.Send(context.Background(), "sess-pod", []executor.Message{
		{Role: "user", Content: "again"},
	}); err != nil {
		t.Fatalf("Send after eviction: %v", err)
	}
	if _, _, gotEnvelope := second.snapshot(); !gotEnvelope {
		t.Error("re-adopt binding never received an envelope; the evicted stream shadowed the fresh binding")
	}
}

// TestPodExecutorEvictStreamOnAnUnstreamedSession confirms EvictStream is a
// safe no-op for a session that never opened an Attach stream, matching the
// teardown Release performs on an unbound session, so a sweep that evicts a
// binding whose stream was never opened does not panic or error.
// spec: 4.7 (single content consumer per session / Attach content stream), 4.6.1 (coordinating replica holds the lease)
func TestPodExecutorEvictStreamOnAnUnstreamedSession(t *testing.T) {
	pe := executor.NewPodExecutor(podsession.NewRegistry(), nil)
	// No panic and no state to drop: the session was never streamed.
	pe.EvictStream("sess-never-streamed")
}

// slotCapturingAdapter is a minimal AdapterServer that records the §6.4
// slotId the gateway's PodExecutor stamped on the Attach binding frame and
// on the message envelope it forwarded over the stream, so a test can
// assert a concurrent-pool bind threads the slotId onto the outbound path
// while an exclusive bind threads none.
type slotCapturingAdapter struct {
	adapterv1.UnimplementedAdapterServer

	mu             sync.Mutex
	attachBindSlot string
	envelopeSlot   string
	gotEnvelope    bool
}

func newSlotCapturingAdapter() *slotCapturingAdapter {
	return &slotCapturingAdapter{}
}

func (a *slotCapturingAdapter) Attach(stream grpc.BidiStreamingServer[adapterv1.AttachRequest, adapterv1.AttachResponse]) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.attachBindSlot = first.GetSessionId().GetValue()
	a.mu.Unlock()
	for {
		req, err := stream.Recv()
		if err != nil {
			return nil //nolint:nilerr // EOF/cancel ends the capture cleanly.
		}
		if env := req.GetEnvelopeJson(); len(env) > 0 {
			var decoded struct {
				SlotID string `json:"slotId"`
			}
			_ = json.Unmarshal(env, &decoded)
			a.mu.Lock()
			a.envelopeSlot = decoded.SlotID
			a.gotEnvelope = true
			a.mu.Unlock()
			// Reply so the executor's Send completes.
			_ = stream.Send(&adapterv1.AttachResponse{
				EnvelopeJson: []byte(`{"type":"response","text":"ack"}`),
			})
		}
	}
}

func (a *slotCapturingAdapter) snapshot() (bindSlot, envelopeSlot string, gotEnvelope bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.attachBindSlot, a.envelopeSlot, a.gotEnvelope
}

// dialSlotCapturingAdapter serves rec over bufconn and returns a connected
// adapterclient.Client.
func dialSlotCapturingAdapter(t *testing.T, rec *slotCapturingAdapter) *adapterclient.Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	adapterv1.RegisterAdapterServer(gs, rec)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	return cl
}

// TestPodExecutorSendAddressesTheAttachStreamBySession pins that the
// executor addresses the Attach stream by the session identifier, which
// under §5.2 names the slot that session holds on the pod, on a binding of
// either concurrency. Before this the stream carried a separate slot
// address that a bind on a pool of one left empty.
// spec: 7.2 (per-session routing), 5.2
func TestPodExecutorSendAddressesTheAttachStreamBySession(t *testing.T) {
	for _, slotID := range []string{"slot_02", ""} {
		rec := newSlotCapturingAdapter()
		cl := dialSlotCapturingAdapter(t, rec)
		reg := podsession.NewRegistry()
		reg.Put(&podsession.BindResult{SessionID: "sess-pod", Adapter: cl, SlotID: slotID})

		pe := executor.NewPodExecutor(reg, nil)
		if _, err := pe.Send(context.Background(), "sess-pod", []executor.Message{
			{Role: "user", Content: "hello"},
		}); err != nil {
			t.Fatalf("Send with recorded slot %q: %v", slotID, err)
		}
		bindSession, _, gotEnvelope := rec.snapshot()
		if !gotEnvelope {
			t.Fatal("adapter never received the forwarded envelope")
		}
		if bindSession != "sess-pod" {
			t.Errorf("Attach binding address = %q, want sess-pod", bindSession)
		}
	}
}
