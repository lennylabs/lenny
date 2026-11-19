// SPDX-License-Identifier: MIT

// Package toolsession converts between the session store's row, which the
// platform tools seam reads and writes itself, and the session record that
// crosses the seam interface. The record carries the row in its Stored
// field, so an operation the session core serves on the record runs on the
// row the platform tool acted on rather than on a second read.
//
// spec: GWTOOLS, GWSEAM
package toolsession
