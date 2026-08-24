// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency checks for the one pod filesystem layout.
//
// Every pod carries the slot layout, so a session's working directory is
// `/workspace/slots/{sessionId}/current` and no pod-global `/workspace/current`
// exists. The retirement is of that one path: the pod-global
// `/workspace/staging` and the read-only, pod-shared `/workspace/shared/`
// remain mounted on every pod.
//
// Two runtime-author pages restate the layout, and these cases assert what
// each page must say about it: the lifecycle page states the retirement of
// the pod-global working directory without generalizing it to the pod-shared
// trees its own printed layout carries, and the integration-levels page
// states `/workspace/slots/{sessionId}/current/` as the Basic-level working
// directory. The predicate over surviving occurrences of the retired literal
// is a directory-wide sweep and lives in
// workspace_path_literal_sweep_test.go, which owns it for every surface
// rather than for these two pages.
//
// These cases read the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 6.1 (pod filesystem volumes), 6.4 (per-session workspace layout)

package tier11_docs_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// spec: 6.1, 6.4
// diagnosis: docs/runtime-author-guide/lifecycle.md states the filesystem
//
//	layout in terms a runtime author cannot implement: either it no longer
//	names the retired pod-global `/workspace/current` and the per-session
//	path that replaces it, or it denies the pod-global `/workspace/staging`
//	and the read-only `/workspace/shared/` that the same page's own tree
//	prints and that every pod mounts. A failure here means the page and the
//	applied layout disagree about which trees exist on a pod.
func TestLifecycleGuideRetiresOnlyThePodGlobalWorkingDirectory(t *testing.T) {
	root := repoRoot(t)
	page := readDocPage(t, filepath.Join(root, "docs", "runtime-author-guide", "lifecycle.md"))

	layout := section(page, "Filesystem Layout")
	if layout == "" {
		t.Fatal("docs/runtime-author-guide/lifecycle.md: Filesystem Layout section not found (renamed or removed?)")
	}

	requireAllContain(t, "lifecycle.md Filesystem Layout section", layout, []string{
		"No pod-global working directory (`/workspace/current`) exists",
		"`/workspace/slots/{sessionId}/current`",
		"`/workspace/staging`",
		"`/workspace/shared/`",
	})

	// The pod-shared trees survive the retirement, so no sentence on the page
	// may generalize it to every pod-global directory or to every shared path.
	requireNoneContain(t, "lifecycle.md Filesystem Layout section", layout, []string{
		"No pod-global workspace directory exists",
		"must not assume a path shared by the pod's sessions",
	})

	// `/tmp/` is shared across the pod's sessions too, so a sentence that
	// enumerates the shared trees may not close the list over the two
	// `/workspace` trees alone.
	requireNoneContain(t, "lifecycle.md Filesystem Layout section", layout, []string{
		"are the only trees shared across the pod's sessions",
	})
	requireAllContain(t, "lifecycle.md Filesystem Layout section", layout, []string{
		"`/tmp/`",
	})
}

// mustRel renders an absolute path relative to the repository root so a
// failure names the file the way a reader would open it.
func mustRel(t testing.TB, root, path string) string {
	t.Helper()
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}

// spec: 6.4
// diagnosis: docs/runtime-author-guide/integration-levels.md still tells a
//
//	Basic-level runtime author that the working directory is
//	`/workspace/current`. That path exists on no pod, so a runtime written
//	from this page reads and writes a directory outside its session's tree.
//	A failure here means the Basic-level page contradicts the lifecycle and
//	glossary statements of the same layout.
func TestIntegrationLevelsGuideNamesThePerSessionWorkingDirectory(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "docs", "runtime-author-guide", "integration-levels.md")
	page := readDocPage(t, path)

	workspace := lineContaining(page, "**Workspace files**")
	if workspace == "" {
		t.Fatal("docs/runtime-author-guide/integration-levels.md: the workspace-files bullet was not found (renamed or removed?)")
	}
	if !strings.Contains(workspace, "`/workspace/slots/{sessionId}/current/`") {
		t.Errorf("docs/runtime-author-guide/integration-levels.md: the workspace-files bullet does not name the per-session working directory:\n%s", workspace)
	}
}
