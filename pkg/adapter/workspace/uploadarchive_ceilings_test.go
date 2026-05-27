// SPDX-License-Identifier: MIT

package workspace_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/upload"
)

// buildTarRaw produces a tar (or gzip-tar) archive from a sequence of
// fully-populated tar headers + bodies. Unlike buildTar in
// uploadarchive_test.go it surfaces the Typeflag so a test can plant a
// hardlink / FIFO / device / symlink entry directly. spec: §7.4 lines
// 457, 458 — F-7.4.3 / F-7.4.4.
func buildTarRaw(t *testing.T, gzipIt bool, entries []*tar.Header, bodies map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	var sink io.Writer = &buf
	var gz *gzip.Writer
	if gzipIt {
		gz = gzip.NewWriter(&buf)
		sink = gz
	}
	tw := tar.NewWriter(sink)
	for _, hdr := range entries {
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header %q: %v", hdr.Name, err)
		}
		if body, ok := bodies[hdr.Name]; ok {
			if _, err := tw.Write(body); err != nil {
				t.Fatalf("write tar body %q: %v", hdr.Name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if gzipIt {
		if err := gz.Close(); err != nil {
			t.Fatalf("close gzip: %v", err)
		}
	}
	return buf.Bytes()
}

func materializeWithPolicy(t *testing.T, format, prefix string, archive []byte, allowSymlinks bool) (string, error) {
	t.Helper()
	root := t.TempDir()
	staging := t.TempDir()
	stageUpload(t, staging, "arch", archive)
	src := &adapterv1.WorkspaceSource{
		Type:      "uploadArchive",
		Path:      prefix,
		UploadRef: "arch",
		Format:    format,
	}
	_, err := workspace.MaterializeWithPolicy(root, staging, []*adapterv1.WorkspaceSource{src}, workspace.ArchivePolicy{
		AllowSymlinks: allowSymlinks,
		WorkspaceRoot: root,
	})
	return root, err
}

func mustExtractionError(t *testing.T, err error, want upload.Reason) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected extraction error %q, got nil", want)
	}
	var ve *upload.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("error %v does not unwrap to *upload.ValidationError", err)
	}
	if ve.Reason != want {
		t.Fatalf("ValidationError.Reason = %q, want %q (err=%v)", ve.Reason, want, err)
	}
}

// TestExtractionRejectsHardlinkEntry covers F-7.4.3: a tar TypeLink
// (hardlink) aborts extraction with details.reason = "non_regular_entry"
// per §7.4 line 457.
func TestExtractionRejectsHardlinkEntry(t *testing.T) {
	archive := buildTarRaw(t, false, []*tar.Header{
		{Name: "anchor", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1},
		{Name: "shortcut", Typeflag: tar.TypeLink, Linkname: "anchor", Mode: 0o644},
	}, map[string][]byte{"anchor": []byte("x")})
	root, err := materializeWithPolicy(t, "tar", "", archive, false)
	mustExtractionError(t, err, upload.ReasonNonRegularEntry)
	// spec: §7.4 line 460 — F-7.4.13: pre-extracted "anchor" is rolled back.
	if _, err := os.Stat(filepath.Join(root, "anchor")); err == nil {
		t.Error("hardlink-abort should have rolled back the pre-extracted regular entry")
	}
}

// TestExtractionRejectsCharDeviceEntry covers F-7.4.3: a tar TypeChar
// aborts extraction with non_regular_entry per §7.4 line 457.
func TestExtractionRejectsCharDeviceEntry(t *testing.T) {
	archive := buildTarRaw(t, false, []*tar.Header{
		{Name: "tty", Typeflag: tar.TypeChar, Mode: 0o644, Devmajor: 5, Devminor: 0},
	}, nil)
	if _, err := materializeWithPolicy(t, "tar", "", archive, false); err == nil {
		t.Fatal("expected character-device entry to abort extraction")
	} else {
		mustExtractionError(t, err, upload.ReasonNonRegularEntry)
	}
}

// TestExtractionRejectsBlockDeviceEntry covers F-7.4.3 for TypeBlock.
func TestExtractionRejectsBlockDeviceEntry(t *testing.T) {
	archive := buildTarRaw(t, false, []*tar.Header{
		{Name: "sda", Typeflag: tar.TypeBlock, Mode: 0o644},
	}, nil)
	if _, err := materializeWithPolicy(t, "tar", "", archive, false); err == nil {
		t.Fatal("expected block-device entry to abort extraction")
	} else {
		mustExtractionError(t, err, upload.ReasonNonRegularEntry)
	}
}

