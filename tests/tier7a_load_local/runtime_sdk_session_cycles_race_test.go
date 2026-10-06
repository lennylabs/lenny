// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage for the Go runtime SDK serving
// many sessions on one process.
//
// One runtime process serves every session the pod holds, so the SDK
// opens and releases sessions while other sessions' handlers run, and a
// session_end can arrive while that session still has messages queued or
// in flight. The SDK keeps each session's context keyed by sessionId and
// startId and runs a later start's OnCreate only after the earlier
// start's OnTerminate. This case interleaves start, message, and end
// cycles across many sessions under the race detector and checks that no
// session's context overlaps another start of the same session, that no
// reply crosses sessions, and that every creation is released once.
//
// spec: §15.7 (Runtime Author SDKs), §4.7.10 (Runtime process lifetime),
// §28.5.3 (CH-MSGSOCK, Inbound: session_end).
package tier7a_load_local_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/sdks/runtime/go/runtime"
)

// cycleHandler tracks which sessions hold a live context. A session that
// is created while it already holds one, or that receives a message
// while it holds none, is a violation.
type cycleHandler struct {
	mu         sync.Mutex
	live       map[string]bool
	violations []string
	creates    atomic.Int64
	terms      atomic.Int64
}

func (h *cycleHandler) violate(format string, args ...any) {
	h.mu.Lock()
	h.violations = append(h.violations, fmt.Sprintf(format, args...))
	h.mu.Unlock()
}

func (h *cycleHandler) OnCreate(_ context.Context, req runtime.CreateRequest) error {
	h.creates.Add(1)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.live[req.SessionID] {
		h.violations = append(h.violations, "OnCreate for "+req.SessionID+" overlapped a held context")
	}
	h.live[req.SessionID] = true
	return nil
}

func (h *cycleHandler) OnMessage(_ context.Context, m runtime.Message) (runtime.Reply, error) {
	h.mu.Lock()
	held := h.live[m.SessionID]
	h.mu.Unlock()
	if !held {
		h.violate("OnMessage for %s outside its context", m.SessionID)
	}
	time.Sleep(time.Duration(rand.Intn(500)) * time.Microsecond)
	return runtime.TextReply(m.SessionID + "|" + m.Envelope.ID), nil
}

func (h *cycleHandler) OnTerminate(_ context.Context, sessionID string, _ runtime.TerminationReason) error {
	h.terms.Add(1)
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.live[sessionID] {
		h.violations = append(h.violations, "OnTerminate for "+sessionID+" without a held context")
	}
	delete(h.live, sessionID)
	return nil
}

// cycleWire serializes the workers' writes onto the runtime's one stdin
// and hands each response to the worker waiting on its message id.
//
// writeMu and waitMu are separate locks: a write to stdin blocks until the
// runtime reads it, and the runtime may be blocked writing a response the
// reader cannot route while the writer holds the waiter lock.
type cycleWire struct {
	writeMu sync.Mutex
	in      io.Writer
	waitMu  sync.Mutex
	waiters map[string]chan map[string]any

	started   atomic.Int64
	errored   atomic.Int64
	crossed   atomic.Int64
	responses atomic.Int64
}

func (w *cycleWire) send(t *testing.T, line string) {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	if _, err := io.WriteString(w.in, line+"\n"); err != nil {
		t.Errorf("write stdin: %v", err)
	}
}

func (w *cycleWire) await(id string) chan map[string]any {
	ch := make(chan map[string]any, 1)
	w.waitMu.Lock()
	w.waiters[id] = ch
	w.waitMu.Unlock()
	return ch
}

// read consumes the runtime's stdout until it closes.
func (w *cycleWire) read(out io.Reader) {
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		var f map[string]any
		if json.Unmarshal(sc.Bytes(), &f) != nil {
			continue
		}
		switch f["type"] {
		case "session_started":
			w.started.Add(1)
		case "response":
			w.responses.Add(1)
			w.route(f)
		}
	}
}

