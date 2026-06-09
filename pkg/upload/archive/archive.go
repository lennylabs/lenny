// SPDX-License-Identifier: MIT

// Package archive decompresses and validates an uploaded workspace
// archive entirely inside the gateway's §4.1 Upload Handler subsystem.
//
// spec: §7.4 line 448 — "All archive extraction runs inside the
// gateway's Upload Handler subsystem; it is never delegated to agent
// pods." spec: §13.4 line 652 — "pod binaries neither decompress
// archives nor canonicalize paths on untrusted input." The gateway
// reads the archive bytes, streams them through the tar/zip parser,
// feeds every entry through pkg/upload.ValidateEntry / ValidateArchive,
// canonicalizes each entry path, and returns a manifest of
// pre-extracted, already-validated files, directories, and symlinks the
// caller hands the pod as ordinary uploadFile/mkdir/symlink sources. The
// pod never sees the compressed bytes. F-7.4.1, F-13.4.1.
//
// Extraction is in-memory: the §13.4 256 MiB decompressed-size ceiling
// bounds the working set, and a failure discards the partial result
// without touching any filesystem, so the §7.4 line 460 "all already-
// extracted files are removed; the staging directory is returned to its
// pre-extraction state" contract is satisfied trivially. F-7.4.13.
package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/lennylabs/lenny/pkg/upload"
)

// DefaultWorkspaceRoot is the §13.4 canonical workspace root symlink
// targets are validated against when the per-Runtime ArchivePolicy does
// not name one. spec: §7.4 line 458; §13.4 line 665.
const DefaultWorkspaceRoot = "/workspace/current"

// StripComponentsSkipCode is the §14 closed-enum WarningCode for the
// §7.4 line 459 "entries with fewer than N segments are skipped"
// advisory. It matches pkg/workspaceplan.WarnStripComponentsSkip so the
// gateway can republish the warning on the session SSE stream. F-7.4.15.
const StripComponentsSkipCode = "workspace_plan_strip_components_skip"

// decompressorPerReadCap is the §7.4 line 451 per-call decompressor size
// cap: a single Read from the gzip/deflate decoder cannot allocate more
// than 1 MiB even when fed a hostile stream.
const decompressorPerReadCap = 1 << 20

// File is one extracted regular file. Path is the workspace-relative
// slash path with the source pathPrefix and stripComponents already
// applied; Content holds the decompressed bytes.
type File struct {
	Path    string
	Mode    os.FileMode
	Content []byte
}

// Dir is one extracted directory entry. Empty directories and explicit
// directory modes are preserved through the manifest so the pod-side
// materialization reproduces them.
type Dir struct {
	Path string
	Mode os.FileMode
}

// Symlink is one extracted symlink entry. Target is the link target
// exactly as recorded in the archive; the gateway has already validated
// it against the workspace root via pkg/upload.ValidateSymlinkTarget.
type Symlink struct {
	Path   string
	Target string
}

// Warning is one non-fatal §14 advisory raised during extraction (the
// §7.4 line 459 strip-components skip is the only v1 producer). The
// fields mirror the proto WorkspacePlanWarning. spec: §14 line 100.
type Warning struct {
	Code            string
	SourceIndex     int
	EntryPath       string
	SegmentCount    int
	StripComponents int
	Message         string
}

// Result is the validated, canonicalized manifest of an extracted
// archive. Directories precede files which precede symlinks so a caller
// laying them down in order materializes parents before children and
// sets directory modes before any implicit parent creation.
type Result struct {
	Files    []File
	Dirs     []Dir
	Symlinks []Symlink
	Warnings []Warning
}

