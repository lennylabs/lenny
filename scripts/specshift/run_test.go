// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/lenny/scripts/specshift/citation"
	"github.com/lennylabs/lenny/scripts/specshift/pass"
	"github.com/lennylabs/lenny/scripts/specshift/register"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// These cases cover migration tooling rather than a platform behavior, so
// they carry no spec-section annotation. The contract they pin is the one
// the package comment on scope states: a single shared file domain, a
// per-file generated-artifact rule, a validated residual register, and a
// dry run whose diff matches the apply.

// fixtureTree is the tracked-tree fixture the domain cases run against.
// It holds one ordinary carrier plus one member of every excluded group.
const fixtureTree = "testdata/tree"

// fixtureRegisters holds the residual-register fixtures.
const fixtureRegisters = "testdata/registers"

// treeDomain lists and reads the fixture tree.
func treeDomain(t *testing.T) (scope.Lister, scope.FileReader) {
	t.Helper()
	return scope.DirLister(fixtureTree), scope.DirReader(fixtureTree)
}

// TestReadDomainSelectsCarrierAndRejectsEveryExcludedGroup pins the walk
// over the tracked tree: an ordinary carrier is in the read domain, and
// each excluded group is out of it. The groups are the staged proposal
// tree, the two historical audit records, the two root planning
// documents, the two citation registers the gates consume, and every
// fixture directory.
func TestReadDomainSelectsCarrierAndRejectsEveryExcludedGroup(t *testing.T) {
	t.Parallel()
	list, _ := treeDomain(t)
	domain, err := scope.ReadDomain(context.Background(), list)
	if err != nil {
		t.Fatalf("ReadDomain over the fixture tree: %v", err)
	}
	in := map[string]bool{}
	for _, p := range domain {
		in[p] = true
	}
	if !in["pkg/carrier/carrier.go"] {
		t.Errorf("read domain omits the ordinary carrier: %v", domain)
	}
	for _, excluded := range []struct {
		path  string
		group string
	}{
		{"proposals/0001_example.md", "the staged proposal tree"},
		{"BUILD-GAPS.md", "the build audit record"},
		{"TEST-GAPS.md", "the test audit record"},
		{"gateway-runtime-comms.md", "a root planning document"},
		{"gateway-runtime-comms-remediation.md", "a root planning document"},
		{"tests/registers/line-citations.yaml", "the per-file citation register"},
		{"tests/registers/line-citation-resolution.yaml", "the resolution baseline"},
		{"pkg/carrier/testdata/fixture.md", "a fixture directory"},
	} {
		if in[excluded.path] {
			t.Errorf("read domain admits %s (%s)", excluded.path, excluded.group)
		}
		if scope.Readable(excluded.path) {
			t.Errorf("Readable(%q) = true, want false (%s)", excluded.path, excluded.group)
		}
	}
}

// TestReadDomainFailsOnAnEmptyTree pins that a walk which inspected
// nothing does not certify the tree.
func TestReadDomainFailsOnAnEmptyTree(t *testing.T) {
	t.Parallel()
	empty := func(context.Context) ([]string, error) { return nil, nil }
	if _, err := scope.ReadDomain(context.Background(), empty); err == nil {
		t.Fatal("ReadDomain over an empty tree returned no error")
	}
}

// TestReadDomainFailsWhenTheExclusionListSelectsZeroFiles pins the
// zero-inspection case over a tree that is not empty. A walk root
// pointed at an excluded subtree lists paths and then filters every one
// of them, and an empty domain reads to every caller as a completed
// inspection: the pass reports an empty diff and the gate reports green
// over content neither opened.
func TestReadDomainFailsWhenTheExclusionListSelectsZeroFiles(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		paths []string
	}{
		{"a walk root under the staged proposal tree", []string{"proposals/0001_example.md", "proposals/0002_example.md"}},
		{"a walk root inside a fixture directory", []string{"pkg/carrier/testdata/fixture.md"}},
		{"a tree whose only paths are the citation registers", []string{
			"tests/registers/line-citations.yaml",
			"tests/registers/line-citation-resolution.yaml",
		}},
	} {
		paths := tc.paths
		list := func(context.Context) ([]string, error) { return paths, nil }
		domain, err := scope.ReadDomain(context.Background(), list)
		if err == nil {
			t.Errorf("ReadDomain over %s returned %v with no error", tc.name, domain)
		}
	}
}

// TestWriteDomainFailsWhenEveryReadablePathIsExcludedFromWriting pins
// the same guard over the write domain, which has exclusions of its own.
// A pass whose write domain collapses to nothing aborts rather than
// reporting the empty diff of a completed migration.
func TestWriteDomainFailsWhenEveryReadablePathIsExcludedFromWriting(t *testing.T) {
	t.Parallel()
	_, read := treeDomain(t)
	list := func(context.Context) ([]string, error) {
		return []string{"charts/lenny/values.schema.json", "charts/lenny/crds/lenny.dev_runtimes.yaml"}, nil
	}
	domain, err := scope.WriteDomain(context.Background(), list, scope.Line, read)
	if err == nil {
		t.Fatalf("WriteDomain over a tree of generated artifacts returned %v with no error", domain)
	}
}

// TestReadableKeepsAPathNamedLikeAFixtureDirectory pins that the fixture
// exclusion matches a path segment rather than a substring, so a file
// whose name merely starts with the segment stays in the domain.
func TestReadableKeepsAPathNamedLikeAFixtureDirectory(t *testing.T) {
	t.Parallel()
	if !scope.Readable("pkg/carrier/testdata.go") {
		t.Error("Readable(pkg/carrier/testdata.go) = false, want true")
	}
}

// TestWriteDomainExcludesThePlanningRecordsForTheNameAndIdentifierPasses
// pins the difference between the two write lists. A reserved phrase in
// a build or queue record is part of what was written at the time, while
// a line citation in the same file is a pointer that has to keep
// resolving.
func TestWriteDomainExcludesThePlanningRecordsForTheNameAndIdentifierPasses(t *testing.T) {
	t.Parallel()
	list, read := treeDomain(t)
	records := []string{"BUILD-PLAN.md", "BUILD-PROGRESS.md", "PROPOSAL-QUEUE.md"}
	for _, p := range []scope.Pass{scope.Name, scope.Identifier} {
		domain, err := scope.WriteDomain(context.Background(), list, p, read)
		if err != nil {
			t.Fatalf("WriteDomain(%s): %v", p, err)
		}
		in := membership(domain)
		for _, rec := range records {
			if in[rec] {
				t.Errorf("%s pass write domain admits %s", p, rec)
			}
		}
		if !in["pkg/carrier/carrier.go"] {
			t.Errorf("%s pass write domain omits the ordinary carrier", p)
		}
	}
	for _, p := range []scope.Pass{scope.Anchor, scope.Line} {
		domain, err := scope.WriteDomain(context.Background(), list, p, read)
		if err != nil {
			t.Fatalf("WriteDomain(%s): %v", p, err)
		}
		in := membership(domain)
		for _, rec := range records {
			if !in[rec] {
				t.Errorf("%s pass write domain omits %s", p, rec)
			}
		}
	}
}

// TestClassReadDomainIsExportedForTheReservedPhraseAndIdentifierClasses
// pins that the domain a residual scan ranges over comes from the shared
// implementation. The scan for the reserved-phrase and identifier
// classes excludes the root-level planning records and applies no
// generated-artifact subtraction, because a generated artifact carries a
// per-file count and reaches zero through the regeneration of its
// source. A gate that restated the record list in its own source is the
// drift the single shared implementation exists to prevent.
func TestClassReadDomainIsExportedForTheReservedPhraseAndIdentifierClasses(t *testing.T) {
	t.Parallel()
	list, _ := treeDomain(t)
	records := scope.PlanningRecords()
	if len(records) != 3 {
		t.Fatalf("PlanningRecords() = %v", records)
	}
	for _, c := range []scope.Class{scope.ClassReservedPhrase, scope.ClassIdentifier} {
		domain, err := scope.ClassReadDomain(context.Background(), list, c)
		if err != nil {
			t.Fatalf("ClassReadDomain(%s): %v", c, err)
		}
		in := membership(domain)
		for _, rec := range records {
			if in[rec] {
				t.Errorf("%s class read domain admits %s", c, rec)
			}
		}
		// The generated artifacts stay in the class read domain: the
		// residual scan ranges wider than the pass writes.
		if !in["charts/lenny/crds/lenny.dev_runtimes.yaml"] {
			t.Errorf("%s class read domain omits a generated artifact", c)
		}
		if !in["pkg/carrier/carrier.go"] {
			t.Errorf("%s class read domain omits the ordinary carrier", c)
		}
	}
	for _, c := range []scope.Class{scope.ClassAnchor, scope.ClassLineCitations} {
		domain, err := scope.ClassReadDomain(context.Background(), list, c)
		if err != nil {
			t.Fatalf("ClassReadDomain(%s): %v", c, err)
		}
		in := membership(domain)
		for _, rec := range records {
			if !in[rec] {
				t.Errorf("%s class read domain omits %s", c, rec)
			}
		}
	}
	if _, err := scope.ClassReadDomain(context.Background(), list, scope.Class("reduction")); err == nil {
		t.Error("ClassReadDomain with an unknown class returned no error")
	}
}

// TestClassReadDomainExcludesTheClassOwnRegisters pins the third
// exclusion of a class's read domain. A residual entry holds a copy of
// its member's text, so a scan that read its own residual register, or
// the pass or baseline register its gate consumes, would report that
// copy under the register's own path as a further member, and the entry
// seeded for the copy would add another copy. The seeding does not
// converge. The exclusion is per class: another class's register stays
// ordinary tree content, because a member found there is recorded in a
// register the reading class does not open.
func TestClassReadDomainExcludesTheClassOwnRegisters(t *testing.T) {
	t.Parallel()
	list, _ := treeDomain(t)
	domain, err := scope.ClassReadDomain(context.Background(), list, scope.ClassReservedPhrase)
	if err != nil {
		t.Fatalf("ClassReadDomain(%s): %v", scope.ClassReservedPhrase, err)
	}
	in := membership(domain)
	own := scope.ClassReservedPhrase.Registers()
	if len(own) != 2 {
		t.Fatalf("Registers() = %v", own)
	}
	for _, reg := range own {
		if in[reg] {
			t.Errorf("%s class read domain admits its own register %s", scope.ClassReservedPhrase, reg)
		}
		ok, err := scope.ReadableForClass(scope.ClassReservedPhrase, reg)
		if err != nil {
			t.Fatalf("ReadableForClass(%s, %s): %v", scope.ClassReservedPhrase, reg, err)
		}
		if ok {
			t.Errorf("ReadableForClass(%s, %q) = true, want false", scope.ClassReservedPhrase, reg)
		}
	}
	const other = "tests/registers/residual-anchors.yaml"
	if !in[other] {
		t.Errorf("%s class read domain omits another class's register %s", scope.ClassReservedPhrase, other)
	}
	if _, err := scope.ReadableForClass(scope.Class("reduction"), "pkg/carrier/carrier.go"); err == nil {
		t.Error("ReadableForClass with an unknown class returned no error")
	}
}

// TestEveryClassCarriesItsOwnRegistersAndNoPassLacksAClass pins that the
// register set is held as data for every class the residual scan covers,
// and that each pass resolves to the class whose domain it writes, so a
// gate reads the exclusion from here instead of re-deriving it.
func TestEveryClassCarriesItsOwnRegistersAndNoPassLacksAClass(t *testing.T) {
	t.Parallel()
	for _, c := range scope.Classes() {
		regs := c.Registers()
		if len(regs) == 0 {
			t.Errorf("class %s carries no register", c)
		}
		want := "tests/registers/residual-" + string(c) + ".yaml"
		if regs[0] != want {
			t.Errorf("class %s first register = %q, want %q", c, regs[0], want)
		}
	}
	for _, p := range scope.Passes() {
		c, err := scope.PassClass(p)
		if err != nil {
			t.Fatalf("PassClass(%s): %v", p, err)
		}
		if !c.Valid() {
			t.Errorf("PassClass(%s) = %q, which is not a class", p, c)
		}
	}
	if _, err := scope.PassClass(scope.Pass("reduction")); err == nil {
		t.Error("PassClass with an unknown pass returned no error")
	}
}

// TestWriteDomainAdmitsTheRegistersTheClassReadDomainExcludes pins that
// the residual scan's register exclusion is not a write exclusion. A
// class's sense register and residual register are ordinary members of
// the shared read domain, so the naming lint and the citation gates read
// them and report the sites they carry. A sense register holds a bare
// reserved phrase at every entry by construction, so a write domain that
// excluded it would leave a site every gate reports with no pass able to
// rewrite it.
func TestWriteDomainAdmitsTheRegistersTheClassReadDomainExcludes(t *testing.T) {
	t.Parallel()
	list, read := treeDomain(t)
	domain, err := scope.WriteDomain(context.Background(), list, scope.Name, read)
	if err != nil {
		t.Fatalf("WriteDomain(%s): %v", scope.Name, err)
	}
	in := membership(domain)
	for _, reg := range scope.ClassReservedPhrase.Registers() {
		if !in[reg] {
			t.Errorf("%s pass write domain omits %s, which the shared read domain admits", scope.Name, reg)
		}
		ok, err := scope.ReadableForClass(scope.ClassReservedPhrase, reg)
		if err != nil {
			t.Fatalf("ReadableForClass(%s, %s): %v", scope.ClassReservedPhrase, reg, err)
		}
		if ok {
			t.Errorf("%s residual scan reads its own register %s", scope.ClassReservedPhrase, reg)
		}
	}
}

