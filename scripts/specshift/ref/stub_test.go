// SPDX-License-Identifier: MIT

package ref

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// The stub command and the stub reader are migration tooling for the
// specification's own structure rather than a platform behavior, so these
// cases carry no spec-section annotation. The contract they pin is the
// one "Successor stubs" in spec/conventions.md states: a stubbed section
// keeps its heading and anchor, gains a successor list built from the
// registry, and loses its text unless it keeps part of it; a full stub
// carries the marker; and lookup follows a section citation through the
// stub.

const (
	stubFixture  = "testdata/stub"
	stubNumbered = "spec/05_pools.md"
)

// sec returns a section citation. The section numbers of the stub fixture
// are built at run time so the citation gates do not read them as
// citations of the repository's specification.
func sec(number string) string { return "§" + number }

// stubTree copies the stub fixture and returns the copy's directory and a
// tree over it.
func stubTree(t *testing.T, git Git) (string, *Tree) {
	t.Helper()
	dir := copyFixture(t, stubFixture)
	return dir, fixtureTree(dir, git)
}

// sectionText returns the text of a numbered section in a fixture copy,
// from its heading up to the next heading of the same or a higher level.
func sectionText(t *testing.T, dir, number string) string {
	t.Helper()
	s, ok := findSection(stubNumbered, readFile(t, dir, stubNumbered), number)
	if !ok {
		t.Fatalf("no section %s in %s", number, stubNumbered)
	}
	return strings.Join(s.text, "\n")
}

func TestStubWritesAFullStub(t *testing.T) {
	dir, tree := stubTree(t, nil)
	before := readFile(t, dir, stubNumbered)
	out, err := run(t, tree, "stub", sec("5.1"), "--to", "POOL-R1,POOL-R2", "--to", "WPOOL")
	if err != nil {
		t.Fatalf("stub: %v\n%s", err, out)
	}
	want := "## 5.1 Pool Configuration\n\n" +
		StubMarker + "\n\n" +
		"The content of this section has moved to these pages and identifiers:\n\n" +
		"- [POOL-R1](capabilities/pools/warm-pools.md#pool-r1): A pool names one runtime.\n" +
		"- [POOL-R2](capabilities/pools/warm-pools.md#pool-r2): A pool keeps its minimum warm.\n" +
		"- [POOL](capabilities/pools/warm-pools.md): Warm pools\n"
	if got := sectionText(t, dir, "5.1"); got != want {
		t.Errorf("stubbed section:\n%s\nwant:\n%s", got, want)
	}
	after := readFile(t, dir, stubNumbered)
	if !strings.HasPrefix(after, before[:strings.Index(before, "## 5.1")]) || !strings.HasSuffix(after, before[strings.Index(before, "## 5.2"):]) {
		t.Errorf("the stub changed text outside the section:\n%s", after)
	}
	if !reflect.DeepEqual(headingAnchors(after), headingAnchors(before)) {
		t.Error("the stub changed a heading anchor")
	}
	for _, w := range []string{
		"stubbed " + sec("5.1") + " in " + stubNumbered,
		"5.1 Pool Configuration: full stub, 2 line(s) of text removed",
		"- [POOL](capabilities/pools/warm-pools.md): Warm pools",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stub output lacks %q:\n%s", w, out)
		}
	}
	mustCheckClean(t, tree)
}

func TestStubTitlesEachKindOfSuccessor(t *testing.T) {
	dir, tree := stubTree(t, nil)
	if _, err := run(t, tree, "stub", sec("5.3"), "--to", "reserved-hold,setting-pool-minwarm,KPCLAIM-create,PLACE,spec/capabilities/pools/warm-pools.md"); err != nil {
		t.Fatal(err)
	}
	got := sectionText(t, dir, "5.3")
	for _, w := range []string{
		"- [`reserved-hold`](capabilities/pools/warm-pools.md#reserved-hold): Reserved hold\n",
		"- [`setting-pool-minwarm`](capabilities/pools/warm-pools.md#setting-pool-minwarm): setting `pool.minWarm`\n",
		"- [KPCLAIM-create](interfaces/kubernetes/podclaim.md#kpclaim-create): operation on PodClaim\n",
		"- [PLACE](components/placement.md): Placement\n",
		"- [POOL](capabilities/pools/warm-pools.md): Warm pools\n",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("stub lacks %q:\n%s", w, got)
		}
	}
	if strings.Contains(got, "The placement text") || strings.Contains(got, "9.9 Not a heading") {
		t.Errorf("the stub kept text of the last section:\n%s", got)
	}
	if after := readFile(t, dir, stubNumbered); !strings.HasSuffix(after, "Warm pools\n") {
		t.Errorf("the file does not end with the list and one newline: %q", after[len(after)-40:])
	}
	mustCheckClean(t, tree)
}

