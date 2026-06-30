// SPDX-License-Identifier: MIT

package events

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNonceCheckpointWindowBelowEveryUsesDefault_spec_25_3_748 asserts a
// Window smaller than Every is widened to the package default so the
// safe-skip window always exceeds the unpersisted-tick gap.
func TestNonceCheckpointWindowBelowEveryUsesDefault_spec_25_3_748(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonce")
	if err := os.WriteFile(path, []byte("100"), 0o600); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}
	// Window (2) < Every (10): loadNonceCheckpoint must widen Window to
	// defaultCheckpointWindow.
	_, start, err := loadNonceCheckpoint(NonceCheckpoint{Path: path, Every: 10, Window: 2})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if want := uint64(100) + defaultCheckpointWindow; start != want {
		t.Fatalf("resume start = %d, want %d (default window applied)", start, want)
	}
}

// TestNonceCheckpointMissingFileStartsFresh_spec_25_3_748 asserts an
// absent checkpoint resumes from nonce 0 (no error, in-process start).
func TestNonceCheckpointMissingFileStartsFresh_spec_25_3_748(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent")
	cp, start, err := loadNonceCheckpoint(NonceCheckpoint{Path: path})
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if start != 0 {
		t.Fatalf("missing-file start = %d, want 0", start)
	}
	if cp == nil {
		t.Fatal("checkpoint should be non-nil so subsequent records persist")
	}
}
