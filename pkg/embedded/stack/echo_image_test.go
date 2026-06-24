// SPDX-License-Identifier: MIT

package stack

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// spec: §24.19.1 (the image bridge reaches the embedded containerd image
// store), §4.7 (the digest-pinned embedded pod image) — parseImageDigest
// returns the sha256 content digest of the `ctr images ls` row that names
// the echo repository, skipping the header row and rows for other
// repositories, so the bring-up resolves the imported image's digest from
// ctr output rather than `docker image inspect`.
func TestParseImageDigest_spec_24_19_1(t *testing.T) {
	const ls = `REF                                              TYPE                                                      DIGEST                                                                  SIZE      PLATFORMS
docker.io/library/busybox:latest                 application/vnd.oci.image.index.v1+json                   sha256:` + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + `   4.0 MiB   linux/amd64
ghcr.io/lennylabs/runtime-echo-embedded:dev      application/vnd.docker.distribution.manifest.v2+json      sha256:` + "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" + `   18 MiB    linux/amd64
`
	digest, ok := parseImageDigest(ls, echoImageRepository)
	if !ok {
		t.Fatalf("parseImageDigest did not find %s", echoImageRepository)
	}
	const want = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if digest != want {
		t.Errorf("digest = %q, want %q (the echo row's digest, not the busybox row's)", digest, want)
	}
}

// spec: §24.19.1, §4.7 — parseImageDigest reports ok=false when no row
// names the repository, so the bring-up fails closed rather than seeding a
// digest belonging to a different image.
func TestParseImageDigestNoMatch_spec_24_19_1(t *testing.T) {
	const ls = `REF                                TYPE      DIGEST                                                                  SIZE
docker.io/library/busybox:latest   index     sha256:` + "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc" + `   4 MiB
`
	if _, ok := parseImageDigest(ls, echoImageRepository); ok {
		t.Error("parseImageDigest matched a repository absent from the listing")
	}
}

// spec: §24.19.1 — imageRepository strips the tag and digest suffixes so a
// tagged (repo:dev) and a digest-pinned (repo@sha256:...) reference both
// reduce to the bare repository, and a registry host:port keeps its port.
func TestImageRepository_spec_24_19_1(t *testing.T) {
	cases := []struct {
		ref  string
		want string
	}{
		{"ghcr.io/lennylabs/runtime-echo-embedded:dev", "ghcr.io/lennylabs/runtime-echo-embedded"},
		{"ghcr.io/lennylabs/runtime-echo-embedded@sha256:" + strings.Repeat("a", 64), "ghcr.io/lennylabs/runtime-echo-embedded"},
		{"ghcr.io/lennylabs/runtime-echo-embedded", "ghcr.io/lennylabs/runtime-echo-embedded"},
		{"localhost:5000/team/img:v1", "localhost:5000/team/img"},
	}
	for _, c := range cases {
		if got := imageRepository(c.ref); got != c.want {
			t.Errorf("imageRepository(%q) = %q, want %q", c.ref, got, c.want)
		}
	}
}

// spec: §24.19.1 (the --file import path), §17.4 (Embedded Mode bring-up)
// — resolveEchoTarball honors an explicit override path and rejects a
// missing one, the operator-tunable escape hatch the non-spec default-path
// rule requires.
func TestResolveEchoTarballOverride_spec_17_4(t *testing.T) {
	dir := t.TempDir()
	tarball := filepath.Join(dir, "custom-echo.tar")
	if err := os.WriteFile(tarball, []byte("tar"), 0o644); err != nil {
		t.Fatalf("seed tarball: %v", err)
	}
	got, err := resolveEchoTarball(tarball)
	if err != nil {
		t.Fatalf("resolveEchoTarball(%q) error = %v", tarball, err)
	}
	if got != tarball {
		t.Errorf("resolveEchoTarball = %q, want the override %q", got, tarball)
	}

	if _, err := resolveEchoTarball(filepath.Join(dir, "missing.tar")); err == nil {
		t.Error("resolveEchoTarball with a missing override = nil error, want a not-found error")
	}
}

