// SPDX-License-Identifier: MIT

//go:build !unix

package k3s

// process_other.go is the off-unix stub of the cross-package process-stop
// surface. The Linux child-process substrate runs only on a unix host
// (childk3s_unix.go), so the process-group stop is meaningful only there.
// macOS and Windows provision the substrate as a Docker container, whose
// stop is StopContainer, so off Linux the recorded k3s PID is always zero and
// StopProcessGroup has nothing to signal. The stub exists so lenny down (in
// pkg/embedded/stack, which compiles on every OS) can call StopProcessGroup
// unconditionally and let the empty handle make it a no-op, mirroring how an
// empty container handle makes StopContainer a no-op.
//
// spec: §17.4 (the Linux substrate is a managed child process; macOS and
// Windows provision it as a Docker container).

// StopProcessGroup is a no-op off unix: there is no Linux k3s host process
// group to terminate. The Docker-backed substrate is stopped through
// StopContainer instead.
func StopProcessGroup(_ int) {}