func TestStubWithKeepWritesAPartialStubAndMergesLater(t *testing.T) {
	dir, tree := stubTree(t, nil)
	if _, err := run(t, tree, "stub", "--keep", "--to", "POOL-R1", sec("5.1")); err != nil {
		t.Fatal(err)
	}
	want := "## 5.1 Pool Configuration\n\n" +
		"Part of the content of this section has moved to these pages and identifiers:\n\n" +
		"- [POOL-R1](capabilities/pools/warm-pools.md#pool-r1): A pool names one runtime.\n\n" +
		"A pool names one runtime.\n\nA pool keeps its minimum warm.\n"
	if got := sectionText(t, dir, "5.1"); got != want {
		t.Errorf("partial stub:\n%s\nwant:\n%s", got, want)
	}
	if stubs := FullStubs(readFile(t, dir, stubNumbered)); len(stubs) != 0 {
		t.Errorf("a partial stub was reported as a full stub: %+v", stubs)
	}

	// A second partial stub merges into the list without repeating an
	// entry, and a full stub then removes the kept text.
	if _, err := run(t, tree, "stub", sec("5.1"), "--keep", "--to", "POOL-R1,POOL-R2"); err != nil {
		t.Fatal(err)
	}
	if got := sectionText(t, dir, "5.1"); strings.Count(got, "pool-r1") != 1 || !strings.Contains(got, "#pool-r2): A pool keeps") || !strings.Contains(got, "A pool keeps its minimum warm.\n") {
		t.Errorf("merged partial stub:\n%s", got)
	}
	out, err := run(t, tree, "stub", sec("5.1"), "--to", "POOL")
	if err != nil {
		t.Fatal(err)
	}
	want = "## 5.1 Pool Configuration\n\n" +
		StubMarker + "\n\n" +
		"The content of this section has moved to these pages and identifiers:\n\n" +
		"- [POOL-R1](capabilities/pools/warm-pools.md#pool-r1): A pool names one runtime.\n" +
		"- [POOL-R2](capabilities/pools/warm-pools.md#pool-r2): A pool keeps its minimum warm.\n" +
		"- [POOL](capabilities/pools/warm-pools.md): Warm pools\n"
	if got := sectionText(t, dir, "5.1"); got != want {
		t.Errorf("full stub after a partial one:\n%s\nwant:\n%s", got, want)
	}
	if !strings.Contains(out, "full stub, 2 line(s) of text removed") {
		t.Errorf("stub output: %s", out)
	}

	// Stubbing a full stub again changes nothing when the successor is
	// already listed.
	before := readFile(t, dir, stubNumbered)
	if _, err := run(t, tree, "stub", sec("5.1"), "--keep", "--to", "POOL-R2"); err != nil {
		t.Fatal(err)
	}
	if after := readFile(t, dir, stubNumbered); after != before {
		t.Errorf("a repeated stub changed the file:\n%s", after)
	}
	mustCheckClean(t, tree)
}

func TestStubRecursiveKeepsEveryHeading(t *testing.T) {
	dir, tree := stubTree(t, nil)
	before := readFile(t, dir, stubNumbered)
	out, err := run(t, tree, "stub", sec("5.2"), "--recursive", "--to", "POOL-R2")
	if err != nil {
		t.Fatalf("stub: %v\n%s", err, out)
	}
	after := readFile(t, dir, stubNumbered)
	if !reflect.DeepEqual(headingAnchors(after), headingAnchors(before)) {
		t.Error("a recursive stub changed a heading anchor")
	}
	for _, gone := range []string{"The claim text", "The queue text", "The ordering text", "The fallback text"} {
		if strings.Contains(after, gone) {
			t.Errorf("a recursive stub kept %q", gone)
		}
	}
	var got []string
	for _, s := range FullStubs(after) {
		got = append(got, s.Number+"|"+s.Title)
	}
	want := []string{"5.2|5.2 Pool Claims", "5.2.1|5.2.1 Claim Queue", "|Queue Ordering", "5.2.2|5.2.2 Claim Fallback"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("full stubs: %q, want %q", got, want)
	}
	if !strings.Contains(out, "Queue Ordering: full stub, 1 line(s) of text removed") {
		t.Errorf("stub output: %s", out)
	}
	if !strings.Contains(sectionText(t, dir, "5.3"), "The placement text") {
		t.Error("a recursive stub reached the next section")
	}
	mustCheckClean(t, tree)
}