// Extract decompresses an in-memory archive and validates every entry
// against the §13.4 ceilings, returning the canonicalized manifest. It
// never touches the filesystem. format is one of "tar", "tar.gz", or
// "zip"; strip drops that many leading path segments from each entry;
// prefix is the source pathPrefix prepended to every entry; sourceIndex
// labels the strip-skip warnings. On any §13.4 violation it returns a
// *upload.ValidationError whose Reason carries the §15.1 sub-code and
// the §16.1 lenny_upload_extraction_aborted_total{error_type} label.
// spec: §7.4 lines 449-462; §13.4 — F-7.4.1, F-13.4.1.
func Extract(data []byte, format string, strip, sourceIndex int, prefix string, allow upload.RuntimeAllow) (*Result, error) {
	if strip < 0 {
		return nil, &upload.ValidationError{Reason: upload.ReasonFormatError, Detail: fmt.Sprintf("stripComponents %d is negative", strip)}
	}
	switch format {
	case "tar":
		return extractTar(bytes.NewReader(data), nopCounter{}, strip, sourceIndex, prefix, allow)
	case "tar.gz":
		// Count the compressed bytes so the §13.4 100:1 decompression-
		// ratio check has its numerator, then cap each decompressor Read
		// at 1 MiB per §7.4 line 451.
		counter := &byteCounter{r: bytes.NewReader(data)}
		gz, err := gzip.NewReader(counter)
		if err != nil {
			return nil, &upload.ValidationError{Reason: upload.ReasonFormatError, Detail: fmt.Sprintf("open gzip stream: %v", err)}
		}
		defer gz.Close()
		capped := &readCap{r: gz, maxRead: decompressorPerReadCap}
		return extractTar(capped, counter, strip, sourceIndex, prefix, allow)
	case "zip":
		return extractZip(data, strip, sourceIndex, prefix, allow)
	default:
		return nil, &upload.ValidationError{Reason: upload.ReasonFormatError, Detail: fmt.Sprintf("unsupported archive format %q", format)}
	}
}

func extractTar(r io.Reader, compressed compressedReporter, strip, sourceIndex int, prefix string, allow upload.RuntimeAllow) (*Result, error) {
	tr := tar.NewReader(r)
	b := newBuilder()
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, &upload.ValidationError{Reason: upload.ReasonFormatError, Detail: fmt.Sprintf("read tar entry: %v", err)}
		}
		rel, segCount, ok := stripPath(hdr.Name, strip)
		if !ok {
			b.warn(sourceIndex, hdr.Name, segCount, strip)
			continue
		}
		kind, abort := classifyTarKind(hdr.Typeflag)
		if abort {
			return nil, &upload.ValidationError{Reason: upload.ReasonNonRegularEntry, Path: hdr.Name, Detail: fmt.Sprintf("entry kind %q is forbidden by §13.4", kind)}
		}
		rawPath, cleanPath := joinPath(prefix, rel)
		if vErr := upload.ValidateEntry(upload.Entry{Path: rawPath, Kind: kind, Size: hdr.Size, LinkTarget: hdr.Linkname}, allow); vErr != nil {
			return nil, vErr
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			b.dir(cleanPath, archiveMode(os.FileMode(hdr.Mode), 0o755))
		case tar.TypeReg, tar.TypeRegA:
			content, err := readEntry(tr, b.written)
			if err != nil {
				return nil, err
			}
			b.file(cleanPath, archiveMode(os.FileMode(hdr.Mode), 0o644), content)
		case tar.TypeSymlink:
			b.symlink(cleanPath, hdr.Linkname)
		}
	}
	if vErr := upload.ValidateArchive(upload.Archive{
		CompressedBytes:   compressed.Compressed(),
		DecompressedBytes: b.written,
		EntryCount:        b.entryCount,
	}); vErr != nil {
		return nil, vErr
	}
	return b.result(), nil
}

func extractZip(data []byte, strip, sourceIndex int, prefix string, allow upload.RuntimeAllow) (*Result, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, &upload.ValidationError{Reason: upload.ReasonFormatError, Detail: fmt.Sprintf("open zip archive: %v", err)}
	}
	// The zip compressed totals are read from the central directory so the
	// §13.4 100:1 ratio check has its numerator.
	var compressedTotal int64
	for _, e := range zr.File {
		compressedTotal += int64(e.CompressedSize64)
	}
	b := newBuilder()
	for _, entry := range zr.File {
		rel, segCount, ok := stripPath(entry.Name, strip)
		if !ok {
			b.warn(sourceIndex, entry.Name, segCount, strip)
			continue
		}
		kind, abort := classifyZipKind(entry)
		if abort {
			return nil, &upload.ValidationError{Reason: upload.ReasonNonRegularEntry, Path: entry.Name, Detail: fmt.Sprintf("entry kind %q is forbidden by §13.4", kind)}
		}
		linkTarget := ""
		if kind == upload.KindSymlink {
			t, err := readZipSymlink(entry)
			if err != nil {
				return nil, err
			}
			linkTarget = t
		}
		rawPath, cleanPath := joinPath(prefix, rel)
		if vErr := upload.ValidateEntry(upload.Entry{Path: rawPath, Kind: kind, Size: int64(entry.UncompressedSize64), LinkTarget: linkTarget}, allow); vErr != nil {
			return nil, vErr
		}
		switch kind {
		case upload.KindDirectory:
			b.dir(cleanPath, archiveMode(entry.Mode(), 0o755))
		case upload.KindRegular:
			rc, err := entry.Open()
			if err != nil {
				return nil, &upload.ValidationError{Reason: upload.ReasonFormatError, Path: entry.Name, Detail: fmt.Sprintf("open zip entry: %v", err)}
			}
			// spec: §7.4 line 451 — cap each deflate Read at 1 MiB.
			content, rerr := readEntry(&readCap{r: rc, maxRead: decompressorPerReadCap}, b.written)
			_ = rc.Close()
			if rerr != nil {
				return nil, rerr
			}
			b.file(cleanPath, archiveMode(entry.Mode(), 0o644), content)
		case upload.KindSymlink:
			b.symlink(cleanPath, linkTarget)
		}
	}
	if vErr := upload.ValidateArchive(upload.Archive{
		CompressedBytes:   compressedTotal,
		DecompressedBytes: b.written,
		EntryCount:        b.entryCount,
	}); vErr != nil {
		return nil, vErr
	}
	return b.result(), nil
}

