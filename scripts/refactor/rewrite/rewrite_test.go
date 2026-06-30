// SPDX-License-Identifier: MIT

package rewrite

import (
	"strings"
	"testing"
)

// These tests pin the boundary-anchored rewrite primitives the pkg/gateway C3
// regroup driver (proposal 0020 §2, §4 C4) depends on. They are tier-1 unit
// tests: pure functions, in-process, no I/O.
//
// spec: §4.1 (gateway is one component; the regroup preserves the subsystem
// seams and changes only import paths and the path references that name them).

// prefix-collision: a moved path that is a strict prefix of a sibling import
// must rewrite only the exact path, never the longer sibling. This is the
// central correctness property the proposal calls out (mcp vs mcptools,
// interceptor vs interceptorstore, delegation vs delegationbudget, admin vs
// admintoken).
func TestImportLiterals_PrefixCollisionDoesNotCorruptSibling(t *testing.T) {
	moves := []Move{
		{Old: "github.com/lennylabs/lenny/pkg/gateway/mcp", New: "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"},
		{Old: "github.com/lennylabs/lenny/pkg/gateway/mcptools", New: "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"},
		{Old: "github.com/lennylabs/lenny/pkg/gateway/interceptor", New: "github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"},
		{Old: "github.com/lennylabs/lenny/pkg/gateway/interceptorstore", New: "github.com/lennylabs/lenny/pkg/gateway/policy/interceptor/interceptorstore"},
	}
	src := `import (
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/mcpruntimes"
	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/interceptorstore"
)`
	got := ImportLiterals(src, moves)

	wantLines := []string{
		`"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"`,
		`"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"`,
		`"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"`,
		`"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor/interceptorstore"`,
	}
	for _, w := range wantLines {
		if !strings.Contains(got, w) {
			t.Errorf("expected rewritten literal %s in output, got:\n%s", w, got)
		}
	}
	// mcpruntimes is not in the manifest subset here; it must be untouched and
	// not corrupted by the mcp -> mcpfabric/mcp rewrite.
	if !strings.Contains(got, `"github.com/lennylabs/lenny/pkg/gateway/mcpruntimes"`) {
		t.Errorf("sibling mcpruntimes was corrupted; got:\n%s", got)
	}
	// The corruption a bare substring replace would produce: mcpfabric/mcptools
	// must NOT contain a doubled rewrite, and mcpruntimes must NOT become
	// mcpfabric/mcpruntimes via the mcp rule.
	if strings.Contains(got, "mcpfabric/mcpfabric") {
		t.Errorf("doubled rewrite detected; got:\n%s", got)
	}
	if strings.Contains(got, "mcpfabric/mcpruntimes") {
		t.Errorf("mcp rule leaked into mcpruntimes; got:\n%s", got)
	}
}

// The exact-literal rewrite must also leave a non-import occurrence of the same
// token (a comment) alone, because ImportLiterals is quote-anchored.
func TestImportLiterals_LeavesCommentTokenAlone(t *testing.T) {
	moves := []Move{
		{Old: "github.com/lennylabs/lenny/pkg/gateway/playground", New: "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"},
	}
	src := "// the pkg/gateway/playground package owns the token struct\n" +
		`x := "github.com/lennylabs/lenny/pkg/gateway/playground"`
	got := ImportLiterals(src, moves)
	if !strings.Contains(got, `"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"`) {
		t.Errorf("import literal not rewritten; got:\n%s", got)
	}
	if !strings.Contains(got, "// the pkg/gateway/playground package owns") {
		t.Errorf("comment token was rewritten by ImportLiterals; got:\n%s", got)
	}
}

// runtime-path: the slash-joined bare literal "pkg/gateway/<old>/file" must
// gain the group segment.
func TestRuntimePaths_SlashJoinedLiteral(t *testing.T) {
	moves := []Move{
		{Old: "github.com/lennylabs/lenny/pkg/gateway/playground", New: "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"},
	}
	src := `path := "pkg/gateway/playground/token.go"`
	got := RuntimePaths(src, moves)
	if !strings.Contains(got, `"pkg/gateway/mcpfabric/playground/token.go"`) {
		t.Errorf("slash-joined runtime literal not rewritten; got:\n%s", got)
	}
}

