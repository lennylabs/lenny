// SPDX-License-Identifier: MIT

// Package subsystem is part of the seam model: the concurrency primitives
// each gateway seam runs behind. It holds an in-memory circuit breaker
// and a max-concurrent semaphore (Limiter), joined in a Subsystem value
// that gates each call a seam makes.
//
// Each seam owns its own Breaker and Limiter, so a fault in one seam does
// not reach another: uploads that fill the Upload Handler's limiter leave
// the Stream Proxy's goroutines free, and an upstream that trips the
// Upload Handler's breaker leaves the Stream Proxy and the MCP Fabric
// untouched, as the §4.1 partial-degradation contract states. The
// package emits the per-seam metrics (queue depth, circuit state, and
// error counters) that the §16.5 alerts and dashboards read for each seam
// on its own.
//
// The breakers are per replica and in memory. The operator-managed
// platform-wide circuit breakers of §11.6, which Redis backs across
// replicas, are a separate mechanism.
//
// spec: GW
package subsystem
