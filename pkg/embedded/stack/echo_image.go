// SPDX-License-Identifier: MIT

package stack

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// echoImageNamespace is the containerd namespace the embedded k3s kubelet
// resolves pod images from. The bring-up imports the echo image into this
// namespace so the digest-pinned Sandbox pod reference resolves locally
// with the default IfNotPresent pull policy. spec: §24.19.1 (the image
// bridge reaches the embedded containerd image store).
const echoImageNamespace = "k8s.io"

// defaultEchoTarballName is the file name of the pre-built echo-embedded
// docker-save tarball shipped alongside the lenny binary. The bring-up
// imports it into the embedded containerd through the registry-free
// --file path. The path is overridable so an operator can point the
// bring-up at a tarball staged elsewhere (LENNY_ECHO_TARBALL); see
// resolveEchoTarball.
const defaultEchoTarballName = "runtime-echo-embedded.tar"

// digestPattern matches the 64-hex content digest containerd reports for
// an imported image. The resolved digest is built into the
// echoImageRepository@sha256:<digest> reference the Runtime CRD pattern
// (pkg/apis/lenny/v1alpha1/runtime_types.go) and the digest-pinned pod
// pull both require.
var digestPattern = regexp.MustCompile(`sha256:[A-Fa-f0-9]{64}`)

// ctrImagesLs runs `ctr images ls` against the embedded containerd and
// captures its stdout. It is a package-level var so a unit test can
// substitute the containerd interaction and exercise the digest-parsing
// and tag-registration logic without a live ctr. spec: §24.19.1 (the
// image bridge reaches the embedded containerd image store).
var ctrImagesLs = func(ctr CtrInvocation, namespace string, stderr io.Writer) (string, error) {
	var stdout bytes.Buffer
	if err := RunStreamed(&stdout, stderr, nil, ctr.Binary,
		ctr.Args(namespace, false, "images", "ls")...); err != nil {
		return "", fmt.Errorf("ctr images ls: %w", err)
	}
	return stdout.String(), nil
}

// ctrImagesTag registers an additional name for an image already present
// in the embedded containerd. The bring-up tags the imported tarball's
// image under its digest-pinned echoImageRepository@sha256:<digest> name
// so the kubelet resolves the digest-pinned Sandbox pod reference from the
// local store without a registry pull. It is a package-level var so a unit
// test can assert the tag argv without a live ctr. spec: §24.19.1.
var ctrImagesTag = func(ctr CtrInvocation, namespace, source, target string, stderr io.Writer) error {
	if err := RunStreamed(io.Discard, stderr, nil, ctr.Binary,
		ctr.Args(namespace, false, "images", "tag", "--force", source, target)...); err != nil {
		return fmt.Errorf("ctr images tag %s %s: %w", source, target, err)
	}
	return nil
}

// resolveEchoTarball locates the pre-built echo-embedded docker-save
// tarball the bring-up imports. An explicit path (the LENNY_ECHO_TARBALL
// override threaded through Config.EchoTarball) is used as given. Otherwise
// the search looks alongside the running lenny binary and in the current
// working directory for defaultEchoTarballName, mirroring resolveBin. The
// tarball ships with the lenny binary, so the binary-relative location is
// the default; the override is the operator-tunable escape hatch the
// non-spec default-path rule requires.
//
// spec: §24.19.1 (the --file import path), §17.4 (Embedded Mode bring-up).
func resolveEchoTarball(explicit string) (string, error) {
	if explicit != "" {
		if fi, err := os.Stat(explicit); err == nil && !fi.IsDir() {
			return explicit, nil
		}
		return "", fmt.Errorf("embedded: echo runtime tarball %q not found", explicit)
	}
	if self, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(self), defaultEchoTarballName)
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return cand, nil
		}
	}
	if wd, err := os.Getwd(); err == nil {
		cand := filepath.Join(wd, defaultEchoTarballName)
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return cand, nil
		}
	}
	return "", fmt.Errorf("embedded: echo runtime tarball %q not found alongside lenny "+
		"or in the working directory; ship it with the binary or set LENNY_ECHO_TARBALL", defaultEchoTarballName)
}

