// SPDX-License-Identifier: MIT

//go:build !linux

package scrub

import "errors"

// errUnsupported is the error every scrub file operation returns off Linux,
// where openat2 is not available. The scrub fails closed rather than falling
// back to operations that follow symbolic links. spec: §5.2 ("What the scrub
// reaches").
var errUnsupported = errors.New("scrub: file operations require openat2 (Linux)")

func removeBeneath(string) error                  { return errUnsupported }
func clearBeneath(string) error                   { return errUnsupported }
func pathStateBeneath(string) (bool, bool, error) { return false, false, errUnsupported }
func shmRemove(int) error                         { return errUnsupported }
