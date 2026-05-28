// SPDX-License-Identifier: MIT

package workspace

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/upload"
)

// extractUploadArchive materializes a §14 uploadArchive source. The
// staged archive is decoded by format (tar, tar.gz, or zip) and its
// entries are written under the source pathPrefix (carried in the proto
// path field) relative to the workspace root. stripComponents leading
// path segments are dropped from each entry per §14.
//
// Every entry flows through pkg/upload.ValidateEntry so the §13.4
// per-entry ceilings (path length, path depth, per-entry size, kind,
// symlink target) fire fail-closed; the aggregate (entry count,
// decompressed bytes, decompression ratio) is checked via
// pkg/upload.ValidateArchive after the last entry. Symlinks are
// rejected unless archive.AllowSymlinks is true. Non-regular entries
// (hardlink, char-device, block-device, FIFO, socket) abort extraction
// per §7.4 line 457 with details.reason = "non_regular_entry".
//
// On any failure, every file/symlink written so far is removed before
// the error returns: the staging directory is restored to its
// pre-extraction state per §7.4 line 460.
//
// The returned []Warning carries the §7.4 line 459
// `workspace_plan_strip_components_skip` advisory: one entry per
// archive entry the strip-components rule discarded. spec: §7.4 lines
// 449-462; §13.4 — F-7.4.2, F-7.4.3, F-7.4.4, F-7.4.13, F-7.4.15.
func extractUploadArchive(root, stagingDir string, sourceIndex int, src *adapterv1.WorkspaceSource, archive ArchivePolicy) ([]Warning, error) {
	if stagingDir == "" {
		return nil, errors.New("uploadArchive source requires a staging directory")
	}
	staged, err := StagingPath(stagingDir, src.GetUploadRef())
	if err != nil {
		return nil, err
	}
	strip := int(src.GetStripComponents())
	if strip < 0 {
		return nil, fmt.Errorf("stripComponents %d is negative", strip)
	}
	prefix := src.GetPath()
	allow := upload.RuntimeAllow{
		AllowSymlinks: archive.AllowSymlinks,
		WorkspaceRoot: archive.WorkspaceRoot,
	}

	switch src.GetFormat() {
	case "tar":
		f, err := os.Open(staged)
		if err != nil {
			return nil, fmt.Errorf("open staged archive: %w", err)
		}
		defer f.Close()
		return extractUploadTar(root, prefix, strip, sourceIndex, f, &nopByteCounter{}, allow)
	case "tar.gz":
		f, err := os.Open(staged)
		if err != nil {
			return nil, fmt.Errorf("open staged archive: %w", err)
		}
		defer f.Close()
		// Wrap the underlying reader so the §13.4 100:1 decompression-
		// ratio check has compressed bytes to compare against; then wrap
		// the gzip output in a per-Read 1 MiB cap so the §7.4 line 451
		// "single read from allocating unbounded memory" defence fires.
		counter := &byteCounter{r: f}
		gz, err := gzip.NewReader(counter)
		if err != nil {
			return nil, fmt.Errorf("open gzip stream: %w", err)
		}
		defer gz.Close()
		capped := &readCap{r: gz, maxRead: decompressorPerReadCap}
		return extractUploadTar(root, prefix, strip, sourceIndex, capped, counter, allow)
	case "zip":
		return extractUploadZip(root, prefix, strip, sourceIndex, staged, allow)
	default:
		return nil, fmt.Errorf("unsupported archive format %q", src.GetFormat())
	}
}

// byteCounter is an io.Reader that records the total bytes read so the
// §13.4 decompression-ratio check (cumulative compressed bytes
// vs. cumulative uncompressed bytes) has the compressed numerator.
// spec: §7.4 line 451 — F-7.4.2.
type byteCounter struct {
	r io.Reader
	n int64
}

