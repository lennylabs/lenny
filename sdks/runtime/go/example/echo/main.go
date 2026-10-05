// SPDX-License-Identifier: MIT

// Command echo is a Basic-level Lenny agent runtime built on the Go
// runtime-author SDK (sdks/runtime/go/runtime). It is the SDK
// counterpart of the no-SDK reference runtime at cmd/runtimes/echo: it
// echoes every inbound message back, prefixing text parts with the
// session's message sequence number, the session's identifier, and the
// session's experiment variant.
//
// The handler implements the three §15.7 Handler methods. The SDK
// drives the §28.5.3 stdin/stdout protocol around it: it opens a session
// on each session_start, invokes OnMessage for the session's messages,
// serializes the returned Reply into a response frame, answers
// heartbeats, and ends the session on session_end. One process serves
// every session the pod holds, so the handler keeps the context each
// session's OnCreate delivered keyed by session. A runtime author
// writing a real agent replaces the body of OnMessage with a model call
// and keeps the rest unchanged.
//
// Exit codes (spec §15.4): 0 success, 1 runtime error, 2 protocol
// error.
package main

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/lennylabs/lenny/sdks/runtime/go/runtime"
)

const (
	exitOK            = 0
	exitRuntimeError  = 1
	exitProtocolError = 2
)

// noVariant is the variant the reply names for a session that is not
// enrolled in an experiment.
const noVariant = "none"

// echoHandler is a Basic-level runtime.Handler. It records each live
// session's experiment variant, from that session's OnCreate, so a reply
// names the context of the session it answers.
type echoHandler struct {
	mu       sync.Mutex
	variants map[string]string
}

// OnCreate records the session's experiment variant.
func (h *echoHandler) OnCreate(_ context.Context, req runtime.CreateRequest) error {
	variant := noVariant
	if req.ExperimentContext != nil && req.ExperimentContext.VariantID != "" {
		variant = req.ExperimentContext.VariantID
	}
	h.mu.Lock()
	h.variants[req.SessionID] = variant
	h.mu.Unlock()
	return nil
}

// OnMessage echoes the inbound parts. Text parts are prefixed with the
// session's message sequence number, identifier, and experiment
// variant; non-text parts are returned unchanged.
func (h *echoHandler) OnMessage(_ context.Context, msg runtime.Message) (runtime.Reply, error) {
	h.mu.Lock()
	variant := h.variants[msg.SessionID]
	h.mu.Unlock()
	in := msg.Envelope.Input
	out := make([]runtime.MessagePart, 0, len(in))
	for _, p := range in {
		if p.Type == "text" && p.Inline != "" {
			out = append(out, runtime.Text(fmt.Sprintf("[echo seq=%d session=%s variant=%s] %s",
				msg.Sequence, msg.SessionID, variant, p.Inline)))
			continue
		}
		out = append(out, p)
	}
	return runtime.Reply{Parts: out, Final: true}, nil
}

// OnTerminate forgets the session's recorded context.
func (h *echoHandler) OnTerminate(_ context.Context, sessionID string, _ runtime.TerminationReason) error {
	h.mu.Lock()
	delete(h.variants, sessionID)
	h.mu.Unlock()
	return nil
}

func main() {
	err := runtime.Run(&echoHandler{variants: map[string]string{}})
	if err == nil {
		os.Exit(exitOK)
	}
	fmt.Fprintln(os.Stderr, err)
	if runtime.ErrIsProtocol(err) {
		os.Exit(exitProtocolError)
	}
	os.Exit(exitRuntimeError)
}
