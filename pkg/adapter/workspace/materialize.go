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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// ErrSourceUnsupported reports a workspace source whose type is valid
// but not yet materialized by the adapter. uploadFile and uploadArchive
// require upload-content delivery; gitClone requires a VCS client.
var ErrSourceUnsupported = errors.New("workspace source type not yet supported by the adapter")

// ErrUnknownSourceType reports a workspace source whose type is not a
// recognized §14 source type.
var ErrUnknownSourceType = errors.New("unknown workspace source type")

// Materialize writes the workspace sources into root in order. It
// handles the filesystem-native source types inlineFile and mkdir;
// uploadFile, uploadArchive, and gitClone return ErrSourceUnsupported
// until the upload-delivery and VCS layers land.
func Materialize(root string, sources []*adapterv1.WorkspaceSource) error {
	for i, src := range sources {
		if err := materializeSource(root, src); err != nil {
			return fmt.Errorf("workspace source %d (type %q): %w", i, src.GetType(), err)
		}
	}
	return nil
}

func materializeSource(root string, src *adapterv1.WorkspaceSource) error {
	switch src.GetType() {
	case "inlineFile":
		return writeInlineFile(root, src)
	case "mkdir":
		return makeDir(root, src)
	case "uploadFile", "uploadArchive", "gitClone":
		return ErrSourceUnsupported
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
