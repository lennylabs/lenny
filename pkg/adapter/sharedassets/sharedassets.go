// SPDX-License-Identifier: MIT

// Package sharedassets materializes the §6.4 concurrent-workspace
// read-only shared assets into a pod's /workspace/shared/ directory.
//
// The §6.4 layout reserves /workspace/shared/ for assets shared
// read-only across a concurrent-workspace pod's slots. The platform
// populates the directory once at warm time, before any slot is
// assigned; the runtime container then mounts it read-only so an agent
// write returns EROFS. This package carries the wire form the controller
// encodes a Runtime's inline shared assets into (Encode), the adapter
// decodes at startup (Decode), and the warm-time materializer the
// adapter runs against its read-write mount (Materialize).
//
// spec: §6.4 line 409 — F-6.4.3.
package sharedassets

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// FileSpec is one inline shared-asset file. It is the package-local
// mirror of the lennyv1.SharedAsset CRD entry, decoupled from the API
// types so both the controller and the adapter depend only on this
// small package.
type FileSpec struct {
	// Path is the destination path relative to the shared root. It must
	// be a relative path that does not escape the root through `..`.
	Path string `json:"path"`
	// Content is the inline file body. An empty value writes an empty
	// file.
	Content string `json:"content,omitempty"`
	// Mode is the octal file mode. An empty value defaults to 0444.
	Mode string `json:"mode,omitempty"`
}

// defaultMode is the §6.4 read-only file mode applied when a FileSpec
// leaves Mode empty. The runtime mounts /workspace/shared/ read-only, so
// the file itself is read-only too: the asset is immutable from every
// angle.
const defaultMode os.FileMode = 0o444

// Encode serializes specs into the compact, transport-safe form the
// pod spec carries to the adapter on the --shared-assets flag: a
// base64-encoded JSON array. An empty or nil slice encodes to the empty
// string so the controller can omit the flag entirely.
func Encode(specs []FileSpec) (string, error) {
	if len(specs) == 0 {
		return "", nil
	}
	data, err := json.Marshal(specs)
	if err != nil {
		return "", fmt.Errorf("encode shared assets: %w", err)
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

// Decode reverses Encode. The empty string decodes to a nil slice so an
// adapter started without the flag populates nothing.
func Decode(encoded string) ([]FileSpec, error) {
	if encoded == "" {
		return nil, nil
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode shared assets: %w", err)
	}
	var specs []FileSpec
	if err := json.Unmarshal(data, &specs); err != nil {
		return nil, fmt.Errorf("parse shared assets: %w", err)
	}
	return specs, nil
}

// Materialize writes each spec into dir as a read-only file. It is the
// warm-time populate step the adapter runs against its read-write mount
// of /workspace/shared/ before signalling READY. The adapter is a
// distinct trust boundary from the controller that produced the specs,
// so Materialize re-checks every path for containment within dir and
// rejects setuid/setgid modes rather than trusting the encoded input.
//
// Materialize is not idempotent against a changed asset set, but the
// platform populates once per warm pod, so a re-run with the same specs
// rewrites identical content.
func Materialize(dir string, specs []FileSpec) error {
	if dir == "" {
		return errors.New("sharedassets: shared root is empty")
	}
	for _, spec := range specs {
		if err := writeFile(dir, spec); err != nil {
			return fmt.Errorf("sharedassets: materialize %q: %w", spec.Path, err)
		}
	}
	return nil
}

func writeFile(root string, spec FileSpec) error {
	dst, err := resolvePath(root, spec.Path)
	if err != nil {
		return err
	}
	mode, err := parseMode(spec.Mode)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create parent directories: %w", err)
	}
	if err := os.WriteFile(dst, []byte(spec.Content), mode); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	// WriteFile honors the umask; pin the requested mode exactly so the
	// asset is read-only regardless of the inherited umask.
	if err := os.Chmod(dst, mode); err != nil {
		return fmt.Errorf("set file mode: %w", err)
	}
	return nil
}

// resolvePath joins rel onto root and confirms the result stays within
// root. It mirrors the workspace materializer's containment check: an
// empty path, an absolute path, or a path that escapes through `..` is
// rejected. spec: §6.4 — F-6.4.3.
func resolvePath(root, rel string) (string, error) {
	if rel == "" {
		return "", errors.New("asset path is empty")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute path %q is not permitted", rel)
	}
	rootClean := filepath.Clean(root)
	full := filepath.Join(rootClean, rel)
	if full != rootClean && !pathWithin(rootClean, full) {
		return "", fmt.Errorf("path %q escapes the shared root", rel)
	}
	return full, nil
}

// pathWithin reports whether child is nested under root.
func pathWithin(root, child string) bool {
	return len(child) > len(root) &&
		child[:len(root)] == root &&
		child[len(root)] == filepath.Separator
}

// parseMode parses an octal mode string. An empty string yields
// defaultMode. setuid and setgid modes are rejected; the sticky bit is
// preserved. It mirrors the workspace materializer's mode handling.
func parseMode(s string) (os.FileMode, error) {
	if s == "" {
		return defaultMode, nil
	}
	parsed, err := strconv.ParseUint(s, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid octal mode %q: %w", s, err)
	}
	v := uint32(parsed)
	if v&0o4000 != 0 || v&0o2000 != 0 {
		return 0, fmt.Errorf("mode %q sets setuid or setgid, which is forbidden", s)
	}
	mode := os.FileMode(v & 0o777)
	if v&0o1000 != 0 {
		mode |= os.ModeSticky
	}
	return mode, nil
}
