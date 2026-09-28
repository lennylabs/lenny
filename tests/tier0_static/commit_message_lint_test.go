// SPDX-License-Identifier: MIT

package tier0_static

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// scripts/lint-commit-messages.sh rejects a commit subject that names a
// proposal's scaffolding label (a build-step id S<n>, a deliverable id
// such as CODE-<n> or TEST-<n>, a decision id D<n>, or a review pass). Those
// labels name parts of a proposal document, and a subject that carries
// one tells a reader of the history nothing once the proposal closes.
// The cases below drive the script over a throwaway repository so each
// subject is judged on its own, and they pin the three exemptions: the
// object-store token S3, identifiers that only look like labels such as
// TEST-GAPS and S256, and a commit confined to proposals/.

// commitLintCase is one commit in the fixture repository.
type commitLintCase struct {
	subject string
	path    string // file the commit changes; empty for an empty commit
	reject  bool
}

// runCommitLint builds a repository holding cases in order, runs the
// lint over every commit after the root, and returns its combined
// output and whether it exited non-zero.
func runCommitLint(t *testing.T, cases []commitLintCase) (string, bool) {
	t.Helper()
	dir, _ := buildLintRepo(t, cases)
	installLintScript(t, dir, lintScriptSource(t))
	return runLintScript(t, dir, "main~"+strconv.Itoa(len(cases))+"..main")
}

// buildLintRepo creates a repository with a root commit followed by one
// commit per case, and returns its directory and the full SHA of each
// case's commit in order.
func buildLintRepo(t *testing.T, cases []commitLintCase) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=alice", "GIT_AUTHOR_EMAIL=alice@acme.com",
			"GIT_COMMITTER_NAME=alice", "GIT_COMMITTER_EMAIL=alice@acme.com",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "root")
	shas := make([]string, 0, len(cases))
	for i, c := range cases {
		if c.path == "" {
			git("commit", "-q", "--allow-empty", "-m", c.subject)
		} else {
			full := filepath.Join(dir, c.path)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.WriteFile(full, []byte(strings.Repeat("x", i+1)), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			git("add", c.path)
			git("commit", "-q", "-m", c.subject)
		}
		shas = append(shas, git("rev-parse", "HEAD"))
	}
	return dir, shas
}

// lintScriptSource returns the repository's lint script.
func lintScriptSource(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(schematest.RepoRoot(t), "scripts", "lint-commit-messages.sh"))
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	return string(src)
}

// installLintScript writes src into the fixture repository. The script
// resolves the repository from its own location, so it runs from a copy
// placed inside the fixture.
func installLintScript(t *testing.T, dir, src string) {
	t.Helper()
	script := filepath.Join(dir, "scripts", "lint-commit-messages.sh")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	if err := os.WriteFile(script, []byte(src), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
}

// runLintScript runs the installed script in dir with args and returns
// its combined output and whether it exited non-zero.
func runLintScript(t *testing.T, dir string, args ...string) (string, bool) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{filepath.Join(dir, "scripts", "lint-commit-messages.sh")}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err != nil
}

// withFloorAndRecorded rewrites the script's FLOOR assignment and
// RECORDED list so a fixture repository's own SHAs drive the default
// range. It fails the test when the script does not declare both.
func withFloorAndRecorded(t *testing.T, src, floor string, recorded ...string) string {
	t.Helper()
	floorRE := regexp.MustCompile(`(?m)^FLOOR="[0-9a-f]+"$`)
	recordedRE := regexp.MustCompile(`(?ms)^RECORDED=\(\n.*?^\)$`)
	if !floorRE.MatchString(src) || !recordedRE.MatchString(src) {
		t.Fatal("lint script declares no FLOOR assignment or no RECORDED list")
	}
	src = floorRE.ReplaceAllLiteralString(src, `FLOOR="`+floor+`"`)
	return recordedRE.ReplaceAllLiteralString(src, "RECORDED=(\n"+strings.Join(recorded, "\n")+"\n)")
}