func (c *byteCounter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func (c *byteCounter) Compressed() int64 { return c.n }

// readCap is the §7.4 line 451 per-call decompressor size cap. It
// truncates the caller's buffer to at most maxRead bytes so a single
// Read from the gzip/deflate decoder cannot allocate unbounded memory
// even when fed a hostile stream. The default cap is 1 MiB. spec: §7.4
// line 451 — F-13.4.7.
const decompressorPerReadCap = 1 << 20

type readCap struct {
	r       io.Reader
	maxRead int
}

func (c *readCap) Read(p []byte) (int, error) {
	if c.maxRead > 0 && len(p) > c.maxRead {
		p = p[:c.maxRead]
	}
	return c.r.Read(p)
}

// nopByteCounter reports zero compressed bytes for archive formats
// where the ratio check is not meaningful (plain tar, zip — the per-
// entry uncompressed-vs-compressed check rides inside zip.Reader and
// is too granular to surface uniformly here). ValidateArchive skips the
// ratio rule when CompressedBytes is zero. spec: §7.4 line 451.
type nopByteCounter struct{}

func (nopByteCounter) Compressed() int64 { return 0 }

// compressedReporter is the small interface extractUploadTar uses to
// query the cumulative compressed-byte count after EOF.
type compressedReporter interface {
	Compressed() int64
}

// archiveSession tracks the paths an in-progress extraction has
// created so a mid-extraction failure can roll back the partial write.
// The §7.4 line 460 atomic-cleanup contract requires "all already-
// extracted files are removed from staging before the error is
// returned". spec: §7.4 line 460 — F-7.4.13.
type archiveSession struct {
	root     string
	written  []string
	madeDirs []string
}

func newArchiveSession(root string) *archiveSession {
	return &archiveSession{root: filepath.Clean(root)}
}

// trackWritten records a file or symlink the extractor created. The
// cleanup pass removes them in reverse order so leaf entries are
// retired before their parent directories. spec: §7.4 line 460.
func (s *archiveSession) trackWritten(path string) {
	s.written = append(s.written, path)
}

// trackDir records an interior directory the extractor created so the
// cleanup pass can remove it after its children are gone. Directories
// already present (e.g., the workspace root itself) are not tracked.
// spec: §7.4 line 460.
func (s *archiveSession) trackDir(path string) {
	if path == s.root || path == "" {
		return
	}
	s.madeDirs = append(s.madeDirs, path)
}

// rollback removes every file, symlink, and directory the session
// created, in reverse order. Removal errors are deliberately swallowed:
// the caller is already reporting a failure and the staging directory
// is best-effort restored. spec: §7.4 line 460 — F-7.4.13.
func (s *archiveSession) rollback() {
	for i := len(s.written) - 1; i >= 0; i-- {
		_ = os.Remove(s.written[i])
	}
	for i := len(s.madeDirs) - 1; i >= 0; i-- {
		_ = os.Remove(s.madeDirs[i]) // only removes if empty
	}
}

// ensureDir creates dir and every missing parent under session.root,
// tracking each newly-made directory so a later rollback can remove
// them. mkdirAll-without-tracking would leak partial directory trees on
// failure. spec: §7.4 line 460.
func (s *archiveSession) ensureDir(dir string, mode os.FileMode) error {
	dir = filepath.Clean(dir)
	if !pathWithin(s.root, dir) && dir != s.root {
		return fmt.Errorf("directory %q escapes the workspace root", dir)
	}
	// Walk upward to the first existing ancestor, then create downward.
	var toMake []string
	cur := dir
	for cur != s.root && cur != "" && cur != "/" {
		_, err := os.Lstat(cur)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		toMake = append(toMake, cur)
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	for i := len(toMake) - 1; i >= 0; i-- {
		if err := os.Mkdir(toMake[i], mode); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return err
		}
		s.trackDir(toMake[i])
	}
	return nil
}

func extractUploadTar(root, prefix string, strip, sourceIndex int, r io.Reader, compressed compressedReporter, allow upload.RuntimeAllow) ([]Warning, error) {
	tr := tar.NewReader(r)
	sess := newArchiveSession(root)
	var written int64
	var entryCount int
	var warnings []Warning
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			if vErr := upload.ValidateArchive(upload.Archive{
				CompressedBytes:   compressed.Compressed(),
				DecompressedBytes: written,
				EntryCount:        entryCount,
			}); vErr != nil {
				sess.rollback()
				return warnings, vErr
			}
			return warnings, nil
		}
		if err != nil {
			sess.rollback()
			return warnings, fmt.Errorf("read tar entry: %w", err)
		}
		rel, segCount, ok := stripPath(hdr.Name, strip)
		if !ok {
			// spec: §7.4 line 459 — an entry with too few segments after
			// stripComponents is skipped without aborting extraction and
			// emits a workspace_plan_strip_components_skip warning event
			// per skipped entry. spec: §14 line 100 — the warning carries
			// `sourceIndex`, `entryPath`, `segmentCount`, `stripComponents`.
			// F-7.4.15, F-14.1.18.
			warnings = append(warnings, Warning{
				Code:            stripComponentsSkipCode,
				SourceIndex:     sourceIndex,
				EntryPath:       hdr.Name,
				SegmentCount:    segCount,
				StripComponents: strip,
				Message:         fmt.Sprintf("entry has %d segment(s); fewer than stripComponents=%d", segCount, strip),
			})
			continue
		}
		validatedKind, abort := classifyTarKind(hdr.Typeflag)
		// spec: §7.4 lines 449-462; §13.4 — §13.4 per-entry ceilings
		// (path length, path depth, per-entry size, non-regular kind,
		// symlink target) fire before any filesystem write. The
		// post-strip path is what extraction will actually create, so it
		// is the path the validator inspects. F-7.4.2 / F-7.4.3 /
		// F-7.4.4.
		if !abort {
			if vErr := upload.ValidateEntry(upload.Entry{
				Path:       filepath.ToSlash(filepath.Join(prefix, rel)),
				Kind:       validatedKind,
				Size:       hdr.Size,
				LinkTarget: hdr.Linkname,
			}, allow); vErr != nil {
				sess.rollback()
				return warnings, vErr
			}
		} else {
			// non_regular_entry abort fast-path: emit the §13.4 typed
			// error without re-running ValidateEntry, which would
			// double-report. F-7.4.3.
			sess.rollback()
			return warnings, &upload.ValidationError{
				Reason: upload.ReasonNonRegularEntry,
				Path:   hdr.Name,
				Detail: fmt.Sprintf("entry kind %q is forbidden by §13.4", validatedKind),
			}
		}
		entryCount++

		dst, err := resolvePath(root, filepath.Join(prefix, rel))
		if err != nil {
			sess.rollback()
			return warnings, err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := sess.ensureDir(dst, 0o755); err != nil {
				sess.rollback()
				return warnings, fmt.Errorf("create archive directory: %w", err)
			}
		case tar.TypeReg:
			if err := sess.ensureDir(filepath.Dir(dst), 0o755); err != nil {
				sess.rollback()
				return warnings, fmt.Errorf("create parent directory: %w", err)
			}
			n, err := extractRegular(dst, tr, archiveMode(os.FileMode(hdr.Mode)), maxExtractBytes-written)
			written += n
			if err != nil {
				sess.rollback()
				return warnings, err
			}
			sess.trackWritten(dst)
		case tar.TypeSymlink:
			// ValidateEntry already enforced the §13.4 symlink target
			// invariants; create the link with the original linkname so
			// the target is preserved on disk.
			if err := sess.ensureDir(filepath.Dir(dst), 0o755); err != nil {
				sess.rollback()
				return warnings, fmt.Errorf("create parent directory: %w", err)
			}
			if err := os.Symlink(hdr.Linkname, dst); err != nil {
				sess.rollback()
				return warnings, fmt.Errorf("create symlink: %w", err)
			}
			sess.trackWritten(dst)
		}
	}
}

