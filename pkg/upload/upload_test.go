// SPDX-License-Identifier: MIT

package upload

import (
	"errors"
	"strings"
	"testing"
)

func TestNormativeCeilingsMatchSpec(t *testing.T) {
	// §13.4 normative values; these are platform ceilings.
	cases := map[string]struct {
		got, want any
	}{
		"MaxDecompressedSize":   {int64(MaxDecompressedSize), int64(256 * 1024 * 1024)},
		"MaxDecompressionRatio": {int64(MaxDecompressionRatio), int64(100)},
		"MaxEntryCount":         {MaxEntryCount, 10_000},
		"MaxPerEntrySize":       {int64(MaxPerEntrySize), int64(64 * 1024 * 1024)},
		"MaxPathDepth":          {MaxPathDepth, 32},
		"MaxPathLength":         {MaxPathLength, 4096},
	}
	for name, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: want %v, got %v (§13.4 ceiling)", name, c.want, c.got)
		}
	}
}

func TestForbiddenEntryKinds(t *testing.T) {
	forbidden := []EntryKind{KindHardlink, KindCharDevice, KindBlockDevice, KindFIFO, KindSocket}
	for _, k := range forbidden {
		if !k.IsForbidden() {
			t.Errorf("%q must be forbidden per §13.4", k)
		}
	}
	allowed := []EntryKind{KindRegular, KindDirectory, KindSymlink}
	for _, k := range allowed {
		if k.IsForbidden() {
			t.Errorf("%q must NOT be unconditionally forbidden", k)
		}
	}
}

func TestValidateEntryAcceptsRegularInWorkspace(t *testing.T) {
	allow := RuntimeAllow{WorkspaceRoot: "/workspace/current"}
	cases := []Entry{
		{Path: "src/main.go", Kind: KindRegular, Size: 1024},
		{Path: "docs/README.md", Kind: KindRegular, Size: 4096},
		{Path: "src/auth/handler.go", Kind: KindRegular, Size: 4096},
	}
	for _, e := range cases {
		if err := ValidateEntry(e, allow); err != nil {
			t.Errorf("ValidateEntry(%+v) = %v, want nil", e, err)
		}
	}
}

func TestValidateEntryRejectsForbiddenKinds(t *testing.T) {
	allow := RuntimeAllow{}
	for _, k := range []EntryKind{KindHardlink, KindCharDevice, KindBlockDevice, KindFIFO, KindSocket} {
		err := ValidateEntry(Entry{Path: "x", Kind: k, Size: 0}, allow)
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Reason != ReasonNonRegularEntry {
			t.Errorf("kind %q: want non_regular_entry, got %v", k, err)
		}
	}
}

func TestValidateEntryRejectsAbsolutePath(t *testing.T) {
	err := ValidateEntry(Entry{Path: "/etc/passwd", Kind: KindRegular, Size: 1}, RuntimeAllow{})
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Reason != ReasonPathTraversal {
		t.Errorf("absolute path: want path_traversal, got %v", err)
	}
}

func TestValidateEntryRejectsTraversal(t *testing.T) {
	cases := []string{
		"../etc/passwd",
		"src/../../etc/passwd",
		"a/b/../../etc/passwd",
		"../../escape",
	}
	for _, p := range cases {
		err := ValidateEntry(Entry{Path: p, Kind: KindRegular, Size: 1}, RuntimeAllow{})
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Reason != ReasonPathTraversal {
			t.Errorf("traversal path %q: want path_traversal, got %v", p, err)
		}
	}
}

func TestValidateEntryRejectsOverLengthPath(t *testing.T) {
	// One byte over the 4096-byte cap.
	long := strings.Repeat("a", MaxPathLength+1)
	err := ValidateEntry(Entry{Path: long, Kind: KindRegular, Size: 1}, RuntimeAllow{})
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Reason != ReasonMaxPathLength {
		t.Errorf("over-length path: want max_path_length, got %v", err)
	}
}

func TestValidateEntryRejectsExcessiveDepth(t *testing.T) {
	parts := make([]string, MaxPathDepth+1)
	for i := range parts {
		parts[i] = "a"
	}
	deep := strings.Join(parts, "/")
	err := ValidateEntry(Entry{Path: deep, Kind: KindRegular, Size: 1}, RuntimeAllow{})
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Reason != ReasonMaxPathDepth {
		t.Errorf("excessive depth: want max_path_depth, got %v", err)
	}
}

func TestValidateEntryRejectsOversizedEntry(t *testing.T) {
	err := ValidateEntry(Entry{Path: "big.bin", Kind: KindRegular, Size: MaxPerEntrySize + 1}, RuntimeAllow{})
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Reason != ReasonMaxEntrySize {
		t.Errorf("oversized entry: want max_entry_size, got %v", err)
	}
}

func TestValidateEntryRejectsSymlinkByDefault(t *testing.T) {
	err := ValidateEntry(Entry{Path: "link", Kind: KindSymlink, LinkTarget: "target"}, RuntimeAllow{WorkspaceRoot: "/workspace/current"})
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Reason != ReasonSymlink {
		t.Errorf("symlink default reject: want symlink, got %v", err)
	}
}

func TestValidateEntrySymlinkOptInWorkspaceLocalTarget(t *testing.T) {
	allow := RuntimeAllow{AllowSymlinks: true, WorkspaceRoot: "/workspace/current"}
	err := ValidateEntry(Entry{Path: "src/link", Kind: KindSymlink, LinkTarget: "../docs/README.md"}, allow)
	if err != nil {
		t.Errorf("workspace-local symlink with opt-in: want nil, got %v", err)
	}
}

