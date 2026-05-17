// SPDX-License-Identifier: MIT

package matrix

import (
	"sort"
	"strings"
	"testing"
)

// spec: 12.3.1 (contract-test matrix runner)
// diagnosis: The matrix expander emits the wrong cross-product, so a
//
//	contract test silently covers fewer cells than the
//	dimensions claim.
func TestExpandCartesian(t *testing.T) {
	t.Parallel()
	dims := []Dimension{
		Dim("a", []string{"1", "2"}),
		Dim("b", []string{"x", "y"}),
	}
	cells := expand(dims)
	if len(cells) != 4 {
		t.Fatalf("got %d cells; want 4", len(cells))
	}
	pairs := []string{}
	for _, c := range cells {
		pairs = append(pairs, c["a"]+","+c["b"])
	}
	sort.Strings(pairs)
	want := []string{"1,x", "1,y", "2,x", "2,y"}
	for i, p := range pairs {
		if p != want[i] {
			t.Errorf("cells[%d]=%q; want %q", i, p, want[i])
		}
	}
}

// spec: 12.3.1 (contract-test matrix runner — naming)
// diagnosis: The deterministic sub-test name drifts so reruns by
//
//	name break.
func TestCellNameDeterministic(t *testing.T) {
	t.Parallel()
	dims := []Dimension{
		Dim("provider", []string{"openai"}),
		Dim("event", []string{"session.start"}),
	}
	cell := map[string]string{"provider": "openai", "event": "session.start"}
	got := cellName(dims, cell)
	want := "event=session.start/provider=openai"
	if got != want {
		t.Fatalf("cellName = %q; want %q", got, want)
	}
}

// spec: 12.3.1 (matrix runner — skip predicate)
// diagnosis: WithSkip rejects a cell that should run, or accepts a
//
//	cell that should be filtered.
func TestRunWithSkipFiltersCells(t *testing.T) {
	t.Parallel()
	var seen []string
	Run(
		t,
		Dim("provider", []string{"a", "b", "c"}),
	)(func(t *testing.T, cell map[string]string) {
		seen = append(seen, cell["provider"])
	}, WithSkip(func(cell map[string]string) string {
		if cell["provider"] == "b" {
			return "not implemented for b"
		}
		return ""
	}))
	sort.Strings(seen)
	if strings.Join(seen, ",") != "a,c" {
		t.Fatalf("cells seen = %v; want [a c]", seen)
	}
}
