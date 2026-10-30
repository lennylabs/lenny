// SPDX-License-Identifier: MIT
//go:build tier11_docs

// The header of this carrier is a prose block the build constraint above
// separates from the package clause by a blank line, so the parser
// attaches it to no declaration. It is no doc comment, so the lifecycle
// channel it names here is outside the position the law governs and
// takes no register entry.

package docs

// requireLine pins a sentence of the specification verbatim, and the
// pinned-literal register names it, so the run that rewrites the
// sentence rewrites this literal in the same diff.
const requireLine = "The base policy allows only the gRPC control channel (port 50051) and DNS."

// skipReason is operator-facing text this carrier prints. The
// pinned-literal register does not name it, so the pass leaves it as it
// stands.
const skipReason = "skipped: the lifecycle channel fixture is unavailable"

// Route documents the lifecycle channel in a comment the parser attaches
// to a declaration, which takes the occurrence number after the pinned
// literal above it.
func Route() {}
