// SPDX-License-Identifier: MIT

package checkpoint

import (
	"testing"
	"time"
)

// FuzzWorkspaceSizePreCheck exercises the §4.4 workspace-size
// pre-check on arbitrary (workspaceBytes, limitBytes) tuples.
// Invariant: never panics; over-limit always errors.
func FuzzWorkspaceSizePreCheck(f *testing.F) {
	f.Add(int64(0), int64(100<<20))
	f.Add(int64(50<<20), int64(100<<20))
	f.Add(int64(101<<20), int64(100<<20))
	f.Add(int64(-1), int64(0))

	f.Fuzz(func(t *testing.T, workspaceBytes, limitBytes int64) {
		err := WorkspaceSizePreCheck(workspaceBytes, limitBytes)
		if workspaceBytes > limitBytes && limitBytes > 0 && err == nil {
			t.Errorf("over-limit workspace (%d > %d) was accepted", workspaceBytes, limitBytes)
		}
	})
}

// FuzzFreshnessCheck exercises the §4.4 freshness predicate on
// arbitrary (now, last, interval) tuples. Invariant: never panics.
func FuzzFreshnessCheck(f *testing.F) {
	f.Add(int64(0), int64(0), int64(int64(time.Hour)))
	f.Add(int64(time.Hour.Nanoseconds()), int64(0), int64(time.Minute.Nanoseconds()))

	f.Fuzz(func(t *testing.T, nowNS, lastNS, intervalNS int64) {
		now := time.Unix(0, nowNS)
		last := time.Unix(0, lastNS)
		interval := time.Duration(intervalNS)
		_ = FreshnessCheck(now, last, interval)
	})
}
