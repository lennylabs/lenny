// SPDX-License-Identifier: MIT

package leaderjobs_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/core/leaderjobs"
)

// spec: §12.5 (Artifact Store)
func TestSetRunsEveryJobUntilTheLeadershipContextEnds(t *testing.T) {
	var s leaderjobs.Set
	var started, stopped atomic.Int32
	for _, name := range []string{"a", "b"} {
		s.Add(name, func(ctx context.Context) {
			started.Add(1)
			<-ctx.Done()
			stopped.Add(1)
		})
	}
	s.Add("nil", nil)
	if s.Len() != 2 {
		t.Fatalf("Len() = %d, want 2 (a nil job registers nothing)", s.Len())
	}
	if got := s.Names(); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("Names() = %q, want [a b]", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Run(ctx)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for started.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("started %d jobs, want 2", started.Load())
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-done:
		t.Fatal("Run returned while the leadership context was live")
	default:
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the leadership context ended")
	}
	if stopped.Load() != 2 {
		t.Fatalf("stopped %d jobs, want 2", stopped.Load())
	}
}

// spec: §12.5 (Artifact Store)
func TestSetRunsAgainOnAFreshLeadership(t *testing.T) {
	var s leaderjobs.Set
	var runs atomic.Int32
	s.Add("probe", func(context.Context) { runs.Add(1) })
	for range 2 {
		ctx, cancel := context.WithCancel(context.Background())
		s.Run(ctx)
		cancel()
	}
	if runs.Load() != 2 {
		t.Fatalf("job ran %d times over two leaderships, want 2", runs.Load())
	}
}

func TestEmptySetRunReturnsAtOnce(t *testing.T) {
	var s leaderjobs.Set
	s.Run(context.Background())
}
