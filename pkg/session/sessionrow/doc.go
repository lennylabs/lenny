// SPDX-License-Identifier: MIT

// Package sessionrow declares the session row that the session store
// persists, and the types the row holds. The row is a value: the package
// declares no method on the row, and it imports no store, tenancy, or
// credential package. A package that carries the row without reading or
// writing the store, such as the seam interface in pkg/gateway/seam,
// therefore links neither the session store nor the packages the store
// imports: the tenant and user stores, and, through the tenant store,
// pkg/credential.
//
// Package sessionstore declares a type alias for each type here, so code
// that names the row through the session store names the same types.
//
// spec: §4.2 (Session Manager)
package sessionrow
