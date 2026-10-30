// SPDX-License-Identifier: MIT

package podsession

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/provisioning/gitref"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/upload"
	"github.com/lennylabs/lenny/pkg/upload/archive"
)

// rewriteExtractedSources rewrites every §14 uploadArchive source and
// every gitClone source into the uploadFile / mkdir / symlink sources its
// extracted entries produce. uploadArchive blobs are fetched from the
// §4.5 blob store; gitClone repositories are cloned on the gateway's
// network path (so the pod never sees VCS credentials). Both are then
// decompressed inside the gateway's §4.1 Upload Handler subsystem
// (UploadGate), so the pod never sees the compressed bytes (§7.4; §13.4). Extracted file content is accumulated under
// synthetic refs in uploads so it rides the same PrepareWorkspace stream;
// directory and symlink entries become source records the adapter
// materializes without parsing untrusted input. A plan with no
// uploadArchive or gitClone source is returned unchanged. spec: §7.4; §13.4 — F-7.4.1, F-13.4.1.
func (b *Binder) rewriteExtractedSources(ctx context.Context, plan *adapterv1.WorkspacePlan, tenantID string, uploads map[string][]byte, allow upload.RuntimeAllow) (*adapterv1.WorkspacePlan, []*adapterv1.WorkspacePlanWarning, error) {
	needsRewrite := false
	for _, src := range plan.GetSources() {
		if t := src.GetType(); t == "uploadArchive" || t == "gitClone" {
			needsRewrite = true
			break
		}
	}
	if !needsRewrite {
		return plan, nil, nil
	}
	newSources := make([]*adapterv1.WorkspaceSource, 0, len(plan.GetSources()))
	var warnings []*adapterv1.WorkspacePlanWarning
	for i, src := range plan.GetSources() {
		var res *archive.Result
		var err error
		switch src.GetType() {
		case "uploadArchive":
			res, err = b.extractOneArchive(ctx, src, i, allow)
		case "gitClone":
			res, err = b.extractGitCloneSource(ctx, src, tenantID, i, allow.WorkspaceRoot)
		default:
			newSources = append(newSources, src)
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		var expanded []*adapterv1.WorkspaceSource
		expanded, warnings = appendExtracted(newSources, uploads, res, i, warnings)
		newSources = expanded
	}
	return &adapterv1.WorkspacePlan{
		SchemaVersion: plan.GetSchemaVersion(),
		Sources:       newSources,
		SetupCommands: plan.GetSetupCommands(),
	}, warnings, nil
}

// appendExtracted expands one extraction Result onto the source list. It
// lays directories first (so directory modes survive any implicit parent
// creation by a later file write), then files (whose content is staged
// under a synthetic ref), then symlinks (whose targets the gateway
// already validated). Returns the grown source slice and the accumulated
// warnings. spec: §7.4 — F-7.4.1.
func appendExtracted(sources []*adapterv1.WorkspaceSource, uploads map[string][]byte, res *archive.Result, sourceIndex int, warnings []*adapterv1.WorkspacePlanWarning) ([]*adapterv1.WorkspaceSource, []*adapterv1.WorkspacePlanWarning) {
	for _, d := range res.Dirs {
		sources = append(sources, &adapterv1.WorkspaceSource{Type: "mkdir", Path: d.Path, Mode: modeOctal(d.Mode)})
	}
	for n, f := range res.Files {
		ref := syntheticArchiveRef(sourceIndex, n)
		uploads[ref] = f.Content
		sources = append(sources, &adapterv1.WorkspaceSource{Type: "uploadFile", Path: f.Path, UploadRef: ref, Mode: modeOctal(f.Mode)})
	}
	for _, sl := range res.Symlinks {
		sources = append(sources, &adapterv1.WorkspaceSource{Type: "symlink", Path: sl.Path, LinkTarget: sl.Target})
	}
	return sources, append(warnings, archiveWarningsToProto(res.Warnings)...)
}

// extractOneArchive fetches an uploadArchive source's blob and decodes it
// inside the §4.1 Upload Handler subsystem gate, recording a §16.1
// extraction-abort metric for any §13.4 violation. F-7.4.1, F-7.4.11.
func (b *Binder) extractOneArchive(ctx context.Context, src *adapterv1.WorkspaceSource, sourceIndex int, allow upload.RuntimeAllow) (*archive.Result, error) {
	if b.Blobs == nil {
		return nil, fmt.Errorf("plan has an uploadArchive source but the binder has no blob store")
	}
	uri, err := blobstore.ParseURI(src.GetUploadRef())
	if err != nil {
		return nil, fmt.Errorf("parse uploadArchive ref %q: %w", src.GetUploadRef(), err)
	}
	_, rc, err := b.Blobs.Get(uri)
	if err != nil {
		return nil, fmt.Errorf("fetch uploadArchive %q: %w", src.GetUploadRef(), err)
	}
	data, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		return nil, fmt.Errorf("read uploadArchive %q: %w", src.GetUploadRef(), err)
	}
	res, err := b.gatedExtract(ctx, func() (*archive.Result, error) {
		return archive.Extract(data, src.GetFormat(), int(src.GetStripComponents()), sourceIndex, src.GetPath(), allow)
	})
	if err != nil {
		return nil, fmt.Errorf("extract uploadArchive %q: %w", src.GetUploadRef(), err)
	}
	return res, nil
}

