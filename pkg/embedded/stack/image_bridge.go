// SPDX-License-Identifier: MIT

package stack

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// ExitK3sUnavailable is the §24.19.1 line 282 K3S_UNAVAILABLE exit code the
// image bridge reports when the embedded containerd is unreachable. The
// `lenny image` CLI dispatch returns it directly; the bring-up import path
// converts it into an error. It is exported because the CLI dispatch in
// pkg/embedded/localcli maps it onto the same normative exit code.
//
// spec: §24.19.1 line 282 (K3S_UNAVAILABLE).
const ExitK3sUnavailable = 4

// containerCtrSocket is the containerd socket path inside the rancher/k3s
// container the Docker-backed launcher runs (macOS and Windows). k3s runs
// its bundled containerd there, so the bridge addresses it through this
// in-container path rather than the host data-directory socket the Linux
// child-process launcher exposes.
const containerCtrSocket = "/run/k3s/containerd/containerd.sock"

// CtrInvocation builds the argv that reaches the embedded k3s containerd
// `ctr` client. It is substrate-aware: on the Linux managed-child-process
// launcher it runs the host k3s binary against the host containerd socket;
// on the Docker-backed launcher (macOS and Windows) it runs the bundled
// `ctr` inside the k3s container via `docker exec`, because there is no host
// k3s binary or host containerd socket for a containerized k3s.
//
// It is exported because both the `lenny image` CLI dispatch and the
// Embedded Mode bring-up import resolve a CtrInvocation and run subcommands
// through it; the socket and container fields stay unexported because only
// the substrate-resolution constructors in this package set them.
//
// spec: §24.19.1 (the image bridge reaches the embedded containerd image
// store), §17.4 (the substrate is provisioned per host operating system).
type CtrInvocation struct {
	// Binary is the executable the bridge runs: the host k3s binary on the
	// Linux substrate, or `docker` on the Docker-backed substrate.
	Binary string
	// socket is the containerd socket address passed to `ctr --address`. It
	// is a host filesystem path on the Linux substrate and the in-container
	// socket path on the Docker-backed substrate.
	socket string
	// container, when non-empty, is the k3s container name the Docker-backed
	// launcher runs; the bridge addresses containerd through `docker exec`
	// into it. Empty selects the host-binary path.
	container string
}

// Args builds the full argv (after the binary) for a ctr subcommand under
// namespace. On the host path it is the bare `ctr` invocation against the
// host socket. On the Docker path it is `exec [-i] <container> ctr` against
// the in-container socket; stdin is requested (`exec -i`) only when the
// subcommand streams a tarball in (the `images import -` case). The
// in-container `ctr` is the rancher/k3s image's bundled `/bin/ctr`; the
// `k3s ctr` multicall form is not used because that build reports
// "No help topic for 'ctr'" when k3s is invoked by its own name rather than
// as the `ctr` symlink.
func (c CtrInvocation) Args(namespace string, stdin bool, sub ...string) []string {
	ctr := append([]string{"ctr", "--address", c.socket, "--namespace", namespace}, sub...)
	if c.container == "" {
		return ctr
	}
	exec := []string{"exec"}
	if stdin {
		exec = append(exec, "-i")
	}
	exec = append(exec, c.container)
	return append(exec, ctr...)
}

