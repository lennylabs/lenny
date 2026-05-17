// SPDX-License-Identifier: MIT

package adapter_test

import (
	"context"
	"io"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

func TestAttachRoundTripsEnvelopes(t *testing.T) {
	s, rt, _ := sessionServer(t)
	rt.output = make(chan []byte, 8)
	rt.echoInput = true
	if _, err := s.StartSession(context.Background(), startReq("sess-x")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	client, _ := adapterClient(t, s)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.Attach(ctx)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}

	// The first message binds the session and carries an envelope; the
	// fake runtime echoes each written envelope to its output stream,
	// so a successful Recv proves the input reached WriteEnvelope.
	if err := stream.Send(&adapterv1.AttachClientMessage{
		SessionId:    &adapterv1.SessionId{Value: "sess-x"},
		EnvelopeJson: []byte(`{"type":"message","n":1}`),
	}); err != nil {
		t.Fatalf("Send first: %v", err)
	}
	got, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv first: %v", err)
	}
	if string(got.GetEnvelopeJson()) != `{"type":"message","n":1}` {
		t.Errorf("received %s, want the echoed first envelope", got.GetEnvelopeJson())
	}

	// A subsequent message round-trips the same way.
	if err := stream.Send(&adapterv1.AttachClientMessage{
		SessionId:    &adapterv1.SessionId{Value: "sess-x"},
		EnvelopeJson: []byte(`{"type":"message","n":2}`),
	}); err != nil {
		t.Fatalf("Send second: %v", err)
	}
	got, err = stream.Recv()
	if err != nil {
		t.Fatalf("Recv second: %v", err)
	}
	if string(got.GetEnvelopeJson()) != `{"type":"message","n":2}` {
		t.Errorf("received %s, want the echoed second envelope", got.GetEnvelopeJson())
	}

	// Closing the runtime output ends the stream cleanly.
	close(rt.output)
	if _, err := stream.Recv(); err != io.EOF {
		t.Errorf("Recv after the runtime output closed = %v, want io.EOF", err)
	}
}

func TestAttachStreamsRuntimeOutput(t *testing.T) {
	s, rt, _ := sessionServer(t)
	rt.output = make(chan []byte, 4)
	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	client, _ := adapterClient(t, s)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.Attach(ctx)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	// Bind the session with an envelope-free first message.
	if err := stream.Send(&adapterv1.AttachClientMessage{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	rt.output <- []byte(`{"type":"response"}`)
	got, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if string(got.GetEnvelopeJson()) != `{"type":"response"}` {
		t.Errorf("received %s, want the runtime output frame", got.GetEnvelopeJson())
	}
}

func TestAttachRejectsMissingSessionID(t *testing.T) {
	s, _, _ := sessionServer(t)
	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	client, _ := adapterClient(t, s)

	stream, err := client.Attach(context.Background())
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := stream.Send(&adapterv1.AttachClientMessage{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	_ = stream.CloseSend()
	if _, err := stream.Recv(); status.Code(err) != codes.InvalidArgument {
		t.Errorf("Recv error code = %v, want InvalidArgument for a missing session id", status.Code(err))
	}
}

func TestAttachRejectsUnassignedSession(t *testing.T) {
	s, _, _ := sessionServer(t)
	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	client, _ := adapterClient(t, s)

	stream, err := client.Attach(context.Background())
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := stream.Send(&adapterv1.AttachClientMessage{
		SessionId: &adapterv1.SessionId{Value: "sess-other"},
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	_ = stream.CloseSend()
	if _, err := stream.Recv(); status.Code(err) != codes.NotFound {
		t.Errorf("Recv error code = %v, want NotFound for an unassigned session", status.Code(err))
	}
}
