// SPDX-License-Identifier: MIT

// Conformance fixture: a Go runtime-author SDK runtime whose handler takes
// longer to answer than the harness takes to send its heartbeat. The SDK
// answers the heartbeat while OnMessage is still running, so the ack must
// report busy: true for the session, which is what the heartbeat_busy
// conformance check requires of an ack written before the response. The
// fixture conforms when the SDK attributes the in-flight handler to its
// session.
//
// spec: §15.4.6 (Conformance Test Suite), §28.5.3 (CH-MSGSOCK)
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/lennylabs/lenny/sdks/runtime/go/runtime"
)

// responseDelay keeps the handler running past the harness's heartbeat.
const responseDelay = 300 * time.Millisecond

type delayedHandler struct{}

func (delayedHandler) OnCreate(context.Context, runtime.CreateRequest) error { return nil }

// OnMessage waits responseDelay, then echoes the inbound parts.
func (delayedHandler) OnMessage(ctx context.Context, msg runtime.Message) (runtime.Reply, error) {
	select {
	case <-time.After(responseDelay):
	case <-ctx.Done():
		return runtime.Reply{}, ctx.Err()
	}
	return runtime.Reply{Parts: msg.Envelope.Input, Final: true}, nil
}

func (delayedHandler) OnTerminate(context.Context, string, runtime.TerminationReason) error {
	return nil
}

func main() {
	err := runtime.Run(delayedHandler{})
	if err == nil {
		os.Exit(0)
	}
	fmt.Fprintln(os.Stderr, err)
	if runtime.ErrIsProtocol(err) {
		os.Exit(2)
	}
	os.Exit(1)
}
