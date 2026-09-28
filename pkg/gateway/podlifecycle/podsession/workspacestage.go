// SPDX-License-Identifier: MIT

package podsession

import (
	"context"
	"fmt"
	"io"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/upload"
)

// stageWorkspace prepares the pod's staging area for the plan's
// non-filesystem-native sources, ahead of FinalizeWorkspace. It extracts
// every uploadArchive source — and every gitClone source's repository
// archive — inside the gateway (§7.4; §13.4 — the pod
// never decompresses external archives), rewriting each into the
// uploadFile / mkdir / symlink sources its already-validated entries
// produce; it fetches the blob content of every (original) uploadFile
// source from the §4.5 blob store; and it streams all of it to the pod
// via PrepareWorkspace. It returns the rewritten plan (which carries no
// uploadArchive or gitClone sources) and the §7.4
// strip-components-skip warnings the gateway raised during extraction. A
// plan that carries upload sources but binds through a Binder with no
// blob store fails rather than materializing an incomplete workspace.
//
// bindAttempt is the calling bind attempt's §4.7.1 token. PrepareWorkspace
// carries it with mid_session false, because stageWorkspace runs only on the
// bind sequence; the §7.4 mid-session upload sends its own pair.
func (b *Binder) stageWorkspace(ctx context.Context, cl *adapterclient.Client, sessionID, tenantID string, plan *adapterv1.WorkspacePlan, allow upload.RuntimeAllow, bindAttempt string) (*adapterv1.WorkspacePlan, []*adapterv1.WorkspacePlanWarning, error) {
	uploads := make(map[string][]byte)

	// §7.4 / §13.4 — extract uploadArchive and gitClone
	// sources in the gateway and rewrite them into pre-extracted
	// file/dir/symlink sources whose bytes ride the same PrepareWorkspace
	// stream.
	rewritten, warnings, err := b.rewriteExtractedSources(ctx, plan, tenantID, uploads, allow)
	if err != nil {
		return nil, nil, err
	}

	if refs := uploadFileRefs(rewritten); len(refs) > 0 {
		for _, ref := range refs {
			// Synthetic refs for archive-extracted files already carry
			// their content; only original client uploadFile refs resolve
			// through the blob store.
			if _, ok := uploads[ref]; ok {
				continue
			}
			if b.Blobs == nil {
				return nil, nil, fmt.Errorf("plan has upload source(s) but the binder has no blob store")
			}
			uri, err := blobstore.ParseURI(ref)
			if err != nil {
				return nil, nil, fmt.Errorf("parse upload ref %q: %w", ref, err)
			}
			_, rc, err := b.Blobs.Get(uri)
			if err != nil {
				return nil, nil, fmt.Errorf("fetch upload %q: %w", ref, err)
			}
			content, err := io.ReadAll(rc)
			_ = rc.Close()
			if err != nil {
				return nil, nil, fmt.Errorf("read upload %q: %w", ref, err)
			}
			uploads[ref] = content
		}
	}

	if len(uploads) > 0 {
		// spec: §6.4 — the uploads stage into the session's own
		// /workspace/slots/{sessionId}/staging area, whose identifier is the
		// session the request already names.
		if _, err := cl.PrepareWorkspace(ctx, sessionID, uploads, bindAttempt, false); err != nil {
			return nil, nil, err
		}
	}
	return rewritten, warnings, nil
}

// firstNonEmpty returns the first non-empty string in vs, or "".
func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// uploadFileRefs collects the distinct uploadRef values of the plan's
// uploadFile sources, in first-seen order. After archive rewriting the
// plan carries no uploadArchive sources, so only uploadFile refs need
// staging (originals through the blob store, synthetics in memory).
func uploadFileRefs(plan *adapterv1.WorkspacePlan) []string {
	seen := make(map[string]bool)
	var refs []string
	for _, src := range plan.GetSources() {
		if src.GetType() != "uploadFile" {
			continue
		}
		if ref := src.GetUploadRef(); ref != "" && !seen[ref] {
			seen[ref] = true
			refs = append(refs, ref)
		}
	}
	return refs
}
