// SPDX-License-Identifier: MIT

// Package workspace materializes a WorkspacePlan into a pod's workspace
// directory. The §4.7 adapter calls Materialize from StartSession,
// before the runtime binary is launched, to lay down the files the
// agent works against.
//
// The adapter is a separate trust boundary from the gateway, so
// Materialize re-checks every source path for containment within the
// workspace root rather than relying on the gateway's §14 validation
// alone, and it rejects setuid and setgid modes.
package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/upload"
)

// Warning is one non-fatal §14 advisory the adapter raised against a
// workspace source during materialization. The fields mirror the
// proto WorkspacePlanWarning so the adapter Server can transcribe a
// slice straight onto FinalizeWorkspaceResponse.
//
// spec: §7.4 line 459 (workspace_plan_strip_components_skip); §14
// WarningCode. F-7.4.15, F-14.1.18.
type Warning struct {
	// Code is the §14 WarningCode enum value (e.g.
	// `workspace_plan_strip_components_skip`).
	Code string
	// SourceIndex is the 0-based plan source index the warning refers to.
	// spec: §14 line 100 — `sourceIndex`.
	SourceIndex int
	// EntryPath is the archive entry path that triggered the warning.
	// Empty when the warning is not entry-scoped.
	// spec: §14 line 100 — `entryPath`. F-14.1.18.
	EntryPath string
	// SegmentCount is the number of `/`-separated segments the rejected
	// entry had after trimming leading/trailing empty segments.
	// spec: §14 line 100 — `segmentCount`. F-14.1.18.
	SegmentCount int
	// StripComponents is the configured `stripComponents` value the
	// entry was tested against.
	// spec: §14 line 100 — `stripComponents`. F-14.1.18.
	StripComponents int
	// UnknownType is the open-string `source.type` the materializer did
	// not recognize and skipped. Populated only on
	// `workspace_plan_unknown_source_type` warnings.
	// spec: §14 line 334 — `unknownType`. F-14.1.2.
	UnknownType string
	// Message is a human-readable explanation.
	Message string
}

// unknownSourceTypeSkipCode is the §14 closed-enum WarningCode for the
// §14 line 334 "unknown source.type is skipped, not rejected" advisory.
// The string matches pkg/workspaceplan.WarnUnknownSourceType so the two
// definitions stay aligned. F-14.1.2.
const unknownSourceTypeSkipCode = "workspace_plan_unknown_source_type"

// ArchivePolicy is the §13.4 per-Runtime archive-extraction policy the
// gateway hands the adapter on FinalizeWorkspace. AllowSymlinks lifts the
// §7.4 line 458 default-deny on symlink entries; WorkspaceRoot is the
// absolute path symlink targets are canonicalized against. spec: §7.4
// lines 458, 462; §13.4 lines 663-672 — F-7.4.4.
type ArchivePolicy struct {
	AllowSymlinks bool
	WorkspaceRoot string
}

// Materialize writes the workspace sources into root in order under
// the platform default §13.4 archive policy (symlinks rejected). It is
// a shorthand for MaterializeWithPolicy with an empty ArchivePolicy.
func Materialize(root, stagingDir string, sources []*adapterv1.WorkspaceSource) ([]Warning, error) {
	return MaterializeWithPolicy(root, stagingDir, sources, ArchivePolicy{WorkspaceRoot: root})
}

const (
	// promotionStagingName is the §7.4 line 433 /workspace/staging build
	// tree. MaterializeWithPolicy lays the resolved workspace down here,
	// then atomically promotes it onto the workspace root
	// (/workspace/current). It is a sibling of the root so the promotion
	// rename never crosses a filesystem boundary. spec: §7.4 line 433 —
	// F-7.4.12, F-13.4.5.
	promotionStagingName = "staging"
	// promotionBackupSuffix names the directory the pre-promotion workspace
	// root is moved aside to while the staging tree is renamed into place,
	// so a failed post-promotion symlink re-validation can restore it.
	// spec: §7.4 line 433 — F-7.4.12.
	promotionBackupSuffix = ".prev"
	// promotionBuildFallbackSuffix is the build-tree name used when the
	// spec-default sibling (/workspace/staging) would alias the configured
	// raw-upload staging directory or the workspace root itself.
	promotionBuildFallbackSuffix = ".build"
	// promotionStagingMode is the permission mode of the build tree. The
	// promotion rename carries the materialized entries' own modes onto the
	// workspace root, so the build tree only needs the adapter's own
	// traverse access while it is assembled.
	promotionStagingMode = 0o755
)