// TestExtractionRejectsFIFOEntry covers F-7.4.3 for TypeFifo.
func TestExtractionRejectsFIFOEntry(t *testing.T) {
	archive := buildTarRaw(t, false, []*tar.Header{
		{Name: "pipe", Typeflag: tar.TypeFifo, Mode: 0o644},
	}, nil)
	if _, err := materializeWithPolicy(t, "tar", "", archive, false); err == nil {
		t.Fatal("expected FIFO entry to abort extraction")
	} else {
		mustExtractionError(t, err, upload.ReasonNonRegularEntry)
	}
}

// TestExtractionRejectsSymlinkByDefault covers F-7.4.4: with the
// platform default (allowSymlinks=false), a tar TypeSymlink aborts
// extraction with details.reason = "symlink" per §7.4 line 458.
func TestExtractionRejectsSymlinkByDefault(t *testing.T) {
	archive := buildTarRaw(t, false, []*tar.Header{
		{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "target.txt", Mode: 0o644},
	}, nil)
	if _, err := materializeWithPolicy(t, "tar", "", archive, false); err == nil {
		t.Fatal("expected symlink entry to abort under the default deny")
	} else {
		mustExtractionError(t, err, upload.ReasonSymlink)
	}
}

// TestExtractionAdmitsSymlinkWhenOptedIn covers F-7.4.4: with
// AllowSymlinks=true and an in-root relative target, the symlink is
// created on disk. spec: §7.4 line 458.
func TestExtractionAdmitsSymlinkWhenOptedIn(t *testing.T) {
	archive := buildTarRaw(t, false, []*tar.Header{
		{Name: "target.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 5},
		{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "target.txt", Mode: 0o644},
	}, map[string][]byte{"target.txt": []byte("hello")})
	root, err := materializeWithPolicy(t, "tar", "", archive, true)
	if err != nil {
		t.Fatalf("Materialize with AllowSymlinks=true returned %v", err)
	}
	info, err := os.Lstat(filepath.Join(root, "link"))
	if err != nil {
		t.Fatalf("Lstat link: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected the entry to be a symlink on disk")
	}
}

// TestExtractionRejectsSymlinkEscapingRoot covers F-7.4.4: even when
// AllowSymlinks=true, a symlink whose target canonicalizes outside the
// workspace root is rejected with details.reason = "path_escapes_root".
func TestExtractionRejectsSymlinkEscapingRoot(t *testing.T) {
	archive := buildTarRaw(t, false, []*tar.Header{
		{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "../../etc/passwd", Mode: 0o644},
	}, nil)
	if _, err := materializeWithPolicy(t, "tar", "", archive, true); err == nil {
		t.Fatal("expected symlink escaping root to be rejected even with AllowSymlinks=true")
	} else {
		mustExtractionError(t, err, upload.ReasonPathEscapesRoot)
	}
}

// TestExtractionRejectsSymlinkTraversingProc covers F-7.4.4: even when
// AllowSymlinks=true, a symlink pointing at /proc, /sys, /dev, or
// /run/lenny is rejected per §7.4 line 458.
func TestExtractionRejectsSymlinkTraversingProc(t *testing.T) {
	archive := buildTarRaw(t, false, []*tar.Header{
		{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/proc/self/environ", Mode: 0o644},
	}, nil)
	if _, err := materializeWithPolicy(t, "tar", "", archive, true); err == nil {
		t.Fatal("expected symlink to /proc to be rejected even with AllowSymlinks=true")
	} else {
		mustExtractionError(t, err, upload.ReasonPathEscapesRoot)
	}
}

// TestExtractionRejectsPerEntrySizeOverflow covers F-7.4.2: a single
// archive entry larger than upload.MaxPerEntrySize (64 MiB) aborts
// extraction with max_entry_size per §7.4 line 453. The test uses
// gzip-tar so the 64 MiB of NUL body compresses to a few KiB on disk;
// ValidateEntry rejects on the header's Size field before any body
// bytes are read.
func TestExtractionRejectsPerEntrySizeOverflow(t *testing.T) {
	body := bytes.Repeat([]byte{0}, int(upload.MaxPerEntrySize)+1)
	archive := buildTarRaw(t, true, []*tar.Header{
		{Name: "big.bin", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))},
	}, map[string][]byte{"big.bin": body})
	_, err := materializeWithPolicy(t, "tar.gz", "", archive, false)
	mustExtractionError(t, err, upload.ReasonMaxEntrySize)
}

