// SPDX-License-Identifier: MIT

package localcli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/lennylabs/lenny/pkg/embedded/k3s"
	"github.com/lennylabs/lenny/pkg/embedded/stack"
)

// imageRefPattern is a pragmatic OCI-reference check: a non-empty
// string of reference characters with no whitespace. The embedded
// containerd performs the authoritative parse; this rejects obvious
// malformed input before the subprocess runs.
var imageRefPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]*$`)

// cmdImage implements `lenny image` (§24.19.1): it bridges images
// between the host Docker daemon and the embedded k3s containerd image
// store so a locally built runtime image is referenceable without a
// remote registry. The subcommands are import, list, and rm.
func cmdImage(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny image: a subcommand is required: import, list, or rm")
		return 2
	}
	switch args[0] {
	case "import":
		return cmdImageImport(args[1:], stdout, stderr)
	case "list":
		return cmdImageList(args[1:], stdout, stderr)
	case "rm":
		return cmdImageRm(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "lenny image: unknown subcommand %q (want import, list, or rm)\n", args[0])
		return 2
	}
}

// cmdImageImport implements `lenny image import <reference>`. It loads
// the image from the host Docker daemon by default, or from a
// docker-save tarball when --file is given, into the embedded
// containerd store under the target namespace (default k8s.io).
func cmdImageImport(args []string, stdout, stderr io.Writer) int {
	var reference, file, namespace string
	namespace = "k8s.io"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--file":
			if i+1 < len(args) {
				file = args[i+1]
				i++
			}
		case "--namespace":
			if i+1 < len(args) {
				namespace = args[i+1]
				i++
			}
		default:
			if reference == "" && !strings.HasPrefix(args[i], "-") {
				reference = args[i]
			}
		}
	}
	if reference == "" {
		fmt.Fprintln(stderr, "lenny image import: a <reference> argument is required")
		return 2
	}
	if !imageRefPattern.MatchString(reference) {
		fmt.Fprintf(stderr, "lenny image import: %q is not a valid OCI reference (INVALID_IMAGE_REFERENCE)\n", reference)
		return exitInvalidImageRef
	}

	ctr, code := ctrCommand(stderr)
	if code != 0 {
		return code
	}

	if file != "" {
		return importFromFile(ctr, namespace, reference, file, stdout, stderr)
	}
	return importFromHostDaemon(ctr, namespace, reference, stdout, stderr)
}

// importFromFile loads the image from a docker-save tarball into the
// embedded containerd store. On the Linux substrate the host `ctr` reads
// the host file directly (`ctr images import <file>`). On the Docker-backed
// substrate the in-container `ctr` cannot read a host path, so the host
// tarball is streamed into `docker exec -i <container> k3s ctr images
// import -`.
//
// spec: §24.19.1 line 275 (the `--file <tar>` air-gapped/CI import path),
// §17.4 (the substrate is provisioned per host operating system).
func importFromFile(ctr ctrInvocation, namespace, reference, file string, stdout, stderr io.Writer) int {
	if ctr.container == "" {
		if err := runStreamed(stdout, stderr, nil, ctr.binary,
			ctr.args(namespace, false, "images", "import", file)...); err != nil {
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
	if err := runStreamed(stdout, stderr, f, ctr.binary,
		ctr.args(namespace, true, "images", "import", "-")...); err != nil {
		fmt.Fprintf(stderr, "lenny image import: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "imported %s from %s into containerd namespace %s\n", reference, file, namespace)
	return 0
}

// importFromHostDaemon streams the image out of the host Docker daemon into
// the embedded containerd: `docker save <ref>` piped to `ctr images import
// -`. The `ctr` side is the host binary on the Linux substrate or `docker
// exec -i <container> k3s ctr` on the Docker-backed substrate. `docker` is
// resolved on PATH up front so a missing binary surfaces a caller-facing
// diagnostic that points at the `--file <tar>` fallback rather than the raw
// `exec: docker: executable file not found in PATH` from os/exec.
//
// spec: §17.4 line 290, §24.19.1 line 274.
func importFromHostDaemon(ctr ctrInvocation, namespace, reference string, stdout, stderr io.Writer) int {
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
	if err := runStreamed(stdout, stderr, pipe, ctr.binary,
		ctr.args(namespace, true, "images", "import", "-")...); err != nil {
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

// cmdImageList implements `lenny image list`.
func cmdImageList(args []string, stdout, stderr io.Writer) int {
	namespace := namespaceFlag(args, "k8s.io")
	ctr, code := ctrCommand(stderr)
	if code != 0 {
		return code
	}
	if err := runStreamed(stdout, stderr, nil, ctr.binary,
		ctr.args(namespace, false, "images", "ls")...); err != nil {
		fmt.Fprintf(stderr, "lenny image list: %v\n", err)
		return 1
	}
	return 0
}

// cmdImageRm implements `lenny image rm <reference>`.
func cmdImageRm(args []string, stdout, stderr io.Writer) int {
	namespace := namespaceFlag(args, "k8s.io")
	var reference string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			reference = a
			break
		}
	}
	if reference == "" {
		fmt.Fprintln(stderr, "lenny image rm: a <reference> argument is required")
		return 2
	}
	if !imageRefPattern.MatchString(reference) {
		fmt.Fprintf(stderr, "lenny image rm: %q is not a valid OCI reference (INVALID_IMAGE_REFERENCE)\n", reference)
		return exitInvalidImageRef
	}
	ctr, code := ctrCommand(stderr)
	if code != 0 {
		return code
	}
	// spec: §24.19.1 line 278 — capture ctr stderr so we can recognise
	// the containerd "image in use" case and surface an actionable
	// diagnostic instead of the raw ctr error.
	var ctrErr bytes.Buffer
	if err := runStreamed(stdout, &ctrErr, nil, ctr.binary,
		ctr.args(namespace, false, "images", "rm", reference)...); err != nil {
		raw := ctrErr.String()
		stderr.Write([]byte(raw))
		if ref := imageInUseReference(raw); ref != "" {
			fmt.Fprintf(stderr, "lenny image rm: image %s is in use by %s; delete the consuming pod or snapshot first\n", reference, ref)
		} else if imageInUseError(raw) {
			fmt.Fprintf(stderr, "lenny image rm: image %s is in use by a running pod or snapshot; delete the consuming pod first\n", reference)
		} else {
			fmt.Fprintf(stderr, "lenny image rm: %v\n", err)
		}
		return 1
	}
	fmt.Fprintf(stdout, "removed %s from containerd namespace %s\n", reference, namespace)
	return 0
}

// imageInUseError reports whether the given ctr stderr indicates the
// "image is referenced/in use" failure (containerd refuses to remove
// an image while it backs a running container or snapshot).
//
// spec: §24.19.1 line 278.
func imageInUseError(raw string) bool {
	lower := strings.ToLower(raw)
	for _, marker := range []string{
		"is referenced by",
		"image is in use",
		"in use by",
		"in use:",
		"failed precondition",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// imageInUseReference extracts the referring container/snapshot name
// from a ctr "is referenced by" message when one is present; the empty
// string means containerd did not name a specific consumer.
//
// spec: §24.19.1 line 278.
func imageInUseReference(raw string) string {
	lower := strings.ToLower(raw)
	idx := strings.Index(lower, "referenced by")
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(raw[idx+len("referenced by"):])
	rest = strings.TrimPrefix(rest, ":")
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return ""
	}
	if nl := strings.IndexAny(rest, "\r\n"); nl >= 0 {
		rest = rest[:nl]
	}
	rest = strings.TrimSpace(rest)
	rest = strings.TrimRight(rest, ":,.;")
	return strings.TrimSpace(rest)
}

// containerCtrSocket is the containerd socket path inside the rancher/k3s
// container the Docker-backed launcher runs (macOS and Windows). k3s runs
// its bundled containerd there, so the bridge addresses it through this
// in-container path rather than the host data-directory socket the Linux
// child-process launcher exposes.
const containerCtrSocket = "/run/k3s/containerd/containerd.sock"

// ctrInvocation builds the argv that reaches the embedded k3s containerd
// `ctr` client. It is substrate-aware: on the Linux managed-child-process
// launcher it runs the host k3s binary against the host containerd socket;
// on the Docker-backed launcher (macOS and Windows) it runs `k3s ctr`
// inside the k3s container via `docker exec`, because there is no host k3s
// binary or host containerd socket for a containerized k3s.
//
// spec: §24.19.1 (the image bridge reaches the embedded containerd image
// store), §17.4 (the substrate is provisioned per host operating system).
type ctrInvocation struct {
	// binary is the executable the bridge runs: the host k3s binary on the
	// Linux substrate, or `docker` on the Docker-backed substrate.
	binary string
	// socket is the containerd socket address passed to `ctr --address`. It
	// is a host filesystem path on the Linux substrate and the in-container
	// socket path on the Docker-backed substrate.
	socket string
	// container, when non-empty, is the k3s container name the Docker-backed
	// launcher runs; the bridge addresses containerd through `docker exec`
	// into it. Empty selects the host-binary path.
	container string
}

// args builds the full argv (after the binary) for a ctr subcommand under
// namespace. On the host path it is the bare `ctr` invocation against the
// host socket. On the Docker path it is `exec [-i] <container> k3s ctr`
// against the in-container socket; stdin is requested (`exec -i`) only when
// the subcommand streams a tarball in (the `images import -` case).
func (c ctrInvocation) args(namespace string, stdin bool, sub ...string) []string {
	ctr := append([]string{"ctr", "--address", c.socket, "--namespace", namespace}, sub...)
	if c.container == "" {
		return ctr
	}
	exec := []string{"exec"}
	if stdin {
		exec = append(exec, "-i")
	}
	exec = append(exec, c.container, "k3s")
	return append(exec, ctr...)
}

// ctrCommand resolves how to reach the embedded k3s containerd from the
// running stack's recorded substrate. On the Linux managed-child-process
// launcher it returns the host k3s binary and the host containerd socket;
// on the Docker-backed launcher it returns a `docker exec` invocation into
// the k3s container. It returns a non-zero exit code when the embedded
// stack is not reachable: §24.19.1 maps an unreachable containerd to
// K3S_UNAVAILABLE. The decision fails closed — an absent stack, an absent
// host k3s, or a stopped Docker container each report K3S_UNAVAILABLE.
//
// spec: §24.19.1 line 282 (K3S_UNAVAILABLE), §17.4 (per-OS substrate).
func ctrCommand(stderr io.Writer) (ctrInvocation, int) {
	root, err := stack.DefaultRoot()
	if err != nil {
		fmt.Fprintf(stderr, "lenny image: %v\n", err)
		return ctrInvocation{}, 1
	}
	sub, err := stack.RunningSubstrate(root)
	if err != nil && !errors.Is(err, stack.ErrNoRunningStack) {
		fmt.Fprintf(stderr, "lenny image: %v\n", err)
		return ctrInvocation{}, 1
	}
	// A recorded Docker-backed substrate (macOS and Windows) reaches
	// containerd by `docker exec` into the k3s container. Every other case —
	// a recorded Linux child-process substrate, or no recorded stack — uses
	// the host-binary path, which checks the host k3s binary and containerd
	// socket on disk and reports K3S_UNAVAILABLE when either is absent. The
	// host path stays byte-identical to its prior behavior on Linux, where
	// it does not depend on the state file.
	if sub.DockerBacked() {
		return dockerCtrCommand(sub.Container, stderr)
	}
	return hostCtrCommand(root, stderr)
}

// hostCtrCommand resolves the host k3s binary and the host containerd
// socket the Linux managed-child-process launcher exposes under the state
// directory. An absent binary or socket reports K3S_UNAVAILABLE.
func hostCtrCommand(root string, stderr io.Writer) (ctrInvocation, int) {
	paths := stack.NewPaths(root)
	binary := filepath.Join(paths.K3s, "k3s")
	socket := filepath.Join(paths.K3s, "data", "agent", "containerd", "containerd.sock")
	if _, err := os.Stat(binary); err != nil {
		fmt.Fprintln(stderr, "lenny image: the embedded k3s is not present; run 'lenny up' first (K3S_UNAVAILABLE)")
		return ctrInvocation{}, exitK3sUnavailable
	}
	if _, err := os.Stat(socket); err != nil {
		fmt.Fprintln(stderr, "lenny image: the embedded containerd socket is not reachable; "+
			"run 'lenny up' and retry once 'lenny status' reports the gateway healthy (K3S_UNAVAILABLE)")
		return ctrInvocation{}, exitK3sUnavailable
	}
	return ctrInvocation{binary: binary, socket: socket}, 0
}

// dockerCtrCommand resolves the `docker exec` invocation that reaches the
// embedded containerd inside the Docker-backed k3s container (macOS and
// Windows). It fails closed: an absent `docker` binary or a stopped k3s
// container reports K3S_UNAVAILABLE so the operator is pointed at the same
// 'lenny up'/'lenny status' recovery as the host path.
func dockerCtrCommand(container string, stderr io.Writer) (ctrInvocation, int) {
	if _, err := lookPathDocker(); err != nil {
		fmt.Fprintln(stderr, "lenny image: the embedded k3s runs under Docker on this host, but the `docker` "+
			"binary is not on PATH; install Docker Desktop and rerun (K3S_UNAVAILABLE)")
		return ctrInvocation{}, exitK3sUnavailable
	}
	if !containerRunning(container) {
		fmt.Fprintln(stderr, "lenny image: the embedded k3s container is not running; "+
			"run 'lenny up' and retry once 'lenny status' reports the gateway healthy (K3S_UNAVAILABLE)")
		return ctrInvocation{}, exitK3sUnavailable
	}
	return ctrInvocation{binary: "docker", socket: containerCtrSocket, container: container}, 0
}

// containerRunning probes whether the Docker-backed k3s container is alive.
// It delegates to the k3s package's container probe and is a var so a unit
// test can substitute it without invoking a real docker.
var containerRunning = k3s.ContainerRunning

// namespaceFlag extracts --namespace from args, returning fallback
// when it is absent.
func namespaceFlag(args []string, fallback string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "--namespace" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return fallback
}

// runStreamed runs name with args, wiring stdin (when non-nil), stdout,
// and stderr to the supplied writers.
func runStreamed(stdout, stderr io.Writer, stdin io.Reader, name string, args ...string) error {
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