// MaterializeWithPolicy writes the workspace sources into the workspace
// root in order, using the §7.4 staging→validation→promotion pattern.
// It handles every §14 source type: inlineFile and mkdir write directly;
// uploadFile and uploadArchive extract content staged under stagingDir
// by PrepareWorkspace; gitClone extracts the repository archive the
// gateway cloned and staged under stagingDir.
//
// Per §7.4 line 433 the resolved tree is built in a sibling
// /workspace/staging directory and atomically promoted onto root only
// after every source succeeds, so the runtime never observes a partial
// workspace and a failure in any source (not only the last) leaves the
// prior /workspace/current untouched. After promotion every symlink is
// re-validated against its new location under root; an escape rolls the
// whole promotion back and restores the previous root. spec: §7.4 line
// 433 and the §7.4 symlink-handling bullet — F-7.4.12, F-13.4.5.
//
// archive carries the §13.4 per-Runtime opt-ins (allowSymlinks +
// workspace root for symlink-target validation). It is consulted only
// by the uploadArchive and gitClone extractors. An empty WorkspaceRoot
// falls back to root.
//
// Non-fatal advisory warnings (per-entry strip-components skips,
// future §14 warning codes) are appended to the returned slice rather
// than aborting materialization, per §7.4 line 459. spec: §7.4 line
// 459; §7.4 lines 458, 462; §13.4 — F-7.4.4, F-7.4.15.
func MaterializeWithPolicy(root, stagingDir string, sources []*adapterv1.WorkspaceSource, archive ArchivePolicy) ([]Warning, error) {
	root = filepath.Clean(root)
	if archive.WorkspaceRoot == "" {
		archive.WorkspaceRoot = root
	}
	// spec: §7.4 line 433 — build into /workspace/staging, not directly
	// into /workspace/current. Symlink targets are validated against the
	// intended final root (archive.WorkspaceRoot) at extraction time; the
	// definitive check runs against root after promotion.
	buildDir := promotionBuildDir(root, stagingDir)
	if err := os.RemoveAll(buildDir); err != nil {
		return nil, fmt.Errorf("clear workspace staging %q: %w", buildDir, err)
	}
	if err := os.MkdirAll(buildDir, promotionStagingMode); err != nil {
		return nil, fmt.Errorf("create workspace staging %q: %w", buildDir, err)
	}

	var warnings []Warning
	for i, src := range sources {
		w, err := materializeSource(buildDir, stagingDir, i, src, archive)
		warnings = append(warnings, w...)
		if err != nil {
			// spec: §7.4 line 460 — a failure at any source returns the
			// staging tree to its pre-extraction state. Discarding the whole
			// build tree leaves the live workspace root untouched.
			_ = os.RemoveAll(buildDir)
			return warnings, fmt.Errorf("workspace source %d (type %q): %w", i, src.GetType(), err)
		}
	}

	// spec: §7.4 line 433 — atomic staging→current promotion.
	promo, err := promoteStaging(buildDir, root)
	if err != nil {
		_ = os.RemoveAll(buildDir)
		return warnings, err
	}
	// spec: §7.4 symlink-handling bullet — re-validate every symlink against
	// its promoted location under root; an escape rolls the promotion back.
	if err := revalidatePromotedSymlinks(root); err != nil {
		promo.rollback()
		return warnings, err
	}
	promo.commit()
	return warnings, nil
}

// promotionBuildDir returns the §7.4 /workspace/staging build tree as a
// sibling of root. It never aliases the workspace root itself or the
// configured raw-upload staging directory (which holds the compressed
// upload payloads the extractors read from), falling back to a
// root-adjacent name in the rare case of a collision. spec: §7.4 line
// 433 — F-7.4.12.
func promotionBuildDir(root, stagingDir string) string {
	dir := filepath.Join(filepath.Dir(root), promotionStagingName)
	if dir == root || (stagingDir != "" && dir == filepath.Clean(stagingDir)) {
		return root + promotionBuildFallbackSuffix
	}
	return dir
}

