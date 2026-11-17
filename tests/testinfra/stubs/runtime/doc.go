// SPDX-License-Identifier: MIT

// Package runtime is the in-process adapter stub used by the load_local tier
// multi-component scenarios. It speaks the documented adapter
// JSONL wire format with configurable latency, error injection,
// and concurrency caps.
//
// The Wave 2 cut exposes the configuration knobs every load_local tier
// scenario needs (latency, error rate, max concurrent in-flight
// tool calls). Scenarios that need more precise wire-level control
// (specific JSONL field round-trips, idle-timer behaviour, etc.)
// extend the stub through additional knobs as Wave 3 lands them.
//
// Used by load_local-tier scenarios under
// tests/load_local/scenarios/.
package runtime