// ImportFromFile loads the image from a docker-save tarball into the
// embedded containerd store. On the Linux substrate the host `ctr` reads
// the host file directly (`ctr images import <file>`). On the Docker-backed
// substrate the in-container `ctr` cannot read a host path, so the host
// tarball is streamed into `docker exec -i <container> ctr images
// import -`.
//
// spec: §24.19.1 line 275 (the `--file <tar>` air-gapped/CI import path),
// §17.4 (the substrate is provisioned per host operating system).
func ImportFromFile(ctr CtrInvocation, namespace, reference, file string, stdout, stderr io.Writer) int {
	if ctr.container == "" {
		if err := RunStreamed(stdout, stderr, nil, ctr.Binary,
			ctr.Args(namespace, false, "images", "import", file)...); err != nil {
			fmt.Fprintf(stderr, "lenny image import: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "imported %s from %s into containerd namespace %s\n", reference, file, namespace)
		return 0
	}
	f, err := os.Open(file)
	if err != nil {
		fmt.Fprintf(stderr, "lenny image import: open %s: %v\n", file, err)
		return 1
	}
	defer f.Close()
	if err := RunStreamed(stdout, stderr, f, ctr.Binary,
		ctr.Args(namespace, true, "images", "import", "-")...); err != nil {
		fmt.Fprintf(stderr, "lenny image import: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "imported %s from %s into containerd namespace %s\n", reference, file, namespace)
	return 0
}

// ImportFromHostDaemon streams the image out of the host Docker daemon into
// the embedded containerd: `docker save <ref>` piped to `ctr images import
// -`. The `ctr` side is the host binary on the Linux substrate or `docker
// exec -i <container> ctr` on the Docker-backed substrate. `docker` is
// resolved on PATH up front so a missing binary surfaces a caller-facing
// diagnostic that points at the `--file <tar>` fallback rather than the raw
// `exec: docker: executable file not found in PATH` from os/exec.
//
// spec: §17.4 line 290, §24.19.1 line 274.
func ImportFromHostDaemon(ctr CtrInvocation, namespace, reference string, stdout, stderr io.Writer) int {
	if _, err := lookPathDocker(); err != nil {
		fmt.Fprintln(stderr, "lenny image import: the `docker` binary is required for the host-daemon path but is not on PATH;")
		fmt.Fprintln(stderr, "  either install Docker and rerun, or produce an OCI/Docker-format tarball with another tool")
		fmt.Fprintf(stderr, "  (`podman save -o image.tar %s`, `skopeo copy docker-daemon:%s oci-archive:image.tar`) and rerun with `--file image.tar`\n",
			reference, reference)
		return 1
	}
	save := exec.Command("docker", "save", reference)
	pipe, err := save.StdoutPipe()
	if err != nil {
		fmt.Fprintf(stderr, "lenny image import: %v\n", err)
		return 1
	}
	save.Stderr = stderr
	if err := save.Start(); err != nil {
		fmt.Fprintf(stderr, "lenny image import: docker save: %v\n", err)
		return 1
	}
	if err := RunStreamed(stdout, stderr, pipe, ctr.Binary,
		ctr.Args(namespace, true, "images", "import", "-")...); err != nil {
		_ = save.Wait()
		fmt.Fprintf(stderr, "lenny image import: %v\n", err)
		return 1
	}
	if err := save.Wait(); err != nil {
		fmt.Fprintf(stderr, "lenny image import: docker save: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "imported %s into containerd namespace %s\n", reference, namespace)
	return 0
}

// CtrCommand resolves how to reach the embedded k3s containerd from the
// running stack's recorded substrate. On the Linux managed-child-process
// launcher it returns the host k3s binary and the host containerd socket;
// on the Docker-backed launcher it returns a `docker exec` invocation into
// the k3s container. It returns a non-zero exit code when the embedded
// stack is not reachable: §24.19.1 maps an unreachable containerd to
// K3S_UNAVAILABLE. The decision fails closed — an absent stack, an absent
// host k3s, or a stopped Docker container each report K3S_UNAVAILABLE.
//
// spec: §24.19.1 line 282 (K3S_UNAVAILABLE), §17.4 (per-OS substrate).
func CtrCommand(stderr io.Writer) (CtrInvocation, int) {
	root, err := DefaultRoot()
	if err != nil {
		fmt.Fprintf(stderr, "lenny image: %v\n", err)
		return CtrInvocation{}, 1
	}
	sub, err := RunningSubstrate(root)
	if err != nil && !errors.Is(err, ErrNoRunningStack) {
		fmt.Fprintf(stderr, "lenny image: %v\n", err)
		return CtrInvocation{}, 1
	}
	// A recorded Docker-backed substrate (macOS and Windows) reaches
	// containerd by `docker exec` into the k3s container. Every other case —
	// a recorded Linux child-process substrate, or no recorded stack — uses
	// the host-binary path, which checks the host k3s binary and containerd
	// socket on disk and reports K3S_UNAVAILABLE when either is absent. The
	// host path stays byte-identical to its prior behavior on Linux, where
	// it does not depend on the state file.
	return CtrCommandForSubstrate(root, sub.Container, stderr)
}

// CtrCommandForSubstrate resolves how to reach the embedded k3s
// containerd from a live substrate handle, without reading the recorded
// stack state file. The Embedded Mode bring-up imports the seeded echo
// image before it writes the state file (writeState runs at the end of
// Up), so it cannot use CtrCommand, which resolves the substrate from the
// not-yet-written state. The container argument is the Docker-backed
// launcher's container name (empty on the Linux child-process launcher);
// it selects the same two reach paths CtrCommand selects. root is the
// resolved Embedded Mode state directory the host path locates the k3s
// binary and socket under. It fails closed identically: an absent host
// k3s, an absent socket, an absent docker binary, or a stopped container
// each report K3S_UNAVAILABLE.
//
// spec: §24.19.1 line 282 (K3S_UNAVAILABLE), §17.4 (per-OS substrate),
// §4.7 (the bring-up import targets the embedded containerd before the
// state file exists).
func CtrCommandForSubstrate(root, container string, stderr io.Writer) (CtrInvocation, int) {
	if container != "" {
		return dockerCtrCommand(container, stderr)
	}
	return hostCtrCommand(root, stderr)
}

// hostCtrCommand resolves the host k3s binary and the host containerd
// socket the Linux managed-child-process launcher exposes under the state
// directory. An absent binary or socket reports K3S_UNAVAILABLE.
func hostCtrCommand(root string, stderr io.Writer) (CtrInvocation, int) {
	paths := NewPaths(root)
	binary := filepath.Join(paths.K3s, "k3s")
	socket := filepath.Join(paths.K3s, "data", "agent", "containerd", "containerd.sock")
	if _, err := os.Stat(binary); err != nil {
		fmt.Fprintln(stderr, "lenny image: the embedded k3s is not present; run 'lenny up' first (K3S_UNAVAILABLE)")
		return CtrInvocation{}, ExitK3sUnavailable
	}
	if _, err := os.Stat(socket); err != nil {
		fmt.Fprintln(stderr, "lenny image: the embedded containerd socket is not reachable; "+
			"run 'lenny up' and retry once 'lenny status' reports the gateway healthy (K3S_UNAVAILABLE)")
		return CtrInvocation{}, ExitK3sUnavailable
	}
	return CtrInvocation{Binary: binary, socket: socket}, 0
}

// dockerCtrCommand resolves the `docker exec` invocation that reaches the
// embedded containerd inside the Docker-backed k3s container (macOS and
// Windows). It fails closed: an absent `docker` binary or a stopped k3s
// container reports K3S_UNAVAILABLE so the operator is pointed at the same
// 'lenny up'/'lenny status' recovery as the host path.
func dockerCtrCommand(container string, stderr io.Writer) (CtrInvocation, int) {
	if _, err := lookPathDocker(); err != nil {
		fmt.Fprintln(stderr, "lenny image: the embedded k3s runs under Docker on this host, but the `docker` "+
			"binary is not on PATH; install Docker Desktop and rerun (K3S_UNAVAILABLE)")
		return CtrInvocation{}, ExitK3sUnavailable
	}
	if !containerRunning(container) {
		fmt.Fprintln(stderr, "lenny image: the embedded k3s container is not running; "+
			"run 'lenny up' and retry once 'lenny status' reports the gateway healthy (K3S_UNAVAILABLE)")
		return CtrInvocation{}, ExitK3sUnavailable
	}
	return CtrInvocation{Binary: "docker", socket: containerCtrSocket, container: container}, 0
}

// RunStreamed runs name with args, wiring stdin (when non-nil), stdout,
// and stderr to the supplied writers.
func RunStreamed(stdout, stderr io.Writer, stdin io.Reader, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// lookPathDocker resolves the `docker` binary on PATH. It is overridden
// in tests so the host-daemon-missing path can be exercised without
// touching the real PATH.
var lookPathDocker = func() (string, error) {
	return exec.LookPath("docker")
}