// TestEveryClassReadDomainPathIsWritableBySomePass extends the
// readable-implies-writable property from the shared read domain to
// every class read domain, so a register a later sub-step seeds inside a
// gate's read domain but outside every pass's write domain turns this
// package red rather than surfacing as a lint failure with no route to
// green.
func TestEveryClassReadDomainPathIsWritableBySomePass(t *testing.T) {
	t.Parallel()
	list, read := treeDomain(t)
	writable := map[string]bool{}
	for _, p := range scope.Passes() {
		domain, err := scope.WriteDomain(context.Background(), list, p, read)
		if err != nil {
			t.Fatalf("WriteDomain(%s): %v", p, err)
		}
		for _, target := range domain {
			writable[target] = true
		}
	}
	for _, c := range scope.Classes() {
		domain, err := scope.ClassReadDomain(context.Background(), list, c)
		if err != nil {
			t.Fatalf("ClassReadDomain(%s): %v", c, err)
		}
		for _, target := range domain {
			if writable[target] {
				continue
			}
			disjunct, err := scope.Generated(target, read)
			if err != nil {
				t.Fatalf("Generated(%s): %v", target, err)
			}
			if disjunct == scope.NotGenerated {
				t.Errorf("%s is inside the %s read domain and generated by nothing, yet no pass may write it", target, c)
			}
		}
	}
}

// TestWriteDomainExcludesEveryGeneratedArtifact pins that no pass writes
// a derived file, whichever disjunct of the generated-artifact rule
// selected it.
func TestWriteDomainExcludesEveryGeneratedArtifact(t *testing.T) {
	t.Parallel()
	list, read := treeDomain(t)
	for _, p := range scope.Passes() {
		domain, err := scope.WriteDomain(context.Background(), list, p, read)
		if err != nil {
			t.Fatalf("WriteDomain(%s): %v", p, err)
		}
		in := membership(domain)
		for _, generated := range []string{
			"charts/lenny/crds/lenny.dev_runtimes.yaml",
			"charts/lenny/values.schema.json",
			"pkg/generated/marked.go",
		} {
			if in[generated] {
				t.Errorf("%s pass write domain admits the generated artifact %s", p, generated)
			}
		}
	}
}

// TestWritableRejectsAnUnknownPass pins that the write domain fails
// closed on a pass name it does not carry.
func TestWritableRejectsAnUnknownPass(t *testing.T) {
	t.Parallel()
	_, read := treeDomain(t)
	if _, err := scope.Writable(scope.Pass("reduction"), "pkg/carrier/carrier.go", read); err == nil {
		t.Fatal("Writable with an unknown pass returned no error")
	}
}

// TestWritableFailsClosedWhenTheFileCannotBeRead pins that a file the
// generated-artifact rule cannot classify is never treated as an
// ordinary carrier.
func TestWritableFailsClosedWhenTheFileCannotBeRead(t *testing.T) {
	t.Parallel()
	failing := func(string) ([]byte, error) { return nil, errors.New("unreadable") }
	writable, err := scope.Writable(scope.Line, "pkg/carrier/carrier.go", failing)
	if err == nil {
		t.Fatal("Writable over an unreadable file returned no error")
	}
	if writable {
		t.Error("Writable over an unreadable file reported the file writable")
	}
}

// TestGeneratedRuleSelectsTheChartCRDsThroughTheProducerOutputDisjunct
// pins the disjunct the chart CRDs are selected by. They carry no header
// generation marker and no document-metadata declaration, so a
// marker-only rule would classify them as ordinary carriers and direct a
// pass to write them.
func TestGeneratedRuleSelectsTheChartCRDsThroughTheProducerOutputDisjunct(t *testing.T) {
	t.Parallel()
	const target = "charts/lenny/crds/lenny.dev_runtimes.yaml"
	_, read := treeDomain(t)

	// The header and metadata disjuncts do not fire on this file: the
	// producer-output disjunct is the only one that selects it.
	content, err := read(target)
	if err != nil {
		t.Fatalf("read %s: %v", target, err)
	}
	if got, err := scope.Generated(target, refuseAllReads(t)); err != nil || got != scope.ProducerOutput {
		t.Fatalf("Generated(%s) = %q, %v; want %q with no read", target, got, err, scope.ProducerOutput)
	}
	relocated := "pkg/relocated/lenny.dev_runtimes.yaml"
	stable := func(string) ([]byte, error) { return content, nil }
	if got, err := scope.Generated(relocated, stable); err != nil || got != scope.NotGenerated {
		t.Fatalf("Generated over the same content outside the producer output set = %q, %v; want %q",
			got, err, scope.NotGenerated)
	}
}

// TestProducerOutputDisjunctSelectsACopyAndSparesItsAuthoredNeighbour
// pins that the generated-artifact rule is applied per file rather than
// per directory over a copying producer's target directory.
// pkg/embedded/crds/ holds the copied manifests alongside the embedding
// package's own authored source, which carries line citations. Selecting
// the directory takes the authored file out of every write domain while
// the citation gates keep counting it, so its per-file count can never
// reach zero.
func TestProducerOutputDisjunctSelectsACopyAndSparesItsAuthoredNeighbour(t *testing.T) {
	t.Parallel()
	list, read := treeDomain(t)
	const (
		copied   = "pkg/embedded/crds/lenny.dev_runtimes.yaml"
		authored = "pkg/embedded/crds/crds.go"
	)
	if got, err := scope.Generated(copied, read); err != nil || got != scope.ProducerOutput {
		t.Fatalf("Generated(%s) = %q, %v; want %q", copied, got, err, scope.ProducerOutput)
	}
	if got, err := scope.Generated(authored, read); err != nil || got != scope.NotGenerated {
		t.Fatalf("Generated(%s) = %q, %v; want %q", authored, got, err, scope.NotGenerated)
	}
	for _, p := range scope.Passes() {
		domain, err := scope.WriteDomain(context.Background(), list, p, read)
		if err != nil {
			t.Fatalf("WriteDomain(%s): %v", p, err)
		}
		in := membership(domain)
		if in[copied] {
			t.Errorf("%s pass write domain admits the copied manifest %s", p, copied)
		}
		if !in[authored] {
			t.Errorf("%s pass write domain omits the authored carrier %s", p, authored)
		}
	}
}

// TestEveryReadableTrackedPathIsWritableBySomePass pins the property the
// per-file generated-artifact rule exists to hold over the committed
// tree: a file the read domain admits, and which therefore feeds the
// citation gates' per-file counts, is writable by at least one pass. A
// readable file no pass can write has no route to a zero count.
func TestEveryReadableTrackedPathIsWritableBySomePass(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	list, read := scope.GitLister(root), scope.DirReader(root)
	readable, err := scope.ReadDomain(context.Background(), list)
	if err != nil {
		t.Fatalf("ReadDomain over the tracked tree: %v", err)
	}
	writable := map[string]bool{}
	for _, p := range scope.Passes() {
		domain, err := scope.WriteDomain(context.Background(), list, p, read)
		if err != nil {
			t.Fatalf("WriteDomain(%s): %v", p, err)
		}
		for _, target := range domain {
			writable[target] = true
		}
	}
	for _, target := range readable {
		if writable[target] {
			continue
		}
		disjunct, err := scope.Generated(target, read)
		if err != nil {
			t.Fatalf("Generated(%s): %v", target, err)
		}
		if disjunct == scope.NotGenerated {
			t.Errorf("%s is readable and generated by nothing, yet no pass may write it", target)
		}
	}
}

// TestGeneratedRuleSelectsTheChartSchemaThroughTheMetadataDisjunct pins
// the case the second disjunct covers: JSON carries no comment syntax,
// so the schema's generation notice sits in its top-level description.
func TestGeneratedRuleSelectsTheChartSchemaThroughTheMetadataDisjunct(t *testing.T) {
	t.Parallel()
	const target = "charts/lenny/values.schema.json"
	_, read := treeDomain(t)
	content, err := read(target)
	if err != nil {
		t.Fatalf("read %s: %v", target, err)
	}
	// Reading the file under a path outside every producer output set
	// isolates the metadata disjunct from the producer-output one.
	relocated := "charts/lenny/other.schema.json"
	stable := func(string) ([]byte, error) { return content, nil }
	if got, err := scope.Generated(relocated, stable); err != nil || got != scope.DocumentMetadata {
		t.Fatalf("Generated(%s) = %q, %v; want %q", relocated, got, err, scope.DocumentMetadata)
	}
	// The same JSON without the generation notice is an ordinary carrier.
	plain := bytes.Replace(content, []byte("generated from pkg/chart/values"), []byte("hand written"), 1)
	if bytes.Equal(plain, content) {
		t.Fatal("the schema fixture no longer carries the generation notice the case pins")
	}
	plainRead := func(string) ([]byte, error) { return plain, nil }
	if got, err := scope.Generated(relocated, plainRead); err != nil || got != scope.NotGenerated {
		t.Fatalf("Generated over the schema without its notice = %q, %v; want %q", got, err, scope.NotGenerated)
	}
}

// TestMetadataDisjunctReadsADeclarationRatherThanProseAboutGeneration
// pins that the second disjunct is anchored the way the first is. An
// authored JSON whose description mentions generation mid-sentence, and
// whose title carries a generation phrase, is an ordinary carrier: the
// rule reads a declaration about the document in the document's
// description, and nothing else. A file the rule wrongly selects leaves
// every pass's write domain with no route back, while the citation gates
// keep counting it, so its per-file count can never reach zero.
func TestMetadataDisjunctReadsADeclarationRatherThanProseAboutGeneration(t *testing.T) {
	t.Parallel()
	const authored = "schemas/authored-plan.json"
	list, read := treeDomain(t)
	if got, err := scope.Generated(authored, read); err != nil || got != scope.NotGenerated {
		t.Fatalf("Generated(%s) = %q, %v; want %q", authored, got, err, scope.NotGenerated)
	}
	for _, p := range scope.Passes() {
		domain, err := scope.WriteDomain(context.Background(), list, p, read)
		if err != nil {
			t.Fatalf("WriteDomain(%s): %v", p, err)
		}
		if !membership(domain)[authored] {
			t.Errorf("%s pass write domain omits the authored schema %s", p, authored)
		}
	}
	// The same document with a declaration opening its description is
	// selected, which is the form charts/lenny/values.schema.json carries.
	content, err := read(authored)
	if err != nil {
		t.Fatalf("read %s: %v", authored, err)
	}
	declared := bytes.Replace(content,
		[]byte(`"description": "Schema for the workspace plans`),
		[]byte(`"description": "Generated from the session plan. Schema for the workspace plans`), 1)
	if bytes.Equal(declared, content) {
		t.Fatal("the authored schema fixture no longer carries the description the case rewrites")
	}
	declaredRead := func(string) ([]byte, error) { return declared, nil }
	if got, err := scope.Generated(authored, declaredRead); err != nil || got != scope.DocumentMetadata {
		t.Fatalf("Generated over the declaring schema = %q, %v; want %q", got, err, scope.DocumentMetadata)
	}
}

// TestMetadataDisjunctTreatsADocumentWithNoTopLevelObjectAsAuthored pins
// the boundary between the disjunct's fail-closed case and its
// declares-nothing case. A document the rule could not parse fails; a
// document that parses and carries no top-level object, an authored JSON
// array for instance, has no top-level metadata to declare anything and
// is an ordinary carrier. Failing on it would abort every pass and every
// domain computation over the whole tree because one authored file is an
// array.
func TestMetadataDisjunctTreatsADocumentWithNoTopLevelObjectAsAuthored(t *testing.T) {
	t.Parallel()
	const authored = "schemas/authored-list.json"
	list, read := treeDomain(t)
	if got, err := scope.Generated(authored, read); err != nil || got != scope.NotGenerated {
		t.Fatalf("Generated(%s) = %q, %v; want %q and no error", authored, got, err, scope.NotGenerated)
	}
	for _, p := range scope.Passes() {
		domain, err := scope.WriteDomain(context.Background(), list, p, read)
		if err != nil {
			t.Fatalf("WriteDomain(%s) over a tree carrying an authored JSON array: %v", p, err)
		}
		if !membership(domain)[authored] {
			t.Errorf("%s pass write domain omits the authored array %s", p, authored)
		}
	}
	// Every other top-level JSON value the rule can parse is the same
	// answer, and a document that does not parse still fails.
	for _, content := range []string{`"a string"`, `42`, `null`} {
		scalar := func(string) ([]byte, error) { return []byte(content), nil }
		if got, err := scope.Generated(authored, scalar); err != nil || got != scope.NotGenerated {
			t.Errorf("Generated over the top-level JSON value %s = %q, %v; want %q and no error", content, got, err, scope.NotGenerated)
		}
	}
}

// TestGeneratedRuleSelectsAMarkedHeader pins the first disjunct.
func TestGeneratedRuleSelectsAMarkedHeader(t *testing.T) {
	t.Parallel()
	_, read := treeDomain(t)
	if got, err := scope.Generated("pkg/generated/marked.go", read); err != nil || got != scope.HeaderMarker {
		t.Fatalf("Generated(pkg/generated/marked.go) = %q, %v; want %q", got, err, scope.HeaderMarker)
	}
	if got, err := scope.Generated("pkg/carrier/carrier.go", read); err != nil || got != scope.NotGenerated {
		t.Fatalf("Generated(pkg/carrier/carrier.go) = %q, %v; want %q", got, err, scope.NotGenerated)
	}
}

// TestGeneratedRuleSelectsAMarkerBelowTheLeadingLines pins the header
// disjunct on the convention this tree actually uses: a licence line, a
// blank line, and a multi-line doc comment sit above the generation
// marker, which puts the marker below the leading lines a fixed window
// would read. The carrier sits outside every producer output set, so
// only the header disjunct can select it. A scan that stops above the
// marker reports the file as an ordinary carrier and admits a generated
// artifact to every pass's write domain, and because the same predicate
// defines the generated-artifact class, the misclassified file cannot
// surface as a residual either.
func TestGeneratedRuleSelectsAMarkerBelowTheLeadingLines(t *testing.T) {
	t.Parallel()
	list, read := treeDomain(t)
	const target = "pkg/generated/deep-header.go"
	for _, p := range scope.Producers() {
		for _, out := range p.Outputs {
			if target == out || (strings.HasSuffix(out, "/") && strings.HasPrefix(target, out)) {
				t.Fatalf("%s is in the output set of %q, so the test cannot isolate the header disjunct", target, p.Command)
			}
		}
	}
	if got, err := scope.Generated(target, read); err != nil || got != scope.HeaderMarker {
		t.Fatalf("Generated(%s) = %q, %v; want %q", target, got, err, scope.HeaderMarker)
	}
	for _, p := range scope.Passes() {
		domain, err := scope.WriteDomain(context.Background(), list, p, read)
		if err != nil {
			t.Fatalf("WriteDomain(%s): %v", p, err)
		}
		if membership(domain)[target] {
			t.Errorf("%s pass write domain includes the generated artifact %s", p, target)
		}
	}
}

