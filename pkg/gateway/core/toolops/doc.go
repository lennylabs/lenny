// SPDX-License-Identifier: MIT

// Package toolops holds the session core's side of the operations through
// which the platform tools seam reaches the packages GW-R2 confines: the
// interceptor chain and its weakening cooldowns, the export scan, the
// caller check of a delegating bearer, the VCS credential pools, the tenant
// store, environment access, and the request-rate counter. Each type wraps
// one confined value and serves one interface of pkg/gateway/seam, so the
// composition root hands the platform tools seam a value of the session
// core and never a value of a confined package.
//
// Callers serves the caller identity operation, which the API front owns;
// the session core serves it until change 0117's step 0117.S8 moves the
// request pipeline into the API front.
//
// spec: GWSEAM, GW
package toolops
