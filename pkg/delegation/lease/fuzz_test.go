// SPDX-License-Identifier: MIT

package lease

import (
	"testing"
)

// FuzzValidateChildSlice exercises the §8 budget validator on
// arbitrary (parent, child, budget) tuples. Invariant: never
// panics; child > parent budget always errors.
func FuzzValidateChildSlice(f *testing.F) {
	f.Add(int64(1000), int64(500), int64(500), int(5), int(2), int(10), int(5))
	f.Add(int64(0), int64(0), int64(0), int(0), int(0), int(0), int(0))
	f.Add(int64(100), int64(200), int64(50), int(1), int(2), int(5), int(2))

	f.Fuzz(func(t *testing.T,
		parentBudget, childBudget int64, remainingBudget int64,
		parentChildren, childChildren, parentTree, childTree int,
	) {
		parent := LeaseSlice{
			MaxTokenBudget:   parentBudget,
			MaxChildrenTotal: parentChildren,
			MaxTreeSize:      parentTree,
		}
		child := LeaseSlice{
			MaxTokenBudget:   childBudget,
			MaxChildrenTotal: childChildren,
			MaxTreeSize:      childTree,
		}
		_ = ValidateChildSlice(parent, child, remainingBudget, parentChildren-1, parentTree-1)
	})
}

// FuzzCheckDepth exercises the §8.2 depth bound on arbitrary
// inputs. Invariant: depth ≥ max always errors; never panics.
func FuzzCheckDepth(f *testing.F) {
	f.Add(0, 5)
	f.Add(5, 5)
	f.Add(6, 5)
	f.Add(-1, 0)

	f.Fuzz(func(t *testing.T, current, max int) {
		err := CheckDepth(current, max)
		if max > 0 && current >= max && err == nil {
			t.Errorf("depth %d >= max %d should error", current, max)
		}
	})
}