// TestGeneratedRuleTreatsProseAboutGenerationAsAuthored pins that the
// header disjunct reads a declaration rather than any sentence that
// mentions generation. A file the rule wrongly selects is removed from
// every pass's write domain with no route back, because a residual
// triage records the misclassification without returning the file to a
// write domain, so its per-file citation count can never reach zero.
func TestGeneratedRuleTreatsProseAboutGenerationAsAuthored(t *testing.T) {
	t.Parallel()
	list, read := treeDomain(t)
	authored := []string{
		// A markdown body sentence. Markdown's leading `#` is a heading
		// rather than a comment, so no line of this file is a declaration.
		"docs/authored-guide.md",
		// A comment line whose generation phrase sits mid-sentence.
		"pkg/carrier/toolcache.conf",
	}
	for _, target := range authored {
		if got, err := scope.Generated(target, read); err != nil || got != scope.NotGenerated {
			t.Errorf("Generated(%s) = %q, %v; want %q", target, got, err, scope.NotGenerated)
		}
	}
	for _, p := range scope.Passes() {
		domain, err := scope.WriteDomain(context.Background(), list, p, read)
		if err != nil {
			t.Fatalf("WriteDomain(%s): %v", p, err)
		}
		in := membership(domain)
		for _, target := range authored {
			if !in[target] {
				t.Errorf("%s pass write domain omits the authored carrier %s", p, target)
			}
		}
	}
}

// TestGeneratedRuleSelectsAMarkupDeclarationInAnHTMLComment pins the
// other half of the dialect rule: a markup carrier declares generation
// in an HTML comment, which is its only comment syntax, and a
// declaration on a continuation line of that block still counts.
func TestGeneratedRuleSelectsAMarkupDeclarationInAnHTMLComment(t *testing.T) {
	t.Parallel()
	_, read := treeDomain(t)
	if got, err := scope.Generated("docs/generated-note.md", read); err != nil || got != scope.HeaderMarker {
		t.Fatalf("Generated(docs/generated-note.md) = %q, %v; want %q", got, err, scope.HeaderMarker)
	}
}

// TestGeneratedRuleFailsWithoutAReader pins that the rule reports a
// missing reader rather than answering that a file is an ordinary
// carrier.
func TestGeneratedRuleFailsWithoutAReader(t *testing.T) {
	t.Parallel()
	if _, err := scope.Generated("pkg/carrier/carrier.go", nil); err == nil {
		t.Fatal("Generated with no reader returned no error")
	}
}

// TestGeneratedRuleFailsRatherThanClassifyingFromAnIncompleteHeaderScan
// pins that the header disjunct is fail-closed on the read as well as on
// the open. A header line longer than the scanner's token limit used to
// end the scan silently and answer that the file was an ordinary carrier,
// which admits a generated artifact to every pass's write domain.
func TestGeneratedRuleFailsRatherThanClassifyingFromAnIncompleteHeaderScan(t *testing.T) {
	t.Parallel()
	oversized := append(bytes.Repeat([]byte("x"), 2*1024*1024),
		[]byte("\n// Code generated by a producer. DO NOT EDIT.\n")...)
	read := func(target string) ([]byte, error) {
		if target != "pkg/carrier/oversized.go" {
			return nil, fs.ErrNotExist
		}
		return oversized, nil
	}
	got, err := scope.Generated("pkg/carrier/oversized.go", read)
	if err == nil {
		t.Fatalf("Generated over an unscannable header returned %q and no error", got)
	}
	if got != scope.NotGenerated {
		t.Errorf("the failed classification reported the disjunct %q", got)
	}
	// The write domain inherits the failure rather than admitting the file.
	if _, err := scope.Writable(scope.Line, "pkg/carrier/oversized.go", read); err == nil {
		t.Error("Writable admitted a file the generated-artifact rule could not classify")
	}
}

// TestGeneratedRuleFailsRatherThanClassifyingFromAnUnparseableDocument
// pins that the document-metadata disjunct is fail-closed the way the
// header disjunct and the producer-output one are. A commentless document
// that does not parse used to answer that the file was an ordinary
// carrier, which admits a generated artifact to every pass's write domain
// with no signal that the rule never reached a decision.
func TestGeneratedRuleFailsRatherThanClassifyingFromAnUnparseableDocument(t *testing.T) {
	t.Parallel()
	const target = "schemas/unparseable.json"
	unparseable := [][]byte{
		// Truncated mid-document.
		[]byte(`{"description": "Generated by a producer. DO NOT EDIT."`),
		// BOM-prefixed, which encoding/json rejects.
		append([]byte{0xef, 0xbb, 0xbf}, []byte(`{"description": "Generated by a producer."}`)...),
		// Carrying a comment, which JSON has no syntax for.
		[]byte("// Code generated by a producer. DO NOT EDIT.\n{\"description\": \"x\"}"),
	}
	for _, content := range unparseable {
		read := func(path string) ([]byte, error) {
			if path != target {
				return nil, fs.ErrNotExist
			}
			return content, nil
		}
		got, err := scope.Generated(target, read)
		if err == nil {
			t.Fatalf("Generated over an unparseable commentless document returned %q and no error", got)
		}
		if got != scope.NotGenerated {
			t.Errorf("the failed classification reported the disjunct %q", got)
		}
		// The write domain inherits the failure rather than admitting the file.
		if _, err := scope.Writable(scope.Line, target, read); err == nil {
			t.Error("Writable admitted a commentless document the generated-artifact rule could not parse")
		}
	}
	// A document that parses but carries a non-string metadata field is a
	// document the rule did read, so it stays an ordinary carrier.
	typed := func(string) ([]byte, error) {
		return []byte(`{"description": {"text": "generated by a producer"}}`), nil
	}
	if got, err := scope.Generated(target, typed); err != nil || got != scope.NotGenerated {
		t.Fatalf("Generated over a non-string metadata field = %q, %v; want %q and no error", got, err, scope.NotGenerated)
	}
}

// TestKeyWriteDomainFailsWhenTheRekeyChannelSelectsNoRegister pins the
// zero-inspection guard on the second write channel. A domain that
// selected nothing used to let a renaming pass report success having
// rekeyed no register, which makes the citation ratchet fire on a file
// that changed no citation.
func TestKeyWriteDomainFailsWhenTheRekeyChannelSelectsNoRegister(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "carrier.go"), []byte("package carrier\n"), 0o644); err != nil {
		t.Fatalf("seed the tree: %v", err)
	}
	list, read := scope.DirLister(root), scope.DirReader(root)
	_, err := scope.KeyWriteDomain(context.Background(), list, scope.Identifier, read)
	if err == nil {
		t.Fatal("KeyWriteDomain over a tree carrying no path-keyed register returned no error")
	}
	for _, reg := range scope.PathKeyedRegisters() {
		if !strings.Contains(err.Error(), reg) {
			t.Errorf("the error does not name %s: %v", reg, err)
		}
	}
	if !strings.Contains(err.Error(), string(scope.Identifier)) {
		t.Errorf("the error does not name the pass: %v", err)
	}
	// A renaming pass driven over that tree fails rather than reporting a
	// completed run.
	h := pass.NewHarnessOver(list, read, dirWriterFor(root))
	r := &renamingRewriter{
		suffixRewriter: suffixRewriter{p: scope.Identifier, suffix: "// rewritten\n"},
		from:           "carrier.go",
		to:             "renamed.go",
	}
	if _, err := h.Plan(context.Background(), r); err == nil {
		t.Fatal("Plan over a tree with no rekeyable register returned no error")
	}
	if _, err := scope.KeyWriteDomain(context.Background(), list, scope.Pass("unknown"), read); err == nil {
		t.Fatal("KeyWriteDomain for an unknown pass returned no error")
	}
}

// TestProducerOutputSetsAreHeldAsData pins that the third disjunct is
// decidable without running a producer, which is what lets a tier-0 gate
// apply it. Every producer names a command, the source it reads, and a
// non-empty output set.
func TestProducerOutputSetsAreHeldAsData(t *testing.T) {
	t.Parallel()
	producers := scope.Producers()
	if len(producers) == 0 {
		t.Fatal("the producer list is empty")
	}
	outputs := map[string]bool{}
	for _, p := range producers {
		if p.Command == "" || p.Source == "" || len(p.Outputs) == 0 {
			t.Errorf("producer %+v is incompletely declared", p)
		}
		for _, out := range p.Outputs {
			outputs[out] = true
		}
	}
	for _, required := range []string{
		"charts/lenny/crds/",
		"pkg/embedded/crds/",
		"pkg/proto/",
		"charts/lenny/values.schema.json",
		"schemas/ocsf-mapping.yaml",
	} {
		if !outputs[required] {
			t.Errorf("the producer output sets omit %s", required)
		}
	}
	// Producers returns a copy, so a caller cannot widen the rule.
	producers[0].Outputs[0] = "pkg/carrier/carrier.go"
	if scope.Producers()[0].Outputs[0] == "pkg/carrier/carrier.go" {
		t.Error("Producers returned the backing output set rather than a copy")
	}
}

// TestChartSchemaInTreeCarriesItsGenerationNoticeInDocumentMetadata pins
// the metadata disjunct against the committed schema rather than the
// fixture alone, so the rule cannot pass on a fixture the tree has
// drifted from.
func TestChartSchemaInTreeCarriesItsGenerationNoticeInDocumentMetadata(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	read := scope.DirReader(root)
	content, err := read("charts/lenny/values.schema.json")
	if err != nil {
		t.Fatalf("read the committed chart schema: %v", err)
	}
	stable := func(string) ([]byte, error) { return content, nil }
	if got, err := scope.Generated("charts/lenny/probe.schema.json", stable); err != nil || got != scope.DocumentMetadata {
		t.Fatalf("the committed chart schema classifies as %q, %v; want %q", got, err, scope.DocumentMetadata)
	}
}

// TestLoadRejectsAMissingOrMalformedRegister pins that a register the
// loader cannot read or parse fails rather than loading as a register
// with no entries. A run over an empty register reports zero work, which
// reads as a completed migration.
func TestLoadRejectsAMissingOrMalformedRegister(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		path string
	}{
		{"missing", filepath.Join(fixtureRegisters, "absent.yaml")},
		{"malformed", filepath.Join(fixtureRegisters, "malformed.yaml")},
		{"no entries block", filepath.Join(fixtureRegisters, "no-entries-block.yaml")},
		{"wrong kind", filepath.Join(fixtureRegisters, "wrong-kind.yaml")},
	} {
		if _, err := register.Load(tc.path); err == nil {
			t.Errorf("Load of the %s register returned no error", tc.name)
		}
	}
	doc, err := register.Load(filepath.Join(fixtureRegisters, "valid.yaml"))
	if err != nil {
		t.Fatalf("Load of the valid register: %v", err)
	}
	if got := doc.Members(); len(got) != 2 {
		t.Fatalf("the valid register carries %v, want two members", got)
	}
}

// TestRunDrivesAPassWithTheRegisterKeyedForItsRewrite pins that the
// driving register is validated by the pass that consumes it rather than
// against the residual entry schema. The two register families are held
// apart: a driving register is keyed for the rewrite it drives, while a
// residual register records a triage decision as a member, a class, a
// disposition, and a reason. Handing a pass its own register runs the
// pass, and handing it a residual register fails.
func TestRunDrivesAPassWithTheRegisterKeyedForItsRewrite(t *testing.T) {
	t.Parallel()
	stub := &suffixRewriter{p: scope.Line, suffix: "// rewritten\n", registerKind: "line-citations"}
	passes := map[scope.Pass]pass.Rewriter{scope.Line: stub}
	var out bytes.Buffer
	err := runWith(context.Background(), passes, []string{
		"-root", fixtureTreeRoot(t),
		"-pass", "line",
		"-register", filepath.Join(fixtureRegisters, "pass-line-citations.yaml"),
	}, &out)
	if err != nil {
		t.Fatalf("run over the pass's own register: %v", err)
	}
	if stub.loadedRegister != filepath.Join(fixtureRegisters, "pass-line-citations.yaml") {
		t.Errorf("the pass loaded %q rather than the register the run named", stub.loadedRegister)
	}
	if !strings.Contains(out.String(), "line pass (dry run)") {
		t.Errorf("the run did not report the dry run: %q", out.String())
	}
	// The residual loader does not accept a driving register, which is
	// why the pass owns the validation of its own.
	if _, err := register.Load(filepath.Join(fixtureRegisters, "pass-line-citations.yaml")); err == nil {
		t.Error("the residual loader accepted a register keyed for a pass")
	}

	for _, tc := range []struct {
		name     string
		register string
	}{
		{"a residual register", filepath.Join(fixtureRegisters, "valid.yaml")},
		{"a missing register", filepath.Join(fixtureRegisters, "absent.yaml")},
		{"a malformed register", filepath.Join(fixtureRegisters, "pass-malformed.yaml")},
	} {
		var rejected bytes.Buffer
		err := runWith(context.Background(), passes, []string{
			"-root", fixtureTreeRoot(t),
			"-pass", "line",
			"-register", tc.register,
		}, &rejected)
		if err == nil {
			t.Errorf("run with %s returned no error", tc.name)
		}
		if rejected.Len() != 0 {
			t.Errorf("run with %s wrote %q", tc.name, rejected.String())
		}
	}
}