// spec: §24.19.1 (the --file import path), §17.4 — resolveEchoTarball
// discovers the tarball in the working directory under its default name
// when no override is given, mirroring the binary-relative discovery the
// shipped tarball uses.
func TestResolveEchoTarballDefaultDiscovery_spec_17_4(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, defaultEchoTarballName), []byte("tar"), 0o644); err != nil {
		t.Fatalf("seed tarball: %v", err)
	}
	prev, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	got, err := resolveEchoTarball("")
	if err != nil {
		t.Fatalf("resolveEchoTarball default discovery error = %v", err)
	}
	// The working directory may resolve through a symlink (macOS /var ->
	// /private/var), so compare the resolved location rather than the
	// literal temp-dir join.
	wantWd, _ := os.Getwd()
	if got != filepath.Join(wantWd, defaultEchoTarballName) {
		t.Errorf("resolveEchoTarball = %q, want %q (the working-dir default)",
			got, filepath.Join(wantWd, defaultEchoTarballName))
	}
	if filepath.Base(got) != defaultEchoTarballName {
		t.Errorf("resolveEchoTarball discovered %q, want the default tarball name", got)
	}
}

// spec: §24.19.1 (the --file import path), §4.7 (the digest-pinned
// embedded pod image) — importEchoImage imports the tarball, resolves the
// imported image's digest from ctr output, registers the digest-pinned
// reference in containerd, and returns echoImageRepository@sha256:<digest>,
// keeping the seeded digest identical to the image present in containerd.
func TestImportEchoImageResolvesDigest_spec_24_19_1(t *testing.T) {
	const digest = "sha256:" + "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	// A ctr stub binary that succeeds for the `images import` invocation so
	// ImportFromFile reports success on the host path.
	ctr := fakeCtrBinary(t)
	defer swapCtrSeams(t,
		func(CtrInvocation, string, io.Writer) (string, error) {
			return echoImageRepository + ":dev      manifest      " + digest + "   18 MiB\n", nil
		},
		func(_ CtrInvocation, _, source, target string, _ io.Writer) error {
			if source != echoImageRepository+":dev" {
				t.Errorf("tag source = %q, want the build-tag reference", source)
			}
			if target != echoImageRepository+"@"+digest {
				t.Errorf("tag target = %q, want the digest-pinned reference", target)
			}
			return nil
		})()

	tar := filepath.Join(t.TempDir(), "echo.tar")
	if err := os.WriteFile(tar, []byte("tar"), 0o644); err != nil {
		t.Fatalf("seed tarball: %v", err)
	}
	var out, errb bytes.Buffer
	ref, err := importEchoImage(CtrInvocation{Binary: ctr}, tar, &out, &errb)
	if err != nil {
		t.Fatalf("importEchoImage error = %v (stderr %q)", err, errb.String())
	}
	if ref != echoImageRepository+"@"+digest {
		t.Errorf("importEchoImage ref = %q, want %s@%s", ref, echoImageRepository, digest)
	}
}

// spec: §24.19.1, §4.7 — importEchoImage fails closed when the imported
// image has no resolvable digest in containerd, so the bring-up does not
// seed a runnable reference for an image that did not land.
func TestImportEchoImageMissingDigestFailsClosed_spec_24_19_1(t *testing.T) {
	ctr := fakeCtrBinary(t)
	defer swapCtrSeams(t,
		func(CtrInvocation, string, io.Writer) (string, error) {
			return "REF   TYPE   DIGEST   SIZE\n", nil // no echo row.
		},
		func(CtrInvocation, string, string, string, io.Writer) error {
			t.Error("ctrImagesTag ran despite an unresolvable digest")
			return nil
		})()

	tar := filepath.Join(t.TempDir(), "echo.tar")
	if err := os.WriteFile(tar, []byte("tar"), 0o644); err != nil {
		t.Fatalf("seed tarball: %v", err)
	}
	var out, errb bytes.Buffer
	if _, err := importEchoImage(CtrInvocation{Binary: ctr}, tar, &out, &errb); err == nil {
		t.Error("importEchoImage with no resolvable digest = nil error, want a failure")
	}
}

// spec: §24.19.1, §4.7 — a ctr tag failure is fatal because the
// digest-pinned pod reference would otherwise ImagePullBackOff, so
// importEchoImage returns an error rather than a reference containerd
// cannot resolve.
func TestImportEchoImageTagFailureFatal_spec_24_19_1(t *testing.T) {
	const digest = "sha256:" + "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	ctr := fakeCtrBinary(t)
	defer swapCtrSeams(t,
		func(CtrInvocation, string, io.Writer) (string, error) {
			return echoImageRepository + ":dev   manifest   " + digest + "   18 MiB\n", nil
		},
		func(CtrInvocation, string, string, string, io.Writer) error {
			return errors.New("tag boom")
		})()

	tar := filepath.Join(t.TempDir(), "echo.tar")
	if err := os.WriteFile(tar, []byte("tar"), 0o644); err != nil {
		t.Fatalf("seed tarball: %v", err)
	}
	var out, errb bytes.Buffer
	if _, err := importEchoImage(CtrInvocation{Binary: ctr}, tar, &out, &errb); err == nil {
		t.Error("importEchoImage with a tag failure = nil error, want a fatal failure")
	}
}

