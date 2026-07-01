// SPDX-License-Identifier: MIT

package fileexport_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation/fileexport"
)

// spec: §8.7 line 789 — destPrefix must be a relative path, no `..`, no
// absolute paths. F-8.7.3.
func TestValidateDestPrefix_spec_8_7_789(t *testing.T) {
	cases := []struct {
		name    string
		prefix  string
		wantErr error
	}{
		{"empty is root", "", nil},
		{"simple relative", "input/", nil},
		{"nested relative", "project/src/", nil},
		{"dot-relative", "./input", nil},
		{"dotdot in filename is fine", "foo..bar/data", nil},
		{"absolute rejected", "/input", fileexport.ErrDestPrefixAbsolute},
		{"leading-slash nested rejected", "/project/src", fileexport.ErrDestPrefixAbsolute},
		{"parent segment rejected", "../escape", fileexport.ErrDestPrefixParentSegment},
		{"mid parent segment rejected", "ok/../bad", fileexport.ErrDestPrefixParentSegment},
		{"trailing parent segment rejected", "a/b/..", fileexport.ErrDestPrefixParentSegment},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := fileexport.ValidateDestPrefix(tc.prefix)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("ValidateDestPrefix(%q) = %v, want nil", tc.prefix, err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ValidateDestPrefix(%q) = %v, want %v", tc.prefix, err, tc.wantErr)
			}
		})
	}
}

// spec: §8.3 line 264 — default fileExportLimits is
// { maxFiles: 100, maxTotalSize: 100MB }. F-8.7.4.
func TestDefaultFileExportLimits_spec_8_3_264(t *testing.T) {
	if fileexport.DefaultFileExportLimits.MaxFiles != 100 {
		t.Errorf("MaxFiles = %d, want 100", fileexport.DefaultFileExportLimits.MaxFiles)
	}
	if fileexport.DefaultFileExportLimits.MaxTotalSize != 100*1024*1024 {
		t.Errorf("MaxTotalSize = %d, want 100MiB", fileexport.DefaultFileExportLimits.MaxTotalSize)
	}
}

// spec: §8.7 lines 790-791 — file count and aggregate size are checked
// against fileExportLimits; the boundary admits and one-over rejects.
// F-8.7.4.
func TestFileExportLimitsCheck_spec_8_7_790(t *testing.T) {
	l := fileexport.FileExportLimits{MaxFiles: 100, MaxTotalSize: 1000}

	if err := l.Check(100, 1000); err != nil {
		t.Errorf("exactly-at-limit should admit: %v", err)
	}
	if err := l.Check(0, 0); err != nil {
		t.Errorf("empty export should admit: %v", err)
	}
	if err := l.Check(101, 0); !errors.Is(err, fileexport.ErrTooManyFiles) {
		t.Errorf("101 files = %v, want ErrTooManyFiles", err)
	}
	if err := l.Check(1, 1001); !errors.Is(err, fileexport.ErrTotalSizeExceeded) {
		t.Errorf("1001 bytes = %v, want ErrTotalSizeExceeded", err)
	}
}

// A non-positive limit disables that dimension (operator opt-out),
// matching the platform's zero-means-unlimited limiters. F-8.7.4.
func TestFileExportLimitsZeroIsUnlimited(t *testing.T) {
	l := fileexport.FileExportLimits{MaxFiles: 0, MaxTotalSize: 0}
	if err := l.Check(1_000_000, 1<<40); err != nil {
		t.Errorf("zero limits should admit everything: %v", err)
	}
}

// spec: §8.7 line 787 — a file resolving (via realpath) inside the
// workspace root is admitted. F-8.7.2.
func TestResolveWithinRootAdmitsContainedFile_spec_8_7_787(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "exports", "data.txt")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := fileexport.ResolveWithinRoot(root, real)
	if err != nil {
		t.Fatalf("ResolveWithinRoot: %v", err)
	}
	wantReal, _ := filepath.EvalSymlinks(real)
	if got != wantReal {
		t.Errorf("resolved = %q, want %q", got, wantReal)
	}
}

// A symlink that stays inside the root is admitted. F-8.7.2.
func TestResolveWithinRootAdmitsInternalSymlink_spec_8_7_787(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "real.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	got, err := fileexport.ResolveWithinRoot(root, link)
	if err != nil {
		t.Fatalf("internal symlink should resolve: %v", err)
	}
	wantReal, _ := filepath.EvalSymlinks(target)
	if got != wantReal {
		t.Errorf("resolved = %q, want %q", got, wantReal)
	}
}

// spec: §8.7 line 787 — the `./data → /etc/passwd` attack: a symlink
// resolving outside the workspace root is rejected. F-8.7.2.
func TestResolveWithinRootRejectsEscapingSymlink_spec_8_7_787(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir() // a sibling dir, definitively outside root
	secret := filepath.Join(outside, "passwd")
	if err := os.WriteFile(secret, []byte("root:x:0:0"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "data")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	_, err := fileexport.ResolveWithinRoot(root, link)
	if !errors.Is(err, fileexport.ErrPathEscapesRoot) {
		t.Fatalf("escaping symlink = %v, want ErrPathEscapesRoot", err)
	}
}

// A non-existent candidate surfaces a wrapped filesystem error (the
// export operates on matched files, so a missing path is an error).
// F-8.7.2.
func TestResolveWithinRootRejectsMissingFile(t *testing.T) {
	root := t.TempDir()
	_, err := fileexport.ResolveWithinRoot(root, filepath.Join(root, "does-not-exist"))
	if err == nil {
		t.Fatal("missing candidate should error")
	}
	if errors.Is(err, fileexport.ErrPathEscapesRoot) {
		t.Errorf("missing file should not be reported as an escape: %v", err)
	}
}
