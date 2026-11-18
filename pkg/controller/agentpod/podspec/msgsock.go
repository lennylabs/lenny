// SPDX-License-Identifier: MIT

package podspec

const (
	// RuntimeSocketName is the §4.7 sidecar-model abstract Unix socket
	// the adapter binds and the runtime container dials. A Linux
	// abstract address begins with "@" and occupies the kernel
	// abstract namespace — no filesystem path, reachable across the
	// pod's containers because they share the network namespace.
	RuntimeSocketName = "@lenny-runtime"

	// RuntimeSocketEnvVar is the environment variable the runtime
	// container reads to discover the adapter's runtime socket. It
	// matches runtimekit.SocketEnvVar.
	RuntimeSocketEnvVar = "LENNY_ADAPTER_SOCKET"
)
