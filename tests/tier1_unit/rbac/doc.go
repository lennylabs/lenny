// SPDX-License-Identifier: MIT

// Package rbac is the tier-1 unit check that the chart's ClusterRoles
// contain every verb the gateway and controller binaries need.
//
// Regression source: commit f54b7bb. The gateway issued
// `client.Delete(... sandboxclaim ...)` while the gateway ClusterRole
// did not grant `delete` on `sandboxclaims`. Every tier-7 cloud-load
// terminate returned HTTP 200 to the caller while leaking the claim,
// and the pool saturated within seconds. The chart RBAC missing one
// verb is one of the highest-leverage bug classes: it surfaces only
// under load and only when the affected RBAC path actually fires.
//
// The check in this package asserts that a known-required set of
// {verb, resource} pairs is present in each ClusterRole template
// the gateway and controller bind to. The list is the source of
// truth derived from the codebase; whenever a new client.Verb call
// site is added, the test fails until the verb is added to both the
// chart and the assertion table here.
//
// A future enhancement walks the Go AST to derive the {verb, resource}
// set automatically. The Wave 4 cut is the hand-curated table.
//
// TESTING.md §6.1 (Wave 4 unit-test uplift).
package rbac
