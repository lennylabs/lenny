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
// arguments, struct literal fields, map, slice, and array elements, field,
// local, index, and tuple assignments, variable declarations, return
// statements, and channel sends. It counts a crossing only where the import
// rules of the layout refuse the receiving part importing the value's
// package (decision 12 of change 0117). The receiving part is the package of
// the struct field or function parameter that takes the value, where the
// composition root names one, and otherwise the package that declares the
// target type; the register keys each crossing by its receiver. Target
// types from the standard library or a dependency, net/http.Handler among
// them, are left out under decision 17 (GW-R1).
//
// The resolver attributes each value to the parts it reaches. It follows a
// value through conversions and type assertions, through local variables
// and every value they are given, through fields of the composition root's
// structs and every value they are given, through calls to composition-root
// functions, local function values, and invoked closures and the values
// they return, through adapters (structs the composition root declares) by
// every value their fields are given, in the literal that builds them or
// later by assignment or through a setter, nested adapters included, and
// through function literals, methods, and functions of the composition root
// by what their bodies call, capture, and read from the composition root's
// struct fields, calls to other composition-root functions included. A
// method value through an interface is attributed to what the receiver
// resolves to, a promoted method to the package that declares it, and a
// function read from another part's struct field, or returned by another
// part's function, to that part. The resolver memoizes what each variable,
// field, type, and function resolves to, cuts cycles, and fails the check
// when a chain reaches its depth bound rather than passing on a partial
// answer.
//
// The check also reports a value attributed to a package GW-R2 confines,
// handed to a struct field, parameter, or result of a part GW-R2 does not
// allow that package (the confined-value report). It uses the same
// resolver, so a confined store held by an adapter or captured by a closure
// counts.
//
// Some forms stay out of reach, and review is the backstop for them:
//
//   - generic helpers, whose type arguments the resolver does not follow;
//   - package-level variables, read anywhere and set anywhere, and globals
//     read inside an adapter's methods;
//   - parameters of composition-root functions, whose values depend on
//     every caller;
//   - values stored in or read from maps, slices, and channels the
//     composition root builds, beyond the literal or assignment that puts
//     them there;
//   - reflection, and values that pass through interfaces of the standard
//     library or a dependency before they reach a part.
//
// The register tests/registers/gateway-wiring.yaml lists the crossings
// that existed when the check landed, each with the step of change 0117
// that routes it through the seam interface. The check fails on a crossing
// the register does not carry and on a row that no longer occurs, and the
// tree test compares the register with its copy at the merge base, so the
// register only shrinks.
//
// The check states the wiring form of GW-R1 and the confined-value form of
// GW-R2 on the gateway's index page.
package gatewaywiring
