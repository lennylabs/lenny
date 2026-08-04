// SPDX-License-Identifier: MIT

package name

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// TestEveryPinnedLiteralEntryNamesALiteralThePassCanWrite holds the
// committed pinned-literal register to the tree it is read against.
//
// The register is the one input of this pass whose mis-seeding does not
// fail closed. checkPinnedClaimed reports an entry only when its
// position is above the count of literals its carrier holds, so an
// in-range but wrong position leaves the literal it was seeded for
// outside the site set findSites builds and outside the set standing
// re-checks: the run exits zero with the literal unwritten, and the
// tier-11 reconciliation reports it only after the specification prose
// it pins has been rewritten. This case is the compensating check, and
// it runs on the committed tree.
//
// The predicate is a disjunction, which is what makes the case hold at
// every point of the migration. Before the rewrite the literal pins
// specification prose carrying a reserved noun phrase. After it, the
// same literal carries the canonical identifier the pass wrote there.
// Nothing empties this register and loadPinnedLiterals refuses an empty
// entries list, so a predicate holding on one side alone would go red on
// the other.
//
// It ranges over the register's entries and asserts no completeness
// conjunct. A tier-11 carrier also holds diagnostic messages that carry
// a reserved noun phrase and pin nothing, and registering one would make
// it a site findSites admits and the pass rewrites, which is what naming
// the literals rather than walking them exists to prevent.
//
// It asserts neither the register's presence nor its non-emptiness. The
// case lives in this package rather than beside the run cases because
// the two spellings it reads, the phrase matcher and the identifier
// token, are stated here and reading them from outside would either
// restate one of them or ship an exported surface no pass enters.
//
// spec: §28.1
func TestEveryPinnedLiteralEntryNamesALiteralThePassCanWrite(t *testing.T) {
	t.Parallel()
	root, err := scope.RepoRoot(context.Background(), ".")
	if err != nil {
		t.Fatalf("locate the repository root: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, pinnedRegisterPath))
	if errors.Is(err, fs.ErrNotExist) {
		t.Skipf("not-yet-applicable: the tree carries no %s", pinnedRegisterPath)
	}
	if err != nil {
		t.Fatalf("read %s: %v", pinnedRegisterPath, err)
	}
	pinned, err := loadPinnedLiterals(data)
	if err != nil {
		t.Fatalf("load %s: %v", pinnedRegisterPath, err)
	}
	read := scope.DirReader(root)
	for _, target := range sortedPinnedFiles(pinned) {
		content, err := read(target)
		if err != nil {
			t.Errorf("%s names %s, which the tree does not carry: %v", pinnedRegisterPath, target, err)
			continue
		}
		fset, file, err := parseGo(target, string(content))
		if err != nil {
			t.Errorf("parse %s: %v", target, err)
			continue
		}
		literals := stringLiteralSpans(fset, file)
		for _, position := range sortedPinnedLiterals(pinned[target]) {
			if position > len(literals) {
				t.Errorf("%s names %s literal %d, and the file carries %d string literal(s)",
					pinnedRegisterPath, target, position, len(literals))
				continue
			}
			text := literalText(string(content), literals[position-1])
			if reservedExpr.MatchString(text) || len(identifierTokens(text)) > 0 {
				continue
			}
			t.Errorf("%s names %s literal %d, which carries neither a reserved noun phrase nor a canonical identifier, so the entry resolves no position the pass writes",
				pinnedRegisterPath, target, position)
		}
	}
}

// literalText returns the text one string literal of a carrier holds,
// unquoted where the spelling admits it. A raw or malformed literal is
// read as it stands, because the predicate above asks what the literal
// carries rather than what it evaluates to.
func literalText(content string, at span) string {
	raw := content[at.lo:at.hi]
	if unquoted, err := strconv.Unquote(raw); err == nil {
		return unquoted
	}
	return raw
}
