// SPDX-License-Identifier: MIT

// Package adminops holds the session core's side of the operations through
// which the API front's admin handlers reach the packages GW-R2 confines
// and does not allow the API front: the token revocation propagator, the
// pool credential revoker, the user lease revoker, the tenant key probe,
// and the connector secret resolver. Each type wraps one confined value
// and serves one interface of pkg/gateway/seam, so the composition root
// hands the API front a value of the session core and never a value of a
// confined package.
//
// spec: GWSEAM
package adminops
