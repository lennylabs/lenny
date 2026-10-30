// SPDX-License-Identifier: MIT

package loadgen

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Run drives s through the profile p and returns the aggregated Result.
//
// The driver guarantees:
//
//  1. Setup runs once before any Run; Teardown runs once after the last
//     Run completes (or after ctx is cancelled, whichever is first).
//  2. Per-iteration latency is observed regardless of error outcome.
//  3. Errors are recorded but do not stop the run (the SLO assertion
//     in Scenario.Assert decides pass/fail).
//  4. ctx cancellation is honoured promptly; in-flight iterations are
//     allowed to finish but no new iterations are dispatched.
func Run(ctx context.Context, s Scenario, p Profile) (*Result, error) {
	if s == nil {
		return nil, errors.New("loadgen: Run requires a non-nil Scenario")
	}
	if err := validateProfile(p); err != nil {
		return nil, fmt.Errorf("loadgen: profile invalid: %w", err)
	}

	if err := s.Setup(ctx); err != nil {
		return nil, fmt.Errorf("loadgen: scenario %s Setup: %w", s.Name(), err)
	}
	defer func() {
		// Best-effort teardown; ignore errors here so they do not
		// shadow Run() failures. Scenarios should log their own
		// teardown problems if surfacing matters.
		_ = s.Teardown(context.Background())
	}()

	result := newResult(s.Name(), p)
	result.StartedAt = time.Now()
	hist := NewHistogram()
	var iters counter
	var errs counter

	runCtx, cancel := context.WithTimeout(ctx, p.Duration)
	defer cancel()

	switch p.Kind {
	case ConstantVU:
		runConstantVU(runCtx, s, p, hist, &iters, &errs, result)
	case ConstantArrivalRate:
		runConstantArrivalRate(runCtx, s, p, hist, &iters, &errs, result)
	case RampingVU:
		runRampingVU(runCtx, s, p, hist, &iters, &errs, result)
	}

	result.Duration = time.Since(result.StartedAt)
	result.Iterations = iters.val()
	result.Errors = errs.val()
	result.Latency = hist.Snapshot()
	if result.Duration.Seconds() > 0 {
		result.Throughput = float64(result.Iterations) / result.Duration.Seconds()
	}
	if result.Iterations > 0 {
		result.ErrorRate = float64(result.Errors) / float64(result.Iterations)
	}
	return result, nil
}

func validateProfile(p Profile) error {
	if p.Duration <= 0 {
		return errors.New("Duration must be positive")
	}
	switch p.Kind {
	case ConstantVU:
		if p.VUs <= 0 {
			return errors.New("ConstantVU requires VUs > 0")
		}
	case ConstantArrivalRate:
		if p.Rate <= 0 {
			return errors.New("ConstantArrivalRate requires Rate > 0")
		}
		if p.VUs <= 0 {
			return errors.New("ConstantArrivalRate requires VUs > 0 (worker pool size)")
		}
	case RampingVU:
		if len(p.RampStages) == 0 {
			return errors.New("RampingVU requires at least one RampStage")
		}
	}
	return nil
}

// runConstantVU dispatches N goroutines that loop calling s.Run until
// the context is done.
func runConstantVU(ctx context.Context, s Scenario, p Profile, hist *Histogram, iters, errs *counter, r *Result) {
	var wg sync.WaitGroup
	for vu := 1; vu <= p.VUs; vu++ {
		vu := vu
		wg.Add(1)
		go func() {
			defer wg.Done()
			workerLoop(ctx, s, vu, hist, iters, errs, r)
		}()
	}
	wg.Wait()
}

// runConstantArrivalRate uses a fixed worker pool (VUs) drained by a
// ticker that emits at Rate iterations per second.
func runConstantArrivalRate(ctx context.Context, s Scenario, p Profile, hist *Histogram, iters, errs *counter, r *Result) {
	jobs := make(chan int, p.Rate*2)
	var wg sync.WaitGroup
	for w := 1; w <= p.VUs; w++ {
		w := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case iter, ok := <-jobs:
					if !ok {
						return
					}
					executeIteration(ctx, s, w, iter, hist, iters, errs, r)
				}
			}
		}()
	}
	interval := time.Second / time.Duration(p.Rate)
	if interval < time.Microsecond {
		interval = time.Microsecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	dispatched := 0
	for {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return
		case <-ticker.C:
			dispatched++
			select {
			case jobs <- dispatched:
			default:
				// Channel full: queue saturated. Drop the iteration
				// and let the load test surface the inability to
				// keep up with Rate as backpressure in throughput.
			}
		}
	}
}

// runRampingVU ramps the active worker count through the RampStages.
// Each stage is run with its target VU count for its duration.
func runRampingVU(ctx context.Context, s Scenario, p Profile, hist *Histogram, iters, errs *counter, r *Result) {
	for _, stage := range p.RampStages {
		stageCtx, cancel := context.WithTimeout(ctx, stage.Duration)
		stageProfile := Profile{Kind: ConstantVU, VUs: stage.Target, Duration: stage.Duration}
		runConstantVU(stageCtx, s, stageProfile, hist, iters, errs, r)
		cancel()
		if ctx.Err() != nil {
			return
		}
	}
}

func workerLoop(ctx context.Context, s Scenario, vu int, hist *Histogram, iters, errs *counter, r *Result) {
	iter := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		iter++
		executeIteration(ctx, s, vu, iter, hist, iters, errs, r)
	}
}

func executeIteration(ctx context.Context, s Scenario, vu, iter int, hist *Histogram, iters, errs *counter, r *Result) {
	start := time.Now()
	err := s.Run(ctx, vu, iter)
	hist.ObserveDuration(time.Since(start))
	iters.inc()
	if err != nil {
		errs.inc()
		r.recordError(err)
	}
}
