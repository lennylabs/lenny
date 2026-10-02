// SPDX-License-Identifier: MIT

package tier0_static

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The proposal-label commit gate extends the comment ratchet in
// proposal_label_comment_test.go to the two carriers that ratchet does not
// read: commit messages, and lines added to proto, YAML, and shell sources.
// A commit message is the first-parent record of what an integration did,
// and a reader of that history has no proposal directory open, so a label
// such as a lane-prefixed change identifier or a bare decision identifier
// names nothing for them. The durable reference is the spec section and a
// description of the behavior.
//
// History cannot be rewritten once pushed, so the gate reads a bounded range
// (since..HEAD, recorded in the register) and carries an exemption list for
// published commits that predate it. An exemption whose message no longer
// carries a label fails, which keeps the list to commits the gate would
// otherwise reject and pins that the detector still catches them.

// proposalLabelCommitRegisterPath is the register the gate reads, relative to
// the repository root.
const proposalLabelCommitRegisterPath = "tests/registers/proposal-label-commits.yaml"

// proposalLabelTextPattern matches a lane-prefixed change identifier (a lane
// prefix, a hyphen, and a number or a letter with an optional number), a
// bare decision identifier, and a bare build step. The letter alternative
// stops at a word boundary, so a file name such as TEST-GAPS.md is not a
// label. findTextLabels passes over the S3 service name.
var proposalLabelTextPattern = regexp.MustCompile(
	`\b(?:(?:SPEC|CODE|TEST|DOCS?|FIXTURE|RES|SCHEMA|CONF)-(?:[0-9]+|[A-Z][0-9]*)|D[0-9]{1,2}|S[0-9]{1,2})\b`,
)

// proposalLabelSourceExts are the non-Go source extensions whose added lines
// the gate reads. Go comments are held by the comment ratchet.
var proposalLabelSourceExts = map[string]bool{".proto": true, ".yaml": true, ".yml": true, ".sh": true}

// proposalLabelCommitRegister is the on-disk register document.
type proposalLabelCommitRegister struct {
	Kind    string `yaml:"kind"`
	Version int    `yaml:"version"`
	Since   string `yaml:"since"`
	Exempt  []struct {
		Commit string `yaml:"commit"`
		Reason string `yaml:"reason"`
	} `yaml:"exempt"`
}

// addedLine is one line a commit adds to a source file.
type addedLine struct {
	commit, path, text string
}

// findTextLabels returns the proposal labels a piece of text carries, in
// order of appearance.
func findTextLabels(text string) []string {
	var out []string
	for _, m := range proposalLabelTextPattern.FindAllString(text, -1) {
		if m != "S3" {
			out = append(out, m)
		}
	}
	return out
}

// labelScannedPath reports whether the gate reads lines added to the
// repository-relative path p.
func labelScannedPath(p string) bool {
	if !proposalLabelSourceExts[path.Ext(p)] || strings.HasPrefix(p, "proposals/") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "testdata" || seg == "vendor" {
			return false
		}
	}
	return true
}

// parseCommitMessages splits `git log --format=%H%x00%B%x1e` output into a
// map from full commit hash to message.
func parseCommitMessages(out string) map[string]string {
	msgs := map[string]string{}
	for _, rec := range strings.Split(out, "\x1e") {
		hash, body, ok := strings.Cut(strings.TrimLeft(rec, "\n"), "\x00")
		if ok && hash != "" {
			msgs[hash] = body
		}
	}
	return msgs
}

// parseAddedLines reads `git log -p -U0 --format=%x01%H` output and returns
// every line added to a path the gate scans.
func parseAddedLines(out string) []addedLine {
	var lines []addedLine
	var commit, file string
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 1<<16), 1<<24)
	for sc.Scan() {
		l := sc.Text()
		switch {
		case strings.HasPrefix(l, "\x01"):
			commit, file = strings.TrimPrefix(l, "\x01"), ""
		case strings.HasPrefix(l, "+++ "):
			file = strings.TrimPrefix(strings.TrimPrefix(l, "+++ "), "b/")
		case strings.HasPrefix(l, "+") && file != "" && labelScannedPath(file):
			lines = append(lines, addedLine{commit: commit, path: file, text: l[1:]})
		}
	}
	return lines
}

