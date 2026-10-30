// SPDX-License-Identifier: MIT

package dispatch

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInMemReceiveAfterSubmit(t *testing.T) {
	d := NewInMem(time.Second)
	defer d.Close()
	d.Submit(&Job{RunID: "r1", Scenario: "s1"})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	j, err := d.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if j.RunID != "r1" {
		t.Errorf("RunID=%q want r1", j.RunID)
	}
	if d.InFlight() != 1 {
		t.Errorf("InFlight=%d want 1", d.InFlight())
	}
}

func TestInMemAckRemovesInFlight(t *testing.T) {
	d := NewInMem(time.Second)
	defer d.Close()
	d.Submit(&Job{RunID: "r2"})
	j, _ := d.Receive(context.Background())
	if err := d.Ack(context.Background(), j); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if d.InFlight() != 0 {
		t.Errorf("InFlight=%d after Ack, want 0", d.InFlight())
	}
	if err := d.Ack(context.Background(), j); !errors.Is(err, ErrJobNotInFlight) {
		t.Errorf("second Ack: %v want ErrJobNotInFlight", err)
	}
}

func TestInMemNackReturnsToQueue(t *testing.T) {
	d := NewInMem(time.Second)
	defer d.Close()
	d.Submit(&Job{RunID: "r3"})
	j, _ := d.Receive(context.Background())
	if err := d.Nack(context.Background(), j, "test"); err != nil {
		t.Fatalf("Nack: %v", err)
	}
	if d.QueueDepth() != 1 {
		t.Errorf("QueueDepth=%d after Nack, want 1", d.QueueDepth())
	}
	// Second receive must succeed.
	j2, err := d.Receive(context.Background())
	if err != nil {
		t.Fatalf("Receive after Nack: %v", err)
	}
	if j2.RunID != "r3" {
		t.Errorf("RunID=%q want r3", j2.RunID)
	}
}

func TestInMemVisibilityTimeoutReturnsJob(t *testing.T) {
	d := NewInMem(50 * time.Millisecond)
	defer d.Close()
	d.Submit(&Job{RunID: "r4"})
	_, _ = d.Receive(context.Background())
	// Don't Ack; sleep past visibility.
	time.Sleep(80 * time.Millisecond)
	if d.QueueDepth() != 1 {
		t.Errorf("QueueDepth=%d after visibility timeout, want 1", d.QueueDepth())
	}
}

func TestInMemReceiveBlocksUntilDeadline(t *testing.T) {
	d := NewInMem(time.Second)
	defer d.Close()
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := d.Receive(ctx)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed := time.Since(start); elapsed < 30*time.Millisecond {
		t.Errorf("Receive returned too fast: %v", elapsed)
	}
}
