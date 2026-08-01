// SPDX-License-Identifier: MIT

package carrier

// Flow reads the flow-control rules.
//
// spec: §15.9.9 (a section the specification no longer declares and the
// anchor-move map carries no entry for)
func Flow() []byte { return nil }

// Frame writes the adapter framing onto the connection.
//
// spec: §15.4.1 (the framing of the connection, whose anchor the map retires)
func Frame() []byte { return nil }
