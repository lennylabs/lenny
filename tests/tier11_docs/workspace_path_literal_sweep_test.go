// SPDX-License-Identifier: MIT

// Tier-11 sweep for the retired pod-global working directory.
//
// Every session is bound to a slot on every pod, whatever the pool's
// sessionPolicy.maxConcurrentSessions, so a session's working directory
// is /workspace/slots/{sessionId}/current and no pod-global
// /workspace/current exists. Every surviving reference to that path is
// false, so the retirement is asserted as a sweep over the surfaces the
// path reaches rather than as the list of sites one change happened to
// touch.
//
// The sweep carries three predicates, because two of the ways a
// restatement goes wrong leave no occurrence of the retired literal
// behind. A statement that names the staging area and the promotion
// target together is half-restated when only its /workspace/current
// token moves, which promotes files across two unrelated trees. A
// statement of a checkpoint bundle is half-restated when its workspace
// half moves onto the slot tree and its session-file half keeps the
// pod-global /sessions/ root, which snapshots a co-tenant's session
// files.
//
// spec: 6.4 (per-session workspace layout), 4.6.1 (warm pool controller pod
// lifecycle), 4.6.2 (per-slot roots), 16.1 (adapter metrics)

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// retiredPodGlobalWorkspacePath is the working directory that exists on
// no pod.
const retiredPodGlobalWorkspacePath = "/workspace/current"

// workspaceSlotRoot is the prefix of every per-session tree that
// replaced it.
const workspaceSlotRoot = "/workspace/slots/"

// podGlobalStagingPath is the pod-global staging tree the retirement
// keeps. Naming it on the same line as a per-session tree is the
// half-restated promotion path.
const podGlobalStagingPath = "/workspace/staging"

// permittedWorkspaceRetirementStatements are the occurrences of the
// retired literal that state its retirement. Each is keyed by the
// repository-relative file it stands in and matched on the trimmed line
// text, so a line that moves keeps its exemption and a line that is
// reworded loses it. Every other occurrence sends a reader to a
// directory no pod has.
var permittedWorkspaceRetirementStatements = map[string][]string{
	filepath.Join("spec", "06_warm-pod-model.md"): {
		"No pod-global `/workspace/current` path exists.",
		"No global `/workspace/current` path exists, and the runtime MUST NOT assume one.",
	},
	filepath.Join("docs", "runtime-author-guide", "lifecycle.md"): {
		"No pod-global working directory (`/workspace/current`) exists.",
	},
	filepath.Join("cmd", "runtimes", "echo-concurrent", "main_test.go"): {
		"// pod-global /workspace/current alternative.",
	},
	filepath.Join("cmd", "lenny-ctl", "runtimescaffold", "scaffold_test.go"): {
		`// collapsed each language template declared "/workspace/current" as a`,
		`if strings.Contains(string(raw), "/workspace/current") {`,
		`t.Errorf("%s names the retired pod-global /workspace/current", name)`,
	},
}

// spec: 6.4
// diagnosis: a swept surface still names the pod-global
//
//	/workspace/current. That directory exists on no pod: the adapter
//	materializes each session's workspace under
//	/workspace/slots/{sessionId}/current. A runtime author, an operator,
//	or a scaffolded runtime that follows the surviving site reads and
//	writes a directory outside every session's tree. A failure names the
//	file and line to restate on the per-session slot path.
func TestNoSurfaceNamesTheRetiredPodGlobalWorkingDirectory(t *testing.T) {
	root := repoRoot(t)
	seen := map[string]map[string]bool{}
	for _, path := range retirementSweepSurfaces(t, root) {
		rel := mustRel(t, root, path)
		permitted := permittedWorkspaceRetirementStatements[rel]
		for i, line := range strings.Split(readSweptFile(t, path), "\n") {
			if !strings.Contains(line, retiredPodGlobalWorkspacePath) {
				continue
			}
			trimmed := strings.TrimSpace(line)
			if match := matchPermitted(permitted, trimmed); match != "" {
				if seen[rel] == nil {
					seen[rel] = map[string]bool{}
				}
				seen[rel][match] = true
				continue
			}
			t.Errorf("%s:%d names the retired pod-global working directory; each session's workspace is %s{sessionId}/current:\n%s",
				rel, i+1, workspaceSlotRoot, trimmed)
		}
	}
	// An exemption that outlives its sentence would silently widen the
	// sweep's permitted set, so every permitted statement must still stand
	// where it is recorded.
	for rel, statements := range permittedWorkspaceRetirementStatements {
		for _, statement := range statements {
			if !seen[rel][statement] {
				t.Errorf("%s no longer carries the retirement statement %q; drop the exemption or restore the statement", rel, statement)
			}
		}
	}
}

