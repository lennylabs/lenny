// SPDX-License-Identifier: MIT

package runtime

import "sync"

// sessionQueue is the per-session FIFO of message frames the frame loop
// hands to a session's goroutine.
//
// The queue is unbounded: push never blocks, so the frame loop keeps
// reading every inbound frame while one session's handler is busy, and a
// slow session delays only its own messages. The adapter writes a
// session's messages one turn at a time, so the backlog a session holds
// is bounded by what the adapter has sent it rather than by this type.
//
// spec: §15.7 (Handler calls for different sessions run concurrently),
// §28.5.3 (CH-MSGSOCK, interleaved delivery).
type sessionQueue struct {
	mu      sync.Mutex
	cond    *sync.Cond
	items   []*MessageEnvelope
	closed  bool // no further pushes; drain what is queued
	discard bool // drop what is queued; set by session_end
}

func newSessionQueue() *sessionQueue {
	q := &sessionQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// push appends env. It reports false when the queue no longer accepts
// messages, which happens only after the session ended.
func (q *sessionQueue) push(env *MessageEnvelope) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	q.items = append(q.items, env)
	q.cond.Signal()
	return true
}

// next blocks until a message is queued or the queue ends. It returns
// false once the queue was closed and drained, or at once after discard.
func (q *sessionQueue) next() (*MessageEnvelope, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed {
		q.cond.Wait()
	}
	if q.discard || len(q.items) == 0 {
		return nil, false
	}
	env := q.items[0]
	q.items[0] = nil
	q.items = q.items[1:]
	return env, true
}

// close stops further pushes and lets the session drain the messages
// already queued. EOF and shutdown close every live session this way.
func (q *sessionQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.cond.Broadcast()
	q.mu.Unlock()
}

// end stops further pushes and drops every queued message without
// dispatching it. Only session_end ends a queue this way.
func (q *sessionQueue) end() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	dropped := len(q.items)
	q.items = nil
	q.closed = true
	q.discard = true
	q.cond.Broadcast()
	return dropped
}
