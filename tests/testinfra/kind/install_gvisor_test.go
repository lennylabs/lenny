// SPDX-License-Identifier: MIT

package kind

import (
	"archive/tar"
	"bytes"
	"crypto/sha512"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The tests in this file check the release download of
// install-gvisor.sh, a test-infrastructure script. No specification
// section states how the Kind fixture fetches gVisor, so they carry no
// spec annotation.

// gvisorFetchSection returns the part of install-gvisor.sh between its
// gvisor_fetch markers: the download, checksum, and extraction helpers.
func gvisorFetchSection(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("install-gvisor.sh")
	if err != nil {
		t.Fatalf("read install-gvisor.sh: %v", err)
	}
	s := string(b)
	start := strings.Index(s, "# begin gvisor_fetch")
	stop := strings.Index(s, "# end gvisor_fetch")
	if start < 0 || stop < 0 || stop < start {
		t.Fatalf("install-gvisor.sh: gvisor_fetch section not found (start=%d end=%d)", start, stop)
	}
	return s[start:stop]
}

// gvisorArchiveEntry is one member of a fixture release archive.
type gvisorArchiveEntry struct {
	name string
	dir  bool
	body string
}

// gvisorFixtureArchive writes a bzip2-compressed tar holding entries to
// path. The tar is built with archive/tar so a member name such as
// "../x" is written verbatim, and compressed with the bzip2 command,
// because the Go standard library only decompresses bzip2.
func gvisorFixtureArchive(t *testing.T, path string, entries []gvisorArchiveEntry) {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: 0o755, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		if e.dir {
			h.Typeflag, h.Size = tar.TypeDir, 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if !e.dir {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bzip2", "-c")
	cmd.Stdin = &buf
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("bzip2: %v", err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

// gvisorSHA512Line returns a checksum line in the layout the release
// bucket publishes: the hex digest, two spaces, and the file name.
func gvisorSHA512Line(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha512.Sum512(b)
	return hex.EncodeToString(sum[:]) + "  gvisor.tar.bz2\n"
}

// gvisorCompleteRelease is the member list of a release archive as the
// bucket publishes it: runsc, the containerd shim, and gvisor-bin/.
var gvisorCompleteRelease = []gvisorArchiveEntry{
	{name: "gvisor-bin/", dir: true},
	{name: "runsc", body: "#!/bin/sh\necho runsc version release-test\n"},
	{name: "containerd-shim-runsc-v1", body: "#!/bin/sh\n"},
	{name: "gvisor-bin/gvisor_sentry", body: "sentry"},
}

// gvisorCurlStub serves files from $STUB_SERVE by the basename of the
// requested URL, answers a missing file the way curl -f answers an HTTP
// 404 (exit 22), and logs each requested URL to $STUB_LOG.
const gvisorCurlStub = `#!/usr/bin/env bash
out="" url=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    -m) shift 2 ;;
    -*) shift ;;
    *) url="$1"; shift ;;
  esac
done
echo "${url}" >> "${STUB_LOG}"
src="${STUB_SERVE}/$(basename "${url}")"
[[ -f "${src}" ]] || { echo "curl: (22) The requested URL returned error: 404" >&2; exit 22; }
cp "${src}" "${out}"
`

// runGvisorFetch runs fetch_gvisor_release for aarch64 under bash with
// the curl stub serving serve, and returns the combined output, whether
// the call succeeded, and the URLs the stub was asked for.
func runGvisorFetch(t *testing.T, section, dir, serve string) (string, bool, []string) {
	t.Helper()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "curl"), []byte(gvisorCurlStub), 0o755); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(dir, "calls")
	_ = os.Remove(calls)
	script := "set -euo pipefail\n" +
		"log() { printf '==> %s\\n' \"$*\" >&2; }\n" +
		"GVISOR_VERSION=20261116\n" +
		"CACHE_DIR=" + filepath.Join(dir, "cache", "20261116") + "\n" +
		section +
		"\ntree=\"$(fetch_gvisor_release aarch64)\"\necho \"FETCHED ${tree}\"\n"
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(
		os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"STUB_SERVE="+serve,
		"STUB_LOG="+calls,
	)
	out, err := cmd.CombinedOutput()
	var urls []string
	if b, rerr := os.ReadFile(calls); rerr == nil {
		urls = strings.Fields(string(b))
	}
	return string(out), err == nil && strings.Contains(string(out), "FETCHED "), urls
}

// TestGvisorFetchVerifiesTheArchiveAgainstThePublishedChecksum runs the
// fetch helpers of install-gvisor.sh against a stub curl. A release
// whose archive matches its published SHA-512 is extracted with runsc,
// the containerd shim, and gvisor-bin/, and a second call reuses the
// cache without downloading. A missing checksum file, a mismatched or
// malformed digest, a member outside the archive root, and an archive
// without the expected members each fail the install and leave no
// extracted tree behind.
func TestGvisorFetchVerifiesTheArchiveAgainstThePublishedChecksum(t *testing.T) {
	// bash and bzip2 are hard requirements: install-gvisor.sh is a bash
	// script and the release archive is bzip2-compressed.
	for _, tool := range []string{"bash", "bzip2", "tar"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("%s not on PATH: %v", tool, err)
		}
	}
	section := gvisorFetchSection(t)
	const wantArchiveURL = "https://storage.googleapis.com/gvisor/releases/release/20261116/aarch64/gvisor.tar.bz2"

	t.Run("matching checksum extracts and caches the release", func(t *testing.T) {
		dir := t.TempDir()
		serve := filepath.Join(dir, "serve")
		if err := os.MkdirAll(serve, 0o755); err != nil {
			t.Fatal(err)
		}
		archive := filepath.Join(serve, "gvisor.tar.bz2")
		gvisorFixtureArchive(t, archive, gvisorCompleteRelease)
		if err := os.WriteFile(archive+".sha512", []byte(gvisorSHA512Line(t, archive)), 0o644); err != nil {
			t.Fatal(err)
		}

		out, ok, urls := runGvisorFetch(t, section, dir, serve)
		if !ok {
			t.Fatalf("fetch failed, want success:\n%s", out)
		}
		if want := []string{wantArchiveURL + ".sha512", wantArchiveURL}; strings.Join(urls, " ") != strings.Join(want, " ") {
			t.Errorf("downloads = %q, want %q", urls, want)
		}
		tree := filepath.Join(dir, "cache", "20261116", "aarch64", "tree")
		if !strings.Contains(out, "FETCHED "+tree) {
			t.Errorf("output does not name the extracted tree %s:\n%s", tree, out)
		}
		for _, f := range []string{"runsc", "containerd-shim-runsc-v1", "gvisor-bin/gvisor_sentry"} {
			if _, err := os.Stat(filepath.Join(tree, f)); err != nil {
				t.Errorf("extracted tree lacks %s: %v", f, err)
			}
		}

		out, ok, urls = runGvisorFetch(t, section, dir, serve)
		if !ok {
			t.Fatalf("cached fetch failed, want success:\n%s", out)
		}
		if len(urls) != 0 {
			t.Errorf("cached fetch downloaded %q, want no download", urls)
		}

		// A cached archive that no longer matches its checksum is
		// discarded and fetched again.
		cached := filepath.Join(dir, "cache", "20261116", "aarch64", "gvisor.tar.bz2")
		if err := os.WriteFile(cached, []byte("corrupted"), 0o644); err != nil {
			t.Fatal(err)
		}
		out, ok, urls = runGvisorFetch(t, section, dir, serve)
		if !ok {
			t.Fatalf("fetch after cache corruption failed, want success:\n%s", out)
		}
		if len(urls) != 2 {
			t.Errorf("fetch after cache corruption downloaded %q, want the checksum and the archive", urls)
		}
	})

	for _, tc := range []struct {
		name    string
		entries []gvisorArchiveEntry
		sum     func(t *testing.T, archive string) string // "" serves no checksum file
		wantMsg string
	}{
		{
			name:    "missing checksum file",
			entries: gvisorCompleteRelease,
			sum:     func(*testing.T, string) string { return "" },
			wantMsg: "refusing to install an unverified archive",
		},
		{
			name:    "mismatched checksum",
			entries: gvisorCompleteRelease,
			sum: func(*testing.T, string) string {
				return strings.Repeat("0", 128) + "  gvisor.tar.bz2\n"
			},
			wantMsg: "SHA-512 mismatch",
		},
		{
			name:    "malformed checksum file",
			entries: gvisorCompleteRelease,
			sum:     func(*testing.T, string) string { return "<html>not found</html>\n" },
			wantMsg: "does not hold a SHA-512 digest",
		},
		{
			name:    "member outside the archive root",
			entries: append([]gvisorArchiveEntry{{name: "../escape", body: "x"}}, gvisorCompleteRelease...),
			sum:     gvisorSHA512Line,
			wantMsg: "has a member outside its root",
		},
		{
			name:    "archive without gvisor-bin",
			entries: gvisorCompleteRelease[1:3],
			sum:     gvisorSHA512Line,
			wantMsg: "lacks runsc, containerd-shim-runsc-v1, or gvisor-bin/",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			serve := filepath.Join(dir, "serve")
			if err := os.MkdirAll(serve, 0o755); err != nil {
				t.Fatal(err)
			}
			archive := filepath.Join(serve, "gvisor.tar.bz2")
			gvisorFixtureArchive(t, archive, tc.entries)
			if sum := tc.sum(t, archive); sum != "" {
				if err := os.WriteFile(archive+".sha512", []byte(sum), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			out, ok, urls := runGvisorFetch(t, section, dir, serve)
			if ok {
				t.Fatalf("fetch succeeded, want refusal:\n%s", out)
			}
			if !strings.Contains(out, tc.wantMsg) {
				t.Errorf("output lacks %q:\n%s", tc.wantMsg, out)
			}
			if tc.sum(t, archive) == "" && len(urls) != 1 {
				t.Errorf("downloads = %q, want only the checksum request", urls)
			}
			if _, err := os.Stat(filepath.Join(dir, "cache", "20261116", "aarch64", "tree")); err == nil {
				t.Errorf("a refused fetch left an extracted tree in the cache")
			}
		})
	}
}
