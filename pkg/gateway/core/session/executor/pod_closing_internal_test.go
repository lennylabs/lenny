// SPDX-License-Identifier: MIT

package executor

import (
	"context"
	"errors"
	"math/rand/v2"
	"runtime"
	"strings"
	"testing"
	"time"
)

// turnWaiters counts the goroutines blocked waiting for an Attach stream's
// turn token, so a test can tell that a Send has passed the closing gate in
// streamFor and is parked on the token.
func turnWaiters() int {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return strings.Count(string(buf[:n]), "(*attachConn).acquireTurn(")
		}
		buf = make([]byte, 2*len(buf))
	}
}

// awaitTurnWaiters blocks until want goroutines wait for a turn token.
func awaitTurnWaiters(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(streamTestTimeout)
	for turnWaiters() != want {
		if time.Now().After(deadline) {
			t.Fatalf("turn-token waiters = %d, want %d", turnWaiters(), want)
		}
		time.Sleep(time.Millisecond)
	}
}

// sendBatchAsync sends messages on sess-1 from a goroutine.
func sendBatchAsync(e *PodExecutor, contents ...string) <-chan batchResult {
	msgs := make([]Message, 0, len(contents))
	for _, c := range contents {
		msgs = append(msgs, Message{Role: "user", Content: c})
	}
	ch := make(chan batchResult, 1)
	go func() {
		out, err := e.Send(context.Background(), "sess-1", msgs)
		ch <- batchResult{out: out, err: err}
	}()
	return ch
}

// batchResult is a batch Send's outcome.
type batchResult struct {
	out Response
	err error
}

func awaitBatch(t *testing.T, ch <-chan batchResult) batchResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(streamTestTimeout):
		t.Fatal("Send did not return")
		return batchResult{}
	}
}

// assertSealedReroute fails unless err is a *SealedError that delivered
// wantDelivered messages whose response text is wantText, and unwraps to
// ErrSessionSealed.
func assertSealedReroute(t *testing.T, err error, wantDelivered int, wantText []string) {
	t.Helper()
	var sealed *SealedError
	if !errors.As(err, &sealed) {
		t.Fatalf("Send stopped by the seal eviction = %v, want *SealedError", err)
	}
	if !errors.Is(err, ErrSessionSealed) {
		t.Fatalf("sealed reroute %v does not unwrap to ErrSessionSealed", err)
	}
	if sealed.Delivered != wantDelivered {
		t.Fatalf("SealedError.Delivered = %d, want %d", sealed.Delivered, wantDelivered)
	}
	if len(sealed.Response.Parts) != len(wantText) {
		t.Fatalf("SealedError response = %+v, want parts %v", sealed.Response, wantText)
	}
	for i, p := range sealed.Response.Parts {
		if p.Text != wantText[i] {
			t.Fatalf("SealedError response part %d = %q, want %q", i, p.Text, wantText[i])
		}
	}
}

// spec: 7.1 (Delivery and checkpoints during the seal), 28.5.1 (Gateway-to-pod)
// A Send that obtained the held stream before the seal began, and waits for
// the turn token behind a turn already in flight, writes nothing once the
// seal's eviction ends the stream. It returns a *SealedError with nothing
// delivered, so its caller routes the message by the session's terminal
// state. Before the fix it returned the wrapped stream-end error, and every
// caller took its executor-failure branch for a message the runtime never
// received. The turn already in flight wrote its message, so it keeps the
// stream-end error.
func TestSendPastTheClosingGateIsReroutedWhenTheSealEvictsItsStream_spec_7_1(t *testing.T) {
	e, a, reg := newScriptedExecutor(t)
	base := turnWaiters()
	inFlight := sendAsync(context.Background(), e, "in flight")
	ss := a.nextStream(t)
	ss.expectFrame(t)

	waiting := sendAsync(context.Background(), e, "waiting")
	awaitTurnWaiters(t, base+1)

	bind, _ := reg.Get("sess-1")
	bind.MarkClosing()
	e.EvictStream("sess-1")

	r := awaitSend(t, waiting)
	var sealed *SealedError
	if !errors.As(r.err, &sealed) || sealed.Delivered != 0 || !errors.Is(r.err, ErrSessionSealed) {
		t.Fatalf("Send waiting for the turn when the seal evicted its stream = %v, want *SealedError with nothing delivered", r.err)
	}
	if r := awaitSend(t, inFlight); r.err == nil || errors.Is(r.err, ErrSessionSealed) {
		t.Fatalf("written turn ended by the seal eviction = %v, want the stream-end error", r.err)
	}
	select {
	case f := <-ss.recv:
		t.Fatalf("a Send stopped by the seal wrote %s", f)
	default:
	}
}

// spec: 7.1 (Delivery and checkpoints during the seal), 28.5.1 (Gateway-to-pod)
// A batch whose first message was delivered before the seal began, and whose
// second message waits for the turn token when the seal's eviction ends the
// stream, returns a *SealedError carrying the one delivered message and its
// response, so the caller routes only the undelivered rest. A third Send
// queued ahead of the batch's second message holds the turn when the seal
// begins, which parks the batch on the token deterministically.
func TestBatchSendMidwayIsReroutedWithItsDeliveredCountWhenTheSealEvictsItsStream_spec_7_1(t *testing.T) {
	e, a, reg := newScriptedExecutor(t)
	base := turnWaiters()
	first := sendAsync(context.Background(), e, "first")
	ss := a.nextStream(t)
	ss.expectFrame(t)

	batch := sendBatchAsync(e, "b1", "b2")
	awaitTurnWaiters(t, base+1)
	queued := sendAsync(context.Background(), e, "queued")
	awaitTurnWaiters(t, base+2)

	// The token is handed to waiters in arrival order: the batch's first
	// message, then the queued Send, then the batch's second message.
	ss.pushFrame(t, reply("one"))
	if r := awaitSend(t, first); r.err != nil {
		t.Fatalf("first Send: %v", r.err)
	}
	ss.expectFrame(t)
	ss.pushFrame(t, reply("b1-reply"))
	ss.expectFrame(t)
	awaitTurnWaiters(t, base+1)

	bind, _ := reg.Get("sess-1")
	bind.MarkClosing()
	e.EvictStream("sess-1")

	assertSealedReroute(t, awaitBatch(t, batch).err, 1, []string{"b1-reply"})
	if r := awaitSend(t, queued); r.err == nil || errors.Is(r.err, ErrSessionSealed) {
		t.Fatalf("written turn ended by the seal eviction = %v, want the stream-end error", r.err)
	}
	select {
	case f := <-ss.recv:
		t.Fatalf("the batch wrote its second message %s after the seal began", f)
	default:
	}
}