// importEchoImage imports the pre-built echo-embedded tarball into the
// embedded containerd and returns the digest-pinned image reference the
// bootstrap seed and the Runtime CRD use. It performs the §24.19.1 --file
// import (substrate-aware via ctr), resolves the imported image's content
// digest from `ctr images ls` (the containerd import does not go through
// the host Docker daemon, so the digest is resolved from ctr output rather
// than `docker image inspect`), and registers the digest-pinned
// echoImageRepository@sha256:<digest> name in containerd so the kubelet
// resolves the Sandbox pod's digest-pinned reference from the local store
// under the default IfNotPresent pull policy. Returning the resolved
// reference keeps the seeded digest, the CRD digest, and the digest of the
// image present in containerd identical, which the digest-pinned pull
// requires.
//
// It fails closed: an absent tarball, an unreachable containerd, a failed
// import, or an unresolvable digest each return an error, leaving the
// caller to keep the gateway on the in-process echo fallback rather than
// activate placement against a missing image.
//
// spec: §24.19.1 (the --file import path), §17.4 (Embedded Mode bring-up
// per host operating system), §4.7 (the digest-pinned embedded pod image).
func importEchoImage(ctr CtrInvocation, tarball string, stdout, stderr io.Writer) (string, error) {
	if code := ImportFromFile(ctr, echoImageNamespace, echoImageRepository, tarball, stdout, stderr); code != 0 {
		return "", fmt.Errorf("import echo runtime image from %s: ctr exit %d", tarball, code)
	}
	digest, err := resolveImportedDigest(ctr, echoImageNamespace, echoImageRepository, stderr)
	if err != nil {
		return "", fmt.Errorf("resolve imported echo runtime image digest: %w", err)
	}
	reference := echoImageRepository + "@" + digest
	if err := ctrImagesTag(ctr, echoImageNamespace, echoImageRepository+":dev", reference, stderr); err != nil {
		// The tarball's own tag is the build tag (Makefile builds
		// ghcr.io/lennylabs/runtime-echo-embedded:dev). Tag the imported
		// image under its digest-pinned name so the kubelet resolves the
		// Sandbox pod reference locally. A tag failure means the
		// digest-pinned pod reference would ImagePullBackOff, so it is fatal.
		return "", fmt.Errorf("register digest-pinned echo runtime reference %s: %w", reference, err)
	}
	return reference, nil
}

// resolveImportedDigest reads the content digest containerd recorded for
// the image imported under repository. The containerd import does not pass
// through the host Docker daemon, so the digest is resolved from `ctr
// images ls` output (mirroring the Kind infra's resolve_digest intent,
// which reads docker's .Id only because the Kind images are loaded through
// the docker daemon). `ctr images ls` prints one row per image as
// `REF TYPE DIGEST ...`; the function finds the row whose REF names
// repository (under any tag) and returns its DIGEST. It fails closed when
// no row names repository or the row carries no sha256 digest.
//
// spec: §24.19.1 (the image bridge reaches the embedded containerd image
// store), §4.7 (the digest-pinned embedded pod image).
func resolveImportedDigest(ctr CtrInvocation, namespace, repository string, stderr io.Writer) (string, error) {
	out, err := ctrImagesLs(ctr, namespace, stderr)
	if err != nil {
		return "", err
	}
	digest, ok := parseImageDigest(out, repository)
	if !ok {
		return "", fmt.Errorf("no image named %s in containerd namespace %s after import", repository, namespace)
	}
	return digest, nil
}

// parseImageDigest scans `ctr images ls` output for the row whose
// reference names repository and returns the sha256 content digest in that
// row. The reference matches when its repository part (the segment before
// the first `:` tag or `@` digest) equals repository, so a tagged
// (repository:dev) or digest-pinned (repository@sha256:...) row both
// match. The header row and rows for other repositories are skipped. It
// returns ok=false when no matching row carries a digest.
func parseImageDigest(lsOutput, repository string) (string, bool) {
	scanner := bufio.NewScanner(strings.NewReader(lsOutput))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		ref := fields[0]
		if ref == "REF" {
			continue // the `ctr images ls` header row.
		}
		if imageRepository(ref) != repository {
			continue
		}
		if digest := digestPattern.FindString(scanner.Text()); digest != "" {
			return digest, true
		}
	}
	return "", false
}

// imageRepository returns the repository portion of a containerd image
// reference, stripping the `:tag` suffix and the `@sha256:...` digest
// suffix. A digest reference (repository@sha256:...) and a tag reference
// (repository:tag) both reduce to repository. A registry host with a port
// (host:5000/repo) keeps its port because the split is on the last
// path-relative tag separator, not any colon.
func imageRepository(ref string) string {
	if at := strings.Index(ref, "@"); at >= 0 {
		ref = ref[:at]
	}
	// A tag separator is a colon after the final path slash; a colon in a
	// registry host:port appears before that slash and is preserved.
	slash := strings.LastIndex(ref, "/")
	if colon := strings.LastIndex(ref, ":"); colon > slash {
		ref = ref[:colon]
	}
	return ref
}
