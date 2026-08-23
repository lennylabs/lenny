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

// The §10.1 partial manifest, its supersede rule, and the §12.5 retention and
// supersession rule are keyed on session_id alone. The checkpoint tables carry
// no slot column, and the indexes over them are keyed on session_id, so the
// two-column pair names no key the schema has. The compiler does not read a
// comment, so a comment that still spells the pair tells a reader of the
// checkpoint pipeline that the supersede fence is per (session, slot) while
// the SQL, the indexes, and the in-memory store are keyed on the session. This
// gate pins the key in the source the same way the tier-11 check pins it in
// prose.

// checkpointScopingCommentRoot is the production tree the gate covers. Test
// files are excluded for the reason the slot-attribution gate excludes them: a
// test comment may describe the pre-drop schema a migration test exercises,
// which is a historical statement rather than a claim about current behavior.
const checkpointScopingCommentRoot = "pkg/gateway/checkpoint"

// checkpointScopingCommentFiles are test files outside that tree whose
// comments describe the checkpoint pipeline's current key rather than a
// pre-drop schema a migration test exercises. They are covered by name so the
// blanket test-file exclusion above does not leave a test that pins the
// per-session single-flight lock free to name the retired pair.
var checkpointScopingCommentFiles = []string{
	"tests/tier7a_load_local/checkpoint_grant_window_test.go",
	"tests/tier4_integration/checkpoint_driver_harness_test.go",
	"tests/tier4_integration/checkpoint_concurrent_pool_test.go",
}

// twoColumnCheckpointKey matches the retired two-column scoping key in the
// spellings a Go comment uses for it: the column pair and the prose pair, with
// or without code-span backticks and with any run of spaces around the comma.
var twoColumnCheckpointKey = regexp.MustCompile(`(?i)\(\s*` + "`?" + `session(_id)?` + "`?" + `\s*,\s*` + "`?" + `slot(_id)?` + "`?" + `\s*\)`)

// diagnosis: a Go comment under the checkpoint pipeline states the partial
// manifest's scoping key as the retired (session, slot) pair. The tables are
// keyed on session_id alone and carry no slot column, so the comment sends a
// reader to a key no index and no table has. Restate the comment on the
// session.
//
// spec: 10.1 (partial manifest scoping key and supersede-on-write), 12.5
// (checkpoint retention and supersession on the same key)
func TestCheckpointCommentsNameTheSessionScopingKeyAlone_spec_10_1(t *testing.T) {
	root := schematest.RepoRoot(t)
	dir := filepath.Join(root, checkpointScopingCommentRoot)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("stat %s: %v", checkpointScopingCommentRoot, err)
	}
	var offenses []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		offenses = append(offenses, checkpointScopingKeyOffenses(t, root, path)...)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", checkpointScopingCommentRoot, err)
	}
	for _, rel := range checkpointScopingCommentFiles {
		path := filepath.Join(root, rel)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("stat %s: %v", rel, err)
		}
		offenses = append(offenses, checkpointScopingKeyOffenses(t, root, path)...)
	}
	if len(offenses) > 0 {
		t.Errorf("comments state the checkpoint scoping key as the retired two-column pair:\n%s", strings.Join(offenses, "\n"))
	}
}

// checkpointScopingKeyOffenses reports every comment in the Go file at path
// that spells the retired two-column key, as "rel:line: text".
func checkpointScopingKeyOffenses(t *testing.T, root, path string) []string {
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
		// Line breaks are folded to spaces so a pair wrapped across two
		// comment lines is one site rather than two halves that match nothing.
		text := strings.Join(strings.Fields(group.Text()), " ")
		if m := twoColumnCheckpointKey.FindString(text); m != "" {
			out = append(out, rel+":"+strconv.Itoa(fset.Position(group.Pos()).Line)+": "+m)
		}
	}
	return out
}

// checkpointScopingKeyCases pin the gate's matcher to the two-column key
// rather than to any mention of a slot, so that prose naming the session key
// reads clean and every spelling of the retired pair is reported.
var checkpointScopingKeyCases = []struct {
	name    string
	comment string
	banned  bool
}{
	{"prose pair", "superseding any prior aborted attempt for the same (session, slot) first.", true},
	{"column pair", "The index was scoped on (session_id, slot_id).", true},
	{"backticked column pair", "The two-column `(session_id`, `slot_id)` key is retired.", true},
	{"padded column pair", "keyed on ( session_id ,  slot_id ).", true},
	{"session alone", "Scope the lookup to this attempt's session, which is exactly the set Put supersedes.", false},
	{"session column alone", "supersede is scoped to (session_id).", false},
	{"slot named outside the pair", "every session is bound to a slot whose identifier is the session's own", false},
}

// diagnosis: the scoping-key gate's matcher no longer matches the key it
// enforces. A false negative lets a comment restate the retired two-column
// key; a false positive bans correct prose that names the session key alone.
//
// spec: 10.1 (partial manifest scoping key and supersede-on-write), 12.5
// (checkpoint retention and supersession on the same key)
func TestCheckpointScopingKeyGateMatchesTheRetiredPairOnly_spec_10_1(t *testing.T) {
	for _, tc := range checkpointScopingKeyCases {
		t.Run(tc.name, func(t *testing.T) {
			got := twoColumnCheckpointKey.FindString(strings.Join(strings.Fields(tc.comment), " "))
			if tc.banned && got == "" {
				t.Errorf("comment states the retired two-column key but was not reported: %q", tc.comment)
			}
			if !tc.banned && got != "" {
				t.Errorf("comment states the session key alone but was reported as %q: %q", got, tc.comment)
			}
		})
	}
}