// spec: 6.4, 4.6.2
// diagnosis: a swept surface states the pod-global staging tree and a
//
//	per-session slot tree on one line, which is what a half-restated
//	promotion path leaves behind: the /workspace/current token moved onto
//	the slot tree and the staging clause beside it did not. Every
//	PrepareWorkspace lands in /workspace/slots/{sessionId}/staging, so the
//	surviving line promotes files across two unrelated trees. A failure
//	names the line to restate whole.
func TestNoSurfaceStatesThePodGlobalStagingTreeBesideASlotTree(t *testing.T) {
	root := repoRoot(t)
	for _, path := range retirementSweepSurfaces(t, root) {
		for i, line := range strings.Split(readSweptFile(t, path), "\n") {
			if !strings.Contains(line, podGlobalStagingPath) || !strings.Contains(line, workspaceSlotRoot) {
				continue
			}
			t.Errorf("%s:%d states the pod-global staging tree and a per-session slot tree on one line; the promotion source is %s{sessionId}/staging:\n%s",
				mustRel(t, root, path), i+1, workspaceSlotRoot, strings.TrimSpace(line))
		}
	}
}

// bareSessionsRoot matches an occurrence of the pod session-file root
// that is not a segment of a longer identifier such as the REST route
// /v1/sessions/. The capture is the segment that follows the root, which
// is the session identifier the per-session tree is keyed on.
var bareSessionsRoot = regexp.MustCompile(`(^|[^0-9A-Za-z])/sessions/([^/\s"'` + "`" + `)\]]*)`)

// spec: 6.4, 4.6.2
// diagnosis: a swept surface states a per-session workspace tree and the
//
//	pod-global /sessions/ root on one line, which is what a half-restated
//	checkpoint bundle leaves behind: the workspace half moved onto the
//	slot tree and the session-file half did not. The adapter resolves the
//	session tmpfs to /sessions/{sessionId} on every pod, so the surviving
//	line describes a checkpoint that snapshots a co-tenant's session
//	files. A failure names the line to restate whole.
func TestNoSurfaceStatesTheBareSessionsRootBesideASlotTree(t *testing.T) {
	root := repoRoot(t)
	for _, path := range retirementSweepSurfaces(t, root) {
		for i, line := range strings.Split(readSweptFile(t, path), "\n") {
			if !strings.Contains(line, workspaceSlotRoot) {
				continue
			}
			for _, m := range bareSessionsRoot.FindAllStringSubmatch(line, -1) {
				if m[2] == "{sessionId}" {
					continue
				}
				t.Errorf("%s:%d states a per-session workspace tree and the bare pod session-file root on one line; the session tmpfs is /sessions/{sessionId}:\n%s",
					mustRel(t, root, path), i+1, strings.TrimSpace(line))
			}
		}
	}
}

// matchPermitted returns the permitted statement the line carries, or
// the empty string when the line carries none.
func matchPermitted(permitted []string, line string) string {
	for _, statement := range permitted {
		if strings.Contains(line, statement) {
			return statement
		}
	}
	return ""
}

// readSweptFile reads one swept surface, failing the test when the walk
// named a file that cannot be read.
func readSweptFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}
