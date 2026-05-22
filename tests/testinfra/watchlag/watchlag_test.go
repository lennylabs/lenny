// SPDX-License-Identifier: MIT

package watchlag

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/clockstep"
)

func TestPublishHeldUntilAdvance(t *testing.T) {
	c := clockstep.New(time.Now())
	s := New(c, 50*time.Millisecond)
	s.Publish("event-1")

	// Pump before any Advance — nothing should be released.
	s.Pump()
	select {
	case e := <-s.Events():
		t.Fatalf("unexpected early delivery: %+v", e)
	default:
	}

	c.Advance(60 * time.Millisecond)
	s.Pump()
	select {
	case e := <-s.Events():
		if e.Payload != "event-1" {
			t.Errorf("unexpected payload: %v", e.Payload)
		}
	default:
		t.Fatal("expected event after Advance + Pump")
	}
}

func TestSetLagAppliesToSubsequent(t *testing.T) {
	c := clockstep.New(time.Now())
	s := New(c, 10*time.Millisecond)
	s.Publish("fast")

	s.SetLag(100 * time.Millisecond)
	s.Publish("slow")

	c.Advance(20 * time.Millisecond)
	s.Pump()
	got1 := <-s.Events()
	if got1.Payload != "fast" {
		t.Errorf("first event %v want fast", got1.Payload)
	}
	select {
	case e := <-s.Events():
		t.Fatalf("unexpected second early delivery: %+v", e)
	default:
	}
	c.Advance(120 * time.Millisecond)
	s.Pump()
	got2 := <-s.Events()
	if got2.Payload != "slow" {
		t.Errorf("second event %v want slow", got2.Payload)
	}
}
