// SPDX-License-Identifier: MIT

package tier0_static

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The gateway mints a session's identifier at claim time and that single
// value is also its slot's identifier (§5.2). The adapter creates the tree
// for that identifier on its first reference to it (§6.4); it does not
// choose the identifier. Comments that say otherwise are not caught by the
// compiler, so a reader who trusts them looks for an assignment step inside
// the adapter that does not exist, and a later change written from that
// reading puts the minting on the wrong side of the boundary. This gate
// pins the attribution in the source the same way §5.2 pins it in prose.

// slotAttributionRoots are the production trees the gate covers. Test files
// are excluded: a test comment may record what an earlier proposal decided,
// which is a historical statement rather than a claim about current behavior.
var slotAttributionRoots = []string{"pkg", "cmd", "sdks", "migrations"}

// slotAttributionPatterns are the spellings that put slot assignment on the
// adapter. Each is matched case-insensitively against the text of every
// comment in a production Go file.
var slotAttributionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)adapter[- ]assigned`),
	regexp.MustCompile(`(?i)adapter assigns`),
	regexp.MustCompile(`(?i)assigns a slot`),
	regexp.MustCompile(`(?i)\bon slot(id)? assignment`),
}

// diagnosis: a Go comment in production code attributes the minting of a
// slot identifier to the adapter. §5.2 makes the gateway the minter and
// §6.4 makes the adapter the creator of the tree; rewrite the comment to
// name the gateway as the minter, or, when the sentence is about the
// gateway allocating a slot, phrase it without the reported spelling.
//
// spec: 5.2 (a session-mode slot's identifier is its session's identifier,
// minted by the gateway at claim time), 6.4 (the adapter creates the
// session's slot tree on first reference to that identifier)
func TestSlotAssignmentIsAttributedToTheGateway_spec_5_2(t *testing.T) {
	root := schematest.RepoRoot(t)
	var offenses []string

	for _, tree := range slotAttributionRoots {
		dir := filepath.Join(root, tree)
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == "vendor" {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			offenses = append(offenses, slotAttributionOffenses(t, root, path)...)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}

	if len(offenses) > 0 {
		t.Errorf("comments attribute slot assignment to the adapter:\n%s", strings.Join(offenses, "\n"))
	}
}

// slotAttributionOffenses reports every comment in the Go file at path whose
// text matches one of the banned spellings, as "rel:line: text".
func slotAttributionOffenses(t *testing.T, root, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	var out []string
	for _, group := range file.Comments {
		// The banned spellings are matched against the comment group with its
		// line breaks folded to spaces, so a phrase wrapped across two comment
		// lines is one site rather than two halves that each match nothing.
		text := strings.Join(strings.Fields(group.Text()), " ")
		for _, re := range slotAttributionPatterns {
			if m := re.FindString(text); m != "" {
				out = append(out, rel+":"+strconv.Itoa(fset.Position(group.Pos()).Line)+": "+m)
				break
			}
		}
	}
	return out
}
