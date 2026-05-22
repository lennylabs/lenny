// SPDX-License-Identifier: MIT

// Package flagdefaults is the tier-1 regression test that every
// operationally-tunable default the gateway chooses is read from a
// flag, exposed in --help, and documented.
//
// Regression source: commit 0b7c71c. The gateway shipped with
// client-go's default QPS=5 / Burst=10, which throttled a tier-7
// scenario at 5 req/s with no operator-visible knob. The added
// --cluster-qps / --cluster-burst flags closed the hole; this
// package asserts they remain operator-tunable.
//
// TESTING.md §6.5 (Wave 4 default-tuning regression test).
package flagdefaults
