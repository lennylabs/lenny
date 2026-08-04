// SPDX-License-Identifier: MIT

// Package carrier is the carrier of this fixture that sits outside the
// specification directory, so one run of the pass over the fixture
// partitions into a specification half and a code half.
package carrier

// claim returns the sandbox claim.
//
// spec: §4.6 line 5
func claim() string { return "claim" }
