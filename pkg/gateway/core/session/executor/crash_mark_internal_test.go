// SPDX-License-Identifier: MIT

package executor

import (
	"context"
	"errors"
	"io"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestStreamEndMarksTheBindingCrashed_spec_28_5_1 asserts that endConn marks
// the conn's binding crashed on a DeadlineExceeded or Internal end before the
// stream-failure handler runs, including with no handler wired, and does not
// mark it on io.EOF or on Unavailable, FailedPrecondition, or InvalidArgument.
//
// spec: §28.5.1 (Gateway-to-pod), §6.2 (Pod State Machine)
func TestStreamEndMarksTheBindingCrashed_spec_28_5_1(t *testing.T) {
	for _, tc := range []struct {
		name    string
		end     error
		marked  bool
		handler bool
	}{
		{"deadline exceeded", status.Error(codes.DeadlineExceeded, "heartbeat"), true, true},
		{"internal", status.Error(codes.Internal, "runtime fault"), true, true},
		{"internal with no handler", status.Error(codes.Internal, "runtime fault"), true, false},
		{"eof", io.EOF, false, true},
		{"unavailable", status.Error(codes.Unavailable, "transport"), false, true},
		{"failed precondition", status.Error(codes.FailedPrecondition, "order"), false, true},
		{"invalid argument", status.Error(codes.InvalidArgument, "frame"), false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, a, reg := newScriptedExecutor(t)
			bind, _ := reg.Get("sess-1")
			markedAtHandler := make(chan bool, 1)
			if tc.handler {
				e.SetStreamFailureHandler(func(_, _, _ string, _ error) {
					markedAtHandler <- bind.Crashed()
				})
			}
			r := sendAsync(context.Background(), e, "open")
			ss := a.nextStream(t)
			ss.expectFrame(t)
			ss.pushFrame(t, reply("ok"))
			awaitSend(t, r)
			conn := e.cachedConn("sess-1")
			if errors.Is(tc.end, io.EOF) {
				ss.end <- nil
			} else {
				ss.end <- tc.end
			}
			awaitClosed(t, conn.done, "reader exit on stream end")
			if bind.Crashed() != tc.marked {
				t.Errorf("binding crashed = %v, want %v", bind.Crashed(), tc.marked)
			}
			if tc.marked && tc.handler {
				select {
				case at := <-markedAtHandler:
					if !at {
						t.Error("the handler ran before the binding was marked")
					}
				default:
					t.Error("the stream-failure handler did not run")
				}
			}
		})
	}
}

// TestEvictedStreamLeavesTheBindingUnmarked_spec_28_5_1 asserts that a stream
// the gateway evicted does not mark its binding, so a release that follows an
// eviction recycles the pod as the disposition directs.
//
// spec: §28.5.1 (Gateway-to-pod)
func TestEvictedStreamLeavesTheBindingUnmarked_spec_28_5_1(t *testing.T) {
	e, a, reg := newScriptedExecutor(t)
	bind, _ := reg.Get("sess-1")
	called := false
	e.SetStreamFailureHandler(func(string, string, string, error) { called = true })
	r := sendAsync(context.Background(), e, "open")
	ss := a.nextStream(t)
	ss.expectFrame(t)
	ss.pushFrame(t, reply("ok"))
	awaitSend(t, r)
	conn := e.cachedConn("sess-1")
	e.EvictStream("sess-1")
	awaitClosed(t, conn.done, "reader exit on eviction")
	if bind.Crashed() || called {
		t.Errorf("crashed %v, handler called %v; want neither after an eviction", bind.Crashed(), called)
	}
}
