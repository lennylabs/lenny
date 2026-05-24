// SPDX-License-Identifier: MIT

package workspace_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
)

// spec: §4.4 line 254 — pre-checkpoint workspace-size probe.

func TestSizeReturnsZeroForMissingRoot(t *testing.T) {
	got, err := workspace.Size(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if got != 0 {
		t.Errorf("Size(missing) = %d, want 0", got)
	}
}

func TestSizeReturnsZeroForEmptyRoot(t *testing.T) {
	root := t.TempDir()
	got, err := workspace.Size(root)
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if got != 0 {
		t.Errorf("Size(empty) = %d, want 0", got)
	}
}

func TestSizeReturnsZeroForEmptyPath(t *testing.T) {
	got, err := workspace.Size("")
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if got != 0 {
		t.Errorf("Size(\"\") = %d, want 0", got)
	}
}

func TestSizeSumsRegularFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("world!"), 0o644); err != nil {
		t.Fatalf("write b: %v", err)
	}
	got, err := workspace.Size(root)
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if got != 11 {
		t.Errorf("Size = %d, want 11", got)
	}
}

func TestSizeRecursesIntoSubdirectories(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "nested", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "file.bin"), []byte("12345678"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "top.txt"), []byte("ab"), 0o644); err != nil {
		t.Fatalf("write top: %v", err)
	}
	got, err := workspace.Size(root)
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if got != 10 {
		t.Errorf("Size = %d, want 10", got)
	}
}
