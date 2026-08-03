// SPDX-License-Identifier: MIT

// Tier-11 documentation check holding the shipped artifacts of the
// communication-channels work, and the commit messages that landed them,
// to the repository rule that keeps a proposal's own scaffolding labels
// out of what ships. A proposal numbers its change sections, decisions,
// and review passes for its reviewers; those labels name parts of the
// proposal document and resolve to nothing for a later reader of the
// code or of the git history.
//
// These cases carry no `// spec:` annotation. The rule is owned by
// .claude/skills/implement-proposal/SKILL.md rather than by a numbered
// section under spec/. The predicate they apply is in
// scripts/specshift/scaffold, whose unit cases decide the accept and
// reject boundaries; these cases apply it to the repository state.
//
// The domain is the artifacts the channel work owns. The wider tree
// carries sites left by earlier proposals, and each of those belongs to
// the work that landed it rather than to this check.
//
// These tests are NOT under a build tag because they exercise the
// repository state directly — no external infrastructure required.

package tier11_docs_test

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/scaffold"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// scaffoldSweepTarget is one tracked artifact the sweep reads. A file
// the channel work owns outright is read whole. A shared record file the
// work appends an entry to is read on the lines of that entry alone,
// which are the lines naming one of the work's proposal documents,
// because the records around it belong to the work that landed them.
type scaffoldSweepTarget struct {
	prefix  string
	records []string
}

// reads reports whether a line of the target is inside the sweep.
func (s scaffoldSweepTarget) reads(line string) bool {
	if len(s.records) == 0 {
		return true
	}
	for _, record := range s.records {
		if strings.Contains(line, record) {
			return true
		}
	}
	return false
}

// scaffoldSweepTargets are the tracked artifacts the communication-
// channels work ships or writes into: the section itself, the
// specification index that carries its rows, the migration tooling that
// reads it, the tier-11 checks that hold it, and the queue record the
// work appends its hand-off note to.
var scaffoldSweepTargets = []scaffoldSweepTarget{
	{prefix: "spec/28_communication-channels.md"},
	{prefix: "spec/README.md"},
	{prefix: "scripts/specshift/"},
	{prefix: "tests/tier11_docs/spec_28_"},
	{prefix: queueRecordFile, records: []string{"0064", "0067"}},
}

// queueRecordFile is the shared proposal-queue record the channel work
// appends its hand-off note to.
const queueRecordFile = "PROPOSAL-QUEUE.md"

// scaffoldFixtureDir is the directory name every fixture tree sits
// under. A fixture records a text as it was written, so it is outside
// the sweep for the same reason it is outside the naming law's domain.
const scaffoldFixtureDir = "/testdata/"

// channelWorkProposals are the proposal documents whose commits this
// check reads: the one that named the channels and the one that authored
// the section they moved into.
var channelWorkProposals = []string{
	"proposals/0064_fix_name-the-communication-channels-and-move-them-into-the-spec.md",
	"proposals/0067_new_author-spec-28-the-channel-naming-law-taxonomy-and-registers.md",
}

// scaffoldCommitRevision is the revision the commits of the work are
// selected out of. Reachable history rather than a range against a
// long-lived branch: a range taken against the merge base with that
// branch empties the moment the work is integrated, and an empty range
// is a check that no longer reads the commits it was written for.
const scaffoldCommitRevision = "HEAD"

