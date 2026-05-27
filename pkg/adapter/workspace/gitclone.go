// SPDX-License-Identifier: MIT

package workspace

import (
	"compress/gzip"
	"errors"
	"fmt"
	"os"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/upload"
)

// GitCloneStagingRef is the staging-area key for a §14 gitClone
// source's repository archive. Per §14 the gateway clones the
// repository on its own network path and streams the resulting tree to
// the staging area under this ref via PrepareWorkspace; extractGitClone
// reads it back under the same ref. The ref is keyed by the repository
// URL and the pinned commit SHA, so the same pinned checkout always
// resolves to the same staged archive.
func GitCloneStagingRef(src *adapterv1.WorkspaceSource) string {
	return src.GetUrl() + "\x00" + src.GetResolvedCommitSha()
}

// extractGitClone materializes a §14 gitClone source. The §14 contract
// places the clone on the gateway's network path, not the pod's, so
// the runtime never sees VCS credentials: the gateway clones the
// repository at the pinned commit and delivers its tree to the staging
// area as a gzip-tar. extractGitClone extracts that staged archive
// under the source path.
func extractGitClone(root, stagingDir string, src *adapterv1.WorkspaceSource) error {
	if stagingDir == "" {
		return errors.New("gitClone source requires a staging directory")
	}
	staged, err := StagingPath(stagingDir, GitCloneStagingRef(src))
	if err != nil {
		return err
	}
	f, err := os.Open(staged)
	if err != nil {
		return fmt.Errorf("open staged repository archive: %w", err)
	}
	defer f.Close()
	counter := &byteCounter{r: f}
	gz, err := gzip.NewReader(counter)
	if err != nil {
		return fmt.Errorf("open gzip stream: %w", err)
	}
	defer gz.Close()
	capped := &readCap{r: gz, maxRead: decompressorPerReadCap}
	// gitClone never raises a strip-components skip — strip=0 cannot
	// drop any entry — so any returned warnings are spurious; discard
	// them rather than mix them into the parent Materialize slice.
	// Git histories often carry symlinks (e.g., monorepos, vendored
	// repos), so gitClone opts in to symlinks unconditionally; the
	// validator still resolves every target through ValidateSymlinkTarget
	// using the workspace root. spec: §7.4 line 458; §13.4 — F-7.4.4.
	allow := upload.RuntimeAllow{AllowSymlinks: true, WorkspaceRoot: root}
	_, err = extractUploadTar(root, src.GetPath(), 0, 0, capped, counter, allow)
	return err
}