// spec: §24.19.1, §17.4, §4.7 — importEchoRuntimeImage returns the empty
// string (and warns) when the tarball is absent, so a missing image leaves
// the gateway on the in-process echo fallback rather than activating
// placement against a non-existent image.
func TestImportEchoRuntimeImageMissingTarballDegrades_spec_17_4(t *testing.T) {
	s := &Stack{}
	var out bytes.Buffer
	// An override pointing at a missing file short-circuits before any ctr
	// interaction, so no live containerd is needed.
	ref := s.importEchoRuntimeImage(t.TempDir(), filepath.Join(t.TempDir(), "missing.tar"), &out)
	if ref != "" {
		t.Errorf("importEchoRuntimeImage with a missing tarball = %q, want empty (degraded)", ref)
	}
	if !strings.Contains(out.String(), "not imported") {
		t.Errorf("output = %q, want the not-imported warning", out.String())
	}
}

// spec: §24.19.1 line 282 (K3S_UNAVAILABLE), §17.4, §4.7 —
// CtrCommandForSubstrate resolves the host path from a live substrate
// handle (empty container) without reading the recorded stack state, and
// fails closed with K3S_UNAVAILABLE when the host k3s artifacts are absent,
// so the bring-up import can run before the state file is written.
func TestCtrCommandForSubstrateHostUnavailable_spec_24_19_1(t *testing.T) {
	var errb bytes.Buffer
	_, code := CtrCommandForSubstrate(t.TempDir(), "", &errb)
	if code != ExitK3sUnavailable {
		t.Fatalf("host-absent exit = %d, want %d", code, ExitK3sUnavailable)
	}
	if !strings.Contains(errb.String(), "K3S_UNAVAILABLE") {
		t.Errorf("stderr = %q, want K3S_UNAVAILABLE", errb.String())
	}
}

// spec: §24.19.1, §17.4 — CtrCommandForSubstrate selects the Docker-backed
// path from a non-empty container handle and fails closed when docker is
// absent, mirroring the recorded-state CtrCommand without the state file.
func TestCtrCommandForSubstrateDockerMissingDocker_spec_24_19_1(t *testing.T) {
	origLook := lookPathDocker
	t.Cleanup(func() { lookPathDocker = origLook })
	lookPathDocker = func() (string, error) {
		return "", errors.New("exec: \"docker\": executable file not found in $PATH")
	}
	var errb bytes.Buffer
	_, code := CtrCommandForSubstrate(t.TempDir(), "lenny-embedded-k3s-x", &errb)
	if code != ExitK3sUnavailable {
		t.Fatalf("docker-missing exit = %d, want %d", code, ExitK3sUnavailable)
	}
	if got := errb.String(); !strings.Contains(got, "K3S_UNAVAILABLE") || !strings.Contains(got, "Docker Desktop") {
		t.Errorf("stderr = %q, want K3S_UNAVAILABLE + Docker Desktop guidance", got)
	}
}

// swapCtrSeams substitutes the ctrImagesLs and ctrImagesTag package vars
// for the duration of a test, returning a restore func. It lets a unit
// exercise importEchoImage's digest-resolution and tag-registration logic
// without a live ctr.
func swapCtrSeams(t *testing.T,
	ls func(CtrInvocation, string, io.Writer) (string, error),
	tag func(CtrInvocation, string, string, string, io.Writer) error,
) func() {
	t.Helper()
	prevLs, prevTag := ctrImagesLs, ctrImagesTag
	ctrImagesLs, ctrImagesTag = ls, tag
	return func() { ctrImagesLs, ctrImagesTag = prevLs, prevTag }
}

// fakeCtrBinary writes a trivial executable that exits 0, so ImportFromFile
// (which shells out to ctr on the host path) reports success without a real
// containerd. The digest-resolution and tag steps are exercised through the
// injected ctrImagesLs/ctrImagesTag seams.
func fakeCtrBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "ctr")
	script := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ctr: %v", err)
	}
	return bin
}