// TestRunRejectsAnUnknownPassName pins that the driver names the passes
// it carries rather than running nothing.
func TestRunRejectsAnUnknownPassName(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	err := run(context.Background(), []string{"-root", repoRoot(t), "-pass", "reduction"}, &out)
	if err == nil {
		t.Fatal("run with an unknown pass returned no error")
	}
	if !strings.Contains(err.Error(), "identifier") {
		t.Errorf("the error does not name the passes the driver carries: %v", err)
	}
}

// TestValidateRejectsEveryEntrySchemaDefect pins the residual entry
// schema: a member, its class, an in-class or excluded disposition, and
// a reason, with each member declared once.
func TestValidateRejectsEveryEntrySchemaDefect(t *testing.T) {
	t.Parallel()
	base := func(entries []register.Entry) *register.Document {
		e := entries
		return &register.Document{
			Kind: register.Kind, Version: register.Version,
			Class: "generated-artifacts", Entries: &e,
		}
	}
	ok := register.Entry{
		Member: "pkg/proto/adapter/v1/adapter.pb.go", Class: "generated-artifacts",
		Disposition: register.InClass, Reason: "buf generate output",
	}
	for _, tc := range []struct {
		name string
		doc  *register.Document
	}{
		{name: "no member", doc: base([]register.Entry{{Class: "generated-artifacts", Disposition: register.InClass, Reason: "r"}})},
		{name: "class mismatch", doc: base([]register.Entry{{Member: "m", Class: "anchors", Disposition: register.InClass, Reason: "r"}})},
		{name: "unknown disposition", doc: base([]register.Entry{{Member: "m", Class: "generated-artifacts", Disposition: "maybe", Reason: "r"}})},
		{name: "no reason", doc: base([]register.Entry{{Member: "m", Class: "generated-artifacts", Disposition: register.Excluded}})},
		{name: "duplicate member", doc: base([]register.Entry{ok, ok})},
	} {
		if err := tc.doc.Validate(); err == nil {
			t.Errorf("Validate accepted a register with %s", tc.name)
		}
	}
	if err := base([]register.Entry{ok}).Validate(); err != nil {
		t.Errorf("Validate rejected a well-formed register: %v", err)
	}
	noClass := base([]register.Entry{})
	noClass.Class = ""
	if err := noClass.Validate(); err == nil {
		t.Error("Validate accepted a register that declares no class")
	}
}

// TestRewriteDownwardRemovesAnInClassEntryWhoseMemberLeftTheClass pins
// the downward-only rule. An in-class entry is removed in the same run in
// which its member stops matching the class predicate, an excluded entry
// is permanent, and no run adds an entry, so a member the predicate
// matches that the register does not carry stays a residual.
func TestRewriteDownwardRemovesAnInClassEntryWhoseMemberLeftTheClass(t *testing.T) {
	t.Parallel()
	entries := []register.Entry{
		{Member: "pkg/a.go", Class: "line-citations", Disposition: register.InClass, Reason: "carries the retired form"},
		{Member: "pkg/b.go", Class: "line-citations", Disposition: register.InClass, Reason: "carries the retired form"},
		{Member: "pkg/c.go", Class: "line-citations", Disposition: register.Excluded, Reason: "the token is a version string"},
	}
	doc := &register.Document{
		Kind: register.Kind, Version: register.Version,
		Class: "line-citations", Entries: &entries,
	}
	// pkg/a.go has been rewritten, so the class predicate no longer
	// matches it.
	stillMatching := map[string]bool{"pkg/b.go": true}
	removed := doc.RewriteDownward(func(e register.Entry) bool { return stillMatching[e.Member] })
	if len(removed) != 1 || removed[0] != "pkg/a.go" {
		t.Fatalf("RewriteDownward removed %v, want [pkg/a.go]", removed)
	}
	if doc.Carries("pkg/a.go") {
		t.Error("the register still carries the member that left the class")
	}
	if !doc.Carries("pkg/c.go") {
		t.Error("the rewrite removed a permanent exclusion")
	}
	// A member the predicate matches that no entry carries is a residual
	// rather than an addition.
	if got := doc.Residual([]string{"pkg/b.go", "pkg/d.go"}, nil); len(got) != 1 || got[0] != "pkg/d.go" {
		t.Fatalf("Residual returned %v, want [pkg/d.go]", got)
	}
	// An enumerated member is not a residual.
	if got := doc.Residual([]string{"pkg/d.go"}, []string{"pkg/d.go"}); len(got) != 0 {
		t.Fatalf("Residual reported the enumerated member %v", got)
	}
}

