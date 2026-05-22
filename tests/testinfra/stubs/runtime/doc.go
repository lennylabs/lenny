// SPDX-License-Identifier: MIT

// Package runtime is the in-process adapter stub used by tier-7a
// multi-component scenarios. It speaks the documented adapter
// JSONL wire format with configurable latency, error injection,
// and concurrency caps.
//
// The Wave 2 cut exposes the configuration knobs every tier-7a
// scenario needs (latency, error rate, max concurrent in-flight
// tool calls). Scenarios that need more precise wire-level control
// (specific JSONL field round-trips, idle-timer behaviour, etc.)
// extend the stub through additional knobs as Wave 3 lands them.
//
// Used by tier-7a scenarios under
// tests/tier7a_load_local/scenarios/.
package runtime
