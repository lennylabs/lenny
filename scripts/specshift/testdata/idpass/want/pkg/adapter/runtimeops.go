// SPDX-License-Identifier: MIT

package adapter

// RuntimeOpsChannel carries the runtime operations of the adapter.
type RuntimeOpsChannel struct {
	socket string
}

// Socket returns the address the runtime dials.
func Socket() string { return "@lenny-runtime-ops" }
