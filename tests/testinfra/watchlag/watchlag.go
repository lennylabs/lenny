// SPDX-License-Identifier: MIT

package watchlag

import (
	"sync"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/clockstep"
)

// Stream is a lag-controlled event delivery channel. Publish() records
// an event; subscribers receive it Delay later (per the configured
// lag). The driver advances the underlying clockstep.Clock to release
// events.
type Stream struct {
	clock *clockstep.Clock

	mu      sync.Mutex
	delay   time.Duration
	pending []pending
	out     chan Event
}

// Event is one delivered event.
type Event struct {
	At      time.Time
	Payload any
}

type pending struct {
	deliverAt time.Time
	event     Event
}

// New returns a Stream backed by clock with the supplied initial lag.
func New(clock *clockstep.Clock, lag time.Duration) *Stream {
	return &Stream{
		clock: clock,
		delay: lag,
		out:   make(chan Event, 256),
	}
}

// SetLag updates the delay applied to subsequent Publish calls.
func (s *Stream) SetLag(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delay = d
}

// Publish records an event for delivery delay after now.
func (s *Stream) Publish(payload any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	s.pending = append(s.pending, pending{
		deliverAt: now.Add(s.delay),
		event:     Event{At: now.Add(s.delay), Payload: payload},
	})
}

// Pump releases every pending event whose deliverAt has passed and
// pushes it to subscribers. Callers invoke Pump after Clock.Advance.
func (s *Stream) Pump() {
	s.mu.Lock()
	now := s.clock.Now()
	due := []Event{}
	rest := s.pending[:0]
	for _, p := range s.pending {
		if !p.deliverAt.After(now) {
			due = append(due, p.event)
		} else {
			rest = append(rest, p)
		}
	}
	s.pending = rest
	s.mu.Unlock()
	for _, e := range due {
		s.out <- e
	}
}

// Events returns the receive-only channel subscribers consume.
func (s *Stream) Events() <-chan Event { return s.out }