func extractUploadZip(root, prefix string, strip, sourceIndex int, archivePath string, allow upload.RuntimeAllow) ([]Warning, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, fmt.Errorf("open zip archive: %w", err)
	}
	defer zr.Close()
	// spec: §13.4 — the zip compressed/uncompressed totals are walked
	// from the central directory so the §13.4 100:1 ratio check has its
	// numerator. F-7.4.2.
	var compressedTotal int64
	for _, e := range zr.File {
		compressedTotal += int64(e.CompressedSize64)
	}
	sess := newArchiveSession(root)
	var written int64
	var entryCount int
	var warnings []Warning
	for _, entry := range zr.File {
		rel, segCount, ok := stripPath(entry.Name, strip)
		if !ok {
			// spec: §7.4 line 459 — strip-components skip per entry.
			// spec: §14 line 100 — `sourceIndex`, `entryPath`,
			// `segmentCount`, `stripComponents`. F-7.4.15, F-14.1.18.
			warnings = append(warnings, Warning{
				Code:            stripComponentsSkipCode,
				SourceIndex:     sourceIndex,
				EntryPath:       entry.Name,
				SegmentCount:    segCount,
				StripComponents: strip,
				Message:         fmt.Sprintf("entry has %d segment(s); fewer than stripComponents=%d", segCount, strip),
			})
			continue
		}
		kind, abort := classifyZipKind(entry)
		if abort {
			sess.rollback()
			return warnings, &upload.ValidationError{
				Reason: upload.ReasonNonRegularEntry,
				Path:   entry.Name,
				Detail: fmt.Sprintf("entry kind %q is forbidden by §13.4", kind),
			}
		}
		// Zip files do not carry an explicit hardlink/device kind; the
		// classifyZipKind helper resolves to the four supported kinds
		// (directory, regular, symlink, non-regular fallthrough).
		linkTarget := ""
		if kind == upload.KindSymlink {
			t, err := readZipSymlink(entry)
			if err != nil {
				sess.rollback()
				return warnings, fmt.Errorf("read zip symlink target: %w", err)
			}
			linkTarget = t
		}
		if vErr := upload.ValidateEntry(upload.Entry{
			Path:       filepath.ToSlash(filepath.Join(prefix, rel)),
			Kind:       kind,
			Size:       int64(entry.UncompressedSize64),
			LinkTarget: linkTarget,
		}, allow); vErr != nil {
			sess.rollback()
			return warnings, vErr
		}
		entryCount++

		dst, err := resolvePath(root, filepath.Join(prefix, rel))
		if err != nil {
			sess.rollback()
			return warnings, err
		}
		switch kind {
		case upload.KindDirectory:
			if err := sess.ensureDir(dst, 0o755); err != nil {
				sess.rollback()
				return warnings, fmt.Errorf("create archive directory: %w", err)
			}
		case upload.KindRegular:
			if err := sess.ensureDir(filepath.Dir(dst), 0o755); err != nil {
				sess.rollback()
				return warnings, fmt.Errorf("create parent directory: %w", err)
			}
			rc, err := entry.Open()
			if err != nil {
				sess.rollback()
				return warnings, fmt.Errorf("open zip entry: %w", err)
			}
			n, err := extractRegular(dst, rc, archiveMode(entry.Mode()), maxExtractBytes-written)
			_ = rc.Close()
			written += n
			if err != nil {
				sess.rollback()
				return warnings, err
			}
			sess.trackWritten(dst)
		case upload.KindSymlink:
			if err := sess.ensureDir(filepath.Dir(dst), 0o755); err != nil {
				sess.rollback()
				return warnings, fmt.Errorf("create parent directory: %w", err)
			}
			if err := os.Symlink(linkTarget, dst); err != nil {
				sess.rollback()
				return warnings, fmt.Errorf("create symlink: %w", err)
			}
			sess.trackWritten(dst)
		}
	}
	if vErr := upload.ValidateArchive(upload.Archive{
		CompressedBytes:   compressedTotal,
		DecompressedBytes: written,
		EntryCount:        entryCount,
	}); vErr != nil {
		sess.rollback()
		return warnings, vErr
	}
	return warnings, nil
}