// spec: 7.1 (Delivery and checkpoints during the seal), 28.5.1 (Gateway-to-pod)
// A write that fails because the stream has ended carries the not-written
// mark. It is a seal stop only when the gateway's eviction ended the stream
// and the binding is marked closing; a failed write on a stream that ended
// for any other reason, or before the closing mark, keeps its stream-end
// error.
func TestFailedWriteIsASealStopOnlyUnderTheClosingEviction_spec_7_1(t *testing.T) {
	for _, tc := range []struct {
		name     string
		evicted  bool
		closing  bool
		wantStop bool
	}{
		{"evicted and closing", true, true, true},
		{"evicted without the closing mark", true, false, false},
		{"closing without the eviction", false, true, false},
		{"neither", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := finishedStreamConn(t)
			c.closedByGateway.Store(tc.evicted)
			if tc.closing {
				c.bind.MarkClosing()
			}
			close(c.done)
			ctx, cancel := context.WithTimeout(context.Background(), streamTestTimeout)
			defer cancel()
			_, err := c.runTurn(ctx, []byte(`{"type":"message"}`))
			if !errors.Is(err, errNotWritten) {
				t.Fatalf("failed write = %v, want the not-written mark", err)
			}
			if got := c.stoppedByClosing(err); got != tc.wantStop {
				t.Fatalf("stoppedByClosing = %v, want %v", got, tc.wantStop)
			}
		})
	}
}

// spec: 7.1 (Delivery and checkpoints during the seal), 28.5.1 (Gateway-to-pod)
// A turn whose message was written and whose stream the seal's eviction then
// ends is never a seal stop, because the runtime may have received the
// message: its error does not carry the not-written mark.
func TestWrittenTurnEndedByTheSealEvictionIsNotASealStop_spec_7_1(t *testing.T) {
	e, a, reg := newScriptedExecutor(t)
	ch := sendAsync(context.Background(), e, "written")
	ss := a.nextStream(t)
	ss.expectFrame(t)
	conn := e.cachedConn("sess-1")
	bind, _ := reg.Get("sess-1")
	bind.MarkClosing()
	e.EvictStream("sess-1")
	r := awaitSend(t, ch)
	if r.err == nil || errors.Is(r.err, errNotWritten) || conn.stoppedByClosing(r.err) {
		t.Fatalf("written turn ended by the seal eviction = %v, want a stream-end error without the not-written mark", r.err)
	}
}

// spec: 7.1 (Delivery and checkpoints during the seal), 28.5.1 (Gateway-to-pod)
// Randomized interleaving of a two-message batch against the seal start. The
// adapter answers every message it receives; the seal marks the binding
// closing and evicts the stream after a random delay. Every outcome must
// satisfy the routing invariant: a batch that completes delivered both
// messages; a *SealedError counts exactly the messages the adapter received
// and answered; and any other error belongs to a message the adapter
// received, which the caller must not route again as undelivered.
func TestBatchSendAgainstTheSealStartKeepsTheRoutingInvariant_spec_7_1(t *testing.T) {
	seed := uint64(time.Now().UnixNano())
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	for i := 0; i < 40; i++ {
		e, a, reg := newScriptedExecutor(t)
		batch := sendBatchAsync(e, "b1", "b2")
		ss := a.nextStream(t)
		received := make(chan int, 1)
		stopAnswer := make(chan struct{})
		go func() {
			n := 0
			defer func() { received <- n }()
			for {
				select {
				case <-ss.recv:
					n++
					select {
					case ss.push <- []byte(reply("r")):
					case <-ss.ctxDone:
						return
					case <-stopAnswer:
						return
					}
				case <-ss.ctxDone:
					return
				case <-stopAnswer:
					return
				}
			}
		}()
		time.Sleep(time.Duration(rng.IntN(400)) * time.Microsecond)
		bind, _ := reg.Get("sess-1")
		bind.MarkClosing()
		e.EvictStream("sess-1")
		r := awaitBatch(t, batch)
		close(stopAnswer)
		n := <-received

		var sealed *SealedError
		switch {
		case r.err == nil:
			if n != 2 || len(r.out.Parts) != 2 {
				t.Fatalf("iteration %d: completed batch with %d received and %d parts, want 2 and 2", i, n, len(r.out.Parts))
			}
		case errors.As(r.err, &sealed):
			if sealed.Delivered != n || len(sealed.Response.Parts) != n {
				t.Fatalf("iteration %d: SealedError delivered %d with %d parts, adapter received %d", i, sealed.Delivered, len(sealed.Response.Parts), n)
			}
		default:
			if errors.Is(r.err, errNotWritten) {
				t.Fatalf("iteration %d: a seal-stopped message surfaced as %v rather than the sealed reroute", i, r.err)
			}
		}
	}
}
