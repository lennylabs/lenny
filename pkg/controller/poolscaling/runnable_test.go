// SPDX-License-Identifier: MIT

package poolscaling_test

import (
	"context"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lennylabs/lenny/pkg/controller/poolscaling"
)

// countingSource records each ListPoolConfigs call by a non-blocking
// send, so a test can observe how many sync passes the runnable made
// without ever stalling the loop.
type countingSource struct {
	calls chan struct{}
}

func (c *countingSource) ListPoolConfigs(context.Context) ([]poolscaling.PoolConfig, error) {
	select {
	case c.calls <- struct{}{}:
	default:
	}
	return nil, nil
}

func TestRunnableSyncsOnIntervalUntilCancelled(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()
	src := &countingSource{calls: make(chan struct{}, 8)}
	rn := &poolscaling.Runnable{
		Reconciler: &poolscaling.Reconciler{Client: c, Source: src},
		Interval:   5 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rn.Start(ctx) }()

	// Expect the immediate sync plus at least two ticked syncs.
	for i := 1; i <= 3; i++ {
		select {
		case <-src.calls:
		case <-time.After(2 * time.Second):
			t.Fatalf("runnable did not perform sync #%d", i)
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Start returned %v, want nil on context cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after context cancellation")
	}
}

func TestRunnableNeedsLeaderElection(t *testing.T) {
	rn := &poolscaling.Runnable{}
	if !rn.NeedLeaderElection() {
		t.Error("the pool-scaling sync loop must run only on the elected leader")
	}
}
