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
// there. This check finds those crossings at every call argument, struct
// literal field, and field assignment of the composition root whose target
// type is an interface or a function, and whose value's type, method, or
// function belongs to another part. A value the composition root builds
// itself, such as an adapter type or a function literal declared in
// cmd/lenny-gateway, is the composition root's own and is not a crossing.
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