// TestSaveRoundTripsAResidualRegister pins that the downward rewrite is
// persistable: the saved document reloads with the entries the rewrite
// kept.
func TestSaveRoundTripsAResidualRegister(t *testing.T) {
	t.Parallel()
	entries := []register.Entry{
		{Member: "pkg/proto/", Class: "generated-artifacts", Disposition: register.InClass, Reason: "buf generate output"},
	}
	doc := &register.Document{
		Kind: register.Kind, Version: register.Version,
		Class: "generated-artifacts", Entries: &entries,
	}
	path := filepath.Join(t.TempDir(), "nested", "residual-generated-artifacts.yaml")
	if err := doc.SaveTo(path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	reloaded, err := register.Load(path)
	if err != nil {
		t.Fatalf("reload the saved register: %v", err)
	}
	if got := reloaded.Members(); len(got) != 1 || got[0] != "pkg/proto/" {
		t.Fatalf("the reloaded register carries %v", got)
	}
	// A generated-artifact member never leaves its class, so the rewrite
	// keeps its in-class entry run after run.
	if removed := reloaded.RewriteDownward(func(register.Entry) bool { return true }); len(removed) != 0 {
		t.Fatalf("the rewrite removed %v from the generated-artifact class", removed)
	}
	if err := doc.Save(); err == nil {
		t.Error("Save on a document with no path returned no error")
	}
}

// TestPlanAndApplyProduceTheSameDiff pins that the dry run and the apply
// are comparable, which is what makes the dry-run output the entry
// criterion for applying a pass.
func TestPlanAndApplyProduceTheSameDiff(t *testing.T) {
	t.Parallel()
	root := copyFixtureTree(t)
	h := pass.NewHarnessOver(scope.DirLister(root), scope.DirReader(root), dirWriterFor(root))
	r := &suffixRewriter{p: scope.Line, suffix: "// rewritten\n"}

	planned, err := h.Plan(context.Background(), r)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(planned.Files) == 0 {
		t.Fatal("the plan is empty over the fixture tree")
	}
	before := treeSnapshot(t, root)
	applied, err := h.Apply(context.Background(), r)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !planned.Equal(applied) {
		t.Fatalf("the applied diff differs from the plan: %v vs %v", planned.Paths(), applied.Paths())
	}
	after := treeSnapshot(t, root)
	for _, p := range planned.Paths() {
		if before[p] == after[p] {
			t.Errorf("apply left %s unchanged", p)
		}
	}
	// A dry run writes nothing.
	rerootedPlan := copyFixtureTree(t)
	dry := pass.NewHarnessOver(scope.DirLister(rerootedPlan), scope.DirReader(rerootedPlan), dirWriterFor(rerootedPlan))
	snapshot := treeSnapshot(t, rerootedPlan)
	if _, err := dry.Plan(context.Background(), r); err != nil {
		t.Fatalf("Plan on the second tree: %v", err)
	}
	if got := treeSnapshot(t, rerootedPlan); !sameSnapshot(snapshot, got) {
		t.Error("the dry run wrote to the tree")
	}
}

// TestARenameRekeysTheRegistersOutsideTheSiteRewriteDomain pins the
// second write channel. The two citation registers are outside every
// pass's site-rewrite domain, because a gate cannot read its own
// baseline as tree content, and a run that renames a file still moves
// that file's key inside them. Without the channel the ratchet fires on
// a rename that changed no citation, and every baselined non-resolving
// citation under the old path reappears as a resolver failure.
func TestARenameRekeysTheRegistersOutsideTheSiteRewriteDomain(t *testing.T) {
	t.Parallel()
	const (
		from       = "pkg/carrier/carrier.go"
		to         = "pkg/carrier/renamed.go"
		resolution = "tests/registers/line-citation-resolution.yaml"
		counts     = "tests/registers/line-citations.yaml"
	)
	root := copyFixtureTree(t)
	list, read := scope.DirLister(root), scope.DirReader(root)
	h := pass.NewHarnessOver(list, read, dirWriterFor(root))
	r := &renamingRewriter{
		suffixRewriter: suffixRewriter{p: scope.Identifier, suffix: "// rewritten\n"},
		from:           from,
		to:             to,
	}

	// The registers are outside the site-rewrite domain and inside the
	// key-write one.
	site, err := scope.WriteDomain(context.Background(), list, scope.Identifier, read)
	if err != nil {
		t.Fatalf("WriteDomain(identifier): %v", err)
	}
	inSite := membership(site)
	keyed, err := scope.KeyWriteDomain(context.Background(), list, scope.Identifier, read)
	if err != nil {
		t.Fatalf("KeyWriteDomain(identifier): %v", err)
	}
	inKey := membership(keyed)
	for _, reg := range []string{resolution, counts} {
		if inSite[reg] {
			t.Errorf("the identifier pass site-rewrite domain admits %s", reg)
		}
		if !inKey[reg] {
			t.Errorf("the key-write domain omits %s", reg)
		}
	}

	before := treeSnapshot(t, root)
	planned, err := h.Plan(context.Background(), r)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	applied, err := h.Apply(context.Background(), r)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !planned.Equal(applied) {
		t.Fatalf("the applied diff differs from the plan: %v vs %v", planned.Paths(), applied.Paths())
	}
	after := treeSnapshot(t, root)
	for _, reg := range []string{resolution, counts} {
		want := strings.ReplaceAll(before[reg], from, to)
		if want == before[reg] {
			t.Fatalf("the %s fixture carries no key for the renamed file", reg)
		}
		if after[reg] != want {
			t.Errorf("%s after the run is not its content with the key moved:\n%s", reg, after[reg])
		}
		if !membership(planned.Paths())[reg] {
			t.Errorf("the planned diff omits %s", reg)
		}
	}
	if !strings.HasSuffix(after[from], "// rewritten\n") {
		t.Errorf("the site rewrite did not reach the ordinary carrier %s", from)
	}
}

// TestApplyAbortsBeforeTheFirstWriteAndLeavesTheTreeByteIdentical pins
// the fail-closed abort. A pass that reaches a site its register does not
// carry reports the file and the line, and every other file it would
// have written stays as it was.
func TestApplyAbortsBeforeTheFirstWriteAndLeavesTheTreeByteIdentical(t *testing.T) {
	t.Parallel()
	root := copyFixtureTree(t)
	h := pass.NewHarnessOver(scope.DirLister(root), scope.DirReader(root), dirWriterFor(root))
	r := &suffixRewriter{
		p:         scope.Line,
		suffix:    "// rewritten\n",
		abortPath: "pkg/carrier/carrier.go",
		abortLine: 4,
	}
	before := treeSnapshot(t, root)
	_, err := h.Apply(context.Background(), r)
	if err == nil {
		t.Fatal("Apply over an unresolvable site returned no error")
	}
	abort, ok := pass.AsAbort(err)
	if !ok {
		t.Fatalf("Apply returned %v, which is not an abort", err)
	}
	if abort.Path != "pkg/carrier/carrier.go" || abort.Line != 4 {
		t.Errorf("the abort names %s:%d, want pkg/carrier/carrier.go:4", abort.Path, abort.Line)
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the aborted run left the tree modified")
	}
	// The driver reports the same file and line to the operator.
	reported := reportAbort(err)
	if !strings.Contains(reported.Error(), "pkg/carrier/carrier.go:4") {
		t.Errorf("the driver message omits the site: %v", reported)
	}
}

// TestPlanRecordsARewriteThatEditedItsBufferInPlace pins that the diff
// and the rollback compare against contents no rewriter has touched. The
// harness used to hand the pre-run buffer straight to the rewriter and
// then compare the result against that same buffer, so a rewriter that
// edited in place made the two identical: the file was dropped from the
// dry run and from the applied change, and the run exited clean reporting
// fewer files than it rewrote.
func TestPlanRecordsARewriteThatEditedItsBufferInPlace(t *testing.T) {
	t.Parallel()
	root := copyFixtureTree(t)
	h := pass.NewHarnessOver(scope.DirLister(root), scope.DirReader(root), dirWriterFor(root))
	r := &inPlaceRewriter{p: scope.Line}

	before := treeSnapshot(t, root)
	planned, err := h.Plan(context.Background(), r)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(planned.Files) == 0 {
		t.Fatal("the plan dropped every file an in-place rewriter changed")
	}
	for _, f := range planned.Files {
		if bytes.Equal(f.Before, f.After) {
			t.Errorf("the diff for %s records no change", f.Path)
		}
		if got := before[f.Path]; got != string(f.Before) {
			t.Errorf("the diff for %s carries post-rewrite contents as its pre-run contents", f.Path)
		}
	}
	applied, err := h.Apply(context.Background(), r)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !planned.Equal(applied) {
		t.Fatalf("the applied diff differs from the plan: %v vs %v", planned.Paths(), applied.Paths())
	}
	after := treeSnapshot(t, root)
	for _, p := range planned.Paths() {
		if before[p] == after[p] {
			t.Errorf("apply left %s unchanged", p)
		}
	}
}

// TestApplyRestoresATreeARewriterEditedInPlace pins the other half of the
// same guarantee: the rollback contents are captured before any rewriter
// sees the buffer, so a mid-write failure still leaves the tree
// byte-identical when the pass rewrote in place.
func TestApplyRestoresATreeARewriterEditedInPlace(t *testing.T) {
	t.Parallel()
	root := copyFixtureTree(t)
	h := pass.NewHarnessOver(scope.DirLister(root), scope.DirReader(root), nil)
	planned, err := h.Plan(context.Background(), &inPlaceRewriter{p: scope.Line})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(planned.Files) < 2 {
		t.Fatalf("the fixture tree plans %d file(s), which cannot exercise a mid-write failure", len(planned.Files))
	}
	h.Write = failingWriter(root, planned.Paths()[len(planned.Files)-1], nil)

	before := treeSnapshot(t, root)
	if _, err := h.Apply(context.Background(), &inPlaceRewriter{p: scope.Line}); err == nil {
		t.Fatal("Apply over a failing writer returned no error")
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the failed apply left the tree partially rewritten")
	}
}

// TestApplyRestoresTheFilesItWroteWhenALaterWriteFails pins that the
// byte-identical guarantee holds for a failure the plan could not
// foresee. The write loop used to return on the first failing write and
// leave every file before it in its rewritten state, so a half-applied
// tree was reported as an aborted run that changed nothing.
func TestApplyRestoresTheFilesItWroteWhenALaterWriteFails(t *testing.T) {
	t.Parallel()
	root := copyFixtureTree(t)
	h := pass.NewHarnessOver(scope.DirLister(root), scope.DirReader(root), nil)
	planned, err := h.Plan(context.Background(), &suffixRewriter{p: scope.Line, suffix: "// rewritten\n"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(planned.Files) < 2 {
		t.Fatalf("the fixture tree plans %d file(s), which cannot exercise a mid-write failure", len(planned.Files))
	}
	failOn := planned.Paths()[len(planned.Files)-1]
	h.Write = failingWriter(root, failOn, nil)

	before := treeSnapshot(t, root)
	if _, err := h.Apply(context.Background(), &suffixRewriter{p: scope.Line, suffix: "// rewritten\n"}); err == nil {
		t.Fatal("Apply over a failing writer returned no error")
	} else if errors.Is(err, pass.ErrTreeNotRestored) {
		t.Fatalf("Apply reported an unrestored tree after a restore that could succeed: %v", err)
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the failed apply left the tree partially rewritten")
	}
}

// TestApplyRestoresTheFileWhoseOwnWriteFailedPartWayThrough pins the
// case a writer that truncates before it fails produces. The production
// writer opens with O_TRUNC, so a write that fails after truncation, on a
// full disk or a lost mount, leaves its own target torn. The rollback
// used to restore only the files written before the failing one, so the
// single file the run damaged was the one file it skipped and the tree
// was neither the pre-run tree nor the applied one.
func TestApplyRestoresTheFileWhoseOwnWriteFailedPartWayThrough(t *testing.T) {
	t.Parallel()
	root := copyFixtureTree(t)
	r := &suffixRewriter{p: scope.Line, suffix: "// rewritten\n"}
	h := pass.NewHarnessOver(scope.DirLister(root), scope.DirReader(root), nil)
	planned, err := h.Plan(context.Background(), r)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(planned.Files) < 2 {
		t.Fatalf("the fixture tree plans %d file(s), which cannot exercise a mid-write failure", len(planned.Files))
	}
	failOn := planned.Paths()[len(planned.Files)-1]
	h.Write = tearingWriter(root, failOn)

	before := treeSnapshot(t, root)
	if _, err := h.Apply(context.Background(), r); err == nil {
		t.Fatal("Apply over a tearing writer returned no error")
	} else if errors.Is(err, pass.ErrTreeNotRestored) {
		t.Fatalf("Apply reported an unrestored tree after a restore that could succeed: %v", err)
	}
	got := treeSnapshot(t, root)
	if got[failOn] != before[failOn] {
		t.Errorf("the torn target %s was left at %q; want its pre-run contents", failOn, got[failOn])
	}
	if !sameSnapshot(before, got) {
		t.Error("the failed apply left the tree partially rewritten")
	}
}

// TestApplyReportsAnUnrestoredTreeWhenTheRollbackAlsoFails pins the
// distinct outcome an operator needs when the tree is neither the pre-run
// tree nor the applied one.
func TestApplyReportsAnUnrestoredTreeWhenTheRollbackAlsoFails(t *testing.T) {
	t.Parallel()
	root := copyFixtureTree(t)
	h := pass.NewHarnessOver(scope.DirLister(root), scope.DirReader(root), nil)
	r := &suffixRewriter{p: scope.Line, suffix: "// rewritten\n"}
	planned, err := h.Plan(context.Background(), r)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(planned.Files) < 2 {
		t.Fatalf("the fixture tree plans %d file(s), which cannot exercise a mid-write failure", len(planned.Files))
	}
	paths := planned.Paths()
	// The first file writes, the last fails, and putting the first one
	// back fails too.
	h.Write = failingWriter(root, paths[len(paths)-1], map[string]bool{paths[0]: true})

	_, err = h.Apply(context.Background(), r)
	if err == nil {
		t.Fatal("Apply over a failing writer and a failing restore returned no error")
	}
	if !errors.Is(err, pass.ErrTreeNotRestored) {
		t.Fatalf("Apply reported %v, which does not carry the unrestored-tree error", err)
	}
	reported := reportAbort(err)
	if !strings.Contains(reported.Error(), "not clean") {
		t.Errorf("the driver message does not tell the operator the tree is not clean: %v", reported)
	}
}

// TestPlanFailsWithoutAListerOrReader pins that an incompletely wired
// harness fails rather than reporting an empty diff.
func TestPlanFailsWithoutAListerOrReader(t *testing.T) {
	t.Parallel()
	h := pass.NewHarnessOver(nil, nil, nil)
	if _, err := h.Plan(context.Background(), &suffixRewriter{p: scope.Line}); err == nil {
		t.Fatal("Plan on an unwired harness returned no error")
	}
	root := copyFixtureTree(t)
	noWriter := pass.NewHarnessOver(scope.DirLister(root), scope.DirReader(root), nil)
	if _, err := noWriter.Apply(context.Background(), &suffixRewriter{p: scope.Line, suffix: "x\n"}); err == nil {
		t.Fatal("Apply on a harness with no writer returned no error")
	}
}

// TestDiffEqualDistinguishesContentAndOrder pins the comparison the
// dry-run entry criterion rests on.
func TestDiffEqualDistinguishesContentAndOrder(t *testing.T) {
	t.Parallel()
	a := pass.Diff{Files: []pass.FileDiff{{Path: "x", Before: []byte("1"), After: []byte("2")}}}
	if !a.Equal(a) {
		t.Error("a diff does not equal itself")
	}
	if a.Equal(pass.Diff{}) {
		t.Error("a diff equals the empty diff")
	}
	b := pass.Diff{Files: []pass.FileDiff{{Path: "x", Before: []byte("1"), After: []byte("3")}}}
	if a.Equal(b) {
		t.Error("diffs with different content compare equal")
	}
}

// TestRunPrintsTheWriteDomainOverTheTrackedTree pins the domain against
// the committed tree rather than the fixture alone: the walk comes from
// the git index, the ordinary carriers are in it, and the excluded groups
// and the generated artifacts are out of it.
func TestRunPrintsTheWriteDomainOverTheTrackedTree(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	if err := run(context.Background(), []string{"-root", repoRoot(t), "-pass", "name", "-domain"}, &out); err != nil {
		t.Fatalf("run -domain: %v", err)
	}
	in := membership(strings.Split(strings.TrimSpace(out.String()), "\n"))
	if !in["scripts/specshift/main.go"] {
		t.Error("the name pass write domain omits an ordinary tracked carrier")
	}
	for _, excluded := range []string{
		"BUILD-GAPS.md",
		"TEST-GAPS.md",
		"BUILD-PLAN.md",
		"PROPOSAL-QUEUE.md",
		"charts/lenny/values.schema.json",
		"schemas/ocsf-mapping.yaml",
		"scripts/specshift/testdata/tree/pkg/carrier/carrier.go",
	} {
		if in[excluded] {
			t.Errorf("the name pass write domain admits %s", excluded)
		}
	}
	// Every CRD under the chart is producer output, so none of them is
	// writable even though none carries a generation marker.
	for _, p := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(p, "charts/lenny/crds/") {
			t.Errorf("the name pass write domain admits the generated CRD %s", p)
		}
	}
}

// TestRunReportsAPassThatIsNotBuiltRatherThanAnEmptyDiff pins that the
// driver names an unbuilt pass instead of reporting a completed run.
func TestRunReportsAPassThatIsNotBuiltRatherThanAnEmptyDiff(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	err := run(context.Background(), []string{
		"-root", repoRoot(t),
		"-pass", "anchor",
		"-register", filepath.Join(fixtureRegisters, "pass-line-citations.yaml"),
	}, &out)
	if err == nil {
		t.Fatal("run with a pass that is not built returned no error")
	}
	if !strings.Contains(err.Error(), "anchor") {
		t.Errorf("the error does not name the requested pass: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("run with a pass that is not built wrote %q", out.String())
	}
}

// TestRunRequiresARegister pins that a pass never runs without the
// register that drives it.
func TestRunRequiresARegister(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	if err := run(context.Background(), []string{"-root", repoRoot(t), "-pass", "line"}, &out); err == nil {
		t.Fatal("run with no register returned no error")
	}
}

// TestReportAbortPassesOtherErrorsThrough pins that only a fail-closed
// abort is reported as one.
func TestReportAbortPassesOtherErrorsThrough(t *testing.T) {
	t.Parallel()
	plain := errors.New("git ls-files failed")
	if got := reportAbort(plain); !errors.Is(got, plain) {
		t.Errorf("reportAbort rewrote a non-abort error: %v", got)
	}
	if _, ok := pass.AsAbort(plain); ok {
		t.Error("AsAbort reported a plain error as an abort")
	}
}

// TestNewHarnessWritesUnderItsRootAndPreservesMode pins the writer the
// driver uses on a real checkout.
func TestNewHarnessWritesUnderItsRootAndPreservesMode(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	existing := filepath.Join(root, "existing.txt")
	if err := os.WriteFile(existing, []byte("before\n"), 0o640); err != nil {
		t.Fatalf("seed the tree: %v", err)
	}
	h := pass.NewHarness(root)
	if err := h.Write("existing.txt", []byte("after\n")); err != nil {
		t.Fatalf("write an existing file: %v", err)
	}
	info, err := os.Stat(existing)
	if err != nil {
		t.Fatalf("stat the written file: %v", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("the write changed the mode to %v", info.Mode().Perm())
	}
	if err := h.Write(filepath.Join("absent-dir", "f.txt"), []byte("x")); err == nil {
		t.Error("writing under a missing directory returned no error")
	}
}

// suffixRewriter appends a marker to every file in the write domain, and
// optionally aborts on one named file at one named line. It stands in
// for a built pass: it owns its driving register's keying and validates
// that register itself.
type suffixRewriter struct {
	p         scope.Pass
	suffix    string
	abortPath string
	abortLine int

	// registerKind is the kind the pass's own driving register declares.
	registerKind string
	// loadedRegister records the register the run handed the pass.
	loadedRegister string
}

func (s *suffixRewriter) Pass() scope.Pass { return s.p }

// LoadRegister validates the register keyed for this pass's rewrite. A
// residual register, which carries a member, a class, a disposition, and
// a reason, is a different family and does not drive a pass.
func (s *suffixRewriter) LoadRegister(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read the %s pass register %s: %w", s.p, path, err)
	}
	var doc struct {
		Kind  string         `yaml:"kind"`
		Files map[string]int `yaml:"files"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse the %s pass register %s: %w", s.p, path, err)
	}
	if doc.Kind != s.registerKind {
		return fmt.Errorf("the %s pass register %s declares kind %q, want %q", s.p, path, doc.Kind, s.registerKind)
	}
	if len(doc.Files) == 0 {
		return fmt.Errorf("the %s pass register %s carries no sites", s.p, path)
	}
	s.loadedRegister = path
	return nil
}

func (s *suffixRewriter) Rewrite(_ context.Context, path string, content []byte) ([]byte, error) {
	if path == s.abortPath {
		return nil, fmt.Errorf("resolve %s: %w", path,
			&pass.Abort{Path: path, Line: s.abortLine, Reason: "no register entry for this site"})
	}
	return append(append([]byte(nil), content...), []byte(s.suffix)...), nil
}

// inPlaceRewriter edits the buffer it is handed and returns that same
// slice, which the Rewriter contract forbids. It stands in for a pass
// that violates the contract, so the harness's guarantees are pinned
// against the buffer aliasing rather than against a rewriter's good
// behaviour.
type inPlaceRewriter struct {
	p scope.Pass
}

func (i *inPlaceRewriter) Pass() scope.Pass          { return i.p }
func (i *inPlaceRewriter) LoadRegister(string) error { return nil }

func (i *inPlaceRewriter) Rewrite(_ context.Context, _ string, content []byte) ([]byte, error) {
	for n := range content {
		if content[n] == 'e' {
			content[n] = 'E'
		}
	}
	return content, nil
}

// renamingRewriter stands in for a pass that renames a file. It carries
// the site rewrite of a suffixRewriter and, through the second channel,
// moves the renamed file's key in every path-keyed register.
type renamingRewriter struct {
	suffixRewriter
	from string
	to   string
}

func (r *renamingRewriter) RewriteKeys(_ context.Context, _ string, content []byte) ([]byte, error) {
	return bytes.ReplaceAll(content, []byte(r.from), []byte(r.to)), nil
}

// refuseAllReads returns a reader that fails the test if it is called. It
// pins that a disjunct decides without reading the file.
func refuseAllReads(t *testing.T) scope.FileReader {
	t.Helper()
	return func(path string) ([]byte, error) {
		t.Errorf("the rule read %s when it should have decided from the producer output sets", path)
		return nil, errors.New("unexpected read")
	}
}

// membership indexes a domain for lookup.
func membership(domain []string) map[string]bool {
	in := make(map[string]bool, len(domain))
	for _, p := range domain {
		in[p] = true
	}
	return in
}

// repoRoot returns the git top level of the working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := scope.RepoRoot(context.Background(), ".")
	if err != nil {
		t.Fatalf("resolve the repo root: %v", err)
	}
	return root
}

// fixtureTreeRoot returns the absolute path of the fixture tree, which a
// run drives as its own tracked tree.
func fixtureTreeRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(fixtureTree)
	if err != nil {
		t.Fatalf("resolve the fixture tree: %v", err)
	}
	return root
}

// copyFixtureTree copies the fixture tree into a temporary directory so
// a case that writes does not touch the committed fixtures.
func copyFixtureTree(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(fixtureTree, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(fixtureTree, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, 0o644)
	})
	if err != nil {
		t.Fatalf("copy the fixture tree: %v", err)
	}
	return dst
}

// dirWriterFor returns a writer rooted at dir.
func dirWriterFor(dir string) func(string, []byte) error {
	return func(target string, content []byte) error {
		return os.WriteFile(filepath.Join(dir, filepath.FromSlash(target)), content, 0o644)
	}
}

// failingWriter returns a writer rooted at dir that refuses the applying
// write of failOn while letting its restore through, and refuses the
// second write of any path in restoreFail, which is the restore of a file
// a failed apply had already written. The harness writes sequentially, so
// the record needs no lock.
func failingWriter(dir, failOn string, restoreFail map[string]bool) func(string, []byte) error {
	base := dirWriterFor(dir)
	written := map[string]bool{}
	refused := false
	return func(target string, content []byte) error {
		if target == failOn && !refused {
			refused = true
			return fmt.Errorf("write %s: refused by the test writer", target)
		}
		if written[target] && restoreFail[target] {
			return fmt.Errorf("restore %s: refused by the test writer", target)
		}
		written[target] = true
		return base(target, content)
	}
}

// tearingWriter returns a writer rooted at dir that truncates failOn and
// then reports a failure, which is what os.WriteFile leaves behind when
// the write fails after the O_TRUNC open. Every other path is written
// normally, including the restore of the torn target.
func tearingWriter(dir, failOn string) func(string, []byte) error {
	base := dirWriterFor(dir)
	torn := false
	return func(target string, content []byte) error {
		if target == failOn && !torn {
			torn = true
			if err := base(target, nil); err != nil {
				return fmt.Errorf("truncate %s: %w", target, err)
			}
			return fmt.Errorf("write %s: the device is full", target)
		}
		return base(target, content)
	}
}

// treeSnapshot records the byte content of every file under root.
func treeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		snap[filepath.ToSlash(rel)] = string(content)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return snap
}

// sameSnapshot reports whether two snapshots are byte-identical.
func sameSnapshot(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// fixtureCitations holds the fixtures that carry the retired citation form.
// They are held as files rather than as Go string literals because a fixture
// carrying the form is input to a gate rather than a pointer into the
// specification, and testdata/ is outside the read domain of the resolver, the
// ratchet, and the residual scan, so no gate reports its own input.
const fixtureCitations = "testdata/citations"

// TestTheToolingSourceHoldsTheRetiredFormOnlyInFixtures pins the rule the
// fixture layout exists for: every verbatim copy of the retired citation form
// this package's own cases need sits in a testdata/ file, so no copy of it
// lands in a Go source the resolver, the ratchet, and the residual scan read.
// A copy in a Go source names no section anyone will retire, and its route out
// of the population is the deletion of the case rather than a retirement, so a
// per-file count or a resolution-baseline entry seeded for it would never fall
// and the zero end state would be unreachable.
func TestTheToolingSourceHoldsTheRetiredFormOnlyInFixtures(t *testing.T) {
	t.Parallel()
	list, read := scope.DirLister("."), scope.DirReader(".")
	paths, err := list(context.Background())
	if err != nil {
		t.Fatalf("list the tooling source: %v", err)
	}
	for _, path := range paths {
		if filepath.Ext(path) != ".go" || !scope.Readable(path) {
			continue
		}
		data, err := read(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, c := range citation.FindIn(path, string(data)) {
			t.Errorf("%s line %d carries the retired citation form %q; hold it in a testdata/ fixture instead",
				c.Path, c.Line, c.Text)
		}
	}
}

// citationFixture reads one such fixture.
func citationFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureCitations, name))
	if err != nil {
		t.Fatalf("read citation fixture %s: %v", name, err)
	}
	return string(data)
}

// wantCitations reads the citation text a fixture's case expects, held in the
// fixture's companion `.want` file, one citation per line. The expected text is
// a verbatim copy of the retired form, so it is held beside the fixture for the
// reason fixtureCitations states rather than in a Go string literal here.
func wantCitations(t *testing.T, fixture string) []string {
	t.Helper()
	name := strings.TrimSuffix(fixture, filepath.Ext(fixture)) + ".want"
	content := strings.TrimSuffix(citationFixture(t, name), "\n")
	if content == "" {
		t.Fatalf("expectation fixture %s is empty", name)
	}
	return strings.Split(content, "\n")
}

// wantCitation reads the single citation text a fixture's case expects.
func wantCitation(t *testing.T, fixture string) string {
	t.Helper()
	texts := wantCitations(t, fixture)
	if len(texts) != 1 {
		t.Fatalf("expectation fixture for %s carries %d citations, want 1", fixture, len(texts))
	}
	return texts[0]
}

// oneCitation reads a fixture and returns the single citation it carries.
func oneCitation(t *testing.T, name string) citation.Citation {
	t.Helper()
	found := citation.Find(citationFixture(t, name))
	if len(found) != 1 {
		t.Fatalf("Find over %s returned %d citations, want 1: %v", name, len(found), found)
	}
	return found[0]
}

// members renders a citation's members as start-end pairs, so a case states
// the members it expects without restating the grammar.
func members(c citation.Citation) []string {
	out := make([]string, 0, len(c.Members))
	for _, m := range c.Members {
		out = append(out, fmt.Sprintf("%d-%d", m.Start, m.End))
	}
	return out
}

// sameStrings reports whether two string slices are equal in content and
// order.
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestFindReadsEverySpellingOfTheRetiredCitationForm pins the grammar against
// one fixture per spelling: the section-level and dotted references, the path
// reference with the prefix present and absent, the keyword and the colon
// standing in for it, the three range separators, the four member separators,
// the continuation member that repeats the keyword, the qualifier in each of
// its written forms, and the trailing gloss.
func TestFindReadsEverySpellingOfTheRetiredCitationForm(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		fixture   string
		ref       string
		qualifier string
		members   []string
	}{
		{"section-level.txt", "§10", "", []string{"437-437"}},
		{"hyphen-range.txt", "§4.4", "", []string{"263-291"}},
		{"endash-range.txt", "§4.4", "", []string{"263-291"}},
		{"emdash-range.txt", "§4.4", "", []string{"263-291"}},
		{"comma-members.txt", "§4.8", "", []string{"1057-1058", "1077-1077"}},
		{"repeated-keyword.txt", "§10.6", "", []string{"601-601", "629-629"}},
		{"slash-members.txt", "§10.7", "", []string{"694-694", "743-743"}},
		{"and-members.txt", "§12.5", "", []string{"315-315", "321-321"}},
		{"plus-members.txt", "§10", "", []string{"437-437", "443-443"}},
		{"qualifier-item.txt", "§11.7", "item 3", []string{"364-364"}},
		{"qualifier-identifier.txt", "§16.4", "NET-063", []string{"88-88"}},
		{"qualifier-table.txt", "§17.2", "table", []string{"12-12"}},
		{"qualifier-parenthetical.txt", "§10.3", "NET-063", []string{"327-327"}},
		{"qualifier-words.txt", "§10.3", "revocation deny list", []string{"352-352"}},
		{"qualifier-parenthetical-preamble.txt", "§24", "preamble", []string{"17-17"}},
		{"gloss-trailing.txt", "§7.3", "", []string{"408-408"}},
		{"gloss-bare.txt", "§9.2", "", []string{"240-240"}},
		{"path-form.txt", "spec/04_system-components.md", "", []string{"1145-1145"}},
		{"path-form-bare-prefix.txt", "spec/15_external-api-surface.md", "", []string{"1315-1315"}},
		{"colon-section.txt", "§17.6", "", []string{"404-404"}},
		{"colon-path.txt", "spec/15_external-api-surface.md", "", []string{"1315-1315"}},
	} {
		c := oneCitation(t, tc.fixture)
		if want := wantCitation(t, tc.fixture); c.Text != want {
			t.Errorf("%s: citation text is %q, want %q", tc.fixture, c.Text, want)
		}
		if c.Ref() != tc.ref {
			t.Errorf("%s: reference is %q, want %q", tc.fixture, c.Ref(), tc.ref)
		}
		if c.Qualifier != tc.qualifier {
			t.Errorf("%s: qualifier is %q, want %q", tc.fixture, c.Qualifier, tc.qualifier)
		}
		if got := members(c); !sameStrings(got, tc.members) {
			t.Errorf("%s: members are %v, want %v", tc.fixture, got, tc.members)
		}
	}
}

// TestFindRequiresTheColonMemberToStandAgainstTheReference pins the colon
// spelling to the form: the first member is written directly against the colon.
// A colon alternative that admitted authored whitespace reads an English or a
// YAML colon as the citation keyword, so a status code, a timeout, a depth
// limit, or a register key becomes a phantom citation. Each phantom enters the
// per-file count of a file that carries no citation, and its only route to zero
// is the pass rewriting prose.
func TestFindRequiresTheColonMemberToStandAgainstTheReference(t *testing.T) {
	t.Parallel()
	if found := citation.Find(citationFixture(t, "colon-prose.txt")); len(found) != 0 {
		t.Errorf("Find over prose colons returned %v, want no citation", found)
	}
	spaced := oneCitation(t, "colon-qualifier-then-prose.txt")
	if want := wantCitation(t, "colon-qualifier-then-prose.txt"); spaced.Text != want {
		t.Errorf("citation text is %q, want %q", spaced.Text, want)
	}
	if got := members(spaced); !sameStrings(got, []string{"396-396"}) {
		t.Errorf("members are %v, want the cited line rather than the prose behind the colon", got)
	}
}

// TestFindRefusesAColonBehindAQualifier pins the other half of the same rule:
// the colon stands directly against the reference, so no qualifier may sit
// between the two. A qualifier admitted there absorbs a prose word together
// with the digits of an unrelated number, which turns `§17.1 flat` wrapped onto
// `maxUnavailable:1` and `§25.11 daily 03:30 UTC` into citations that name a
// section, carry a member, and resolve nowhere. Each would be seeded into the
// resolution baseline, counted by the per-file ratchet, and rewritten by the
// line pass, so the only route to a zero count is the pass rewriting prose.
func TestFindRefusesAColonBehindAQualifier(t *testing.T) {
	t.Parallel()
	if found := citation.Find(citationFixture(t, "colon-after-qualifier.txt")); len(found) != 0 {
		t.Errorf("Find over a qualified prose colon returned %v, want no citation", found)
	}
}

// TestFindJoinsAWrappedColonCitation pins the one spaced colon spelling the
// form admits, which is a colon citation wrapped across two comment lines. The
// space stands for the wrap the join consumed rather than for whitespace an
// author wrote, so the citation is read whole while an authored space after a
// colon still ends the match.
func TestFindJoinsAWrappedColonCitation(t *testing.T) {
	t.Parallel()
	c := oneCitation(t, "colon-wrapped.txt")
	if want := wantCitation(t, "colon-wrapped.txt"); c.Text != want {
		t.Errorf("joined citation text is %q, want %q", c.Text, want)
	}
	if c.File != "spec/25_example-operability.md" {
		t.Errorf("reference is %q, want the path form", c.Ref())
	}
	if got := members(c); !sameStrings(got, []string{"9-11"}) {
		t.Errorf("members are %v, want 9-11", got)
	}
	if !strings.Contains(c.Raw, "\n") {
		t.Errorf("raw citation %q does not span the wrap", c.Raw)
	}
}

// TestFindConsumesMembersWrittenAfterTheHeadClosingParenthesis pins that a
// member list resuming behind the parenthesis the head opened is consumed with
// the rest of the citation. Stopping at that parenthesis drops the remaining
// members, which the resolver then does not read and the ratchet does not
// count, so the rewritten carrier reads as an anchor followed by orphan
// integers while its file reaches a zero count with a stale pointer surviving.
func TestFindConsumesMembersWrittenAfterTheHeadClosingParenthesis(t *testing.T) {
	t.Parallel()
	c := oneCitation(t, "members-after-close-paren.txt")
	if got := members(c); !sameStrings(got, []string{"3333-3358", "3404-3406"}) {
		t.Errorf("members are %v, want both the parenthesized and the trailing member", got)
	}
	if want := wantCitation(t, "members-after-close-paren.txt"); c.Text != want {
		t.Errorf("citation text is %q, want it to run to the last member (%q)", c.Text, want)
	}
	if c.Qualifier != "Image Resolution" {
		t.Errorf("qualifier is %q, want %q", c.Qualifier, "Image Resolution")
	}
}

// balancedParens reports whether every parenthesis a citation's text opens is
// closed inside that text. A citation the pass converts to a single anchor is
// replaced whole, so an unbalanced text leaves the matching delimiter and the
// prose between the two behind in the carrier.
func balancedParens(text string) bool {
	depth := 0
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

// TestFindClosesTheParenthesisItsHeadOpened pins that a citation whose head
// opened a parenthesis of the carrier's own runs to the parenthesis that closes
// it, with the text between the last member and that parenthesis read as the
// member's gloss. A citation ending inside the parenthetical carries an
// unpaired opening parenthesis, so converting it to a single anchor deletes
// that delimiter and the words behind the last member while leaving the
// matching one in the carrier, which is the residue the whole-citation rule
// forbids. A parenthesis written inside the parenthetical is closed with it, so
// the citation ends at the parenthesis its own head opened. Where the
// parenthetical crosses a continuation join the same
// conversion also deletes the newline and the following line's comment marker,
// and in the `#` dialect that removal stops the following text being a comment.
func TestFindClosesTheParenthesisItsHeadOpened(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		fixture string
		members []string
		wrapped bool
	}{
		{"paren-prose-after-member.txt", []string{"2768-2780"}, false},
		{"paren-wrapped-slash.txt", []string{"4317-4317"}, true},
		{"paren-wrapped-hash.txt", []string{"4317-4317"}, true},
		{"members-after-close-paren-glossed.txt", []string{"3333-3358", "3404-3406"}, false},
		{"paren-nested.txt", []string{"408-408"}, false},
	} {
		c := oneCitation(t, tc.fixture)
		if want := wantCitation(t, tc.fixture); c.Text != want {
			t.Errorf("%s: citation text is %q, want %q", tc.fixture, c.Text, want)
		}
		if !balancedParens(c.Text) {
			t.Errorf("%s: citation text %q leaves a parenthesis unclosed", tc.fixture, c.Text)
		}
		if got := members(c); !sameStrings(got, tc.members) {
			t.Errorf("%s: members are %v, want %v", tc.fixture, got, tc.members)
		}
		if tc.wrapped && !strings.Contains(c.Raw, "\n") {
			t.Errorf("%s: raw citation %q does not span the wrap", tc.fixture, c.Raw)
		}
	}
}

// TestFindRefusesAParenthesisItCannotClose pins the other half of that rule.
// The parenthesis a head opened is closed inside the citation or the occurrence
// is refused, so no returned citation carries an unpaired delimiter. The
// parenthesis is out of reach when it sits behind a newline the join did not
// consume, and when it sits behind the head of the next citation, where
// consuming up to it would swallow the citation written in between: a swallowed
// citation is never returned, so the resolver does not resolve it and the
// ratchet does not count it. The scan resumes inside the refused occurrence, so
// the citation written behind the refused head is still returned.
func TestFindRefusesAParenthesisItCannotClose(t *testing.T) {
	t.Parallel()
	for _, fixture := range []string{
		"paren-unreachable-close.txt",
		"paren-close-behind-next-citation.txt",
	} {
		c := oneCitation(t, fixture)
		if want := wantCitation(t, fixture); c.Text != want {
			t.Errorf("%s: citation text is %q, want %q", fixture, c.Text, want)
		}
		if !balancedParens(c.Text) {
			t.Errorf("%s: citation text %q leaves a parenthesis unclosed", fixture, c.Text)
		}
	}
}

// TestFindReturnsNoUnbalancedCitationOverEveryFixture sweeps the whole fixture
// set for the same invariant, so a spelling added later cannot reintroduce a
// citation the pass would convert to an anchor while leaving a delimiter and
// the prose behind it in the carrier.
func TestFindReturnsNoUnbalancedCitationOverEveryFixture(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(fixtureCitations)
	if err != nil {
		t.Fatalf("read the citation fixture directory: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".txt" {
			continue
		}
		for _, c := range citation.Find(citationFixture(t, entry.Name())) {
			if !balancedParens(c.Text) {
				t.Errorf("%s: citation text %q leaves a parenthesis unclosed", entry.Name(), c.Text)
			}
		}
	}
}

// TestFindConsumesEveryMemberRatherThanTheHead pins the failure a matcher that
// stopped at the first separator produces. The remaining members stay in place,
// where the resolver does not read them and the ratchet does not count them, so
// the file reaches a zero count while a stale pointer survives.
func TestFindConsumesEveryMemberRatherThanTheHead(t *testing.T) {
	t.Parallel()
	for _, fixture := range []string{
		"comma-members.txt",
		"repeated-keyword.txt",
		"slash-members.txt",
		"and-members.txt",
		"plus-members.txt",
	} {
		c := oneCitation(t, fixture)
		if len(c.Members) != 2 {
			t.Errorf("%s: citation carries %d members, want 2: %v", fixture, len(c.Members), members(c))
			continue
		}
		last := c.Members[len(c.Members)-1]
		if !strings.Contains(c.Text, last.Text) {
			t.Errorf("%s: citation text %q stops before its last member %q", fixture, c.Text, last.Text)
		}
	}
}

// TestFindConsumesATrailingGlossWithItsMember pins that the gloss does not
// terminate the match, and that a bare gloss is bounded at a word or two so a
// citation followed by ordinary prose ends near its member.
func TestFindConsumesATrailingGlossWithItsMember(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		fixture string
		gloss   string
	}{
		{"gloss-trailing.txt", "step (e)"},
		{"gloss-bare.txt", "messagingScope"},
		{"gloss-then-prose.txt", "for the"},
	} {
		c := oneCitation(t, tc.fixture)
		if len(c.Members) != 1 {
			t.Fatalf("%s: citation carries %d members, want 1", tc.fixture, len(c.Members))
		}
		if c.Members[0].Gloss != tc.gloss {
			t.Errorf("%s: gloss is %q, want %q", tc.fixture, c.Members[0].Gloss, tc.gloss)
		}
	}
}

// TestFindBoundsTheGlossSoItDoesNotSwallowTheNextCitation pins the bound on
// the trailing gloss. A quote written directly against a member's last digit is
// the closing quote of the carrier's own string literal or an English
// apostrophe, and an unbounded quoted gloss opened on either of them runs to
// the next quote anywhere in the file. Because the scan resumes at the end of
// the consumed span, every citation inside the run goes unreturned: the
// resolver does not resolve it, the ratchet does not count it, and the pass
// rewrites the whole span, code included, to one anchor. The file then reaches
// a zero count while a stale pointer survives, which is what the whole-citation
// rule exists to prevent.
func TestFindBoundsTheGlossSoItDoesNotSwallowTheNextCitation(t *testing.T) {
	t.Parallel()
	for _, fixture := range []string{
		"gloss-unpaired-quote.txt",
		"gloss-apostrophe.txt",
		"adjacent-path-citations.txt",
	} {
		found := citation.Find(citationFixture(t, fixture))
		got := make([]string, 0, len(found))
		for _, c := range found {
			got = append(got, c.Text)
			if strings.Contains(c.Text, "\n") {
				t.Errorf("%s: citation text %q spans a line the join did not consume", fixture, c.Text)
			}
		}
		if want := wantCitations(t, fixture); !sameStrings(got, want) {
			t.Errorf("%s: Find returned %q, want %q", fixture, got, want)
		}
	}
}

// TestFindLeavesASeparatorWithNoMemberOutsideTheCitation pins that a member
// separator spelled as a word and followed by prose or by another citation
// rather than by a member stays out of the citation, whether it stands directly
// against the member or behind a gloss. Absorbing it puts a dangling
// conjunction in the text the register is keyed by and that the pass replaces,
// so the rewritten sentence loses its conjunction.
func TestFindLeavesASeparatorWithNoMemberOutsideTheCitation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		fixture string
		glosses []string
	}{
		{"separator-then-prose.txt", []string{""}},
		{"gloss-then-separator.txt", []string{"interfaces", "baseline"}},
	} {
		found := citation.Find(citationFixture(t, tc.fixture))
		texts := make([]string, 0, len(found))
		glosses := make([]string, 0, len(found))
		for _, c := range found {
			texts = append(texts, c.Text)
			glosses = append(glosses, c.Members[0].Gloss)
		}
		if want := wantCitations(t, tc.fixture); !sameStrings(texts, want) {
			t.Errorf("%s: Find returned %q, want %q", tc.fixture, texts, want)
		}
		if !sameStrings(glosses, tc.glosses) {
			t.Errorf("%s: glosses are %q, want %q", tc.fixture, glosses, tc.glosses)
		}
	}
}

// TestFindConsumesAGlossRunWithoutDroppingTheMembersAfterIt pins that a member
// carrying more than one gloss segment does not terminate the citation. The
// spellings a bare word followed by a parenthesized phrase, a bare word
// followed by a quoted fragment, and a run of bare words each precede a
// continuation member in the tree, and a matcher that gave up on the second
// segment would drop every member after the gloss: the resolver would not read
// them, the ratchet would not count them, and the rewritten carrier would read
// as an anchor followed by orphan integers.
func TestFindConsumesAGlossRunWithoutDroppingTheMembersAfterIt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		fixture string
		gloss   string
		members []string
	}{
		{
			"gloss-run-parenthesized-plus-member.txt",
			"step (e) (replay workspace checkpoint)",
			[]string{"408-408", "409-409"},
		},
		{
			"gloss-run-quoted-plus-member.txt",
			`step (e) "Replay latest workspace checkpoint"`,
			[]string{"408-408", "409-409"},
		},
		{
			"gloss-run-words-plus-member.txt",
			"EventBus retranscribe worker",
			[]string{"685-689", "683-683", "699-699"},
		},
	} {
		c := oneCitation(t, tc.fixture)
		if got := members(c); !sameStrings(got, tc.members) {
			t.Errorf("%s: members are %v, want %v", tc.fixture, got, tc.members)
		}
		if c.Members[0].Gloss != tc.gloss {
			t.Errorf("%s: gloss is %q, want %q", tc.fixture, c.Members[0].Gloss, tc.gloss)
		}
		last := c.Members[len(c.Members)-1]
		if !strings.Contains(c.Text, last.Text) {
			t.Errorf("%s: citation text %q stops before its last member %q", tc.fixture, c.Text, last.Text)
		}
	}
}

