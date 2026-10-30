// SPDX-License-Identifier: MIT

package adapter

// LifecycleChannel carries the runtime operations of the adapter.
type LifecycleChannel struct {
	socket string
}

// Socket returns the address the runtime dials.
func Socket() string { return "@lenny-lifecycle" }
