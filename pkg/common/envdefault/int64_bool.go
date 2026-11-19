// SPDX-License-Identifier: MIT

package envdefault

import (
	"os"
	"strconv"
	"strings"
)

// Int64 returns the variable name parsed as an int64, or def when it is
// unset, blank, or does not parse.
func Int64(name string, def int64) int64 {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}

// Bool returns true when the variable name is "1", "true", "yes", or "on",
// in any case and with surrounding space trimmed, false when it is "0",
// "false", "no", or "off", and def otherwise, including when it is unset.
func Bool(name string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

// Float64 returns the variable name parsed as a float64, or def when it is
// unset, blank, or does not parse.
func Float64(name string, def float64) float64 {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}