func TestValidateEntrySymlinkRejectsEscape(t *testing.T) {
	allow := RuntimeAllow{AllowSymlinks: true, WorkspaceRoot: "/workspace/current"}
	cases := []string{
		"/etc/passwd",
		"../../etc/passwd",
		"/proc/self/environ",
		"/sys/kernel/debug",
		"/dev/null",
		"/run/lenny/credentials.json",
	}
	for _, target := range cases {
		err := ValidateEntry(Entry{Path: "link", Kind: KindSymlink, LinkTarget: target}, allow)
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("symlink target %q: want ValidationError, got %v", target, err)
			continue
		}
		if ve.Reason != ReasonPathEscapesRoot {
			t.Errorf("symlink target %q: want path_escapes_root, got %q", target, ve.Reason)
		}
	}
}

func TestValidateSymlinkTargetRejectsEmpty(t *testing.T) {
	err := ValidateSymlinkTarget("link", "", "/workspace/current")
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Reason != ReasonSymlink {
		t.Errorf("empty target: want symlink, got %v", err)
	}
}

func TestValidateArchiveAcceptsWithinCeilings(t *testing.T) {
	a := Archive{CompressedBytes: 1_000_000, DecompressedBytes: 5_000_000, EntryCount: 100}
	if err := ValidateArchive(a); err != nil {
		t.Errorf("archive within ceilings should admit, got %v", err)
	}
}

func TestValidateArchiveRejectsOversizedAggregate(t *testing.T) {
	a := Archive{DecompressedBytes: MaxDecompressedSize + 1, EntryCount: 1}
	err := ValidateArchive(a)
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Reason != ReasonMaxDecompressedSize {
		t.Errorf("oversized aggregate: want max_decompressed_size, got %v", err)
	}
}

func TestValidateArchiveRejectsTooManyEntries(t *testing.T) {
	a := Archive{EntryCount: MaxEntryCount + 1}
	err := ValidateArchive(a)
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Reason != ReasonMaxEntryCount {
		t.Errorf("too many entries: want max_entry_count, got %v", err)
	}
}

func TestValidateArchiveRejectsExtremeRatio(t *testing.T) {
	// 1MB compressed, 200MB decompressed → ratio 200:1, exceeds 100:1.
	a := Archive{CompressedBytes: 1_000_000, DecompressedBytes: 200_000_000, EntryCount: 1}
	err := ValidateArchive(a)
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Reason != ReasonMaxDecompressionRatio {
		t.Errorf("zip bomb: want max_decompression_ratio, got %v", err)
	}
}

func TestValidateArchiveAcceptsBoundaryRatio(t *testing.T) {
	// Exactly at 100:1 should admit (the ceiling is `> 100`).
	a := Archive{CompressedBytes: 100_000, DecompressedBytes: 10_000_000, EntryCount: 1}
	if err := ValidateArchive(a); err != nil {
		t.Errorf("100:1 ratio at boundary should admit, got %v", err)
	}
}

// spec: §13.4 line 659 — a ratio just above the integer boundary must be
// rejected; integer division previously truncated it to the boundary and
// admitted it (F-13.4.14).
func TestValidateArchiveRejectsRatioJustOverBoundary_spec_13_4(t *testing.T) {
	cases := []struct {
		name    string
		archive Archive
		wantErr bool
	}{
		{name: "exactly 100:1 admitted", archive: Archive{CompressedBytes: 100, DecompressedBytes: 10_000, EntryCount: 1}},
		{name: "100.5:1 rejected", archive: Archive{CompressedBytes: 100, DecompressedBytes: 10_050, EntryCount: 1}, wantErr: true},
		{name: "one byte over boundary rejected", archive: Archive{CompressedBytes: 1000, DecompressedBytes: 100_001, EntryCount: 1}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateArchive(tc.archive)
			if tc.wantErr {
				var ve *ValidationError
				if !errors.As(err, &ve) || ve.Reason != ReasonMaxDecompressionRatio {
					t.Fatalf("want max_decompression_ratio, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("want admit, got %v", err)
			}
		})
	}
}

// spec: §13.4 line 665 — ValidateSymlinkTarget must reject a workspace root
// that is not a clean absolute slash path so the containment check cannot
// silently misbehave on a relative or OS-native root (F-13.4.13).
func TestValidateSymlinkTargetRejectsMalformedRoot_spec_13_4(t *testing.T) {
	roots := []struct {
		name string
		root string
	}{
		{name: "relative root", root: "workspace/current"},
		{name: "windows root", root: `C:\workspace\current`},
		{name: "empty root", root: ""},
		{name: "trailing slash", root: "/workspace/current/"},
		{name: "dotdot in root", root: "/workspace/../current"},
		{name: "dot in root", root: "/workspace/./current"},
	}
	for _, rt := range roots {
		t.Run(rt.name, func(t *testing.T) {
			err := ValidateSymlinkTarget("link", "sub/file.txt", rt.root)
			var ve *ValidationError
			if !errors.As(err, &ve) || ve.Reason != ReasonPathEscapesRoot {
				t.Fatalf("want path_escapes_root for root %q, got %v", rt.root, err)
			}
		})
	}
	// The canonical §13.4 root must remain valid.
	if err := ValidateSymlinkTarget("link", "sub/file.txt", "/workspace/current"); err != nil {
		t.Fatalf("canonical root must be accepted, got %v", err)
	}
}
