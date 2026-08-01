// SPDX-License-Identifier: MIT

package carrier

// Flow reads the flow-control rules.
//
// spec: §15.5 (a section a specification file still declares, which the
// anchor-move map retires no anchor for)
func Flow() []byte { return nil }

// Card names the channel contract card the framing moved to.
//
// spec: §28.5.2 (another section the specification declares and the
// anchor-move map retires no anchor for)
func Card() string { return "the channel contract card" }