// classifyTarKind maps a tar.Header.Typeflag onto the §13.4 EntryKind
// the validator understands. abort is true when the typeflag is in the
// §7.4 line 457 outright-rejected list (hardlink, char-device, block-
// device, FIFO, socket). spec: §7.4 line 457; §13.4 — F-7.4.3.
func classifyTarKind(typeflag byte) (upload.EntryKind, bool) {
	switch typeflag {
	case tar.TypeReg, tar.TypeRegA:
		return upload.KindRegular, false
	case tar.TypeDir:
		return upload.KindDirectory, false
	case tar.TypeSymlink:
		return upload.KindSymlink, false
	case tar.TypeLink:
		return upload.KindHardlink, true
	case tar.TypeChar:
		return upload.KindCharDevice, true
	case tar.TypeBlock:
		return upload.KindBlockDevice, true
	case tar.TypeFifo:
		return upload.KindFIFO, true
	default:
		// tar.TypeXHeader, TypeXGlobalHeader, TypeGNUSparse, etc. are
		// metadata; let archive/tar consume them silently by reporting
		// "regular" so ValidateEntry processes them. A genuinely
		// unknown entry kind that escapes archive/tar's parser would
		// surface here with abort=true to err on the safe side.
		return upload.EntryKind(fmt.Sprintf("tar_typeflag_%d", typeflag)), true
	}
}

