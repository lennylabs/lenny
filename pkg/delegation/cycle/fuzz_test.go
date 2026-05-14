// SPDX-License-Identifier: MIT

package cycle

import (
	"testing"
)

// FuzzLineageContains exercises Lineage.Contains on arbitrary
// (lineage, probe) tuples. Invariant: never panics; bounded by
// linear scan correctness.
func FuzzLineageContains(f *testing.F) {
	f.Add("a,b,c", "b")
	f.Add("", "x")
	f.Add("a", "a")

	f.Fuzz(func(t *testing.T, lineageCSV, probeName string) {
		lineage := Lineage{}
		for _, name := range splitCSV(lineageCSV) {
			lineage = append(lineage, Identity{RuntimeName: name, PoolName: "p"})
		}
		probe := Identity{RuntimeName: probeName, PoolName: "p"}
		_ = lineage.Contains(probe)
		_ = lineage.Depth()
	})
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	out := []string{}
	cur := ""
	for _, c := range s {
		if c == ',' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(c)
	}
	out = append(out, cur)
	return out
}
