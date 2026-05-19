// SPDX-License-Identifier: MIT

//go:build unix

package stack

import "syscall"

// syscall0 returns the no-op signal used for process liveness probes.
// Signal 0 performs error checking in the kernel without delivering a
// signal, so it reports whether a PID is alive and signalable.
func syscall0() syscall.Signal { return syscall.Signal(0) }
