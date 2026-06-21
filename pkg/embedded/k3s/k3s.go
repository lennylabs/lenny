// SPDX-License-Identifier: MIT

// Package k3s provisions the embedded Kubernetes layer for §17.4
// Embedded Mode. The single-node k3s distribution bundles a Linux
// container runtime and an embedded datastore, so it needs a Linux
// kernel to run. The package provisions that substrate per host
// operating system behind the Launcher interface: a managed k3s child
// process on Linux, and a Docker-backed k3s container under Docker
// Desktop's Linux VM on macOS and Windows. Both launchers run the same
// pinned k3s version with the same cluster-disabling flag set, so the
// embedded cluster is the same k3s distribution on every host.
//
// New selects the launcher by host operating system and Docker
// availability. SupportedPlatform reports Linux as supported
// unconditionally, and macOS and Windows as supported when the docker
// CLI is on PATH (Docker Desktop supplies the Linux VM the embedded k3s
// runs in); a non-Linux host without Docker is unsupported. On an
// unsupported host the stack routes around the absent cluster: the
// embedded Postgres, Redis, OIDC, TLS, and gateway components still come
// up so a developer can exercise the storage and identity paths.
//
// The package compiles on every target OS of Embedded Mode (Linux,
// macOS, and Windows). The Linux child-process launcher is build-tagged
// unix-only (childk3s_unix.go); its OS-specific process-group start and
// tree termination live in the unix process-control substrate
// (process_unix.go). On macOS and Windows the substrate is a container,
// so the Docker-backed launcher (dockerk3s.go) manages it through the
// docker CLI rather than host process groups. The Docker-backed launcher
// and the launcher selection (launcher.go) compile on every OS; the
// off-unix child stub (childk3s_other.go) keeps New's reference to the
// child launcher resolvable on a non-unix build.
//
// spec: §17.4 (Embedded Mode runs the production stack on a host; the
// embedded Kubernetes substrate is provisioned per host operating
// system, as a managed child process on Linux and a Docker-backed
// container on macOS and Windows), §24.19 (lenny up/down manage the
// substrate).
package k3s
