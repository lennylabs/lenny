// SPDX-License-Identifier: MIT

// Package linefanout broadcasts the newline-delimited frames one reader
// produces to every live subscriber. It is the output side every
// RuntimeProcess transport shares: the sidecar socket, the embedded
// in-process loop, and the developer-loop subprocess each read the
// runtime's §28.5.3 JSONL output with one goroutine, and each Output call
// is a subscription to that one reader.
//
// The fan-out matters because the adapter subscribes more than once per
// session. The Attach stream subscribes for the session's lifetime, and a
// start's open sequence subscribes for the span of its wait for the
// runtime's session_started, then cancels. A cancelled subscriber is
// removed and consumes nothing afterwards, so a frame written after the
// wait reaches the Attach subscription rather than a dead reader. A
// subscriber that stops draining blocks only its own pump, never the
// shared reader's delivery to its siblings.
//
// spec: §28.5.3 (CH-MSGSOCK); §28.5.3 (CH-MSGSOCK, Outbound:
// session_started).
package linefanout

import (
	"bufio"
	"context"
	"errors"
	"io"
	"sync"
)

// ErrEnded is returned by Subscribe once the hub's source has ended. A
// transport maps it onto its own ended-state error.
var ErrEnded = errors.New("linefanout: source ended")

// feedDepth is each subscriber's buffered intake. A subscriber whose
// consumer stops draining blocks the reader's send to it only once this
// many frames are queued, and its own cancellation releases the send.
const feedDepth = 64

// initialLineBuffer is the reader's starting line buffer, which grows up to
// the maxLine the transport passes to Serve.
const initialLineBuffer = 64 * 1024

// Hub is one source's subscriber set. The zero value is not usable; call
// New. Hub is safe for concurrent use.
type Hub struct {
	// mu guards subs and ended. It is never held across a send to a
	// subscriber or a close of one.
	mu    sync.Mutex
	subs  map[*subscriber]struct{}
	ended bool
	// serveOnce starts the single reader at the first Serve call.
	serveOnce sync.Once
}

// New returns a hub with no subscribers and no reader.
func New() *Hub {
	return &Hub{subs: map[*subscriber]struct{}{}}
}

// Serve starts the hub's single reader over src at its first call and does
// nothing at a later one. The reader admits a line of up to maxLine bytes,
// broadcasts each line to the subscribers present when the line arrives,
// and when the scan ends runs onEnd, when non-nil, and then ends the hub. onEnd lets a transport
// record its own ended state before any subscriber observes the close, so
// a caller that checks that state and then subscribes either subscribes
// before the end and is closed by it, or fails.
func (h *Hub) Serve(src io.Reader, maxLine int, onEnd func()) {
	h.serveOnce.Do(func() {
		scanner := bufio.NewScanner(src)
		scanner.Buffer(make([]byte, 0, initialLineBuffer), maxLine)
		go h.run(scanner, onEnd)
	})
}

// run is the hub's reader loop.
func (h *Hub) run(scanner *bufio.Scanner, onEnd func()) {
	for scanner.Scan() {
		h.broadcast(append([]byte(nil), scanner.Bytes()...))
	}
	if onEnd != nil {
		onEnd()
	}
	h.end()
}

// Subscribe registers a subscriber and returns its channel, which carries
// every line broadcast after the call and closes when the hub ends or ctx
// ends, whichever is first. A hub end delivers the lines already queued
// for the subscriber before the close. After ctx ends the subscriber is
// removed and the lines still queued for it are dropped, so it consumes
// nothing further. It returns ErrEnded once the hub has ended.
func (h *Hub) Subscribe(ctx context.Context) (<-chan []byte, error) {
	h.mu.Lock()
	if h.ended {
		h.mu.Unlock()
		return nil, ErrEnded
	}
	sub := newSubscriber()
	h.subs[sub] = struct{}{}
	h.mu.Unlock()
	go func() {
		select {
		case <-ctx.Done():
			h.unsubscribe(sub)
		case <-sub.exited:
		}
	}()
	return sub.out, nil
}

// broadcast hands line to every subscriber registered when it is called.
// A send to one subscriber never blocks delivery to another beyond that
// subscriber's buffered intake, and a send to a cancelled subscriber is
// abandoned.
func (h *Hub) broadcast(line []byte) {
	h.mu.Lock()
	subs := make([]*subscriber, 0, len(h.subs))
	for s := range h.subs {
		subs = append(subs, s)
	}
	h.mu.Unlock()
	for _, s := range subs {
		s.send(line)
	}
}

// end marks the hub ended and ends every subscriber still registered,
// each after it delivers the lines already queued for it. A later
// Subscribe returns ErrEnded. The reader calls it once, after its last
// broadcast, so no line is queued after the end.
func (h *Hub) end() {
	h.mu.Lock()
	h.ended = true
	subs := make([]*subscriber, 0, len(h.subs))
	for s := range h.subs {
		subs = append(subs, s)
		delete(h.subs, s)
	}
	h.mu.Unlock()
	for _, s := range subs {
		s.finish()
	}
}

// unsubscribe removes sub and stops its pump.
func (h *Hub) unsubscribe(sub *subscriber) {
	h.mu.Lock()
	delete(h.subs, sub)
	h.mu.Unlock()
	sub.close()
}

// subscriber is one consumer of the hub. The reader hands each line to
// feed, and a dedicated pump drains feed into out, so one slow consumer
// never blocks the reader's delivery to a sibling. done stops the pump at
// once when the consumer's context ends; ended tells the pump that the
// source ended, so it delivers what feed holds and then closes out.
type subscriber struct {
	feed  chan []byte
	out   chan []byte
	done  chan struct{}
	ended chan struct{}
	// exited is closed when the pump returns, so the context watcher
	// Subscribe starts exits with it.
	exited chan struct{}
	// closeOnce and endOnce make each signal a single close, whichever of
	// the hub's end and the context cancellation runs first.
	closeOnce sync.Once
	endOnce   sync.Once
}

// newSubscriber starts a subscriber and its pump.
func newSubscriber() *subscriber {
	s := &subscriber{
		feed:   make(chan []byte, feedDepth),
		out:    make(chan []byte),
		done:   make(chan struct{}),
		ended:  make(chan struct{}),
		exited: make(chan struct{}),
	}
	go s.pump()
	return s
}

// pump drains feed into out until done is closed, or until the source
// ended and feed is empty, then closes out so the consumer observes the
// end of the stream.
func (s *subscriber) pump() {
	defer close(s.exited)
	defer close(s.out)
	for {
		select {
		case line := <-s.feed:
			if !s.deliver(line) {
				return
			}
		case <-s.ended:
			s.drain()
			return
		case <-s.done:
			return
		}
	}
}

// drain delivers every line still queued in feed after the source ended.
func (s *subscriber) drain() {
	for {
		select {
		case line := <-s.feed:
			if !s.deliver(line) {
				return
			}
		default:
			return
		}
	}
}

// deliver hands one line to the consumer and reports false when the
// consumer cancelled first.
func (s *subscriber) deliver(line []byte) bool {
	select {
	case s.out <- line:
		return true
	case <-s.done:
		return false
	}
}

// send queues one line, abandoning it when the subscriber is done.
func (s *subscriber) send(line []byte) {
	select {
	case s.feed <- line:
	case <-s.done:
	}
}

// close stops the pump exactly once, dropping what feed holds.
func (s *subscriber) close() {
	s.closeOnce.Do(func() { close(s.done) })
}

// finish tells the pump the source ended, exactly once.
func (s *subscriber) finish() {
	s.endOnce.Do(func() { close(s.ended) })
}