// promotion records the state needed to commit or roll back an atomic
// staging→current promotion. spec: §7.4 line 433 — F-7.4.12.
type promotion struct {
	root    string
	backup  string
	hadPrev bool
}

// promoteStaging atomically replaces root with the build tree. The prior
// root (the warm-time empty /workspace/current, or a previously
// materialized tree) is moved aside to a backup so a failed
// post-promotion check can restore it. The build→root rename is a single
// atomic syscall, so the runtime sees either the complete promoted tree
// or the prior root, never a partial workspace. spec: §7.4 line 433 —
// F-7.4.12.
func promoteStaging(build, root string) (*promotion, error) {
	p := &promotion{root: root, backup: root + promotionBackupSuffix}
	if err := os.RemoveAll(p.backup); err != nil {
		return nil, fmt.Errorf("clear promotion backup %q: %w", p.backup, err)
	}
	if _, err := os.Lstat(root); err == nil {
		if err := os.Rename(root, p.backup); err != nil {
			return nil, fmt.Errorf("move workspace root aside: %w", err)
		}
		p.hadPrev = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("stat workspace root: %w", err)
	}
	if err := os.Rename(build, root); err != nil {
		if p.hadPrev {
			_ = os.Rename(p.backup, root)
		}
		return nil, fmt.Errorf("promote staging to workspace root: %w", err)
	}
	return p, nil
}

// commit drops the saved previous root after a successful promotion and
// re-validation. spec: §7.4 line 433 — F-7.4.12.
func (p *promotion) commit() { _ = os.RemoveAll(p.backup) }

// rollback removes the promoted tree and restores the previous root. When
// no prior root existed it recreates an empty one so the §6.1 warm-time
// invariant ("/workspace/current exists") holds after a rolled-back
// promotion. spec: §7.4 symlink-handling bullet, §6.1 line 11 — F-7.4.12.
func (p *promotion) rollback() {
	_ = os.RemoveAll(p.root)
	if p.hadPrev {
		_ = os.Rename(p.backup, p.root)
		return
	}
	_ = os.MkdirAll(p.root, promotionStagingMode)
}

// revalidatePromotedSymlinks re-resolves every symlink in the promoted
// tree against its new location under root and fails when any target
// escapes root or traverses a forbidden pseudo-filesystem mount. The
// staging-time check resolves targets relative to the symlink's location
// in /workspace/staging; because the promoted location differs, §7.4
// mandates this second pass against /workspace/current. spec: §7.4
// symlink-handling bullet — F-7.4.12, F-7.4.4.
func revalidatePromotedSymlinks(root string) error {
	root = filepath.Clean(root)
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&fs.ModeSymlink == 0 {
			return nil
		}
		target, err := os.Readlink(p)
		if err != nil {
			return fmt.Errorf("read promoted symlink %q: %w", p, err)
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return fmt.Errorf("locate promoted symlink %q: %w", p, err)
		}
		return upload.ValidateSymlinkTarget(filepath.ToSlash(rel), target, filepath.ToSlash(root))
	})
}

func materializeSource(root, stagingDir string, sourceIndex int, src *adapterv1.WorkspaceSource, archive ArchivePolicy) ([]Warning, error) {
	switch src.GetType() {
	case "inlineFile":
		return nil, writeInlineFile(root, src)
	case "mkdir":
		return nil, makeDir(root, src)
	case "uploadFile":
		return nil, writeUploadFile(root, stagingDir, src)
	case "uploadArchive":
		return extractUploadArchive(root, stagingDir, sourceIndex, src, archive)
	case "gitClone":
		return nil, extractGitClone(root, stagingDir, src)
	default:
		// spec: §14 line 334 — a consumer that encounters an unknown
		// source.type MUST skip the entry and emit a
		// workspace_plan_unknown_source_type warning rather than reject
		// the whole plan. The adapter is the live consumer at
		// materialization time, so a newer gateway can inject a source
		// type this adapter predates during a rolling upgrade. The
		// `schemaVersion` warning field is stamped by FinalizeWorkspace,
		// which holds the plan. F-14.1.2.
		return []Warning{{
			Code:        unknownSourceTypeSkipCode,
			SourceIndex: sourceIndex,
			UnknownType: src.GetType(),
			Message:     fmt.Sprintf("unknown source type %q; skipped per §14 open-string discriminator", src.GetType()),
		}}, nil
	}
}

