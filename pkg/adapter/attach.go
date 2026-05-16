// SPDX-License-Identifier: MIT

package adapter

import (
	"errors"
	"io"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// Attach opens the §4.7 bidirectional content stream for a session. The
// first AttachClientMessage binds the stream to the pod's session; from
// then on the gateway streams client-to-agent envelopes, which the
// adapter writes to the runtime's stdin, and the adapter streams the
// runtime's output envelopes back. The stream ends when the runtime's
// output closes, the gateway half-closes the client direction, or
// either side errors.
func (s *Server) Attach(stream grpc.BidiStreamingServer[adapterv1.AttachClientMessage, adapterv1.AttachServerMessage]) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	sessionID := first.GetSessionId().GetValue()
	if sessionID == "" {
		return status.Error(codes.InvalidArgument, "Attach requires a session id on the first message")
	}
	if err := s.checkSession(sessionID); err != nil {
		return err
	}
	if env := first.GetEnvelopeJson(); len(env) > 0 {
		if err := s.Runtime.WriteEnvelope(sessionID, env); err != nil {
			return status.Errorf(codes.Internal, "deliver message to runtime: %v", err)
		}
	}

	ctx := stream.Context()
	out, err := s.Runtime.Output(ctx, sessionID)
	if err != nil {
		return status.Errorf(codes.Internal, "open runtime output: %v", err)
	}

	// The receive loop forwards client envelopes to the runtime's
	// stdin; it runs concurrently with the send loop below. recvErr is
	// buffered so the loop's final send never blocks once the send
	// loop has stopped selecting on it.
	recvErr := make(chan error, 1)
	go func() {
		recvErr <- s.attachRecvLoop(stream, sessionID)
	}()

	for {
		select {
		case line, ok := <-out:
			if !ok {
				// The runtime's output ended; the session is done.
				return nil
			}
			if err := stream.Send(&adapterv1.AttachServerMessage{EnvelopeJson: line}); err != nil {
				return err
			}
		case err := <-recvErr:
			if err == nil || errors.Is(err, io.EOF) {
				// The gateway half-closed the client direction; keep
				// streaming runtime output until it ends. A nil channel
				// disables this case for the rest of the stream.
				recvErr = nil
				continue
			}
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// attachRecvLoop forwards each client envelope on the Attach stream to
// the runtime's stdin until the stream ends.
func (s *Server) attachRecvLoop(stream grpc.BidiStreamingServer[adapterv1.AttachClientMessage, adapterv1.AttachServerMessage], sessionID string) error {
	for {
		msg, err := stream.Recv()
		if err != nil {
			return err
		}
		if env := msg.GetEnvelopeJson(); len(env) > 0 {
			if err := s.Runtime.WriteEnvelope(sessionID, env); err != nil {
				return status.Errorf(codes.Internal, "deliver message to runtime: %v", err)
			}
		}
	}
}
