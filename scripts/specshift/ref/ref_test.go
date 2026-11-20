// SPDX-License-Identifier: MIT

package ref

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// The reference pass is migration tooling for the specification's own
// structure rather than a platform behavior, so these cases carry no
// spec-section annotation. The contract they pin is the one the package
// comment states: the registry records every page, identifier, anchor,
// and block with its file; check reports each broken link rule; fix
// rewrites link paths after a move from the committed registry; and
// lookup names where an identifier or a numbered section is.

// update rewrites the committed registry of the clean fixture.
var update = flag.Bool("update", false, "rewrite testdata/clean/spec/registry.json")

const (
	cleanFixture  = "testdata/clean"
	errorsFixture = "testdata/errors"
)

// noGit is a Git with no tags and no history.
func noGit(context.Context, ...string) ([]byte, error) {
	return nil, errors.New("not a git repository")
}

// fixtureTree returns a tree over dir listed from the filesystem.
func fixtureTree(dir string, git Git) *Tree {
	if git == nil {
		git = noGit
	}
	return &Tree{Root: dir, List: scope.DirLister(dir), Git: git}
}

// copyFixture copies a fixture tree into a fresh directory, so a case
// that writes leaves the committed fixture unchanged.
func copyFixture(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatalf("copy %s: %v", src, err)
	}
	return dst
}

func readFile(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// move moves a file inside a fixture copy, as `git mv` does.
func move(t *testing.T, dir, from, to string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(to))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, filepath.FromSlash(from)), full); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, tree *Tree, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := Run(context.Background(), tree, args, &out)
	return out.String(), err
}

func mustCheckClean(t *testing.T, tree *Tree) {
	t.Helper()
	findings, err := Check(context.Background(), tree)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, f := range findings {
		t.Errorf("unexpected finding: %s", f)
	}
}

