// SPDX-License-Identifier: MIT

package anchor

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// The committed anchor-move map, which the pass takes on the command
// line rather than from a fixed path, and which the case below reads to
// derive which sections the reduction retires.
const committedMovePath = "tests/spec-anchor-moves.json"

// carveOut is one block the reduction carves out of the anchor it
// moves: the title a citation names the block by, and the heading the
// block keeps.
type carveOut struct {
	title       string
	destination string
}

// carveOuts names the material the §15.4 reduction leaves in
// spec/15_external-api-surface.md under its own surviving heading. A
// citation that names one of these blocks by its title means that
// heading, and sending it to the channel-contract card the move map
// carries would resolve to a card that does not define the cited
// material.
//
// The titles are matched where the citation names one directly, so a
// carrier that spells a block's name as a Go identifier or a test name
// elsewhere on the line is not read as citing it.
//
// spec: §28.1
var carveOuts = []carveOut{
	{title: "translation fidelity", destination: "spec/15_external-api-surface.md#translation-fidelity-matrix"},
	{title: "unified message format", destination: "spec/15_external-api-surface.md#messageenvelope--unified-message-format"},
}

// TestEverySenseOfCarvedOutMaterialNamesItsSurvivingHeading holds the
// committed sense register to the text of the citations it resolves.
//
// A destination is the one input of this pass whose mis-seeding does not
// fail closed. checkRegisters holds every destination to a heading a
// document of the tree declares, so an entry naming a heading that
// exists and does not define the cited material passes the check, the
// citation is rewritten to it, and the run exits zero. Nothing downstream
// reads the citation's meaning: the rewritten token carries a canonical
// anchor and resolves. This case is the compensating check for that
// class, and it runs on the committed tree.
//
// It reads the citation's own line rather than the register alone,
// because what a citation means is a property of the sentence it sits
// in. When that line names one of the blocks the reduction carves out of
// the anchor it moves, the occurrence means the heading that block keeps,
// and any other destination sends a reader to a heading that does not
// define what was cited.
//
// It asserts no completeness conjunct and no non-emptiness. Membership
// is derived from the move map by the pass itself, and a line that names
// none of the carved-out blocks is resolved by the seeding judgement the
// register records rather than by a rule a case can state.
//
// spec: §28.1
func TestEverySenseOfCarvedOutMaterialNamesItsSurvivingHeading(t *testing.T) {
	t.Parallel()
	root, err := scope.RepoRoot(context.Background(), ".")
	if err != nil {
		t.Fatalf("locate the repository root: %v", err)
	}
	senses, ok := readCommittedSenses(t, root)
	if !ok {
		return
	}
	moves, err := loadMoves(filepath.Join(root, filepath.FromSlash(committedMovePath)))
	if err != nil {
		t.Fatalf("load %s: %v", committedMovePath, err)
	}
	read := scope.DirReader(root)
	for _, target := range sortedKeys(senses) {
		content, err := read(target)
		if err != nil {
			t.Errorf("%s names %s, which the tree does not carry: %v", senseRegisterPath, target, err)
			continue
		}
		text := string(content)
		named := retiredCitationSubjects(text, moves)
		for _, occurrence := range sortedOccurrences(senses[target]) {
			if occurrence > len(named) {
				t.Errorf("%s names %s occurrence %d, and the file carries %d retired-section citation(s)",
					senseRegisterPath, target, occurrence, len(named))
				continue
			}
			block, ok := carvedOutBlock(named[occurrence-1])
			if !ok {
				continue
			}
			if got := senses[target][occurrence].Destination; got != block.destination {
				t.Errorf("%s sends %s occurrence %d to %s, and the citation names material the reduction leaves under %s",
					senseRegisterPath, target, occurrence, got, block.destination)
			}
		}
	}
}

// readCommittedSenses loads the sense register out of the tree, and
// reports that the tree carries none when the migration has emptied it
// or has not yet seeded it.
func readCommittedSenses(t *testing.T, root string) (map[string]map[int]Sense, bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(senseRegisterPath)))
	if errors.Is(err, fs.ErrNotExist) {
		t.Skipf("not-yet-applicable: the tree carries no %s", senseRegisterPath)
		return nil, false
	}
	if err != nil {
		t.Fatalf("read %s: %v", senseRegisterPath, err)
	}
	senses, err := loadSenses(data)
	if err != nil {
		t.Fatalf("load %s: %v", senseRegisterPath, err)
	}
	return senses, true
}

// retiredCitationSubjects returns, for each retired-section citation one
// file carries, the words the citation is written in front of, in the
// source order the sense register numbers occurrences in. A citation
// written inside a markdown link is passed over, as findSites passes
// over it, so the positions here are the positions the pass resolves.
func retiredCitationSubjects(text string, moves *moveMap) []string {
	var links []span
	for _, m := range linkExpr.FindAllStringIndex(text, -1) {
		links = append(links, span{start: m[0], end: m[1]})
	}
	var out []string
	for _, m := range bareCitationExpr.FindAllStringSubmatchIndex(text, -1) {
		if covers(links, m[0]) || !moves.retiresSection(text[m[2]:m[3]]) {
			continue
		}
		out = append(out, citationSubject(text[m[1]:]))
	}
	return out
}

// subjectWidth is how much of the text after a citation is read as what
// the citation names. It spans a title of a few words, including one
// wrapped across a comment's line break.
const subjectWidth = 64

// citationSubject normalises the words a citation stands in front of, so
// a title is matched whether it is written on one line, wrapped across a
// comment's line break, or spelled with backticks around a field name.
func citationSubject(after string) string {
	if len(after) > subjectWidth {
		after = after[:subjectWidth]
	}
	var b strings.Builder
	for _, r := range strings.ToLower(after) {
		switch r {
		case '\n', '\t', '`', '*', '"':
			b.WriteByte(' ')
		case '/':
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// carvedOutBlock returns the carved-out block a citation names, and
// reports whether it names one. A citation that names none of them is
// resolved by the register's own judgement rather than by this rule.
func carvedOutBlock(subject string) (carveOut, bool) {
	for _, block := range carveOuts {
		if strings.HasPrefix(subject, block.title) {
			return block, true
		}
	}
	return carveOut{}, false
}
