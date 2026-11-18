// SPDX-License-Identifier: MIT

// Package gatewaywiring is the wiring check of the gateway: the static check
// that reads the composition root, cmd/lenny-gateway, with full type
// information and refuses a value of one gateway part passed where another
// part expects an interface or a function, unless the interface is declared
// in the seam interface package, pkg/gateway/seam.
//
// The layout gate reads imports. A part also reaches another part when the
// composition root passes it a value of the other part through an interface
// the first part declares, such as the session server passed where the
// platform tools seam expects its terminal interface; no import crosses
// there. This check finds those crossings wherever the composition root
// gives a value to a target whose type is an interface or a function: call
// arguments, struct literal fields, map, slice, and array elements, field
// assignments, typed variable declarations, return statements, and channel
// sends. It counts a crossing only where the import rules of the layout
// refuse the receiving part importing the value's package (decision 12 of
// change 0117). A value of an adapter type the composition root declares
// counts as the values its fields hold, and a function literal or a method
// value of the composition root counts as what its body calls and captures.
//
// The register tests/registers/gateway-wiring.yaml lists the crossings
// that existed when the check landed, each with the step of change 0117
// that routes it through the seam interface. The check fails on a crossing
// the register does not carry and on a row that no longer occurs, and the
// tree test compares the register with its copy at the merge base, so the
// register only shrinks.
//
// The check states the wiring form of GW-R1 on the gateway's index page.
package gatewaywiring
