// SPDX-License-Identifier: MIT

package adapter_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// spec: §15.4.1 line 1826 — a runtime that does not answer a heartbeat
// within the ack window is hung; the adapter sends SIGTERM (the clean
// Interrupt) and ends the Attach stream with DeadlineExceeded.
func TestAttachHeartbeatHungSendsSIGTERM_spec_15_4_1_1826(t *testing.T) {
	s, rt, _ := sessionServer(t)
	// Unbuffered, never-closed output keeps the runtime "alive" with no
	// frames, so only the heartbeat path drives the stream.
	rt.output = make(chan []byte)
	s.HeartbeatInterval = 15 * time.Millisecond
	s.HeartbeatAckTimeout = 40 * time.Millisecond
	if _, err := s.StartSession(context.Background(), startReq("sess-hb")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	client, _ := adapterClient(t, s)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.Attach(ctx)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := stream.Send(&adapterv1.AttachRequest{SessionId: &adapterv1.SessionId{Value: "sess-hb"}}); err != nil {
		t.Fatalf("Send bind: %v", err)
	}

	// The runtime never acks, so within HeartbeatAckTimeout the adapter
	// declares it hung and the stream ends with DeadlineExceeded.
	_, err = stream.Recv()
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("Recv error = %v (code %v), want DeadlineExceeded", err, status.Code(err))
	}

	// A SIGTERM (clean Interrupt, hard=false) was issued, and at least one
	// heartbeat frame reached the runtime.
	var interrupts []bool
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if interrupts = rt.interruptsSnapshot(); len(interrupts) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(interrupts) == 0 || interrupts[0] != false {
		t.Errorf("interrupts = %v, want a single clean (SIGTERM) interrupt", interrupts)
	}
	if !containsHeartbeat(rt.envelopesSnapshot()) {
		t.Errorf("no heartbeat frame was written to the runtime")
	}
}

// spec: §15.4.1 line 1453 — heartbeat_ack is protocol-level and is never
// relayed to the gateway. A normal frame after it still reaches the
// gateway.
func TestAttachConsumesHeartbeatAck_spec_15_4_1_1453(t *testing.T) {
	s, rt, _ := sessionServer(t)
	rt.output = make(chan []byte, 4)
	// A long interval keeps the monitor from sending its own heartbeat
	// during the test while still activating the ack-interception path.
	s.HeartbeatInterval = 10 * time.Second
	s.HeartbeatAckTimeout = 10 * time.Second
	if _, err := s.StartSession(context.Background(), startReq("sess-ack")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	client, _ := adapterClient(t, s)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.Attach(ctx)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := stream.Send(&adapterv1.AttachRequest{SessionId: &adapterv1.SessionId{Value: "sess-ack"}}); err != nil {
		t.Fatalf("Send bind: %v", err)
	}

	// The ack is consumed by the adapter; the status frame is relayed.
	rt.output <- []byte(`{"type":"heartbeat_ack"}`)
	rt.output <- []byte(`{"type":"status","state":"thinking"}`)

	got, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if ty := jsonType(t, got.GetEnvelopeJson()); ty != "status" {
		t.Errorf("relayed frame type = %q, want status (the heartbeat_ack must be consumed, not relayed)", ty)
	}
}

// spec: §15.4.1 line 1455 — the adapter consumes an outbound
// set_tracing_context frame and registers it with the gateway's
// lenny/set_tracing_context tool (scoped to the bound session); the frame
// is never relayed as content.
func TestAttachForwardsSetTracingContext_spec_15_4_1_1455(t *testing.T) {
	s, rt, _ := sessionServer(t)
	rt.output = make(chan []byte, 4)
	fwd := &fakePlatformForwarder{result: json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`)}
	s.PlatformForwarder = fwd
	if _, err := s.StartSession(context.Background(), startReq("sess-trace")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	client, _ := adapterClient(t, s)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.Attach(ctx)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := stream.Send(&adapterv1.AttachRequest{SessionId: &adapterv1.SessionId{Value: "sess-trace"}}); err != nil {
		t.Fatalf("Send bind: %v", err)
	}

	rt.output <- []byte(`{"type":"set_tracing_context","context":{"langsmith_run_id":"run_abc"}}`)
	rt.output <- []byte(`{"type":"status","state":"thinking"}`)

	got, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if ty := jsonType(t, got.GetEnvelopeJson()); ty != "status" {
		t.Errorf("relayed frame type = %q, want status (set_tracing_context must be consumed)", ty)
	}

	session, tool, args := fwd.lastCall()
	if tool != "lenny/set_tracing_context" {
		t.Fatalf("forwarded tool = %q, want lenny/set_tracing_context", tool)
	}
	if session != "sess-trace" {
		t.Errorf("forwarded session = %q, want sess-trace", session)
	}
	var forwarded struct {
		SessionID string            `json:"sessionId"`
		Context   map[string]string `json:"context"`
	}
	if err := json.Unmarshal(args, &forwarded); err != nil {
		t.Fatalf("forwarded args not JSON: %v (%s)", err, args)
	}
	if forwarded.SessionID != "sess-trace" {
		t.Errorf("forwarded args sessionId = %q, want sess-trace (the adapter injects the bound session)", forwarded.SessionID)
	}
	if forwarded.Context["langsmith_run_id"] != "run_abc" {
		t.Errorf("forwarded context = %v, want langsmith_run_id=run_abc", forwarded.Context)
	}
}

func containsHeartbeat(frames [][]byte) bool {
	for _, f := range frames {
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(f, &probe) == nil && probe.Type == "heartbeat" {
			return true
		}
	}
	return false
}

func jsonType(t *testing.T, frame []byte) string {
	t.Helper()
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(frame, &probe); err != nil {
		t.Fatalf("frame not JSON: %v (%s)", err, frame)
	}
	return probe.Type
}