// runtime-path: the slash-joined literal must not corrupt a longer sibling.
func TestRuntimePaths_SlashJoinedPrefixSafe(t *testing.T) {
	moves := []Move{
		{Old: "github.com/lennylabs/lenny/pkg/gateway/mcp", New: "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"},
	}
	src := `a := "pkg/gateway/mcp/schema.json"` + "\n" + `b := "pkg/gateway/mcptools/registry.json"`
	got := RuntimePaths(src, moves)
	if !strings.Contains(got, `"pkg/gateway/mcpfabric/mcp/schema.json"`) {
		t.Errorf("mcp runtime literal not rewritten; got:\n%s", got)
	}
	if !strings.Contains(got, `"pkg/gateway/mcptools/registry.json"`) {
		t.Errorf("mcptools runtime literal was corrupted; got:\n%s", got)
	}
}

// runtime-path: the split path-segment form a filepath.Join/readRepoFile call
// passes must gain the group segment between gateway and the leaf basename.
func TestRuntimePaths_SplitSegmentForm(t *testing.T) {
	moves := []Move{
		{Old: "github.com/lennylabs/lenny/pkg/gateway/playground", New: "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"},
	}
	src := `tokenGo := readRepoFile(t, root, "pkg", "gateway", "playground", "token.go")`
	got := RuntimePaths(src, moves)
	want := `readRepoFile(t, root, "pkg", "gateway", "mcpfabric", "playground", "token.go")`
	if !strings.Contains(got, want) {
		t.Errorf("split path-segment form not rewritten;\nwant substring: %s\ngot: %s", want, got)
	}
}

// runtime-path: the split-segment rewrite must not match a bare leaf token that
// is not under "pkg", "gateway".
func TestRuntimePaths_SplitSegmentAnchoredUnderGateway(t *testing.T) {
	moves := []Move{
		{Old: "github.com/lennylabs/lenny/pkg/gateway/playground", New: "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"},
	}
	// "playground" appears as a lone segment not preceded by "gateway".
	src := `other := filepath.Join(root, "docs", "playground", "index.md")`
	got := RuntimePaths(src, moves)
	if got != src {
		t.Errorf("split-segment rewrite leaked outside the gateway head;\nwant unchanged: %s\ngot: %s", src, got)
	}
}

// JSON token boundaries: the exact path, the /... glob, and a deeper file
// reference must each rewrite; a longer sibling key must not.
func TestJSONTokens_BoundaryForms(t *testing.T) {
	moves := []Move{
		{Old: "github.com/lennylabs/lenny/pkg/gateway/mcp", New: "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"},
		{Old: "github.com/lennylabs/lenny/pkg/gateway/mcptools", New: "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"},
	}
	json := `{
  "exact": "pkg/gateway/mcp",
  "glob": "pkg/gateway/mcp/...",
  "file": "pkg/gateway/mcptools/mcptools_test.go",
  "method": "pkg/gateway/mcptools/discover_test.go::TestDiscover"
}`
	got := JSONTokens(json, moves)

	for _, want := range []string{
		`"pkg/gateway/mcpfabric/mcp"`,
		`"pkg/gateway/mcpfabric/mcp/..."`,
		`"pkg/gateway/mcpfabric/mcptools/mcptools_test.go"`,
		`"pkg/gateway/mcpfabric/mcptools/discover_test.go::TestDiscover"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %s in rewritten JSON; got:\n%s", want, got)
		}
	}
	// The mcp rule must not have corrupted the mcptools entries into
	// mcpfabric/mcp + tools.
	if strings.Contains(got, "mcpfabric/mcptools") == false {
		t.Errorf("mcptools entry lost; got:\n%s", got)
	}
	if strings.Contains(got, "mcpfabric/mcpfabric") {
		t.Errorf("doubled rewrite; got:\n%s", got)
	}
}

// JSON token boundaries: a moved path that is a strict prefix of a sibling glob
// key must not eat the sibling. "pkg/gateway/interceptor" vs
// "pkg/gateway/interceptorstore".
func TestJSONTokens_PrefixGlobSafe(t *testing.T) {
	moves := []Move{
		{Old: "github.com/lennylabs/lenny/pkg/gateway/interceptor", New: "github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"},
		{Old: "github.com/lennylabs/lenny/pkg/gateway/interceptorstore", New: "github.com/lennylabs/lenny/pkg/gateway/policy/interceptor/interceptorstore"},
	}
	json := `[
  "pkg/gateway/interceptor/...",
  "pkg/gateway/interceptorstore/..."
]`
	got := JSONTokens(json, moves)
	if !strings.Contains(got, `"pkg/gateway/policy/interceptor/..."`) {
		t.Errorf("interceptor glob not rewritten; got:\n%s", got)
	}
	if !strings.Contains(got, `"pkg/gateway/policy/interceptor/interceptorstore/..."`) {
		t.Errorf("interceptorstore glob not rewritten correctly; got:\n%s", got)
	}
	// The interceptor rule must not have produced
	// policy/interceptorstore (i.e. eaten the "store" sibling).
	if strings.Contains(got, `"pkg/gateway/policy/interceptorstore`) {
		t.Errorf("interceptor rule corrupted interceptorstore; got:\n%s", got)
	}
}

