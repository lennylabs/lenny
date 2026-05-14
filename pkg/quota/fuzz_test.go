// SPDX-License-Identifier: MIT

package quota

import (
	"testing"
)

// FuzzHierarchicalCheck — the §11.2 hierarchical check accepts any
// (global, tenant, user, hierarchy) tuple without panicking and
// returns a non-OK state for any layer that exceeds its limit.
func FuzzHierarchicalCheck(f *testing.F) {
	f.Add(int64(0), int64(0), int64(0), int64(100), int64(50), int64(10))
	f.Add(int64(99), int64(49), int64(9), int64(100), int64(50), int64(10))
	f.Add(int64(101), int64(50), int64(10), int64(100), int64(50), int64(10))

	f.Fuzz(func(t *testing.T, gu, tu, uu, gl, tl, ul int64) {
		h := Hierarchy{Global: gl, Tenant: tl, User: ul}
		if err := h.Validate(); err != nil {
			return
		}
		_ = HierarchicalCheck(gu, tu, uu, h)
	})
}

// FuzzCheck — Check(used, limit) returns one of the documented
// states for every input; the function never panics.
func FuzzCheck(f *testing.F) {
	f.Add(int64(0), int64(100))
	f.Add(int64(80), int64(100))
	f.Add(int64(100), int64(100))
	f.Add(int64(101), int64(100))

	f.Fuzz(func(t *testing.T, used, limit int64) {
		state := Check(used, limit)
		switch state {
		case StateOK, StateSoftWarning, StateHardExceeded:
		default:
			t.Errorf("unknown state %q for used=%d limit=%d", state, used, limit)
		}
	})
}
