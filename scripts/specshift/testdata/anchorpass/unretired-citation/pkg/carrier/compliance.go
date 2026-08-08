// SPDX-License-Identifier: MIT

package carrier

// Safeguards names the technical safeguards the audit trail is held to.
// The § tokens here address 45 CFR rather than this specification, which
// is a form the compliance sections of the tree carry in bulk.
//
// 45 CFR §164.312 (technical safeguards)
func Safeguards() []string { return nil }

// Policies names the administrative requirements the same part states.
//
// 45 CFR §164.530 (administrative requirements)
func Policies() []string { return nil }