// readEntry reads one archive entry's decompressed content into memory,
// enforcing the §13.4 64 MiB per-entry ceiling and the running 256 MiB
// archive-total ceiling so extraction aborts immediately when either is
// crossed rather than buffering the whole bomb and checking at the end
// ("no extract then check", §7.4). spec: §7.4 lines 449-456.
func readEntry(r io.Reader, written int64) ([]byte, error) {
	remainingTotal := upload.MaxDecompressedSize - written
	if remainingTotal < 0 {
		remainingTotal = 0
	}
	limit := upload.MaxPerEntrySize
	if remainingTotal < limit {
		limit = remainingTotal
	}
	// Admit one byte past the limit so an over-budget entry is detected
	// rather than silently truncated.
	buf, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, &upload.ValidationError{Reason: upload.ReasonFormatError, Detail: fmt.Sprintf("read archive entry: %v", err)}
	}
	n := int64(len(buf))
	if n > upload.MaxPerEntrySize {
		return nil, &upload.ValidationError{Reason: upload.ReasonMaxEntrySize, Detail: fmt.Sprintf("entry size %d exceeds maximum %d", n, upload.MaxPerEntrySize)}
	}
	if written+n > upload.MaxDecompressedSize {
		return nil, &upload.ValidationError{Reason: upload.ReasonMaxDecompressedSize, Detail: fmt.Sprintf("decompressed bytes exceed maximum %d", upload.MaxDecompressedSize)}
	}
	return buf, nil
}

// builder accumulates the manifest, deduplicating entries that resolve
// to the same path within one archive (last-writer-wins, mirroring the
// §14 within-source overwrite semantics so an intra-archive duplicate
// does not raise a cross-source collision warning later).
type builder struct {
	files      []File
	dirs       []Dir
	symlinks   []Symlink
	warnings   []Warning
	written    int64
	entryCount int
	fileIdx    map[string]int
	dirIdx     map[string]int
	symIdx     map[string]int
}

func newBuilder() *builder {
	return &builder{fileIdx: map[string]int{}, dirIdx: map[string]int{}, symIdx: map[string]int{}}
}

func (b *builder) dir(p string, mode os.FileMode) {
	b.entryCount++
	if i, ok := b.dirIdx[p]; ok {
		b.dirs[i].Mode = mode
		return
	}
	b.dirIdx[p] = len(b.dirs)
	b.dirs = append(b.dirs, Dir{Path: p, Mode: mode})
}

func (b *builder) file(p string, mode os.FileMode, content []byte) {
	b.entryCount++
	b.written += int64(len(content))
	if i, ok := b.fileIdx[p]; ok {
		b.files[i].Mode = mode
		b.files[i].Content = content
		return
	}
	b.fileIdx[p] = len(b.files)
	b.files = append(b.files, File{Path: p, Mode: mode, Content: content})
}

func (b *builder) symlink(p, target string) {
	b.entryCount++
	if i, ok := b.symIdx[p]; ok {
		b.symlinks[i].Target = target
		return
	}
	b.symIdx[p] = len(b.symlinks)
	b.symlinks = append(b.symlinks, Symlink{Path: p, Target: target})
}

