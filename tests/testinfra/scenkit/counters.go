// SPDX-License-Identifier: MIT

package scenkit

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
)

// Counters is a named set of atomic int64 counters. Every tier-7a
// scenario carries a handful of counters that track outcomes
// (successes, failures, rejections, leaks). Centralising them here
// removes the per-scenario `field + atomic.Int64 + Add + Load +
// r.AddCustom` boilerplate.
//
// Safe for concurrent use.
type Counters struct {
	mu     sync.Mutex
	values map[string]*atomic.Int64
}

// NewCounters returns an empty set. Counters are auto-created on
// first Inc / Get so scenarios do not have to pre-declare them.
func NewCounters() *Counters {
	return &Counters{values: map[string]*atomic.Int64{}}
}

// Inc increments name by 1 and returns the new value.
func (c *Counters) Inc(name string) int64 {
	return c.counter(name).Add(1)
}

// Add adds delta to name and returns the new value.
func (c *Counters) Add(name string, delta int64) int64 {
	return c.counter(name).Add(delta)
}

// IncOnError increments name only if err is non-nil AND the error
// is not a benign run-end ctx cancellation. Scenarios use this to
// count "real" failures distinct from the tail of in-flight requests
// at run boundary.
func (c *Counters) IncOnError(ctx context.Context, name string, err error) {
	if err == nil {
		return
	}
	if IsBenignCancel(ctx, err) {
		return
	}
	c.Inc(name)
}

// Get returns the current value of name. Zero for an unknown name.
func (c *Counters) Get(name string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.values[name]
	if !ok {
		return 0
	}
	return v.Load()
}

// EmitTo records every counter as a custom metric on r. The metric
// name is the counter name verbatim.
func (c *Counters) EmitTo(r *loadgen.Result) {
	c.mu.Lock()
	names := make([]string, 0, len(c.values))
	for k := range c.values {
		names = append(names, k)
	}
	values := make(map[string]int64, len(c.values))
	for _, k := range names {
		values[k] = c.values[k].Load()
	}
	c.mu.Unlock()
	sort.Strings(names)
	for _, k := range names {
		r.AddCustom(k, float64(values[k]))
	}
}

func (c *Counters) counter(name string) *atomic.Int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.values[name]
	if ok {
		return v
	}
	v = new(atomic.Int64)
	c.values[name] = v
	return v
}
