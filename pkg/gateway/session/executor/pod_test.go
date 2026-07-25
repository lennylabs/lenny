// SPDX-License-Identifier: MIT

package executor_test

import (
	"context"
	"encoding/json"
	"errors"
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
// written envelope with a §15.4.1 response frame on its output stream.
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
	srv.WorkspaceRoot = t.TempDir()
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
	a.attachBindSlot = first.GetSlotId().GetValue()
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

// spec: 7.2 (per-slot routing), 15.4.1 (slotId multiplexing)
func TestPodExecutorSendStampsTheSlotIDForAConcurrentPoolBind(t *testing.T) {
	rec := newSlotCapturingAdapter()
	cl := dialSlotCapturingAdapter(t, rec)
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{SessionID: "sess-pod", Adapter: cl, SlotID: "slot_01"})

	pe := executor.NewPodExecutor(reg, nil)
	if _, err := pe.Send(context.Background(), "sess-pod", []executor.Message{
		{Role: "user", Content: "hello"},
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	bindSlot, envelopeSlot, gotEnvelope := rec.snapshot()
	if !gotEnvelope {
		t.Fatal("adapter never received the forwarded envelope")
	}
	if bindSlot != "slot_01" {
		t.Errorf("Attach binding frame carried slotId %q, want slot_01", bindSlot)
	}
	if envelopeSlot != "slot_01" {
		t.Errorf("outbound envelope carried slotId %q, want slot_01", envelopeSlot)
	}
}

// TestPodExecutorSendFailsClosedOnConcurrentBindWithNoSlot pins the §7.2
// SLOT_ID_REQUIRED fail-closed invariant. A bind reporting
// maxConcurrentSessions > 1 with an empty SlotID is a routing bug: per-slot
// dispatch was reached for a concurrent pod but the gateway resolved no slot
// for the session. The executor must fail closed with ErrSlotIDRequired
// rather than open a no-slotId Attach stream that the adapter would route to
// the whole-pod base path and misdeliver into another session's slot.
// spec: 7.2 (per-slot routing, SLOT_ID_REQUIRED), 5.2
func TestPodExecutorSendFailsClosedOnConcurrentBindWithNoSlot(t *testing.T) {
	rec := newSlotCapturingAdapter()
	cl := dialSlotCapturingAdapter(t, rec)
	reg := podsession.NewRegistry()
	// A concurrent-session pool bind that resolved no slot (the routing bug):
	// MaxConcurrentSessions > 1 with an empty SlotID.
	reg.Put(&podsession.BindResult{SessionID: "sess-pod", Adapter: cl, MaxConcurrentSessions: 4})

	pe := executor.NewPodExecutor(reg, nil)
	_, err := pe.Send(context.Background(), "sess-pod", []executor.Message{
		{Role: "user", Content: "hello"},
	})
	if err == nil {
		t.Fatal("Send on a concurrent bind with no resolved slot succeeded, want a fail-closed error")
	}
	if !errors.Is(err, executor.ErrSlotIDRequired) {
		t.Errorf("Send error = %v, want ErrSlotIDRequired", err)
	}
	// The invariant fails closed before any envelope reaches the adapter.
	if _, _, gotEnvelope := rec.snapshot(); gotEnvelope {
		t.Error("an envelope was forwarded to the adapter despite the fail-closed invariant")
	}
}

// TestPodExecutorSendAdmitsAWellRoutedConcurrentBind confirms the
// SLOT_ID_REQUIRED invariant does NOT fire on the normal concurrent path: a
// maxConcurrentSessions > 1 bind that carries a resolved SlotID routes per
// slot and stamps the slotId outbound, exactly as the single-slot stamp test
// asserts, with the per-pod concurrency bound present.
// spec: 7.2 (per-slot routing, SLOT_ID_REQUIRED), 5.2
func TestPodExecutorSendAdmitsAWellRoutedConcurrentBind(t *testing.T) {
	rec := newSlotCapturingAdapter()
	cl := dialSlotCapturingAdapter(t, rec)
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{
		SessionID: "sess-pod", Adapter: cl, SlotID: "slot_02", MaxConcurrentSessions: 4,
	})

	pe := executor.NewPodExecutor(reg, nil)
	if _, err := pe.Send(context.Background(), "sess-pod", []executor.Message{
		{Role: "user", Content: "hello"},
	}); err != nil {
		t.Fatalf("Send on a well-routed concurrent bind: %v", err)
	}
	bindSlot, envelopeSlot, gotEnvelope := rec.snapshot()
	if !gotEnvelope {
		t.Fatal("adapter never received the forwarded envelope")
	}
	if bindSlot != "slot_02" || envelopeSlot != "slot_02" {
		t.Errorf("slotId routing: attach=%q envelope=%q, want slot_02 on both", bindSlot, envelopeSlot)
	}
}

// TestPodExecutorSendAdmitsAnExclusiveBindWithNoSlot confirms the invariant
// leaves the maxConcurrentSessions <= 1 whole-pod path untouched: an
// exclusive bind has no SlotID and no concurrency bound, so it routes
// whole-pod with no slotId rather than triggering SLOT_ID_REQUIRED.
// spec: 7.2 (per-slot routing, SLOT_ID_REQUIRED), 5.2
func TestPodExecutorSendAdmitsAnExclusiveBindWithNoSlot(t *testing.T) {
	rec := newSlotCapturingAdapter()
	cl := dialSlotCapturingAdapter(t, rec)
	reg := podsession.NewRegistry()
	// An exclusive bind: MaxConcurrentSessions is 1 (or 0), SlotID empty.
	reg.Put(&podsession.BindResult{SessionID: "sess-pod", Adapter: cl, MaxConcurrentSessions: 1})

	pe := executor.NewPodExecutor(reg, nil)
	if _, err := pe.Send(context.Background(), "sess-pod", []executor.Message{
		{Role: "user", Content: "hello"},
	}); err != nil {
		t.Fatalf("Send on an exclusive bind: %v", err)
	}
	bindSlot, envelopeSlot, gotEnvelope := rec.snapshot()
	if !gotEnvelope {
		t.Fatal("adapter never received the forwarded envelope")
	}
	if bindSlot != "" || envelopeSlot != "" {
		t.Errorf("exclusive bind stamped a slotId: attach=%q envelope=%q, want empty", bindSlot, envelopeSlot)
	}
}

// spec: 7.2 (per-slot routing), 15.4.1 (slotId multiplexing)
func TestPodExecutorSendStampsNoSlotIDForAnExclusiveBind(t *testing.T) {
	rec := newSlotCapturingAdapter()
	cl := dialSlotCapturingAdapter(t, rec)
	reg := podsession.NewRegistry()
	// An exclusive (maxConcurrentSessions=1) bind leaves SlotID empty.
	reg.Put(&podsession.BindResult{SessionID: "sess-pod", Adapter: cl})

	pe := executor.NewPodExecutor(reg, nil)
	if _, err := pe.Send(context.Background(), "sess-pod", []executor.Message{
		{Role: "user", Content: "hello"},
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	bindSlot, envelopeSlot, gotEnvelope := rec.snapshot()
	if !gotEnvelope {
		t.Fatal("adapter never received the forwarded envelope")
	}
	if bindSlot != "" {
		t.Errorf("Attach binding frame on an exclusive bind carried slotId %q, want empty", bindSlot)
	}
	if envelopeSlot != "" {
		t.Errorf("outbound envelope on an exclusive bind carried slotId %q, want empty", envelopeSlot)
	}
}
