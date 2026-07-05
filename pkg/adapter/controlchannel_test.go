// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// fakeControlStream is the server side of the §4.7 LifecycleChannel bidi
// stream. It captures envelopes the adapter sends and lets the test drive
// the inbound (gateway→adapter) direction and the stream context.
type fakeControlStream struct {
	grpc.ServerStream
	ctx  context.Context
	sent chan *adapterv1.LifecycleChannelResponse
	recv chan recvResult
}

type recvResult struct {
	req *adapterv1.LifecycleChannelRequest
	err error
}

func newFakeControlStream(ctx context.Context) *fakeControlStream {
	return &fakeControlStream{
		ctx:  ctx,
		sent: make(chan *adapterv1.LifecycleChannelResponse, 16),
		recv: make(chan recvResult),
	}
}

func (f *fakeControlStream) Context() context.Context { return f.ctx }

func (f *fakeControlStream) Send(r *adapterv1.LifecycleChannelResponse) error {
	f.sent <- r
	return nil
}

func (f *fakeControlStream) Recv() (*adapterv1.LifecycleChannelRequest, error) {
	select {
	case r := <-f.recv:
		return r.req, r.err
	case <-f.ctx.Done():
		return nil, f.ctx.Err()
	}
}

// awaitRegistration blocks until the LifecycleChannel handler has attached
// its event sink so emitted events are not dropped.
func awaitRegistration(t *testing.T, s *Server) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.controlMu.Lock()
		ready := s.controlSink != nil
		s.controlMu.Unlock()
		if ready {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("control stream did not register its sink")
}

func recvEvent(t *testing.T, stream *fakeControlStream) controlEvent {
	t.Helper()
	select {
	case resp := <-stream.sent:
		var ev controlEvent
		if err := json.Unmarshal(resp.GetEnvelopeJson(), &ev); err != nil {
			t.Fatalf("decode control event: %v", err)
		}
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("no control event received")
		return controlEvent{}
	}
}

// spec: §4.7 lines 652-662 — every adapter→gateway control event is
// surfaced on the LifecycleChannel stream with its type and fields.
func TestLifecycleChannelEmitsControlEvents_spec_4_7(t *testing.T) {
	s := New("served")
	s.mu.Lock()
	s.sessionID = "sess-1"
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := newFakeControlStream(ctx)
	done := make(chan error, 1)
	go func() { done <- s.LifecycleChannel(stream) }()
	awaitRegistration(t, s)

	s.EmitRateLimited("anthropic")
	if ev := recvEvent(t, stream); ev.Type != eventRateLimited || ev.Provider != "anthropic" || ev.SessionID != "sess-1" {
		t.Errorf("RATE_LIMITED event = %+v", ev)
	}

	s.EmitAuthExpired("openai", "lease-9")
	if ev := recvEvent(t, stream); ev.Type != eventAuthExpired || ev.Provider != "openai" || ev.LeaseID != "lease-9" {
		t.Errorf("AUTH_EXPIRED event = %+v", ev)
	}

	s.EmitProviderUnavailable("vertex")
	if ev := recvEvent(t, stream); ev.Type != eventProviderUnavailable || ev.Provider != "vertex" {
		t.Errorf("PROVIDER_UNAVAILABLE event = %+v", ev)
	}

	s.EmitLeaseRejected("bedrock", "lease-3", "incompatible provider")
	if ev := recvEvent(t, stream); ev.Type != eventLeaseRejected || ev.Reason != "incompatible provider" {
		t.Errorf("LEASE_REJECTED event = %+v", ev)
	}

	s.EmitAdapterTerminating("coordinator_lost")
	if ev := recvEvent(t, stream); ev.Type != eventAdapterTerminating || ev.Reason != "coordinator_lost" {
		t.Errorf("AdapterTerminating event = %+v", ev)
	}

	s.EmitFinalUsageReport(Usage{InputTokens: 10, OutputTokens: 20, WallClockMS: 30})
	ev := recvEvent(t, stream)
	if ev.Type != eventFinalUsageReport || ev.Usage == nil || ev.Usage.InputTokens != 10 || ev.Usage.OutputTokens != 20 || ev.Usage.WallClockMs != 30 {
		t.Errorf("FINAL_USAGE_REPORT event = %+v", ev)
	}

	cancel()
	if err := <-done; status.Code(err) != codes.Canceled && err != context.Canceled {
		t.Errorf("LifecycleChannel returned %v, want context cancellation", err)
	}
}

