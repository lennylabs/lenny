// SPDX-License-Identifier: MIT

package loadctl

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"
)

// Concurrency coverage for the hub's terminal transition. A run's
// progress events and its terminal ack arrive on separate HTTP
// handlers with no ordering between them, so Hub.Publish and
// Hub.Close run concurrently on the same run id in normal operation.
// The subscriber channels are owned by the run channel's mutex: a
// send and the close of the same channel never overlap, so a Publish
// racing a Close either completes its fan-out first or observes the
// run as terminal and sends nothing. Violating that invariant is a
// data race on the channel and, once the timing lands inside the
// send, a "send on closed channel" panic in library code.
//
// spec: none. `pkg/loadctl` is tier-12 test infrastructure (the
// `lenny-loadctl` control plane described in TESTING.md §12.12)
// rather than a spec behavior, so this case cites the harness
// document that governs it.

const (
	// hubRaceRounds is how many times the whole scenario repeats so
	// the Close lands at varying points of the publish loop.
	hubRaceRounds = 64
	// hubRaceSubscribers is how many subscribers each round attaches
	// before the race starts. Every one of them is a channel the
	// Close closes while the publishers are running.
	hubRaceSubscribers = 4
	// hubRacePublishers is how many goroutines publish into the run
	// concurrently, so the fan-out is contended as well as raced
	// against the Close.
	hubRacePublishers = 2
	// hubRacePreCap bounds the events one publisher emits before the
	// Close lands, so a round whose Close somehow never returns stops
	// rather than spins forever.
	hubRacePreCap = 4096
	// hubRacePostEvents is how many events each publisher emits after
	// the Close has returned. None of them may reach a subscriber
	// that joined before the Close.
	hubRacePostEvents = 32
	// hubRaceJitter bounds the delay before the closing goroutine
	// calls Close, so the terminal transition lands at a different
	// point of the publish loop on each round.
	hubRaceJitter = 300 * time.Microsecond
)

// TestHubPublishRacesCloseOnTerminalRun drives Publish and Close
// concurrently on one run id under the race detector.
//
// Run it with `-race`; without the detector it only catches the
// coarser failure, which is the panic a send into a closed subscriber
// channel raises.
func TestHubPublishRacesCloseOnTerminalRun(t *testing.T) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	for round := range hubRaceRounds {
		runID := fmt.Sprintf("run-%d", round)
		h := NewHub()

		var consumers sync.WaitGroup
		errs := make(chan error, hubRaceSubscribers)
		for i := range hubRaceSubscribers {
			events, _, unsub := h.SubscribeForTest(runID)
			consumers.Add(1)
			go func() {
				defer consumers.Done()
				defer unsub()
				// Drain until the Close closes the channel. An event
				// published after Close returned must never arrive
				// here: the run is terminal for this subscriber.
				for e := range events {
					if e.Type == "post" {
						errs <- fmt.Errorf(
							"round %d subscriber %d: received %q published after Close returned",
							round, i, e.Type)
						return
					}
				}
			}()
		}

		closed := make(chan struct{})
		var producers sync.WaitGroup
		producers.Add(1)
		go func() {
			defer producers.Done()
			time.Sleep(time.Duration(rng.Int63n(int64(hubRaceJitter) + 1)))
			h.Close(runID)
			close(closed)
		}()

		for p := range hubRacePublishers {
			producers.Add(1)
			go func() {
				defer producers.Done()
				for i := 0; i < hubRacePreCap; i++ {
					select {
					case <-closed:
						// The terminal transition is complete. Every
						// event from here on is published into a run
						// no pre-Close subscriber may still see.
						for j := 0; j < hubRacePostEvents; j++ {
							h.Publish(runID, Event{
								Type:    "post",
								Payload: fmt.Sprintf("p%d-%d", p, j),
							})
						}
						return
					default:
					}
					h.Publish(runID, Event{
						Type:    "progress",
						Payload: fmt.Sprintf("p%d-%d", p, i),
					})
				}
			}()
		}

		producers.Wait()
		consumers.Wait()
		close(errs)
		for err := range errs {
			t.Error(err)
		}
		if t.Failed() {
			return
		}
	}
}

// TestHubPublishAfterCloseLeavesSubscriberTerminal is the
// deterministic half of the invariant above: once Close has returned,
// a subscriber that joined before it sees the channel closed and
// receives nothing further, whatever is published afterwards.
func TestHubPublishAfterCloseLeavesSubscriberTerminal(t *testing.T) {
	h := NewHub()
	events, _, unsub := h.SubscribeForTest("r1")
	defer unsub()

	h.Publish("r1", Event{Type: "progress", Payload: "before"})
	h.Close("r1")
	h.Publish("r1", Event{Type: "post", Payload: "after"})

	var got []Event
	for e := range events {
		got = append(got, e)
	}
	if len(got) != 1 || got[0].Type != "progress" {
		t.Fatalf("expected only the pre-Close event, got %+v", got)
	}
}
