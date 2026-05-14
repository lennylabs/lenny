// SPDX-License-Identifier: MIT

// Package matrix is the §12.3.1 contract-test matrix runner.
//
// A contract test states an invariant ("the OCSF translator emits a
// schema-valid event for every Lenny event type"); the matrix runner
// expands that invariant across a cross-product of dimensions (every
// event type × every catalog version × every translator variant) and
// reports pass/fail per cell. Tests use Run to declare their
// dimensions; the helper takes care of t.Run, sub-test naming, and
// failure aggregation.
//
// Usage:
//
//	matrix.Run(t,
//	    matrix.Dim("provider", []string{"anthropic", "openai", "google"}),
//	    matrix.Dim("event",    []string{"session.start", "session.end", "tool.call"}),
//	)(func(t *testing.T, cell map[string]string) {
//	    // cell["provider"] and cell["event"] are bound here.
//	    actual := translate(cell["provider"], cell["event"])
//	    assertSchemaValid(t, actual)
//	})
//
// The expansion is fail-fast at the cell level (a failed cell does
// not stop the matrix; failures aggregate in the parent test's
// status). For dimensions whose cells share fixture cost, callers
// register a per-dimension setup via WithSetup.
package matrix

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// Dimension is one axis of the cross-product. Name is the label used
// in the sub-test name; Values is the enumeration.
type Dimension struct {
	Name   string
	Values []string
}

// Dim is shorthand for constructing a Dimension.
func Dim(name string, values []string) Dimension {
	return Dimension{Name: name, Values: values}
}

// CellFn is the test body invoked once per cell. The map is keyed by
// dimension name; the value is the cell's binding for that dimension.
type CellFn func(t *testing.T, cell map[string]string)

// Option customizes a matrix run. WithSetup registers a per-cell
// setup hook; WithSkip registers a predicate that returns a non-empty
// skip reason for cells the runner should bypass.
type Option func(*config)

type config struct {
	setup func(t *testing.T, cell map[string]string)
	skip  func(cell map[string]string) string
}

// WithSetup runs `fn` before each cell's CellFn. Use to prepare
// fixtures that depend on the cell's bindings.
func WithSetup(fn func(t *testing.T, cell map[string]string)) Option {
	return func(c *config) { c.setup = fn }
}

// WithSkip returns a reason for cells that should not run, or "" to
// proceed. Use to skip the not-yet-supported combinations during
// staged rollouts.
func WithSkip(fn func(cell map[string]string) string) Option {
	return func(c *config) { c.skip = fn }
}

// Run expands the cross-product of `dims` and invokes the returned
// closure once per cell via t.Run. The sub-test name is the cell
// bindings joined by `/`. Cells run sequentially within the parent
// test; the runner does not call t.Parallel — callers that want
// parallelism opt in inside their CellFn.
func Run(t *testing.T, dims ...Dimension) func(CellFn, ...Option) {
	t.Helper()
	return func(fn CellFn, opts ...Option) {
		cfg := config{}
		for _, opt := range opts {
			opt(&cfg)
		}
		if len(dims) == 0 {
			t.Fatal("matrix.Run: no dimensions")
		}
		for _, d := range dims {
			if len(d.Values) == 0 {
				t.Fatalf("matrix.Run: dimension %q has no values", d.Name)
			}
		}
		for _, cell := range expand(dims) {
			cell := cell
			name := cellName(dims, cell)
			t.Run(name, func(t *testing.T) {
				if cfg.skip != nil {
					if reason := cfg.skip(cell); reason != "" {
						t.Skip(reason)
					}
				}
				if cfg.setup != nil {
					cfg.setup(t, cell)
				}
				fn(t, cell)
			})
		}
	}
}

// expand returns every cell as a map keyed by dimension name. The
// cells are emitted in lexicographic order of dimension names, then
// in the order of each dimension's Values.
func expand(dims []Dimension) []map[string]string {
	if len(dims) == 0 {
		return []map[string]string{}
	}
	out := []map[string]string{{}}
	for _, d := range dims {
		next := make([]map[string]string, 0, len(out)*len(d.Values))
		for _, partial := range out {
			for _, v := range d.Values {
				cell := make(map[string]string, len(partial)+1)
				for k, val := range partial {
					cell[k] = val
				}
				cell[d.Name] = v
				next = append(next, cell)
			}
		}
		out = next
	}
	return out
}

// cellName renders a deterministic sub-test name like
// "provider=anthropic/event=session.start".
func cellName(dims []Dimension, cell map[string]string) string {
	keys := make([]string, 0, len(dims))
	for _, d := range dims {
		keys = append(keys, d.Name)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, cell[k]))
	}
	return strings.Join(parts, "/")
}
