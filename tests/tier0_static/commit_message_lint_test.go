// SPDX-License-Identifier: MIT

package tier0_static

import (
	"os"
	"os/exec"
	"path/filepath"
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
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=alice", "GIT_AUTHOR_EMAIL=alice@acme.com",
			"GIT_COMMITTER_NAME=alice", "GIT_COMMITTER_EMAIL=alice@acme.com",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "root")
	for i, c := range cases {
		if c.path == "" {
			git("commit", "-q", "--allow-empty", "-m", c.subject)
			continue
		}
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

	// The script resolves the repository from its own location, so run
	// a copy placed inside the fixture repository.
	src, err := os.ReadFile(filepath.Join(schematest.RepoRoot(t), "scripts", "lint-commit-messages.sh"))
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	script := filepath.Join(dir, "scripts", "lint-commit-messages.sh")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	if err := os.WriteFile(script, src, 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	cmd := exec.Command("bash", script, "main~"+strconv.Itoa(len(cases))+"..main")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err != nil
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