func TestStubRefusesAndWritesNothing(t *testing.T) {
	cases := map[string][]string{
		"an unregistered identifier":        {"stub", sec("5.1"), "--to", "POOL-R1,POOL-R99"},
		"a retired identifier":              {"stub", sec("5.1"), "--to", "POOL-R3"},
		"a retired anchor":                  {"stub", sec("5.1"), "--to", "pool-r3"},
		"a missing section":                 {"stub", sec("5.9"), "--to", "POOL-R1"},
		"a section with subsections":        {"stub", sec("5.2"), "--to", "POOL-R1"},
		"subsections with keep":             {"stub", sec("5.2"), "--keep", "--to", "POOL-R1"},
		"a malformed section number":        {"stub", "§five", "--to", "POOL-R1"},
		"no successor":                      {"stub", sec("5.1")},
		"an empty successor list":           {"stub", sec("5.1"), "--to", ","},
		"no section":                        {"stub", "--to", "POOL-R1"},
		"two sections":                      {"stub", sec("5.1"), sec("5.3"), "--to", "POOL-R1"},
		"an unknown flag":                   {"stub", sec("5.1"), "--to", "POOL-R1", "--bogus"},
		"a heading inside an example fence": {"stub", sec("9.9"), "--to", "POOL-R1"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			dir, tree := stubTree(t, nil)
			before := readFile(t, dir, stubNumbered)
			if out, err := run(t, tree, args...); err == nil {
				t.Fatalf("stub %q succeeded:\n%s", args, out)
			}
			if after := readFile(t, dir, stubNumbered); after != before {
				t.Errorf("a refused stub changed the file:\n%s", after)
			}
		})
	}
	_, tree := stubTree(t, nil)
	_, err := run(t, tree, "stub", sec("5.1"), "--to", "POOL-R3,NOPE")
	if err == nil || !strings.Contains(err.Error(), "name its successor POOL-R2") || !strings.Contains(err.Error(), "NOPE is not a registered") {
		t.Errorf("refusal does not name every problem: %v", err)
	}
	_, err = run(t, tree, "stub", sec("5.2"), "--to", "POOL-R1")
	if err == nil || !strings.Contains(err.Error(), "3 subsection(s)") || !strings.Contains(err.Error(), "--recursive") {
		t.Errorf("subsection refusal: %v", err)
	}
}

func TestLookupFollowsAStub(t *testing.T) {
	_, tree := stubTree(t, nil)
	out, err := run(t, tree, "lookup", sec("5.1"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "not a successor stub: the content of "+sec("5.1")+" has not migrated") || strings.Contains(out, "successors:") {
		t.Errorf("lookup before the stub:\n%s", out)
	}

	if _, err := run(t, tree, "stub", sec("5.1"), "--to", "POOL-R1,POOL"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, tree, "stub", sec("5.3"), "--keep", "--to", "PLACE"); err != nil {
		t.Fatal(err)
	}
	out, err = run(t, tree, "lookup", sec("5.1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{
		sec("5.1 Pool Configuration") + ": " + stubNumbered + "#51-pool-configuration",
		"successor stub: the content of " + sec("5.1") + " has migrated",
		"successors:\n  spec/capabilities/pools/warm-pools.md#pool-r1\n  spec/capabilities/pools/warm-pools.md\n",
		"frozen text: the tag spec-frozen does not exist yet",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("lookup of a full stub lacks %q:\n%s", w, out)
		}
	}
	out, err = run(t, tree, "lookup", sec("5.3"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "partial successor stub: part of the content of "+sec("5.3")+" has migrated") || !strings.Contains(out, "  spec/components/placement.md\n") {
		t.Errorf("lookup of a partial stub:\n%s", out)
	}
}

func TestLookupOfAStubReadsTheFrozenText(t *testing.T) {
	original := readFile(t, stubFixture, stubNumbered)
	git := func(_ context.Context, args ...string) ([]byte, error) {
		switch {
		case args[0] == "rev-parse":
			return []byte("abc\n"), nil
		case args[0] == "show" && args[1] == FrozenTag+":"+stubNumbered:
			return []byte(original), nil
		}
		return nil, errors.New("unexpected git call")
	}
	_, tree := stubTree(t, git)
	if _, err := run(t, tree, "stub", sec("5.2"), "--recursive", "--to", "POOL-R2"); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, tree, "lookup", sec("5.2.1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{
		"successor stub: the content of " + sec("5.2.1") + " has migrated",
		"  spec/capabilities/pools/warm-pools.md#pool-r2\n",
		"frozen text at spec-frozen:\n### 5.2.1 Claim Queue\n\nThe queue text.",
		"The ordering text.",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("lookup of a stub at the frozen tag lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "The fallback text") {
		t.Errorf("the frozen text ran past the section:\n%s", out)
	}
}

func TestFullStubsReadsTheMarker(t *testing.T) {
	content := "# 7. File\n\n" +
		"## 7.1 Stub\n\n\n" + StubMarker + "\n\nThe content of this section has moved to these pages and identifiers:\n\n- [A](a.md)\n\n" +
		"## 7.2 Text first\n\nText.\n\n" + StubMarker + "\n\n" +
		"## 7.3 Partial\n\nPart of the content of this section has moved to these pages and identifiers:\n\n- [A](a.md)\n\nText.\n\n" +
		"## 7.4 Fenced\n\n```text\n" + StubMarker + "\n```\n\n" +
		"### Unnumbered {#kept-anchor}\n" + StubMarker + "\n"
	got := FullStubs(content)
	want := []StubSection{
		{Number: "7.1", Title: "7.1 Stub", Anchor: "71-stub", Level: 2, Line: 3},
		{Number: "", Title: "Unnumbered", Anchor: "kept-anchor", Level: 3, Line: 32},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FullStubs:\n%+v\nwant:\n%+v", got, want)
	}
	if FullStubs("") != nil || FullStubs("# 7. File\n\nText.\n") != nil {
		t.Error("a document with no stub reported one")
	}
}