// TestFindWritesARangeEndpointDirectlyAgainstItsSeparator pins that a range's
// two endpoints stand against the separator with no whitespace between them.
// A separator that admitted whitespace reads an ordinary prose aside written
// after a single-line member as the range's second endpoint, which produces a
// descending range the resolver reports as a straddle with nothing to correct,
// and a citation text that runs past the member into the sentence, so
// converting the citation to an anchor deletes the sentence's own number.
func TestFindWritesARangeEndpointDirectlyAgainstItsSeparator(t *testing.T) {
	t.Parallel()
	c := oneCitation(t, "emdash-aside.txt")
	if want := wantCitation(t, "emdash-aside.txt"); c.Text != want {
		t.Errorf("citation text is %q, want it to end at the member (%q)", c.Text, want)
	}
	if got := members(c); !sameStrings(got, []string{"277-277"}) {
		t.Errorf("members are %v, want the single cited line", got)
	}
	// The unspaced range spellings still read as ranges.
	for _, fixture := range []string{"hyphen-range.txt", "endash-range.txt", "emdash-range.txt"} {
		if got := members(oneCitation(t, fixture)); !sameStrings(got, []string{"263-291"}) {
			t.Errorf("%s: members are %v, want 263-291", fixture, got)
		}
	}
}

// TestFindConsumesTheMembersWrittenAfterALongGloss pins that the length of a
// gloss never ends the member list. A gloss bounded by a byte count rejects the
// gloss that exceeds it, and a rejected gloss stops the scan, so every member
// written after it is left unconsumed: the resolver does not read them, the
// ratchet does not count them, and the rewritten carrier reads as an anchor
// followed by orphan integers.
func TestFindConsumesTheMembersWrittenAfterALongGloss(t *testing.T) {
	t.Parallel()
	c := oneCitation(t, "gloss-long-then-members.txt")
	if got := members(c); !sameStrings(got, []string{"470-470", "440-440", "472-472"}) {
		t.Errorf("members are %v, want all three", got)
	}
	if len(c.Members[0].Gloss) <= 80 {
		t.Errorf("the fixture's first gloss is %d bytes, which no longer exceeds the bound the case pins", len(c.Members[0].Gloss))
	}
	if !strings.HasSuffix(c.Text, "line 472 (fail closed)") {
		t.Errorf("citation text is %q, want it to run to the last member and its gloss", c.Text)
	}
}

