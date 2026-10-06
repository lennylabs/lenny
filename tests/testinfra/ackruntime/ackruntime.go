// SPDX-License-Identifier: MIT

// Package ackruntime provides an in-memory runtime process for adapter
// tests whose runtime has completed the CH-RUNTIMEOPS capability handshake.
// Such a start waits inside its open sequence for the runtime's
// session_started answer to its session_start, and every session-scoped
// CH-RUNTIMEOPS frame waits for that answer, so a test that starts a
// session through the adapter on a Full-level runtime needs a runtime that
// writes it. Runtime records every CH-MSGSOCK frame the adapter writes and
// answers each session_start with the session_started that echoes its
// sessionId and startId.
//
// The type satisfies the adapter's RuntimeProcess method set structurally
// and imports no adapter package, so the adapter's own in-package tests can
// use it without an import cycle.
//
// spec: §28.5.3 (CH-MSGSOCK, Outbound: session_started); §28.5.3
// (CH-RUNTIMEOPS, Messages).
package ackruntime

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/linefanout"
)

// maxFrameBytes bounds one output line.
const maxFrameBytes = 16 * 1024 * 1024

// Reply decides what the runtime writes back for one inbound frame. It
// returns nil to write nothing. The default, AnswerSessionStart, answers
// session_start with session_started and writes nothing for any other frame.
type Reply func(frame []byte) []byte

// Runtime is an in-memory runtime process that answers session_start.
type Runtime struct {
	mu     sync.Mutex
	frames [][]byte
	reply  Reply
	hub    *linefanout.Hub
	src    *io.PipeReader
	// out queues the runtime's replies in write order for the single
	// writer goroutine, so WriteEnvelope never blocks on a reader that has
	// not subscribed yet.
	out chan []byte
	// stopped records that the test ended and out is closed, so a frame
	// the adapter writes from a goroutine that outlives the test is
	// dropped. Guarded by mu.
	stopped bool
}

// New returns a Runtime that answers every session_start, and stops it when
// the test ends.
func New(t testing.TB) *Runtime {
	t.Helper()
	pr, pw := io.Pipe()
	r := &Runtime{
		reply: AnswerSessionStart,
		hub:   linefanout.New(),
		src:   pr,
		out:   make(chan []byte, 1024),
	}
	go r.write(pw)
	t.Cleanup(r.stop)
	return r
}

// SetReply replaces the runtime's reply policy, for a test that withholds
// or fails the acknowledgement.
func (r *Runtime) SetReply(reply Reply) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reply = reply
}

// write is the single writer of the runtime's output.
func (r *Runtime) write(pw *io.PipeWriter) {
	for line := range r.out {
		if _, err := pw.Write(append(line, '\n')); err != nil {
			return
		}
	}
	_ = pw.Close()
}

// stop ends the runtime's output, which ends every subscription.
func (r *Runtime) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.stopped {
		r.stopped = true
		close(r.out)
	}
}

// enqueue queues one output line for the writer, and drops it once the
// runtime has stopped. The queue is deep enough that a send under mu does
// not block the writer, which never takes mu.
func (r *Runtime) enqueue(line []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.stopped {
		r.out <- line
	}
}

// Start makes the runtime live for a session. It does nothing.
func (r *Runtime) Start(context.Context, string) error { return nil }

// WriteEnvelope records one frame and queues the runtime's reply to it.
func (r *Runtime) WriteEnvelope(_ string, envelope []byte) error {
	r.mu.Lock()
	r.frames = append(r.frames, append([]byte(nil), envelope...))
	reply := r.reply
	r.mu.Unlock()
	if out := reply(envelope); out != nil {
		r.enqueue(out)
	}
	return nil
}

// Output subscribes to the runtime's output. A subscription whose context
// ends is removed and consumes nothing afterwards.
func (r *Runtime) Output(ctx context.Context, _ string) (<-chan []byte, error) {
	ch, err := r.hub.Subscribe(ctx)
	if err != nil {
		return nil, err
	}
	r.hub.Serve(r.src, maxFrameBytes, nil)
	return ch, nil
}

// Interrupt does nothing: the runtime process lives as long as the pod.
func (r *Runtime) Interrupt(context.Context, string, bool) error { return nil }

// Close does nothing: the runtime process lives as long as the pod.
func (r *Runtime) Close(context.Context, string) error { return nil }

// Emit writes one frame on the runtime's output, for a test that answers a
// session_start late or out of turn.
func (r *Runtime) Emit(frame []byte) {
	r.enqueue(append([]byte(nil), frame...))
}

// Frames returns a copy of every frame the adapter wrote, in write order.
func (r *Runtime) Frames() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]byte, len(r.frames))
	copy(out, r.frames)
	return out
}

// StartID returns the startId of the n-th session_start the adapter wrote,
// counting from zero, and false when it has written fewer.
func (r *Runtime) StartID(n int) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range r.frames {
		f := frameField(b)
		if f.Type != "session_start" {
			continue
		}
		if n == 0 {
			return f.StartID, true
		}
		n--
	}
	return "", false
}

// FrameTypes returns the type discriminator of each frame the adapter
// wrote, in write order.
func (r *Runtime) FrameTypes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	types := make([]string, 0, len(r.frames))
	for _, f := range r.frames {
		types = append(types, frameField(f).Type)
	}
	return types
}

// frame is the part of a CH-MSGSOCK frame the runtime reads.
type frame struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	StartID   string `json:"startId"`
}

// frameField decodes the routing members of one frame, zero when it does
// not decode.
func frameField(b []byte) frame {
	var f frame
	_ = json.Unmarshal(b, &f)
	return f
}

// AnswerSessionStart is the default reply: a session_start is answered with
// the session_started that echoes its sessionId and startId, and every other
// frame with nothing.
func AnswerSessionStart(in []byte) []byte {
	f := frameField(in)
	if f.Type != "session_start" {
		return nil
	}
	return SessionStarted(f.SessionID, f.StartID, "")
}

// Withhold is a reply that never answers, for a test whose start's wait
// must end without the frame.
func Withhold([]byte) []byte { return nil }

// SessionStarted encodes a session_started frame, carrying an error object
// with code when code is not empty.
func SessionStarted(sessionID, startID, code string) []byte {
	m := map[string]any{"type": "session_started", "sessionId": sessionID, "startId": startID}
	if code != "" {
		m["error"] = map[string]string{"code": code, "message": "context creation failed"}
	}
	b, _ := json.Marshal(m)
	return b
}
