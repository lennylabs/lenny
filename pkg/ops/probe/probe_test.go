// SPDX-License-Identifier: MIT

package probe_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/probe"
)

func okProbe(context.Context) error   { return nil }
func failProbe(context.Context) error { return errors.New("connection refused") }

func TestRunAllSucceed(t *testing.T) {
	results := probe.Run(context.Background(), map[string]probe.Func{
		"postgres": okProbe,
		"redis":    okProbe,
	}, time.Second)

	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if !probe.AllOK(results) {
		t.Errorf("AllOK = false, want true; results = %+v", results)
	}
	if !results["postgres"].OK || !results["redis"].OK {
		t.Errorf("results = %+v, want both OK", results)
	}
}

func TestRunRecordsFailure(t *testing.T) {
	results := probe.Run(context.Background(), map[string]probe.Func{
		"postgres": okProbe,
		"minio":    failProbe,
	}, time.Second)

	if probe.AllOK(results) {
		t.Error("AllOK = true, want false when a probe fails")
	}
	if results["minio"].OK {
		t.Error("minio probe reported OK, want failure")
	}
	if results["minio"].Detail != "connection refused" {
		t.Errorf("minio detail = %q, want the failure message", results["minio"].Detail)
	}
	if !results["postgres"].OK {
		t.Error("postgres probe should still be OK")
	}
}

func TestRunBoundsAProbeThatIgnoresCancellation(t *testing.T) {
	// A probe that never returns and ignores ctx must still be bounded:
	// Run records a timeout failure rather than hanging.
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	stuck := func(context.Context) error {
		<-block
		return nil
	}

	start := time.Now()
	results := probe.Run(context.Background(), map[string]probe.Func{
		"k8s": stuck,
	}, 80*time.Millisecond)
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Fatalf("Run took %v, want it bounded near the 80ms timeout", elapsed)
	}
	if results["k8s"].OK {
		t.Error("a timed-out probe reported OK, want failure")
	}
	if results["k8s"].Detail != "probe timed out" {
		t.Errorf("k8s detail = %q, want \"probe timed out\"", results["k8s"].Detail)
	}
}

func TestRunHonorsCancellingProbe(t *testing.T) {
	// A probe that honors ctx returns the cancellation error within the
	// timeout and is recorded as a failure.
	results := probe.Run(context.Background(), map[string]probe.Func{
		"redis": func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}, 50*time.Millisecond)

	if results["redis"].OK {
		t.Error("a cancelled probe reported OK, want failure")
	}
}

func TestAllOKEmptyIsTrue(t *testing.T) {
	if !probe.AllOK(map[string]probe.Result{}) {
		t.Error("AllOK of an empty result set = false, want true")
	}
}
