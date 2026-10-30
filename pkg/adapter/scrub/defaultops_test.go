// SPDX-License-Identifier: MIT

package scrub

import (
	"os"
	"path/filepath"
	"testing"
)

// spec: §5.2 step 2 — RemoveAll removes the workspace tree; a missing path
// is not an error.
func TestDefaultOps_RemoveAll(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "current")
	if err := os.MkdirAll(filepath.Join(ws, "sub"), 0o755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "sub", "f"), []byte("x"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ops := DefaultOps{}
	if err := ops.RemoveAll(ws); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if _, err := os.Stat(ws); !os.IsNotExist(err) {
		t.Fatalf("workspace not removed")
	}
	// Idempotent: removing again is not an error.
	if err := ops.RemoveAll(ws); err != nil {
		t.Fatalf("RemoveAll on missing path: %v", err)
	}
}

// spec: §5.2 step 4 — ClearContents empties a directory but leaves the
// directory itself; a missing directory is not an error.
func TestDefaultOps_ClearContents(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "d", "e"), 0o755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ops := DefaultOps{}
	if err := ops.ClearContents(dir); err != nil {
		t.Fatalf("ClearContents: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("dir not cleared: %d entries remain", len(entries))
	}
	if err := ops.ClearContents(filepath.Join(dir, "missing")); err != nil {
		t.Fatalf("ClearContents on missing dir: %v", err)
	}
}

// spec: §5.2 step 6 — PathState distinguishes absent, empty, and non-empty
// so the verification can flag a dirty path.
func TestDefaultOps_PathState(t *testing.T) {
	dir := t.TempDir()
	ops := DefaultOps{}

	// Absent path: not exists, vacuously empty.
	exists, empty, err := ops.PathState(filepath.Join(dir, "nope"))
	if err != nil || exists || !empty {
		t.Fatalf("absent: exists=%v empty=%v err=%v", exists, empty, err)
	}

	// Empty dir.
	empties := filepath.Join(dir, "empty")
	if err := os.Mkdir(empties, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	exists, empty, err = ops.PathState(empties)
	if err != nil || !exists || !empty {
		t.Fatalf("empty dir: exists=%v empty=%v err=%v", exists, empty, err)
	}

	// Non-empty dir.
	if err := os.WriteFile(filepath.Join(empties, "leftover"), []byte("x"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	exists, empty, err = ops.PathState(empties)
	if err != nil || !exists || empty {
		t.Fatalf("non-empty dir: exists=%v empty=%v err=%v", exists, empty, err)
	}

	// A regular file that exists is non-empty for verification purposes.
	f := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(f, []byte("{}"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	exists, empty, err = ops.PathState(f)
	if err != nil || !exists || empty {
		t.Fatalf("file: exists=%v empty=%v err=%v", exists, empty, err)
	}
}
