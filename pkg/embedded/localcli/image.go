// SPDX-License-Identifier: MIT

package localcli

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"

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
//
// The image-bridge primitives (ctr-invocation resolution, the tarball and
// host-daemon import paths) live in pkg/embedded/stack so the Embedded Mode
// bring-up can import the seeded echo image at `lenny up`; the dispatch
// here threads CLI argument parsing and the §24.19.1 exit codes onto them.
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

	ctr, code := stack.CtrCommand(stderr)
	if code != 0 {
		return code
	}

	if file != "" {
		return stack.ImportFromFile(ctr, namespace, reference, file, stdout, stderr)
	}
	return stack.ImportFromHostDaemon(ctr, namespace, reference, stdout, stderr)
}

// cmdImageList implements `lenny image list`.
func cmdImageList(args []string, stdout, stderr io.Writer) int {
	namespace := namespaceFlag(args, "k8s.io")
	ctr, code := stack.CtrCommand(stderr)
	if code != 0 {
		return code
	}
	if err := stack.RunStreamed(stdout, stderr, nil, ctr.Binary,
		ctr.Args(namespace, false, "images", "ls")...); err != nil {
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
	ctr, code := stack.CtrCommand(stderr)
	if code != 0 {
		return code
	}
	// spec: §24.19.1 line 278 — capture ctr stderr so we can recognise
	// the containerd "image in use" case and surface an actionable
	// diagnostic instead of the raw ctr error.
	var ctrErr bytes.Buffer
	if err := stack.RunStreamed(stdout, &ctrErr, nil, ctr.Binary,
		ctr.Args(namespace, false, "images", "rm", reference)...); err != nil {
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
