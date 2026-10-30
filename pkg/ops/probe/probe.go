// SPDX-License-Identifier: MIT

// Package probe runs the §25 lenny-ops dependency probes. lenny-ops
// checks its dependencies — Postgres, Redis, MinIO, the Kubernetes API,
// the gateway admin API — in parallel, bounding each by a timeout, and
// reports the results in its connectivity report and readiness signal.
//
// This package is the parallel-probe runner. The individual probe
// functions, which dial each dependency, are supplied by the caller so
// the runner has one tested implementation independent of them.
package probe

import (
	"context"
	"sync"
	"time"
)

// Func probes one dependency. It returns nil when the dependency is
// reachable and an error describing the failure otherwise. A probe
// should honor ctx cancellation; Run bounds it regardless.
type Func func(ctx context.Context) error

// Result is the outcome of one dependency probe.
type Result struct {
	// Name identifies the probed dependency.
	Name string
	// OK is true when the probe reported the dependency reachable.
	OK bool
	// Detail describes the failure when OK is false; it is empty on
	// success.
	Detail string
	// Duration is how long the probe took to settle.
	Duration time.Duration
}

// Run executes every probe in probes concurrently, bounding each by
// timeout, and returns the results keyed by probe name. A probe that
// neither completes nor honors cancellation within timeout is recorded
// as a timeout failure so Run itself always returns within roughly
// timeout. Run returns once every probe has settled or timed out.
func Run(ctx context.Context, probes map[string]Func, timeout time.Duration) map[string]Result {
	results := make(map[string]Result, len(probes))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for name, fn := range probes {
		wg.Add(1)
		go func(name string, fn Func) {
			defer wg.Done()
			start := time.Now()

			pctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- fn(pctx) }()

			var res Result
			select {
			case err := <-done:
				res = Result{Name: name, OK: err == nil, Duration: time.Since(start)}
				if err != nil {
					res.Detail = err.Error()
				}
			case <-time.After(timeout):
				res = Result{
					Name:     name,
					OK:       false,
					Detail:   "probe timed out",
					Duration: time.Since(start),
				}
			}

			mu.Lock()
			results[name] = res
			mu.Unlock()
		}(name, fn)
	}

	wg.Wait()
	return results
}

// AllOK reports whether every probe succeeded. An empty result set is
// reported as OK.
func AllOK(results map[string]Result) bool {
	for _, r := range results {
		if !r.OK {
			return false
		}
	}
	return true
}