func writeInlineFile(root string, src *adapterv1.WorkspaceSource) error {
	path, err := resolvePath(root, src.GetPath())
	if err != nil {
		return err
	}
	mode, err := parseMode(src.GetMode(), 0o644)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent directories: %w", err)
	}
	if err := os.WriteFile(path, []byte(src.GetContent()), mode); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	// WriteFile honors the umask; pin the requested mode exactly.
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("set file mode: %w", err)
	}
	return nil
}

func makeDir(root string, src *adapterv1.WorkspaceSource) error {
	path, err := resolvePath(root, src.GetPath())
	if err != nil {
		return err
	}
	mode, err := parseMode(src.GetMode(), 0o755)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(path, mode); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("set directory mode: %w", err)
	}
	return nil
}

// writeUploadFile places a file staged by PrepareWorkspace at the
// source path. The staged content lives at stagingDir/<uploadRef>.
func writeUploadFile(root, stagingDir string, src *adapterv1.WorkspaceSource) error {
	if stagingDir == "" {
		return errors.New("uploadFile source requires a staging directory")
	}
	staged, err := StagingPath(stagingDir, src.GetUploadRef())
	if err != nil {
		return err
	}
	dst, err := resolvePath(root, src.GetPath())
	if err != nil {
		return err
	}
	mode, err := parseMode(src.GetMode(), 0o644)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create parent directories: %w", err)
	}
	in, err := os.Open(staged)
	if err != nil {
		return fmt.Errorf("open staged upload: %w", err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("copy staged upload: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close file: %w", err)
	}
	// OpenFile honors the umask; pin the requested mode exactly.
	if err := os.Chmod(dst, mode); err != nil {
		return fmt.Errorf("set file mode: %w", err)
	}
	return nil
}

// StagingPath maps an upload ref to a deterministic, contained path
// inside the staging directory. The ref is a §14 WorkspaceSource
// uploadRef — a §4.5 lenny-blob:// URI or any opaque token — so it is
// hashed: the staging file name is always a fixed-charset hex string
// that cannot contain a path separator and cannot escape the staging
// directory. PrepareWorkspace staging and uploadFile/uploadArchive
// materialization resolve the same ref to the same path.
func StagingPath(stagingDir, uploadRef string) (string, error) {
	if uploadRef == "" {
		return "", errors.New("upload ref is empty")
	}
	sum := sha256.Sum256([]byte(uploadRef))
	return filepath.Join(stagingDir, hex.EncodeToString(sum[:])), nil
}

// resolvePath joins rel onto root and confirms the result stays within
// root. An empty path, an absolute path, or a path that escapes the
// root through `..` is rejected.
func resolvePath(root, rel string) (string, error) {
	if rel == "" {
		return "", errors.New("source path is empty")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute path %q is not permitted", rel)
	}
	rootClean := filepath.Clean(root)
	full := filepath.Join(rootClean, rel)
	if full != rootClean && !pathWithin(rootClean, full) {
		return "", fmt.Errorf("path %q escapes the workspace root", rel)
	}
	return full, nil
}

// pathWithin reports whether child is root itself or nested under it.
func pathWithin(root, child string) bool {
	return len(child) > len(root) &&
		child[:len(root)] == root &&
		child[len(root)] == filepath.Separator
}

// parseMode parses an octal mode string. An empty string yields def.
// setuid and setgid modes are rejected; the sticky bit is preserved.
func parseMode(s string, def uint32) (os.FileMode, error) {
	v := def
	if s != "" {
		parsed, err := strconv.ParseUint(s, 8, 32)
		if err != nil {
			return 0, fmt.Errorf("invalid octal mode %q: %w", s, err)
		}
		v = uint32(parsed)
	}
	if v&0o4000 != 0 || v&0o2000 != 0 {
		return 0, fmt.Errorf("mode %q sets setuid or setgid, which is forbidden", s)
	}
	mode := os.FileMode(v & 0o777)
	if v&0o1000 != 0 {
		mode |= os.ModeSticky
	}
	return mode, nil
}