// TestFindEndsABareGlossAtASentenceTerminatingPeriod pins that a bare-word
// gloss stops at the end of the sentence its member sits in. A word run that
// absorbed the terminating period takes the first word of the next sentence as
// its second word, and the citation text is what a register is keyed by and
// what the pass replaces with an anchor, so the rewrite would delete that word.
// A dotted identifier is one word, because its dots are followed by word bytes.
func TestFindEndsABareGlossAtASentenceTerminatingPeriod(t *testing.T) {
	t.Parallel()
	sentence := oneCitation(t, "gloss-sentence-boundary.txt")
	if want := wantCitation(t, "gloss-sentence-boundary.txt"); sentence.Text != want {
		t.Errorf("citation text is %q, want it to stop at the word ending the sentence (%q)", sentence.Text, want)
	}
	if sentence.Members[0].Gloss != "reassembly" {
		t.Errorf("gloss is %q, want %q", sentence.Members[0].Gloss, "reassembly")
	}
	dotted := oneCitation(t, "gloss-dotted-word.txt")
	if dotted.Members[0].Gloss != "lenny.dev/schema-version annotation" {
		t.Errorf("gloss is %q, want the dotted identifier read as one word", dotted.Members[0].Gloss)
	}
}

// TestFindJoinsAContinuationInEveryWrapPositionAndCarrierDialect pins the join
// that lets a citation wrapped across two comment lines be read as one
// citation. The three wrap positions are a wrap between the reference and the
// keyword, a wrap between the keyword and its first member, and a wrap inside a
// member list, and the marker the join consumes is one of the four dialects.
// Without the join a line-oriented scan sees a reference with no line-number
// token and a line-number token with no reference, so the resolver does not
// resolve the citation and the ratchet does not count it.
func TestFindJoinsAContinuationInEveryWrapPositionAndCarrierDialect(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		fixture string
		members []string
		line    int
	}{
		{"wrap-reference-keyword-slash.txt", []string{"1060-1060"}, 1},
		{"wrap-keyword-member-slash.txt", []string{"1672-1721"}, 1},
		{"wrap-member-list-slash.txt", []string{"806-823", "897-917"}, 1},
		{"wrap-reference-keyword-hash.txt", []string{"1350-1350"}, 1},
		{"wrap-member-list-dash.txt", []string{"315-315", "321-325"}, 1},
		{"wrap-keyword-member-block.txt", []string{"51-51"}, 2},
	} {
		c := oneCitation(t, tc.fixture)
		if want := wantCitation(t, tc.fixture); c.Text != want {
			t.Errorf("%s: joined citation text is %q, want %q", tc.fixture, c.Text, want)
		}
		if got := members(c); !sameStrings(got, tc.members) {
			t.Errorf("%s: members are %v, want %v", tc.fixture, got, tc.members)
		}
		if c.Line != tc.line {
			t.Errorf("%s: citation starts on line %d, want %d", tc.fixture, c.Line, tc.line)
		}
		if !strings.Contains(c.Raw, "\n") {
			t.Errorf("%s: raw citation %q does not span the wrap", tc.fixture, c.Raw)
		}
	}
}

