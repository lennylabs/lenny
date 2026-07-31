// SPDX-License-Identifier: MIT

// Package carrier is an ordinary tracked Go carrier. It documents the lifecycle
// channel, which is a site the pass writes, and it holds the same phrase in a
// string literal, which is operator-facing text outside the law's domain.
package carrier

// validateUsage is the help text `lenny runtime validate` prints. The phrase it
// carries sits in a string literal rather than in a comment, so the pass leaves
// it as it stands.
const validateUsage = "Validate a runtime manifest, including its lifecycle channel declaration."

// Ack reports delivery.
func Ack() bool {
	// The implementation comment here names the control channel. A comment
	// inside a function body is a comment of a tracked Go file like any other,
	// so it is a site the pass writes and the register resolves.
	return true
}
