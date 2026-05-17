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
	"os"
	"path/filepath"
	"strconv"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// ErrUnknownSourceType reports a workspace source whose type is not a
// recognized §14 source type.
var ErrUnknownSourceType = errors.New("unknown workspace source type")

// Materialize writes the workspace sources into root in order. It
// handles every §14 source type: inlineFile and mkdir write directly;
// uploadFile and uploadArchive extract content staged under stagingDir
// by PrepareWorkspace; gitClone extracts the repository archive the
// gateway cloned and staged under stagingDir.
func Materialize(root, stagingDir string, sources []*adapterv1.WorkspaceSource) error {
	for i, src := range sources {
		if err := materializeSource(root, stagingDir, src); err != nil {
			return fmt.Errorf("workspace source %d (type %q): %w", i, src.GetType(), err)
		}
	}
	return nil
}

func materializeSource(root, stagingDir string, src *adapterv1.WorkspaceSource) error {
	switch src.GetType() {
	case "inlineFile":
		return writeInlineFile(root, src)
	case "mkdir":
		return makeDir(root, src)
	case "uploadFile":
		return writeUploadFile(root, stagingDir, src)
	case "uploadArchive":
		return extractUploadArchive(root, stagingDir, src)
	case "gitClone":
		return extractGitClone(root, stagingDir, src)
	default:
		return ErrUnknownSourceType
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