// TestFindReportsTheCarrierLineOfEachCitation pins the line a gate names when
// it reports a citation, which is the source line the citation starts on rather
// than a line of the joined text.
func TestFindReportsTheCarrierLineOfEachCitation(t *testing.T) {
	t.Parallel()
	content := citationFixture(t, "section-level.txt") + citationFixture(t, "colon-section.txt")
	found := citation.Find(content)
	if len(found) != 2 {
		t.Fatalf("Find over two stacked fixtures returned %d citations, want 2", len(found))
	}
	if found[0].Line != 1 || found[1].Line != 2 {
		t.Errorf("citations start on lines %d and %d, want 1 and 2", found[0].Line, found[1].Line)
	}
	located := citation.FindIn("pkg/carrier/carrier.go", content)
	if len(located) != 2 || located[0].Path != "pkg/carrier/carrier.go" {
		t.Fatalf("FindIn returned %v, want both citations under the carrier path", located)
	}
	if !strings.Contains(located[1].String(), "pkg/carrier/carrier.go line 2") {
		t.Errorf("located citation renders as %q, want it to name its carrier and line", located[1])
	}
}

// TestFindEndsACitationAtTheCommentLineThatCarriesIt pins the gloss to the
// source line its member sits on. The continuation join covers three wrap
// positions, which are a wrap between the reference and the keyword, a wrap
// between the keyword and its first member, and a wrap inside a member list. A
// gloss that read across a consumed continuation would take the opening word or
// two of the following comment line, so the citation text a register is keyed
// by would carry prose the citation does not own and the anchor the pass writes
// in place of the whole citation would delete the newline, the following line's
// comment marker, and its opening words, merging two comment lines and, in the
// # dialect, leaving the following text outside any comment.
func TestFindEndsACitationAtTheCommentLineThatCarriesIt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		fixture string
		gloss   string
	}{
		{"citation-then-comment-slash.txt", ""},
		{"citation-then-comment-hash.txt", ""},
		{"gloss-then-comment-slash.txt", "messagingScope"},
		{"gloss-then-comment-hash.txt", "tombstone"},
	} {
		c := oneCitation(t, tc.fixture)
		if want := wantCitation(t, tc.fixture); c.Text != want {
			t.Errorf("%s: citation text is %q, want it to end on its own line (%q)", tc.fixture, c.Text, want)
		}
		if strings.Contains(c.Raw, "\n") {
			t.Errorf("%s: raw citation %q spans the following comment line", tc.fixture, c.Raw)
		}
		if got := c.Members[len(c.Members)-1].Gloss; got != tc.gloss {
			t.Errorf("%s: gloss is %q, want %q", tc.fixture, got, tc.gloss)
		}
	}
}

// citationResolver builds the resolver over the fixture specification tree.
// That tree carries a section with two subsections and one nested
// sub-subsection, a second file with two subsections, a third file with two
// top-level headings so a section-level range ends before the next heading of
// its own level, and a fourth file that states its numbered title at level one
// and carries a fenced code block with a numbered heading-like line in it, so
// neither the title nor the fenced line declares a section. The ranges the
// cases below state are the tree's, and the fixture files themselves are the
// statement of record.
func citationResolver(t *testing.T) *citation.Resolver {
	t.Helper()
	r, err := citation.NewResolver(context.Background(),
		scope.DirLister(fixtureCitations), scope.DirReader(fixtureCitations))
	if err != nil {
		t.Fatalf("NewResolver over the fixture specification tree: %v", err)
	}
	return r
}

// TestSectionRangeCoversASectionAndItsSubsections pins the range computation
// over the ## through ###### headings: a section ends at the line before the
// next heading at its own level or above, so a parent covers its children and a
// section-level citation resolves against the whole of the section it names.
func TestSectionRangeCoversASectionAndItsSubsections(t *testing.T) {
	t.Parallel()
	r := citationResolver(t)
	for _, tc := range []struct {
		number     string
		start, end int
	}{
		{"4", 1, 19},
		{"4.1", 5, 12},
		{"4.2", 13, 19},
		{"4.2.1", 17, 19},
		{"7.1", 3, 6},
		{"7.2", 7, 9},
	} {
		s, ok := r.Section(tc.number)
		if !ok {
			t.Errorf("§%s is absent from the resolver index", tc.number)
			continue
		}
		if s.Start != tc.start || s.End != tc.end {
			t.Errorf("§%s spans lines %d-%d, want %d-%d", tc.number, s.Start, s.End, tc.start, tc.end)
		}
	}
	if len(r.Sections()) == 0 {
		t.Error("the resolver indexed no section")
	}
}

// TestSectionIndexExcludesALevelOneHeading pins the heading rule the range
// computation is stated over, which is the ## through ###### headings. A
// specification file that states its number in a level-one title declares no
// section under that number, so a section-level citation naming it is reported
// as a heading the specification does not declare rather than resolving against
// the whole file. Indexing level one instead widens the resolver's answer over
// a population the seeded baseline is measured on. The file's own ## headings
// are indexed as usual, and the walk skips fenced code, because a heading-like
// line inside an example is a comment and indexing it declares sections the
// specification does not have, including one that collides with a genuine
// section number.
func TestSectionIndexExcludesALevelOneHeading(t *testing.T) {
	t.Parallel()
	r := citationResolver(t)
	if s, ok := r.Section("30"); ok {
		t.Errorf("a level-one heading declared %v", s)
	}
	child, ok := r.Section("30.1")
	if !ok || child.File != "spec/30_level-one-title.md" || child.Start != 9 || child.End != 11 {
		t.Errorf("§30.1 is %v (present=%v), want lines 9-11 of the level-one-titled file", child, ok)
	}
	if s, ok := r.Section("1"); ok {
		t.Errorf("a numbered heading inside a fenced code block declared %v", s)
	}
	top := r.Resolve(oneCitation(t, "resolve/level-one-heading.txt"))
	if len(top) != 1 || top[0].Kind != citation.UnknownSection {
		t.Errorf("a citation naming the level-one heading reported %v, want one unknown-section failure", top)
	}
	if f := r.Resolve(oneCitation(t, "resolve/level-one-subsection.txt")); len(f) != 0 {
		t.Errorf("a citation naming the file's own subsection reported %v, want it to resolve", f)
	}
}

// TestResolveReportsEveryFailureClassDistinctly pins the resolver's answer per
// citation class. A member outside its section, a range whose endpoints
// disagree about which section they name, a section number no heading declares,
// and a path that does not resolve under spec/ are reported as their own kinds,
// because their remedies differ: collapsing them would report a mistyped file
// name as a stale line number.
func TestResolveReportsEveryFailureClassDistinctly(t *testing.T) {
	t.Parallel()
	r := citationResolver(t)
	for _, tc := range []struct {
		fixture string
		want    []citation.FailureKind
	}{
		{"resolve/inside.txt", nil},
		{"resolve/section-level.txt", nil},
		{"resolve/path-inside.txt", nil},
		{"resolve/outside.txt", []citation.FailureKind{citation.OutsideSection}},
		{"resolve/multi-member-one-outside.txt", []citation.FailureKind{citation.OutsideSection}},
		{"resolve/straddling.txt", []citation.FailureKind{citation.StraddlingRange}},
		{"resolve/path-straddling.txt", []citation.FailureKind{citation.StraddlingRange}},
		{"resolve/path-preamble-into-subsection.txt", nil},
		{"resolve/unknown-section.txt", []citation.FailureKind{citation.UnknownSection}},
		{"resolve/path-unknown-file.txt", []citation.FailureKind{citation.UnknownFile}},
		{"resolve/path-outside-any-section.txt", []citation.FailureKind{citation.OutsideSection}},
	} {
		c := oneCitation(t, tc.fixture)
		got := r.Resolve(c)
		kinds := make([]string, 0, len(got))
		for _, f := range got {
			kinds = append(kinds, string(f.Kind))
		}
		want := make([]string, 0, len(tc.want))
		for _, k := range tc.want {
			want = append(want, string(k))
		}
		if !sameStrings(kinds, want) {
			t.Errorf("%s: Resolve reported %v, want %v (%v)", tc.fixture, kinds, want, got)
		}
		if r.Resolves(c) != (len(tc.want) == 0) {
			t.Errorf("%s: Resolves reported %v", tc.fixture, r.Resolves(c))
		}
	}
	member := r.Resolve(oneCitation(t, "resolve/outside.txt"))
	if len(member) != 1 || !strings.Contains(member[0].String(), `member "15"`) {
		t.Errorf("a member failure renders as %v, want it to name the member that did not resolve", member)
	}
	file := r.Resolve(oneCitation(t, "resolve/path-unknown-file.txt"))
	if len(file) != 1 || !strings.Contains(file[0].String(), "spec/99_missing.md") {
		t.Errorf("a file failure renders as %v, want it to name the path that did not resolve", file)
	}
}

// TestResolveResolvesThePathFormAgainstTheContainingSection pins that a
// path-form citation resolves against the section of the named file that
// contains the cited line rather than against a section it names, so the same
// line resolves under the path form and under the number of the section that
// contains it.
func TestResolveResolvesThePathFormAgainstTheContainingSection(t *testing.T) {
	t.Parallel()
	r := citationResolver(t)
	path := oneCitation(t, "resolve/path-inside.txt")
	if path.File != "spec/04_example.md" || path.Section != "" {
		t.Fatalf("the path form parsed as %+v, want a file reference", path)
	}
	if f := r.Resolve(path); len(f) != 0 {
		t.Errorf("the path form did not resolve: %v", f)
	}
	number := oneCitation(t, "resolve/inside.txt")
	if f := r.Resolve(number); len(f) != 0 {
		t.Errorf("the section-number form for the same line did not resolve: %v", f)
	}
}

// TestResolvePathRangeStraddlesOnlyWhenNoSectionContainsBothEndpoints pins the
// straddling rule for the path form. A range that runs from a section's
// preamble into one of its own subsections lies inside the section that
// contains it, so it resolves, and the same range written in the section-number
// spelling resolves too. A range whose endpoints fall in two sibling sections
// has no containing section, so it is reported as a straddle, which is the
// citation the pass fails rather than guessing an anchor for. Reporting the
// first as a straddle would send a citation with nothing to correct into the
// hand-correction list and hold its file above a zero count.
func TestResolvePathRangeStraddlesOnlyWhenNoSectionContainsBothEndpoints(t *testing.T) {
	t.Parallel()
	r := citationResolver(t)
	inner := oneCitation(t, "resolve/path-preamble-into-subsection.txt")
	if f := r.Resolve(inner); len(f) != 0 {
		t.Errorf("a range from a section's preamble into its own subsection reported %v", f)
	}
	parent, ok := r.Section("25.1")
	if !ok || !parent.Contains(inner.Members[0].Start) || !parent.Contains(inner.Members[0].End) {
		t.Fatalf("the fixture range %v does not sit inside §25.1 (%v)", inner.Members, parent)
	}
	crossing := oneCitation(t, "resolve/path-straddling.txt")
	f := r.Resolve(crossing)
	if len(f) != 1 || f[0].Kind != citation.StraddlingRange {
		t.Errorf("a range crossing into a sibling section reported %v, want one straddling range", f)
	}
}

// TestNewResolverFailsRatherThanIndexingNothing pins that the resolver refuses
// a tree with no specification file and a file that declares no numbered
// section. An empty index reads to every gate as a tree in which no citation
// resolves, which is the report a genuinely broken tree produces.
func TestNewResolverFailsRatherThanIndexingNothing(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		paths []string
		files map[string]string
	}{
		{
			name:  "a tree with no specification file",
			paths: []string{"pkg/carrier/carrier.go"},
			files: map[string]string{"pkg/carrier/carrier.go": "package carrier\n"},
		},
		{
			name:  "a specification file that declares no numbered section",
			paths: []string{"spec/04_example.md"},
			files: map[string]string{"spec/04_example.md": "## Overview\n\nProse.\n"},
		},
	} {
		list := func(context.Context) ([]string, error) { return tc.paths, nil }
		read := func(target string) ([]byte, error) {
			body, ok := tc.files[target]
			if !ok {
				return nil, fmt.Errorf("no such file %s", target)
			}
			return []byte(body), nil
		}
		if _, err := citation.NewResolver(context.Background(), list, read); err == nil {
			t.Errorf("NewResolver over %s returned no error", tc.name)
		}
	}
}

// TestNewResolverRejectsASectionDeclaredTwice pins that a section number
// declared in two files fails the build rather than resolving against whichever
// file was walked last, which would make a citation's answer depend on the walk
// order.
func TestNewResolverRejectsASectionDeclaredTwice(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"spec/04_example.md": "## 4. Example\n\nProse.\n",
		"spec/05_other.md":   "## 4. Example Again\n\nProse.\n",
	}
	list := func(context.Context) ([]string, error) {
		return []string{"spec/04_example.md", "spec/05_other.md"}, nil
	}
	read := func(target string) ([]byte, error) { return []byte(files[target]), nil }
	_, err := citation.NewResolver(context.Background(), list, read)
	if err == nil {
		t.Fatal("NewResolver over a duplicated section number returned no error")
	}
	if !strings.Contains(err.Error(), "declared in both") {
		t.Errorf("error is %q, want it to name the two files", err)
	}
}

// TestNewResolverFailsWithoutAListerOrReader pins that the resolver refuses to
// build with a missing dependency rather than indexing an empty tree.
func TestNewResolverFailsWithoutAListerOrReader(t *testing.T) {
	t.Parallel()
	if _, err := citation.NewResolver(context.Background(), nil, scope.DirReader(fixtureCitations)); err == nil {
		t.Error("NewResolver without a lister returned no error")
	}
	if _, err := citation.NewResolver(context.Background(), scope.DirLister(fixtureCitations), nil); err == nil {
		t.Error("NewResolver without a reader returned no error")
	}
}
