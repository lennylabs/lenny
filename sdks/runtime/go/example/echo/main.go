// SPDX-License-Identifier: MIT

// Command echo is a Basic-level Lenny agent runtime built on the Go
// runtime-author SDK (sdks/runtime/go/runtime). It is the SDK
// counterpart of the no-SDK reference runtime at cmd/runtimes/echo: it
// echoes every inbound message back, prefixing text parts with a
// per-session sequence number.
//
// The handler implements the three §15.7 Handler methods. The SDK
// drives the §15.4.1 stdin/stdout protocol around it: it reads the
// inbound message frames, invokes OnMessage, serializes the returned
// Reply into a response frame, answers heartbeats, and exits on
// shutdown. A runtime author writing a real agent replaces the body of
// OnMessage with a model call and keeps the rest unchanged.
//
// Exit codes (spec §15.4): 0 success, 1 runtime error, 2 protocol
// error.
package main

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/lennylabs/lenny/sdks/runtime/go/runtime"
)

const (
	exitOK            = 0
	exitRuntimeError  = 1
	exitProtocolError = 2
)

// echoHandler is a Basic-level runtime.Handler. It holds a per-process
// sequence counter so multi-message sessions can be correlated.
type echoHandler struct {
	seq atomic.Uint64
}

// OnCreate has no task-scoped setup for an echo runtime.
func (h *echoHandler) OnCreate(context.Context, runtime.CreateRequest) error {
	return nil
}

// OnMessage echoes the inbound parts. Text parts are prefixed with the
// per-session sequence number; non-text parts are returned unchanged.
func (h *echoHandler) OnMessage(_ context.Context, msg runtime.Message) (runtime.Reply, error) {
	n := h.seq.Add(1)
	in := msg.Envelope.Input
	out := make([]runtime.MessagePart, 0, len(in))
	for _, p := range in {
		if p.Type == "text" && p.Inline != "" {
			out = append(out, runtime.Text(fmt.Sprintf("[echo seq=%d] %s", n, p.Inline)))
			continue
		}
		out = append(out, p)
	}
	return runtime.Reply{Parts: out, Final: true}, nil
}

// OnTerminate has no teardown for an echo runtime.
func (h *echoHandler) OnTerminate(context.Context, runtime.TerminationReason) error {
	return nil
}

func main() {
	err := runtime.Run(&echoHandler{})
	if err == nil {
		os.Exit(exitOK)
	}
	fmt.Fprintln(os.Stderr, err)
	if runtime.ErrIsProtocol(err) {
		os.Exit(exitProtocolError)
	}
	os.Exit(exitRuntimeError)
}
