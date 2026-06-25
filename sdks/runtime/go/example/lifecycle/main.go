// SPDX-License-Identifier: MIT

// Command lifecycle is a Full-level Lenny agent runtime built on the Go
// runtime-author SDK (sdks/runtime/go/runtime). It is the SDK
// counterpart of the no-SDK reference runtime at
// cmd/runtimes/streaming-echo.
//
// The runtime is started with WithFullLevel, so the SDK opens the
// §15.4.3 lifecycle channel and completes the lifecycle_capabilities /
// lifecycle_support handshake in addition to the Standard-level MCP
// setup. The SDK answers the checkpoint, interrupt, credential-rotation,
// and deadline events automatically; the lifecycle callbacks below show
// where a real Full-level runtime quiesces output, reaches a safe
// interrupt point, and rebinds rotated credentials.
//
// The message handler is a plain echo — the Full-level value is in the
// lifecycle channel, not the per-turn behavior.
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

// lifecycleHandler is a Full-level runtime.Handler. The message path is
// a plain echo; the Full-level behavior lives in the lifecycle hooks
// passed to runtime.Run.
type lifecycleHandler struct {
	seq atomic.Uint64
}

// OnCreate has no task-scoped setup.
func (h *lifecycleHandler) OnCreate(context.Context, runtime.CreateRequest) error {
	return nil
}

// OnMessage echoes the inbound parts.
func (h *lifecycleHandler) OnMessage(_ context.Context, msg runtime.Message) (runtime.Reply, error) {
	n := h.seq.Add(1)
	in := msg.Envelope.Input
	out := make([]runtime.MessagePart, 0, len(in))
	for _, p := range in {
		if p.Type == "text" && p.Inline != "" {
			out = append(out, runtime.Text(fmt.Sprintf("[lifecycle seq=%d] %s", n, p.Inline)))
			continue
		}
		out = append(out, p)
	}
	return runtime.Reply{Parts: out, Final: true}, nil
}

// OnTerminate has no teardown.
func (h *lifecycleHandler) OnTerminate(context.Context, runtime.TerminationReason) error {
	return nil
}

func main() {
	err := runtime.Run(
		&lifecycleHandler{},
		runtime.WithFullLevel(),
		runtime.WithLifecycleHandlers(
			// A checkpoint quiesces output before the SDK replies
			// checkpoint_ready. This echo runtime holds no streaming
			// state, so it is quiescent immediately.
			runtime.OnCheckpoint(func(string) error { return nil }),
			// An interrupt brings the runtime to a safe stop point
			// before the SDK replies interrupt_acknowledged.
			runtime.OnInterrupt(func(string) error { return nil }),
			// On rotation the SDK has already re-read the credential
			// file; a real runtime rebinds its upstream client to the
			// refreshed bundle here.
			runtime.OnCredentialsRotated(func(*runtime.CredentialBundle) {}),
		),
	)
	if err == nil {
		os.Exit(exitOK)
	}
	fmt.Fprintln(os.Stderr, err)
	if runtime.ErrIsProtocol(err) {
		os.Exit(exitProtocolError)
	}
	os.Exit(exitRuntimeError)
}