// TestChannelArtifactsCarryNoProposalScaffoldingLabel sweeps the tracked
// artifacts of the channel work for a proposal's own change-section,
// decision, or review-pass label.
//
// diagnosis: a failure means a shipped file names a part of a proposal
// document instead of citing the specification section or describing the
// behaviour, so a later reader has no way to resolve the reference.
func TestChannelArtifactsCarryNoProposalScaffoldingLabel(t *testing.T) {
	ctx := context.Background()
	root := repoRoot(t)
	tracked, err := scope.GitLister(root)(ctx)
	if err != nil {
		t.Fatalf("list the tracked tree: %v", err)
	}
	read := scope.DirReader(root)
	swept := map[string]int{}
	for _, path := range tracked {
		target, inside := scaffoldSweepTargetOf(path)
		if !inside {
			continue
		}
		content, err := read(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		lines := strings.Split(string(content), "\n")
		for _, site := range scaffold.Find(string(content)) {
			if !target.reads(lines[site.Line-1]) {
				continue
			}
			t.Errorf("%s:%d carries the proposal-internal label %q; cite the specification section or describe the behaviour",
				path, site.Line, site.Text)
		}
		for _, line := range lines {
			if target.reads(line) {
				swept[target.prefix]++
			}
		}
	}
	// Every target has to reach a line of the tree. A target that
	// reaches none is an artifact the work writes and the sweep passes
	// over, which is how a label ships inside the domain the check
	// claims to enforce.
	for _, target := range scaffoldSweepTargets {
		if swept[target.prefix] == 0 {
			t.Errorf("the sweep read no line under %s out of %d tracked file(s), so the artifact it names is outside the domain the check enforces",
				target.prefix, len(tracked))
		}
	}
}

// TestChannelWorkCommitsCarryNoProposalScaffoldingLabel reads the commit
// messages that landed the channel work and holds their subjects and
// bodies to the same rule.
//
// diagnosis: a failure means a commit message names a sub-step, a
// decision, or a review pass of a proposal document, which is a
// reference the git history cannot resolve. Name the sub-step by what it
// does and the content by its specification heading.
func TestChannelWorkCommitsCarryNoProposalScaffoldingLabel(t *testing.T) {
	ctx := context.Background()
	root := repoRoot(t)
	messages := map[string]string{}
	for _, proposal := range channelWorkProposals {
		found, err := commitMessages(ctx, root, scaffoldCommitRevision, proposal)
		if err != nil {
			t.Fatalf("read the commits referencing %s: %v", proposal, err)
		}
		for sha, message := range found {
			messages[sha] = message
		}
	}
	scan, err := scaffold.ScanCommits(messages)
	if err != nil {
		t.Fatalf("scan the commits of the channel work reachable from %s: %v", scaffoldCommitRevision, err)
	}
	for sha, sites := range scan.Sites {
		for _, site := range sites {
			t.Errorf("commit %s line %d carries the proposal-internal label %q; name the sub-step by what it does and cite the specification heading",
				short(sha), site.Line, site.Text)
		}
	}
}

// scaffoldSweepTargetOf returns the sweep target a tracked path belongs
// to, and whether the path is inside the sweep at all.
func scaffoldSweepTargetOf(path string) (scaffoldSweepTarget, bool) {
	if strings.Contains(path, scaffoldFixtureDir) {
		return scaffoldSweepTarget{}, false
	}
	for _, target := range scaffoldSweepTargets {
		if strings.HasPrefix(path, target.prefix) {
			return target, true
		}
	}
	return scaffoldSweepTarget{}, false
}

// commitMessages returns the whole message of every commit reachable
// from a revision that names a path, keyed by full commit hash. The
// match is a fixed string so a path's punctuation is read literally.
func commitMessages(ctx context.Context, root, revision, path string) (map[string]string, error) {
	const recordSep = "\x1e"
	const fieldSep = "\x1f"
	out, err := exec.CommandContext(ctx, "git", "-C", root, "log", revision,
		"--fixed-strings", "--grep="+path, "--format=%H"+fieldSep+"%B"+recordSep).Output()
	if err != nil {
		return nil, fmt.Errorf("git log %s: %w", revision, err)
	}
	messages := make(map[string]string)
	for _, record := range strings.Split(string(out), recordSep) {
		sha, message, found := strings.Cut(strings.TrimLeft(record, "\n"), fieldSep)
		if !found {
			continue
		}
		messages[sha] = message
	}
	return messages, nil
}

// short abbreviates a commit hash for a report.
func short(sha string) string {
	if len(sha) < 8 {
		return sha
	}
	return sha[:8]
}