func TestCleanFixtureRegistryIsCurrent(t *testing.T) {
	tree := fixtureTree(cleanFixture, nil)
	if *update {
		if _, err := run(t, tree, "registry"); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := run(t, tree, "registry", "--check"); err != nil {
		t.Fatalf("registry --check on the clean fixture: %v\n%s", err, out)
	}
}

func TestRegistryRecordsEveryKindOfEntry(t *testing.T) {
	reg, findings, err := Build(context.Background(), fixtureTree(cleanFixture, nil), emptyRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings on the clean fixture: %v", findings)
	}
	x := newIndex(reg)
	const pools = "spec/capabilities/pools/warm-pools.md"
	const claims = "spec/capabilities/pools/pod-claims.md"
	const podclaim = "spec/interfaces/kubernetes/podclaim.md"
	want := map[string]string{
		"pool-r1":                  pools,
		"pool-r2":                  pools,
		"claim-r1":                 claims,
		"reserved-hold":            pools,
		"claim":                    claims,
		"setting-pool-minwarm":     pools,
		"custom-resource-podclaim": podclaim,
		"kpclaim-create":           podclaim,
		"kpclaim-updatestatus":     podclaim,
		"pool-r3":                  pools,
		"pool-r4":                  pools,
	}
	for anchor, file := range want {
		if got := x.anchorFile[anchor]; got != file {
			t.Errorf("anchor %s: registered on %q, want %q", anchor, got, file)
		}
	}
	if _, ok := x.anchorFile["setting-example-notregistered"]; ok {
		t.Error("a block inside an example fence was registered")
	}
	if got := x.idAnchor["KPCLAIM-updateStatus"]; got != "kpclaim-updatestatus" {
		t.Errorf("operation KPCLAIM-updateStatus: anchor %q", got)
	}
	if got := x.codePath["WPOOL"]; got != pools {
		t.Errorf("alias WPOOL resolves to %q", got)
	}
	if p := x.pageByPath["spec/components/placement.md"]; p.Status != "placeholder" || p.Code != "PLACE" {
		t.Errorf("placeholder page recorded as %+v", p)
	}
	if _, ok := x.pageByPath["spec/04_system-components.md"]; ok {
		t.Error("a numbered file was registered as a page")
	}
	r3, r4 := x.retired["POOL-R3"], x.retired["POOL-R4"]
	if r3.Successor != "CLAIM-R1" || r4.Removed == "" {
		t.Errorf("retired records: %+v, %+v", r3, r4)
	}
	if len(reg.Requirements) != 3 || reg.Requirements[0].ID != "CLAIM-R1" || reg.Requirements[2].ID != "POOL-R2" {
		t.Errorf("requirements: %+v", reg.Requirements)
	}
	for _, term := range reg.Terms {
		if term.Anchor == "kpclaim-create" || term.Anchor == "pool-r3" {
			t.Errorf("anchor %s was recorded as a term", term.Anchor)
		}
	}
}

func TestRegistryEncodingIsStable(t *testing.T) {
	tree := fixtureTree(cleanFixture, nil)
	a, _, err := Build(context.Background(), tree, emptyRegistry())
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := Build(context.Background(), tree, emptyRegistry())
	if err != nil {
		t.Fatal(err)
	}
	ea, _ := a.Encode()
	eb, _ := b.Encode()
	if !bytes.Equal(ea, eb) {
		t.Fatal("two builds of one tree encode differently")
	}
	if !strings.Contains(string(ea), `"moves": []`) {
		t.Errorf("an empty list is not encoded as []:\n%s", ea)
	}
}

func TestRegistryCheckFailsOnAStaleFile(t *testing.T) {
	dir := copyFixture(t, cleanFixture)
	tree := fixtureTree(dir, nil)
	writeFile(t, dir, "spec/capabilities/pools/warm-pools.md",
		readFile(t, dir, "spec/capabilities/pools/warm-pools.md")+"\n<a id=\"pool-r5\"></a>**POOL-R5. New.** Text.\n")
	out, err := run(t, tree, "registry", "-check")
	if err == nil || !strings.Contains(err.Error(), "differs from a fresh build") {
		t.Fatalf("registry --check on a stale registry: err %v, out %s", err, out)
	}
	if _, err := run(t, tree, "registry"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, tree, "registry", "--check"); err != nil {
		t.Fatalf("registry --check after a rebuild: %v", err)
	}
}

func TestRegistryCheckFailsWhenTheFileIsMissing(t *testing.T) {
	dir := copyFixture(t, cleanFixture)
	if err := os.Remove(filepath.Join(dir, "spec", "registry.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, fixtureTree(dir, nil), "registry", "--check"); err == nil {
		t.Fatal("registry --check passed with no committed registry")
	}
}

func TestCheckPassesOnTheCleanFixture(t *testing.T) {
	mustCheckClean(t, fixtureTree(cleanFixture, nil))
	out, err := run(t, fixtureTree(cleanFixture, nil), "check")
	if err != nil || !strings.Contains(out, "no problems") {
		t.Fatalf("check: %v\n%s", err, out)
	}
}

func TestCheckReportsEveryPlantedError(t *testing.T) {
	findings, err := Check(context.Background(), fixtureTree(errorsFixture, nil))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range findings {
		got = append(got, f.String())
	}
	all := strings.Join(got, "\n")
	const pools = "spec/pages/warm-pools.md"
	want := []string{
		"docs/links.md:3: link ../spec/pages/missing.md does not resolve",
		"docs/links.md:5: link ../spec/pages/warm-pools.md#no-such-anchor: #no-such-anchor is not a registered identifier or term anchor",
		"docs/links.md:7: link ../spec/pages/warm-pools.md#overview targets a heading anchor on another page",
		"docs/links.md:9: link ../spec/pages/warm-pools.md#model targets a heading anchor on another page",
		"docs/links.md:11: link ../spec/pages/derived.md#derived-r1 carries a fragment into DERIVED, which is a placeholder",
		"docs/links.md:13: link ../spec/pages/warm-pools.md#claim-r1: #claim-r1 is registered on spec/pages/pod-claims.md",
		"spec/pages/pod-claims.md:2: code CLAIM is also registered by spec/pages/claims-copy.md",
		"spec/pages/derived.md:2: code DERIVED differs from DERIVE (spec/pages/derive.md) only by a suffix",
		pools + ":17: anchor #reserved-hold is also registered at spec/pages/pod-claims.md:12",
		pools + ":29: link #claim-r1: the identifier is registered on spec/pages/pod-claims.md",
		pools + ":36: anchor #setting-pool-minwarm is also registered at " + pools + ":31",
		pools + ":41: definition block has no kind: line",
		pools + ":27: requirement POOL-R4 has no explicit anchor",
		pools + ":7: retired identifier POOL-R8 names successor CLAIM-R99, which is not registered",
		pools + ":8: retired identifier POOL-R9 has no successor or removed record",
		pools + ": identifier POOL-R7 left the tree with no retired record",
		"spec/registry.json: code GONE (spec/pages/gone.md) left the tree",
	}
	for _, w := range want {
		if !strings.Contains(all, w) {
			t.Errorf("missing finding %q", w)
		}
	}
	if strings.Contains(all, "04_system-components") {
		t.Error("a link into a numbered file was checked")
	}
	if strings.Contains(all, "pod-claims.md#claim-r1:") || strings.Contains(all, "derived.md does not") {
		t.Errorf("a valid link was reported:\n%s", all)
	}
	if len(findings) != len(want) {
		t.Errorf("got %d findings, want %d:\n%s", len(findings), len(want), all)
	}
	if _, err := run(t, fixtureTree(errorsFixture, nil), "check"); err == nil {
		t.Error("check exited zero on planted errors")
	}
}

func TestCheckSkipsTheExcludedDomain(t *testing.T) {
	for _, p := range []string{"proposals/0001-old.md", "changes/archive/0001-old/change.md", "BUILD-GAPS.md", "TEST-GAPS.md", "CODE-REVIEW.md", "scripts/x/testdata/a.md"} {
		if inDomain(p) {
			t.Errorf("%s is inside the domain", p)
		}
	}
	for _, p := range []string{"README.md", "docs/a.md", "changes/0115-spec-structure/change.md", "spec/04_system-components.md"} {
		if !inDomain(p) {
			t.Errorf("%s is outside the domain", p)
		}
	}
}

// assertContains fails the test when a file lacks any of the strings.
func assertContains(t *testing.T, dir, rel string, wants ...string) {
	t.Helper()
	content := readFile(t, dir, rel)
	for _, w := range wants {
		if !strings.Contains(content, w) {
			t.Errorf("%s does not contain %q:\n%s", rel, w, content)
		}
	}
}

func TestFixAfterAPageMove(t *testing.T) {
	dir := copyFixture(t, cleanFixture)
	tree := fixtureTree(dir, nil)
	const from = "spec/capabilities/pools/warm-pools.md"
	const to = "spec/capabilities/runtimes/pools/warm-pools.md"
	move(t, dir, from, to)

	findings, err := Check(context.Background(), fixtureTree(dir, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) == 0 {
		t.Fatal("check found nothing after a page move")
	}

	out, err := run(t, tree, "fix")
	if err != nil {
		t.Fatalf("fix: %v\n%s", err, out)
	}
	if strings.Contains(out, "unresolved") {
		t.Errorf("fix left links unresolved:\n%s", out)
	}
	// A link from each depth: the root, a docs page two levels down, a
	// sibling page, the numbered file, and the moved page itself.
	assertContains(t, dir, "README.md",
		"(spec/capabilities/runtimes/pools/warm-pools.md#pool-r2)",
		"(spec/capabilities/runtimes/pools/warm-pools.md#pool-r3)",
		"[WPOOL](spec/capabilities/runtimes/pools/warm-pools.md)")
	assertContains(t, dir, "docs/guide/pools.md",
		"(../../spec/capabilities/runtimes/pools/warm-pools.md#pool-r1)",
		"(../../spec/components/placement.md)")
	assertContains(t, dir, "spec/capabilities/pools/pod-claims.md",
		"(../runtimes/pools/warm-pools.md#pool)",
		"(../runtimes/pools/warm-pools.md#pool-r2)",
		"(../runtimes/pools/warm-pools.md#setting-pool-minwarm)")
	assertContains(t, dir, "spec/04_system-components.md",
		"[Warm pools](capabilities/runtimes/pools/warm-pools.md)",
		"(capabilities/pools/pod-claims.md#claim-r1)")
	assertContains(t, dir, to,
		"(../../pools/pod-claims.md#claim-r1)",
		"[Pod claims](../../pools/pod-claims.md)",
		"(../../../interfaces/kubernetes/podclaim.md#kpclaim-create)",
		"(../../../04_system-components.md#46-pod-lifecycle-controllers)",
		"[POOL-R1](#pool-r1)")
	// The archives keep the paths they were written with.
	assertContains(t, dir, "changes/archive/0001-old/change.md", "(../../../spec/capabilities/pools/warm-pools.md#overview)")

	mustCheckClean(t, tree)
	if _, err := run(t, tree, "registry", "--check"); err != nil {
		t.Fatalf("registry --check after fix: %v", err)
	}
	committed, err := LoadCommitted(tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(committed.Moves) != 1 || committed.Moves[0] != (Move{ID: "POOL", From: from, To: to}) {
		t.Errorf("moves: %+v", committed.Moves)
	}

	again, err := Fix(context.Background(), fixtureTree(dir, nil))
	if err != nil {
		t.Fatal(err)
	}
	if again.Links != 0 {
		t.Errorf("a second fix rewrote %d link(s) in %v", again.Links, again.Files)
	}
}

func TestFixAfterTheRegistryWasRebuiltFirst(t *testing.T) {
	dir := copyFixture(t, cleanFixture)
	move(t, dir, "spec/components/placement.md", "spec/placement.md")
	// Rebuilding the registry before fixing records the move in the
	// history, which is where fix then finds the page's old path.
	if _, err := run(t, fixtureTree(dir, nil), "registry"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, fixtureTree(dir, nil), "fix"); err != nil {
		t.Fatal(err)
	}
	assertContains(t, dir, "docs/guide/pools.md", "(../../spec/placement.md)")
	assertContains(t, dir, "spec/capabilities/pools/pod-claims.md", "[Placement](../../placement.md)")
	assertContains(t, dir, "spec/placement.md", "(04_system-components.md#46-pod-lifecycle-controllers)")
	mustCheckClean(t, fixtureTree(dir, nil))
}

func TestFixAfterARequirementMovesBetweenPages(t *testing.T) {
	dir := copyFixture(t, cleanFixture)
	const pools = "spec/capabilities/pools/warm-pools.md"
	const claims = "spec/capabilities/pools/pod-claims.md"
	poolText := readFile(t, dir, pools)
	start := strings.Index(poolText, `<a id="pool-r2"></a>`)
	end := strings.Index(poolText, `<a id="pool-r3"></a>`)
	requirement := poolText[start:end]
	writeFile(t, dir, pools, poolText[:start]+poolText[end:])
	writeFile(t, dir, claims, readFile(t, dir, claims)+"\n"+requirement)

	if _, err := run(t, fixtureTree(dir, nil), "fix"); err != nil {
		t.Fatal(err)
	}
	assertContains(t, dir, "README.md", "(spec/capabilities/pools/pod-claims.md#pool-r2)")
	assertContains(t, dir, claims, "[POOL-R2](#pool-r2)", "[POOL-R1](warm-pools.md#pool-r1)", "[setting](warm-pools.md#setting-pool-minwarm)")
	mustCheckClean(t, fixtureTree(dir, nil))
	committed, err := LoadCommitted(fixtureTree(dir, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(committed.Moves) != 1 || committed.Moves[0] != (Move{ID: "POOL-R2", From: pools, To: claims}) {
		t.Errorf("moves: %+v", committed.Moves)
	}
}

func TestCheckReportsARequirementRemovedWithoutARecord(t *testing.T) {
	dir := copyFixture(t, cleanFixture)
	const pools = "spec/capabilities/pools/warm-pools.md"
	content := readFile(t, dir, pools)
	content = strings.Replace(content, "<a id=\"pool-r1\"></a>**POOL-R1. A pool names one runtime.** Text.\n", "", 1)
	writeFile(t, dir, pools, content)
	findings, err := Check(context.Background(), fixtureTree(dir, nil))
	if err != nil {
		t.Fatal(err)
	}
	all := ""
	for _, f := range findings {
		all += f.String() + "\n"
	}
	if !strings.Contains(all, "identifier POOL-R1 left the tree") {
		t.Errorf("removal without a record not reported:\n%s", all)
	}
	if !strings.Contains(all, "#pool-r1 is not a registered identifier") {
		t.Errorf("link to the removed requirement not reported:\n%s", all)
	}
}

func TestLookupOfIdentifiersAndCodes(t *testing.T) {
	tree := fixtureTree(cleanFixture, nil)
	cases := map[string][]string{
		"POOL-R1":              {"POOL-R1 spec/capabilities/pools/warm-pools.md#pool-r1"},
		"CLAIM":                {"CLAIM spec/capabilities/pools/pod-claims.md"},
		"WPOOL":                {"WPOOL spec/capabilities/pools/warm-pools.md (alias of POOL)"},
		"KPCLAIM-updateStatus": {"KPCLAIM-updateStatus spec/interfaces/kubernetes/podclaim.md#kpclaim-updatestatus"},
		"reserved-hold":        {"reserved-hold spec/capabilities/pools/warm-pools.md#reserved-hold"},
		"POOL-R3":              {"retired: successor CLAIM-R1", "CLAIM-R1 spec/capabilities/pools/pod-claims.md#claim-r1"},
		"POOL-R4":              {"retired: removed (the execution modes left the design)"},
	}
	for query, wants := range cases {
		out, err := run(t, tree, "lookup", query)
		if err != nil {
			t.Errorf("lookup %s: %v", query, err)
			continue
		}
		for _, w := range wants {
			if !strings.Contains(out, w) {
				t.Errorf("lookup %s: output %q lacks %q", query, out, w)
			}
		}
	}
	if _, err := run(t, tree, "lookup", "NOPE-R1"); err == nil {
		t.Error("lookup of an unregistered identifier succeeded")
	}
}

func TestLookupOfASectionThatIsAStub(t *testing.T) {
	out, err := run(t, fixtureTree(cleanFixture, nil), "lookup", "§4.6")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{
		"§4.6 Pod Lifecycle Controllers: spec/04_system-components.md#46-pod-lifecycle-controllers",
		"successors:",
		"spec/capabilities/pools/warm-pools.md\n",
		"spec/capabilities/pools/pod-claims.md#claim-r1",
		"| **Pod claim mechanism:** | CLAIM-R1 |",
		"the tag spec-frozen does not exist yet",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("lookup §4.6: output lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "04_system-components.md#no-such-heading") {
		t.Errorf("a link into a numbered file was listed as a successor:\n%s", out)
	}
}

// TestLookupOfAFullStubListsSuccessorsOutsideTheSpecification pins that
// a full stub retired in favour of records outside the specification,
// such as a program and a documentation page, lists them as successors,
// while a numbered file it links is still left out.
func TestLookupOfAFullStubListsSuccessorsOutsideTheSpecification(t *testing.T) {
	root := copyFixture(t, cleanFixture)
	stub := "# 18. Build Sequence\n\n" + StubMarker + "\n\n" + fullStubLead + "\n\n" +
		"- [Program v1](../changes/programs/v1.md): the plan.\n" +
		"- [Status](../docs/about/status.md#planned-after-v1): the work planned after v1.\n" +
		"- [Pools](04_system-components.md#46-pod-lifecycle-controllers): a numbered file.\n"
	if err := os.WriteFile(filepath.Join(root, "spec", "18_build-sequence.md"), []byte(stub), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, fixtureTree(root, nil), "lookup", "§"+"18")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{
		"successor stub: the content of",
		"changes/programs/v1.md\n",
		"docs/about/status.md#planned-after-v1",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("lookup of the stub: output lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "04_system-components.md#46") {
		t.Errorf("a link into a numbered file was listed as a successor:\n%s", out)
	}
}

// missingSection is a section number no fixture declares. It is built by
// concatenation so the citation gates do not read it as a citation of the
// repository's specification.
const missingSection = "§" + "9.9"

func TestLookupOfASectionWithoutAStubReadsTheFrozenTag(t *testing.T) {
	frozen := "# 4. System Components\n\n## 4.6 Pod Lifecycle Controllers\n\n### 4.6.1 Warm Pool Controller\n\nThe original text.\n\n#### Detail\n\nMore original text.\n\n### 4.6.2 Next\n\nNot printed.\n"
	git := func(_ context.Context, args ...string) ([]byte, error) {
		switch {
		case args[0] == "rev-parse":
			return []byte("abc\n"), nil
		case args[0] == "show" && args[1] == "spec-frozen:spec/04_system-components.md":
			return []byte(frozen), nil
		}
		return nil, errors.New("unexpected git call")
	}
	out, err := run(t, fixtureTree(cleanFixture, git), "lookup", "§4.6.1")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{
		"§4.6.1 Warm Pool Controller: spec/04_system-components.md#461-warm-pool-controller",
		"not a successor stub",
		"frozen text at spec-frozen:\n### 4.6.1 Warm Pool Controller",
		"More original text.",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("lookup §4.6.1: output lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "Not printed") {
		t.Errorf("the frozen text ran past the section:\n%s", out)
	}
	if _, err := run(t, fixtureTree(cleanFixture, nil), "lookup", missingSection); err == nil {
		t.Error("lookup of a missing section succeeded")
	}
	if _, err := run(t, fixtureTree(cleanFixture, nil), "lookup", "§four"); err == nil {
		t.Error("lookup of a malformed section number succeeded")
	}
}

func TestRunRefusesMalformedCommands(t *testing.T) {
	tree := fixtureTree(cleanFixture, nil)
	for _, args := range [][]string{{}, {"nope"}, {"check", "x"}, {"fix", "x"}, {"lookup"}, {"registry", "x"}, {"registry", "-bogus"}} {
		if _, err := run(t, tree, args...); err == nil {
			t.Errorf("args %q: no error", args)
		}
	}
	if err := Main(context.Background(), []string{"-root"}, &bytes.Buffer{}); err == nil {
		t.Error("Main accepted a flag with no value")
	}
}

func TestCheckReportsACodeThatBreaksTheGrammar(t *testing.T) {
	dir := t.TempDir()
	page := func(code string, extra string) string {
		return "---\ncode: " + code + "\ntype: capability\nsummary: s\n" + extra + "---\n\n# Page\n"
	}
	writeFile(t, dir, "spec/a.md", page("POOL", ""))
	writeFile(t, dir, "spec/b.md", page("P", ""))
	writeFile(t, dir, "spec/c.md", page("TOOLONGCODE", ""))
	writeFile(t, dir, "spec/d.md", page("PO1", ""))
	writeFile(t, dir, "spec/e.md", page("Pool", ""))
	writeFile(t, dir, "spec/f.md", page("ALIASED", "aliases: [OLD-CODE]\n"))
	findings, err := Check(context.Background(), fixtureTree(dir, nil))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range findings {
		if strings.Contains(f.Message, "is not two to nine upper-case letters") {
			got = append(got, f.String())
		}
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{"spec/b.md:2: code P ", "spec/c.md:2: code TOOLONGCODE ", "spec/d.md:2: code PO1 ", "spec/e.md:2: code Pool ", "spec/f.md:2: code OLD-CODE "} {
		if !strings.Contains(all, want) {
			t.Errorf("missing finding %q in:\n%s", want, all)
		}
	}
	if len(got) != 5 || strings.Contains(all, "code POOL ") || strings.Contains(all, "code ALIASED ") {
		t.Errorf("got %d grammar findings, want the five broken codes:\n%s", len(got), all)
	}
}

func TestCheckReportsAPageThatTakesAReservedIssueCode(t *testing.T) {
	dir := t.TempDir()
	page := func(code string, extra string) string {
		return "---\ncode: " + code + "\ntype: capability\nsummary: s\n" + extra + "---\n\n# Page\n"
	}
	writeFile(t, dir, "spec/a.md", page("GATE", ""))
	writeFile(t, dir, "spec/b.md", page("GATES", ""))
	writeFile(t, dir, "spec/c.md", page("GAT", ""))
	writeFile(t, dir, "spec/d.md", page("POOL", "aliases: [GATE]\n"))
	writeFile(t, dir, "spec/e.md", page("GATEWAY", ""))
	findings, err := Check(context.Background(), fixtureTree(dir, nil))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range findings {
		if strings.Contains(f.Message, "reserved") {
			got = append(got, f.String())
		}
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{
		"spec/a.md:2: code GATE is reserved for the issues about",
		"spec/b.md:2: code GATES differs from the reserved issue code GATE only by a suffix",
		"spec/c.md:2: code GAT differs from the reserved issue code GATE only by a suffix",
		"spec/d.md:2: code GATE is reserved",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing finding %q in:\n%s", want, all)
		}
	}
	if len(got) != 4 || strings.Contains(all, "GATEWAY") {
		t.Errorf("got %d reserved-code findings, want four and none for GATEWAY:\n%s", len(got), all)
	}
}
