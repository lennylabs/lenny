// SPDX-License-Identifier: MIT

// Package fakekube is a fake Kubernetes API surface for tier-7a load
// tests. It builds on controller-runtime's fake client and the
// client-go fake.Clientset, but adds two behaviours the upstream
// fakes lack:
//
//  1. Honours ResourceVersion on update and patch so SSA conflicts
//     surface truthfully. The upstream controller-runtime fake
//     happily silently overwrites without ResourceVersion checks,
//     which masks every SSA last-write-wins bug that load tests
//     need to catch.
//
//  2. Configurable watch-event delay. A write completes before the
//     corresponding watch event is delivered to subscribers. The
//     delay is per-watcher and configurable so admission-ordering
//     tests can express "the webhook saw a stale phase mirror"
//     deterministically.
//
// The package wraps the standard fake clients rather than replacing
// them. Tests that do not need the additional behaviours can use
// the embedded *fake.Clientset directly.
//
// Used by tier-7a multi-component scenarios under
// tests/tier7a_load_local/scenarios/.
package fakekube
