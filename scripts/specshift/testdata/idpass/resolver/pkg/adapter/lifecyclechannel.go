// SPDX-License-Identifier: MIT

package adapter

// LifecycleChannel carries the runtime operations of the adapter.
//
// The naming law is stated at §28.1 line 400, a citation that resolves
// outside the section it names and that the resolution baseline carries.
type LifecycleChannel struct {
	socket string
}

// Socket returns the address the runtime dials.
func Socket() string { return "@lenny-lifecycle" }
