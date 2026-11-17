// SPDX-License-Identifier: MIT

// Package loadgen is the pure-Go load driver for the load_local tier.
//
// The load_local tier runs in-process with no Kubernetes, no Docker, and no k6.
// Scenarios are Go files under tests/load_local/scenarios/<name>/
// that implement Scenario and register themselves with the default
// Registry via Register().
//
// The driver supports three profiles defined in TESTING.md §12.7.a:
//
//   - ConstantVU            N virtual users for the full duration.
//   - ConstantArrivalRate   R iterations per second across the worker
//     pool for the full duration.
//   - RampingVU             N-step ramp from start VUs to target VUs.
//
// The summary format mirrors k6's so baselines are comparable across
// the load_local tier, the load_kind tier, and load_cloud-tier even though load_kind-tier and load_cloud-tier use
// k6 as the driver.
//
// The race detector is expected to be on for every load_local-tier run; the
// scaffolds_test.go in tests/load_local/ invokes `go test`
// with `-race`, and scenarios that race in their public surface fail
// the run.
package loadgen
