// SPDX-License-Identifier: MIT

package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"
)

// liveSDK drives an in-process Run over pipes, so a test can write
// CH-MSGSOCK frames one at a time and read each frame the SDK writes as
// it is written.
type liveSDK struct {
	t      *testing.T
	in     *io.PipeWriter
	frames chan map[string]any
	// done is closed when Run returns; err holds Run's error then.
	done chan struct{}
	err  error
}

// startLiveSDK runs h under Run with the given extra options.
func startLiveSDK(t *testing.T, h Handler, opts ...Option) *liveSDK {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	l := &liveSDK{t: t, in: inW, frames: make(chan map[string]any, 256), done: make(chan struct{})}
	all := append([]Option{WithStreams(inR, outW), WithLogger(nil), WithSocketTransport(false)}, opts...)
	go func() {
		l.err = Run(h, all...)
		_ = outW.Close()
		close(l.done)
	}()
	go func() {
		defer close(l.frames)
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 64*1024), maxFrameBytes)
		for sc.Scan() {
			var m map[string]any
			if err := json.Unmarshal(sc.Bytes(), &m); err == nil {
				l.frames <- m
			}
		}
	}()
	t.Cleanup(func() {
		_ = inW.Close()
		select {
		case <-l.done:
		case <-time.After(5 * time.Second):
		}
	})
	return l
}

// send writes one frame line to the runtime's stdin.
func (l *liveSDK) send(line string) {
	l.t.Helper()
	if _, err := io.WriteString(l.in, line+"\n"); err != nil {
		l.t.Fatalf("write stdin: %v", err)
	}
}

// next returns the next frame the runtime wrote, failing after d.
func (l *liveSDK) next(d time.Duration) map[string]any {
	l.t.Helper()
	select {
	case f, ok := <-l.frames:
		if !ok {
			l.t.Fatal("the runtime closed stdout")
		}
		return f
	case <-time.After(d):
		l.t.Fatalf("no frame from the runtime within %s", d)
		return nil
	}
}

// heartbeatBarrier writes a heartbeat and returns every frame written
// before its heartbeat_ack. The frame loop answers heartbeats inline, so
// the barrier also shows the loop is not blocked.
func (l *liveSDK) heartbeatBarrier() []map[string]any {
	l.t.Helper()
	l.send(`{"type":"heartbeat","ts":1}`)
	var before []map[string]any
	for {
		f := l.next(3 * time.Second)
		if f["type"] == "heartbeat_ack" {
			return before
		}
		before = append(before, f)
	}
}

// closeAndWait closes stdin and returns Run's error and every frame the
// runtime wrote after the close.
func (l *liveSDK) closeAndWait() ([]map[string]any, error) {
	l.t.Helper()
	_ = l.in.Close()
	var rest []map[string]any
	for f := range l.frames {
		rest = append(rest, f)
	}
	select {
	case <-l.done:
		return rest, l.err
	case <-time.After(5 * time.Second):
		l.t.Fatal("Run did not return after stdin closed")
		return nil, nil
	}
}

// startFrame is a session_start frame for sessionID with startID and no
// optional members.
func startFrame(sessionID, startID string) string {
	return fmt.Sprintf(`{"type":"session_start","sessionId":%q,"startId":%q}`, sessionID, startID)
}

// endFrame is a session_end frame for sessionID.
func endFrame(sessionID string) string {
	return fmt.Sprintf(`{"type":"session_end","sessionId":%q}`, sessionID)
}

// msgFrame is a message frame for sessionID carrying one text part.
func msgFrame(sessionID, id, text string) string {
	return fmt.Sprintf(`{"type":"message","id":%q,"sessionId":%q,"input":[{"type":"text","inline":%q}]}`, id, sessionID, text)
}

// errorCode returns the error.code of a frame, or "".
func errorCode(f map[string]any) string {
	e, _ := f["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

// firstOutput returns output[0].inline of a response frame.
func firstOutput(f map[string]any) string {
	out, _ := f["output"].([]any)
	if len(out) == 0 {
		return ""
	}
	p, _ := out[0].(map[string]any)
	s, _ := p["inline"].(string)
	return s
}

// recorder is a Handler whose behavior each test configures. It records
// every call in order, and its zero value echoes every message.
type recorder struct {
	mu      sync.Mutex
	events  []string
	creates []CreateRequest
	terms   map[string][]TerminationReason

	// createGate, when set, is called inside OnCreate and its error is
	// returned.
	createGate func(ctx context.Context, req CreateRequest) error
	// messageGate, when set, is called inside OnMessage before the echo.
	messageGate func(ctx context.Context, m Message)
}

func (r *recorder) log(e string) {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
}

func (r *recorder) OnCreate(ctx context.Context, req CreateRequest) error {
	r.mu.Lock()
	r.creates = append(r.creates, req)
	r.mu.Unlock()
	r.log("create:" + req.SessionID)
	if r.createGate != nil {
		return r.createGate(ctx, req)
	}
	return nil
}

func (r *recorder) OnMessage(ctx context.Context, m Message) (Reply, error) {
	r.log("message:" + m.SessionID + ":" + m.Envelope.ID)
	if r.messageGate != nil {
		r.messageGate(ctx, m)
	}
	return Reply{Parts: m.Envelope.Input, Final: true}, nil
}

func (r *recorder) OnTerminate(_ context.Context, sessionID string, reason TerminationReason) error {
	r.mu.Lock()
	if r.terms == nil {
		r.terms = map[string][]TerminationReason{}
	}
	r.terms[sessionID] = append(r.terms[sessionID], reason)
	r.mu.Unlock()
	r.log("terminate:" + sessionID)
	return nil
}

func (r *recorder) eventLog() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

func (r *recorder) terminations(sessionID string) []TerminationReason {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]TerminationReason(nil), r.terms[sessionID]...)
}

func (r *recorder) createRequests() []CreateRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]CreateRequest(nil), r.creates...)
}

// indexOf returns the position of e in the event log, or -1.
func indexOf(events []string, e string) int {
	for i, v := range events {
		if v == e {
			return i
		}
	}
	return -1
}

// waitUntil polls cond until it holds or d elapses.
func waitUntil(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return cond()
}
