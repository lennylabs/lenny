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

// scaffoldSweepPrefixes are the repo-relative path prefixes of the
// artifacts the communication-channels work ships: the section itself,
// the specification index that carries its rows, the migration tooling
// that reads it, and the tier-11 checks that hold it.
var scaffoldSweepPrefixes = []string{
	"spec/28_communication-channels.md",
	"spec/README.md",
	"scripts/specshift/",
	"tests/tier11_docs/spec_28_",
}

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

// scaffoldCommitBase is the long-lived branch the range of the work is
// taken against. The commits under check are those between the merge
// base with it and HEAD.
const scaffoldCommitBase = "main"

// scaffoldResidualCommits are the landed commits whose messages carry a
// label this check reports, keyed by full commit hash and held with the
// rewording each one needs. Their messages name a sub-step of the
// proposal that staged the ownership transfer instead of naming what the
// sub-step does.
//
// They are registered rather than corrected because a message is fixed
// only by rewriting the commit that carries it, which rewrites every
// commit after it on the branch. The register keeps the check green over
// the history that already exists while it fails on any commit added
// after it, and an entry whose message no longer carries a label is
// reported as stale so the register empties as the history is rewritten.
var scaffoldResidualCommits = map[string]string{
	"7db4b28b472b329292f476cfecc9ce649789ecd2": "name the sub-step that takes each `spec/README.md` row's link text and anchor from the §4.8 heading table by what it does",
	"bd86ed81e7d4b498ce147a6cf9121b168ea99763": "name the sub-step holding the attribution clauses, and the sub-step they credit, by what each one does",
}

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
	swept := 0
	for _, target := range tracked {
		if !inScaffoldSweep(target) {
			continue
		}
		swept++
		content, err := read(target)
		if err != nil {
			t.Fatalf("read %s: %v", target, err)
		}
		for _, site := range scaffold.Find(string(content)) {
			t.Errorf("%s:%d carries the proposal-internal label %q; cite the specification section or describe the behaviour",
				target, site.Line, site.Text)
		}
	}
	if swept == 0 {
		t.Fatalf("the sweep selected no tracked file out of %d, so it asserts nothing", len(tracked))
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
	base, err := mergeBase(ctx, root, scaffoldCommitBase)
	if err != nil {
		t.Skipf("no merge base with %s to take the range against: %v", scaffoldCommitBase, err)
	}
	read := 0
	seen := make(map[string]bool)
	for _, proposal := range channelWorkProposals {
		messages, err := commitMessages(ctx, root, base+"..HEAD", proposal)
		if err != nil {
			t.Fatalf("read the commits referencing %s: %v", proposal, err)
		}
		read += len(messages)
		for sha, message := range messages {
			sites := scaffold.FindInProposalText(message)
			if rewording, residual := scaffoldResidualCommits[sha]; residual {
				seen[sha] = true
				if len(sites) == 0 {
					t.Errorf("commit %s is registered as needing to %s and carries no label; drop its entry from the residual register",
						short(sha), rewording)
				}
				continue
			}
			for _, site := range sites {
				t.Errorf("commit %s line %d carries the proposal-internal label %q; name the sub-step by what it does and cite the specification heading",
					short(sha), site.Line, site.Text)
			}
		}
	}
	if read == 0 {
		t.Skipf("no commit between %s and HEAD references the channel work, so the range carries nothing to check", scaffoldCommitBase)
	}
	for sha := range scaffoldResidualCommits {
		if !seen[sha] {
			t.Errorf("the residual register names commit %s, which the range between %s and HEAD does not carry; drop the entry or point it at the commit that replaced it",
				short(sha), scaffoldCommitBase)
		}
	}
}

// inScaffoldSweep reports whether a tracked path is inside the sweep.
func inScaffoldSweep(target string) bool {
	if strings.Contains(target, scaffoldFixtureDir) {
		return false
	}
	for _, prefix := range scaffoldSweepPrefixes {
		if strings.HasPrefix(target, prefix) {
			return true
		}
	}
	return false
}

// mergeBase returns the merge base of HEAD and a branch.
func mergeBase(ctx context.Context, root, branch string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", root, "merge-base", "HEAD", branch).Output()
	if err != nil {
		return "", fmt.Errorf("git merge-base HEAD %s: %w", branch, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// commitMessages returns the whole message of every commit in a range
// that names a path, keyed by full commit hash. The match is a
// fixed string so a path's punctuation is read literally.
func commitMessages(ctx context.Context, root, revRange, path string) (map[string]string, error) {
	const recordSep = "\x1e"
	const fieldSep = "\x1f"
	out, err := exec.CommandContext(ctx, "git", "-C", root, "log", revRange,
		"--fixed-strings", "--grep="+path, "--format=%H"+fieldSep+"%B"+recordSep).Output()
	if err != nil {
		return nil, fmt.Errorf("git log %s: %w", revRange, err)
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
