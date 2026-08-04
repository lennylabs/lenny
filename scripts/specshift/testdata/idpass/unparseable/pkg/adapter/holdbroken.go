// SPDX-License-Identifier: MIT
//
// This carrier does not parse: the function body below is left open, so
// go/parser fails on it. A run whose confinement covers it reads and
// parses it; a run confined to the specification never opens it.

package adapter

// Serve serves the LifecycleChannel of the adapter.
func Serve() {
