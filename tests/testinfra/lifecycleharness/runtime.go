// SPDX-License-Identifier: MIT

package lifecycleharness

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"sync/atomic"

	"github.com/lennylabs/lenny/pkg/adapter/linefanout"
)

// maxRuntimeLine bounds one runtime output line.
const maxRuntimeLine = 1 << 20

// Received is one message a harness runtime received.
type Received struct {
	// SessionID is the session the message was addressed to.
	SessionID string
	// Text is the message's first inline text part.
	Text string
	// Seq orders the message among every message any harness runtime
	// received.
	Seq int64
}

// receivedSeq numbers the messages harness runtimes receive.
var receivedSeq atomic.Int64

// RecordingRuntime is the runtime behind a harness pod. It records every
// session start, message, and session end it receives, answers each message
// with a response naming its session, and answers each heartbeat with a
// heartbeat_ack that reports busy: true while a message for the session is
// being handled (§28.5.3). Its output is a fan-out the adapter subscribes
// to, with the adapter's activity tap, so the adapter runs heartbeat
// monitors and seals sessions on it as it does on a production runtime.
//
// Hold makes the runtime keep each later message in hand until Release, so
// a scenario can race an operation against a turn in flight.
type RecordingRuntime struct {
	hub *linefanout.Hub
	w   *io.PipeWriter
	tap atomic.Pointer[func(line []byte)]

	// wmu serializes output writes.
	wmu sync.Mutex

	mu       sync.Mutex
	started  []string
	received []Received
	ended    []string
	frames   []Received
	handling map[string]int
	held     chan struct{}
	// responded counts the responses written per session.
	responded map[string]int
}

// NewRecordingRuntime returns a runtime whose output reader is running.
func NewRecordingRuntime() *RecordingRuntime {
	pr, pw := io.Pipe()
	r := &RecordingRuntime{
		hub: linefanout.New(), w: pw,
		handling: map[string]int{}, responded: map[string]int{},
	}
	r.hub.Tap(func(line []byte) {
		if fn := r.tap.Load(); fn != nil {
			(*fn)(line)
		}
	})
	r.hub.Serve(pr, maxRuntimeLine, nil)
	return r
}

// TapOutput records fn as the adapter's activity tap.
func (r *RecordingRuntime) TapOutput(fn func(line []byte)) { r.tap.Store(&fn) }

// Start records the session start.
func (r *RecordingRuntime) Start(_ context.Context, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started = append(r.started, sessionID)
	return nil
}

// inboundFrame is the part of an inbound frame the runtime reads.
type inboundFrame struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	Input     []struct {
		Inline string `json:"inline"`
	} `json:"input"`
}

// WriteEnvelope handles one inbound frame.
func (r *RecordingRuntime) WriteEnvelope(sessionID string, env []byte) error {
	var f inboundFrame
	if json.Unmarshal(env, &f) != nil {
		return nil
	}
	if f.SessionID == "" {
		f.SessionID = sessionID
	}
	r.mu.Lock()
	r.frames = append(r.frames, Received{SessionID: f.SessionID, Text: f.Type, Seq: receivedSeq.Add(1)})
	r.mu.Unlock()
	switch f.Type {
	case "message":
		r.receive(f)
	case "heartbeat":
		r.ackHeartbeat(f.SessionID)
	case "session_end":
		r.mu.Lock()
		r.ended = append(r.ended, f.SessionID)
		r.mu.Unlock()
	}
	return nil
}

// receive records a message and answers it, after Release when held.
func (r *RecordingRuntime) receive(f inboundFrame) {
	text := ""
	if len(f.Input) > 0 {
		text = f.Input[0].Inline
	}
	r.mu.Lock()
	r.received = append(r.received, Received{SessionID: f.SessionID, Text: text, Seq: receivedSeq.Add(1)})
	r.handling[f.SessionID]++
	held := r.held
	r.mu.Unlock()
	go func() {
		if held != nil {
			<-held
		}
		r.emit(map[string]any{
			"type": "response", "sessionId": f.SessionID,
			"output": []map[string]any{{"type": "text", "inline": "echo:" + text}},
		})
		r.mu.Lock()
		r.handling[f.SessionID]--
		r.responded[f.SessionID]++
		r.mu.Unlock()
	}()
}

// ackHeartbeat answers a heartbeat, reporting busy for a session with a
// message in hand.
func (r *RecordingRuntime) ackHeartbeat(sessionID string) {
	ack := map[string]any{"type": "heartbeat_ack"}
	if sessionID != "" {
		ack["sessionId"] = sessionID
		r.mu.Lock()
		if r.handling[sessionID] > 0 {
			ack["busy"] = true
		}
		r.mu.Unlock()
	}
	r.emit(ack)
}

// emit writes one output frame.
func (r *RecordingRuntime) emit(frame map[string]any) {
	b, err := json.Marshal(frame)
	if err != nil {
		return
	}
	r.wmu.Lock()
	defer r.wmu.Unlock()
	_, _ = r.w.Write(append(b, '\n'))
}

// Output subscribes to the runtime's output.
func (r *RecordingRuntime) Output(ctx context.Context, _ string) (<-chan []byte, error) {
	return r.hub.Subscribe(ctx)
}

// Interrupt does nothing.
func (r *RecordingRuntime) Interrupt(context.Context, string, bool) error { return nil }

// Close does nothing.
func (r *RecordingRuntime) Close(context.Context, string) error { return nil }

// Hold makes the runtime keep every later message in hand until Release.
func (r *RecordingRuntime) Hold() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.held == nil {
		r.held = make(chan struct{})
	}
}

// Release answers every held message and stops holding.
func (r *RecordingRuntime) Release() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.held != nil {
		close(r.held)
		r.held = nil
	}
}

// Handling reports how many messages for the session are in hand.
func (r *RecordingRuntime) Handling(sessionID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.handling[sessionID]
}

// Responded reports how many responses the runtime wrote for the session.
func (r *RecordingRuntime) Responded(sessionID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.responded[sessionID]
}

// Received returns the messages the runtime received for the session, in
// arrival order.
func (r *RecordingRuntime) Received(sessionID string) []Received {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Received
	for _, m := range r.received {
		if m.SessionID == sessionID {
			out = append(out, m)
		}
	}
	return out
}

// Ended returns the sessions the runtime received session_end for.
func (r *RecordingRuntime) Ended() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.ended...)
}

// FrameTypes returns the type of every frame the runtime received for the
// session, in arrival order.
func (r *RecordingRuntime) FrameTypes(sessionID string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, f := range r.frames {
		if f.SessionID == sessionID {
			out = append(out, f.Text)
		}
	}
	return out
}
