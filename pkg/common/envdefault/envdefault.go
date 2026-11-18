// SPDX-License-Identifier: MIT

// Package envdefault reads the environment variable that supplies a
// command-line flag's default value. A gateway part registers its own flags
// through it, so a flag the part moves out of cmd/lenny-gateway keeps the
// default the binary read before: the variable's value when it parses, and
// the built-in default when it is unset, blank, or does not parse.
//
// The package is contributor plumbing that decides no platform behavior, so
// it carries no specification annotation.
package envdefault

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Int returns the variable name parsed as an int, or def when it is unset,
// blank, or does not parse.
func Int(name string, def int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// Duration returns the variable name parsed by time.ParseDuration, such as
// "60s" or "5m", or def when it is unset, blank, or does not parse.
func Duration(name string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
