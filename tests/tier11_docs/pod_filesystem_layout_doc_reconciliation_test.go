// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency checks for the one pod filesystem layout.
//
// Every pod carries the slot layout, so a session's working directory is
// `/workspace/slots/{sessionId}/current` and no pod-global `/workspace/current`
// exists. The retirement is of that one path: the pod-global
// `/workspace/staging` and the read-only, pod-shared `/workspace/shared/`
// remain mounted on every pod. Two runtime-author pages restate the layout,
// and each has carried a statement a runtime author would implement wrongly:
// the lifecycle page generalized the retirement to every pod-global workspace
// directory, contradicting the tree printed directly above it, and the
// integration-levels page still sent a Basic-level author to the retired path.
//
// These cases read the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 6.1 (pod filesystem volumes), 6.4 (per-session workspace layout)

package tier11_docs_test

import (
	"io/fs"
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

// spec: 6.1, 6.4
// diagnosis: a documentation line states the per-session workspace tree and
//
//	the pod-global staging tree together, which is what a half-finished
//	restatement of the retired pod-global working directory leaves behind:
//	the `/workspace/current` token was moved onto the slot path and the
//	sibling `/workspace/staging` clause was left describing the same
//	sentence's subject. A failure here means one line documents two layouts.
func TestNoDocLineStatesBothTheSlotTreeAndThePodGlobalStagingTree(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range docsMarkdownFiles(t, filepath.Join(root, "docs")) {
		page := readDocPage(t, rel)
		for i, ln := range strings.Split(page, "\n") {
			if strings.Contains(ln, "/workspace/staging") && strings.Contains(ln, "/workspace/slots/") {
				t.Errorf("%s:%d states the pod-global staging tree and a slot path on one line:\n%s",
					mustRel(t, root, rel), i+1, ln)
			}
		}
	}
}

// spec: 6.4
// diagnosis: the runtime author guide still names the retired pod-global
//
//	`/workspace/current`. That directory exists on no pod, so a runtime
//	written from the guide reads and writes outside its session's tree. A
//	failure here names the page and line that survived the retirement.
func TestRuntimeAuthorGuideNamesNoRetiredPodGlobalWorkingDirectory(t *testing.T) {
	root := repoRoot(t)
	guide := filepath.Join(root, "docs", "runtime-author-guide")
	// The one permitted occurrence is the lifecycle page's statement that the
	// path does not exist; every other mention would send an author to it.
	retirement := "No pod-global working directory (`/workspace/current`) exists"
	for _, path := range docsMarkdownFiles(t, guide) {
		page := readDocPage(t, path)
		for i, ln := range strings.Split(page, "\n") {
			if !strings.Contains(ln, "/workspace/current") {
				continue
			}
			if strings.Contains(ln, retirement) {
				continue
			}
			t.Errorf("%s:%d names the retired pod-global working directory:\n%s",
				mustRel(t, root, path), i+1, ln)
		}
	}
}

// docsMarkdownFiles returns every markdown file under dir, so a sweep reaches
// pages added after the sweep was written.
func docsMarkdownFiles(t testing.TB, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".md") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	if len(out) == 0 {
		t.Fatalf("walk %s: no markdown pages found (moved or renamed?)", dir)
	}
	return out
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

	// The retired path may not stand anywhere on the page: every other mention
	// would send the same author to the same missing directory.
	for i, ln := range strings.Split(page, "\n") {
		if strings.Contains(ln, "/workspace/current") {
			t.Errorf("docs/runtime-author-guide/integration-levels.md:%d names the retired pod-global path:\n%s", i+1, ln)
		}
	}
}
