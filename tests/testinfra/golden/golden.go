// SPDX-License-Identifier: MIT

// Package golden implements §18.3 golden-file comparison with the
// canonical `--update-golden` flag.
//
// Usage:
//
//	got := renderManifest(t)
//	golden.Assert(t, "manifest.yaml", got)
//
// The first invocation writes `testdata/golden/manifest.yaml` and
// fails the test asking the author to inspect. Subsequent invocations
// diff against the stored file. Running with `go test -update-golden`
// (or the GOLDEN_UPDATE=1 env var) rewrites the file from the
// current output.
//
// Conventions:
//   - Golden files live next to the test under testdata/golden/
//   - Path components join with the filesystem separator; nested
//     subdirectories are allowed for organization
//   - The file is treated as bytes; line-ending normalisation is
//     up to the caller (use NormalizeNewlines if needed)
package golden

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// updateFlag exposes -update-golden on every test binary that
// imports this package.
var updateFlag = flag.Bool("update-golden", envBool("GOLDEN_UPDATE", false),
	"rewrite golden files from the current output (also enabled by GOLDEN_UPDATE=1)")

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "":
		return def
	}
	return false
}

// Assert compares got against the stored golden file at
// testdata/golden/<name>. The path is resolved relative to the
// test's working directory.
//
//   - When --update-golden is set the file is rewritten and the
//     test passes with a Log message.
//   - When the file does not exist on disk and --update-golden is
//     not set the test fails with a "run with -update-golden" hint.
//   - When the bytes differ the test fails with a unified-style
//     diff (first 40 line pairs).
func Assert(t testing.TB, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *updateFlag {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("golden: mkdir: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("golden: write: %v", err)
		}
		t.Logf("golden: wrote %s (%d bytes)", path, len(got))
		return
	}
	want, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Fatalf("golden: %s does not exist yet. Run `go test -update-golden ./%s` and inspect the result before committing.",
			path, filepath.Dir(path))
	}
	if err != nil {
		t.Fatalf("golden: read: %v", err)
	}
	if bytes.Equal(want, got) {
		return
	}
	t.Errorf("golden %s mismatch:\n%s", path, diff(want, got))
}

// AssertJSON normalises both sides through canonical JSON encoding
// before comparing — useful for outputs that may shuffle map order
// across runs.
func AssertJSON(t testing.TB, name string, got []byte) {
	t.Helper()
	normalized := canonicalJSON(t, got)
	Assert(t, name, normalized)
}

// NormalizeNewlines collapses CRLF / CR to LF so golden comparisons
// are platform-independent.
func NormalizeNewlines(b []byte) []byte {
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	b = bytes.ReplaceAll(b, []byte("\r"), []byte("\n"))
	return b
}

// canonicalJSON re-encodes a JSON document with sorted keys and
// 2-space indentation. Returns the input unchanged on parse error
// so non-JSON payloads still compare meaningfully.
func canonicalJSON(t testing.TB, body []byte) []byte {
	t.Helper()
	var v any
	if err := jsonUnmarshal(body, &v); err != nil {
		t.Logf("golden.AssertJSON: input not JSON, treating as raw bytes (%v)", err)
		return body
	}
	out, err := jsonMarshalIndentSorted(v)
	if err != nil {
		return body
	}
	return out
}

func diff(want, got []byte) string {
	wantLines := strings.SplitAfter(string(want), "\n")
	gotLines := strings.SplitAfter(string(got), "\n")
	n := len(wantLines)
	if len(gotLines) > n {
		n = len(gotLines)
	}
	if n > 40 {
		n = 40
	}
	var sb strings.Builder
	for i := 0; i < n; i++ {
		var w, g string
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if w == g {
			sb.WriteString("    " + g)
		} else {
			sb.WriteString("  - " + w)
			sb.WriteString("  + " + g)
		}
	}
	if n == 40 {
		sb.WriteString("    ... (output truncated at 40 line pairs)\n")
	}
	if !strings.HasSuffix(sb.String(), "\n") {
		sb.WriteString("\n")
	}
	return sb.String()
}

// Update reports whether --update-golden is in effect. Tests that
// want to skip expensive recomputation when only checking can branch
// on this.
func Update() bool { return *updateFlag }

// String wraps Assert for string payloads.
func String(t testing.TB, name, got string) {
	t.Helper()
	Assert(t, name, []byte(got))
}

// guard against unused-import: keep fmt available for diff messages
// in future expansion of the package.
var _ = fmt.Sprintf
