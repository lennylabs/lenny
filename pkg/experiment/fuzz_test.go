// SPDX-License-Identifier: MIT

package experiment

import (
	"testing"
)

// FuzzAssignVariant exercises the §10.7 HMAC-SHA256 bucketing on
// arbitrary (assignmentKey, experimentID) inputs. Invariants:
//
//   - AssignVariant never panics.
//   - Identical inputs return identical assignments (determinism).
//   - The returned variant id is always one of the configured set
//     (or the "control" implicit variant).
func FuzzAssignVariant(f *testing.F) {
	f.Add("user-123", "exp-rollout-1")
	f.Add("", "")
	f.Add("alice", "experiment-with-control-only")

	variants := []Variant{
		{ID: "treatment-a", Weight: 0.3},
		{ID: "treatment-b", Weight: 0.4},
	}

	f.Fuzz(func(t *testing.T, key, id string) {
		a := AssignVariant(key, id, variants)
		b := AssignVariant(key, id, variants)
		if a != b {
			t.Errorf("non-deterministic: %q vs %q for key=%q id=%q", a, b, key, id)
		}
		ok := a == "control"
		for _, v := range variants {
			if v.ID == a {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("unknown variant %q for key=%q id=%q", a, key, id)
		}
	})
}
