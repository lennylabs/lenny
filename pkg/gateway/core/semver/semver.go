// SPDX-License-Identifier: MIT

// Package semver compares MAJOR.MINOR.PATCH version strings. It
// tolerates a leading "v" and a pre-release or build suffix. An
// unparseable version sorts below any parseable one, so a comparison
// against a malformed or empty version is well defined rather than a
// panic.
package semver

import (
	"strconv"
	"strings"
)

// Parse parses a MAJOR.MINOR.PATCH version into its numeric components,
// tolerating a leading "v" and a pre-release or build suffix. Missing
// trailing components default to zero. ok is false when a component is
// non-numeric or the version is empty.
func Parse(v string) (parts [3]int, ok bool) {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	if v == "" {
		return parts, false
	}
	for i, seg := range strings.SplitN(v, ".", 3) {
		n, err := strconv.Atoi(seg)
		if err != nil {
			return parts, false
		}
		parts[i] = n
	}
	return parts, true
}

// Compare orders two version strings, returning -1, 0, or 1. The
// MAJOR.MINOR.PATCH components are compared numerically, so 1.9.0 sorts
// below 1.10.0. An unparseable version sorts below any parseable one;
// two unparseable versions compare equal.
func Compare(a, b string) int {
	pa, aok := Parse(a)
	pb, bok := Parse(b)
	switch {
	case !aok && !bok:
		return 0
	case !aok:
		return -1
	case !bok:
		return 1
	}
	for i := range pa {
		if pa[i] < pb[i] {
			return -1
		}
		if pa[i] > pb[i] {
			return 1
		}
	}
	return 0
}