// TestExtractionRejectsPathDepthOverflow covers F-7.4.2: an entry with
// a depth greater than upload.MaxPathDepth (32) aborts extraction with
// max_path_depth.
func TestExtractionRejectsPathDepthOverflow(t *testing.T) {
	deep := strings.Repeat("a/", upload.MaxPathDepth+1) + "leaf"
	archive := buildTarRaw(t, false, []*tar.Header{
		{Name: deep, Typeflag: tar.TypeReg, Mode: 0o644, Size: 0},
	}, nil)
	_, err := materializeWithPolicy(t, "tar", "", archive, false)
	mustExtractionError(t, err, upload.ReasonMaxPathDepth)
}

// TestExtractionRejectsPathLengthOverflow covers F-7.4.2 for the §13.4
// MaxPathLength (4096 bytes) check.
func TestExtractionRejectsPathLengthOverflow(t *testing.T) {
	// Build a path under 32 depth but over 4096 bytes total.
	long := strings.Repeat("a", upload.MaxPathLength+1)
	archive := buildTarRaw(t, false, []*tar.Header{
		{Name: long, Typeflag: tar.TypeReg, Mode: 0o644, Size: 0},
	}, nil)
	_, err := materializeWithPolicy(t, "tar", "", archive, false)
	mustExtractionError(t, err, upload.ReasonMaxPathLength)
}

// TestExtractionRejectsEntryCountOverflow covers F-7.4.2: an archive
// with more than upload.MaxEntryCount (10 000) entries aborts with
// max_entry_count per §7.4 line 452.
func TestExtractionRejectsEntryCountOverflow(t *testing.T) {
	entries := make([]*tar.Header, 0, upload.MaxEntryCount+1)
	for i := 0; i <= upload.MaxEntryCount; i++ {
		entries = append(entries, &tar.Header{
			Name:     "f" + strconvI(i),
			Typeflag: tar.TypeReg,
			Mode:     0o644,
			Size:     0,
		})
	}
	archive := buildTarRaw(t, false, entries, nil)
	_, err := materializeWithPolicy(t, "tar", "", archive, false)
	mustExtractionError(t, err, upload.ReasonMaxEntryCount)
}

// TestExtractionRejectsDecompressionRatioBomb covers F-7.4.2: a
// gzip-tar whose decompressed/compressed ratio exceeds 100:1 aborts
// with max_decompression_ratio per §7.4 line 451.
func TestExtractionRejectsDecompressionRatioBomb(t *testing.T) {
	// One entry whose body is a long run of NULs compresses massively
	// under gzip — typically 30 000:1 or higher. The exact ratio is
	// implementation-dependent; the test only requires that it crosses
	// the §13.4 100:1 ceiling.
	body := bytes.Repeat([]byte{0}, 200*1024) // 200 KiB of NULs
	archive := buildTarRaw(t, true, []*tar.Header{
		{Name: "zeros.bin", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))},
	}, map[string][]byte{"zeros.bin": body})
	_, err := materializeWithPolicy(t, "tar.gz", "", archive, false)
	mustExtractionError(t, err, upload.ReasonMaxDecompressionRatio)
}