// warn-vs-abort: a surviving import literal in a Go file is an Abort (the
// driver was supposed to rewrite it).
func TestClassifyGo_SurvivingImportLiteralAborts(t *testing.T) {
	m := Move{Old: "github.com/lennylabs/lenny/pkg/gateway/auditstore", New: "github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"}
	content := `import "github.com/lennylabs/lenny/pkg/gateway/auditstore"`
	if got := ClassifyGo(content, m); got != Abort {
		t.Errorf("surviving import literal must abort; got %v", got)
	}
}

// warn-vs-abort: a surviving slash-joined runtime literal is an Abort.
func TestClassifyGo_SurvivingRuntimeLiteralAborts(t *testing.T) {
	m := Move{Old: "github.com/lennylabs/lenny/pkg/gateway/playground", New: "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"}
	content := `p := "pkg/gateway/playground/token.go"`
	if got := ClassifyGo(content, m); got != Abort {
		t.Errorf("surviving runtime literal must abort; got %v", got)
	}
}

// warn-vs-abort: a surviving split path-segment run is an Abort.
func TestClassifyGo_SurvivingSplitSegmentAborts(t *testing.T) {
	m := Move{Old: "github.com/lennylabs/lenny/pkg/gateway/playground", New: "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"}
	content := `readRepoFile(t, root, "pkg", "gateway", "playground", "token.go")`
	if got := ClassifyGo(content, m); got != Abort {
		t.Errorf("surviving split-segment run must abort; got %v", got)
	}
}

// warn-vs-abort: a path token in a // diagnosis: comment is a Warn, never an
// Abort, because the driver provably cannot rewrite it and aborting on it would
// make every C3 group move unsatisfiable (proposal Pass 4).
func TestClassifyGo_DiagnosisCommentWarns(t *testing.T) {
	m := Move{Old: "github.com/lennylabs/lenny/pkg/gateway/auditstore", New: "github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"}
	content := "// diagnosis: a failure here means pkg/gateway/auditstore lost a row.\n"
	if got := ClassifyGo(content, m); got != Warn {
		t.Errorf("// diagnosis: comment token must warn, not abort; got %v", got)
	}
}

// warn-vs-abort: an informational string with a leading space before the token
// (a t.Logf / skip message) is a Warn.
func TestClassifyGo_InformationalStringWarns(t *testing.T) {
	m := Move{Old: "github.com/lennylabs/lenny/pkg/gateway/leasestore", New: "github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"}
	content := `t.Logf("scaffold missing for pkg/gateway/leasestore; skipping")`
	if got := ClassifyGo(content, m); got != Warn {
		t.Errorf("informational-string token (space-bounded) must warn; got %v", got)
	}
}

