// SPDX-License-Identifier: MIT

package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/upload"
)

// TestRevalidatePromotedSymlinks_spec_7_4 covers the §7.4 symlink-handling
// bullet: after promotion every symlink is re-resolved against its new
// location under the workspace root. An in-root target is admitted; a
// target that escapes the root is rejected with path_escapes_root so the
// caller can roll the promotion back. F-7.4.12.
func TestRevalidatePromotedSymlinks_spec_7_4(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	if err := os.Symlink("target.txt", filepath.Join(root, "good")); err != nil {
		t.Fatalf("create in-root symlink: %v", err)
	}
	if err := revalidatePromotedSymlinks(root); err != nil {
		t.Fatalf("in-root promoted symlink was rejected: %v", err)
	}

	if err := os.Symlink("/etc/passwd", filepath.Join(root, "bad")); err != nil {
		t.Fatalf("create escaping symlink: %v", err)
	}
	err := revalidatePromotedSymlinks(root)
	if err == nil {
		t.Fatal("an escaping promoted symlink was not rejected")
	}
	var ve *upload.ValidationError
	if !errors.As(err, &ve) || ve.Reason != upload.ReasonPathEscapesRoot {
		t.Fatalf("want path_escapes_root ValidationError, got %v", err)
	}
}

// TestPromoteStagingCommit_spec_7_4_433 covers the §7.4 line 433 atomic
// promotion happy path: the build tree replaces the workspace root, the
// prior root content does not survive a committed promotion, and commit
// drops the backup. F-7.4.12.
func TestPromoteStagingCommit_spec_7_4_433(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "current")
	build := filepath.Join(base, "staging")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("seed root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "stale.txt"), []byte("old"), 0o644); err != nil {
		t.Fatalf("seed stale file: %v", err)
	}
	if err := os.MkdirAll(build, 0o755); err != nil {
		t.Fatalf("seed build: %v", err)
	}
	if err := os.WriteFile(filepath.Join(build, "new.txt"), []byte("new"), 0o644); err != nil {
		t.Fatalf("seed build file: %v", err)
	}

	promo, err := promoteStaging(build, root)
	if err != nil {
		t.Fatalf("promoteStaging: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "new.txt")); err != nil {
		t.Fatalf("promoted content missing from root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "stale.txt")); err == nil {
		t.Fatal("stale prior content survived the staging→current replacement")
	}
	promo.commit()
	if _, err := os.Stat(root + promotionBackupSuffix); !os.IsNotExist(err) {
		t.Fatalf("commit left the promotion backup behind: %v", err)
	}
}

// TestPromoteStagingRollback_spec_7_4_433 covers the §7.4 symlink-bullet
// rollback contract: a rolled-back promotion removes the promoted tree
// and restores the prior workspace root exactly. F-7.4.12.
func TestPromoteStagingRollback_spec_7_4_433(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "current")
	build := filepath.Join(base, "staging")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("seed root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "prior.txt"), []byte("prior"), 0o644); err != nil {
		t.Fatalf("seed prior file: %v", err)
	}
	if err := os.MkdirAll(build, 0o755); err != nil {
		t.Fatalf("seed build: %v", err)
	}
	if err := os.WriteFile(filepath.Join(build, "promoted.txt"), []byte("promoted"), 0o644); err != nil {
		t.Fatalf("seed build file: %v", err)
	}

	promo, err := promoteStaging(build, root)
	if err != nil {
		t.Fatalf("promoteStaging: %v", err)
	}
	promo.rollback()

	if got, err := os.ReadFile(filepath.Join(root, "prior.txt")); err != nil || string(got) != "prior" {
		t.Fatalf("rollback did not restore the prior root: got %q err %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, "promoted.txt")); err == nil {
		t.Fatal("rollback left the promoted content in the workspace root")
	}
}

// TestPromotionBuildDirAvoidsAliasing_spec_7_4_433 confirms the build
// tree is the spec-named /workspace/staging sibling but never aliases the
// configured raw-upload staging directory or the root itself. F-7.4.12.
func TestPromotionBuildDirAvoidsAliasing_spec_7_4_433(t *testing.T) {
	if got := promotionBuildDir("/workspace/current", "/workspace/.staging"); got != "/workspace/staging" {
		t.Errorf("build dir = %q, want /workspace/staging", got)
	}
	if got := promotionBuildDir("/workspace/current", "/workspace/staging"); got != "/workspace/current"+promotionBuildFallbackSuffix {
		t.Errorf("colliding build dir = %q, want the %q fallback", got, promotionBuildFallbackSuffix)
	}
}
