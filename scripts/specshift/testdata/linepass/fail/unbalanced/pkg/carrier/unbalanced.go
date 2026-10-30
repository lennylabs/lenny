// SPDX-License-Identifier: MIT

// Package carrier holds a citation whose head opens a parenthesis that
// nothing closes inside the citation.
package carrier

// lease returns the claim lease.
//
// The claim is a lease per §4.6 (lines 5-6; the renewal is
// idempotent per §4.6.1 line 10).
func lease() string { return "lease" }
