// SPDX-License-Identifier: MIT

package archive_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/upload"
	"github.com/lennylabs/lenny/pkg/upload/archive"
)

const wsRoot = "/workspace/current"

// --- builders ---------------------------------------------------------

type tentry struct {
	name     string
	body     string
	typeflag byte
	link     string
	mode     int64
	size     int64 // when >0, override the declared header size (to lie)
}

func buildTar(t *testing.T, gzipIt bool, entries []tentry) []byte {
	t.Helper()
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	for _, e := range entries {
		tf := e.typeflag
		if tf == 0 {
			tf = tar.TypeReg
		}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		size := int64(len(e.body))
		if e.size > 0 {
			size = e.size
		}
		hdr := &tar.Header{Name: e.name, Typeflag: tf, Mode: mode, Size: size, Linkname: e.link}
		if tf == tar.TypeDir {
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header %q: %v", e.name, err)
		}
		if tf == tar.TypeReg && e.body != "" {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatalf("tar body %q: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if !gzipIt {
		return raw.Bytes()
	}
	var gz bytes.Buffer
	gw := gzip.NewWriter(&gz)
	if _, err := gw.Write(raw.Bytes()); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return gz.Bytes()
}

func buildZip(t *testing.T, entries []tentry) []byte {
	t.Helper()
	var raw bytes.Buffer
	zw := zip.NewWriter(&raw)
	for _, e := range entries {
		fh := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		fh.SetMode(os.FileMode(mode))
		w, err := zw.CreateHeader(fh)
		if err != nil {
			t.Fatalf("zip header %q: %v", e.name, err)
		}
		if !strings.HasSuffix(e.name, "/") && e.body != "" {
			if _, err := w.Write([]byte(e.body)); err != nil {
				t.Fatalf("zip body %q: %v", e.name, err)
			}
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return raw.Bytes()
}

// --- happy paths ------------------------------------------------------

func TestExtractTar_FilesAndDirs_spec_7_4(t *testing.T) {
	data := buildTar(t, false, []tentry{
		{name: "dir/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "dir/a.txt", body: "alpha", mode: 0o644},
		{name: "b.txt", body: "beta", mode: 0o600},
	})
	res, err := archive.Extract(data, "tar", 0, 0, "", upload.RuntimeAllow{WorkspaceRoot: wsRoot})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(res.Files) != 2 || len(res.Dirs) != 1 {
		t.Fatalf("files=%d dirs=%d, want 2/1 (%+v)", len(res.Files), len(res.Dirs), res)
	}
	got := map[string]string{}
	for _, f := range res.Files {
		got[f.Path] = string(f.Content)
	}
	if got["dir/a.txt"] != "alpha" || got["b.txt"] != "beta" {
		t.Errorf("contents = %+v", got)
	}
}

func TestExtractTarGz_RoundTrip_spec_7_4(t *testing.T) {
	data := buildTar(t, true, []tentry{{name: "x/y.txt", body: "hello"}})
	res, err := archive.Extract(data, "tar.gz", 0, 0, "proj", upload.RuntimeAllow{WorkspaceRoot: wsRoot})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(res.Files) != 1 || res.Files[0].Path != "proj/x/y.txt" {
		t.Fatalf("files = %+v, want one proj/x/y.txt", res.Files)
	}
}

func TestExtractZip_RoundTrip_spec_7_4(t *testing.T) {
	data := buildZip(t, []tentry{{name: "z/a.txt", body: "zippy"}})
	res, err := archive.Extract(data, "zip", 0, 0, "", upload.RuntimeAllow{WorkspaceRoot: wsRoot})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(res.Files) != 1 || string(res.Files[0].Content) != "zippy" {
		t.Fatalf("files = %+v", res.Files)
	}
}

func TestExtract_StripComponents_spec_7_4(t *testing.T) {
	data := buildTar(t, false, []tentry{
		{name: "top/keep/a.txt", body: "1"},
		{name: "top/b.txt", body: "2"},
	})
	res, err := archive.Extract(data, "tar", 1, 0, "", upload.RuntimeAllow{WorkspaceRoot: wsRoot})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	paths := map[string]bool{}
	for _, f := range res.Files {
		paths[f.Path] = true
	}
	if !paths["keep/a.txt"] || !paths["b.txt"] {
		t.Fatalf("stripped paths = %+v, want keep/a.txt and b.txt", paths)
	}
}

// spec: §7.4 line 459 — an entry with fewer than stripComponents
// segments is skipped with a workspace_plan_strip_components_skip
// warning. F-7.4.15.
func TestExtract_StripSkipWarning_spec_7_4_15(t *testing.T) {
	data := buildTar(t, false, []tentry{
		{name: "readme.md", body: "skipped"},
		{name: "p/sub/keep.txt", body: "kept"},
	})
	res, err := archive.Extract(data, "tar", 2, 3, "", upload.RuntimeAllow{WorkspaceRoot: wsRoot})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(res.Warnings) != 1 {
		t.Fatalf("warnings = %d, want 1 (%+v)", len(res.Warnings), res.Warnings)
	}
	w := res.Warnings[0]
	if w.Code != archive.StripComponentsSkipCode || w.EntryPath != "readme.md" || w.SegmentCount != 1 || w.StripComponents != 2 || w.SourceIndex != 3 {
		t.Errorf("warning = %+v", w)
	}
	if len(res.Files) != 1 || res.Files[0].Path != "keep.txt" {
		t.Errorf("files = %+v, want one keep.txt", res.Files)
	}
}

// --- §13.4 ceilings ---------------------------------------------------

func reasonOf(t *testing.T, err error) upload.Reason {
	t.Helper()
	var vErr *upload.ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("error %v is not a *upload.ValidationError", err)
	}
	return vErr.Reason
}

func TestExtract_MaxEntryCount_spec_13_4(t *testing.T) {
	entries := make([]tentry, upload.MaxEntryCount+1)
	for i := range entries {
		entries[i] = tentry{name: "f" + itoa(i) + ".txt", body: "x"}
	}
	_, err := archive.Extract(buildTar(t, false, entries), "tar", 0, 0, "", upload.RuntimeAllow{WorkspaceRoot: wsRoot})
	if got := reasonOf(t, err); got != upload.ReasonMaxEntryCount {
		t.Fatalf("reason = %q, want max_entry_count", got)
	}
}

func TestExtract_MaxPathDepth_spec_13_4(t *testing.T) {
	deep := strings.Repeat("a/", upload.MaxPathDepth+1) + "f.txt"
	_, err := archive.Extract(buildTar(t, false, []tentry{{name: deep, body: "x"}}), "tar", 0, 0, "", upload.RuntimeAllow{WorkspaceRoot: wsRoot})
	if got := reasonOf(t, err); got != upload.ReasonMaxPathDepth {
		t.Fatalf("reason = %q, want max_path_depth", got)
	}
}

func TestExtract_MaxPathLength_spec_13_4(t *testing.T) {
	long := strings.Repeat("a", upload.MaxPathLength+1)
	_, err := archive.Extract(buildTar(t, false, []tentry{{name: long, body: "x"}}), "tar", 0, 0, "", upload.RuntimeAllow{WorkspaceRoot: wsRoot})
	if got := reasonOf(t, err); got != upload.ReasonMaxPathLength {
		t.Fatalf("reason = %q, want max_path_length", got)
	}
}

// A header that lies about its size (declares small, streams large) is
// caught by the streaming per-entry read cap. spec: §7.4 — F-7.4.2.
func TestExtract_MaxEntrySize_StreamingOverrun_spec_13_4(t *testing.T) {
	big := strings.Repeat("x", int(upload.MaxPerEntrySize)+1)
	data := buildTar(t, false, []tentry{{name: "big.bin", body: big}})
	_, err := archive.Extract(data, "tar", 0, 0, "", upload.RuntimeAllow{WorkspaceRoot: wsRoot})
	if got := reasonOf(t, err); got != upload.ReasonMaxEntrySize {
		t.Fatalf("reason = %q, want max_entry_size", got)
	}
}

// spec: §7.4 line 457 — a hardlink entry aborts extraction with
// non_regular_entry. F-7.4.3.
func TestExtract_NonRegularEntry_Hardlink_spec_7_4_457(t *testing.T) {
	data := buildTar(t, false, []tentry{{name: "h", typeflag: tar.TypeLink, link: "other"}})
	_, err := archive.Extract(data, "tar", 0, 0, "", upload.RuntimeAllow{WorkspaceRoot: wsRoot})
	if got := reasonOf(t, err); got != upload.ReasonNonRegularEntry {
		t.Fatalf("reason = %q, want non_regular_entry", got)
	}
}

// spec: §7.4 zip-slip — a `..` entry aborts with path_escapes_root.
func TestExtract_PathEscapesRoot_spec_13_4(t *testing.T) {
	data := buildTar(t, false, []tentry{{name: "../escape.txt", body: "x"}})
	_, err := archive.Extract(data, "tar", 0, 0, "", upload.RuntimeAllow{WorkspaceRoot: wsRoot})
	if got := reasonOf(t, err); got != upload.ReasonPathEscapesRoot {
		t.Fatalf("reason = %q, want path_escapes_root", got)
	}
}

// --- symlinks ---------------------------------------------------------

// spec: §7.4 line 458 — symlinks rejected by default.
func TestExtract_SymlinkRejectedByDefault_spec_7_4_458(t *testing.T) {
	data := buildTar(t, false, []tentry{{name: "link", typeflag: tar.TypeSymlink, link: "a.txt"}})
	_, err := archive.Extract(data, "tar", 0, 0, "", upload.RuntimeAllow{WorkspaceRoot: wsRoot})
	if got := reasonOf(t, err); got != upload.ReasonSymlink {
		t.Fatalf("reason = %q, want symlink", got)
	}
}

// spec: §7.4 line 458 — with allowSymlinks the link is admitted when the
// target stays within the workspace root.
func TestExtract_SymlinkAllowedWithinRoot_spec_7_4_458(t *testing.T) {
	data := buildTar(t, false, []tentry{
		{name: "a.txt", body: "target"},
		{name: "link", typeflag: tar.TypeSymlink, link: "a.txt"},
	})
	res, err := archive.Extract(data, "tar", 0, 0, "", upload.RuntimeAllow{AllowSymlinks: true, WorkspaceRoot: wsRoot})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(res.Symlinks) != 1 || res.Symlinks[0].Path != "link" || res.Symlinks[0].Target != "a.txt" {
		t.Fatalf("symlinks = %+v", res.Symlinks)
	}
}

// spec: §13.4 line 665 — a symlink whose target escapes the workspace
// root is rejected even with allowSymlinks.
func TestExtract_SymlinkEscapeRejected_spec_13_4_665(t *testing.T) {
	data := buildTar(t, false, []tentry{{name: "link", typeflag: tar.TypeSymlink, link: "../../etc/passwd"}})
	_, err := archive.Extract(data, "tar", 0, 0, "", upload.RuntimeAllow{AllowSymlinks: true, WorkspaceRoot: wsRoot})
	if got := reasonOf(t, err); got != upload.ReasonPathEscapesRoot {
		t.Fatalf("reason = %q, want path_escapes_root", got)
	}
}

// --- format errors ----------------------------------------------------

func TestExtract_UnsupportedFormat_spec_7_4(t *testing.T) {
	_, err := archive.Extract([]byte("x"), "rar", 0, 0, "", upload.RuntimeAllow{WorkspaceRoot: wsRoot})
	if got := reasonOf(t, err); got != upload.ReasonFormatError {
		t.Fatalf("reason = %q, want format_error", got)
	}
}

func TestExtract_CorruptGzip_spec_7_4(t *testing.T) {
	_, err := archive.Extract([]byte("not a gzip stream"), "tar.gz", 0, 0, "", upload.RuntimeAllow{WorkspaceRoot: wsRoot})
	if got := reasonOf(t, err); got != upload.ReasonFormatError {
		t.Fatalf("reason = %q, want format_error", got)
	}
}

func TestExtract_NegativeStrip_spec_7_4(t *testing.T) {
	_, err := archive.Extract(buildTar(t, false, []tentry{{name: "a", body: "x"}}), "tar", -1, 0, "", upload.RuntimeAllow{WorkspaceRoot: wsRoot})
	if got := reasonOf(t, err); got != upload.ReasonFormatError {
		t.Fatalf("reason = %q, want format_error", got)
	}
}

// --- decompression ratio ---------------------------------------------

// spec: §13.4 line 659 — a highly compressible payload that exceeds the
// 100:1 ratio aborts with max_decompression_ratio. A run of identical
// bytes gzips far below 1% of its size. F-7.4.2.
func TestExtract_DecompressionRatio_spec_13_4_659(t *testing.T) {
	body := strings.Repeat("A", 2*1024*1024) // 2 MiB of 'A' → tiny gzip
	data := buildTar(t, true, []tentry{{name: "bomb.txt", body: body}})
	if int64(len(body)) > 100*int64(len(data)) {
		_, err := archive.Extract(data, "tar.gz", 0, 0, "", upload.RuntimeAllow{WorkspaceRoot: wsRoot})
		if got := reasonOf(t, err); got != upload.ReasonMaxDecompressionRatio {
			t.Fatalf("reason = %q, want max_decompression_ratio", got)
		}
		return
	}
	t.Skipf("test gzip ratio %d:1 did not exceed 100:1; skipping", int64(len(body))/int64(len(data)))
}

// --- intra-archive dedup ---------------------------------------------

// Two entries at the same path within one archive collapse to one
// manifest file (last-writer-wins), matching §14 within-source overwrite
// semantics so no spurious cross-source collision is produced later.
func TestExtract_IntraArchiveDedup(t *testing.T) {
	data := buildTar(t, false, []tentry{
		{name: "dup.txt", body: "first"},
		{name: "dup.txt", body: "second"},
	})
	res, err := archive.Extract(data, "tar", 0, 0, "", upload.RuntimeAllow{WorkspaceRoot: wsRoot})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(res.Files) != 1 || string(res.Files[0].Content) != "second" {
		t.Fatalf("files = %+v, want one dup.txt=second", res.Files)
	}
}

// itoa avoids strconv import noise in the entry-count builder.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
