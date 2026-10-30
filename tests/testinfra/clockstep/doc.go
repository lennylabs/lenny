// SPDX-License-Identifier: MIT

// Package clockstep is a deterministic, advanceable clock for tests
// that need to express "advance to T+N seconds and observe that event
// E fires at T+M seconds." It is the explicit time control that
// ordering and lease-window scenarios need: every Now() returns the
// clock's current value rather than wall-clock time, and Advance(d)
// shifts the clock forward by d while waking every timer or ticker
// whose deadline has passed.
//
// Used by tier-1 unit tests that need a deterministic clock under
// the race detector. The companion package watchlag wraps the same
// mechanism for "delivery happens N units after mutation."
//
// TESTING.md §6.3 (Wave 4 ordering and mirror-lag tests).
package clockstep
