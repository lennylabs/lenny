// SPDX-License-Identifier: MIT

//go:build linux

package scrub

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// skipAsRoot skips a case that relies on permission bits, which root bypasses.
func skipAsRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("not-yet-applicable: root bypasses the permission bits this case relies on; run as a non-root user")
	}
}

// lockDir sets dir's mode for the test and restores it at cleanup, so
// t.TempDir can remove the tree.
func lockDir(t *testing.T, dir string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(dir, mode); err != nil {
		t.Fatalf("chmod %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
}

// spec: 5.2 ("What the scrub reaches")
func TestClearBeneathReportsAnEntryItCannotRemove_spec_5_2(t *testing.T) {
	skipAsRoot(t)
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "g"), []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	lockDir(t, locked, 0o500)

	err := clearBeneath(root)
	if !errors.Is(err, unix.EACCES) {
		t.Fatalf("clearBeneath = %v, want the EACCES of the entry it could not remove", err)
	}
	// Every entry is attempted: the removable sibling is gone.
	if _, statErr := os.Lstat(filepath.Join(root, "g")); !os.IsNotExist(statErr) {
		t.Fatalf("the removable sibling survived a failed entry: %v", statErr)
	}
	if err := clearBeneath(filepath.Join(root, "absent")); err != nil {
		t.Fatalf("clearBeneath of a missing directory = %v, want nil", err)
	}
}

// spec: 5.2 ("What the scrub reaches")
func TestPathStateBeneath_spec_5_2(t *testing.T) {
	root := t.TempDir()
	t.Run("missing parent is absent and clean", func(t *testing.T) {
		exists, empty, err := pathStateBeneath(filepath.Join(root, "no", "such"))
		if err != nil || exists || !empty {
			t.Fatalf("= %t, %t, %v; want absent and clean", exists, empty, err)
		}
	})
	t.Run("missing path is absent and clean", func(t *testing.T) {
		exists, empty, err := pathStateBeneath(filepath.Join(root, "such"))
		if err != nil || exists || !empty {
			t.Fatalf("= %t, %t, %v; want absent and clean", exists, empty, err)
		}
	})
	t.Run("a file left behind is dirty", func(t *testing.T) {
		p := filepath.Join(root, "file")
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		exists, empty, err := pathStateBeneath(p)
		if err != nil || !exists || empty {
			t.Fatalf("= %t, %t, %v; want present and dirty", exists, empty, err)
		}
	})
	t.Run("an unreadable directory is an error", func(t *testing.T) {
		skipAsRoot(t)
		p := filepath.Join(root, "sealed")
		if err := os.Mkdir(p, 0o755); err != nil {
			t.Fatal(err)
		}
		lockDir(t, p, 0)
		if _, _, err := pathStateBeneath(p); !errors.Is(err, unix.EACCES) {
			t.Fatalf("err = %v, want EACCES", err)
		}
	})
	t.Run("an unreadable parent is an error", func(t *testing.T) {
		skipAsRoot(t)
		parent := filepath.Join(root, "closed")
		if err := os.Mkdir(parent, 0o755); err != nil {
			t.Fatal(err)
		}
		lockDir(t, parent, 0)
		if _, _, err := pathStateBeneath(filepath.Join(parent, "x")); !errors.Is(err, unix.EACCES) {
			t.Fatalf("err = %v, want EACCES", err)
		}
	})
}

// spec: 5.2 ("What the scrub reaches")
func TestShmRemoveReportsAnUnknownSegment_spec_5_2(t *testing.T) {
	if err := shmRemove(-1); err == nil {
		t.Fatal("removing segment -1 succeeded")
	}
}