// classifyZipKind maps a zip entry onto the §13.4 EntryKind. zip does
// not natively encode hardlinks or device files, but unix-mode bits on
// the entry can declare them; abort is true when the mode signals a
// forbidden kind. spec: §7.4 line 457; §13.4 — F-7.4.3.
func classifyZipKind(entry *zip.File) (upload.EntryKind, bool) {
	mode := entry.Mode()
	if entry.FileInfo().IsDir() {
		return upload.KindDirectory, false
	}
	switch {
	case mode&os.ModeSymlink != 0:
		return upload.KindSymlink, false
	case mode&os.ModeDevice != 0 && mode&os.ModeCharDevice != 0:
		return upload.KindCharDevice, true
	case mode&os.ModeDevice != 0:
		return upload.KindBlockDevice, true
	case mode&os.ModeNamedPipe != 0:
		return upload.KindFIFO, true
	case mode&os.ModeSocket != 0:
		return upload.KindSocket, true
	}
	if mode.IsRegular() {
		return upload.KindRegular, false
	}
	// Anything else (irregular-but-not-recognized) — fail closed.
	return upload.EntryKind(fmt.Sprintf("zip_mode_%v", mode)), true
}

// readZipSymlink decodes the link target stored as the body of a zip
// symlink entry. Zip records the target as the entry's regular body.
func readZipSymlink(entry *zip.File) (string, error) {
	rc, err := entry.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	// Bound the read so a hostile symlink cannot stream gigabytes of
	// "target" bytes. §13.4 caps the symlink path at the per-entry
	// path-length ceiling; reading one extra byte detects oversize.
	limited := io.LimitReader(rc, int64(upload.MaxPathLength)+1)
	b, err := io.ReadAll(limited)
	if err != nil {
		return "", err
	}
	if len(b) > upload.MaxPathLength {
		return "", &upload.ValidationError{Reason: upload.ReasonMaxPathLength, Path: entry.Name, Detail: "symlink target exceeds path length cap"}
	}
	return string(b), nil
}

// stripComponentsSkipCode is the §14 closed-enum WarningCode for the
// §7.4 line 459 "entries with fewer than N segments are skipped"
// advisory. The string matches pkg/workspaceplan.WarnStripComponentsSkip
// so a future deduplication can drop one of the two definitions.
// F-7.4.15.
const stripComponentsSkipCode = "workspace_plan_strip_components_skip"

// archiveMode reduces an archive entry mode to its permission bits,
// dropping setuid, setgid, and sticky bits. A zero mode (an archive
// entry recorded without permission metadata) falls back to 0644.
func archiveMode(mode os.FileMode) os.FileMode {
	perm := mode.Perm()
	if perm == 0 {
		return 0o644
	}
	return perm
}

// stripPath drops the first n path segments from an archive entry path
// and returns the remainder joined with "/" along with the
// pre-stripping segment count (after trimming leading/trailing empty
// segments). ok is false when the entry has n or fewer segments: per
// §14 such an entry is skipped rather than failing extraction. The
// returned segment count rides on the
// `workspace_plan_strip_components_skip` warning per §14 line 100.
// F-14.1.18.
func stripPath(entryPath string, n int) (string, int, bool) {
	trimmed := strings.Trim(entryPath, "/")
	if trimmed == "" {
		return "", 0, false
	}
	parts := strings.Split(trimmed, "/")
	segCount := len(parts)
	if segCount <= n {
		return "", segCount, false
	}
	rest := strings.Join(parts[n:], "/")
	if rest == "" {
		return "", segCount, false
	}
	return rest, segCount, true
}