func (w *cycleWire) route(f map[string]any) {
	if f["error"] != nil {
		w.errored.Add(1)
		return
	}
	out, _ := f["output"].([]any)
	if len(out) == 0 {
		return
	}
	part, _ := out[0].(map[string]any)
	text, _ := part["inline"].(string)
	session, id, _ := strings.Cut(text, "|")
	if session != f["sessionId"] {
		w.crossed.Add(1)
	}
	w.waitMu.Lock()
	ch := w.waiters[id]
	delete(w.waiters, id)
	w.waitMu.Unlock()
	if ch != nil {
		ch <- f
	}
}

// spec: 15.7 (Runtime Author SDKs), 4.7.10 (Runtime process lifetime),
// 28.5.3 (CH-MSGSOCK, Inbound: session_end)
//
// diagnosis: under concurrent start, message, and end cycles on one Go-SDK
//
//	runtime process, a session's context overlapped another start of the
//	same session, a message reached a session outside its context, a reply
//	carried another session's identifier, a message routed before its
//	session_end was answered with a session error, a creation was released
//	other than once, or the race detector fired. The SDK's routing table,
//	its release ordering, or its per-session stdout drop mark lost an
//	update under concurrency.
func TestGoRuntimeSDKSessionCyclesAreRaceFree_spec_15_7(t *testing.T) {
	const (
		workers = 16
		cycles  = 25
	)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	h := &cycleHandler{live: map[string]bool{}}
	wire := &cycleWire{in: inW, waiters: map[string]chan map[string]any{}}

	runErr := make(chan error, 1)
	go func() {
		err := runtime.Run(h, runtime.WithStreams(inR, outW), runtime.WithLogger(nil), runtime.WithSocketTransport(false))
		_ = outW.Close()
		runErr <- err
	}()
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		wire.read(outR)
	}()

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			session := fmt.Sprintf("sess_%02d", w)
			for c := 0; c < cycles; c++ {
				runCycle(t, wire, session, fmt.Sprintf("w%d-c%d", w, c))
			}
		}(w)
	}
	wg.Wait()
	_ = inW.Close()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not return after stdin closed")
	}
	<-readDone

	if len(h.violations) > 0 {
		t.Fatalf("%d context violations, first: %s", len(h.violations), h.violations[0])
	}
	starts := int64(workers * cycles)
	if got := wire.started.Load(); got != starts {
		t.Errorf("session_started frames = %d, want one per session_start (%d)", got, starts)
	}
	if c, term := h.creates.Load(), h.terms.Load(); c != starts || term != starts {
		t.Errorf("OnCreate ran %d and OnTerminate %d times, want %d each", c, term, starts)
	}
	if n := wire.errored.Load(); n != 0 {
		t.Errorf("%d responses carried an error, want none", n)
	}
	if n := wire.crossed.Load(); n != 0 {
		t.Errorf("%d responses named a session other than the one whose handler answered", n)
	}
}

// runCycle opens session, waits for the answer to one message, sends two
// more messages, and ends the session without waiting for them, so the
// session_end lands while messages are queued or in flight.
func runCycle(t *testing.T, wire *cycleWire, session, tag string) {
	first := tag + "-m0"
	wait := wire.await(first)
	wire.send(t, fmt.Sprintf(`{"type":"session_start","sessionId":%q,"startId":%q}`, session, tag))
	wire.send(t, fmt.Sprintf(`{"type":"message","id":%q,"sessionId":%q,"input":[{"type":"text","inline":"x"}]}`, first, session))
	select {
	case <-wait:
	case <-time.After(10 * time.Second):
		t.Errorf("no answer to %s", first)
		return
	}
	for i := 1; i <= 2; i++ {
		wire.send(t, fmt.Sprintf(`{"type":"message","id":"%s-m%d","sessionId":%q,"input":[{"type":"text","inline":"x"}]}`, tag, i, session))
	}
	wire.send(t, fmt.Sprintf(`{"type":"session_end","sessionId":%q}`, session))
}
