// SPDX-License-Identifier: MIT

// Package watchlag delivers events with a configurable per-subscriber
// delay between mutation and notification. It backs the §5.2-class
// ordering tests where a write completes on the fake API server but
// the corresponding watch event is artificially deferred so admission
// rules that observe stale phase mirrors can be expressed
// deterministically.
//
// Used by tier-1 unit tests that need ordering control. The companion
// package clockstep provides the underlying deterministic clock.
//
// TESTING.md §6.3 (Wave 4 ordering and mirror-lag tests).
package watchlag
