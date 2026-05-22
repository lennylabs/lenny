// SPDX-License-Identifier: MIT

// Package errorprop is the tier-0 static check that an error returned
// from a Close, Cleanup, Release, Drain, Stop, or Flush call site is
// always propagated, logged, or recorded as a metric — never silently
// dropped.
//
// Regression source: commit f54b7bb. The gateway's
// recordSessionCompleted dropped executor.Close errors silently, which
// hid the 403 on missing RBAC for a full debug cycle. The check
// in this package catches the class of bug at PR time.
//
// The check is intentionally narrow: it only fires on the well-known
// teardown verbs Close, Cleanup, Release, Drain, Stop, Flush. Other
// errors are reviewed at code-review time.
//
// TESTING.md §6.4 (Wave 4 unit-test uplift).
package errorprop
