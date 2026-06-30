// SPDX-License-Identifier: MIT

package ratelimit_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit"
)

// spec: §11.1 requests-per-minute rate limiting.

func TestIncrCountsWithinTheSameMinute(t *testing.T) {
	c := ratelimit.NewMemory()
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// Three requests at different seconds of the same minute share one
	// window.
	for i, want := 0, 1; i < 3; i, want = i+1, want+1 {
		n, err := c.Incr(ctx, "user:alice", base.Add(time.Duration(i*15)*time.Second))
		if err != nil {
			t.Fatalf("Incr: %v", err)
		}
		if n != want {
			t.Errorf("Incr #%d: got count %d, want %d", i+1, n, want)
		}
	}
}

func TestIncrResetsWhenTheMinuteAdvances(t *testing.T) {
	c := ratelimit.NewMemory()
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	if _, err := c.Incr(ctx, "k", base); err != nil {
		t.Fatalf("Incr: %v", err)
	}
	if _, err := c.Incr(ctx, "k", base); err != nil {
		t.Fatalf("Incr: %v", err)
	}
	n, err := c.Incr(ctx, "k", base.Add(time.Minute))
	if err != nil {
		t.Fatalf("Incr: %v", err)
	}
	if n != 1 {
		t.Errorf("the count must restart at 1 in a new minute window, got %d", n)
	}
}

func TestIncrIsPerKey(t *testing.T) {
	c := ratelimit.NewMemory()
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	if _, err := c.Incr(ctx, "global", base); err != nil {
		t.Fatalf("Incr: %v", err)
	}
	if _, err := c.Incr(ctx, "global", base); err != nil {
		t.Fatalf("Incr: %v", err)
	}
	n, err := c.Incr(ctx, "user:bob", base)
	if err != nil {
		t.Fatalf("Incr: %v", err)
	}
	if n != 1 {
		t.Errorf("a distinct key has its own window, got count %d, want 1", n)
	}
}