// commitLabelViolations returns one violation per non-exempt commit message
// that carries a label, and one per exempt commit that no longer does or
// that lies outside the range.
func commitLabelViolations(msgs map[string]string, exempt map[string]bool) []string {
	var out []string
	for hash, body := range msgs {
		labels := findTextLabels(body)
		switch {
		case len(labels) > 0 && !exempt[hash]:
			out = append(out, fmt.Sprintf("commit %.12s: message carries proposal label(s) %v; "+
				"reword it to name the spec section and the behavior", hash, labels))
		case len(labels) == 0 && exempt[hash]:
			out = append(out, fmt.Sprintf("commit %.12s: exempt in %s but its message carries no label; "+
				"remove the exemption", hash, proposalLabelCommitRegisterPath))
		}
	}
	for hash := range exempt {
		if _, ok := msgs[hash]; !ok {
			out = append(out, fmt.Sprintf("commit %.12s: exempt in %s but outside the checked range; "+
				"remove the exemption", hash, proposalLabelCommitRegisterPath))
		}
	}
	sort.Strings(out)
	return out
}

// sourceLabelViolations returns one violation per added source line that
// carries a label.
func sourceLabelViolations(lines []addedLine) []string {
	var out []string
	for _, l := range lines {
		if labels := findTextLabels(l.text); len(labels) > 0 {
			out = append(out, fmt.Sprintf("%s (commit %.12s): added line carries proposal label(s) %v: %q",
				l.path, l.commit, labels, strings.TrimSpace(l.text)))
		}
	}
	sort.Strings(out)
	return out
}

// loadProposalLabelCommitRegister reads and validates the register.
func loadProposalLabelCommitRegister(t *testing.T, repo string) proposalLabelCommitRegister {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repo, proposalLabelCommitRegisterPath))
	if err != nil {
		t.Fatalf("read the proposal-label commit register: %v", err)
	}
	var doc proposalLabelCommitRegister
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse the proposal-label commit register: %v", err)
	}
	if doc.Kind != "proposal-label-commit-baseline" || doc.Version != 1 || doc.Since == "" {
		t.Fatalf("proposal-label commit register declares kind %q version %d since %q, "+
			"want proposal-label-commit-baseline version 1 with a since commit", doc.Kind, doc.Version, doc.Since)
	}
	for _, e := range doc.Exempt {
		if e.Commit == "" || strings.TrimSpace(e.Reason) == "" {
			t.Fatalf("proposal-label commit register: exemption %q needs a commit and a reason", e.Commit)
		}
	}
	return doc
}

