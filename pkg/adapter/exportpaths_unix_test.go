// SPDX-License-Identifier: MIT

//go:build unix

package adapter

import "syscall"

// makeFIFO creates a named pipe at path so the export test can assert
// non-regular files are dropped (§8.7 / §13.4).
func makeFIFO(path string) error {
	return syscall.Mkfifo(path, 0o600)
}
