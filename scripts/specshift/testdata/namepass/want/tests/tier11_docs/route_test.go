// SPDX-License-Identifier: MIT
//go:build tier11_docs

// The header of this carrier is a prose block the build constraint above
// separates from the package clause by a blank line, so the parser
// attaches it to nothing. It names the CH-PODLIFECYCLE here, and it
// names the CH-ADAPTEREVENTS again in this second sentence.

package docs

// requireLine pins a sentence of the specification verbatim, and the
// pinned-literal register names it, so the run that rewrites the
// sentence rewrites this literal in the same diff.
const requireLine = "The base policy allows only the gRPC LNK-GWCONTROL (port 50051) and DNS."

// skipReason is operator-facing text this carrier prints. The
// pinned-literal register does not name it, so the pass leaves it as it
// stands.
const skipReason = "skipped: the lifecycle channel fixture is unavailable"

// Route documents the LNK-GWCONTROL in a comment the parser does
// attach to a declaration, which takes the occurrence number after the
// header block's sites and after the pinned literal.
func Route() {}
