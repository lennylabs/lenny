// SPDX-License-Identifier: MIT

// Package seam is the seam interface of the gateway: the Go interfaces
// through which each seam and the session core reach each other, the
// route registration interfaces of the API front, and the values all of
// them carry.
//
// A seam reaches the session core and every other seam only through this
// package, and the session core reaches a seam only through it. The layout
// gate in tests/static/lint/gatewaylayout holds that rule over the import
// graph, and it also holds this package to importing no other package of
// the gateway. cmd/lenny-gateway wires the concrete types of each part into
// these interfaces, so extracting a seam into its own service means putting
// a transport under interfaces that already exist.
//
// Each method of each interface is one operation of the gateway seams page,
// identified GWSEAM-<method>. The interfaces are declared here and
// implemented by the steps of change 0117 that move each part into its
// directory.
//
// spec: GWSEAM, GW
package seam
