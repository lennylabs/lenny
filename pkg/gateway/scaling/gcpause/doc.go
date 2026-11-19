// SPDX-License-Identifier: MIT

// Package gcpause emits the §4.1 lenny_gateway_gc_pause_p99_ms gauge.
//
// The §4.1 shared-process GC pressure signal is the additional
// indicator that the combined gateway binary is becoming a bottleneck
// regardless of per-subsystem queue depth: a sustained p99 GC pause
// >50 ms gates the §16.5 Tier3GCPressureHigh alert and the Tier 3
// promotion criterion. This package owns the periodic
// runtime/debug.ReadGCStats sweep, a sliding-window p99 calculator,
// and the gauge emission.
//
// The collector is goroutine-safe and clock-injectable for tests.
//
// spec: GWSCALE
package gcpause