// extractGitCloneSource clones a §14 gitClone repository on the gateway's
// network path (so the runtime never sees VCS credentials) and decodes
// the resulting gzip-tar inside the §4.1 Upload Handler subsystem gate,
// exactly as an uploadArchive. Git histories commonly carry symlinks, so
// gitClone opts in to symlinks unconditionally; every target is still
// resolved through pkg/upload.ValidateSymlinkTarget against the workspace
// root. spec: §7.4; §13.4; §14 — F-7.4.1.
func (b *Binder) extractGitCloneSource(ctx context.Context, src *adapterv1.WorkspaceSource, tenantID string, sourceIndex int, workspaceRoot string) (*archive.Result, error) {
	// §14: an authenticated clone resolves the §4.9 VCS
	// credential-lease token on the gateway and injects it into the fetch.
	// A public clone (no auth block) proceeds with a zero credential.
	var cred gitref.Credential
	if mode := src.GetAuth().GetMode(); mode != "" {
		if b.VCSCreds == nil {
			return nil, fmt.Errorf("gitClone of %q uses auth.mode=%q but no VCS credential resolver is wired", src.GetUrl(), mode)
		}
		c, err := b.VCSCreds.Resolve(ctx, tenantID, src.GetUrl(), src.GetAuth().GetLeaseScope())
		if err != nil {
			return nil, fmt.Errorf("resolve gitClone credential for %q: %w", src.GetUrl(), err)
		}
		cred = gitref.Credential{Username: c.Username, Token: c.Token}
	}
	repoArchive, err := gitref.CloneArchive(ctx, src.GetUrl(), src.GetResolvedCommitSha(),
		gitref.CloneOptions{Depth: int(src.GetDepth()), Submodules: src.GetSubmodules(), Credential: cred})
	if err != nil {
		return nil, fmt.Errorf("clone %q: %w", src.GetUrl(), err)
	}
	allow := upload.RuntimeAllow{AllowSymlinks: true, WorkspaceRoot: workspaceRoot}
	res, err := b.gatedExtract(ctx, func() (*archive.Result, error) {
		return archive.Extract(repoArchive, "tar.gz", 0, sourceIndex, src.GetPath(), allow)
	})
	if err != nil {
		return nil, fmt.Errorf("extract gitClone %q: %w", src.GetUrl(), err)
	}
	return res, nil
}

// gatedExtract runs one archive decode inside the §4.1 Upload Handler
// subsystem (UploadGate) so a hostile archive's decompression shares the
// upload path's goroutine pool, concurrency limiter, and circuit breaker
// and cannot starve session attachment or delegation. It records the
// §16.1 extraction-abort metric on any failure. A nil gate runs the
// decode directly (tests). spec: §7.4; §16.1 — F-7.4.1, F-7.4.11.
func (b *Binder) gatedExtract(ctx context.Context, fn func() (*archive.Result, error)) (*archive.Result, error) {
	var res *archive.Result
	do := func(context.Context) error {
		r, err := fn()
		if err != nil {
			return err
		}
		res = r
		return nil
	}
	var err error
	if b.UploadGate != nil {
		err = b.UploadGate.Do(ctx, do)
	} else {
		err = do(ctx)
	}
	if err != nil {
		b.recordExtractionAbort(err)
		return nil, err
	}
	return res, nil
}

// recordExtractionAbort increments lenny_upload_extraction_aborted_total
// for a §13.4 extraction failure, labeling by the typed sub-code when the
// error is a *upload.ValidationError and "format_error" otherwise. spec:
// §7.4; §16.1 — F-7.4.11.
func (b *Binder) recordExtractionAbort(err error) {
	if b.ExtractionAbort == nil {
		return
	}
	errorType := string(upload.ReasonFormatError)
	var vErr *upload.ValidationError
	if errors.As(err, &vErr) {
		errorType = string(vErr.Reason)
	}
	b.ExtractionAbort(errorType)
}

// syntheticArchiveRef is the PrepareWorkspace upload ref for one archive-
// extracted file. It is a plain token with no path separators (the
// adapter hashes it into the staging directory), unique across the plan,
// and namespaced so it never collides with a client-supplied blob ref.
func syntheticArchiveRef(sourceIndex, fileIndex int) string {
	return fmt.Sprintf("__archx_%d_%d", sourceIndex, fileIndex)
}

// modeOctal renders a file mode's permission bits as the octal string the
// adapter's mkdir / uploadFile materializer parses.
func modeOctal(mode os.FileMode) string {
	return "0" + strconv.FormatUint(uint64(mode.Perm()), 8)
}

// archiveWarningsToProto transcribes the gateway extractor's strip-skip
// warnings onto the proto warning surface the binder republishes on the
// session SSE stream. F-7.4.15.
func archiveWarningsToProto(ws []archive.Warning) []*adapterv1.WorkspacePlanWarning {
	if len(ws) == 0 {
		return nil
	}
	out := make([]*adapterv1.WorkspacePlanWarning, 0, len(ws))
	for _, w := range ws {
		out = append(out, &adapterv1.WorkspacePlanWarning{
			Code:            w.Code,
			SourceIndex:     int32(w.SourceIndex),
			EntryPath:       w.EntryPath,
			SegmentCount:    int32(w.SegmentCount),
			StripComponents: int32(w.StripComponents),
			Message:         w.Message,
		})
	}
	return out
}
