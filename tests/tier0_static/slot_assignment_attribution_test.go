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

// slotAttributionGatewaySubject matches the participant the correction makes
// the minter, either by name or through the gateway-side claim path that
// mints the identifier. A clause with one of these subjects states the rule
// §5.2 fixes, so a minting phrase inside it is correct prose and is exempt.
var slotAttributionGatewaySubject = regexp.MustCompile(`(?i)\bgateway\b|\bslot[- ]claim\b|\bSlotClaimer\b`)

// slotAttributionDirect are the compound spellings that name the adapter as
// the assigner on their own, without needing a surrounding sentence.
var slotAttributionDirect = regexp.MustCompile(`(?i)adapter[- ]assign(s|ed|ment)`)

// slotAttributionVerbPhrases describe the minting of a slot. They are
// reported in every clause that does not name the gateway, because the
// subject that carries the misattribution is often a pod-side actor other
// than the adapter itself ("a slot bind assigns a slot") or is elided
// entirely by a quotation ("... on slotId assignment"). Requiring the word
// "adapter" in the clause would let both of those spellings through.
var slotAttributionVerbPhrases = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bassign(s|ed|ing)? a slot`),
	regexp.MustCompile(`(?i)\bon slot(id)? assignment`),
}

// slotAttributionEllipsis matches the elision marks a quoted specification
// sentence uses. They are folded to a space before the text is split into
// clauses, so that an elided quote stays one clause and the words on either
// side of the elision are still read together.
var slotAttributionEllipsis = regexp.MustCompile(`\.{2,}|…`)

// slotAttributionClauses splits comment text into clauses on the sentence and
// clause punctuation, so that the gateway named in one clause does not exempt
// an adapter-subject clause beside it. A period splits only when whitespace or
// the end of the text follows it, which keeps a section number such as §6.4
// whole.
var slotAttributionClauses = regexp.MustCompile(`[;:]|\.(\s|$)`)

// slotAttributionMatch reports the banned spelling in the comment text, or the
// empty string when the text puts the minting of a slot identifier nowhere but
// the gateway. A minting phrase counts unless its own clause names the
// gateway, so prose that makes the gateway the assigner reads clean while a
// clause with any other subject, named or elided, is reported.
func slotAttributionMatch(text string) string {
	if m := slotAttributionDirect.FindString(text); m != "" {
		return m
	}
	folded := slotAttributionEllipsis.ReplaceAllString(text, " ")
	for _, clause := range slotAttributionClauses.Split(folded, -1) {
		if slotAttributionGatewaySubject.MatchString(clause) {
			continue
		}
		for _, re := range slotAttributionVerbPhrases {
			if m := re.FindString(clause); m != "" {
				return m
			}
		}
	}
	return ""
}

// diagnosis: a Go comment in production code attributes the minting of a
// slot identifier to the adapter. §5.2 makes the gateway the minter and
// §6.4 makes the adapter the creator of the tree; rewrite the comment to
// name the gateway as the minter of the identifier and the adapter as the
// creator of the tree it names.
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
		if m := slotAttributionMatch(text); m != "" {
			out = append(out, rel+":"+strconv.Itoa(fset.Position(group.Pos()).Line)+": "+m)
		}
	}
	return out
}

// slotAttributionCases pin the gate's predicate to the attribution rule rather
// than to the verb phrase. The correction §5.2 states is about who mints the
// identifier, so a comment that names the gateway as the assigner is correct
// prose and must not be reported, whatever spelling of "assign" it uses.
var slotAttributionCases = []struct {
	name    string
	comment string
	banned  bool
}{
	{"adapter as the assigner", "The adapter assigns a slot for the session.", true},
	{"adapter-assigned compound", "The workspace root is the adapter-assigned slot path.", true},
	{"adapter with a subjectless phrase", "The adapter bumps the counter on slot assignment.", true},
	{"pod-side subject other than the adapter", "slots holds the §6.4 concurrent-workspace per-slot state, keyed by slotId. Populated when a slot bind assigns a slot. Each slot owns its own workspace tree, state, and credential lease set.", true},
	{"elided quotation of the retired sentence", `spec: §6.4 — "Adapter — creates ... per-slot directory trees ... on slotId assignment".`, true},
	{"quotation of the retired directory sentence", `spec: §6.4 — "The adapter creates the slot directory on slotId assignment".`, true},
	{"gateway as the assigner", "The gateway assigns a slot at claim time.", false},
	{"gateway assigning an identifier", "The gateway enforces the cap before it ever assigns a slotId.", false},
	{"gateway phrase beside an adapter clause", "The gateway assigns a slot; the adapter creates its tree.", false},
	{"gateway phrase before the adapter in one clause", "The gateway assigns a slot that the adapter then registers", false},
	{"bare slot assignment with the gateway", "The counter is bumped on slot assignment by the gateway.", false},
	{"adapter registering a slot", "The adapter registers the slot the gateway minted.", false},
	{"gateway-side claim path named without the word gateway", "The slot-claim path skips a candidate pod whose uptime exceeds it before assigning a slot.", false},
	{"gateway minting inside a section number", "Resolved per §6.4: the gateway assigns a slot at claim time.", false},
}

// diagnosis: the attribution gate's predicate no longer matches the rule it
// enforces. A false negative lets a comment put the minting of a slot
// identifier on the adapter; a false positive bans correct prose that names
// the gateway as the assigner, which §5.2 states and leaves untouched.
//
// spec: 5.2 (a session-mode slot's identifier is its session's identifier,
// minted by the gateway at claim time), 6.4 (the adapter creates the
// session's slot tree on first reference to that identifier)
func TestSlotAttributionGateReportsAdapterSubjectsOnly_spec_5_2(t *testing.T) {
	for _, tc := range slotAttributionCases {
		t.Run(tc.name, func(t *testing.T) {
			got := slotAttributionMatch(tc.comment)
			if tc.banned && got == "" {
				t.Errorf("comment attributes slot assignment to the adapter but was not reported: %q", tc.comment)
			}
			if !tc.banned && got != "" {
				t.Errorf("comment names the gateway as the assigner but was reported for %q: %q", got, tc.comment)
			}
		})
	}
}
