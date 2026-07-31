// SPDX-License-Identifier: MIT
//go:build tier11_docs

// The header of this carrier is a prose block the build constraint above
// separates from the package clause by a blank line, so the parser
// attaches it to nothing. It names the CH-PODLIFECYCLE here, and it
// names the CH-ADAPTEREVENTS again in this second sentence.

package docs

// Route documents the LNK-GWCONTROL in a comment the parser does
// attach to a declaration, which takes the occurrence number after the
// header block's sites.
func Route() {}
