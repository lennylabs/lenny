// SPDX-License-Identifier: MIT

// Package carrier holds a citation whose bare-word gloss runs to a
// trailing colon, so the anchor that replaces it stands against that
// colon and reads the integer opening the next comment line as a
// member.
package carrier

// hold reports the lease gauge the §4.6 line 5 claim gauge:
// 1 while the lease is held, 0 once the lease lapses.
func hold() int { return 1 }