// warn-vs-abort: a file that, after rewrite, carries neither form returns None.
func TestClassifyGo_NoneWhenAbsent(t *testing.T) {
	m := Move{Old: "github.com/lennylabs/lenny/pkg/gateway/auditstore", New: "github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"}
	content := `import "github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"`
	if got := ClassifyGo(content, m); got != None {
		t.Errorf("rewritten content must classify None; got %v", got)
	}
}

// warn-vs-abort: a surviving JSON token always aborts (no comment form in JSON).
func TestClassifyJSON_SurvivorAborts(t *testing.T) {
	m := Move{Old: "github.com/lennylabs/lenny/pkg/gateway/mcptools", New: "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"}
	if got := ClassifyJSON(`"pkg/gateway/mcptools/mcptools_test.go"`, m); got != Abort {
		t.Errorf("surviving JSON token must abort; got %v", got)
	}
	if got := ClassifyJSON(`"pkg/gateway/mcpfabric/mcptools/mcptools_test.go"`, m); got != None {
		t.Errorf("rewritten JSON token must classify None; got %v", got)
	}
}

// The driver must never rewrite a sibling whose prefix the audit would
// otherwise flag: classify the longer sibling as None when only the shorter
// path moved and was rewritten. Guards against the audit's hasBoundedToken
// over-matching a sibling.
func TestClassifyGo_SiblingNotFlagged(t *testing.T) {
	// mcp moved and was rewritten; mcptools is present as a (rewritten) import.
	m := Move{Old: "github.com/lennylabs/lenny/pkg/gateway/mcp", New: "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"}
	content := `import "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"`
	if got := ClassifyGo(content, m); got != None {
		t.Errorf("mcp audit must not flag the mcptools sibling; got %v", got)
	}
}

func TestParseManifest_SkipsCommentsAndBlankLines(t *testing.T) {
	in := `# header comment

github.com/lennylabs/lenny/pkg/gateway/mcp	github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp
   # indented comment
github.com/lennylabs/lenny/pkg/gateway/mcptools	github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools
`
	moves, err := ParseManifest(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if len(moves) != 2 {
		t.Fatalf("expected 2 moves, got %d: %+v", len(moves), moves)
	}
	// Longest-old-path-first: mcptools before mcp.
	if moves[0].Old != "github.com/lennylabs/lenny/pkg/gateway/mcptools" {
		t.Errorf("expected longest-first ordering; got %s first", moves[0].Old)
	}
}

func TestParseManifest_RejectsMalformedLine(t *testing.T) {
	in := "github.com/lennylabs/lenny/pkg/gateway/mcp only-one-field-no-tab\n"
	if _, err := ParseManifest(strings.NewReader(in)); err == nil {
		t.Fatalf("expected error on malformed line, got nil")
	}
}

// The committed manifest must parse cleanly and contain the §1.3 exclusion
// moves at their re-anchored destinations. This pins the driver to the actual
// manifest the C1 step authored.
func TestParseManifest_CommittedManifestExclusions(t *testing.T) {
	data := readCommittedManifest(t)
	moves, err := ParseManifest(strings.NewReader(data))
	if err != nil {
		t.Fatalf("committed manifest does not parse: %v", err)
	}
	if len(moves) == 0 {
		t.Fatal("committed manifest parsed to zero moves")
	}
	byOld := make(map[string]string, len(moves))
	for _, m := range moves {
		byOld[m.Old] = m.New
	}
	want := map[string]string{
		"github.com/lennylabs/lenny/pkg/gateway/breakerstore":     "github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker/breakerstore",
		"github.com/lennylabs/lenny/pkg/gateway/interceptorstore": "github.com/lennylabs/lenny/pkg/gateway/policy/interceptor/interceptorstore",
		"github.com/lennylabs/lenny/pkg/gateway/policy":           "github.com/lennylabs/lenny/pkg/gateway/policy/policy",
		"github.com/lennylabs/lenny/pkg/gateway/environmentstore": "github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore",
	}
	for old, newPath := range want {
		if got := byOld[old]; got != newPath {
			t.Errorf("manifest move for %s = %q, want %q", old, got, newPath)
		}
	}
}
