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

// nested-package import: a package nested under a moved directory (for example
// "<old>/pgstore") is relocated by git mv with the directory tree, so its
// importers must be rewritten to the new path. The manifest lists only the
// moved leaf package, so ImportLiterals must rewrite the "<old>/sub" form too,
// not only the exact "<old>" leaf. This is constructed to FAIL against the
// pre-fix ImportLiterals, which rewrote only the exact quote-bounded "<old>"
// literal and left "<old>/pgstore" dangling (the move's go build gate caught
// this against the real tree: leasestore/pgstore and erasurejob/saltlockpg).
func TestImportLiterals_RewritesNestedSubpackageImport(t *testing.T) {
	moves := []Move{
		{Old: "github.com/lennylabs/lenny/pkg/gateway/leasestore", New: "github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"},
		{Old: "github.com/lennylabs/lenny/pkg/gateway/erasurejob", New: "github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"},
	}
	src := `import (
	"github.com/lennylabs/lenny/pkg/gateway/leasestore"
	leasepg "github.com/lennylabs/lenny/pkg/gateway/leasestore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/erasurejob/saltlockpg"
)`
	got := ImportLiterals(src, moves)
	for _, want := range []string{
		`"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"`,
		`"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore/pgstore"`,
		`"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob/saltlockpg"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("nested-import rewrite missing %s; got:\n%s", want, got)
		}
	}
	// The pre-fix dangling forms must be gone.
	for _, gone := range []string{
		`"github.com/lennylabs/lenny/pkg/gateway/leasestore/pgstore"`,
		`"github.com/lennylabs/lenny/pkg/gateway/erasurejob/saltlockpg"`,
	} {
		if strings.Contains(got, gone) {
			t.Errorf("stale nested import %s survived; got:\n%s", gone, got)
		}
	}
}

// A nested-package import that survives the rewrite must abort the move: the
// audit's ClassifyGo flags a surviving "<oldImport>/sub" import literal as a
// driver-rewritable token. This pins the audit to the same nested form
// ImportLiterals now rewrites, so a future regression that drops the nested
// rewrite is caught by the audit as well as the build gate.
func TestClassifyGo_AbortsOnSurvivingNestedImport(t *testing.T) {
	m := Move{Old: "github.com/lennylabs/lenny/pkg/gateway/leasestore", New: "github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"}
	stale := `import leasepg "github.com/lennylabs/lenny/pkg/gateway/leasestore/pgstore"`
	if got := ClassifyGo(stale, m); got != Abort {
		t.Fatalf("ClassifyGo on a surviving nested import = %v, want Abort", got)
	}
	// The correctly rewritten nested import must not abort.
	rewritten := `import leasepg "github.com/lennylabs/lenny/pkg/gateway/storage/leasestore/pgstore"`
	if got := ClassifyGo(rewritten, m); got != None {
		t.Fatalf("ClassifyGo on the rewritten nested import = %v, want None", got)
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

// warn-vs-abort: the §4 C4-named canonical informational-string form places the
// path token immediately after the opening quote with a trailing space (the real
// t.Log strings in tests/tier2_component/gateway_subsystems/scaffolds_test.go and
// tests/tier4_integration/scaffolds_test.go). Pass 4 names these as the residual
// drift the audit must record as a non-fatal Warn. The byte before the token is
// the opening quote, so the abort forms ("<P>/ and "<P>") miss (a space, not / or
// ", follows) and the classification must be Warn rather than None. These are the
// real strings from the named files, pinned verbatim.
func TestClassifyGo_QuoteThenSpaceInformationalStringWarns(t *testing.T) {
	cases := []struct {
		name    string
		old     string
		content string
	}{
		{
			name:    "scaffolds mcptools",
			old:     "github.com/lennylabs/lenny/pkg/gateway/mcptools",
			content: `"pkg/gateway/mcptools unit suites."`,
		},
		{
			name:    "scaffolds breakerstore",
			old:     "github.com/lennylabs/lenny/pkg/gateway/breakerstore",
			content: `"pkg/gateway/breakerstore Redis-backed contract suite."`,
		},
		{
			name:    "scaffolds mcpruntimes",
			old:     "github.com/lennylabs/lenny/pkg/gateway/mcpruntimes",
			content: `"pkg/gateway/mcpruntimes and covered by its unit tests."`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := Move{Old: c.old, New: c.old + "/moved"}
			if got := ClassifyGo(c.content, m); got != Warn {
				t.Errorf("quote-then-space informational string %q must warn, got %v", c.content, got)
			}
		})
	}
}

// warn-vs-abort: relaxing hasBoundedToken to "one side a comment boundary" must
// still reject a new path that carries the old path as a prefix, so the
// quote-then-space relaxation does not re-flag the moved package's own new path.
// pkg/gateway/mcp is a prefix of pkg/gateway/mcpfabric/mcp; the byte after the
// inner occurrence is a path-continuation letter, so it must not classify Warn.
func TestClassifyGo_PrefixInNewPathNotWarned(t *testing.T) {
	m := Move{Old: "github.com/lennylabs/lenny/pkg/gateway/mcp", New: "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"}
	// An informational string naming ONLY the new path (post-rewrite). The old
	// token pkg/gateway/mcp is a prefix substring of pkg/gateway/mcpfabric/mcp,
	// but the trailing slash-delimited final segment is mcp under a continuation,
	// so neither occurrence is a standalone old-path token.
	content := `"the pkg/gateway/mcpfabric/mcp package handles schema."`
	if got := ClassifyGo(content, m); got != None {
		t.Errorf("new path carrying old path as prefix must classify None, got %v", got)
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

// warn-vs-abort (JSON informational string): an in-manifest old path that
// survives only inside a larger informational JSON string value (a "notes"
// sentence, where the token is bounded by a space, '(', '.', or ';' rather than
// by a quote or a slash) must classify Warn, not None and not Abort. The
// driver's JSONTokens rewrites only quote/slash-bounded tokens, so it leaves
// such a reference untouched by design; the §4 C4 audit must still record it for
// the manual sweep, the same way it records the equivalent *.go and prose
// informational strings. tests/spec-map.json carries exactly these references in
// its "notes" values (for example pkg/gateway/podclaim.SlotClaimer,
// pkg/gateway/jwtaudit, pkg/gateway/auditstore, pkg/gateway/eventbus), bounded
// by '.', a space, or '('. This test is constructed to FAIL against the pre-fix
// ClassifyJSON, which returned None for every non-quote/slash occurrence.
func TestClassifyJSON_InformationalStringWarns(t *testing.T) {
	cases := []struct {
		name    string
		old     string
		content string
	}{
		{
			name:    "notes dotted method reference",
			old:     "github.com/lennylabs/lenny/pkg/gateway/podclaim",
			content: `"notes": "Phase 12c adds pkg/gateway/podclaim.SlotClaimer as the per-pod slot claim path."`,
		},
		{
			name:    "notes parenthesized package reference",
			old:     "github.com/lennylabs/lenny/pkg/gateway/jwtaudit",
			content: `"notes": "the audit observer (pkg/gateway/jwtaudit), and the rotation state machine."`,
		},
		{
			name:    "notes space-bounded package reference",
			old:     "github.com/lennylabs/lenny/pkg/gateway/auditstore",
			content: `"notes": "the audit-log row writer pkg/gateway/auditstore with the advisory lock."`,
		},
		{
			name:    "notes colon-bounded package reference",
			old:     "github.com/lennylabs/lenny/pkg/gateway/eventbus",
			content: `"notes": "The EventBus is implemented by pkg/gateway/eventbus: the RedisEventBus."`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// A real move re-anchors the package under a group, so the old token is
			// genuinely stale post-move (its new path differs).
			m := Move{Old: c.old, New: strings.Replace(c.old, "pkg/gateway/", "pkg/gateway/group/", 1)}
			if got := ClassifyJSON(c.content, m); got != Warn {
				t.Errorf("informational JSON string %q must classify Warn, got %v", c.content, got)
			}
		})
	}
}

// warn-vs-abort (JSON): a quote/slash-bounded driver-rewritable token still
// aborts, taking precedence over the informational-string warn path, so the
// new Warn branch does not weaken the abort guarantee for tokens the driver was
// supposed to rewrite.
func TestClassifyJSON_QuotedSurvivorStillAbortsOverWarn(t *testing.T) {
	m := Move{Old: "github.com/lennylabs/lenny/pkg/gateway/eventbus", New: "github.com/lennylabs/lenny/pkg/gateway/storage/eventbus"}
	// Same file carries both a stale quoted glob (Abort) and an informational
	// "notes" reference (Warn); the quoted form must win and abort.
	content := `{
  "key": "pkg/gateway/eventbus/...",
  "notes": "implemented by pkg/gateway/eventbus: the RedisEventBus."
}`
	if got := ClassifyJSON(content, m); got != Abort {
		t.Errorf("a surviving quoted JSON token must abort even alongside an informational reference; got %v", got)
	}
}

// warn-vs-abort (JSON): the informational warn path must not false-positive on
// the new path, which carries the old path as a prefix substring. After a clean
// rewrite the JSON names only the new path; ClassifyJSON must return None.
func TestClassifyJSON_NewPathInformationalIsNone(t *testing.T) {
	m := Move{Old: "github.com/lennylabs/lenny/pkg/gateway/mcp", New: "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"}
	content := `"notes": "the pkg/gateway/mcpfabric/mcp package handles schema validation."`
	if got := ClassifyJSON(content, m); got != None {
		t.Errorf("a new path carrying the old path as a prefix must classify None; got %v", got)
	}
}

// self-prefix abort: a self-prefix move (policy -> policy/policy,
// llmproxy -> llmproxy/llmproxy, coordination -> coordination/coordination from
// the committed manifest) makes the correct new token carry the old path as a
// prefix: "pkg/gateway/policy/policy/..." contains the substring
// "pkg/gateway/policy/. A naive deeper-reference check would re-flag the token
// the driver already correctly rewrote and abort the move, violating the §4 C4
// invariant that the abort condition matches the driver's rewrite granularity.
// ClassifyJSON and ClassifyGo must therefore return None on the correctly
// rewritten content, and Abort only on a genuine surviving pre-move token.
func TestClassifyJSON_SelfPrefixRewrittenIsNone(t *testing.T) {
	moves := []Move{
		{Old: "github.com/lennylabs/lenny/pkg/gateway/policy", New: "github.com/lennylabs/lenny/pkg/gateway/policy/policy"},
		{Old: "github.com/lennylabs/lenny/pkg/gateway/llmproxy", New: "github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"},
		{Old: "github.com/lennylabs/lenny/pkg/gateway/coordination", New: "github.com/lennylabs/lenny/pkg/gateway/coordination/coordination"},
	}
	for _, m := range moves {
		// The recursive-glob form the maps carry for every package, after a clean
		// rewrite by JSONTokens.
		oldGlob := `"` + RepoRel(m.Old) + `/..."`
		rewritten := JSONTokens(oldGlob, []Move{m})
		if rewritten == oldGlob {
			t.Fatalf("JSONTokens did not rewrite self-prefix glob %q", oldGlob)
		}
		if got := ClassifyJSON(rewritten, m); got != None {
			t.Errorf("ClassifyJSON on rewritten self-prefix %s = %v, want None (rewritten=%q)", RepoRel(m.Old), got, rewritten)
		}
		// A genuine surviving pre-move token (the un-rewritten old glob) must abort.
		if got := ClassifyJSON(oldGlob, m); got != Abort {
			t.Errorf("ClassifyJSON on surviving self-prefix token %s = %v, want Abort", RepoRel(m.Old), got)
		}
	}
}

// self-prefix abort (Go): the runtime slash-joined literal, the import literal,
// and the split path-segment form of a self-prefix move must classify None after
// a clean rewrite, and Abort on a genuine survivor. The split-segment form is the
// case the driver's splitSegmentForm provably cannot rewrite (it appends a tail
// segment), so the audit must not abort on it either.
func TestClassifyGo_SelfPrefixRewrittenIsNone(t *testing.T) {
	m := Move{Old: "github.com/lennylabs/lenny/pkg/gateway/policy", New: "github.com/lennylabs/lenny/pkg/gateway/policy/policy"}

	// Runtime slash-joined literal: rewritten -> None, surviving -> Abort.
	survRuntime := `p := "pkg/gateway/policy/engine.go"`
	if got := ClassifyGo(RuntimePaths(survRuntime, []Move{m}), m); got != None {
		t.Errorf("ClassifyGo on rewritten self-prefix runtime literal = %v, want None", got)
	}
	if got := ClassifyGo(survRuntime, m); got != Abort {
		t.Errorf("ClassifyGo on surviving self-prefix runtime literal = %v, want Abort", got)
	}

	// Import literal: rewritten -> None, surviving -> Abort.
	survImport := `import "github.com/lennylabs/lenny/pkg/gateway/policy"`
	if got := ClassifyGo(ImportLiterals(survImport, []Move{m}), m); got != None {
		t.Errorf("ClassifyGo on rewritten self-prefix import literal = %v, want None", got)
	}
	if got := ClassifyGo(survImport, m); got != Abort {
		t.Errorf("ClassifyGo on surviving self-prefix import literal = %v, want Abort", got)
	}

	// Split path-segment form: the driver cannot rewrite a self-prefix split
	// form (commonPrefixLen consumes all of oldSegs), so a surviving split form
	// must NOT abort the move.
	split := `readRepoFile(t, root, "pkg", "gateway", "policy", "engine.go")`
	if got := ClassifyGo(split, m); got != Warn && got != None {
		t.Errorf("ClassifyGo on self-prefix split-segment form = %v, want None or Warn (never Abort)", got)
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
