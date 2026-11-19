// SPDX-License-Identifier: MIT

// Package platformtools holds the platform tools seam's side of the
// operations the session core calls on it. It serves the delegation tree
// operations over the tree budget counters, the tree archive, the usage
// rollups, the orphan classification, and the bottom-up tree recovery,
// and the registries of the input requests and tool-call approval waits a
// session's runtime opens through platform tools. It also serves the
// client API memory routes, which it registers through the route
// registration interface.
//
// The package reaches the session core only through pkg/gateway/seam: the
// tree recovery drives each node through the tree node operations the
// core serves, and the memory routes resolve the request's tenant through
// the request identity operation. It converts the seam's session record
// into the session store's row for the packages it adapts, which read the
// row's fields the record carries.
//
// spec: GWTOOLS, DLG, DTREE
package platformtools