func (b *builder) warn(sourceIndex int, entryPath string, segCount, strip int) {
	// spec: §7.4 line 459 — an entry with too few segments after
	// stripComponents is skipped without aborting and emits one
	// workspace_plan_strip_components_skip warning per skipped entry.
	// spec: §14 line 100 — the warning carries sourceIndex, entryPath,
	// segmentCount, stripComponents. F-7.4.15.
	b.warnings = append(b.warnings, Warning{
		Code:            StripComponentsSkipCode,
		SourceIndex:     sourceIndex,
		EntryPath:       entryPath,
		SegmentCount:    segCount,
		StripComponents: strip,
		Message:         fmt.Sprintf("entry has %d segment(s); fewer than stripComponents=%d", segCount, strip),
	})
}

func (b *builder) result() *Result {
	return &Result{Files: b.files, Dirs: b.dirs, Symlinks: b.symlinks, Warnings: b.warnings}
}

// joinPath prepends the source pathPrefix to a stripped entry path. It
// returns the raw (uncleaned) join for ValidateEntry — which rejects any
// `..` segment that path.Clean would otherwise absorb — and the cleaned
// slash path for the manifest. An empty or "." prefix unpacks the entry
// at the workspace root (the §8.7 root-level export case).
func joinPath(prefix, rel string) (raw, clean string) {
	prefix = strings.Trim(prefix, "/")
	if prefix == "" || prefix == "." {
		return rel, path.Clean(rel)
	}
	raw = prefix + "/" + rel
	return raw, path.Clean(raw)
}

// archiveMode reduces an archive entry mode to its permission bits,
// dropping setuid, setgid, and sticky bits. A zero mode (an entry with
// no permission metadata) falls back to def.
func archiveMode(mode os.FileMode, def os.FileMode) os.FileMode {
	perm := mode.Perm()
	if perm == 0 {
		return def
	}
	return perm
}

// stripPath drops the first n path segments from an archive entry path,
// returning the remainder joined with "/", the pre-strip segment count,
// and ok=false when the entry has n or fewer segments (the §14
// strip-skip case). F-7.4.15.
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

// classifyTarKind maps a tar typeflag onto the §13.4 EntryKind. abort is
// true for the §7.4 line 457 outright-rejected kinds (hardlink, char-
// device, block-device, FIFO) and for any unknown typeflag. spec: §7.4
// line 457 — F-7.4.3.
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
		// tar.TypeXHeader / TypeXGlobalHeader / GNU sparse metadata are
		// consumed by archive/tar before reaching here; any residual
		// unknown typeflag fails closed.
		return upload.EntryKind(fmt.Sprintf("tar_typeflag_%d", typeflag)), true
	}
}

// classifyZipKind maps a zip entry onto the §13.4 EntryKind. zip does not
// natively encode hardlinks or device files, but unix-mode bits can
// declare them; abort is true when the mode signals a forbidden kind.
// spec: §7.4 line 457 — F-7.4.3.
func classifyZipKind(entry *zip.File) (upload.EntryKind, bool) {
	if entry.FileInfo().IsDir() {
		return upload.KindDirectory, false
	}
	mode := entry.Mode()
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
	return upload.EntryKind(fmt.Sprintf("zip_mode_%v", mode)), true
}

// readZipSymlink decodes the link target stored as the body of a zip
// symlink entry, bounded by the §13.4 path-length ceiling.
func readZipSymlink(entry *zip.File) (string, error) {
	rc, err := entry.Open()
	if err != nil {
		return "", &upload.ValidationError{Reason: upload.ReasonFormatError, Path: entry.Name, Detail: fmt.Sprintf("open zip symlink: %v", err)}
	}
	defer rc.Close()
	limited := io.LimitReader(rc, int64(upload.MaxPathLength)+1)
	b, err := io.ReadAll(limited)
	if err != nil {
		return "", &upload.ValidationError{Reason: upload.ReasonFormatError, Path: entry.Name, Detail: fmt.Sprintf("read zip symlink target: %v", err)}
	}
	if len(b) > upload.MaxPathLength {
		return "", &upload.ValidationError{Reason: upload.ReasonMaxPathLength, Path: entry.Name, Detail: "symlink target exceeds path length cap"}
	}
	return string(b), nil
}

// compressedReporter reports the cumulative compressed bytes a stream has
// consumed so the §13.4 ratio check has its numerator after EOF.
type compressedReporter interface {
	Compressed() int64
}

// byteCounter records the total bytes read from the underlying stream.
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

// nopCounter reports zero compressed bytes for plain tar, where the
// ratio rule is not meaningful (ValidateArchive skips it when
// CompressedBytes is zero).
type nopCounter struct{}

func (nopCounter) Compressed() int64 { return 0 }

// readCap is the §7.4 line 451 per-call decompressor size cap: it
// truncates the caller's buffer so a single Read cannot allocate more
// than maxRead bytes.
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
