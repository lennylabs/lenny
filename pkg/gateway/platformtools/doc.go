// SPDX-License-Identifier: MIT

// Package platformtools holds the platform tools seam's side of the
// operations the session core calls on it, and the seam's client routes.
// It serves the delegation tree operations over the tree budget counters,
// the tree archive, the usage rollups, the orphan classification, and the
// bottom-up tree recovery; the registries of the input requests and
// tool-call approval waits a session's runtime opens through platform
// tools; and the tool approval gate the session core's pod executor holds
// a runtime's tool call in until the session's user decides. Its Handler
// serves the client API memory routes, the tool-use and elicitation
// resolution routes, and the delegation tree read, which it registers
// through the route registration interface.
//
// The package reaches the session core only through pkg/gateway/seam: the
// tree recovery drives each node through the tree node operations the
// core serves, and the client routes resolve the request's tenant, its
// caller, the session a memory route names, and the user lifecycle check
// through the operations the core serves. It converts the seam's session
// record into the session store's row for the packages it adapts, which
// read the row's fields the record carries.
//
// spec: GWTOOLS, DLG, DTREE
package platformtools
