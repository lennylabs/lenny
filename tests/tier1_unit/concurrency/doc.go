// SPDX-License-Identifier: MIT

// Package concurrency holds tier-1 concurrency contract tests for
// state-machine packages whose public surface is not driven through
// their own concurrent_test.go yet.
//
// The pattern mirrors `pkg/gateway/slotcounter/slotcounter_test.go::TestReserveIsAtomicUnderRace`:
// N goroutines exercise the package's public surface concurrently
// with the race detector on; the test asserts the documented
// invariants hold under contention.
//
// TESTING.md §6.2 (Wave 4 concurrency contract tests).
package concurrency
