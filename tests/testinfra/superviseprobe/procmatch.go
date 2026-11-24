// SPDX-License-Identifier: MIT

package superviseprobe

// The process matchers read only a Process value, so they build on every
// host: the supervised-pod helpers, which build on a developer's macOS
// host, call IsProbeDaemon on the process list a cluster test reads from a
// pod. They lived in procs_linux.go, which left `go vet ./...` on macOS
// with an undefined name (change 0172, decision 10).

// IsServingProbe reports whether p is a serving probe: the probe binary with
// no subcommand, outside PID 1, which is the supervisor.
func IsServingProbe(p Process, exe string) bool {
	return p.PID != 1 && len(p.Cmdline) == 1 && p.Cmdline[0] == exe && p.State != "Z"
}

// IsProbeDaemon reports whether p is a live probe daemon, the grandchild
// that CmdDaemon or a cleanup command running SubDaemonChild leaves behind.
func IsProbeDaemon(p Process, exe string) bool {
	return len(p.Cmdline) == 2 && p.Cmdline[0] == exe && p.Cmdline[1] == subDaemonGrandchild && p.State != "Z"
}
