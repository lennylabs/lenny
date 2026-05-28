// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/lennylabs/lenny/pkg/embedded/stack"
)

// Exit codes for `lenny image` (§24.19.1).
const (
	exitInvalidImageRef = 2
	exitK3sUnavailable  = 4
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
		if err := runStreamed(stdout, stderr, nil, ctr.binary,
			append(ctr.baseArgs(namespace), "images", "import", file)...); err != nil {
			fmt.Fprintf(stderr, "lenny image import: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "imported %s from %s into containerd namespace %s\n", reference, file, namespace)
		return 0
	}

	// Stream the image out of the host Docker daemon and into
	// containerd: `docker save <ref>` piped to `ctr images import -`.
	// Resolve `docker` on PATH up front so a missing binary surfaces a
	// caller-facing diagnostic that points at the `--file <tar>`
	// fallback rather than the raw `exec: docker: executable file not
	// found in PATH` from os/exec.
	//
	// spec: §17.4 line 290, §24.19.1 line 274.
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
		append(ctr.baseArgs(namespace), "images", "import", "-")...); err != nil {
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
		append(ctr.baseArgs(namespace), "images", "ls")...); err != nil {
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
		append(ctr.baseArgs(namespace), "images", "rm", reference)...); err != nil {
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

// ctrInvocation locates the embedded k3s ctr client and its containerd
// socket.
type ctrInvocation struct {
	binary string
	socket string
}

// baseArgs returns the k3s-ctr argument prefix for a namespace.
func (c ctrInvocation) baseArgs(namespace string) []string {
	return []string{"ctr", "--address", c.socket, "--namespace", namespace}
}

// ctrCommand resolves the embedded k3s binary and the containerd
// socket the running embedded stack exposes. It returns a non-zero
// exit code when the embedded stack is not reachable: §24.19.1 maps an
// unreachable containerd socket to K3S_UNAVAILABLE.
func ctrCommand(stderr io.Writer) (ctrInvocation, int) {
	root, err := stack.DefaultRoot()
	if err != nil {
		fmt.Fprintf(stderr, "lenny image: %v\n", err)
		return ctrInvocation{}, 1
	}
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
