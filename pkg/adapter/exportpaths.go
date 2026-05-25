// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// globMeta are the glob metacharacters filepath.Match recognizes. The
// first source segment containing one of these marks the end of the
// export's wildcard-free base path (§8.7 "base path is stripped").
const globMeta = "*?["

// ExportPaths packages files from /workspace/current for delegation,
// rebased per the §8.7 file-export model. For each export it resolves
// the source glob inside the workspace root, walks any matched
// directory, strips the glob's base path, and re-roots each file under
// the export's dest_prefix. Matched files whose realpath escapes the
// workspace root via symlink are rejected (§8.7 validation). The
// gateway applies the lease's fileExportLimits and optional content
// scanning to the returned set.
//
// spec: §4.7 line 633, §8.7
func (s *Server) ExportPaths(_ context.Context, req *adapterv1.ExportPathsRequest) (*adapterv1.ExportPathsResponse, error) {
	if req.GetSessionId().GetValue() == "" {
		return nil, status.Error(codes.InvalidArgument, "ExportPaths requires a session id")
	}
	if s.WorkspaceRoot == "" {
		return nil, status.Error(codes.FailedPrecondition,
			"adapter is not configured with a workspace root")
	}
	// realRoot is the workspace root's own realpath. Every exported
	// file's resolved path must stay within it; resolving the root once
	// keeps the per-file symlink check comparing realpath to realpath
	// (spec: §8.7 — "rejects any file whose resolved path is outside the
	// workspace root").
	realRoot, err := filepath.EvalSymlinks(filepath.Clean(s.WorkspaceRoot))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "resolve workspace root: %v", err)
	}

	// §8.7 "Multiple exports ... applied in order; if paths overlap, later
	// entries overwrite earlier ones." order preserves first-seen
	// position; byPath holds the live (last-write-wins) content per path.
	var order []string
	byPath := map[string]*adapterv1.ExportedFile{}

	for _, spec := range req.GetExports() {
		destPrefix, err := cleanDestPrefix(spec.GetDestPrefix())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "%v", err)
		}
		base := exportBase(spec.GetSource())
		matches, err := filepath.Glob(filepath.Join(realRoot, filepath.FromSlash(spec.GetSource())))
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument,
				"invalid export source glob %q: %v", spec.GetSource(), err)
		}
		for _, match := range matches {
			if err := s.collectExport(realRoot, base, destPrefix, match, &order, byPath); err != nil {
				return nil, err
			}
		}
	}

	resp := &adapterv1.ExportPathsResponse{Files: make([]*adapterv1.ExportedFile, 0, len(order))}
	for _, p := range order {
		f := byPath[p]
		resp.Files = append(resp.Files, f)
		resp.TotalBytes += f.GetSizeBytes()
	}
	return resp, nil
}

// collectExport resolves match against the workspace realpath, rejecting
// symlink escapes, then walks directories and adds each regular file to
// order/byPath rebased relative to base under destPrefix. base is the
// workspace-relative wildcard-free prefix of the export source.
func (s *Server) collectExport(realRoot, base, destPrefix, match string, order *[]string, byPath map[string]*adapterv1.ExportedFile) error {
	info, err := os.Lstat(match)
	if err != nil {
		return status.Errorf(codes.Internal, "stat export match %q: %v", match, err)
	}
	if info.IsDir() {
		// §8.7: a glob that matches a directory contributes every regular
		// file beneath it (e.g., `./exports/export1/*` includes
		// `./exports/export1/lib/bar.ts`).
		return filepath.WalkDir(match, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return status.Errorf(codes.Internal, "walk export dir %q: %v", match, walkErr)
			}
			if d.IsDir() {
				return nil
			}
			return s.addExportFile(realRoot, base, destPrefix, p, order, byPath)
		})
	}
	return s.addExportFile(realRoot, base, destPrefix, match, order, byPath)
}

// addExportFile resolves path's realpath, rejects a symlink escape and
// any non-regular file, rebases the path relative to base under
// destPrefix, reads the bytes, and records the result with last-write
// semantics on path collision.
func (s *Server) addExportFile(realRoot, base, destPrefix, path string, order *[]string, byPath map[string]*adapterv1.ExportedFile) error {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return status.Errorf(codes.Internal, "resolve export path %q: %v", path, err)
	}
	// §8.7: reject any file whose resolved path leaves the workspace root
	// (e.g., a `./data -> /etc/passwd` symlink planted by the agent).
	if real != realRoot && !strings.HasPrefix(real, realRoot+string(filepath.Separator)) {
		return status.Errorf(codes.FailedPrecondition,
			"export source resolves outside workspace: %q", path)
	}
	rinfo, err := os.Stat(real)
	if err != nil {
		return status.Errorf(codes.Internal, "stat export path %q: %v", path, err)
	}
	// §8.7 inherits the §13.4 upload rules: only regular files cross the
	// boundary. Sockets, devices, and FIFOs are dropped.
	if !rinfo.Mode().IsRegular() {
		return status.Errorf(codes.FailedPrecondition,
			"export source is not a regular file: %q", path)
	}

	rel, err := filepath.Rel(filepath.Join(realRoot, base), path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return status.Errorf(codes.Internal, "rebase export path %q under base %q", path, base)
	}
	dest := filepath.ToSlash(filepath.Join(destPrefix, rel))

	content, err := os.ReadFile(real)
	if err != nil {
		return status.Errorf(codes.Internal, "read export path %q: %v", path, err)
	}
	sum := sha256.Sum256(content)
	file := &adapterv1.ExportedFile{
		Path:      dest,
		Content:   content,
		Sha256:    hex.EncodeToString(sum[:]),
		SizeBytes: int64(len(content)),
	}
	if _, seen := byPath[dest]; !seen {
		*order = append(*order, dest)
	}
	byPath[dest] = file
	return nil
}

// exportBase returns the wildcard-free base path of a §8.7 export
// source, cleaned and workspace-relative. For a glob it is the join of
// the leading segments before the first segment containing a glob
// metacharacter (`./exports/export1/*` → `exports/export1`); for a
// literal path it is the directory portion (`./results.json` → `.`),
// so rebasing strips the directory and leaves the file at the child
// workspace root.
func exportBase(source string) string {
	source = filepath.ToSlash(source)
	segs := strings.Split(filepath.Clean(source), "/")
	var prefix []string
	literal := true
	for _, seg := range segs {
		if strings.ContainsAny(seg, globMeta) {
			literal = false
			break
		}
		prefix = append(prefix, seg)
	}
	if literal {
		// No wildcard: strip the file's own directory.
		return filepath.Clean(filepath.Dir(filepath.Clean(source)))
	}
	if len(prefix) == 0 {
		return "."
	}
	return filepath.Clean(strings.Join(prefix, "/"))
}

// cleanDestPrefix validates and normalizes a §8.7 dest_prefix: it must
// be relative and contain no ".." traversal. An empty prefix places the
// rebased files at the child workspace root.
func cleanDestPrefix(prefix string) (string, error) {
	if prefix == "" {
		return "", nil
	}
	p := filepath.ToSlash(prefix)
	if filepath.IsAbs(p) {
		return "", &exportError{msg: "export destPrefix must be relative: " + prefix}
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", &exportError{msg: "export destPrefix must not escape the workspace: " + prefix}
	}
	return clean, nil
}

// exportError carries a §8.7 export-validation message into the gRPC
// status formatting at the call site.
type exportError struct{ msg string }

func (e *exportError) Error() string { return e.msg }
