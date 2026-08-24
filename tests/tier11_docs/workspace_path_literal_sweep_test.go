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
// spec: 6.4 (pod filesystem layout), 6.1 (the pod-global staging tree the retirement keeps)

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
// repository-relative file it stands in and matched as a substring of
// the trimmed line text, so a line that moves keeps its exemption and a
// line that is reworded loses it. The exemption covers the statement's
// own occurrence, so any further occurrence on the same line is still
// reported. Every other occurrence sends a reader to a directory no pod
// has.
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
			residue, matched := stripPermitted(permitted, trimmed)
			for _, statement := range matched {
				if seen[rel] == nil {
					seen[rel] = map[string]bool{}
				}
				seen[rel][statement] = true
			}
			if !strings.Contains(residue, retiredPodGlobalWorkspacePath) {
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

// spec: 6.4, 6.1
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

// spec: 6.4
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

// stripPermitted removes every permitted retirement statement the line
// carries and returns the text that remains together with the statements
// that matched. The exemption is stated over an occurrence rather than
// over a line, because a markdown paragraph is one physical line and
// each prose exemption is a whole paragraph: exempting the line would
// excuse any further occurrence a later sentence in that paragraph
// introduces.
func stripPermitted(permitted []string, line string) (residue string, matched []string) {
	residue = line
	for _, statement := range permitted {
		if !strings.Contains(residue, statement) {
			continue
		}
		residue = strings.ReplaceAll(residue, statement, "")
		matched = append(matched, statement)
	}
	return residue, matched
}

// spec: 6.4
// diagnosis: a permitted retirement statement exempts the whole line it
//
//	stands on rather than its own occurrence. Each prose exemption is a
//	full markdown paragraph, which is one physical line, so a sentence
//	added to that paragraph naming the retired pod-global working
//	directory would send a reader to a directory no pod has and the sweep
//	would stay green. A failure means the exemption is line-wide again.
func TestAPermittedRetirementStatementExemptsOnlyItsOwnOccurrence(t *testing.T) {
	const permittedStatement = "No pod-global `/workspace/current` path exists."
	line := permittedStatement + " Write build output to " + retiredPodGlobalWorkspacePath + " instead."

	residue, matched := stripPermitted([]string{permittedStatement}, line)
	if len(matched) != 1 || matched[0] != permittedStatement {
		t.Errorf("stripPermitted recorded %q as the statements it matched; want the one permitted statement", matched)
	}
	if !strings.Contains(residue, retiredPodGlobalWorkspacePath) {
		t.Errorf("a live occurrence beside the permitted statement was excused with it; residue: %q", residue)
	}

	// A line carrying the permitted statement alone is still exempt.
	residue, matched = stripPermitted([]string{permittedStatement}, permittedStatement)
	if len(matched) != 1 {
		t.Errorf("the permitted statement alone recorded %q as matched; want it recorded once", matched)
	}
	if strings.Contains(residue, retiredPodGlobalWorkspacePath) {
		t.Errorf("the permitted statement alone was reported as a surviving occurrence; residue: %q", residue)
	}
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

// podAttributedStaging matches a statement that attributes the staging
// area to the pod. The pod-global /workspace/staging tree is retained as
// a directory, so the phrase is only false where it names where staged
// content lands.
var podAttributedStaging = regexp.MustCompile(`(?i)\bpod(-global|'s|s')?\s+staging\b`)

// stagedContentDestinationCue matches the wording that turns a mention
// of the staging area into a statement of where staged content lands.
var stagedContentDestinationCue = regexp.MustCompile(`(?i)\b(upload|uploads|uploaded|stream|streams|streamed|accepts|accepted|lands|written|writes|promot\w*)\b`)

// stagingStatements returns the statements of a swept surface a
// staging-destination predicate reads: every physical line, plus the
// join of each consecutive pair whose pod-attributed staging phrase
// straddles the boundary between them. A comment reflow can wrap the
// subject and the object of one sentence onto separate lines, which a
// per-line predicate misses; joining every pair unconditionally instead
// fuses two unrelated rows of a filesystem-layout listing into one false
// statement, so the join is taken only where the phrase itself spans the
// two lines. Each statement carries the line it starts on.
func stagingStatements(body string) []statementWindow {
	lines := strings.Split(body, "\n")
	statements := make([]statementWindow, 0, len(lines))
	for i, line := range lines {
		text := stripStatementMarkers(line)
		statements = append(statements, statementWindow{line: i + 1, text: text})
		if i+1 >= len(lines) {
			continue
		}
		joined := text + " " + stripStatementMarkers(lines[i+1])
		if straddlesBoundary(podAttributedStaging.FindAllStringIndex(joined, -1), len(text)) {
			statements = append(statements, statementWindow{line: i + 1, text: joined})
		}
	}
	return statements
}

// statementWindow is one statement of a swept surface together with the
// line the statement starts on.
type statementWindow struct {
	line int
	text string
}

// straddlesBoundary reports whether any match begins before the join
// boundary and ends after it, which is what a phrase wrapped across two
// lines looks like once the lines are joined.
func straddlesBoundary(matches [][]int, boundary int) bool {
	for _, m := range matches {
		if m[0] < boundary && m[1] > boundary {
			return true
		}
	}
	return false
}

// stripStatementMarkers removes the leading comment, list, and
// indentation markers a carrier prefixes a continued statement with, so
// joining two lines yields the prose the author wrote.
func stripStatementMarkers(line string) string {
	return strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "/#*->| \t"))
}

// spec: 6.4, 6.1
// diagnosis: a swept surface states that staged content lands in the
//
//	pod's staging area. Every session is bound to a slot, so every
//	PrepareWorkspace writes into /workspace/slots/{sessionId}/staging and
//	FinalizeWorkspace promotes from there into that session's current
//	tree. The pod-global /workspace/staging directory is retained but is
//	not what the RPC writes into, so the surviving statement points a
//	reader of the contract at the wrong tree for the source half of the
//	promotion. A failure names the statement to restate on the
//	per-session staging tree.
func TestNoSurfaceLandsStagedContentInThePodStagingArea(t *testing.T) {
	root := repoRoot(t)
	for _, path := range retirementSweepSurfaces(t, root) {
		for _, window := range stagingStatements(readSweptFile(t, path)) {
			if !podAttributedStaging.MatchString(window.text) {
				continue
			}
			if !stagedContentDestinationCue.MatchString(window.text) {
				continue
			}
			t.Errorf("%s:%d states that staged content lands in the pod's staging area; every upload lands in %s{sessionId}/staging:\n%s",
				mustRel(t, root, path), window.line, workspaceSlotRoot, window.text)
		}
	}
}
