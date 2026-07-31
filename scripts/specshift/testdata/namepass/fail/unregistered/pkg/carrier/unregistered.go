// SPDX-License-Identifier: MIT

package carrier

// Ack reports delivery. This comment names the control channel, and the
// register carries no sense for the occurrence, so the run aborts here.
func Ack() bool { return true }
