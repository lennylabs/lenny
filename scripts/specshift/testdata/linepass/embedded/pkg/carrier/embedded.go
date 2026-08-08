// SPDX-License-Identifier: MIT

// Package carrier holds a citation written inside a sentence, where the
// identifier standing behind the member is the sentence's object rather
// than a gloss on the cited line.
package carrier

// Guard reports whether the tenant guard is enforced. The admission webhook reads
// this to decide whether to enforce the §12.3 line 56 lenny_tenant_guard label on
// the namespace it admits.
func Guard() bool { return true }
