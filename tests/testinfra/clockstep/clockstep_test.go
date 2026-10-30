// SPDX-License-Identifier: MIT

package clockstep

import (
	"testing"
	"time"
)

func TestNowAdvances(t *testing.T) {
	origin := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	c := New(origin)
	if got := c.Now(); !got.Equal(origin) {
		t.Errorf("Now=%v want %v", got, origin)
	}
	c.Advance(30 * time.Second)
	if got := c.Now(); !got.Equal(origin.Add(30 * time.Second)) {
		t.Errorf("Now after Advance=%v want %v", got, origin.Add(30*time.Second))
	}
}

func TestAfterFiresOnAdvance(t *testing.T) {
	c := New(time.Now())
	timer := c.After(50 * time.Millisecond)
	if timer.Fired() {
		t.Fatal("Timer fired before Advance")
	}
	c.Advance(60 * time.Millisecond)
	if !timer.Fired() {
		t.Fatal("Timer did not fire after Advance past deadline")
	}
}

func TestAfterDoesNotFireUntilAdvance(t *testing.T) {
	c := New(time.Now())
	timer := c.After(100 * time.Millisecond)
	c.Advance(50 * time.Millisecond)
	if timer.Fired() {
		t.Fatal("Timer fired before deadline")
	}
	c.Advance(60 * time.Millisecond)
	if !timer.Fired() {
		t.Fatal("Timer did not fire after deadline")
	}
}

func TestPendingTimersDecrements(t *testing.T) {
	c := New(time.Now())
	c.After(10 * time.Millisecond)
	c.After(20 * time.Millisecond)
	if got := c.PendingTimers(); got != 2 {
		t.Errorf("PendingTimers=%d want 2", got)
	}
	c.Advance(15 * time.Millisecond)
	if got := c.PendingTimers(); got != 1 {
		t.Errorf("PendingTimers after partial Advance=%d want 1", got)
	}
}