// spec: 13.0 (TESTING.md §13.0 Tier 0 deliverables)
// diagnosis: scripts/lint-commit-messages.sh accepted a commit subject
// that carries a proposal scaffolding label, or rejected a subject that
// only resembles one. A missed label lets a subject such as
// "0001 S2: ..." into the first-parent history; a false rejection blocks
// a legitimate commit. Read the named subject against the script's
// pattern list.
func TestCommitMessageLintRejectsProposalScaffoldingLabels(t *testing.T) {
	cases := []commitLintCase{
		{subject: "0001 S2: record a waiter predicate left for human review", path: "", reject: true},
		{subject: "warmpool: record the drain-request transition reason (0001 CODE-B)", path: "pkg/a.go", reject: true},
		{subject: "sessionstore test: pin the waiter predicate (TEST-7 step 3)", path: "tests/b.go", reject: true},
		{subject: "Revert S11's excursion into the sidecar lifetime", path: "pkg/c.go", reject: true},
		{subject: "gateway: tick build step S8", path: "pkg/d.go", reject: true},
		{subject: "apply the D12 option to the claim path", path: "pkg/e.go", reject: true},
		{subject: "fold in the Pass 4 review", path: "pkg/f.go", reject: true},
		{subject: "sessionstore: persist the workspace plan on Update (§15.1)", path: "pkg/g.go", reject: false},
		{subject: "s3store: encrypt S3 objects with SSE-KMS (§12.5)", path: "pkg/h.go", reject: false},
		{subject: "connector: pin the PKCE S256 transformation", path: "pkg/i.go", reject: false},
		{subject: "TEST-GAPS: resolve T-25.8.22 found already satisfied", path: "TEST-GAPS.md", reject: false},
		{subject: "change-proposal 0001: CODE-3 and TEST-1 as predicate-defined steps", path: "proposals/0001/x.md", reject: false},
	}
	out, failed := runCommitLint(t, cases)
	if !failed {
		t.Fatalf("lint exited zero over a range with labelled subjects:\n%s", out)
	}
	for _, c := range cases {
		reported := strings.Contains(out, c.subject)
		if c.reject && !reported {
			t.Errorf("subject %q carries a proposal label but was not reported:\n%s", c.subject, out)
		}
		if !c.reject && reported {
			t.Errorf("subject %q was reported but carries no proposal label:\n%s", c.subject, out)
		}
	}
}

// spec: 13.0 (TESTING.md §13.0 Tier 0 deliverables)
// diagnosis: scripts/lint-commit-messages.sh failed a range whose
// subjects all name behavior, so tier 0 would block every commit.
func TestCommitMessageLintAcceptsBehaviorSubjects(t *testing.T) {
	out, failed := runCommitLint(t, []commitLintCase{
		{subject: "sessionserver: admit exactly one finalize per session (§15.1)", path: "pkg/a.go"},
		{subject: "tests: record the row-lock waiter predicate for review (§15.1)"},
	})
	if failed {
		t.Fatalf("lint rejected behavior-named subjects:\n%s", out)
	}
}

// spec: 13.0 (TESTING.md §13.0 Tier 0 deliverables)
// diagnosis: the default run of scripts/lint-commit-messages.sh either
// failed on a commit listed in RECORDED, passed an unlisted labelled
// commit after FLOOR, or kept a RECORDED entry that no longer sits in
// the checked range. A labelled commit already in shared history is
// accepted only by its full SHA, and a dead entry must fail so the
// floor cannot silently move past it.
func TestCommitMessageLintDefaultRangeHonorsRecordedExceptions(t *testing.T) {
	cases := []commitLintCase{
		{subject: "spec: state the finalize precondition", path: "spec/a.md"},
		{subject: "0001 S5: record a waiter predicate left for human review"},
		{subject: "0001 S6: pin the waiter predicate", path: "tests/b.go"},
	}
	dir, shas := buildLintRepo(t, cases)
	floor, recordedSHA, unrecorded := shas[0], shas[1], cases[2].subject

	installLintScript(t, dir, withFloorAndRecorded(t, lintScriptSource(t), floor, recordedSHA))
	out, failed := runLintScript(t, dir)
	if !failed {
		t.Fatalf("default run passed an unrecorded labelled subject %q:\n%s", unrecorded, out)
	}
	if !strings.Contains(out, recordedSHA[:9]+" recorded exception") {
		t.Errorf("default run did not report %s as a recorded exception:\n%s", recordedSHA[:9], out)
	}
	if strings.Contains(out, recordedSHA[:9]+" subject carries") {
		t.Errorf("default run reported recorded commit %s as a violation:\n%s", recordedSHA[:9], out)
	}
	if !strings.Contains(out, unrecorded) {
		t.Errorf("default run did not report unrecorded subject %q:\n%s", unrecorded, out)
	}

	// A floor past the recorded commit leaves its entry dead.
	installLintScript(t, dir, withFloorAndRecorded(t, lintScriptSource(t), shas[1], recordedSHA))
	out, failed = runLintScript(t, dir)
	if !failed || !strings.Contains(out, "remove it from RECORDED") {
		t.Errorf("default run kept a RECORDED entry outside its range (failed=%v):\n%s", failed, out)
	}
}

// spec: 13.0 (TESTING.md §13.0 Tier 0 deliverables)
// diagnosis: the default run of scripts/lint-commit-messages.sh over
// this repository failed or checked nothing. A failure names a labelled
// subject or a dead RECORDED entry. A run that checked nothing means
// FLOOR is not an ancestor of HEAD, so tier 0 guards no commit.
func TestCommitMessageLintDefaultRangeChecksThisRepository(t *testing.T) {
	root := schematest.RepoRoot(t)
	shallow, err := exec.Command("git", "-C", root, "rev-parse", "--is-shallow-repository").Output()
	if err != nil {
		t.Skipf("not a git checkout: %v", err)
	}
	if strings.TrimSpace(string(shallow)) == "true" {
		t.Skip("shallow clone: the lint floor is not reachable")
	}
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "lint-commit-messages.sh"))
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("default run failed: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "nothing to check") {
		t.Fatalf("default run checked no commit:\n%s", out)
	}
}