// TestExtractionAtomicRollbackOnMidExtractionFailure covers F-7.4.13:
// when a later entry trips a §13.4 ceiling, the earlier admitted
// regular entries are removed from disk before the error returns.
func TestExtractionAtomicRollbackOnMidExtractionFailure(t *testing.T) {
	// Two regular entries, then a hardlink — the hardlink aborts and
	// the two regulars must be removed.
	archive := buildTarRaw(t, false, []*tar.Header{
		{Name: "good1.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 4},
		{Name: "good2.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 4},
		{Name: "bad", Typeflag: tar.TypeLink, Linkname: "good1.txt", Mode: 0o644},
	}, map[string][]byte{"good1.txt": []byte("aaaa"), "good2.txt": []byte("bbbb")})
	root, err := materializeWithPolicy(t, "tar", "", archive, false)
	mustExtractionError(t, err, upload.ReasonNonRegularEntry)
	for _, name := range []string{"good1.txt", "good2.txt"} {
		if _, statErr := os.Stat(filepath.Join(root, name)); statErr == nil {
			t.Errorf("partial entry %q survived the rollback", name)
		}
	}
}

// TestExtractionAtomicRollbackRemovesNewlyCreatedDirs covers F-7.4.13:
// directories the extractor created (as opposed to ones already on
// disk) are removed on rollback, leaving the staging directory in its
// pre-extraction state.
func TestExtractionAtomicRollbackRemovesNewlyCreatedDirs(t *testing.T) {
	archive := buildTarRaw(t, false, []*tar.Header{
		{Name: "a/b/c/leaf.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1},
		{Name: "fail", Typeflag: tar.TypeBlock, Mode: 0o644},
	}, map[string][]byte{"a/b/c/leaf.txt": []byte("x")})
	root, err := materializeWithPolicy(t, "tar", "", archive, false)
	mustExtractionError(t, err, upload.ReasonNonRegularEntry)
	if _, statErr := os.Stat(filepath.Join(root, "a")); statErr == nil {
		t.Error("the extractor-created `a/` directory survived the rollback")
	}
}

// TestExtractionAtomicRollbackPreservesPreexistingFiles covers F-7.4.13:
// files unrelated to the failed extraction that already existed in the
// destination are preserved through the rollback. spec: §7.4 line 460
// ("returned to its pre-extraction state").
func TestExtractionAtomicRollbackPreservesPreexistingFiles(t *testing.T) {
	root := t.TempDir()
	preexisting := filepath.Join(root, "kept.txt")
	if err := os.WriteFile(preexisting, []byte("untouched"), 0o644); err != nil {
		t.Fatalf("seed pre-existing file: %v", err)
	}
	staging := t.TempDir()
	stageUpload(t, staging, "arch", buildTarRaw(t, false, []*tar.Header{
		{Name: "x.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1},
		{Name: "fifo", Typeflag: tar.TypeFifo, Mode: 0o644},
	}, map[string][]byte{"x.txt": []byte("y")}))
	src := &adapterv1.WorkspaceSource{
		Type: "uploadArchive", UploadRef: "arch", Format: "tar",
	}
	_, err := workspace.MaterializeWithPolicy(root, staging, []*adapterv1.WorkspaceSource{src}, workspace.ArchivePolicy{WorkspaceRoot: root})
	mustExtractionError(t, err, upload.ReasonNonRegularEntry)
	if _, statErr := os.Stat(preexisting); statErr != nil {
		t.Errorf("pre-existing file was removed by the rollback: %v", statErr)
	}
}

// TestExtractionZipRejectsSymlinkByDefault covers F-7.4.4 over the zip
// format: a unix-mode symlink in the zip aborts unless opted in.
func TestExtractionZipRejectsSymlinkByDefault(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{Name: "link"}
	hdr.SetMode(os.ModeSymlink | 0o644)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatalf("create zip symlink header: %v", err)
	}
	if _, err := w.Write([]byte("target.txt")); err != nil {
		t.Fatalf("write zip symlink target: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	_, err = materializeWithPolicy(t, "zip", "", buf.Bytes(), false)
	mustExtractionError(t, err, upload.ReasonSymlink)
}

// TestExtractionZipAdmitsSymlinkWhenOptedIn covers F-7.4.4 over the
// zip format: AllowSymlinks=true + in-root target yields a symlink on
// disk.
func TestExtractionZipAdmitsSymlinkWhenOptedIn(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	regHdr := &zip.FileHeader{Name: "target.txt"}
	regHdr.SetMode(0o644)
	rw, err := zw.CreateHeader(regHdr)
	if err != nil {
		t.Fatalf("create zip regular header: %v", err)
	}
	if _, err := rw.Write([]byte("hi")); err != nil {
		t.Fatalf("write zip body: %v", err)
	}
	hdr := &zip.FileHeader{Name: "link"}
	hdr.SetMode(os.ModeSymlink | 0o644)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatalf("create zip symlink header: %v", err)
	}
	if _, err := w.Write([]byte("target.txt")); err != nil {
		t.Fatalf("write zip symlink target: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	root, err := materializeWithPolicy(t, "zip", "", buf.Bytes(), true)
	if err != nil {
		t.Fatalf("Materialize zip with AllowSymlinks: %v", err)
	}
	info, err := os.Lstat(filepath.Join(root, "link"))
	if err != nil {
		t.Fatalf("Lstat zip-extracted link: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("expected zip-extracted link to be a symlink on disk")
	}
}

// strconvI is an inline strconv.Itoa to avoid a fresh import in the
// per-test scope.
func strconvI(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
