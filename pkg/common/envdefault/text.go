// SPDX-License-Identifier: MIT

package envdefault

import (
	"os"
	"strconv"
	"strings"
)

// String returns the trimmed variable name, or def when it is unset or
// blank.
func String(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

// Flag reports whether the variable name holds one of the switch-on words,
// 1, true, yes, or on, in any case. Any other value, and an unset
// variable, reads as false.
func Flag(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// Bool returns the variable name parsed by strconv.ParseBool, or def when
// it is unset, blank, or does not parse.
func Bool(name string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