// spec: §4.7 lines 652-662 — events emitted while no gateway stream is
// attached are recorded on lenny_adapter_control_events_dropped_total so
// the loss is observable.
func TestControlEventDroppedWhenNoStream_spec_4_7(t *testing.T) {
	s := New("served")
	before := testutil.ToFloat64(controlEventsDropped.WithLabelValues(eventRateLimited, "no_stream"))
	s.EmitRateLimited("anthropic")
	after := testutil.ToFloat64(controlEventsDropped.WithLabelValues(eventRateLimited, "no_stream"))
	if after-before != 1 {
		t.Errorf("dropped counter delta = %v, want 1", after-before)
	}
}

// spec: §6.1 one-session-per-pod — a second concurrent control stream is
// rejected so events fan out to a single gateway connection.
func TestLifecycleChannelRejectsSecondStream_spec_4_7(t *testing.T) {
	s := New("served")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := newFakeControlStream(ctx)
	go func() { _ = s.LifecycleChannel(first) }()
	awaitRegistration(t, s)

	second := newFakeControlStream(context.Background())
	if err := s.LifecycleChannel(second); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("second LifecycleChannel = %v, want FailedPrecondition", status.Code(err))
	}
}

// stubUsage is a UsageMeter returning a fixed accounting.
type stubUsage struct {
	u   Usage
	err error
}

func (s stubUsage) Usage(context.Context, string) (Usage, error)      { return s.u, s.err }
func (s stubUsage) Cumulative(context.Context, string) (Usage, error) { return s.u, s.err }

// spec: §4.7 lines 661-662 — Shutdown flushes a FINAL_USAGE_REPORT with
// the session's totals before the stream closes (§8.3 budget return).
func TestEmitFinalUsageOnShutdownPath_spec_4_7(t *testing.T) {
	s := New("served")
	s.Usage = stubUsage{u: Usage{InputTokens: 5, OutputTokens: 7, WallClockMS: 9}}
	s.mu.Lock()
	s.sessionID = "sess-fin"
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := newFakeControlStream(ctx)
	go func() { _ = s.LifecycleChannel(stream) }()
	awaitRegistration(t, s)

	s.emitFinalUsage(context.Background(), "sess-fin")
	ev := recvEvent(t, stream)
	if ev.Type != eventFinalUsageReport || ev.Usage == nil || ev.Usage.InputTokens != 5 || ev.Usage.WallClockMs != 9 {
		t.Errorf("final usage event = %+v", ev)
	}
}

// emitFinalUsage is a no-op when no usage meter is configured (Basic-level
// adapter); it must not panic or emit.
func TestEmitFinalUsageNoMeterIsNoop_spec_4_7(t *testing.T) {
	s := New("served")
	before := testutil.ToFloat64(controlEventsDropped.WithLabelValues(eventFinalUsageReport, "no_stream"))
	s.emitFinalUsage(context.Background(), "sess-x")
	after := testutil.ToFloat64(controlEventsDropped.WithLabelValues(eventFinalUsageReport, "no_stream"))
	if after != before {
		t.Errorf("nil usage meter emitted an event: dropped delta = %v", after-before)
	}
}

// spec: §4.7 lines 661-662 — a gateway frame that closes the stream
// (io.EOF on Recv) ends LifecycleChannel cleanly.
func TestLifecycleChannelClosesOnGatewayEOF_spec_4_7(t *testing.T) {
	s := New("served")
	stream := newFakeControlStream(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.LifecycleChannel(stream) }()
	awaitRegistration(t, s)

	stream.recv <- recvResult{err: io.EOF}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("LifecycleChannel on EOF = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("LifecycleChannel did not return on gateway EOF")
	}
}