// gitIn runs git with args in repo and returns its standard output.
func gitIn(repo string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// requireSinceCommit skips when the checkout cannot resolve the register's
// range (no git, or a shallow clone), and fails when a full checkout does
// not contain the since commit, which means the register is wrong.
func requireSinceCommit(t *testing.T, repo, since string) {
	t.Helper()
	shallow, err := gitIn(repo, "rev-parse", "--is-shallow-repository")
	if err != nil {
		t.Skipf("git unavailable (not a git checkout?): %v", err)
	}
	_, err = gitIn(repo, "merge-base", "--is-ancestor", since, "HEAD")
	if err != nil && strings.TrimSpace(shallow) == "true" {
		t.Skipf("shallow clone does not reach the register's since commit %.12s", since)
	}
	if err != nil {
		t.Fatalf("since commit %.12s in %s is not an ancestor of HEAD: %v", since, proposalLabelCommitRegisterPath, err)
	}
}

// spec: 13.0 (TESTING.md §13.0 Tier 0 deliverables)
//
// No commit message on the first-parent line since the register's since
// commit carries a proposal label unless it is a recorded exemption, and
// every exemption still carries one.
func TestCommitMessagesCarryNoProposalLabels(t *testing.T) {
	repo := schematest.RepoRoot(t)
	reg := loadProposalLabelCommitRegister(t, repo)
	requireSinceCommit(t, repo, reg.Since)
	out, err := gitIn(repo, "log", "--first-parent", "--format=%H%x00%B%x1e", reg.Since+"..HEAD")
	if err != nil {
		t.Fatalf("list commit messages: %v", err)
	}
	exempt := map[string]bool{}
	for _, e := range reg.Exempt {
		exempt[e.Commit] = true
	}
	for _, v := range commitLabelViolations(parseCommitMessages(out), exempt) {
		t.Error(v)
	}
}

// spec: 13.0 (TESTING.md §13.0 Tier 0 deliverables)
//
// No line a first-parent commit since the register's since commit adds to a
// proto, YAML, or shell source outside testdata/ and proposals/ carries a
// proposal label.
func TestAddedSourceLinesCarryNoProposalLabels(t *testing.T) {
	repo := schematest.RepoRoot(t)
	reg := loadProposalLabelCommitRegister(t, repo)
	requireSinceCommit(t, repo, reg.Since)
	out, err := gitIn(repo, "log", "--first-parent", "--no-merges", "-p", "-U0", "--no-color",
		"--no-ext-diff", "--format=%x01%H", reg.Since+"..HEAD")
	if err != nil {
		t.Fatalf("list added lines: %v", err)
	}
	for _, v := range sourceLabelViolations(parseAddedLines(out)) {
		t.Error(v)
	}
}

// spec: 13.0 (TESTING.md §13.0 Tier 0 deliverables)
//
// The detector catches each lane-prefixed change identifier, including a
// numbered spec change with a lettered sub-item list, a bare decision
// identifier, and a bare build step, and passes over §-section citations,
// the spec's own step numbers, the S3 service name, channel identifiers, and
// audit file names.
func TestProposalLabelDetectorCatchesLabelsAndPassesCitations(t *testing.T) {
	cases := []struct {
		text string
		want []string
	}{
		{"Applies proposal 0079 SPEC-8 (a) through (j) to §5.2.", []string{"SPEC-8"}},
		{"per CODE-A and CODE-D2, see D14 and S4", []string{"CODE-A", "CODE-D2", "D14", "S4"}},
		{"TEST-3, DOC-B, DOCS-A1, FIXTURE-2, RES-1, SCHEMA-C, CONF-4", []string{"TEST-3", "DOC-B", "DOCS-A1", "FIXTURE-2", "RES-1", "SCHEMA-C", "CONF-4"}},
		{"§5.2 Deployer acknowledgment; step 7 of §4.7.10; objects land in S3", nil},
		{"close TEST-GAPS.md findings on CH-RUNTIMEOPS and LNK-GWCONTROL", nil},
		{"sha 8D2f0 and a D123 part number", nil},
	}
	for _, c := range cases {
		if got := findTextLabels(c.text); fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("findTextLabels(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

// spec: 13.0 (TESTING.md §13.0 Tier 0 deliverables)
//
// The message check fails a labelled commit that is not exempt, an exempt
// commit whose message carries no label, and an exemption outside the range,
// and passes a clean commit and a labelled exempt commit.
func TestCommitLabelViolationsFailsUnexemptAndStaleExemptions(t *testing.T) {
	msgs := parseCommitMessages("aaa\x00spec: §5.2 acknowledgment\n\x1e\n" +
		"bbb\x00spec: applies SPEC-8 to §5.2\n\x1e\n" +
		"ccc\x00tests: pin CODE-B\n\x1e\n" +
		"ddd\x00docs: plain\n\x1e\n")
	got := commitLabelViolations(msgs, map[string]bool{"bbb": true, "ddd": true, "zzz": true})
	if len(got) != 3 {
		t.Fatalf("violations = %q, want one each for ccc, ddd, and zzz", got)
	}
	for i, frag := range []string{"commit ccc: message carries", "commit ddd: exempt", "commit zzz: exempt"} {
		if !strings.HasPrefix(got[i], frag) {
			t.Errorf("violation %d = %q, want prefix %q", i, got[i], frag)
		}
	}
}

// spec: 13.0 (TESTING.md §13.0 Tier 0 deliverables)
//
// The added-line parser reads only additions to proto, YAML, and shell
// sources outside testdata/ and proposals/, and the source check reports the
// labelled ones.
func TestAddedLineParserScopesToNonGoSources(t *testing.T) {
	diff := "\x01c1\n" +
		"diff --git a/charts/x.yaml b/charts/x.yaml\n--- a/charts/x.yaml\n+++ b/charts/x.yaml\n" +
		"@@ -0,0 +1,2 @@\n+# implements CODE-C\n+key: value\n" +
		"diff --git a/pkg/a.go b/pkg/a.go\n--- a/pkg/a.go\n+++ b/pkg/a.go\n@@ -0,0 +1 @@\n+// SPEC-8\n" +
		"\x01c2\n" +
		"diff --git a/pkg/x/testdata/f.yaml b/pkg/x/testdata/f.yaml\n--- /dev/null\n+++ b/pkg/x/testdata/f.yaml\n" +
		"@@ -0,0 +1 @@\n+label: CODE-A\n" +
		"diff --git a/scripts/run.sh b/scripts/run.sh\n--- a/scripts/run.sh\n+++ b/scripts/run.sh\n" +
		"@@ -1 +1 @@\n-# old D3\n+# per D3\n"
	lines := parseAddedLines(diff)
	if len(lines) != 3 {
		t.Fatalf("added lines = %+v, want the two chart lines and the shell line", lines)
	}
	got := sourceLabelViolations(lines)
	if len(got) != 2 || !strings.HasPrefix(got[0], "charts/x.yaml (commit c1)") ||
		!strings.HasPrefix(got[1], "scripts/run.sh (commit c2)") {
		t.Errorf("violations = %q, want charts/x.yaml from c1 and scripts/run.sh from c2", got)
	}
}
