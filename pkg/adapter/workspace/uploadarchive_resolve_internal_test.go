// SPDX-License-Identifier: MIT

package workspace

import (
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/upload"
)

// spec: §7.4 zip-slip; §13.4 line 654; §15.1 line 1093 — resolveArchivePath
// is the defense-in-depth filesystem containment check on the archive
// extraction path. A residual escape (e.g. a `..`-bearing pathPrefix that
// slips past ValidateEntry) surfaces as a typed
// *upload.ValidationError{Reason: path_escapes_root} rather than a generic
// error string, so the gateway maps it to the §15.1 sub-code. F-13.4.9.
func TestResolveArchivePathTypedEscape(t *testing.T) {
	root := t.TempDir()

	cases := []struct {
		name   string
		prefix string
		rel    string
	}{
		{"parent-traversal", "", "../escape"},
		{"prefix-traversal", "../..", "x.txt"},
		{"absolute", "", "/etc/passwd"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveArchivePath(root, tc.prefix, tc.rel)
			var ve *upload.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("error %v does not unwrap to *upload.ValidationError", err)
			}
			if ve.Reason != upload.ReasonPathEscapesRoot {
				t.Fatalf("Reason = %q, want %q", ve.Reason, upload.ReasonPathEscapesRoot)
			}
		})
	}
}

// A clean post-strip path resolves to a destination within the root.
// F-13.4.9.
func TestResolveArchivePathCleanWithinRoot(t *testing.T) {
	root := t.TempDir()
	dst, err := resolveArchivePath(root, "vendor", "lib/x.go")
	if err != nil {
		t.Fatalf("resolveArchivePath: %v", err)
	}
	if !strings.HasPrefix(dst, root) {
		t.Fatalf("dst %q is not under root %q", dst, root)
	}
}
