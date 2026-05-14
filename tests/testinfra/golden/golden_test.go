// SPDX-License-Identifier: MIT

package golden_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/golden"
)

// spec: 18.3 (golden file comparison happy path)
// diagnosis: Asserting against a freshly-written golden file
//
//	produced a mismatch. The compare path must accept the
//	exact bytes the update path wrote.
func TestAssertRoundtrip(t *testing.T) {
	// Setup: chdir to a temp dir so testdata/golden ends up isolated.
	tmp := t.TempDir()
	t.Chdir(tmp)
	body := []byte("hello golden\n")

	// First write the golden file directly.
	path := filepath.Join("testdata", "golden", "roundtrip.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Compare path should accept identical bytes.
	golden.Assert(t, "roundtrip.txt", body)
}

// spec: 18.3 (mismatch produces a useful diff)
// diagnosis: A diff should be reported when content differs. The
//
//	check below uses a captureT to assert Errorf fires
//	without failing the parent test.
func TestAssertMismatchReportsDiff(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	stored := []byte("stored\n")
	got := []byte("changed\n")
	path := filepath.Join("testdata", "golden", "mismatch.txt")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, stored, 0o644)

	tt := &captureT{TB: t}
	golden.Assert(tt, "mismatch.txt", got)
	if !tt.errored {
		t.Errorf("expected Errorf on mismatch")
	}
}

// spec: 18.3 (AssertJSON canonicalises field order)
// diagnosis: Two semantically-equivalent JSON documents with
//
//	different key order must compare equal.
func TestAssertJSONCanonicalisesFieldOrder(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	stored := []byte("{\n  \"a\": 1,\n  \"b\": 2\n}\n")
	got := []byte(`{"b":2,"a":1}`)
	path := filepath.Join("testdata", "golden", "canonical.json")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, stored, 0o644)

	golden.AssertJSON(t, "canonical.json", got)
}

type captureT struct {
	testing.TB
	errored bool
	fataled bool
}

func (c *captureT) Errorf(format string, args ...any) { c.errored = true }
func (c *captureT) Fatalf(format string, args ...any) { c.fataled = true }
