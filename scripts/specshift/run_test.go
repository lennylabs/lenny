// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/lenny/scripts/specshift/anchor"
	"github.com/lennylabs/lenny/scripts/specshift/citation"
	"github.com/lennylabs/lenny/scripts/specshift/gate"
	"github.com/lennylabs/lenny/scripts/specshift/identifier"
	"github.com/lennylabs/lenny/scripts/specshift/line"
	"github.com/lennylabs/lenny/scripts/specshift/name"
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
// It holds ordinary carriers plus one member of every excluded group.
//
// The ordinary carriers span the specification directory and the code
// tree, so a run confined to either one partitions the write domain into
// a non-empty selected half and a non-empty excluded half. The tree also
// carries a sibling directory beside the specification directory
// (spec-notes/) and a suffixed file beside a named carrier
// (pkg/carrier/carrier.go.bak), both of which the write domain admits, so
// a confinement matched by substring rather than by path segment selects
// one of them and the confinement cases detect it. Pruning or renaming
// any of these four carriers removes the property the confinement cases
// stand on.
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

// TestClassReadDomainExcludesEveryResidualRegisterAndTheClassOwnBaseline
// pins the third exclusion of a class's read domain. A residual entry
// holds a copy of its member's text, so a scan that read a residual
// register, or the pass or baseline register its own gate consumes,
// would report that copy under the register's own path as a further
// member, and the entry seeded for the copy would add another copy. The
// seeding does not converge.
//
// The residual registers are excluded from every class rather than from
// their own class alone. Two classes whose predicates overlap otherwise
// pin each other's registers permanently, so neither can empty. The pass
// or baseline register stays per class, because one gate consumes it.
func TestClassReadDomainExcludesEveryResidualRegisterAndTheClassOwnBaseline(t *testing.T) {
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
	for _, other := range scope.Classes() {
		reg := other.ResidualRegister()
		if in[reg] {
			t.Errorf("%s class read domain admits the %s residual register %s, whose copies of that class's members it would report as its own",
				scope.ClassReservedPhrase, other, reg)
		}
		ok, err := scope.ReadableForClass(scope.ClassReservedPhrase, reg)
		if err != nil {
			t.Fatalf("ReadableForClass(%s, %s): %v", scope.ClassReservedPhrase, reg, err)
		}
		if ok {
			t.Errorf("ReadableForClass(%s, %q) = true, want false", scope.ClassReservedPhrase, reg)
		}
	}
	// Another class's pass or baseline register stays ordinary tree
	// content, so the exclusion above is bounded rather than covering the
	// whole register directory.
	const sibling = "tests/registers/anchor-senses.yaml"
	ok, err := scope.ReadableForClass(scope.ClassReservedPhrase, sibling)
	if err != nil {
		t.Fatalf("ReadableForClass(%s, %s): %v", scope.ClassReservedPhrase, sibling, err)
	}
	if !ok {
		t.Errorf("ReadableForClass(%s, %q) = false; only a residual register and the class's own baseline are excluded",
			scope.ClassReservedPhrase, sibling)
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

// TestNoGateSourceOutsideItsFixtureTreeCarriesAReservedPhrase holds the
// three trees that carry the passes and the gates to the rule their own
// fixtures rest on. A case that has to present a reserved noun phrase
// verbatim holds it in a file under testdata, which the tree beside each
// of them exists for, rather than in a Go string literal in the test
// source.
//
// A phrase written in the source instead is a live prose site: the file
// is inside the name pass's write domain and inside the reserved-phrase
// class's residual domain, so the pass aborts on it, and its route out
// of the population is the deletion of the case rather than a
// retirement. Under testdata it is neither, because testdata sits
// outside every read and write domain.
//
// The retired citation form is held to the same rule by the
// line-citation ratchet, which counts it per file and never raises a
// count, so it is not measured again here.
//
// spec: §28.1 (N3, the naming law: no bare reserved noun phrase stands
// in prose, and the prohibition's domain excludes the fixture trees)
func TestNoGateSourceOutsideItsFixtureTreeCarriesAReservedPhrase(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	all, err := scope.GitLister(root)(context.Background())
	if err != nil {
		t.Fatalf("list the tracked tree: %v", err)
	}
	trees := []string{"scripts/specshift/", "cmd/lenny-test/", "tests/tier0_static/"}
	read := scope.DirReader(root)
	measured := 0
	for _, target := range all {
		if !underAny(target, trees) || !scope.Readable(target) {
			continue
		}
		measured++
		content, err := read(target)
		if err != nil {
			t.Fatalf("read %s: %v", target, err)
		}
		for _, line := range name.FindReservedPhrases(string(content)) {
			t.Errorf("%s:%d carries a reserved noun phrase in its own source; hold the fixture text under testdata",
				target, line)
		}
	}
	if measured == 0 {
		t.Fatal("no tracked source under the pass and gate trees was measured")
	}
}

// underAny reports whether a path sits under one of the prefixes.
func underAny(target string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(target, prefix) {
			return true
		}
	}
	return false
}

// TestEveryTestInfrastructureRegisterIsRekeyedByARename pins the rekey
// domain over the committed tree. Every register under tests/registers
// is keyed by a tracked path or carries one at the head of a member, and
// each is rewritten downward and never widened: a key the renaming run
// leaves under the old path drops the entry, the member reappears under
// the new path with no entry, and the gate that owns the register fails
// with no permitted route back to green. An enumeration of registers
// held in the rekey domain would omit each register added after it was
// written, so the domain is derived and this case measures it against
// the tree.
//
// spec: §28.1 (N4, the naming law: the run that moves a carrier moves
// every key written for it)
func TestEveryTestInfrastructureRegisterIsRekeyedByARename(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	all, err := scope.GitLister(root)(context.Background())
	if err != nil {
		t.Fatalf("list the tracked tree: %v", err)
	}
	registers := 0
	for _, target := range all {
		if !strings.HasPrefix(target, "tests/registers/") || !strings.HasSuffix(target, ".yaml") {
			continue
		}
		registers++
		if !scope.KeyWritable(target) {
			t.Errorf("%s is a test-infrastructure register that a rename does not rekey", target)
		}
	}
	if registers == 0 {
		t.Fatal("the tracked tree carries no test-infrastructure register, so the case measured nothing")
	}
	for _, m := range []string{"tests/change-graph.json", "tests/spec-map.json"} {
		if !scope.KeyWritable(m) {
			t.Errorf("%s is keyed by tracked path and a rename does not rekey it", m)
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
	for _, reg := range scope.PathKeyedRegisterRule() {
		if !strings.Contains(err.Error(), reg) {
			t.Errorf("the error does not state %s: %v", reg, err)
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

// underDir reports whether a tracked path sits under a directory, matched
// on the segment terminator so a sibling whose name merely starts with
// the directory name is outside it.
func underDir(target, dir string) bool {
	return strings.HasPrefix(target, strings.TrimSuffix(dir, "/")+"/")
}

// TestTheKeyRewriteChannelSelectsARegisterByItsOwnTrackedPath pins the
// axis a confinement is matched against on the second write channel. A
// register joins the key-write domain by where the register file itself
// sits, and the paths its entries are keyed by are never consulted. A
// fixture that discriminates on its entries' paths therefore pins nothing:
// it can neither make the channel run nor make it skip, and a confinement
// case wired to it would record a rule the tool does not implement.
//
// spec: §28.1 (N4, the naming law: the run that moves a carrier moves
// every key written for it, so the register carrying those keys is the
// carrier the channel selects)
func TestTheKeyRewriteChannelSelectsARegisterByItsOwnTrackedPath(t *testing.T) {
	t.Parallel()
	// Membership is the register's own path against the rule.
	for target, want := range map[string]bool{
		"tests/registers/line-citations.yaml": true,
		"tests/change-graph.json":             true,
		"tests/spec-map.json":                 true,
		// A register-shaped document whose entries are keyed by paths
		// under the specification directory, sitting outside the register
		// directory. Its entries do not put it in the channel.
		"scripts/specshift/testdata/registers/valid.yaml": false,
		"spec/04_system-components.md":                    false,
	} {
		if got := scope.KeyWritable(target); got != want {
			t.Errorf("KeyWritable(%q) = %v, want %v", target, got, want)
		}
	}

	list, read := treeDomain(t)
	domain, err := scope.KeyWriteDomain(context.Background(), list, scope.Identifier, read)
	if err != nil {
		t.Fatalf("KeyWriteDomain(identifier) over the fixture tree: %v", err)
	}
	want := []string{
		"tests/registers/line-citation-resolution.yaml",
		"tests/registers/line-citations.yaml",
	}
	if !reflect.DeepEqual(domain, want) {
		t.Fatalf("the key-write domain over the fixture tree is %v, want %v", domain, want)
	}
	// The domain sits wholly outside the specification directory, which is
	// what makes both halves of a confined run reachable over this tree: a
	// run confined to that directory selects no register and skips the
	// channel, and a run that excludes it retains the whole domain, so the
	// channel's own emptiness guard still decides that run.
	var confinedToSpec, outsideSpec []string
	for _, target := range domain {
		if underDir(target, "spec/") {
			confinedToSpec = append(confinedToSpec, target)
			continue
		}
		outsideSpec = append(outsideSpec, target)
	}
	if len(confinedToSpec) != 0 {
		t.Errorf("a run confined to the specification directory still selects %v, so the skipped channel is unreachable", confinedToSpec)
	}
	if !reflect.DeepEqual(outsideSpec, want) {
		t.Errorf("a run that excludes the specification directory selects %v, want the whole domain %v", outsideSpec, want)
	}
}

// TestTheStandaloneRegisterFixturesAreTheOnesALoaderCaseNames pins what
// the flat register fixture directory holds. Every file in it is passed
// straight to a loader as the -register argument, and none of it is part
// of any walked tree, so a fixture placed here can never enter a file
// domain. A fixture meant to exercise the key-rewrite channel belongs in
// a walked tree at a path the path-keyed register rule matches.
//
// spec: §28.1 (N3, the naming law: the fixture trees sit outside every
// read and write domain, and this directory is not walked at all)
func TestTheStandaloneRegisterFixturesAreTheOnesALoaderCaseNames(t *testing.T) {
	t.Parallel()
	named := map[string]bool{
		"malformed.yaml":           true,
		"no-entries-block.yaml":    true,
		"pass-line-citations.yaml": true,
		"pass-malformed.yaml":      true,
		"valid.yaml":               true,
		"wrong-kind.yaml":          true,
	}
	entries, err := os.ReadDir(fixtureRegisters)
	if err != nil {
		t.Fatalf("read %s: %v", fixtureRegisters, err)
	}
	for _, e := range entries {
		if !named[e.Name()] {
			t.Errorf("%s holds %s, which no loader case names and which no tree walk lists", fixtureRegisters, e.Name())
		}
		delete(named, e.Name())
	}
	for missing := range named {
		t.Errorf("%s no longer holds %s, which a loader case names", fixtureRegisters, missing)
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
	passes := func(string) map[scope.Pass]pass.Rewriter {
		return map[scope.Pass]pass.Rewriter{scope.Line: stub}
	}
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
	// move drives the third channel as well, so a case can exercise the
	// file move a rename performs alongside the key rewrite it forces.
	move bool
}

// MoveTo moves the one file the rewriter renames.
func (r *renamingRewriter) MoveTo(_ context.Context, path string) (string, error) {
	if !r.move || path != r.from {
		return "", nil
	}
	return r.to, nil
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
	copyTreeInto(t, fixtureTree, dst)
	return dst
}

// copyTreeInto copies one fixture directory into dst, so a case
// assembles the tree it runs over from the fixture parts it needs.
func copyTreeInto(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
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
		content, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, 0o644)
	})
	if err != nil {
		t.Fatalf("copy the fixture tree %s: %v", src, err)
	}
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
		{"qualifier-word.txt", "§25.8", "Metrics", []string{"312-312"}},
		{"qualifier-hyphen-words.txt", "§25.3", "error-code table", []string{"601-604"}},
		{"qualifier-numeric-range.txt", "§7.2", "paths 1-7", []string{"300-310"}},
		{"qualifier-quoted.txt", "§15.2", `"Version negotiation"`, []string{"100-110"}},
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
// the citation ends at the parenthesis its own head opened.
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

// formFailureKinds are the failure kinds the citation form names, which are a
// member outside the section it names, a range whose endpoints straddle a
// section boundary, a section number no heading declares, a path that does not
// resolve under spec/, a member whose digits are not a line number, and a
// citation carrying a parenthesis its head opened and nothing closed. A
// citation is converted to a single anchor in every other case, so a kind
// outside this set is a population the retirement has no correction staged for,
// and every occurrence carrying it holds its file above a per-file count of
// zero with no route down but a hand edit.
var formFailureKinds = map[citation.FailureKind]bool{
	citation.OutsideSection:     true,
	citation.StraddlingRange:    true,
	citation.UnknownSection:     true,
	citation.UnknownFile:        true,
	citation.OutOfRangeMember:   true,
	citation.UnbalancedCitation: true,
}

// reportedKinds renders the failure kinds a resolution produced, so a case
// states the report it expects without restating the resolver's messages.
func reportedKinds(failures []citation.Failure) []string {
	out := make([]string, 0, len(failures))
	for _, f := range failures {
		out = append(out, string(f.Kind))
	}
	return out
}

// TestFindClosesAHeadParenthesisOnTheContinuationLine pins the wrap position
// the parenthesis a head opened is written in throughout the tree, which is a
// parenthetical that opens on the member's line and closes on the following
// comment line the join folded into the same text. Ending the citation at the
// wrap instead leaves the span carrying an unpaired opening parenthesis and
// withholds it from the pass, so the occurrence resolves nowhere, holds its
// file above a per-file count of zero, and has no route down but a hand edit
// that no correction covers. The two cases carry the spelling in the // and the
// # dialect, and the citation written behind the parenthetical is returned with
// it.
func TestFindClosesAHeadParenthesisOnTheContinuationLine(t *testing.T) {
	t.Parallel()
	r := citationResolver(t)
	for _, fixture := range []string{"paren-across-wrap-slash.txt", "paren-across-wrap-hash.txt"} {
		found := citation.Find(citationFixture(t, fixture))
		texts := make([]string, 0, len(found))
		for _, c := range found {
			texts = append(texts, c.Text)
		}
		want := wantCitations(t, fixture)
		if !sameStrings(texts, want) {
			t.Errorf("%s: Find returned %q, want %q", fixture, texts, want)
			continue
		}
		if !balancedParens(found[0].Text) {
			t.Errorf("%s: citation text %q leaves the parenthesis its head opened unclosed", fixture, found[0].Text)
		}
		if !strings.Contains(found[0].Raw, "\n") {
			t.Errorf("%s: raw citation %q does not span the wrap the parenthetical closes on", fixture, found[0].Raw)
		}
		for _, f := range r.Resolve(found[0]) {
			if !formFailureKinds[f.Kind] {
				t.Errorf("%s: Resolve reported %v, want only the failure kinds the form names", fixture, f)
			}
		}
	}
}

// TestFindReportsACitationWhoseHeadParenthesisNeverCloses pins the bound on
// that search and the disposition of the occurrence it leaves. The parenthesis
// a head opened is out of reach when it sits behind a newline the join did not
// consume and when it sits behind the head of the next citation, where
// consuming up to it would swallow the citation written in between. The
// occurrence ends at its last member in both cases and is still returned, so
// the resolver resolves it and the ratchet counts it, and it is marked
// unbalanced so the resolver reports it for hand correction. Converting a span
// carrying an unpaired opening parenthesis to a single anchor instead deletes
// that parenthesis and strands the carrier's closing one with the prose between
// them, which is the residue the whole-citation rule forbids. The citation
// written behind it is returned too, and carries no such report.
func TestFindReportsACitationWhoseHeadParenthesisNeverCloses(t *testing.T) {
	t.Parallel()
	r := citationResolver(t)
	for _, fixture := range []string{
		"paren-unreachable-close.txt",
		"paren-close-behind-next-citation.txt",
	} {
		found := citation.Find(citationFixture(t, fixture))
		texts := make([]string, 0, len(found))
		for _, c := range found {
			texts = append(texts, c.Text)
		}
		want := wantCitations(t, fixture)
		if !sameStrings(texts, want) {
			t.Errorf("%s: Find returned %q, want %q", fixture, texts, want)
			continue
		}
		if strings.Contains(found[0].Raw, "\n") {
			t.Errorf("%s: raw citation %q runs past the line its last member sits on", fixture, found[0].Raw)
		}
		if !found[0].Unbalanced {
			t.Errorf("%s: citation %q carries an unpaired parenthesis and is not marked unbalanced",
				fixture, found[0].Text)
		}
		if !hasKind(r.Resolve(found[0]), citation.UnbalancedCitation) {
			t.Errorf("%s: Resolve reported %v, want the unbalanced citation among them",
				fixture, reportedKinds(r.Resolve(found[0])))
		}
		for _, f := range r.Resolve(found[0]) {
			if !formFailureKinds[f.Kind] {
				t.Errorf("%s: Resolve reported %v, want only the failure kinds the form names", fixture, f)
			}
		}
		if found[1].Unbalanced {
			t.Errorf("%s: citation %q is balanced and is marked unbalanced", fixture, found[1].Text)
		}
	}
}

// hasKind reports whether a resolution carries a failure of the kind.
func hasKind(failures []citation.Failure, kind citation.FailureKind) bool {
	for _, f := range failures {
		if f.Kind == kind {
			return true
		}
	}
	return false
}

// TestFindReturnsNoUnbalancedCitationOverEveryFixture sweeps the whole fixture
// set for the same invariant, so a spelling added later cannot reintroduce a
// citation the pass would convert to an anchor while leaving a delimiter and
// the prose behind it in the carrier. A citation whose text carries an unpaired
// parenthesis is admitted only when it is marked unbalanced, which is what
// withholds it from the pass and routes it to a hand correction; the sweep
// reads that mark rather than a list of exempt fixtures, so a later spelling
// that leaves an unpaired delimiter unmarked is caught here.
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
			if balancedParens(c.Text) == c.Unbalanced {
				t.Errorf("%s: citation text %q is %s and Unbalanced is %v",
					entry.Name(), c.Text, balanceOf(c.Text), c.Unbalanced)
			}
		}
	}
}

// balanceOf names a citation text's delimiter balance for a failure message.
func balanceOf(text string) string {
	if balancedParens(text) {
		return "delimiter-balanced"
	}
	return "carrying an unpaired parenthesis"
}

// TestFindKeepsAMemberWhoseDigitsAreNotALineNumber pins the disposition of a
// member the grammar reads but no line number fits. The member is kept and
// reported rather than dropped: dropping a continuation member ends the member
// list there, and dropping a head member discards the occurrence whole, so the
// resolver does not read what is left, the ratchet does not count it, and a
// file whose only citation carries such a member reaches a per-file count of
// zero while the pointer stands. The three fixtures carry the digits in a
// continuation member, in the sole member of a citation, and in the second
// endpoint of a range.
func TestFindKeepsAMemberWhoseDigitsAreNotALineNumber(t *testing.T) {
	t.Parallel()
	r := citationResolver(t)
	for _, tc := range []struct {
		fixture string
		members int
	}{
		{"member-out-of-range.txt", 3},
		{"member-out-of-range-alone.txt", 1},
		{"member-out-of-range-endpoint.txt", 1},
	} {
		c := oneCitation(t, tc.fixture)
		if want := wantCitation(t, tc.fixture); c.Text != want {
			t.Errorf("%s: citation text is %q, want %q", tc.fixture, c.Text, want)
		}
		if len(c.Members) != tc.members {
			t.Errorf("%s: citation carries %d members, want %d", tc.fixture, len(c.Members), tc.members)
			continue
		}
		out := r.Resolve(c)
		if !hasKind(out, citation.OutOfRangeMember) {
			t.Errorf("%s: Resolve reported %v, want the out-of-range member among them",
				tc.fixture, reportedKinds(out))
		}
		for _, f := range out {
			if f.Kind == citation.OutOfRangeMember && f.Member.Text == "" {
				t.Errorf("%s: the out-of-range failure names no member", tc.fixture)
			}
			if !formFailureKinds[f.Kind] {
				t.Errorf("%s: Resolve reported %v, want only the failure kinds the form names", tc.fixture, f)
			}
		}
	}
}

// TestResolveResolvesTheMembersWrittenBesideAnOutOfRangeOne pins that the
// members the citation carries beside an unreadable one are still checked, so
// one report names everything a hand correction has to settle. The fixture
// cites two lines inside the section it names on either side of the unreadable
// member, which resolve, so the out-of-range member is the only failure.
func TestResolveResolvesTheMembersWrittenBesideAnOutOfRangeOne(t *testing.T) {
	t.Parallel()
	r := citationResolver(t)
	c := oneCitation(t, "member-out-of-range.txt")
	got := reportedKinds(r.Resolve(c))
	if want := []string{string(citation.OutOfRangeMember)}; !sameStrings(got, want) {
		t.Errorf("Resolve reported %v, want %v", got, want)
	}
	if r.Resolves(c) {
		t.Error("Resolves accepted a citation carrying a member that is not a line number")
	}
}

// TestFindEndsAMemberListAtTheHeadOfTheNextCitation pins the bound on member
// consumption. The path spelling that omits the spec/ prefix opens on two
// digits, which a member expression matches as readily as a line number, so a
// citation written behind a member separator would otherwise be read as the
// preceding citation's next member. The second citation is then never
// returned, so the resolver does not resolve it and the ratchet does not count
// it, and the pass rewrites its opening digits away with the span that
// swallowed them, leaving the rest of its path in the carrier. The phantom
// member the first citation gains resolves nowhere and holds its file above a
// zero count.
func TestFindEndsAMemberListAtTheHeadOfTheNextCitation(t *testing.T) {
	t.Parallel()
	const fixture = "bare-prefix-behind-separator.txt"
	found := citation.Find(citationFixture(t, fixture))
	texts := make([]string, 0, len(found))
	for _, c := range found {
		texts = append(texts, c.Text)
		if len(c.Members) != 1 {
			t.Errorf("%s: citation %q carries %d members, want 1: %v", fixture, c.Text, len(c.Members), members(c))
		}
	}
	if want := wantCitations(t, fixture); !sameStrings(texts, want) {
		t.Errorf("%s: Find returned %q, want %q", fixture, texts, want)
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
//
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

// TestFindRequiresACommentMarkerOnTheContinuationLine pins the bound on the
// join. The join consumes the newline together with the carrier's comment
// marker, which is what identifies the second line as the continuation of the
// comment the citation is written in. A join that crossed a bare line break
// folds two unrelated lines of a carrier together, so a reference ending one
// line and a number opening the next read as one citation the author never
// wrote, and the wrapped population the resolver baseline and the per-file
// ratchet baseline are seeded from is measured under the marker rule. The two
// carriers below wrap with nothing but indentation on the continuation line,
// which is a CRD description block scalar and a block comment whose interior
// lines carry no leading star.
func TestFindRequiresACommentMarkerOnTheContinuationLine(t *testing.T) {
	t.Parallel()
	for _, fixture := range []string{"unmarked-wrap-block-scalar.txt", "unmarked-wrap-block.txt"} {
		if found := citation.Find(citationFixture(t, fixture)); len(found) != 0 {
			t.Errorf("%s: Find returned %v across a wrap with no comment marker, want none", fixture, found)
		}
	}
}

// TestFindLeavesADelimitedGlossThatOpensTheFollowingLineOutsideTheCitation pins
// the side of the join a gloss may open on. A delimited gloss closes on its
// delimiter, so it may close on the continuation line the join folded in, and
// it opens on the line its member sits on. A gloss allowed to open behind the
// join takes the whole of the following comment line whenever that line opens
// with a parenthesis, a quote, or a backtick, even though nothing of the
// citation was wrapped, and that fragment is a sentence's own code span or
// parenthetical. The citation text a register is keyed by would then carry
// prose the citation does not own, and the anchor the pass writes in place of
// the whole citation would delete the newline, the carrier's comment marker,
// and that fragment, merging two comment lines and, in the # dialect, leaving
// the following text outside any comment.
func TestFindLeavesADelimitedGlossThatOpensTheFollowingLineOutsideTheCitation(t *testing.T) {
	t.Parallel()
	for _, fixture := range []string{
		"gloss-backtick-opens-next-line-slash.txt",
		"gloss-backtick-opens-next-line-hash.txt",
		"gloss-quoted-opens-next-line-slash.txt",
		"gloss-quoted-opens-next-line-hash.txt",
		"gloss-paren-opens-next-line-slash.txt",
		"gloss-paren-opens-next-line-hash.txt",
	} {
		c := oneCitation(t, fixture)
		if want := wantCitation(t, fixture); c.Text != want {
			t.Errorf("%s: citation text is %q, want it to end on its own line (%q)", fixture, c.Text, want)
		}
		if strings.Contains(c.Raw, "\n") {
			t.Errorf("%s: raw citation %q spans the following comment line", fixture, c.Raw)
		}
		if got := c.Members[len(c.Members)-1].Gloss; got != "" {
			t.Errorf("%s: gloss is %q, want the fragment on the following line left outside the citation", fixture, got)
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
//
// The rule is stated over the bare-word gloss, which closes on nothing but the
// end of its line. A gloss written inside a delimiter closes on that delimiter,
// so it may close on the continuation line, which the case below pins.
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

// TestFindConsumesAGlossThatClosesOnTheContinuationLine pins the third wrap
// position over a member that carries a delimited gloss. A gloss that may
// neither open across nor close across a consumed continuation fails to match
// at the wrap, the separator behind it fails with it, and the citation ends at
// that member, so every member written behind the wrap is left unconsumed:
// the resolver does not read them, the ratchet does not count them, and the
// rewritten carrier reads as an anchor followed by orphan integers while its
// file reaches a per-file count of zero. The first two cases carry the
// parenthesized gloss in the // and the # dialect, the third carries a quoted
// gloss ahead of a further member, and the fourth is the spelling the
// annotation preflight carries, where the wrapped gloss stands between two
// members of the same citation and the preflight is the fail-closed comparison
// that aborts an upgrade on a mismatched CRD annotation.
func TestFindConsumesAGlossThatClosesOnTheContinuationLine(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		fixture string
		members []string
		gloss   string
	}{
		{
			"gloss-paren-across-wrap-slash.txt",
			[]string{"413-413"},
			"tmpfs reservation (576Mi: /sessions 256Mi plus /tmp 256Mi for the scratch mount)",
		},
		{
			"gloss-paren-across-wrap-hash.txt",
			[]string{"413-413"},
			"tmpfs reservation (576Mi: /sessions 256Mi plus /tmp 256Mi for the scratch mount)",
		},
		{
			"gloss-quoted-across-wrap-slash.txt",
			[]string{"240-240", "241-241"},
			`messagingScope "the scope the session opens with, quoted from the table"`,
		},
		{
			"gloss-paren-across-wrap-then-member.txt",
			[]string{"437-437", "443-443"},
			"(\"`lenny.dev/schema-version` annotation on the CRD object\")",
		},
	} {
		c := oneCitation(t, tc.fixture)
		if want := wantCitation(t, tc.fixture); c.Text != want {
			t.Errorf("%s: citation text is %q, want %q", tc.fixture, c.Text, want)
		}
		if got := members(c); !sameStrings(got, tc.members) {
			t.Errorf("%s: members are %v, want %v", tc.fixture, got, tc.members)
		}
		if c.Members[0].Gloss != tc.gloss {
			t.Errorf("%s: gloss is %q, want %q", tc.fixture, c.Members[0].Gloss, tc.gloss)
		}
		if !strings.Contains(c.Raw, "\n") {
			t.Errorf("%s: raw citation %q does not span the wrap its gloss closes on", tc.fixture, c.Raw)
		}
	}
}

// citationResolver builds the resolver over the fixture specification tree.
// That tree carries a section with two subsections and one nested
// sub-subsection, a second file with two subsections, a third file with two
// top-level headings so a section-level range ends before the next heading of
// its own level, a fourth file that states its numbered title at level one and
// carries a fenced code block with a numbered heading-like line in it, so
// neither the title nor the fenced line declares a section, and a
// fifth
// file laid out the way every specification file is, which is a whole-file
// heading whose range runs to the end of the file with two sibling subsections
// under it. The ranges the cases below state are the tree's, and the fixture
// files themselves are the statement of record.
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

// TestSectionIndexReadsTheLevelTwoThroughLevelSixHeadingsOnly pins the heading
// predicate a section's range is computed from, which the resolver, the
// per-file ratchet, and every population the migration measures share. A
// section is declared by a `##` through `######` heading, so a file that states
// its number at level one alone declares no section of that number: a citation
// naming it is reported as an unknown section and enters the seeded resolution
// baseline, and a path-form citation into the title's own lines falls in no
// section. Admitting the level-one title instead resolves those citations
// clean, which measures a different population than the one the baselines are
// seeded from and the per-file counts are driven to zero against.
//
// The headings below such a title are indexed as usual, and the walk skips
// fenced code, because a heading-like line inside an example is a comment and
// indexing it declares sections the specification does not have, including one
// that collides with a genuine section number.
func TestSectionIndexReadsTheLevelTwoThroughLevelSixHeadingsOnly(t *testing.T) {
	t.Parallel()
	r := citationResolver(t)
	if title, ok := r.Section("30"); ok {
		t.Errorf("the level-one title declared %v, want no section of that number", title)
	}
	child, ok := r.Section("30.1")
	if !ok || child.File != "spec/30_level-one-title.md" || child.Start != 9 || child.End != 11 {
		t.Errorf("§30.1 is %v (present=%v), want lines 9-11 of the level-one-titled file", child, ok)
	}
	if s, ok := r.Section("1"); ok {
		t.Errorf("a numbered heading inside a fenced code block declared %v", s)
	}
	title := r.Resolve(oneCitation(t, "resolve/level-one-heading.txt"))
	if len(title) != 1 || title[0].Kind != citation.UnknownSection {
		t.Errorf("a citation naming the level-one title reported %v, want one unknown section", title)
	}
	if f := r.Resolve(oneCitation(t, "resolve/level-one-subsection.txt")); len(f) != 0 {
		t.Errorf("a citation naming the file's own subsection reported %v, want it to resolve", f)
	}
	preamble := r.Resolve(oneCitation(t, "resolve/path-level-one-preamble.txt"))
	if len(preamble) != 1 || preamble[0].Kind != citation.OutsideSection {
		t.Errorf("a path-form citation into the title's own lines reported %v, want one outside-section failure", preamble)
	}
	titled := map[string]string{
		"spec/31_untitled.md": "# Untitled\n\nProse.\n\n## 31.1 Only Section\n\nBody.\n",
		"spec/32_repeated.md": "# 32. Repeated\n\nProse.\n\n## 32. Repeated\n\nBody.\n",
	}
	list := func(context.Context) ([]string, error) {
		return []string{"spec/31_untitled.md", "spec/32_repeated.md"}, nil
	}
	read := func(target string) ([]byte, error) { return []byte(titled[target]), nil }
	plain, err := citation.NewResolver(context.Background(), list, read)
	if err != nil {
		t.Fatalf("NewResolver over a tree whose files carry level-one titles: %v", err)
	}
	if s, ok := plain.Section("31"); ok {
		t.Errorf("an unnumbered level-one title declared %v", s)
	}
	if s, ok := plain.Section("31.1"); !ok || s.Start != 5 {
		t.Errorf("§31.1 is %v (present=%v), want it to start at line 5", s, ok)
	}
	if s, ok := plain.Section("32"); !ok || s.Start != 5 {
		t.Errorf("§32 is %v (present=%v), want the level-two heading at line 5 to declare it", s, ok)
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
		{"resolve/path-straddling-under-whole-file-parent.txt", []citation.FailureKind{citation.StraddlingRange}},
		{"resolve/path-preamble-under-whole-file-parent.txt", nil},
		{"resolve/straddling-under-whole-file-parent.txt", []citation.FailureKind{citation.StraddlingRange}},
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

// TestResolvePathAnchorsAMemberOnTheDeepestSectionContainingItsStart pins the
// straddling rule for the path form. A member is anchored on the deepest
// section containing its start line, and it straddles when its end line falls
// outside that section. A range that runs from a section's preamble into one of
// its own subsections is anchored on the parent, whose range covers its
// subsections, so it resolves the way the same range written in the
// section-number spelling resolves against that parent.
//
// The last case is the layout every specification file carries, which is a
// whole-file heading whose range runs to the end of the file. Asking instead
// whether any section of the file contains both endpoints lets that heading
// answer for every range, so a range crossing a boundary inside it resolves
// clean under the path form while the same range written by section number is
// reported, and the citation is never sent for hand correction while it holds
// its file above a zero count.
func TestResolvePathAnchorsAMemberOnTheDeepestSectionContainingItsStart(t *testing.T) {
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
	whole, ok := r.Section("40")
	under := oneCitation(t, "resolve/path-straddling-under-whole-file-parent.txt")
	if !ok || !whole.Contains(under.Members[0].Start) || !whole.Contains(under.Members[0].End) {
		t.Fatalf("the fixture range %v does not sit inside the whole-file section (%v)", under.Members, whole)
	}
	byPath := r.Resolve(under)
	if len(byPath) != 1 || byPath[0].Kind != citation.StraddlingRange {
		t.Errorf("a range crossing a boundary under a whole-file heading reported %v, want one straddling range", byPath)
	}
	byNumber := r.Resolve(oneCitation(t, "resolve/straddling-under-whole-file-parent.txt"))
	if len(byNumber) != 1 || byNumber[0].Kind != citation.StraddlingRange {
		t.Errorf("the same range written by section number reported %v, want one straddling range", byNumber)
	}
	if f := r.Resolve(oneCitation(t, "resolve/path-preamble-under-whole-file-parent.txt")); len(f) != 0 {
		t.Errorf("a range from the whole-file preamble into its own subsection reported %v", f)
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

// fixtureLinePass holds the line pass fixtures: the shared
// specification the citations resolve against, the tree the pass runs
// over, the expected content of every file it rewrites, the trees whose
// single carrier the pass fails on, and the registers that drive each
// run. The trees are held apart from the specification so a case
// assembles the carriers it needs against one section index.
const fixtureLinePass = "testdata/linepass"

// lineTree assembles the tree one line pass case runs over, which is the
// shared specification fixture plus the case's own carriers.
func lineTree(t *testing.T, carriers string) string {
	t.Helper()
	root := t.TempDir()
	copyTreeInto(t, filepath.Join(fixtureLinePass, "spec"), root)
	copyTreeInto(t, filepath.Join(fixtureLinePass, carriers), root)
	return root
}

// lineRewriter returns the line pass over the tree at root, driven by
// the named register fixture.
func lineRewriter(t *testing.T, root, register string) *line.Rewriter {
	t.Helper()
	r := line.New(scope.DirLister(root), scope.DirReader(root))
	if err := r.LoadRegister(filepath.Join(fixtureLinePass, "registers", register)); err != nil {
		t.Fatalf("load the line pass register %s: %v", register, err)
	}
	return r
}

// applyLinePass runs the line pass over the tree at root and returns the
// applied diff.
func applyLinePass(t *testing.T, root, register string) pass.Diff {
	t.Helper()
	h := pass.NewHarnessOver(scope.DirLister(root), scope.DirReader(root), dirWriterFor(root))
	diff, err := h.Apply(context.Background(), lineRewriter(t, root, register))
	if err != nil {
		t.Fatalf("apply the line pass: %v", err)
	}
	return diff
}

// planLinePass runs the line pass over the tree at root without writing,
// and returns the error a fail-closed case expects.
func planLinePass(t *testing.T, root, register string) (pass.Diff, error) {
	t.Helper()
	h := pass.NewHarnessOver(scope.DirLister(root), scope.DirReader(root), dirWriterFor(root))
	return h.Plan(context.Background(), lineRewriter(t, root, register))
}

// assertConverted compares one rewritten carrier against the expected
// content held beside the fixture tree, and checks the two properties
// the expectation alone does not state: the file carries no citation of
// the retired form any more, and no member of the citations it carried
// is left standing as an orphan integer.
func assertConverted(t *testing.T, root, target string) {
	t.Helper()
	before := readFixtureFile(t, filepath.Join(fixtureLinePass, "tree", target))
	after := readFixtureFile(t, filepath.Join(root, filepath.FromSlash(target)))
	want := readFixtureFile(t, filepath.Join(fixtureLinePass, "want", target))
	if after != want {
		t.Fatalf("%s after the line pass is\n%s\nwant\n%s", target, after, want)
	}
	if left := citation.Find(after); len(left) > 0 {
		t.Errorf("%s still carries %v", target, left)
	}
	for _, c := range citation.Find(before) {
		for _, m := range c.Members {
			// The member is looked for as a number standing on its own,
			// so a digit of the anchor's own section number does not read
			// as an orphan.
			orphan := regexp.MustCompile(`(?:^|[^0-9.])` + regexp.QuoteMeta(m.Text) + `(?:[^0-9]|$)`)
			if orphan.MatchString(after) {
				t.Errorf("%s left the member %q standing", target, m.Text)
			}
		}
	}
}

// readFixtureFile reads a file of the fixture tree or of the tree a case
// wrote.
func readFixtureFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

// TestLinePassConvertsEverySpellingToASingleAnchorCitation pins the
// conversion against one carrier per spelling of the retired form. Each
// carrier becomes the anchor for the section it names, with no line
// number and no orphan integer left behind, and a qualifier is carried
// through the conversion.
//
// spec: §28.1 (N8, the citation rule: a citation of the retired form is
// replaced by the anchor of the section it names)
func TestLinePassConvertsEverySpellingToASingleAnchorCitation(t *testing.T) {
	t.Parallel()
	root := lineTree(t, "tree")
	applyLinePass(t, root, "tree.yaml")
	for _, tc := range []struct {
		spelling string
		target   string
	}{
		{"dotted section number", "pkg/spellings/dotted.go"},
		{"section level", "pkg/spellings/section-level.go"},
		{"comma-separated members", "pkg/spellings/comma-list.go"},
		{"slash-separated members", "pkg/spellings/slash-list.go"},
		{"and-separated members", "pkg/spellings/and-list.go"},
		{"plus-separated members with glosses", "pkg/spellings/plus-gloss.go"},
		{"a member list repeating the keyword", "pkg/spellings/repeated-keyword.go"},
		{"a qualifier", "pkg/spellings/qualifier.go"},
		{"a hyphenated range", "pkg/spellings/hyphen-range.go"},
		{"an en-dash range", "pkg/spellings/endash-range.go"},
		{"an em-dash range", "pkg/spellings/emdash-range.go"},
		{"the colon standing in for the keyword", "pkg/spellings/colon-section.go"},
		{"the colon against a path reference", "pkg/spellings/colon-path.go"},
		{"a path reference", "pkg/spellings/path-form.go"},
		{"a path reference with the prefix absent", "pkg/spellings/path-bare-prefix.go"},
	} {
		t.Run(tc.spelling, func(t *testing.T) {
			assertConverted(t, root, tc.target)
		})
	}
}

// TestLinePassConvertsAWrappedCitationInEveryPositionAndDialect pins the
// conversion of a citation wrapped across two comment lines, which the
// continuation join reads as one citation. The cases are one per wrap
// position, which are a wrap between the reference and the keyword, a
// wrap between the keyword and its first member, and a wrap inside the
// member list, in each of the carrier dialects.
//
// spec: §28.1 (N8, the citation rule: a wrapped citation is one citation
// and is retired whole)
func TestLinePassConvertsAWrappedCitationInEveryPositionAndDialect(t *testing.T) {
	t.Parallel()
	root := lineTree(t, "tree")
	applyLinePass(t, root, "tree.yaml")
	for _, dialect := range []struct {
		name   string
		prefix string
		suffix string
	}{
		{"slash comments", "pkg/wrapped/", ".go"},
		{"hash comments", "compose/", ".yaml"},
		{"dash comments", "migrations/", ".sql"},
	} {
		for _, position := range []string{"reference", "keyword", "members"} {
			t.Run(dialect.name+" wrapped at the "+position, func(t *testing.T) {
				assertConverted(t, root, dialect.prefix+position+dialect.suffix)
			})
		}
	}
}

// TestLinePassStripsAServedArtifactAndConvertsEveryOtherCarrier pins the
// served client artifacts: a citation in the text a client reads is
// removed rather than converted, because a specification anchor is not
// part of the client contract, while the same run converts the citation
// an ordinary carrier holds. The cases are one per served dialect, which
// are the served JSON values, the Go string literals of the served tool
// definitions, and the struct tags the chart schema is generated from.
// The Go doc comments of a served carrier are ordinary authoring sites
// and convert.
//
// spec: §28.1 (N8, the citation rule: the retired form leaves the tree,
// and a served artifact carries no anchor in its place)
func TestLinePassStripsAServedArtifactAndConvertsEveryOtherCarrier(t *testing.T) {
	t.Parallel()
	root := lineTree(t, "tree")
	applyLinePass(t, root, "tree.yaml")
	for _, tc := range []struct {
		dialect string
		target  string
	}{
		{"served JSON values", "pkg/gateway/externalapi/openapi/openapi.json"},
		{"served tool definitions", "pkg/gateway/mcpfabric/mcptools/mcptools.go"},
		{"generated chart schema descriptions", "pkg/chart/values/values.go"},
	} {
		t.Run(tc.dialect, func(t *testing.T) {
			assertConverted(t, root, tc.target)
			after := readFixtureFile(t, filepath.Join(root, filepath.FromSlash(tc.target)))
			if strings.Contains(after, "line 5") || strings.Contains(after, "line 6") {
				t.Errorf("%s carries a line number after the strip:\n%s", tc.target, after)
			}
		})
	}
	// The ordinary carrier in the same run is converted rather than
	// stripped, so the strip is a property of the served artifact.
	assertConverted(t, root, "pkg/spellings/dotted.go")
}

// TestLinePassStripsTwoAdjacentServedCitationsWithoutCuttingACharacter
// pins the strip of two citations written against one separator inside a
// single served value. Each strip widens over the punctuation that
// introduced its citation, so two neighbours widen over the blanks
// between them; every span is measured against the file as it stood, so
// two spans that overlap cannot both be spliced and the second cuts bytes
// belonging to neither. In a served document the bytes it cuts sit inside
// a multi-byte character, which leaves the document a client reads
// invalid. Nothing downstream reads it: both citations are gone, the
// accounting balances, and the dry-run diff carries the same bytes the
// apply writes.
//
// spec: §28.1 (N8, the citation rule: a citation in a served client
// artifact is stripped from the text a client reads)
func TestLinePassStripsTwoAdjacentServedCitationsWithoutCuttingACharacter(t *testing.T) {
	t.Parallel()
	const target = "pkg/gateway/externalapi/openapi/openapi.json"
	before := readFixtureFile(t, filepath.Join(fixtureLinePass, "tree", target))
	if n := len(citation.Find(before)); n < 2 {
		t.Fatalf("the served fixture %s carries %d citations, so it holds no adjacent pair", target, n)
	}
	root := lineTree(t, "tree")
	applyLinePass(t, root, "tree.yaml")
	after := readFixtureFile(t, filepath.Join(root, filepath.FromSlash(target)))
	if !utf8.ValidString(after) {
		t.Errorf("the strip left %s carrying an incomplete character:\n%q", target, after)
	}
	var document any
	if err := json.Unmarshal([]byte(after), &document); err != nil {
		t.Errorf("the strip left %s unreadable as JSON: %v\n%s", target, err, after)
	}
	if !strings.Contains(after, `"description": "renewal is idempotent while the lease stands."`) {
		t.Errorf("the two adjacent strips did not leave the served description standing:\n%s", after)
	}
}

// TestLinePassLeavesTheServedTextAStrippedCitationIntroduced pins what a
// served strip removes. A conversion replaces the whole citation because
// the anchor it writes says what the citation said. A strip writes
// nothing in its place, and the served artifact is the client contract,
// so what it removes is the reference-and-members run alone: the gloss
// written against the last member is the description's own prose, and
// removing it with the pointer empties or truncates the description a
// client reads. The punctuation that introduced the text, including the
// dash of a citation opening a bracketed clause, goes with the pointer.
//
// spec: §28.1 (N8, the citation rule: a citation in a served client
// artifact is stripped from the text a client reads)
func TestLinePassLeavesTheServedTextAStrippedCitationIntroduced(t *testing.T) {
	t.Parallel()
	const target = "pkg/gateway/externalapi/openapi/openapi.json"
	root := lineTree(t, "tree")
	applyLinePass(t, root, "tree.yaml")
	after := readFixtureFile(t, filepath.Join(root, filepath.FromSlash(target)))
	for _, tc := range []struct {
		name  string
		value string
	}{
		{"a citation opening a description with a bare-word gloss", `"description": "authored beside the session."`},
		{"a citation opening a bracketed clause", `"description": "Drain a pool (stop admitting new sessions, report the in-flight count)."`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(after, tc.value) {
				t.Errorf("the strip did not leave %s reading as %s:\n%s", target, tc.value, after)
			}
		})
	}
}

// TestLinePassReadsAServedToolSchemaTieFromItsWholeAuthoringSource pins
// where the tie of a served tool schema stands. The strip removes text a
// client reads and leaves the Go source that authored it, so the tie it
// has to leave standing is any reference to the section in that source.
// A served schema literal spans several lines, and the section it names
// is often tied by a comment over another declaration of the same file,
// so a tie looked for on the preceding source line, or in the doc
// comment of the one declaration the literal sits in, would report a tie
// the file plainly carries as missing and stop the pass on every such
// site.
//
// spec: §28.1 (N8, the citation rule: a stripped citation leaves a
// standing tie to the section it named)
func TestLinePassReadsAServedToolSchemaTieFromItsWholeAuthoringSource(t *testing.T) {
	t.Parallel()
	const target = "pkg/gateway/mcpfabric/mcptools/mcptools.go"
	before := readFixtureFile(t, filepath.Join(fixtureLinePass, "tree", target))
	cited := citation.Find(before)
	if len(cited) == 0 {
		t.Fatalf("the served fixture %s carries no citation", target)
	}
	// The fixture holds a served citation at least two lines below the
	// declaration whose doc comment carries its tie, which is the layout
	// the served tool schemas are written in.
	declared := lineNumberOf(before, "var sendMessageInputSchema")
	if declared == 0 {
		t.Fatal("the served fixture holds no multi-line served literal")
	}
	deep := false
	for _, c := range cited {
		if c.Line >= declared+2 {
			deep = true
		}
	}
	if !deep {
		t.Fatal("the served fixture holds no citation below its declaration's own line")
	}
	root := lineTree(t, "tree")
	applyLinePass(t, root, "tree.yaml")
	assertConverted(t, root, target)
	after := readFixtureFile(t, filepath.Join(root, filepath.FromSlash(target)))
	if strings.Contains(after, "line 14") {
		t.Errorf("%s carries a line number after the strip:\n%s", target, after)
	}
	if !strings.Contains(after, "§4.8") {
		t.Errorf("the strip left no tie to the section the served literal named:\n%s", after)
	}
	// The tie of the served description inside registerMemoryTools stands
	// in another declaration's doc comment, so a tie read from the
	// enclosing declaration alone would abort the whole run here.
	if !strings.Contains(after, `Description: "Write a memory to the store."`) {
		t.Errorf("the served description tied elsewhere in the file was not stripped:\n%s", after)
	}
}

// TestLinePassConvertsAnMCPErrorMessageOutsideAToolSchema pins the served
// surface of the MCP tool source. What the gateway serves as a tool
// schema is the tool definition's description and input schema, so a
// citation in any other string literal of that file, such as the message
// a handler returns when an argument is missing, is an ordinary
// authoring site and converts to an anchor.
//
// spec: §28.1 (N8, the citation rule: a citation of the retired form is
// replaced by the anchor of the section it names)
func TestLinePassConvertsAnMCPErrorMessageOutsideAToolSchema(t *testing.T) {
	t.Parallel()
	const target = "pkg/gateway/mcpfabric/mcptools/mcptools.go"
	root := lineTree(t, "tree")
	applyLinePass(t, root, "tree.yaml")
	assertConverted(t, root, target)
	after := readFixtureFile(t, filepath.Join(root, filepath.FromSlash(target)))
	if !strings.Contains(after, `"content is required (§4.6)"`) {
		t.Errorf("the error message outside a tool schema was not converted to its anchor:\n%s", after)
	}
}

// lineNumberOf returns the 1-based line the prefix first stands on, zero
// when the text does not carry it.
func lineNumberOf(text, prefix string) int {
	for i, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return i + 1
		}
	}
	return 0
}

// TestLinePassConvertsAChartValuesLiteralOutsideADescTag pins the served
// surface of the chart values source. What the chart schema generator
// copies verbatim is the `desc:` struct-tag value, so a citation in any
// other string literal of that file is an ordinary authoring site and
// converts to an anchor like every other carrier.
//
// spec: §28.1 (N8, the citation rule: a citation of the retired form is
// replaced by the anchor of the section it names)
func TestLinePassConvertsAChartValuesLiteralOutsideADescTag(t *testing.T) {
	t.Parallel()
	const target = "pkg/chart/values/values.go"
	root := lineTree(t, "tree")
	applyLinePass(t, root, "tree.yaml")
	assertConverted(t, root, target)
	after := readFixtureFile(t, filepath.Join(root, filepath.FromSlash(target)))
	if !strings.Contains(after, `"The cluster-type dimension is stated in §4.6."`) {
		t.Errorf("the literal outside the desc tag was not converted to its anchor:\n%s", after)
	}
	if !strings.Contains(after, `desc:"Cluster-type composition dimension (laptop, eks, or gke)."`) {
		t.Errorf("the served desc tag was not stripped:\n%s", after)
	}
}

// TestLinePassFailsAStraddlingRangeRatherThanGuessingAnAnchor pins the
// first fail-and-report case. A range whose endpoints fall in two
// sections names no single anchor, so the pass reports it for hand
// correction and the harness leaves the tree byte-identical.
//
// spec: §28.1 (N8, the citation rule: a citation that cannot be retired
// mechanically is reported rather than converted against a guess)
func TestLinePassFailsAStraddlingRangeRatherThanGuessingAnAnchor(t *testing.T) {
	t.Parallel()
	root := lineTree(t, "fail/straddling-range")
	before := treeSnapshot(t, root)
	_, err := planLinePass(t, root, "fail-straddling-range.yaml")
	if err == nil {
		t.Fatal("the line pass converted a straddling range")
	}
	abort, ok := pass.AsAbort(err)
	if !ok {
		t.Fatalf("the straddling range was not reported as a fail-closed abort: %v", err)
	}
	if abort.Path != "pkg/carrier/straddle.go" || abort.Line == 0 {
		t.Errorf("the abort does not name the carrier and the line: %v", abort)
	}
	if !strings.Contains(abort.Reason, "straddles") {
		t.Errorf("the abort does not name the straddle: %v", abort)
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the failed run wrote to the tree")
	}
}

// TestLinePassFailsAPathFormCitationNamingAnUnresolvedFile pins the
// second fail-and-report case. A path-form citation names no section, so
// a file that does not resolve under spec/ leaves no anchor to infer and
// the pass reports the citation rather than converting it against a
// guessed file.
//
// spec: §28.1 (N8, the citation rule: a citation that cannot be retired
// mechanically is reported rather than converted against a guess)
func TestLinePassFailsAPathFormCitationNamingAnUnresolvedFile(t *testing.T) {
	t.Parallel()
	root := lineTree(t, "fail/unknown-file")
	before := treeSnapshot(t, root)
	_, err := planLinePass(t, root, "fail-unknown-file.yaml")
	if err == nil {
		t.Fatal("the line pass converted a path-form citation naming a file that does not resolve")
	}
	abort, ok := pass.AsAbort(err)
	if !ok {
		t.Fatalf("the unresolved path was not reported as a fail-closed abort: %v", err)
	}
	if abort.Path != "pkg/carrier/unknown.go" {
		t.Errorf("the abort does not name the carrier: %v", abort)
	}
	if !strings.Contains(abort.Reason, "does not resolve") {
		t.Errorf("the abort does not name the unresolved file: %v", abort)
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the failed run wrote to the tree")
	}
}

// TestLinePassFailsAStrippedServedCitationWithNoSurvivingTie pins the
// third fail-and-report case. A citation stripped from a served artifact
// is removed rather than replaced, so the tie has to stand in the
// authoring source the strip leaves behind. A field whose struct tag is
// the only carrier of the tie fails instead, because stripping it would
// delete the tie rather than relocate it, and a deleted citation reads
// to the ratchet and the resolver as a retirement.
//
// spec: §28.1 (N8, the citation rule: a retired citation leaves a
// standing tie to the section it named)
func TestLinePassFailsAStrippedServedCitationWithNoSurvivingTie(t *testing.T) {
	t.Parallel()
	root := lineTree(t, "fail/served-no-tie")
	before := treeSnapshot(t, root)
	_, err := planLinePass(t, root, "fail-served-no-tie.yaml")
	if err == nil {
		t.Fatal("the line pass stripped the only carrier of a tie")
	}
	abort, ok := pass.AsAbort(err)
	if !ok {
		t.Fatalf("the missing tie was not reported as a fail-closed abort: %v", err)
	}
	if abort.Path != "pkg/chart/values/values.go" {
		t.Errorf("the abort does not name the served carrier: %v", abort)
	}
	if !strings.Contains(abort.Reason, "tie") {
		t.Errorf("the abort does not name the tie the strip would delete: %v", abort)
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the failed run wrote to the tree")
	}
}

// TestLinePassFailsAServedJSONStripWithNoSurvivingTie pins that the
// surviving-tie rule ranges over every served artifact rather than over
// the Go carriers alone. A data artifact has no comment channel, so the
// document itself is the only authoring channel it has: a strip that
// leaves nothing in the document naming the section deletes the tie
// rather than relocating it, and both the ratchet and the resolver read
// that deletion as a retirement.
//
// spec: §28.1 (N8, the citation rule: a retired citation leaves a
// standing tie to the section it named)
func TestLinePassFailsAServedJSONStripWithNoSurvivingTie(t *testing.T) {
	t.Parallel()
	const target = "pkg/gateway/externalapi/openapi/openapi.json"
	root := lineTree(t, "fail/served-json-no-tie")
	before := treeSnapshot(t, root)
	_, err := planLinePass(t, root, "fail-served-json-no-tie.yaml")
	if err == nil {
		t.Fatal("the line pass stripped the document's only tie")
	}
	abort, ok := pass.AsAbort(err)
	if !ok {
		t.Fatalf("the missing tie was not reported as a fail-closed abort: %v", err)
	}
	if abort.Path != target {
		t.Errorf("the abort does not name the served document: %v", abort)
	}
	if !strings.Contains(abort.Reason, "tie") {
		t.Errorf("the abort does not name the tie the strip would delete: %v", abort)
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the failed run wrote to the tree")
	}
	// The same strip stands when the document keeps a tie, so the rule
	// is the surviving tie rather than a refusal to strip JSON.
	kept := lineTree(t, "tree")
	applyLinePass(t, kept, "tree.yaml")
	assertConverted(t, kept, target)
}

// TestLinePassReportsEveryUnconvertibleSiteInOneRun pins the disposition
// of the sites the pass hands back. The operator hand-corrects the whole
// population before the pass is re-run, so a run that stopped at the
// first site would need one run per site to enumerate what the
// correction covers. Every site of the walk is named, across the files
// of the tree and within one file, and the tree is left byte-identical.
//
// spec: §28.1 (N8, the citation rule: a citation that cannot be retired
// mechanically is reported rather than converted against a guess)
func TestLinePassReportsEveryUnconvertibleSiteInOneRun(t *testing.T) {
	t.Parallel()
	root := lineTree(t, "fail/many-sites")
	before := treeSnapshot(t, root)
	_, err := planLinePass(t, root, "fail-many-sites.yaml")
	if err == nil {
		t.Fatal("the line pass converted the sites it cannot resolve")
	}
	sites, ok := pass.AllAborts(err)
	if !ok {
		t.Fatalf("the sites were not reported as fail-closed aborts: %v", err)
	}
	reported := make(map[string]bool, len(sites))
	for _, site := range sites {
		reported[fmt.Sprintf("%s:%d", site.Path, site.Line)] = true
	}
	for _, want := range []string{"pkg/carrier/straddle.go:9", "pkg/carrier/straddle.go:14", "pkg/carrier/unknown.go:7"} {
		if !reported[want] {
			t.Errorf("the run does not report %s; it reported %v", want, reported)
		}
	}
	// The operator-facing message names each of them, because it is what
	// the hand correction is worked from.
	message := reportAbort(err).Error()
	for _, want := range []string{"straddle.go:9", "straddle.go:14", "unknown.go:7"} {
		if !strings.Contains(message, want) {
			t.Errorf("the reported message does not name %s: %s", want, message)
		}
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the failed run wrote to the tree")
	}
}

// TestLinePassFailsAConversionThatComposesTheRetiredFormAgain pins the
// post-condition on the conversion. The pass replaces a citation with
// the anchor of the section it names, and the text beside the citation
// stays where it was written, so for two spellings the anchor and that
// text compose a fresh citation of the retired form. Writing the file
// would leave it above zero after the pass has run over it, and a
// second run would convert the composed citation, whose span now covers
// the carrier's own prose. Each spelling is reported for hand
// correction instead, and the tree is left byte-identical.
//
// The cases are one per composing spelling: a bare-word gloss running
// to a trailing colon, whose anchor then stands against that colon and
// reads the integer opening the next comment line as a member; and a
// separator word followed by a parenthesized reference on the next
// comment line, whose reference the anchor's qualifier then absorbs.
//
// spec: §28.1 (N8, the citation rule: a citation that cannot be retired
// mechanically is reported rather than converted, and a conversion
// leaves no citation of the retired form standing)
func TestLinePassFailsAConversionThatComposesTheRetiredFormAgain(t *testing.T) {
	t.Parallel()
	// composed is the head of the text the abort quotes, held short of a
	// span the matcher reads as the retired form so the fixture rule
	// keeps holding that form in testdata/ alone. The head is what
	// distinguishes the composition: the anchor standing against the
	// carrier's colon, and the qualifier that absorbed the words behind
	// the separator.
	for _, tc := range []struct {
		spelling string
		carriers string
		register string
		target   string
		composed string
	}{
		{
			spelling: "a gloss running to a trailing colon",
			carriers: "fail/reformed-colon",
			register: "fail-reformed-colon.yaml",
			target:   "pkg/carrier/gloss.go",
			composed: "§4.6: 1",
		},
		{
			spelling: "a separator word ahead of a wrapped reference",
			carriers: "fail/reformed-join",
			register: "fail-reformed-join.yaml",
			target:   "pkg/carrier/join.go",
			composed: "§4.8 workspace and egress",
		},
	} {
		t.Run(tc.spelling, func(t *testing.T) {
			t.Parallel()
			root := lineTree(t, tc.carriers)
			before := treeSnapshot(t, root)
			_, err := planLinePass(t, root, tc.register)
			if err == nil {
				t.Fatal("the line pass wrote a conversion that composes the retired form again")
			}
			abort, ok := pass.AsAbort(err)
			if !ok {
				t.Fatalf("the composed citation was not reported as a fail-closed abort: %v", err)
			}
			if abort.Path != tc.target || abort.Line == 0 {
				t.Errorf("the abort does not name the carrier and the line: %v", abort)
			}
			if !strings.Contains(abort.Reason, tc.composed) {
				t.Errorf("the abort does not name the composed citation %q: %v", tc.composed, abort)
			}
			if !strings.Contains(abort.Reason, "composes the retired form again") {
				t.Errorf("the abort does not say what it reports: %v", abort)
			}
			if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
				t.Error("the failed run wrote to the tree")
			}
		})
	}
}

// TestLinePassLeavesEveryWriteExcludedCarrierByteIdentical pins the
// write exclusion. A citation in the staged proposal tree, in either
// historical audit record, and in either root planning document is left
// exactly as it was written and appears in neither the dry-run output
// nor the applied diff, while an equivalent citation in an ordinary
// carrier in the same run is converted.
//
// spec: §28.1 (N8, the citation rule: the excluded records are outside
// the writable population)
func TestLinePassLeavesEveryWriteExcludedCarrierByteIdentical(t *testing.T) {
	t.Parallel()
	root := lineTree(t, "tree")
	excluded := []string{
		"proposals/0001_example.md",
		"BUILD-GAPS.md",
		"TEST-GAPS.md",
		"gateway-runtime-comms.md",
		"gateway-runtime-comms-remediation.md",
	}
	before := treeSnapshot(t, root)
	planned, err := planLinePass(t, root, "tree.yaml")
	if err != nil {
		t.Fatalf("plan the line pass: %v", err)
	}
	applied := applyLinePass(t, root, "tree.yaml")
	after := treeSnapshot(t, root)
	inPlan, inApplied := membership(planned.Paths()), membership(applied.Paths())
	for _, target := range excluded {
		if before[target] == "" {
			t.Fatalf("the fixture tree carries no %s", target)
		}
		if after[target] != before[target] {
			t.Errorf("%s was rewritten:\n%s", target, after[target])
		}
		if inPlan[target] {
			t.Errorf("the dry-run output names the excluded %s", target)
		}
		if inApplied[target] {
			t.Errorf("the applied diff names the excluded %s", target)
		}
	}
	assertConverted(t, root, "pkg/spellings/dotted.go")
}

// TestLinePassLeavesAGeneratedArtifactUnmodified pins that a file the
// per-file generated-artifact rule selects is left as it stands, because
// its route to a zero count is the regeneration of its source rather
// than a rewrite. The case runs over a CRD under charts/lenny/crds/,
// which the rule selects through its producer-output disjunct rather
// than through a generation marker.
//
// spec: §28.1 (N8, the citation rule: a generated artifact reaches zero
// through its producer)
func TestLinePassLeavesAGeneratedArtifactUnmodified(t *testing.T) {
	t.Parallel()
	const generated = "charts/lenny/crds/lenny.dev_runtimes.yaml"
	root := lineTree(t, "tree")
	before := treeSnapshot(t, root)
	if len(citation.Find(before[generated])) == 0 {
		t.Fatalf("the generated fixture %s carries no citation", generated)
	}
	planned, err := planLinePass(t, root, "tree.yaml")
	if err != nil {
		t.Fatalf("plan the line pass: %v", err)
	}
	applied := applyLinePass(t, root, "tree.yaml")
	if membership(planned.Paths())[generated] || membership(applied.Paths())[generated] {
		t.Errorf("the line pass names the generated artifact %s", generated)
	}
	if got := treeSnapshot(t, root); got[generated] != before[generated] {
		t.Errorf("the generated artifact was rewritten:\n%s", got[generated])
	}
	assertConverted(t, root, "pkg/spellings/dotted.go")
}

// TestLinePassDryRunOutputEqualsTheAppliedDiff pins the entry criterion
// for applying the pass: what the dry run reports is what the apply
// writes, so a reviewer reads the whole change before any file moves.
//
// spec: §28.1 (N8, the citation rule: the retirement is reviewed before
// it is applied)
func TestLinePassDryRunOutputEqualsTheAppliedDiff(t *testing.T) {
	t.Parallel()
	root := lineTree(t, "tree")
	before := treeSnapshot(t, root)
	planned, err := planLinePass(t, root, "tree.yaml")
	if err != nil {
		t.Fatalf("plan the line pass: %v", err)
	}
	if len(planned.Files) == 0 {
		t.Fatal("the dry run reports no work over the fixture tree")
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the dry run wrote to the tree")
	}
	applied := applyLinePass(t, root, "tree.yaml")
	if !planned.Equal(applied) {
		t.Fatalf("the applied diff differs from the dry run: %v vs %v", planned.Paths(), applied.Paths())
	}
}

// TestLinePassFailsARunThatRetiresACitationWithNoAnchor pins the
// accounting identity the pass checks over every file it rewrites. A
// citation deleted rather than converted reads to the per-file ratchet
// and to the resolver as a retirement, so the run that reduced the count
// without emitting the anchor that replaces it fails instead.
//
// spec: §28.1 (N8, the citation rule: a count falls only when an anchor
// replaces the citation)
func TestLinePassFailsARunThatRetiresACitationWithNoAnchor(t *testing.T) {
	t.Parallel()
	before := readFixtureFile(t, filepath.Join(fixtureLinePass, "tree", "pkg/spellings/dotted.go"))
	converted := readFixtureFile(t, filepath.Join(fixtureLinePass, "want", "pkg/spellings/dotted.go"))
	if err := line.Account(before, converted, 0); err != nil {
		t.Fatalf("the accounting rejected a conversion that emitted its anchor: %v", err)
	}
	cited := citation.Find(before)
	if len(cited) != 1 {
		t.Fatalf("the fixture carries %d citations, want 1", len(cited))
	}
	deleted := before[:cited[0].Offset] + before[cited[0].Offset+len(cited[0].Raw):]
	if err := line.Account(before, deleted, 0); err == nil {
		t.Error("the accounting accepted a citation deleted with no anchor in its place")
	}
	// A strip is the one retirement that emits no anchor, and it is
	// accounted for by being declared.
	if err := line.Account(before, deleted, 1); err != nil {
		t.Errorf("the accounting rejected a declared strip: %v", err)
	}
}

// TestLinePassRewritesAFileWhoseCountFellBelowItsRegisterEntry pins the
// direction the driving register is one-sided in. A hand correction that
// retires a citation before the run leaves the file carrying fewer
// citations than the register counts, and that is the retirement the
// register absorbs downward. Refusing the file would stop the pass on
// every carrier a reported straddling range was corrected in, so the
// re-run the correction exists to enable could never write.
//
// spec: §28.1 (N8, the citation rule: a count that falls is a
// retirement and the remaining citations are still retired)
func TestLinePassRewritesAFileWhoseCountFellBelowItsRegisterEntry(t *testing.T) {
	t.Parallel()
	root := lineTree(t, "tree")
	applyLinePass(t, root, "tree-count-fallen.yaml")
	assertConverted(t, root, "pkg/spellings/dotted.go")
}

// TestLinePassRefusesAFileTheRegisterDoesNotAccountFor pins the driving
// register. The pass rewrites a file only against the count the register
// carries for it, so a carrier the enumeration missed, and a carrier
// carrying more citations than the register counts, each abort the run
// with the tree byte-identical rather than being retired against a count
// nobody measured.
//
// spec: §28.1 (N8, the citation rule: the retirement ranges over the
// enumerated population)
func TestLinePassRefusesAFileTheRegisterDoesNotAccountFor(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		register string
		reason   string
	}{
		{"no count for the carrier", "tree-no-count.yaml", "carries no count"},
		{"a count above the registered one", "tree-count-risen.yaml", "citation(s) where"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := lineTree(t, "tree")
			before := treeSnapshot(t, root)
			_, err := planLinePass(t, root, tc.register)
			if err == nil {
				t.Fatal("the line pass rewrote a file the register does not account for")
			}
			abort, ok := pass.AsAbort(err)
			if !ok {
				t.Fatalf("the register failure was not reported as a fail-closed abort: %v", err)
			}
			if !strings.Contains(abort.Reason, tc.reason) {
				t.Errorf("the abort does not state why the register refused the file: %v", abort)
			}
			if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
				t.Error("the failed run wrote to the tree")
			}
		})
	}
}

// TestTheDriverCarriesTheLinePass pins that the pass the driver runs is
// the built one, so a run of the engine over a checkout retires
// citations rather than reporting a pass that is not built.
//
// spec: §28.1 (N8, the citation rule: the retirement is performed by the
// committed tooling)
func TestTheDriverCarriesTheLinePass(t *testing.T) {
	t.Parallel()
	built := builtPasses(repoRoot(t))
	r, ok := built[scope.Line]
	if !ok {
		t.Fatal("the driver carries no line pass")
	}
	if r.Pass() != scope.Line {
		t.Errorf("the built pass names the %s write domain", r.Pass())
	}
}

// TestLinePassFailsACitationWhoseHeadParenthesisNeverCloses pins the
// fourth fail-and-report case. A citation whose head opened a
// parenthesis that nothing closes inside its bounds cannot be replaced
// by an anchor without stranding the carrier's closing parenthesis and
// the prose between them, so the pass reports the punctuation for hand
// correction.
//
// spec: §28.1 (N8, the citation rule: a citation that cannot be retired
// mechanically is reported rather than converted against a guess)
func TestLinePassFailsACitationWhoseHeadParenthesisNeverCloses(t *testing.T) {
	t.Parallel()
	root := lineTree(t, "fail/unbalanced")
	before := treeSnapshot(t, root)
	_, err := planLinePass(t, root, "fail-unbalanced.yaml")
	if err == nil {
		t.Fatal("the line pass converted a citation with an unpaired parenthesis")
	}
	abort, ok := pass.AsAbort(err)
	if !ok {
		t.Fatalf("the unpaired parenthesis was not reported as a fail-closed abort: %v", err)
	}
	if !strings.Contains(abort.Reason, "parenthesis") {
		t.Errorf("the abort does not name the parenthesis: %v", abort)
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the failed run wrote to the tree")
	}
}

// TestTheServedArtifactsAreWritableCarriersOfTheTrackedTree pins the set
// the strip rule ranges over and that each member is a file the pass can
// reach. A served artifact outside the write domain would leave its
// citations with no route out of the population, because the strip is
// the only route they have.
//
// spec: §28.1 (N8, the citation rule: every carrier has a route to a
// zero count)
func TestTheServedArtifactsAreWritableCarriersOfTheTrackedTree(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	read := scope.DirReader(root)
	served := line.ServedArtifacts()
	want := []string{
		"pkg/chart/values/values.go",
		"pkg/gateway/externalapi/openapi/openapi.json",
		"pkg/gateway/mcpfabric/mcptools/mcptools.go",
	}
	if !sameStrings(served, want) {
		t.Fatalf("the served client artifacts are %v, want %v", served, want)
	}
	for _, target := range served {
		writable, err := scope.Writable(scope.Line, target, read)
		if err != nil {
			t.Fatalf("write domain for %s: %v", target, err)
		}
		if !writable {
			t.Errorf("the line pass cannot write the served artifact %s", target)
		}
	}
}

// fixtureNamePass holds the name pass fixtures: the shared
// specification that declares the identifier space every substitution is
// held to, the tree the pass runs over, the expected content of every
// file it rewrites, the trees whose carriers the pass fails on, and the
// registers that drive each run. The reserved phrases the cases need sit
// in those files rather than in a Go string literal here, for the reason
// fixtureCitations states: testdata/ is outside the read domain of every
// pass and every gate, so no gate reports this package's own input.
const fixtureNamePass = "testdata/namepass"

// nameTree assembles the tree one name pass case runs over, which is the
// shared specification fixture plus the case's own carriers.
func nameTree(t *testing.T, carriers string) string {
	t.Helper()
	root := t.TempDir()
	copyTreeInto(t, filepath.Join(fixtureNamePass, "spec"), root)
	copyTreeInto(t, filepath.Join(fixtureNamePass, carriers), root)
	return root
}

// nameRewriter returns the name pass over the tree at root, driven by
// the named register fixture.
func nameRewriter(t *testing.T, root, register string) *name.Rewriter {
	t.Helper()
	r := name.New(scope.DirLister(root), scope.DirReader(root))
	if err := r.LoadRegister(filepath.Join(fixtureNamePass, "registers", register)); err != nil {
		t.Fatalf("load the name pass register %s: %v", register, err)
	}
	return r
}

// applyNamePass runs the name pass over the tree at root and returns the
// applied diff.
func applyNamePass(t *testing.T, root, register string) pass.Diff {
	t.Helper()
	h := pass.NewHarnessOver(scope.DirLister(root), scope.DirReader(root), dirWriterFor(root))
	diff, err := h.Apply(context.Background(), nameRewriter(t, root, register))
	if err != nil {
		t.Fatalf("apply the name pass: %v", err)
	}
	return diff
}

// planNamePass runs the name pass over the tree at root without writing,
// and returns the error a fail-closed case expects.
func planNamePass(t *testing.T, root, register string) (pass.Diff, error) {
	t.Helper()
	h := pass.NewHarnessOver(scope.DirLister(root), scope.DirReader(root), dirWriterFor(root))
	return h.Plan(context.Background(), nameRewriter(t, root, register))
}

// applyNamePassErr runs the name pass over the tree at root through the
// writing path and returns the error a fail-closed case expects. The
// harness has a writer, so a run that raised its abort after a write
// rather than before one would leave the tree changed.
func applyNamePassErr(t *testing.T, root, register string) (pass.Diff, error) {
	t.Helper()
	h := pass.NewHarnessOver(scope.DirLister(root), scope.DirReader(root), dirWriterFor(root))
	return h.Apply(context.Background(), nameRewriter(t, root, register))
}

// assertSubstituted compares one rewritten carrier against the expected
// content held beside the fixture tree.
func assertSubstituted(t *testing.T, root, target string) {
	t.Helper()
	after := readFixtureFile(t, filepath.Join(root, filepath.FromSlash(target)))
	want := readFixtureFile(t, filepath.Join(fixtureNamePass, "want", target))
	if after != want {
		t.Fatalf("%s after the name pass is\n%s\nwant\n%s", target, after, want)
	}
}

// TestNamePassAbortsAtAnUnregisteredSiteAndLeavesTheTreeUnmodified pins
// the fail-closed rule the whole pass rests on. A site the sense
// register does not carry aborts the run non-zero, names the file and
// the line, and leaves every carrier byte-identical, including the
// carrier whose own site the register does resolve. A default
// substitution there would read as canonical to the naming lint and to
// the identifier-resolution gate while stating the wrong mechanism, and
// no gate reads meaning.
//
// The run goes through the writing path rather than the dry run, so the
// byte-identity assertion covers a run that had a writer and a
// resolvable sibling carrier it would otherwise have rewritten. The
// abort is raised before any file is written, so no partially rewritten
// tree is left behind.
//
// spec: §28.1 (N3, the naming law: a bare reserved noun phrase is
// replaced by the identifier its site denotes)
func TestNamePassAbortsAtAnUnregisteredSiteAndLeavesTheTreeUnmodified(t *testing.T) {
	t.Parallel()
	root := nameTree(t, "fail/unregistered")
	before := treeSnapshot(t, root)
	_, err := applyNamePassErr(t, root, "fail-unregistered.yaml")
	if err == nil {
		t.Fatal("the name pass returned no error at an unregistered site")
	}
	abort, ok := pass.AsAbort(err)
	if !ok {
		t.Fatalf("the failure is not a fail-closed abort: %v", err)
	}
	if abort.Path != "pkg/carrier/unregistered.go" || abort.Line != 5 {
		t.Errorf("the abort names %s line %d, want pkg/carrier/unregistered.go line 5", abort.Path, abort.Line)
	}
	if !strings.Contains(abort.Reason, "fail-unregistered.yaml") {
		t.Errorf("the abort does not name the register: %v", abort)
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the aborted run wrote to the tree")
	}
}

// TestNamePassSubstitutesTheIdentifierEachSiteResolvesTo pins the
// resolved substitution across the identifier classes the register draws
// from. The specification carrier resolves to a link identifier rather
// than to one of the conversations that link carries, because the
// sentence describes the connection, and collapsing it onto a channel
// would narrow a security-normative statement.
//
// spec: §28.1 (N3, the naming law: a site is replaced by the identifier
// the register resolves it to, drawn from the whole identifier space)
func TestNamePassSubstitutesTheIdentifierEachSiteResolvesTo(t *testing.T) {
	t.Parallel()
	root := nameTree(t, "tree")
	applyNamePass(t, root, "tree.yaml")
	for _, tc := range []struct {
		sense  string
		target string
	}{
		{"a link identifier", "spec/13_security-model.md"},
		{"a channel identifier in a schema description", "schemas/lenny-adapter.schema.json"},
	} {
		t.Run(tc.sense, func(t *testing.T) {
			assertSubstituted(t, root, tc.target)
		})
	}
}

// TestNamePassFailsAnEntryNamingAnUndeclaredIdentifier pins that the
// substitution is held to the identifier space the specification
// declares. A misspelled entry would land as a canonical-looking pointer
// to a mechanism that exists nowhere, which the naming lint reads as
// clean and the identifier-resolution gate reads as one spelling of one
// name.
//
// spec: §28.1 (N3, the naming law: an identifier the specification
// declares is what replaces a site)
func TestNamePassFailsAnEntryNamingAnUndeclaredIdentifier(t *testing.T) {
	t.Parallel()
	root := nameTree(t, "fail/undeclared")
	before := treeSnapshot(t, root)
	_, err := planNamePass(t, root, "fail-undeclared.yaml")
	if err == nil {
		t.Fatal("the name pass accepted an entry naming an undeclared identifier")
	}
	if !strings.Contains(err.Error(), "CH-NOSUCHCONVERSATION") {
		t.Errorf("the failure does not name the undeclared identifier: %v", err)
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the failed run wrote to the tree")
	}
}

// TestNamePassFailsAnUndeclaredIdentifierCarriedOnlyByAReplacement pins
// that the declaration check ranges over the text the pass writes rather
// than over the entry's identifier list alone. A replacement is written
// at the site verbatim and the schema check requires only that it carry
// each identifier the entry names, so an entry whose replacement adds a
// further spelling would otherwise land that spelling in the tree, where
// the naming lint reads no reserved phrase and the
// identifier-resolution gate reads one spelling of a name nothing else
// carries.
//
// spec: §28.1 (N3, the naming law: an identifier the specification
// declares is what replaces a site)
func TestNamePassFailsAnUndeclaredIdentifierCarriedOnlyByAReplacement(t *testing.T) {
	t.Parallel()
	root := nameTree(t, "tree")
	before := treeSnapshot(t, root)
	_, err := planNamePass(t, root, "fail-undeclared-in-replacement.yaml")
	if err == nil {
		t.Fatal("the name pass accepted a replacement carrying an undeclared identifier")
	}
	if !strings.Contains(err.Error(), "CH-BOGUS") {
		t.Errorf("the failure does not name the undeclared identifier: %v", err)
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the failed run wrote to the tree")
	}
}

// TestNamePassReadsNoSiteInADetachedGoHeaderCommentBlock pins the Go
// position rule against the layout a build constraint forces. A header
// prose block behind the blank line the constraint requires is attached
// to no declaration, so it is no doc comment and the law does not reach
// it: the run demands no register entry for the phrase it carries, even
// though that phrase wraps across two comment lines the join folds
// together, and leaves the block as the author wrote it. A pass reading
// a wider position than the law would abort at that unregistered site
// and shift the occurrence numbering the register is keyed by for the
// sites below it. The doc comment and the pinned literal of the same
// carrier are substituted in the same run, so the case pins the
// enumeration as well as the exclusion.
//
// spec: §28.1 (N3, the naming law: the Go domain is a doc comment of a
// tracked Go file)
func TestNamePassReadsNoSiteInADetachedGoHeaderCommentBlock(t *testing.T) {
	t.Parallel()
	const target = "tests/tier11_docs/route_test.go"
	root := nameTree(t, "tree")
	applyNamePass(t, root, "tree.yaml")
	assertSubstituted(t, root, target)
	before := readFixtureFile(t, filepath.Join(fixtureNamePass, "tree", target))
	after := readFixtureFile(t, filepath.Join(root, filepath.FromSlash(target)))
	header := fixtureLine(t, before, "attaches it to no declaration")
	if !strings.Contains(after, header) {
		t.Errorf("the pass rewrote the detached header block line %q:\n%s", header, after)
	}
	literal := strings.Index(after, "LNK-GWCONTROL")
	attached := strings.Index(after, "CH-PODLIFECYCLE")
	if literal < 0 || attached < 0 {
		t.Fatalf("%s is missing a substitution after the pass:\n%s", target, after)
	}
	if literal >= attached {
		t.Errorf("the substitutions land at offsets %d and %d, so the carrier is enumerated out of source order:\n%s",
			literal, attached, after)
	}
}

// TestNamePassReadsNoSiteInAGoCommentInsideAPackageLevelLiteral pins the
// Go position rule against the second layout parser attachment misses. A
// comment written between the elements of a package-level composite
// literal is attached to nothing, because the parser attaches a doc
// group to a declaration and to a field rather than to a literal
// element, so it is no doc comment and the law does not reach it. The
// case pins the trailing comment of the same carrier the same way, and
// pins the doc comment above them as the one site of the file, so a pass
// reading a wider position would abort at a phrase no register entry
// covers.
//
// spec: §28.1 (N3, the naming law: the Go domain is a doc comment of a
// tracked Go file)
func TestNamePassReadsNoSiteInAGoCommentInsideAPackageLevelLiteral(t *testing.T) {
	t.Parallel()
	const target = "pkg/catalog/catalog.go"
	root := nameTree(t, "tree")
	applyNamePass(t, root, "tree.yaml")
	assertSubstituted(t, root, target)
	before := readFixtureFile(t, filepath.Join(fixtureNamePass, "tree", target))
	after := readFixtureFile(t, filepath.Join(root, filepath.FromSlash(target)))
	if !strings.Contains(after, "CH-PODLIFECYCLE") {
		t.Errorf("%s carries no substitution after the pass:\n%s", target, after)
	}
	for _, open := range []string{"literal and names the", `Name: "minimal"`} {
		line := fixtureLine(t, before, open)
		if !strings.Contains(after, line) {
			t.Errorf("the pass rewrote the comment %q:\n%s", line, after)
		}
	}
}

// TestNamePassWritesEachIdentifierOfAMultiIdentifierEntry pins the site
// whose sentence denotes two mechanisms. The entry records the
// replacement each identifier sits in, so both are written at the
// positions the entry gives rather than collapsed onto one of them.
//
// spec: §28.1 (N3, the naming law: a site denoting more than one
// mechanism names each of them)
func TestNamePassWritesEachIdentifierOfAMultiIdentifierEntry(t *testing.T) {
	t.Parallel()
	const target = "spec/16_observability.md"
	root := nameTree(t, "tree")
	applyNamePass(t, root, "tree.yaml")
	assertSubstituted(t, root, target)
	after := readFixtureFile(t, filepath.Join(root, filepath.FromSlash(target)))
	for _, id := range []string{"LNK-POD-GRPC", "CH-LLMPROXY"} {
		if !strings.Contains(after, id) {
			t.Errorf("%s does not carry %s after the pass:\n%s", target, id, after)
		}
	}
}

// TestNamePassWritesAGoCommentAndASchemaDescription pins that the pass
// writes the surfaces the naming lint reads beyond markdown prose, which
// are the comments of a tracked Go file and the description value of a
// schema document. It pins the one carrier position outside the law's
// domain at the same time: a phrase in a Go string literal is
// operator-facing text this migration leaves as it stands.
//
// spec: §28.1 (N3, the naming law: the domain covers the schemas and the
// doc comments of tracked Go files)
func TestNamePassWritesAGoCommentAndASchemaDescription(t *testing.T) {
	t.Parallel()
	root := nameTree(t, "tree")
	applyNamePass(t, root, "tree.yaml")
	for _, target := range []string{"pkg/carrier/carrier.go", "schemas/lenny-adapter.schema.json"} {
		assertSubstituted(t, root, target)
	}
	before := readFixtureFile(t, filepath.Join(fixtureNamePass, "tree", "pkg/carrier/carrier.go"))
	after := readFixtureFile(t, filepath.Join(root, filepath.FromSlash("pkg/carrier/carrier.go")))
	literal := fixtureLine(t, before, "validateUsage = ")
	if !strings.Contains(after, literal) {
		t.Errorf("the pass rewrote the string literal %q:\n%s", literal, after)
	}
}

// fixtureLine returns the line of a fixture that carries the marker, so
// a case states an expectation about a reserved phrase without holding a
// copy of the phrase in this source.
func fixtureLine(t *testing.T, content, marker string) string {
	t.Helper()
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, marker) {
			return line
		}
	}
	t.Fatalf("no fixture line carries %q", marker)
	return ""
}

// TestNamePassReadsAPhraseWrappedAcrossTwoCommentLinesAsOneSite pins the
// continuation join the matcher applies before either spelling. A phrase
// wrapped across two consecutive comment lines is one site the register
// resolves and one site the pass writes, rather than two half-sites a
// line-oriented matcher reads as neither.
//
// spec: §28.1 (N3, the naming law: the matcher applies the continuation
// join before either spelling)
func TestNamePassReadsAPhraseWrappedAcrossTwoCommentLinesAsOneSite(t *testing.T) {
	t.Parallel()
	root := nameTree(t, "tree")
	applyNamePass(t, root, "tree.yaml")
	assertSubstituted(t, root, "schemas/lenny-adapter.proto")
}

// TestNamePassReadsNoSiteAcrossTwoMarkdownParagraphs pins the bound the
// matcher puts on a folded match. The one continuation join folds a
// markdown heading, list item, or emphasis run onto the paragraph line
// above it, because each of those opens with a marker the join consumes
// and each may interrupt a paragraph legally. The matcher holds a folded
// match to one comment afterwards, and the paragraph line above such a
// marker carries none, so the fold across the two is no site: the run
// neither aborts at a position that is no bare noun phrase nor splices a
// substitution over the newline, which would delete the heading or list
// marker between them and take the anchor derived from the heading with
// it. Either outcome would also shift the occurrence numbering the sense
// register is keyed by for every later site of the file.
//
// The case runs the whole fixture tree, so the carrier that legitimately
// wraps across two comment lines is substituted in the same run.
//
// spec: §28.1 (N3, the naming law: the matcher applies the comment-marker
// continuation join before either spelling)
func TestNamePassReadsNoSiteAcrossTwoMarkdownParagraphs(t *testing.T) {
	t.Parallel()
	const target = "docs/reference/wrapped-paragraph.md"
	root := nameTree(t, "tree")
	before := treeSnapshot(t, root)
	planned, err := planNamePass(t, root, "tree.yaml")
	if err != nil {
		t.Fatalf("plan the name pass: %v", err)
	}
	applied := applyNamePass(t, root, "tree.yaml")
	if membership(planned.Paths())[target] || membership(applied.Paths())[target] {
		t.Errorf("the name pass names the markdown carrier %s", target)
	}
	after := treeSnapshot(t, root)
	if after[target] != before[target] {
		t.Errorf("the markdown carrier was rewritten:\n%s", after[target])
	}
	content := readFixtureFile(t, filepath.Join(root, filepath.FromSlash(target)))
	// The exclusion is the matcher's, so the join itself still folds the
	// heading and the list item of this carrier. A join that skipped a
	// markdown carrier would enumerate a narrower wrapped population there
	// than the citation matcher, which reads every carrier under it.
	if joined, _ := citation.Join(content); strings.Count(joined, "\n") >= strings.Count(content, "\n") {
		t.Errorf("the join folded no line of the markdown carrier:\n%s", joined)
	}
	for _, marker := range []string{"\n## ", "\n* "} {
		if !strings.Contains(content, marker) {
			t.Errorf("the run consumed the line opener %q:\n%s", marker, content)
		}
	}
	assertSubstituted(t, root, "schemas/lenny-adapter.proto")
}

// TestNamePassReadsAPhraseWrappedAcrossTwoYamlCommentLinesAsOneSite
// pins the other side of the fold rule. A number sign opens a comment in
// a YAML document, so a phrase wrapped across two comment lines of a
// schema document is one site there, while the same marker opens a
// heading in a markdown document and folds nothing. A rule reading the
// marker without the carrier's format would answer one of the two wrong,
// leaving either a site no pass writes or a substitution spliced over
// markup the author wrote.
//
// spec: §28.1 (N3, the naming law: the matcher applies the comment-marker
// continuation join before either spelling)
func TestNamePassReadsAPhraseWrappedAcrossTwoYamlCommentLinesAsOneSite(t *testing.T) {
	t.Parallel()
	const target = "schemas/lenny-adapter-defaults.yaml"
	root := nameTree(t, "tree")
	applied := applyNamePass(t, root, "tree.yaml")
	if !membership(applied.Paths())[target] {
		t.Errorf("the name pass names no wrapped site in the schema carrier %s", target)
	}
	assertSubstituted(t, root, target)
}

// TestNamePassReadsNoSiteBetweenTwoMarkdownMarkupLines pins the fold
// rule against the case where both lines of the fold open behind a
// marker. Two consecutive headings and two consecutive list items each
// open behind a marker the one continuation join consumes, so the join
// folds them together, but a number sign and an asterisk are markdown
// heading and list markup rather than comment markers and the two lines
// are two separate blocks. A rule reading only the line the match opens
// on would admit the fold as one site: with a register entry the pass
// would splice the substitution over the newline and the second line's
// marker, deleting a list item or a heading and the anchor derived from
// that heading, which the anchor pass and the fragment-link gate resolve
// against; without one the run would abort fail-closed at a position
// that is no bare noun phrase and no hand correction reaches. The
// carrier therefore takes no register entry and stands byte-identical,
// while the markdown carrier that folds two comment lines of a fenced
// snippet is substituted in the same run.
//
// spec: §28.1 (N3, the naming law: the matcher applies the comment-marker
// continuation join before either spelling)
func TestNamePassReadsNoSiteBetweenTwoMarkdownMarkupLines(t *testing.T) {
	t.Parallel()
	const target = "docs/reference/wrapped-markup.md"
	root := nameTree(t, "tree")
	before := treeSnapshot(t, root)
	planned, err := planNamePass(t, root, "tree.yaml")
	if err != nil {
		t.Fatalf("plan the name pass: %v", err)
	}
	applied := applyNamePass(t, root, "tree.yaml")
	if membership(planned.Paths())[target] || membership(applied.Paths())[target] {
		t.Errorf("the name pass names the markdown carrier %s", target)
	}
	after := treeSnapshot(t, root)
	if after[target] != before[target] {
		t.Errorf("the markdown carrier was rewritten:\n%s", after[target])
	}
	content := readFixtureFile(t, filepath.Join(root, filepath.FromSlash(target)))
	for _, marker := range []string{"\n## Channels of the runtime", "\n* channel entry of a session"} {
		if !strings.Contains(content, marker) {
			t.Errorf("the run consumed the markup line %q:\n%s", marker, content)
		}
	}
	assertSubstituted(t, root, "docs/reference/snippets.md")
}

// TestNamePassSubstitutesAPhraseWrappedInsideAMarkdownComment pins the
// join to one join over every carrier. A markdown document carries
// comment lines wherever it holds a fenced snippet, and a phrase wrapped
// across two of them is one site there exactly as it is in the file the
// snippet was taken from. A matcher that read a markdown carrier under a
// join of its own would read the two lines as unjoined text, so the
// occurrence would be a site no pass writes and no lint reports, while
// the citation matcher over the same file read one wrapped citation
// across a comparable wrap.
//
// spec: §28.1 (N3, the naming law: the matcher applies the comment-marker
// continuation join before either spelling)
func TestNamePassSubstitutesAPhraseWrappedInsideAMarkdownComment(t *testing.T) {
	t.Parallel()
	const target = "docs/reference/snippets.md"
	root := nameTree(t, "tree")
	planned, err := planNamePass(t, root, "tree.yaml")
	if err != nil {
		t.Fatalf("plan the name pass: %v", err)
	}
	applied := applyNamePass(t, root, "tree.yaml")
	if !membership(planned.Paths())[target] || !membership(applied.Paths())[target] {
		t.Errorf("the name pass names no wrapped site in the markdown carrier %s", target)
	}
	assertSubstituted(t, root, target)
}

// TestNamePassReadsAPhraseWrappedAfterItsHyphenAsOneSite pins the
// separator the matcher admits between the reserved word and the head
// noun. A wrap falling immediately after the hyphen of the compound
// spelling leaves the hyphen and the join byte standing together, and a
// matcher admitting the join byte alone matches neither line, so the
// occurrence is written by no pass and read by no lint. The case pins
// the enumeration as well as the substitution: the wrapped site opens
// the file and an unwrapped site follows it, and the two resolve to
// different identifiers, so a run that read the wrap as no site would
// write the first identifier at the second site.
//
// spec: §28.1 (N3, the naming law: the matcher applies the comment-marker
// continuation join before either spelling)
func TestNamePassReadsAPhraseWrappedAfterItsHyphenAsOneSite(t *testing.T) {
	t.Parallel()
	root := nameTree(t, "tree")
	applyNamePass(t, root, "tree.yaml")
	assertSubstituted(t, root, "pkg/carrier/notify.go")
}

// TestNamePassLeavesAMarkdownAnchorIdentifierUnmodified pins the one
// population the matcher excludes. A kramdown anchor attribute and the
// fragment of an intra-repo markdown link are addressable link targets
// rather than prose, so neither takes a register entry and neither is
// rewritten; rewriting one breaks every inbound link, including the
// untracked links this repository cannot see. No assertion about the
// naming lint is made here, because the lint does not exist yet.
//
// spec: §28.1 (N3, the naming law: a markdown anchor identifier is
// outside the matcher)
func TestNamePassLeavesAMarkdownAnchorIdentifierUnmodified(t *testing.T) {
	t.Parallel()
	const anchors = "docs/reference/glossary.md"
	root := nameTree(t, "tree")
	before := treeSnapshot(t, root)
	planned, err := planNamePass(t, root, "tree.yaml")
	if err != nil {
		t.Fatalf("plan the name pass: %v", err)
	}
	applied := applyNamePass(t, root, "tree.yaml")
	if membership(planned.Paths())[anchors] || membership(applied.Paths())[anchors] {
		t.Errorf("the name pass names the anchor carrier %s", anchors)
	}
	if got := treeSnapshot(t, root); got[anchors] != before[anchors] {
		t.Errorf("the anchor carrier was rewritten:\n%s", got[anchors])
	}
	assertSubstituted(t, root, "spec/13_security-model.md")
}

// TestJoinFoldsEveryCarrierUnderOneRule pins the continuation join the
// reserved-phrase matcher and the citation matcher share. There is one
// join and it reads every carrier, so the two matchers enumerate one
// wrapped population and the naming lint is built against the rule the
// pass writes under. The marker set is the line comment of the slash
// dialects, the number-sign dialects, the double-hyphen dialects, and
// the leading star of a block comment, and a wrap with nothing but
// indentation on the continuation line is no continuation at all.
//
// spec: §28.1 (N3, the naming law: the matcher applies the
// comment-marker continuation join before either spelling)
func TestJoinFoldsEveryCarrierUnderOneRule(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		content string
		want    bool
	}{
		{"slash", "// one\n// two\n", true},
		{"hash", "# one\n# two\n", true},
		{"dash", "-- one\n-- two\n", true},
		{"block star", "/* one\n * two */\n", true},
		{"markdown heading", "one\n## two\n", true},
		{"unmarked", "one\n   two\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			joined, offsets := citation.Join(tc.content)
			if folded := strings.Count(joined, "\n") < strings.Count(tc.content, "\n"); folded != tc.want {
				t.Errorf("the join over %q returns %q, want folded=%t", tc.content, joined, tc.want)
			}
			if len(offsets) != len(joined)+1 {
				t.Errorf("the join over %q returns %d offsets for %d bytes", tc.content, len(offsets), len(joined))
			}
		})
	}
}

// TestMarkdownAnchorIdentifiersAreOneSharedExclusion pins where the
// anchor-identifier exclusion lives and how far it reaches. The naming
// law gives the pass that writes the reserved-phrase sites and the lint
// that reads them one exclusion, so it sits on the shared file-domain
// surface beside the carrier predicate rather than inside either
// consumer; an exclusion restated in the lint would report a site the
// pass cannot write. Its extent inside a markdown link is the fragment,
// leaving the path half of the destination a site the register resolves.
//
// spec: §28.1 (N3, the naming law: the lint reads the same
// markdown-anchor-identifier exclusion the name pass reads)
func TestMarkdownAnchorIdentifiersAreOneSharedExclusion(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		// fixture is the specimen file, read rather than written here,
		// so the reserved phrases it carries stay out of this source.
		fixture string
		// open and endBefore delimit the one span the case expects,
		// with open empty when the case expects none.
		open      string
		endBefore string
	}{
		{
			name:      "the kramdown attribute is excluded whole",
			fixture:   "kramdown.md",
			open:      "{:",
			endBefore: "\n",
		},
		{
			name:      "a link is excluded from its fragment alone",
			fixture:   "link-with-fragment.md",
			open:      "#",
			endBefore: ")",
		},
		{
			name:    "a link carrying no fragment is excluded nowhere",
			fixture: "link-without-fragment.md",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			text := readFixtureFile(t, filepath.Join(fixtureNamePass, "anchors", tc.fixture))
			var want [][2]int
			if tc.open != "" {
				want = [][2]int{spanFrom(t, text, tc.open, tc.endBefore)}
			}
			got := scope.MarkdownAnchorIdentifiers(text)
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("the excluded spans of %s are %v, want %v", tc.fixture, got, want)
			}
		})
	}
}

// spanFrom returns the half-open byte span of a fixture text that opens
// at the marker and ends before the next occurrence of the terminator,
// so a case states an expected span without holding a copy of the text
// it covers in this source.
func spanFrom(t *testing.T, text, open, endBefore string) [2]int {
	t.Helper()
	lo := strings.Index(text, open)
	if lo < 0 {
		t.Fatalf("no fixture text opens at %q", open)
	}
	rest := strings.Index(text[lo:], endBefore)
	if rest < 0 {
		t.Fatalf("no fixture text after %q reaches %q", open, endBefore)
	}
	return [2]int{lo, lo + rest}
}

// TestGoDocCommentPositionIsOneSharedRule pins where the in-file
// position the naming law governs in a Go carrier lives and which
// layouts it reaches. The position is the doc comment, which is the
// group the parser attaches to the package clause or to a declaration.
// The pass that writes the reserved-phrase sites of a Go file and the
// lint that reads them are held to one statement of the position, so it
// sits on the shared file-domain surface beside the carrier predicate
// and the anchor exclusion rather than inside either consumer.
//
// Every other comment stands outside it and takes no register entry: a
// header block behind the blank line a build constraint forces, a
// comment between the elements of a package-level composite literal, a
// comment trailing the code it annotates, and an implementation comment
// inside a function body. A position wider than the law's would demand a
// register entry at a site the law does not govern and shift the
// occurrence numbering the register is keyed by for every later site of
// the file.
//
// spec: §28.1 (N3, the naming law: the Go domain is a doc comment of a
// tracked Go file)
func TestGoDocCommentPositionIsOneSharedRule(t *testing.T) {
	t.Parallel()
	const src = `//go:build tier11_docs

// A header prose block the build constraint separates from the package
// clause.

package docs

// Entry documents a declaration.
type Entry struct {
	// Name documents a field.
	Name string
}

var entries = []Entry{
	{
		// A comment between the elements of a package-level literal.
		Name: "full", // A comment trailing the code it annotates.
	},
}

func Names() int {
	// An implementation comment inside a function body.
	return len(entries)
}
`
	admitted := map[string]bool{}
	spans, err := scope.GoDocCommentSpans("pkg/catalog/catalog.go", src)
	if err != nil {
		t.Fatalf("read the doc comments of the carrier: %v", err)
	}
	for _, s := range spans {
		admitted[src[s[0]:s[1]]] = true
	}
	for _, tc := range []struct {
		name string
		text string
		want bool
	}{
		{"a header block behind a build constraint", "// A header prose block the build constraint separates from the package", false},
		{"a comment documenting a declaration", "// Entry documents a declaration.", true},
		{"a comment documenting a field", "// Name documents a field.", true},
		{"a comment inside a package-level literal", "// A comment between the elements of a package-level literal.", false},
		{"a comment trailing the code it annotates", "// A comment trailing the code it annotates.", false},
		{"an implementation comment inside a function body", "// An implementation comment inside a function body.", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if admitted[tc.text] != tc.want {
				t.Errorf("the position rule reads %q as admitted=%t, want %t", tc.text, admitted[tc.text], tc.want)
			}
		})
	}
}

// TestGoDocCommentPositionFailsACarrierTheParserCannotRead pins the
// fail-closed half of the shared position rule. A Go file the parser
// cannot read has no comment position at all, so reading it whole would
// rewrite the implementation comments and the operator-facing literals
// the law does not govern, and skipping it would leave every governed
// comment it carries with no writer.
//
// spec: §28.1 (N3, the naming law: the Go domain is a doc comment of a
// tracked Go file)
func TestGoDocCommentPositionFailsACarrierTheParserCannotRead(t *testing.T) {
	t.Parallel()
	_, err := scope.GoDocCommentSpans("pkg/carrier/carrier.go", "package carrier\n\nfunc Ack( {\n")
	if err == nil {
		t.Fatal("the position rule read a carrier the parser cannot parse")
	}
	if !strings.Contains(err.Error(), "pkg/carrier/carrier.go") {
		t.Errorf("the failure does not name the carrier: %v", err)
	}
}

// TestNamePassSubstitutesInTheLinkPathBesideAnExcludedFragment pins the
// extent of the anchor-identifier exclusion inside a markdown link. The
// naming law places the fragment of an intra-repo link outside the
// matcher and leaves the rest of the destination inside it, so the path
// half is a site the sense register resolves and the substitution
// carries into it while the fragment beside it in the same destination
// is left as it stands with no entry. An exclusion spanning the whole
// destination would leave a phrase in the path with no pass able to
// write it, which is a site the naming lint reports and nothing can
// clear.
//
// spec: §28.1 (N3, the naming law: a markdown anchor identifier is
// outside the matcher, which for a link is its fragment)
func TestNamePassSubstitutesInTheLinkPathBesideAnExcludedFragment(t *testing.T) {
	t.Parallel()
	const target = "docs/reference/links.md"
	root := nameTree(t, "tree")
	applied := applyNamePass(t, root, "tree.yaml")
	if !membership(applied.Paths())[target] {
		t.Errorf("the name pass does not name the link carrier %s", target)
	}
	assertSubstituted(t, root, target)
}

// TestNamePassLeavesAGeneratedCarrierUnmodified pins that a file the
// per-file generated-artifact rule selects is left as it stands, because
// its route out of the population is the regeneration of its source.
//
// Both carriers the case runs over are inside this pass's own carrier
// domain and hold a reserved-phrase site in a doc comment the register
// carries no entry for, so the exclusion is what keeps the run from
// aborting at them. A generated file outside the carrier domain would
// pin nothing here: the domain filter alone would leave it unread.
// One carrier is selected through the producer-output disjunct and
// through its header marker, as the proto stubs are; the other sits
// under no producer's output set, so the header marker is the only
// disjunct that reaches it.
//
// spec: §28.1 (N3, the naming law: a generated artifact leaves the
// population through its producer)
func TestNamePassLeavesAGeneratedCarrierUnmodified(t *testing.T) {
	t.Parallel()
	generated := []string{
		"pkg/proto/adapter/v1/lenny-adapter.pb.go",
		"pkg/apis/lenny/v1alpha1/zz_generated.deepcopy.go",
	}
	root := nameTree(t, "tree")
	before := treeSnapshot(t, root)
	planned, err := planNamePass(t, root, "tree.yaml")
	if err != nil {
		t.Fatalf("plan the name pass: %v", err)
	}
	applied := applyNamePass(t, root, "tree.yaml")
	inPlan, inApplied := membership(planned.Paths()), membership(applied.Paths())
	after := treeSnapshot(t, root)
	for _, target := range generated {
		if before[target] == "" {
			t.Fatalf("the fixture tree carries no %s", target)
		}
		if inPlan[target] || inApplied[target] {
			t.Errorf("the name pass names the generated carrier %s", target)
		}
		if after[target] != before[target] {
			t.Errorf("the generated carrier was rewritten:\n%s", after[target])
		}
	}
	assertSubstituted(t, root, "spec/13_security-model.md")
}

// TestNamePassLeavesEveryWriteExcludedCarrierByteIdentical pins the
// write exclusion of this class, which is wider than the citation
// passes' by the three root build and queue records: a reserved phrase
// in one of them is part of what was written at the time. Each member of
// every excluded group is left exactly as it was written and appears in
// neither the dry-run output nor the applied diff, while an equivalent
// site in an ordinary carrier in the same run is substituted.
//
// spec: §28.1 (N3, the naming law: the excluded records are outside the
// writable population)
func TestNamePassLeavesEveryWriteExcludedCarrierByteIdentical(t *testing.T) {
	t.Parallel()
	root := nameTree(t, "tree")
	excluded := []string{
		"proposals/0001_example.md",
		"BUILD-GAPS.md",
		"TEST-GAPS.md",
		"gateway-runtime-comms.md",
		"gateway-runtime-comms-remediation.md",
		"BUILD-PLAN.md",
		"BUILD-PROGRESS.md",
		"PROPOSAL-QUEUE.md",
	}
	before := treeSnapshot(t, root)
	planned, err := planNamePass(t, root, "tree.yaml")
	if err != nil {
		t.Fatalf("plan the name pass: %v", err)
	}
	applied := applyNamePass(t, root, "tree.yaml")
	after := treeSnapshot(t, root)
	inPlan, inApplied := membership(planned.Paths()), membership(applied.Paths())
	for _, target := range excluded {
		if before[target] == "" {
			t.Fatalf("the fixture tree carries no %s", target)
		}
		if after[target] != before[target] {
			t.Errorf("%s was rewritten:\n%s", target, after[target])
		}
		if inPlan[target] {
			t.Errorf("the dry-run output names the excluded %s", target)
		}
		if inApplied[target] {
			t.Errorf("the applied diff names the excluded %s", target)
		}
	}
	assertSubstituted(t, root, "spec/13_security-model.md")
}

// TestNamePassRejectsAMalformedOrMissingSenseRegister pins that the pass
// refuses to run rather than reporting the zero substitutions of a
// completed migration. A register that loaded as empty would abort at
// the first site in the tree, which reads as a register nobody seeded,
// and over an already-rewritten tree it would report a migration it
// never performed.
//
// spec: §28.1 (N3, the naming law: the removal is driven by the register
// of senses)
func TestNamePassRejectsAMalformedOrMissingSenseRegister(t *testing.T) {
	t.Parallel()
	root := nameTree(t, "tree")
	for _, tc := range []struct {
		name     string
		register string
	}{
		{"a missing register", "absent.yaml"},
		{"a malformed register", "malformed.yaml"},
		{"a register with no entries block", "no-entries-block.yaml"},
		{"a register with no entry", "empty-entries.yaml"},
		{"a register of another kind", "wrong-kind.yaml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := name.New(scope.DirLister(root), scope.DirReader(root))
			if err := r.LoadRegister(filepath.Join(fixtureNamePass, "registers", tc.register)); err == nil {
				t.Fatalf("the name pass loaded %s", tc.name)
			}
			h := pass.NewHarnessOver(scope.DirLister(root), scope.DirReader(root), dirWriterFor(root))
			if _, err := h.Plan(context.Background(), r); err == nil {
				t.Error("a pass with no register loaded reported a plan")
			}
		})
	}
}

// TestNamePassDryRunOutputEqualsTheAppliedDiff pins the entry criterion
// for applying the pass: what the dry run reports is what the apply
// writes, so a reviewer reads the whole substitution before any file
// moves.
//
// spec: §28.1 (N3, the naming law: the removal is reviewed before it is
// applied)
func TestNamePassDryRunOutputEqualsTheAppliedDiff(t *testing.T) {
	t.Parallel()
	root := nameTree(t, "tree")
	before := treeSnapshot(t, root)
	planned, err := planNamePass(t, root, "tree.yaml")
	if err != nil {
		t.Fatalf("plan the name pass: %v", err)
	}
	if len(planned.Files) == 0 {
		t.Fatal("the dry run reports no work over the fixture tree")
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the dry run wrote to the tree")
	}
	applied := applyNamePass(t, root, "tree.yaml")
	if !planned.Equal(applied) {
		t.Fatalf("the applied diff differs from the dry run: %v vs %v", planned.Paths(), applied.Paths())
	}
}

// TestNamePassRejectsEverySenseEntrySchemaDefect pins the entry schema
// the substitution rests on: a site key of file and occurrence, declared
// once, and one or more identifiers with the position of each recorded.
// An entry naming more than one identifier without the replacement text
// they sit in states no position for either, and a replacement that
// omits an identifier the entry names never writes it. An identifier
// standing in the replacement only as the prefix of a longer identifier
// is omitted in the same sense, because it reaches no position of its
// own and the site's sense would be collapsed onto the longer one. A substitution
// that is itself a site of the class leaves the site standing after the
// pass has run over the file, where the naming lint reads it and no
// further pass writes it.
//
// spec: §28.1 (N3, the naming law: each identifier a site denotes is
// written at the position the register records)
func TestNamePassRejectsEverySenseEntrySchemaDefect(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		defect   string
		register string
		names    string
	}{
		{"an entry with no file", "invalid-no-file.yaml", "carries no file"},
		{"an occurrence below one", "invalid-occurrence.yaml", "numbered from one"},
		{"an entry naming no identifier", "invalid-no-identifier.yaml", "names no identifier"},
		{"a site declared twice", "invalid-duplicate.yaml", "declared twice"},
		{"two identifiers with no recorded position", "invalid-position-unstated.yaml", "position of each is unstated"},
		{"a replacement omitting an identifier", "invalid-replacement-omits-identifier.yaml", "CH-LLMPROXY"},
		{"a substitution that is itself a site", "invalid-reserved-replacement.yaml", "the class the pass removes"},
		{"a replacement carrying an identifier only as the prefix of a longer one", "invalid-replacement-carries-a-prefix-alone.yaml", "names LNK-POD and"},
	} {
		t.Run(tc.defect, func(t *testing.T) {
			t.Parallel()
			r := name.New(scope.DirLister(fixtureNamePass), scope.DirReader(fixtureNamePass))
			err := r.LoadRegister(filepath.Join(fixtureNamePass, "registers", tc.register))
			if err == nil {
				t.Fatalf("the name pass loaded a register carrying %s", tc.defect)
			}
			if !strings.Contains(err.Error(), tc.names) {
				t.Errorf("the failure does not name the defect %q: %v", tc.names, err)
			}
		})
	}
}

// TestNamePassFailsASubstitutionThatComposesASiteAgain pins the check
// over the rewritten text. A substitution can compose a reserved noun
// phrase out of the identifier it writes and the carrier text beside the
// site, which leaves the file carrying a site after the pass has run
// over it: the naming lint reads it, no further pass writes it, and a
// second run resolves it against an entry keyed for another occurrence.
// The site is reported for hand correction instead, with the tree left
// byte-identical.
//
// spec: §28.1 (N3, the naming law: no bare reserved noun phrase stands
// in a file the pass has written)
func TestNamePassFailsASubstitutionThatComposesASiteAgain(t *testing.T) {
	t.Parallel()
	root := nameTree(t, "fail/composed")
	before := treeSnapshot(t, root)
	_, err := planNamePass(t, root, "fail-composed.yaml")
	if err == nil {
		t.Fatal("the name pass wrote a substitution that composes a site again")
	}
	abort, ok := pass.AsAbort(err)
	if !ok {
		t.Fatalf("the failure is not a fail-closed abort: %v", err)
	}
	if abort.Path != "pkg/carrier/carrier.go" {
		t.Errorf("the abort names %s, want pkg/carrier/carrier.go", abort.Path)
	}
	if !strings.Contains(abort.Reason, "hand correction") {
		t.Errorf("the abort does not report the site for hand correction: %v", abort)
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the failed run wrote to the tree")
	}
}

// TestNamePassReadsNoSiteInACarrierOutsideTheNamingLawDomain pins the
// carrier domain of this pass, which is narrower than the tracked tree
// the write exclusions leave. A chart values file carries the phrase in
// operator-facing configuration text the law does not govern, so it is
// neither a site the pass substitutes at nor a site the fail-closed rule
// aborts on, while an ordinary carrier in the same run is substituted. A
// pass ranging over the whole write domain would abort on it and on
// every chart template, scaffold template, and runtime SDK source
// beside it, none of which the register is ever seeded for.
//
// spec: §28.1 (N3, the naming law: the prohibition covers the
// specification, the documentation, the schemas, the doc comments of
// tracked Go files, and the tracked root-level markdown documents)
func TestNamePassReadsNoSiteInACarrierOutsideTheNamingLawDomain(t *testing.T) {
	t.Parallel()
	const outside = "charts/lenny/values.yaml"
	root := nameTree(t, "tree")
	before := treeSnapshot(t, root)
	if before[outside] == "" {
		t.Fatalf("the fixture tree carries no %s", outside)
	}
	planned, err := planNamePass(t, root, "tree.yaml")
	if err != nil {
		t.Fatalf("plan the name pass: %v", err)
	}
	applied := applyNamePass(t, root, "tree.yaml")
	if membership(planned.Paths())[outside] || membership(applied.Paths())[outside] {
		t.Errorf("the name pass names the out-of-domain carrier %s", outside)
	}
	if got := treeSnapshot(t, root); got[outside] != before[outside] {
		t.Errorf("the out-of-domain carrier was rewritten:\n%s", got[outside])
	}
	assertSubstituted(t, root, "spec/13_security-model.md")
}

// TestNamePassWritesADocCommentOfAGoFileUnderSdks pins that the carrier
// domain follows the file's language rather than its directory: a
// tracked Go file under sdks/ carries the prohibition in its doc
// comments like any other tracked Go file, including a test file. The
// runtime SDK's Go sources hold wrapped doc-comment sites, and a domain
// that placed them outside would leave them with a reader in the naming
// lint and no writer in this pass.
//
// spec: §28.1 (N3, the naming law: the prohibition covers the doc
// comment of a tracked Go file)
func TestNamePassWritesADocCommentOfAGoFileUnderSdks(t *testing.T) {
	t.Parallel()
	const target = "sdks/runtime/go/runtime/lifecycle_test.go"
	if !scope.ReservedPhraseCarrier(target) {
		t.Fatalf("%s is outside the reserved-phrase carrier domain", target)
	}
	root := nameTree(t, "tree")
	applied := applyNamePass(t, root, "tree.yaml")
	if !membership(applied.Paths())[target] {
		t.Errorf("the applied diff does not name %s", target)
	}
	assertSubstituted(t, root, target)
}

// TestNamePassReadsNoSiteInAGoCommentOutsideADocComment pins the in-file
// position the law governs in a Go carrier. The naming lint reads the
// doc comment of a tracked Go file, so an implementation comment inside
// a function body is outside the population on both sides: the pass
// neither demands a register entry for it nor rewrites it, and the
// doc comment of the same file is substituted in the same run.
//
// spec: §28.1 (N3, the naming law: the Go domain is the doc comment of a
// tracked Go file)
func TestNamePassReadsNoSiteInAGoCommentOutsideADocComment(t *testing.T) {
	t.Parallel()
	const target = "pkg/carrier/carrier.go"
	root := nameTree(t, "tree")
	applyNamePass(t, root, "tree.yaml")
	assertSubstituted(t, root, target)
	before := readFixtureFile(t, filepath.Join(fixtureNamePass, "tree", target))
	after := readFixtureFile(t, filepath.Join(root, filepath.FromSlash(target)))
	implementation := fixtureLine(t, before, "implementation comment here")
	if !strings.Contains(after, implementation) {
		t.Errorf("the pass rewrote the implementation comment %q:\n%s", implementation, after)
	}
}

// TestNamePassFailsAnEntryNamingAnIdentifierNoRegisterDeclares pins
// where the identifier space is read from. A section outside the
// communication-channels registers cites an identifier in a table
// routinely, and a citation is not a declaration, so an entry whose
// spelling appears only at such a site fails rather than being written.
// Reading a citation as a declaration would pass exactly the misspelled
// entry this check exists to refuse.
//
// spec: §28.1 (N3, the naming law: an identifier the specification
// declares is what replaces a site)
func TestNamePassFailsAnEntryNamingAnIdentifierNoRegisterDeclares(t *testing.T) {
	t.Parallel()
	root := nameTree(t, "fail/cited")
	before := treeSnapshot(t, root)
	_, err := planNamePass(t, root, "fail-cited.yaml")
	if err == nil {
		t.Fatal("the name pass accepted an entry naming an identifier no register declares")
	}
	if !strings.Contains(err.Error(), "CH-ELSEWHERE") {
		t.Errorf("the failure does not name the identifier: %v", err)
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the failed run wrote to the tree")
	}
}

// TestNamePassFailsWhenTheTreeCarriesNoChannelRegisters pins that an
// absent declaration source fails the run rather than reporting an empty
// identifier space. An empty space fails every entry of the register,
// which reads as a register of misspellings rather than as a tree the
// pass cannot run against yet.
//
// spec: §28.1 (N3, the naming law: the identifier space is declared in
// the communication-channels registers)
func TestNamePassFailsWhenTheTreeCarriesNoChannelRegisters(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	copyTreeInto(t, filepath.Join(fixtureNamePass, "fail/undeclared"), root)
	_, err := planNamePass(t, root, "fail-undeclared.yaml")
	if err == nil {
		t.Fatal("the name pass ran over a tree that declares no identifier")
	}
	if !strings.Contains(err.Error(), "spec/28") {
		t.Errorf("the failure does not name the missing declaration source: %v", err)
	}
}

// TestNamePassFailsAnEntryNoSiteInTheTreeClaims pins the register-tree
// consistency in the direction the walk does not cover. An entry keyed
// to a file the tree does not carry, to a file the write exclusions
// remove, or to an occurrence number above the count of sites its file
// carries is reached by no site, so a run that skipped it would exit
// zero having written nothing for a site a reviewer had resolved. That
// is the completed migration the loader already refuses to report for a
// register with no entry at all, at the granularity an off-by-one
// enumeration produces.
//
// spec: §28.1 (N3, the naming law: the removal is driven by the register
// of senses, and every entry names a site)
func TestNamePassFailsAnEntryNoSiteInTheTreeClaims(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		entry    string
		register string
		names    string
	}{
		{"a file outside the write domain", "fail-unclaimed-excluded.yaml", "proposals/0001_example.md occurrence 1"},
		{"an occurrence above the site count", "fail-unclaimed-occurrence.yaml", "spec/13_security-model.md occurrence 2"},
		{"a file the tree does not carry", "fail-unclaimed-missing.yaml", "spec/99_absent.md occurrence 1"},
	} {
		t.Run(tc.entry, func(t *testing.T) {
			t.Parallel()
			root := nameTree(t, "tree")
			before := treeSnapshot(t, root)
			_, err := planNamePass(t, root, tc.register)
			if err == nil {
				t.Fatalf("the name pass ran with %s in the register", tc.entry)
			}
			if !strings.Contains(err.Error(), tc.names) {
				t.Errorf("the failure does not name the unclaimed entry %q: %v", tc.names, err)
			}
			if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
				t.Error("the failed run wrote to the tree")
			}
		})
	}
}

// pinnedRegisterFixture is the tree path of the register naming the
// string literals of the tier-11 reconciliation carriers that pin
// specification prose, a heading slug, or an intra-spec link. A case
// that exercises a defective register rewrites the copy inside the tree
// it runs over.
const pinnedRegisterFixture = "tests/registers/pinned-spec-literals.yaml"

// writePinnedRegister replaces the pinned-literal register of a copied
// fixture tree with the body a case needs.
func writePinnedRegister(t *testing.T, root, body string) {
	t.Helper()
	target := filepath.Join(root, filepath.FromSlash(pinnedRegisterFixture))
	if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
		t.Fatalf("write the pinned-literal register: %v", err)
	}
}

// TestNamePassWritesAPinnedSpecLiteralAndLeavesAnUnpinnedOne pins the
// second position the naming law governs in a Go carrier. A tier-11
// reconciliation test pins specification prose, a specification heading
// slug, or an intra-spec markdown link as a string literal, so the run
// that rewrites such a sentence in the specification rewrites the
// literal that pins it in the same diff. Leaving the literal behind
// leaves tier 11 red with a reader on the sentence and no pass able to
// write it. Every other string literal stays outside the pass, which the
// case pins in the same carrier: the register names one literal, and the
// operator-facing literal beside it is byte-identical afterwards.
//
// spec: §28.1 (N3, the naming law: the sites of a Go carrier are its doc
// comments and the literals that pin the specification)
func TestNamePassWritesAPinnedSpecLiteralAndLeavesAnUnpinnedOne(t *testing.T) {
	t.Parallel()
	const target = "tests/tier11_docs/route_test.go"
	root := nameTree(t, "tree")
	before := readFixtureFile(t, filepath.Join(fixtureNamePass, "tree", target))
	applied := applyNamePass(t, root, "tree.yaml")
	if !membership(applied.Paths())[target] {
		t.Fatalf("the applied diff does not name %s", target)
	}
	assertSubstituted(t, root, target)
	after := readFixtureFile(t, filepath.Join(root, filepath.FromSlash(target)))
	pinned := fixtureLine(t, before, "requireLine = ")
	if strings.Contains(after, pinned) {
		t.Errorf("the pass left the pinned literal %q unwritten:\n%s", pinned, after)
	}
	unpinned := fixtureLine(t, before, "skipReason = ")
	if !strings.Contains(after, unpinned) {
		t.Errorf("the pass rewrote the unpinned literal %q:\n%s", unpinned, after)
	}
}

// TestNamePassRejectsAMissingOrMalformedPinnedLiteralRegister pins the
// fail-closed rule over the second driving register. A tree carrying a
// tier-11 reconciliation Go carrier and no readable register for its
// pinned literals would run with those literals silently outside the
// pass, which is the writerless site the shared domain exists to
// prevent, so the run fails and the tree is left byte-identical.
//
// spec: §28.1 (N3, the naming law: every site of a carrier the law
// governs has a pass able to write it)
func TestNamePassRejectsAMissingOrMalformedPinnedLiteralRegister(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		defect string
		body   string
		names  string
	}{
		{"a malformed register", "kind: [", "parse the pinned-literal register"},
		{"a register of another kind", "kind: reserved-phrase-senses\nversion: 1\nentries: []\n", "expected kind"},
		{"a register of another version", "kind: pinned-spec-literals\nversion: 2\nentries: []\n", "expected version"},
		{"a register with no entries block", "kind: pinned-spec-literals\nversion: 1\n", "carries no entries block"},
		{"a register with no entry", "kind: pinned-spec-literals\nversion: 1\nentries: []\n", "carries no entry"},
	} {
		t.Run(tc.defect, func(t *testing.T) {
			t.Parallel()
			root := nameTree(t, "tree")
			writePinnedRegister(t, root, tc.body)
			before := treeSnapshot(t, root)
			_, err := applyNamePassErr(t, root, "tree.yaml")
			if err == nil {
				t.Fatalf("the name pass ran with %s", tc.defect)
			}
			if !strings.Contains(err.Error(), tc.names) {
				t.Errorf("the failure does not name the defect %q: %v", tc.names, err)
			}
			if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
				t.Error("the failed run wrote to the tree")
			}
		})
	}
}

// TestNamePassFailsWhenTheTreeCarriesNoPinnedLiteralRegister pins the
// same rule for the register's absence, which is how the population
// reaches a run that was never seeded for it. The failure names the
// register the run needs rather than narrowing the pass to the doc
// comments in silence.
//
// spec: §28.1 (N3, the naming law: every site of a carrier the law
// governs has a pass able to write it)
func TestNamePassFailsWhenTheTreeCarriesNoPinnedLiteralRegister(t *testing.T) {
	t.Parallel()
	root := nameTree(t, "tree")
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(pinnedRegisterFixture))); err != nil {
		t.Fatalf("remove the pinned-literal register: %v", err)
	}
	before := treeSnapshot(t, root)
	_, err := applyNamePassErr(t, root, "tree.yaml")
	if err == nil {
		t.Fatal("the name pass ran over a tier-11 carrier with no pinned-literal register")
	}
	if !strings.Contains(err.Error(), pinnedRegisterFixture) {
		t.Errorf("the failure does not name the register the run needs: %v", err)
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the failed run wrote to the tree")
	}
}

// TestNamePassFailsAPinnedLiteralEntryNoLiteralClaims pins the
// register-tree consistency of the pinned-literal register in the
// direction the walk does not cover, and pins its carrier population. An
// entry keyed to a file the tree does not carry or to a position above
// the count of string literals its file holds names no literal, so the
// site a reviewer resolved would be left unwritten by a run that exited
// zero. An entry keyed outside the tier-11 reconciliation tests would
// admit an operator-facing literal, which the naming law does not
// govern, into the pass.
//
// spec: §28.1 (N3, the naming law: the literals that pin the
// specification are resolved per occurrence before the substitution
// runs)
func TestNamePassFailsAPinnedLiteralEntryNoLiteralClaims(t *testing.T) {
	t.Parallel()
	const head = "kind: pinned-spec-literals\nversion: 1\nentries:\n"
	for _, tc := range []struct {
		entry string
		body  string
		names string
	}{
		{
			"a position above the literal count",
			head + "  - file: tests/tier11_docs/route_test.go\n    literal: 9\n",
			"literal 9",
		},
		{
			"a file the tree does not carry",
			head + "  - file: tests/tier11_docs/absent_test.go\n    literal: 1\n",
			"tests/tier11_docs/absent_test.go",
		},
		{
			"a carrier outside the reconciliation tests",
			head + "  - file: pkg/carrier/carrier.go\n    literal: 1\n",
			"not a Go carrier under",
		},
		{
			"a position below one",
			head + "  - file: tests/tier11_docs/route_test.go\n    literal: 0\n",
			"numbered from one",
		},
		{
			"a literal declared twice",
			head + "  - file: tests/tier11_docs/route_test.go\n    literal: 1\n  - file: tests/tier11_docs/route_test.go\n    literal: 1\n",
			"declared twice",
		},
	} {
		t.Run(tc.entry, func(t *testing.T) {
			t.Parallel()
			root := nameTree(t, "tree")
			writePinnedRegister(t, root, tc.body)
			before := treeSnapshot(t, root)
			_, err := applyNamePassErr(t, root, "tree.yaml")
			if err == nil {
				t.Fatalf("the name pass ran with %s in the pinned-literal register", tc.entry)
			}
			if !strings.Contains(err.Error(), tc.names) {
				t.Errorf("the failure does not name the entry %q: %v", tc.names, err)
			}
			if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
				t.Error("the failed run wrote to the tree")
			}
		})
	}
}

// The anchor pass cases run over their own fixture tree, held under
// testdata/anchorpass/ for the reason the citation fixtures under
// testdata/citations/ are: testdata/ is outside the read domain of every
// pass and every gate, so no gate reports this package's own input.
const fixtureAnchorPass = "testdata/anchorpass"

// anchorTree assembles the tree one anchor pass case runs over, which is
// the shared specification fixture plus the case's own carriers.
func anchorTree(t *testing.T, carriers string) string {
	t.Helper()
	root := t.TempDir()
	copyTreeInto(t, filepath.Join(fixtureAnchorPass, "spec"), root)
	copyTreeInto(t, filepath.Join(fixtureAnchorPass, carriers), root)
	return root
}

// anchorRewriter returns the anchor pass over the tree at root, driven
// by the named anchor-move map fixture.
func anchorRewriter(t *testing.T, root, moves string) *anchor.Rewriter {
	t.Helper()
	r := anchor.New(scope.DirLister(root), scope.DirReader(root))
	if err := r.LoadRegister(filepath.Join(fixtureAnchorPass, "maps", moves)); err != nil {
		t.Fatalf("load the anchor-move map %s: %v", moves, err)
	}
	return r
}

// applyAnchorPass runs the anchor pass over the tree at root and returns
// the applied diff.
func applyAnchorPass(t *testing.T, root, moves string) pass.Diff {
	t.Helper()
	h := pass.NewHarnessOver(scope.DirLister(root), scope.DirReader(root), dirWriterFor(root))
	diff, err := h.Apply(context.Background(), anchorRewriter(t, root, moves))
	if err != nil {
		t.Fatalf("apply the anchor pass: %v", err)
	}
	return diff
}

// planAnchorPass runs the anchor pass over the tree at root without
// writing, and returns the error a fail-closed case expects.
func planAnchorPass(t *testing.T, root, moves string) (pass.Diff, error) {
	t.Helper()
	h := pass.NewHarnessOver(scope.DirLister(root), scope.DirReader(root), dirWriterFor(root))
	return h.Plan(context.Background(), anchorRewriter(t, root, moves))
}

// applyAnchorPassErr runs the anchor pass through the writing path and
// returns the error a fail-closed case expects. The harness has a
// writer, so a run that raised its failure after a write rather than
// before one would leave the tree changed.
func applyAnchorPassErr(t *testing.T, root, moves string) (pass.Diff, error) {
	t.Helper()
	h := pass.NewHarnessOver(scope.DirLister(root), scope.DirReader(root), dirWriterFor(root))
	return h.Apply(context.Background(), anchorRewriter(t, root, moves))
}

// assertRedirected compares one rewritten carrier against the expected
// content held beside the fixture tree.
func assertRedirected(t *testing.T, root, target string) {
	t.Helper()
	after := readFixtureFile(t, filepath.Join(root, filepath.FromSlash(target)))
	want := readFixtureFile(t, filepath.Join(fixtureAnchorPass, "want", target))
	if after != want {
		t.Fatalf("%s after the anchor pass is\n%s\nwant\n%s", target, after, want)
	}
}

// TestAnchorPassRedirectsAFileQualifiedLinkAndLeavesASurvivingOneUntouched
// pins the map-decidable link class. A file-qualified link into a
// retired anchor is rewritten to the successor the map names, written as
// the path from the citing page, while a link into a surviving anchor,
// a link into a file the repository does not track, and an absolute URL
// the fragment-link gate does not read are each left as they stand.
//
// spec: §28.1 (N8, the citation rule: a reference into a retired anchor
// names the heading the material moved to)
func TestAnchorPassRedirectsAFileQualifiedLinkAndLeavesASurvivingOneUntouched(t *testing.T) {
	t.Parallel()
	root := anchorTree(t, "tree")
	applyAnchorPass(t, root, "tree.json")
	for _, target := range []string{
		"spec/07_session-lifecycle.md",
		"docs/reference/adapter-contract.md",
	} {
		t.Run(target, func(t *testing.T) {
			assertRedirected(t, root, target)
		})
	}
}

// TestAnchorPassRedirectsASamePageLinkAndLeavesASurvivingOneUntouched
// pins the same-page form of the same class, which is the majority form
// inside a specification file. The link whose successor sits on another
// page takes the file-qualified form of that successor, the link whose
// successor stays on the page keeps the same-page form, and the link
// into the surviving anchor beside them is left as it stands.
//
// spec: §28.1 (N8, the citation rule: a reference into a retired anchor
// names the heading the material moved to)
func TestAnchorPassRedirectsASamePageLinkAndLeavesASurvivingOneUntouched(t *testing.T) {
	t.Parallel()
	root := anchorTree(t, "tree")
	applyAnchorPass(t, root, "tree.json")
	assertRedirected(t, root, "spec/15_external-api-surface.md")
}

// TestAnchorPassResolvesACitationOfCarvedOutMaterialToTheSurvivingHeading
// pins the class the map alone cannot decide. Two bare citations of the
// same retired section stand in one carrier: the first names material
// the carve-out keeps where it is and resolves to the surviving heading,
// and the second names material that moved and resolves to the section
// the map's successor declares. Sending the first to the map's single
// successor would land a canonical-looking pointer at a card that does
// not define the envelope it cites.
//
// spec: §28.1 (N8, the citation rule: a citation names the heading that
// defines the material it cites)
func TestAnchorPassResolvesACitationOfCarvedOutMaterialToTheSurvivingHeading(t *testing.T) {
	t.Parallel()
	root := anchorTree(t, "tree")
	applyAnchorPass(t, root, "tree.json")
	assertRedirected(t, root, "sdks/runtime/go/runtime/types.go")
}

// TestAnchorPassResolvesBareCitationsCarriedOnBothSidesOfTheSpecificationDirectory
// pins that the sense register decides citations on both sides of the
// specification directory in one run. A specification file and a code
// carrier each hold a bare citation of the same retired section, and the
// register records one occurrence for each, so the run redirects both
// and the register's entries partition when a run is confined to one
// side. A register whose every entry sat on one side would report the
// same result whether the run read the register per carrier or read it
// whole.
//
// spec: §28.1 (N8, the citation rule: a citation names the heading that
// defines the material it cites)
func TestAnchorPassResolvesBareCitationsCarriedOnBothSidesOfTheSpecificationDirectory(t *testing.T) {
	t.Parallel()
	root := anchorTree(t, "tree")
	applyAnchorPass(t, root, "tree.json")
	for _, target := range []string{
		"spec/07_session-lifecycle.md",
		"sdks/runtime/go/runtime/types.go",
	} {
		t.Run(target, func(t *testing.T) {
			after := readFixtureFile(t, filepath.Join(root, filepath.FromSlash(target)))
			if strings.Contains(after, "§15.4.1") {
				t.Errorf("%s still cites the retired §15.4.1 after the anchor pass:\n%s", target, after)
			}
			if !strings.Contains(after, "§28.5.1") {
				t.Errorf("%s carries no citation of the heading the material moved to:\n%s", target, after)
			}
		})
	}
}

// TestAnchorPassAbortsAtACitationTheSenseRegisterDoesNotRecord pins the
// fail-closed rule the citation class rests on. An occurrence the sense
// register does not carry aborts the run non-zero, names the file and
// the line, and leaves every carrier byte-identical, including the
// carrier whose own occurrences the register does resolve. Substituting
// the map's single successor there would read as resolved to every gate
// over the anchor classes while naming a heading that does not define
// the cited material.
//
// The run goes through the writing path, so the byte-identity assertion
// covers a run that had a writer and a resolvable sibling carrier it
// would otherwise have rewritten.
//
// spec: §28.1 (N8, the citation rule: a citation whose destination is
// unresolved is reported rather than redirected against a guess)
func TestAnchorPassAbortsAtACitationTheSenseRegisterDoesNotRecord(t *testing.T) {
	t.Parallel()
	root := anchorTree(t, "fail/unregistered")
	before := treeSnapshot(t, root)
	_, err := applyAnchorPassErr(t, root, "tree.json")
	if err == nil {
		t.Fatal("the anchor pass returned no error at a citation the sense register does not record")
	}
	abort, ok := pass.AsAbort(err)
	if !ok {
		t.Fatalf("the failure is not a fail-closed abort: %v", err)
	}
	if abort.Path != "pkg/carrier/unregistered.go" || abort.Line != 7 {
		t.Errorf("the abort names %s line %d, want pkg/carrier/unregistered.go line 7", abort.Path, abort.Line)
	}
	if !strings.Contains(abort.Reason, "anchor-senses.yaml") {
		t.Errorf("the abort does not name the sense register: %v", abort)
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the aborted run wrote to the tree")
	}
}

// TestAnchorPassRedirectsALinkFromARootLevelCarrierWithoutLeavingTheRoot
// pins the destination a redirect is written with from a document at the
// root of the repository. The root is the directory the successor's path
// is already stated from, so the rewritten link names it directly. A
// link written as if the carrier sat one directory down would resolve
// nowhere, which is the outcome the fragment-link gate fails on.
//
// spec: §28.1 (N8, the citation rule: a reference into a retired anchor
// names the heading the material moved to)
func TestAnchorPassRedirectsALinkFromARootLevelCarrierWithoutLeavingTheRoot(t *testing.T) {
	t.Parallel()
	root := anchorTree(t, "tree")
	applyAnchorPass(t, root, "tree.json")
	assertRedirected(t, root, "README.md")
}

// TestAnchorPassLeavesACitationOfASectionTheSpecificationStillDeclares
// pins the surviving half of the bare-citation class. One carrier holds
// two §-form citations of sections a specification file of the tree
// still declares and the anchor-move map retires no anchor for. Neither
// is a reference the reduction invalidated, so the pass leaves both as
// they are written, reports no work over the carrier, and runs to
// completion with no entry of the sense register recording either, while
// the sibling carrier whose citations do name a retired anchor is
// redirected in the same run.
//
// spec: §28.1 (N8, the citation rule: a citation of a section that still
// exists names the heading that defines its material)
func TestAnchorPassLeavesACitationOfASectionTheSpecificationStillDeclares(t *testing.T) {
	t.Parallel()
	const carrier = "pkg/carrier/flow.go"
	root := anchorTree(t, "surviving-citation")
	before := treeSnapshot(t, root)
	diff := applyAnchorPass(t, root, "tree.json")
	if membership(diff.Paths())[carrier] {
		t.Errorf("the applied diff names %s, whose citations name sections the specification still declares", carrier)
	}
	after := treeSnapshot(t, root)
	if after[carrier] != before[carrier] {
		t.Errorf("%s was rewritten:\n%s", carrier, after[carrier])
	}
	assertRedirected(t, root, "sdks/runtime/go/runtime/types.go")
}

// TestAnchorPassAbortsAtACitationOfASectionNoSpecificationFileStates
// pins the fail-closed half of the bare-citation class at the site the
// anchor-move map does not answer for. A citation names a section no
// specification file of the tree states a heading for and no map entry
// carries a successor for, which is what a citation into an anchor the
// reduction retired looks like when the hand-seeded map omits it. The
// run stops non-zero before any write, names the file and the line, and
// leaves every carrier byte-identical, including the sibling whose own
// occurrences the sense register resolves.
//
// The citation class carries its own proof because no gate over the
// anchor classes reads a §X.Y token: the fragment-link gate reads links
// alone, and the citation resolver and the per-file ratchet match the
// retired line-citation form alone. Deciding the class by the map alone
// would pass the citation over and exit zero, which reads as the
// completed migration it is not, and the change that empties the map
// would then destroy the record of what the run should have done.
//
// spec: §28.1 (N8, the citation rule: a citation whose destination is
// unresolved is reported rather than left naming a section that is gone)
func TestAnchorPassAbortsAtACitationOfASectionNoSpecificationFileStates(t *testing.T) {
	t.Parallel()
	root := anchorTree(t, "fail/undeclared-citation")
	before := treeSnapshot(t, root)
	_, err := applyAnchorPassErr(t, root, "tree.json")
	if err == nil {
		t.Fatal("the anchor pass returned no error at a citation of a section no specification file states")
	}
	abort, ok := pass.AsAbort(err)
	if !ok {
		t.Fatalf("the failure is not a fail-closed abort: %v", err)
	}
	if abort.Path != "pkg/carrier/flow.go" || abort.Line != 7 {
		t.Errorf("the abort names %s line %d, want pkg/carrier/flow.go line 7", abort.Path, abort.Line)
	}
	if !strings.Contains(abort.Reason, "15.9.9") {
		t.Errorf("the abort does not name the cited section: %v", abort)
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the aborted run wrote to the tree")
	}
}

// TestAnchorPassLeavesACitationWrittenAsALinkLabelToTheHandCorrection
// pins that the pass performs a target-only redirect. A link whose label
// spells the retiring subsection has its target redirected while the
// label is left exactly as it was written, because the label is
// corrected in the change that makes the reduction. The prose citation
// beside it is the carrier's first occurrence, and it resolves to the
// heading the sense register records for that occurrence.
//
// Reading the label as a bare citation would rewrite a site this pass
// does not own and shift the occurrence numbering of the file, so the
// entry written for the prose citation would land at the label instead.
//
// spec: §28.1 (N8, the citation rule: a reference into a retired anchor
// names the heading the material moved to)
func TestAnchorPassLeavesACitationWrittenAsALinkLabelToTheHandCorrection(t *testing.T) {
	t.Parallel()
	root := anchorTree(t, "link-label")
	applyAnchorPass(t, root, "tree.json")
	assertRedirected(t, root, "docs/reference/label.md")
}

// TestAnchorPassLeavesACitationInALinkLabelCarryingNoFragment pins the
// same target-only rule at the two link forms a fragment-carrying
// single-line match does not cover. A link whose destination carries no
// fragment, and a link whose label wraps across a line, each name the
// retiring subsection in their label. Both labels stand exactly as they
// were written, the second link's target is redirected, and the prose
// citation beside them is still the carrier's first occurrence, so the
// entry the sense register holds for occurrence 1 lands on it.
//
// Reading either label as a bare citation would rewrite a site the hand
// correction owns and shift the occurrence numbering of every citation
// below it, so the register's entry would resolve the wrong site.
//
// spec: §28.1 (N8, the citation rule: a reference into a retired anchor
// names the heading the material moved to)
func TestAnchorPassLeavesACitationInALinkLabelCarryingNoFragment(t *testing.T) {
	t.Parallel()
	root := anchorTree(t, "link-label-plain")
	applyAnchorPass(t, root, "tree.json")
	assertRedirected(t, root, "docs/reference/label-plain.md")
}

// TestAnchorPassLeavesALinkIntoAnAnchorItsDocumentStillDeclares pins the
// surviving half of the link class. One carrier links into an anchor the
// anchor-move map retires, and another links into an anchor its target
// document still declares. The first is redirected in the same run that
// leaves the second exactly as it was written and reports no work over
// its carrier.
//
// spec: §28.1 (N8, the citation rule: a reference into an anchor that
// still resolves names the heading that defines its material)
func TestAnchorPassLeavesALinkIntoAnAnchorItsDocumentStillDeclares(t *testing.T) {
	t.Parallel()
	const surviving = "docs/reference/versioning.md"
	root := anchorTree(t, "surviving-link")
	before := treeSnapshot(t, root)
	diff := applyAnchorPass(t, root, "tree.json")
	if membership(diff.Paths())[surviving] {
		t.Errorf("the applied diff names %s, whose link its target document still declares", surviving)
	}
	after := treeSnapshot(t, root)
	if after[surviving] != before[surviving] {
		t.Errorf("%s was rewritten:\n%s", surviving, after[surviving])
	}
	assertRedirected(t, root, "docs/reference/framing.md")
}

// TestAnchorPassLeavesALinkTheAnchorMoveMapRetiresNoAnchorFor pins the
// population of the link class at the site whose target the tree does
// not resolve. An intra-repo fragment link names an anchor its
// destination document does not declare and the anchor-move map retires
// no reference into, so it resolved nowhere before this run and is a
// pre-existing broken link rather than one the reduction invalidated.
// The pass leaves it exactly as it is written and reports no work over
// its carrier, while the sibling link the map does retire is redirected
// in the same run.
//
// Aborting there would stop every run of the pass over the tree at links
// no register entry redirects, whose correction is the hand enumeration
// that lands with the fragment-link gate, and the abort would report them
// as references the anchor-move map omits a successor for.
//
// spec: §28.1 (N8, the citation rule: the redirect is driven by the map
// the change ships)
func TestAnchorPassLeavesALinkTheAnchorMoveMapRetiresNoAnchorFor(t *testing.T) {
	t.Parallel()
	const carrier = "docs/reference/flow-control.md"
	root := anchorTree(t, "link-outside-the-map")
	before := treeSnapshot(t, root)
	diff := applyAnchorPass(t, root, "tree.json")
	if membership(diff.Paths())[carrier] {
		t.Errorf("the applied diff names %s, whose link the anchor-move map retires no anchor for", carrier)
	}
	after := treeSnapshot(t, root)
	if after[carrier] != before[carrier] {
		t.Errorf("%s was rewritten:\n%s", carrier, after[carrier])
	}
	assertRedirected(t, root, "docs/reference/framing.md")
}

// TestAnchorPassCitesALevelOneSpecificationTitleByItsSectionNumber pins
// the spelling a redirect takes when the heading it names is the
// level-one title a specification file opens with, which states that
// file's own section number. The citation keeps the §-form the migration
// establishes rather than being rewritten into a fragment link, which
// would move a live citation off the form every other citation of that
// section is written in.
//
// spec: §28.1 (N8, the citation rule: a citation names the heading that
// defines the material it cites)
func TestAnchorPassCitesALevelOneSpecificationTitleByItsSectionNumber(t *testing.T) {
	t.Parallel()
	root := anchorTree(t, "level-one-section-destination")
	applyAnchorPass(t, root, "tree.json")
	assertRedirected(t, root, "pkg/carrier/audit.go")
}

// TestAnchorPassLeavesTheCitationItWroteForALevelOneTitleStanding pins
// the heading levels a citation is judged live against, which are every
// level a specification file states a number in. The pass writes a
// citation of a file that states its number only in its level-one title,
// so a second run over its own output reads that citation as naming a
// section the specification states and leaves it exactly as it stands,
// reporting no work.
//
// Judging a citation against the level-two-and-deeper section index
// alone would stop the second run at the citation the first run wrote,
// which is a pass that fails closed on its own output.
//
// spec: §28.1 (N8, the citation rule: a citation of a section that still
// exists names the heading that defines its material)
func TestAnchorPassLeavesTheCitationItWroteForALevelOneTitleStanding(t *testing.T) {
	t.Parallel()
	const carrier = "pkg/carrier/audit.go"
	root := anchorTree(t, "level-one-section-destination")
	applyAnchorPass(t, root, "tree.json")
	before := treeSnapshot(t, root)
	diff := applyAnchorPass(t, root, "tree.json")
	if membership(diff.Paths())[carrier] {
		t.Errorf("the second run names %s, whose citation names a section the specification states", carrier)
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the second run rewrote the output of the first")
	}
}

// TestAnchorPassJudgesASectionRetiredAgainstTheAnchorMoveMap pins that
// what retires a section is the anchor-move map rather than any heading
// of the tree. A documentation page numbers its own headings, and one of
// those numbers collides with a section this reduction retired. The map
// carries the retired anchor of that section, so the citation is read as
// naming it and is redirected to the heading the material moved to.
//
// Reading a heading anywhere in the tree as the answer would make the
// retired section look alive at that number: the citation would be
// neither rewritten nor reported, the run would exit zero, and the
// change that empties the anchor-move map would then destroy the record
// of what the run should have done.
//
// spec: §28.1 (N8, the citation rule: a citation names the heading that
// defines the material it cites)
func TestAnchorPassJudgesASectionRetiredAgainstTheAnchorMoveMap(t *testing.T) {
	t.Parallel()
	root := anchorTree(t, "collision")
	applyAnchorPass(t, root, "tree.json")
	assertRedirected(t, root, "pkg/carrier/collide.go")
}

// TestAnchorPassReportsARetiredSectionANonSpecificationHeadingNumbers
// pins the same rule at the site the sense register does not answer for,
// which is where reading a heading of the tree as the answer is silent.
// The map retires the anchor of the cited section, a documentation page
// numbers a heading of its own the same way, and no entry resolves the
// occurrence. The run stops, names the file and the line, and leaves the
// tree byte-identical, including the sibling carrier whose own
// occurrences the register does resolve.
//
// Reading the documentation heading as the answer would report the
// section as alive, pass over the citation, and exit zero, and the
// change that empties the registers would then destroy the record of
// what the run should have done.
//
// spec: §28.1 (N8, the citation rule: a citation whose destination is
// unresolved is reported rather than left naming a heading that is gone)
func TestAnchorPassReportsARetiredSectionANonSpecificationHeadingNumbers(t *testing.T) {
	t.Parallel()
	root := anchorTree(t, "fail/collision-unrecorded")
	before := treeSnapshot(t, root)
	_, err := applyAnchorPassErr(t, root, "tree.json")
	if err == nil {
		t.Fatal("the anchor pass returned no error at a retired section a documentation heading numbers")
	}
	abort, ok := pass.AsAbort(err)
	if !ok {
		t.Fatalf("the failure is not a fail-closed abort: %v", err)
	}
	if abort.Path != "pkg/carrier/collide.go" || abort.Line != 7 {
		t.Errorf("the abort names %s line %d, want pkg/carrier/collide.go line 7", abort.Path, abort.Line)
	}
	if !strings.Contains(abort.Reason, "15.4.1") || !strings.Contains(abort.Reason, "anchor-senses.yaml") {
		t.Errorf("the abort names neither the citation nor the sense register: %v", abort)
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the aborted run wrote to the tree")
	}
}

// TestAnchorPassCitesANonSpecificationDestinationByItsFileAndAnchor pins
// the spelling a redirect is written in when the heading it names is not
// a specification heading. A documentation page numbers a heading of its
// own, the sense register records an occurrence as meaning that heading,
// and the citation is rewritten to the file and anchor that address it.
//
// Writing the §-form there would compose a citation of a section no
// specification file declares, and nothing over the anchor classes reads
// it: the fragment-link gate reads links alone, the citation resolver
// matches the retired line-citation form alone, and a later run of this
// pass would read the citation as a site of a retired section.
//
// spec: §28.1 (N8, the citation rule: a citation names the heading that
// defines the material it cites)
func TestAnchorPassCitesANonSpecificationDestinationByItsFileAndAnchor(t *testing.T) {
	t.Parallel()
	root := anchorTree(t, "non-specification-destination")
	applyAnchorPass(t, root, "tree.json")
	assertRedirected(t, root, "pkg/carrier/framing.go")
}

// TestAnchorPassRefusesASenseEntryThatNamesNoDestination pins that every
// occurrence the register records names the heading it means. An entry
// that records an occurrence without naming a heading resolves nothing,
// so it fails the load rather than standing as a recorded answer that
// leaves the occurrence as it is written, and the tree is byte-identical
// after the failure.
//
// spec: §28.1 (N8, the citation rule: a citation whose destination is
// unresolved is reported rather than left naming a heading that is gone)
func TestAnchorPassRefusesASenseEntryThatNamesNoDestination(t *testing.T) {
	t.Parallel()
	root := anchorTree(t, "fail/sense-no-destination")
	before := treeSnapshot(t, root)
	_, err := applyAnchorPassErr(t, root, "tree.json")
	if err == nil {
		t.Fatal("the anchor pass ran with a sense entry that names no destination")
	}
	if !strings.Contains(err.Error(), "<path>#<anchor>") {
		t.Errorf("the failure does not name the destination defect: %v", err)
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the failed run wrote to the tree")
	}
}

// TestAnchorPassResolvesASuccessorDeclaredByALevelOneTitle pins the
// heading levels the redirect is held against. A documentation page
// opens with a level-one title, a renderer gives that title an anchor,
// and a redirect may name it. The index covers every heading level a
// renderer derives an anchor from, so the redirect resolves and the link
// is rewritten to it.
//
// Indexing only the levels a section's range is computed from would fail
// the run at a successor the tree does declare, and would report a link
// into such a title as a reference into a retired anchor.
//
// spec: §28.1 (N8, the citation rule: a redirect names a heading the
// tree declares)
func TestAnchorPassResolvesASuccessorDeclaredByALevelOneTitle(t *testing.T) {
	t.Parallel()
	const carrier = "spec/15_external-api-surface.md"
	root := anchorTree(t, "tree")
	applyAnchorPass(t, root, "successor-level-one-title.json")
	after := treeSnapshot(t, root)
	const want = "[the message format](../docs/reference/adapter-contract.md#adapter-contract)"
	if !strings.Contains(after[carrier], want) {
		t.Errorf("the link was not redirected to the level-one title:\n%s", after[carrier])
	}
}

// TestAnchorPassFailsAMoveWhoseSuccessorHeadingDoesNotExist pins that
// every redirect is held to a heading of the tree before any file is
// written. A successor nothing declares would send every inbound
// reference to a page position that does not resolve, and the rewritten
// reference reads as resolved to a reader of the diff.
//
// spec: §28.1 (N8, the citation rule: a redirect names a heading the
// tree declares)
func TestAnchorPassFailsAMoveWhoseSuccessorHeadingDoesNotExist(t *testing.T) {
	t.Parallel()
	root := anchorTree(t, "tree")
	before := treeSnapshot(t, root)
	_, err := applyAnchorPassErr(t, root, "successor-missing.json")
	if err == nil {
		t.Fatal("the anchor pass ran with a successor heading no document declares")
	}
	if !strings.Contains(err.Error(), "2859-ch-nothing-declares-this") {
		t.Errorf("the failure does not name the successor: %v", err)
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the failed run wrote to the tree")
	}
}

// TestAnchorPassFailsASuccessorWrittenOnlyInsideAFencedExample pins that
// the anchor half of the heading index obeys the fence rule its heading
// half obeys. A page documenting how a kramdown anchor attribute is
// written shows the attribute inside a fenced code block, which is
// example text and declares nothing. Reading it as a declaration would
// let a successor naming that identifier pass the pre-write existence
// check, and every inbound reference would then be written to a page
// position no renderer produces.
//
// spec: §28.1 (N8, the citation rule: a redirect names a heading the
// tree declares)
func TestAnchorPassFailsASuccessorWrittenOnlyInsideAFencedExample(t *testing.T) {
	t.Parallel()
	root := anchorTree(t, "fenced-anchor")
	before := treeSnapshot(t, root)
	_, err := applyAnchorPassErr(t, root, "successor-in-a-fenced-example.json")
	if err == nil {
		t.Fatal("the anchor pass ran with a successor an example inside a fenced code block writes")
	}
	if !strings.Contains(err.Error(), "2859-ch-fenced-example") {
		t.Errorf("the failure does not name the successor: %v", err)
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the failed run wrote to the tree")
	}
}

// TestAnchorPassRedirectsALinkWhoseAnchorSurvivesOnlyInAFencedExample
// pins the other half of the same rule. A link into a retired anchor
// whose identifier appears in a fenced example on the page it addresses
// is a reference the reduction retired, so it is redirected. Reading the
// example as a declaration would read the link as one the reduction left
// alone and leave a reference the fragment-link gate fails.
//
// spec: §28.1 (N8, the citation rule: a reference into a retired anchor
// names the heading the material moved to)
func TestAnchorPassRedirectsALinkWhoseAnchorSurvivesOnlyInAFencedExample(t *testing.T) {
	t.Parallel()
	root := anchorTree(t, "fenced-anchor")
	applyAnchorPass(t, root, "tree.json")
	assertRedirected(t, root, "docs/reference/anchor-style.md")
}

// TestAnchorPassFailsADestinationWrittenAboveTheFirstHeading pins that a
// standalone anchor attribute standing before a document's first heading
// declares nothing. It addresses no heading, so binding it to one would
// resolve a destination against a heading with no number and no line and
// write a reference to a position no renderer produces.
//
// spec: §28.1 (N8, the citation rule: a redirect names a heading the
// tree declares)
func TestAnchorPassFailsADestinationWrittenAboveTheFirstHeading(t *testing.T) {
	t.Parallel()
	root := anchorTree(t, "fail/anchor-above-the-first-heading")
	before := treeSnapshot(t, root)
	_, err := applyAnchorPassErr(t, root, "tree.json")
	if err == nil {
		t.Fatal("the anchor pass ran with a destination only an attribute above the first heading writes")
	}
	if !strings.Contains(err.Error(), "the-preamble") {
		t.Errorf("the failure does not name the destination: %v", err)
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the failed run wrote to the tree")
	}
}

// TestAnchorPassFailsASenseDestinationNoHeadingDeclares pins the same
// check over the second register. A destination is resolved against the
// headings of the tree before any file is written, so a typo in an entry
// fails the run rather than landing at a site as a pointer to a heading
// that exists nowhere.
//
// spec: §28.1 (N8, the citation rule: a redirect names a heading the
// tree declares)
func TestAnchorPassFailsASenseDestinationNoHeadingDeclares(t *testing.T) {
	t.Parallel()
	root := anchorTree(t, "fail/undeclared-destination")
	before := treeSnapshot(t, root)
	_, err := applyAnchorPassErr(t, root, "tree.json")
	if err == nil {
		t.Fatal("the anchor pass ran with a destination no heading declares")
	}
	if !strings.Contains(err.Error(), "messageenvelope-that-nothing-declares") {
		t.Errorf("the failure does not name the destination: %v", err)
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the failed run wrote to the tree")
	}
}

// TestAnchorPassRejectsAMissingOrMalformedAnchorMoveMap pins that the
// map is validated before the run rather than loaded as an empty one. A
// map that carried nothing would rewrite no site while the run exited
// zero, which reads as a completed migration, and the change that
// empties the map is the change that would destroy the record of what
// the run should have done.
//
// spec: §28.1 (N8, the citation rule: the redirect is driven by the map
// the change ships)
func TestAnchorPassRejectsAMissingOrMalformedAnchorMoveMap(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		defect string
		file   string
		names  string
	}{
		{"a map that is not in the tree", "absent.json", "read the anchor-move map"},
		{"a map that does not parse", "malformed.json", "parse the anchor-move map"},
		{"a map declaring another kind", "wrong-kind.json", "expected kind"},
		{"a map declaring another version", "wrong-version.json", "expected version"},
		{"a map with no moves block", "no-moves-block.json", "carries no moves block"},
		{"a map with no move", "empty-moves.json", "carries no move"},
		{"a move naming no retired anchor", "invalid-no-anchor.json", "names no retired anchor"},
		{"a move naming no successor file", "invalid-no-successor-file.json", "names no successor file"},
		{"a move naming no successor anchor", "invalid-no-successor-anchor.json", "names no successor anchor"},
		{"an anchor declared twice", "invalid-duplicate-anchor.json", "is declared twice"},
	} {
		t.Run(tc.defect, func(t *testing.T) {
			t.Parallel()
			root := anchorTree(t, "tree")
			r := anchor.New(scope.DirLister(root), scope.DirReader(root))
			err := r.LoadRegister(filepath.Join(fixtureAnchorPass, "maps", tc.file))
			if err == nil {
				t.Fatalf("the anchor pass loaded %s", tc.defect)
			}
			if !strings.Contains(err.Error(), tc.names) {
				t.Errorf("the failure does not name the defect %q: %v", tc.names, err)
			}
		})
	}
}

// TestAnchorPassRejectsAMissingOrMalformedSenseRegister pins the same
// rule over the register the tree carries. A register that loaded as
// carrying nothing would abort the run at the first bare citation of the
// tree, reporting a register that had not been seeded while one had
// been.
//
// spec: §28.1 (N8, the citation rule: the redirect is driven by the
// register the change ships)
func TestAnchorPassRejectsAMissingOrMalformedSenseRegister(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		defect   string
		carriers string
		names    string
	}{
		{"a register that is not in the tree", "fail/sense-missing", "read the anchor sense register"},
		{"a register that does not parse", "fail/sense-malformed", "parse the anchor sense register"},
		{"a register declaring another kind", "fail/sense-wrong-kind", "expected kind"},
		{"a register declaring another version", "fail/sense-wrong-version", "expected version"},
		{"a register with no entries block", "fail/sense-no-entries-block", "carries no entries block"},
		{"a register with no entry", "fail/sense-empty-entries", "carries no entry"},
		{"an entry naming no file", "fail/sense-no-file", "carries no file"},
		{"an entry numbered from zero", "fail/sense-bad-occurrence", "numbered from one"},
		{"an entry whose destination names no anchor", "fail/sense-bad-destination", "<path>#<anchor>"},
		{"an occurrence declared twice", "fail/sense-duplicate", "is declared twice"},
	} {
		t.Run(tc.defect, func(t *testing.T) {
			t.Parallel()
			root := anchorTree(t, tc.carriers)
			before := treeSnapshot(t, root)
			_, err := applyAnchorPassErr(t, root, "tree.json")
			if err == nil {
				t.Fatalf("the anchor pass ran with %s", tc.defect)
			}
			if !strings.Contains(err.Error(), tc.names) {
				t.Errorf("the failure does not name the defect %q: %v", tc.names, err)
			}
			if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
				t.Error("the failed run wrote to the tree")
			}
		})
	}
}

// TestAnchorPassLeavesEveryWriteExcludedCarrierByteIdentical pins the
// write exclusion. A reference into a retired anchor in the staged
// proposal tree, in either historical audit record, and in either root
// planning document is left exactly as it was written and appears in
// neither the dry-run output nor the applied diff, while an equivalent
// reference in an ordinary carrier in the same run is redirected.
//
// spec: §28.1 (N8, the citation rule: the excluded records are outside
// the writable population)
func TestAnchorPassLeavesEveryWriteExcludedCarrierByteIdentical(t *testing.T) {
	t.Parallel()
	root := anchorTree(t, "tree")
	excluded := []string{
		"proposals/0001_example.md",
		"BUILD-GAPS.md",
		"TEST-GAPS.md",
		"gateway-runtime-comms.md",
		"gateway-runtime-comms-remediation.md",
	}
	before := treeSnapshot(t, root)
	planned, err := planAnchorPass(t, root, "tree.json")
	if err != nil {
		t.Fatalf("plan the anchor pass: %v", err)
	}
	applied := applyAnchorPass(t, root, "tree.json")
	after := treeSnapshot(t, root)
	inPlan, inApplied := membership(planned.Paths()), membership(applied.Paths())
	for _, target := range excluded {
		if before[target] == "" {
			t.Fatalf("the fixture tree carries no %s", target)
		}
		if after[target] != before[target] {
			t.Errorf("%s was rewritten:\n%s", target, after[target])
		}
		if inPlan[target] {
			t.Errorf("the dry-run output names the excluded %s", target)
		}
		if inApplied[target] {
			t.Errorf("the applied diff names the excluded %s", target)
		}
	}
	assertRedirected(t, root, "spec/07_session-lifecycle.md")
}

// TestAnchorPassDryRunOutputEqualsTheAppliedDiff pins the entry
// criterion for applying the pass: what the dry run reports is what the
// apply writes, so a reviewer reads the whole redirect before any file
// moves.
//
// spec: §28.1 (N8, the citation rule: the redirect is reviewed before it
// is applied)
func TestAnchorPassDryRunOutputEqualsTheAppliedDiff(t *testing.T) {
	t.Parallel()
	root := anchorTree(t, "tree")
	before := treeSnapshot(t, root)
	planned, err := planAnchorPass(t, root, "tree.json")
	if err != nil {
		t.Fatalf("plan the anchor pass: %v", err)
	}
	if len(planned.Files) == 0 {
		t.Fatal("the dry run reports no work over the fixture tree")
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the dry run wrote to the tree")
	}
	applied := applyAnchorPass(t, root, "tree.json")
	if !planned.Equal(applied) {
		t.Fatalf("the applied diff differs from the dry run: %v vs %v", planned.Paths(), applied.Paths())
	}
}

// TestTheDriverCarriesTheAnchorPass pins that the pass the driver runs
// is the built one, so a run of the engine over a checkout redirects
// references rather than reporting a pass that is not built.
//
// spec: §28.1 (N8, the citation rule: the redirect is performed by the
// committed tooling)
func TestTheDriverCarriesTheAnchorPass(t *testing.T) {
	t.Parallel()
	built := builtPasses(repoRoot(t))
	r, ok := built[scope.Anchor]
	if !ok {
		t.Fatal("the driver carries no anchor pass")
	}
	if r.Pass() != scope.Anchor {
		t.Errorf("the built pass names the %s write domain", r.Pass())
	}
}

// fixtureIDPass holds the identifier pass fixtures: the shared
// specification overlay carrying the naming table, the carriers of each
// case, the expected contents, and the driving registers.
const fixtureIDPass = "testdata/idpass"

// idTree assembles the tree one identifier pass case runs over, which is
// the shared specification fixture plus the case's own carriers, later
// overlays overriding earlier ones.
func idTree(t *testing.T, carriers ...string) string {
	t.Helper()
	root := t.TempDir()
	copyTreeInto(t, filepath.Join(fixtureIDPass, "spec"), root)
	for _, dir := range carriers {
		copyTreeInto(t, filepath.Join(fixtureIDPass, dir), root)
	}
	return root
}

// idHarness returns the harness one identifier pass case runs through.
// It carries a remover as well as a writer, because the pass moves the
// carrier whose own name carries a retired spelling.
func idHarness(root string) *pass.Harness {
	h := pass.NewHarnessOver(scope.DirLister(root), scope.DirReader(root), dirWriterFor(root))
	h.Remove = scope.DirRemover(root)
	return h
}

// idRewriter returns the identifier pass over the tree at root, driven
// by the named register fixture.
func idRewriter(t *testing.T, root, register string) *identifier.Rewriter {
	t.Helper()
	r := identifier.New(scope.DirLister(root), scope.DirReader(root))
	if err := r.LoadRegister(filepath.Join(fixtureIDPass, "registers", register)); err != nil {
		t.Fatalf("load the identifier pass register %s: %v", register, err)
	}
	return r
}

// keyWritableIn returns the path-keyed registers the tree at root
// carries, sorted, as the rekey channel derives them.
func keyWritableIn(t *testing.T, root string) []string {
	t.Helper()
	all, err := scope.DirLister(root)(context.Background())
	if err != nil {
		t.Fatalf("list the tree at %s: %v", root, err)
	}
	var keyed []string
	for _, target := range all {
		if scope.KeyWritable(target) {
			keyed = append(keyed, target)
		}
	}
	sort.Strings(keyed)
	return keyed
}

// applyIDPass runs the identifier pass over the tree at root and returns
// the applied diff.
func applyIDPass(t *testing.T, root, register string) pass.Diff {
	t.Helper()
	diff, err := idHarness(root).Apply(context.Background(), idRewriter(t, root, register))
	if err != nil {
		t.Fatalf("apply the identifier pass: %v", err)
	}
	return diff
}

// planIDPass runs the identifier pass over the tree at root without
// writing, and returns the error a fail-closed case expects.
func planIDPass(t *testing.T, root, register string) (pass.Diff, error) {
	t.Helper()
	return idHarness(root).Plan(context.Background(), idRewriter(t, root, register))
}

// applyIDPassErr runs the identifier pass through the writing path and
// returns the error a fail-closed case expects. The harness has a writer
// and a remover, so a run that raised its abort after a write rather
// than before one would leave the tree changed.
func applyIDPassErr(t *testing.T, root, register string) (pass.Diff, error) {
	t.Helper()
	return idHarness(root).Apply(context.Background(), idRewriter(t, root, register))
}

// assertRewritten compares one carrier of the run against the expected
// content held beside the fixture tree, under the path the carrier has
// after the run.
func assertRewritten(t *testing.T, root, target string) {
	t.Helper()
	after := readFixtureFile(t, filepath.Join(root, filepath.FromSlash(target)))
	want := readFixtureFile(t, filepath.Join(fixtureIDPass, "want", target))
	if after != want {
		t.Fatalf("%s after the identifier pass is\n%s\nwant\n%s", target, after, want)
	}
}

// TestIdentifierPassAbortsAtAnUnregisteredOccurrenceAndLeavesTheTreeUnmodified
// pins the fail-closed rule the whole pass rests on. An occurrence of a
// retired spelling the sense register does not carry aborts the run
// non-zero, names the file and the line, and leaves every carrier
// byte-identical, including the sibling whose own occurrence the
// register does resolve. A default substitution there would read as
// canonical to the identifier-resolution gate, which reads the forward
// relation and so does not observe one spelling resolved to the wrong
// identifier.
//
// spec: §28.1 (N4, the naming law: the canonical identifier is written
// in every carrier of the channel it denotes)
func TestIdentifierPassAbortsAtAnUnregisteredOccurrenceAndLeavesTheTreeUnmodified(t *testing.T) {
	t.Parallel()
	root := idTree(t, "fail/unregistered")
	before := treeSnapshot(t, root)
	_, err := applyIDPassErr(t, root, "fail-unregistered.yaml")
	if err == nil {
		t.Fatal("the identifier pass returned no error at an unregistered occurrence")
	}
	abort, ok := pass.AsAbort(err)
	if !ok {
		t.Fatalf("the pass returned %v, which is not an abort", err)
	}
	if abort.Path != "pkg/adapter/unregistered.go" {
		t.Errorf("the abort names %s, want pkg/adapter/unregistered.go", abort.Path)
	}
	if abort.Line != 5 {
		t.Errorf("the abort names line %d, want 5", abort.Line)
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the aborted run left the tree modified")
	}
}

// TestIdentifierPassAbortsAtAFileNameNoEntryResolves pins the same rule
// over the file-name position. The naming law reaches the file-name
// stem, so a carrier named after a retired spelling moves in the same
// run, and moving it on a guess renames a file after a mechanism its
// contents never mention while every path-keyed register follows the
// guess.
//
// spec: §28.1 (N4, the naming law: the file-name stem of a carrier is
// the channel's identifier)
func TestIdentifierPassAbortsAtAFileNameNoEntryResolves(t *testing.T) {
	t.Parallel()
	root := idTree(t, "fail/unregisteredpath")
	before := treeSnapshot(t, root)
	_, err := applyIDPassErr(t, root, "fail-unregisteredpath.yaml")
	if err == nil {
		t.Fatal("the identifier pass returned no error at an unresolved file name")
	}
	abort, ok := pass.AsAbort(err)
	if !ok {
		t.Fatalf("the pass returned %v, which is not an abort", err)
	}
	if abort.Path != "pkg/adapter/lifecyclechannel.go" {
		t.Errorf("the abort names %s, want pkg/adapter/lifecyclechannel.go", abort.Path)
	}
	// The rename plan is computed once per run, so the file name is
	// reported once. A plan re-computed per file in the write domain
	// would re-walk and re-parse the whole tree once per file and report
	// the same site as many times as the tree has files, which is a
	// hand-correction population the operator cannot read.
	sites, ok := pass.AllAborts(err)
	if !ok {
		t.Fatalf("the pass returned %v, which carries no aborts", err)
	}
	if len(sites) != 1 {
		t.Errorf("the run reports the one unresolved file name %d time(s): %v", len(sites), err)
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the aborted run left the tree modified")
	}
}

// TestIdentifierPassResolvesAFileNameFromThePathRowAlone pins the form
// rule over the file-name position. A file name is the carrier of the
// path stem, so it takes the path row of the naming table, and a channel
// that states the retired spelling for some other carrier alone leaves
// the file name with no substitution. Resolving it from whichever row
// happened to be the only one would move a carrier to a name the table
// states for no path, with the run exiting clean.
//
// spec: §28.1 (N4, the naming law: the file-name stem of a carrier is
// the channel's identifier)
func TestIdentifierPassResolvesAFileNameFromThePathRowAlone(t *testing.T) {
	t.Parallel()
	root := idTree(t, "fail/nopathrow")
	before := treeSnapshot(t, root)
	_, err := applyIDPassErr(t, root, "fail-nopathrow.yaml")
	if err == nil {
		t.Fatal("the identifier pass moved a file from a row stated for another carrier")
	}
	abort, ok := pass.AsAbort(err)
	if !ok {
		t.Fatalf("the pass returned %v, which is not an abort", err)
	}
	if abort.Path != "pkg/adapter/lifecyclechannel.go" {
		t.Errorf("the abort names %s, want pkg/adapter/lifecyclechannel.go", abort.Path)
	}
	if !strings.Contains(abort.Reason, "file name") {
		t.Errorf("the abort does not name the form of the site: %s", abort.Reason)
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the aborted run left the tree modified")
	}
}

// TestIdentifierPassWritesTheCanonicalSpellingOfEachResolvedOccurrence
// pins the substitution and the move together. Each occurrence takes the
// spelling the naming table states for the channel its entry names and
// for the carrier it stands in, and the carrier whose own name carries a
// retired spelling leaves the run under its canonical name with nothing
// left behind at the old one.
//
// spec: §28.1 (N4, the naming law: the canonical identifier is written
// in every carrier of the channel it denotes)
func TestIdentifierPassWritesTheCanonicalSpellingOfEachResolvedOccurrence(t *testing.T) {
	t.Parallel()
	root := idTree(t, "tree")
	applyIDPass(t, root, "tree.yaml")
	for _, target := range []string{
		"pkg/adapter/runtimeops.go",
		"pkg/adapter/runtimeops_test.go",
		"docs/reference/channels.md",
		"schemas/runtime-ops-events.schema.json",
	} {
		assertRewritten(t, root, target)
	}
	after := treeSnapshot(t, root)
	for _, gone := range []string{
		"pkg/adapter/lifecyclechannel.go",
		"pkg/adapter/lifecyclechannel_test.go",
		"schemas/lifecycle-events.schema.json",
	} {
		if _, ok := after[gone]; ok {
			t.Errorf("%s is still in the tree after the run moved it", gone)
		}
	}
}

// TestIdentifierPassResolvesAGrpcFullMethodLiteralFromTheProtoRow pins
// the rule that selects a naming-table row by the form of the site. The
// same retired token stands in a gRPC full-method literal and in the Go
// symbol beside it, and the two take different canonical spellings: the
// literal carries the RPC name the service definition declares, so a
// literal resolved from the Go type row would name a method the service
// does not declare while reading as canonical to every gate.
//
// spec: §28.1 (N4, the naming law: the proto RPC name stem is the
// channel's identifier)
func TestIdentifierPassResolvesAGrpcFullMethodLiteralFromTheProtoRow(t *testing.T) {
	t.Parallel()
	root := idTree(t, "tree")
	applyIDPass(t, root, "tree.yaml")
	assertRewritten(t, root, "pkg/adapter/holdstate.go")
	after := readFixtureFile(t, filepath.Join(root, "pkg", "adapter", "holdstate.go"))
	if !strings.Contains(after, "/lenny.adapter.v1.Adapter/AdapterEvents\"") {
		t.Errorf("the full-method literal was not resolved from the proto RPC row:\n%s", after)
	}
	if !strings.Contains(after, "\"AdapterEventsChannel\"") {
		t.Errorf("the Go symbol beside it was not resolved from the Go type row:\n%s", after)
	}
}

// TestIdentifierPassWritesTheProtoRowIntoTheServiceDefinition pins that
// the form rule selects a row where it applies and rules nothing out
// anywhere else. The primary carrier of the proto RPC row is the
// `rpc <Name>` declaration of the service definition, which is ordinary
// carrier text rather than a full-method literal, so a rule that
// subtracted that row from every site outside such a literal would leave
// the declaration with no route to the spelling the naming table states
// for it: the run would either abort there with no register value able to
// select the row, or write the Go type spelling into a method the service
// does not declare. The declaration, the two message types it names, and
// the full-method literal of the client beside it are written in one run,
// while the Go symbol of the same channel takes the Go type spelling.
//
// spec: §28.1 (N4, the naming law: the proto RPC name stem is the
// channel's identifier)
func TestIdentifierPassWritesTheProtoRowIntoTheServiceDefinition(t *testing.T) {
	t.Parallel()
	root := idTree(t, "proto")
	applyIDPass(t, root, "proto.yaml")
	assertRewritten(t, root, "schemas/lenny-adapter.proto")
	assertRewritten(t, root, "pkg/adapter/client.go")
	service := readFixtureFile(t, filepath.Join(root, "schemas", "lenny-adapter.proto"))
	if !strings.Contains(service, "rpc AdapterEvents(stream AdapterEventsRequest)") {
		t.Errorf("the RPC declaration was not written from the proto RPC row:\n%s", service)
	}
	if strings.Contains(service, "AdapterEventsChannel") {
		t.Errorf("the service definition carries the Go type spelling:\n%s", service)
	}
}

// TestIdentifierPassRefusesACarrierTheFormOfTheSiteRulesOut pins the
// precedence of the two row-selection rules. The form of the site
// decides first and decides unconditionally, and the carrier a register
// entry names resolves what the form leaves open. An entry naming the Go
// type carrier at a gRPC full-method literal would otherwise write the
// Go spelling into the literal, naming a method the service definition
// does not declare, and the run would exit clean because no gate reads
// which spelling the site meant.
//
// spec: §28.1 (N4, the naming law: the proto RPC name stem is the
// channel's identifier)
func TestIdentifierPassRefusesACarrierTheFormOfTheSiteRulesOut(t *testing.T) {
	t.Parallel()
	root := idTree(t, "tree")
	before := treeSnapshot(t, root)
	_, err := applyIDPassErr(t, root, "fail-carrier-overrides-form.yaml")
	if err == nil {
		t.Fatal("the identifier pass resolved a full-method literal from the carrier the entry named")
	}
	abort, ok := pass.AsAbort(err)
	if !ok {
		t.Fatalf("the pass returned %v, which is not an abort", err)
	}
	if abort.Path != "pkg/adapter/holdstate.go" {
		t.Errorf("the abort names %s, want pkg/adapter/holdstate.go", abort.Path)
	}
	for _, names := range []string{"go-symbol", "full-method"} {
		if !strings.Contains(abort.Reason, names) {
			t.Errorf("the abort does not name %s: %s", names, abort.Reason)
		}
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the aborted run left the tree modified")
	}
}

// TestIdentifierPassRewritesASitePathKeyedRegisterCarriesOutsideAKey
// pins the write domain of the two path-keyed registers that sit inside
// the read domain the passes and the gates share. Their keys move on the
// key channel, and every other occurrence they carry, a section title
// and a wildcard glob key here, is an ordinary site resolved through the
// sense register. Leaving those occurrences to the key channel would
// leave a readable file with a retired spelling no pass can write, which
// the identifier-resolution gate and the residual scan both report.
//
// spec: §28.1 (N4, the naming law: the canonical identifier is written
// in every carrier of the channel it denotes)
func TestIdentifierPassRewritesASitePathKeyedRegisterCarriesOutsideAKey(t *testing.T) {
	t.Parallel()
	root := idTree(t, "tree")
	before := treeSnapshot(t, root)
	for _, register := range []string{"tests/change-graph.json", "tests/spec-map.json"} {
		if !strings.Contains(before[register], "LifecycleChannel") && !strings.Contains(before[register], "lifecyclechannel*") {
			t.Fatalf("the fixture %s carries no occurrence outside a key position", register)
		}
	}
	applyIDPass(t, root, "tree.yaml")
	after := treeSnapshot(t, root)
	if !strings.Contains(after["tests/spec-map.json"], "Registers of the RuntimeOpsChannel") {
		t.Errorf("the section title was left at its retired spelling:\n%s", after["tests/spec-map.json"])
	}
	if !strings.Contains(after["tests/change-graph.json"], "pkg/adapter/runtimeops*_test.go") {
		t.Errorf("the wildcard glob key was left at its retired spelling:\n%s", after["tests/change-graph.json"])
	}
	// The keys the move writes are not resolved a second time as sites,
	// so each is written once and reads as the path the file moved to.
	if !strings.Contains(after["tests/change-graph.json"], `"pkg/adapter/runtimeops.go"`) {
		t.Errorf("the moved glob key is not the path the file moved to:\n%s", after["tests/change-graph.json"])
	}
}

// TestIdentifierPassLeavesASingleChannelSpellingAtANonChannelSiteAsItStands
// pins both dispositions of the occurrence-scoped trigger. A spelling
// the naming table maps to exactly one channel still occurs where the
// text is not that channel, here a command-line file argument that
// happens to carry the socket token. With no entry the run aborts rather
// than substituting, and with an entry recording the occurrence as no
// channel the site is left byte-identical while an equivalent occurrence
// in an ordinary carrier is rewritten in the same run.
//
// spec: §28.1 (N4, the naming law: a spelling is rewritten where it
// denotes the channel)
func TestIdentifierPassLeavesASingleChannelSpellingAtANonChannelSiteAsItStands(t *testing.T) {
	t.Parallel()
	const argument = "spec/17_deployment-topology.md"
	t.Run("no entry aborts the run", func(t *testing.T) {
		t.Parallel()
		root := idTree(t, "tree")
		before := treeSnapshot(t, root)
		_, err := applyIDPassErr(t, root, "fail-nonchannel.yaml")
		if err == nil {
			t.Fatal("the identifier pass returned no error at an unresolved non-channel site")
		}
		abort, ok := pass.AsAbort(err)
		if !ok {
			t.Fatalf("the pass returned %v, which is not an abort", err)
		}
		if abort.Path != argument {
			t.Errorf("the abort names %s, want %s", abort.Path, argument)
		}
		if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
			t.Error("the aborted run left the tree modified")
		}
	})
	t.Run("an entry recording no channel leaves the site", func(t *testing.T) {
		t.Parallel()
		root := idTree(t, "tree")
		before := treeSnapshot(t, root)
		diff := applyIDPass(t, root, "tree.yaml")
		after := treeSnapshot(t, root)
		if after[argument] != before[argument] {
			t.Errorf("the non-channel site was rewritten:\n%s", after[argument])
		}
		if membership(diff.Paths())[argument] {
			t.Errorf("the applied diff names %s, whose site the run left standing", argument)
		}
		assertRewritten(t, root, "docs/reference/channels.md")
	})
}

// TestIdentifierPassLeavesEveryWriteExcludedCarrierByteIdentical pins the
// write exclusion, which no gate reads and no gate could report. A
// retired spelling in a staged proposal, in either historical audit
// record, in either root planning document, or in any of the three build
// and queue records is part of what was written at the time, so the run
// leaves it byte-identical and names it in neither the dry run nor the
// applied diff, while an equivalent occurrence in an ordinary carrier is
// rewritten in the same run.
//
// spec: §28.1 (N3, the naming law: the record of a finding is not
// rewritten by the migration that resolves it)
func TestIdentifierPassLeavesEveryWriteExcludedCarrierByteIdentical(t *testing.T) {
	t.Parallel()
	root := idTree(t, "tree")
	excluded := []string{
		"proposals/0001_example.md",
		"BUILD-GAPS.md",
		"TEST-GAPS.md",
		"gateway-runtime-comms.md",
		"gateway-runtime-comms-remediation.md",
		"BUILD-PLAN.md",
		"BUILD-PROGRESS.md",
		"PROPOSAL-QUEUE.md",
	}
	before := treeSnapshot(t, root)
	planned, err := planIDPass(t, root, "tree.yaml")
	if err != nil {
		t.Fatalf("plan the identifier pass: %v", err)
	}
	applied := applyIDPass(t, root, "tree.yaml")
	after := treeSnapshot(t, root)
	inPlan, inApplied := membership(planned.Paths()), membership(applied.Paths())
	for _, target := range excluded {
		if before[target] == "" {
			t.Fatalf("the fixture tree carries no %s", target)
		}
		if after[target] != before[target] {
			t.Errorf("%s was rewritten:\n%s", target, after[target])
		}
		if inPlan[target] {
			t.Errorf("the dry-run output names the excluded %s", target)
		}
		if inApplied[target] {
			t.Errorf("the applied diff names the excluded %s", target)
		}
	}
	assertRewritten(t, root, "docs/reference/channels.md")
}

// TestIdentifierPassRekeysEveryPathKeyedRegisterAMoveInvalidates reads
// each register after the run rather than asking a validator, because
// the map validator does not existence-check the `schemas` paths and no
// check reads a citation baseline at all, so a stale key there is
// invisible to every gate until the gate that owns it fires on the wrong
// file. Without the rekey the line-citation ratchet fires on a rename
// that changed no citation, and every baselined non-resolving citation
// under the old path reappears as a resolver failure.
//
// spec: §28.1 (N4, the naming law: the run that moves a carrier moves
// every key written for it)
func TestIdentifierPassRekeysEveryPathKeyedRegisterAMoveInvalidates(t *testing.T) {
	t.Parallel()
	root := idTree(t, "tree")
	before := treeSnapshot(t, root)
	diff := applyIDPass(t, root, "tree.yaml")
	after := treeSnapshot(t, root)
	inDiff := membership(diff.Paths())
	// The registers are read out of the tree rather than named, so a
	// register the tree gains joins the case by construction, as it joins
	// the rekey domain by construction. The skip-reason baseline and the
	// residual register beside it are keyed by path as much as the
	// citation baselines are: a member left under the moved file's old
	// path fails a gate that never adds an entry, so it has no route back
	// to green.
	registers := keyWritableIn(t, root)
	inDomain := membership(registers)
	for _, want := range []string{
		"tests/change-graph.json",
		"tests/registers/line-citation-resolution.yaml",
		"tests/registers/line-citations.yaml",
		"tests/registers/residual-skip-reasons.yaml",
		"tests/registers/skip-reasons.yaml",
		"tests/spec-map.json",
	} {
		if !inDomain[want] {
			t.Fatalf("the rekey domain over the fixture tree omits %s", want)
		}
	}
	for _, register := range registers {
		if before[register] == "" {
			t.Fatalf("the fixture tree carries no %s", register)
		}
		if !inDiff[register] {
			t.Errorf("the applied diff omits the path-keyed register %s", register)
		}
		if strings.Contains(after[register], "lifecyclechannel") {
			t.Errorf("%s still carries a key of the file the run moved:\n%s", register, after[register])
		}
		assertRewritten(t, root, register)
	}
	// The spec map carries three key positions a rename invalidates: the
	// package path, the schemas entry no check existence-checks, and the
	// `::<symbol>` reference, which is keyed by symbol rather than by
	// path and is resolved against its declaring file by a tier-0 check.
	spec := after["tests/spec-map.json"]
	for _, want := range []string{
		"pkg/adapter/runtimeops.go",
		"schemas/runtime-ops-events.schema.json",
		"pkg/adapter/runtimeops_test.go::TestRuntimeOpsChannelServes",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("the spec map does not carry %s after the run:\n%s", want, spec)
		}
	}
}

// TestIdentifierPassFailsWhenARegisterStillNamesAMovedFile pins the
// stale-key check that makes the rekey channel fail closed. A citation
// baseline is outside the read domain the passes share, so the key
// rewrite is the only channel that reaches it, and a moved path it
// writes inside a wider value such as a citation text keeps pointing at
// a file the run has moved. The run stops with the tree unchanged and
// the operator corrects the value, rather than completing and leaving
// the gates to fire on the wrong file afterwards.
//
// spec: §28.1 (N4, the naming law: the run that moves a carrier moves
// every key written for it)
func TestIdentifierPassFailsWhenARegisterStillNamesAMovedFile(t *testing.T) {
	t.Parallel()
	root := idTree(t, "fail/stalekey")
	before := treeSnapshot(t, root)
	_, err := applyIDPassErr(t, root, "fail-stalekey.yaml")
	if err == nil {
		t.Fatal("the identifier pass completed with a register naming a file it moved")
	}
	if !strings.Contains(err.Error(), "tests/registers/line-citation-resolution.yaml") {
		t.Errorf("the failure does not name the register: %v", err)
	}
	if !strings.Contains(err.Error(), "pkg/adapter/lifecyclechannel.go") {
		t.Errorf("the failure does not name the stale key: %v", err)
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the failed run left the tree modified")
	}
}

// TestIdentifierPassDryRunOutputEqualsTheAppliedDiff pins the entry
// criterion for applying the pass. This pass's applied change is the
// largest and the hardest to reverse, because it moves files and edits
// the registers keyed by their paths, and no other case observes a
// divergence: the register cases read the tree after the run, and the
// identifier-resolution gate runs after the pass is applied.
//
// spec: §28.1 (N4, the naming law: the rename is reviewed before it is
// applied)
func TestIdentifierPassDryRunOutputEqualsTheAppliedDiff(t *testing.T) {
	t.Parallel()
	root := idTree(t, "tree")
	before := treeSnapshot(t, root)
	planned, err := planIDPass(t, root, "tree.yaml")
	if err != nil {
		t.Fatalf("plan the identifier pass: %v", err)
	}
	if len(planned.Files) == 0 {
		t.Fatal("the dry run reports no work over the fixture tree")
	}
	moves := 0
	for _, f := range planned.Files {
		if f.To != "" {
			moves++
		}
	}
	if moves == 0 {
		t.Fatal("the dry run reports no move over a tree carrying a file named after a retired spelling")
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the dry run wrote to the tree")
	}
	applied := applyIDPass(t, root, "tree.yaml")
	if !planned.Equal(applied) {
		t.Fatalf("the applied diff differs from the dry run: %v vs %v", planned.Paths(), applied.Paths())
	}
}

// TestIdentifierPassRejectsAMalformedOrMissingSenseRegister pins that the
// pass refuses to run rather than reporting the zero substitutions of a
// completed migration. A register that loaded as empty would abort at the
// first occurrence in the tree, which reads as a register nobody seeded,
// and over an already-rewritten tree it would report a migration it never
// performed.
//
// spec: §28.1 (N4, the naming law: the rename is driven by the register
// of senses)
func TestIdentifierPassRejectsAMalformedOrMissingSenseRegister(t *testing.T) {
	t.Parallel()
	root := idTree(t, "tree")
	for _, tc := range []struct {
		name     string
		register string
	}{
		{"a missing register", "absent.yaml"},
		{"a malformed register", "malformed.yaml"},
		{"a register with no entries block", "no-entries-block.yaml"},
		{"a register with no entry", "empty-entries.yaml"},
		{"a register of another kind", "wrong-kind.yaml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := identifier.New(scope.DirLister(root), scope.DirReader(root))
			if err := r.LoadRegister(filepath.Join(fixtureIDPass, "registers", tc.register)); err == nil {
				t.Fatalf("the identifier pass loaded %s", tc.name)
			}
			if _, err := idHarness(root).Plan(context.Background(), r); err == nil {
				t.Error("a pass with no register loaded reported a plan")
			}
		})
	}
}

// TestIdentifierPassRejectsEverySenseEntrySchemaDefect pins the entry
// schema the substitution rests on: a site key naming one position,
// which is either one occurrence of the content or the file name, and
// one disposition, which is either the channel the site denotes or the
// record that the site is no channel. An entry stating both positions is
// two entries, an entry stating both dispositions says at once that the
// site is a channel and that it is not, and a carrier named on an entry
// that records no channel selects nothing.
//
// spec: §28.1 (N4, the naming law: each site's sense is recorded once)
func TestIdentifierPassRejectsEverySenseEntrySchemaDefect(t *testing.T) {
	t.Parallel()
	root := idTree(t, "tree")
	for _, tc := range []struct {
		name     string
		register string
	}{
		{"an entry with no file", "invalid-no-file.yaml"},
		{"an entry stating no position", "invalid-no-position.yaml"},
		{"an entry stating both positions", "invalid-both-positions.yaml"},
		{"an entry stating no disposition", "invalid-no-disposition.yaml"},
		{"an entry stating both dispositions", "invalid-both-dispositions.yaml"},
		{"an entry naming a channel that is no identifier", "invalid-channel.yaml"},
		{"an entry naming a carrier outside the closed set", "invalid-carrier.yaml"},
		{"a position declared twice", "invalid-duplicate.yaml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := identifier.New(scope.DirLister(root), scope.DirReader(root))
			if err := r.LoadRegister(filepath.Join(fixtureIDPass, "registers", tc.register)); err == nil {
				t.Errorf("the identifier pass loaded %s", tc.name)
			}
		})
	}
}

// TestIdentifierPassFailsAnEntryTheNamingTableOrTheTreeDoesNotBackPins
// the two directions the register is checked in. A channel the naming
// table does not carry states no spelling, so the site it resolves has
// no substitution and the operator would be sent to the register rather
// than to the table. An entry no site in the tree claims is written
// nowhere, so the run would exit zero having reported a substitution it
// never performed.
//
// spec: §28.1 (N4, the naming law: every entry resolves a site of the
// tree to a spelling the table states)
func TestIdentifierPassFailsAnEntryTheNamingTableOrTheTreeDoesNotBack(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		register string
		names    string
	}{
		{"a channel the naming table does not carry", "fail-undeclared-channel.yaml", "CH-NOSUCHCHANNEL"},
		{"an occurrence the file does not carry", "fail-unclaimed.yaml", "occurrence 4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := idTree(t, "tree")
			before := treeSnapshot(t, root)
			_, err := applyIDPassErr(t, root, tc.register)
			if err == nil {
				t.Fatalf("the identifier pass accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.names) {
				t.Errorf("the failure does not name %s: %v", tc.names, err)
			}
			if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
				t.Error("the failed run left the tree modified")
			}
		})
	}
}

// TestIdentifierPassFailsWhenTheTreeCarriesNoNamingTable pins that the
// pass reads its spellings out of the specification rather than from a
// list of its own. A tree with no naming table resolves no site, so a
// run over it would abort at every occurrence, which reads as a register
// nobody seeded rather than as a tree the pass cannot run against yet.
//
// spec: §28.1 (N4, the naming law: the carrier spellings are stated in
// the specification)
func TestIdentifierPassFailsWhenTheTreeCarriesNoNamingTable(t *testing.T) {
	t.Parallel()
	root := idTree(t, "tree")
	if err := os.Remove(filepath.Join(root, "spec", "28_communication-channels.md")); err != nil {
		t.Fatalf("remove the naming table: %v", err)
	}
	_, err := planIDPass(t, root, "tree.yaml")
	if err == nil {
		t.Fatal("the identifier pass planned a run over a tree with no naming table")
	}
	if !strings.Contains(err.Error(), "naming table") {
		t.Errorf("the failure does not name the naming table: %v", err)
	}
}

// TestTheDriverCarriesTheIdentifierPass pins that the pass the driver
// runs is the built one, so a run of the engine over a checkout performs
// the rename rather than reporting a pass that is not built.
//
// spec: §28.1 (N4, the naming law: the rename is performed by the
// committed tooling)
func TestTheDriverCarriesTheIdentifierPass(t *testing.T) {
	t.Parallel()
	built := builtPasses(repoRoot(t))
	r, ok := built[scope.Identifier]
	if !ok {
		t.Fatal("the driver carries no identifier pass")
	}
	if r.Pass() != scope.Identifier {
		t.Errorf("the built pass names the %s write domain", r.Pass())
	}
}

// TestABaselinedCitationFollowsTheFileTheIdentifierPassRenamed pins the
// interaction between the rekey channel and the citation resolver, which
// the register-contents cases do not supply because the resolver reads
// the tree rather than the register. A non-resolving citation carried in
// the resolution baseline under the file it was written for still passes
// after the run that moved that file, and fails when the rekey is
// suppressed, which is what makes the baseline follow the file rather
// than the path.
//
// spec: §28.1 (N8, the citation rule: an exemption is keyed to the file
// the citation was written in)
func TestABaselinedCitationFollowsTheFileTheIdentifierPassRenamed(t *testing.T) {
	t.Parallel()
	const baseline = "tests/registers/line-citation-resolution.yaml"
	root := idTree(t, "tree", "resolver")
	stale := readFixtureFile(t, filepath.Join(root, filepath.FromSlash(baseline)))
	applyIDPass(t, root, "tree.yaml")

	resolver := gate.NewResolutionOver(scope.DirLister(root), scope.DirReader(root), dirWriterFor(root))
	report, err := resolver.Run(context.Background())
	if err != nil {
		t.Fatalf("run the citation resolver after the rename: %v", err)
	}
	if report.NonResolving == 0 {
		t.Fatal("the fixture carries no non-resolving citation, so the case proves nothing")
	}
	if len(report.Failures) > 0 {
		t.Errorf("the baseline did not follow the renamed file: %v", report.Failures)
	}

	// With the rekey suppressed, the same citation is a failure under the
	// new path, which is the outcome the second write channel exists to
	// prevent.
	if err := dirWriterFor(root)(baseline, []byte(stale)); err != nil {
		t.Fatalf("restore the pre-run baseline: %v", err)
	}
	suppressed, err := gate.NewResolutionOver(scope.DirLister(root), scope.DirReader(root), dirWriterFor(root)).
		Run(context.Background())
	if err != nil {
		t.Fatalf("run the citation resolver with the rekey suppressed: %v", err)
	}
	if len(suppressed.Failures) == 0 {
		t.Error("the resolver passed with the baseline keyed to the path the run moved away from")
	}
}

// TestIdentifierPassRefusesASiteNeitherTheFormNorTheRegisterResolves
// pins the second half of the row-selection rule. A channel that states
// the same retired spelling in more carriers than the site's form
// selects between is resolved by the carrier the register entry names,
// and an occurrence naming none aborts rather than taking the first row:
// both spellings are canonical, so no gate downstream reads which of
// them the site meant.
//
// spec: §28.1 (N4, the naming law: each carrier of a channel has one
// canonical spelling)
func TestIdentifierPassRefusesASiteNeitherTheFormNorTheRegisterResolves(t *testing.T) {
	t.Parallel()
	root := idTree(t, "tree")
	before := treeSnapshot(t, root)
	_, err := applyIDPassErr(t, root, "fail-ambiguous.yaml")
	if err == nil {
		t.Fatal("the identifier pass resolved a site two naming-table rows answer for")
	}
	abort, ok := pass.AsAbort(err)
	if !ok {
		t.Fatalf("the pass returned %v, which is not an abort", err)
	}
	if abort.Path != "pkg/adapter/holdstate.go" {
		t.Errorf("the abort names %s, want pkg/adapter/holdstate.go", abort.Path)
	}
	for _, carrier := range []string{"go-symbol", "metric"} {
		if !strings.Contains(abort.Reason, carrier) {
			t.Errorf("the abort does not name the %s row: %s", carrier, abort.Reason)
		}
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Error("the aborted run left the tree modified")
	}
	// The same site resolves once the entry names the carrier, which is
	// what the fixture register does.
	resolved := idTree(t, "tree")
	applyIDPass(t, resolved, "tree.yaml")
	assertRewritten(t, resolved, "pkg/adapter/holdstate.go")
}

// TestApplyPutsAMovedFileBackWhenALaterWriteFails pins the rollback of
// the move channel. A move is a write of the new path and a removal of
// the old one, so a failure later in the diff has to restore both halves:
// a tree left carrying the file under both names, or under neither, is
// neither the pre-run tree nor the applied one.
func TestApplyPutsAMovedFileBackWhenALaterWriteFails(t *testing.T) {
	t.Parallel()
	const (
		from = "pkg/carrier/carrier.go"
		to   = "pkg/carrier/renamed.go"
	)
	root := copyFixtureTree(t)
	h := pass.NewHarnessOver(scope.DirLister(root), scope.DirReader(root),
		failingWriter(root, "tests/registers/line-citations.yaml", nil))
	h.Remove = scope.DirRemover(root)
	r := &renamingRewriter{
		suffixRewriter: suffixRewriter{p: scope.Identifier, suffix: "// rewritten\n"},
		from:           from,
		to:             to,
		move:           true,
	}
	before := treeSnapshot(t, root)
	if _, err := h.Apply(context.Background(), r); err == nil {
		t.Fatal("Apply returned no error when a later write failed")
	}
	if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
		t.Errorf("the rolled-back tree is not the pre-run tree: %v", diffPaths(before, got))
	}
}

// TestApplyRefusesAMoveWithNoRemoverAndAMoveOntoAnExistingFile pins the
// two ways a move ends with a file gone. A harness with no remover would
// leave the content under both names with the run reporting a clean
// move, and a destination the tree already carries, or that a second
// move also takes, overwrites a file the pass never read.
func TestApplyRefusesAMoveWithNoRemoverAndAMoveOntoAnExistingFile(t *testing.T) {
	t.Parallel()
	const from = "pkg/carrier/carrier.go"
	t.Run("no remover", func(t *testing.T) {
		t.Parallel()
		root := copyFixtureTree(t)
		h := pass.NewHarnessOver(scope.DirLister(root), scope.DirReader(root), dirWriterFor(root))
		r := &renamingRewriter{
			suffixRewriter: suffixRewriter{p: scope.Identifier, suffix: "// rewritten\n"},
			from:           from,
			to:             "pkg/carrier/renamed.go",
			move:           true,
		}
		if _, err := h.Apply(context.Background(), r); err == nil {
			t.Fatal("Apply performed a move with no remover")
		}
	})
	t.Run("a destination the tree carries", func(t *testing.T) {
		t.Parallel()
		root := copyFixtureTree(t)
		h := pass.NewHarnessOver(scope.DirLister(root), scope.DirReader(root), dirWriterFor(root))
		h.Remove = scope.DirRemover(root)
		r := &renamingRewriter{
			suffixRewriter: suffixRewriter{p: scope.Identifier, suffix: "// rewritten\n"},
			from:           from,
			to:             "pkg/carrier/toolcache.conf",
			move:           true,
		}
		before := treeSnapshot(t, root)
		if _, err := h.Plan(context.Background(), r); err == nil {
			t.Fatal("Plan admitted a move onto a file the tree carries")
		}
		if got := treeSnapshot(t, root); !sameSnapshot(before, got) {
			t.Error("the refused plan wrote to the tree")
		}
	})
}

// diffPaths names the paths two snapshots disagree on, so a rollback
// failure reports what was left behind.
func diffPaths(a, b map[string]string) []string {
	var out []string
	for p, content := range a {
		if b[p] != content {
			out = append(out, p)
		}
	}
	for p := range b {
		if _, ok := a[p]; !ok {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// TestIdentifierPassRejectsEveryNamingTableRowDefect pins that a row the
// reader cannot use fails the run rather than being skipped. A skipped
// row is a retired spelling with no substitution, so the pass aborts at
// every site carrying it and sends the operator to the sense register
// rather than to the table the defect is in.
//
// spec: §28.1 (N4, the naming law: the carrier spellings are stated in
// the specification)
func TestIdentifierPassRejectsEveryNamingTableRowDefect(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		row  string
	}{
		{"a channel cell that is no identifier", "| runtime ops | socket | `@lenny-lifecycle` | `@lenny-runtime-ops` |"},
		{"a carrier outside the closed set", "| `CH-RUNTIMEOPS` | prose | `@lenny-lifecycle` | `@lenny-runtime-ops` |"},
		{"a row stating no canonical spelling", "| `CH-RUNTIMEOPS` | socket | `@lenny-lifecycle` |  |"},
		{"a row retiring a spelling to itself", "| `CH-RUNTIMEOPS` | socket | `@lenny-lifecycle` | `@lenny-lifecycle` |"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := idTree(t, "tree")
			table := filepath.Join(root, "spec", "28_communication-channels.md")
			content := readFixtureFile(t, table)
			if err := os.WriteFile(table, []byte(content+tc.row+"\n"), 0o644); err != nil {
				t.Fatalf("write the naming table: %v", err)
			}
			_, err := planIDPass(t, root, "tree.yaml")
			if err == nil {
				t.Fatalf("the identifier pass accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), "naming table") {
				t.Errorf("the failure does not name the naming table: %v", err)
			}
		})
	}
}

// The broad citation predicate is what the residual scan of the two
// line-citation classes ranges over. It is wider than the form the
// grammar reads on purpose: a spelling the form misses is still selected
// and reported for triage rather than passing unread. These cases pin
// both halves of that relation, because the residual is the difference
// between the two and a predicate narrower than the form would make the
// difference empty for the wrong reason.

// TestBroadPredicateSelectsEveryCitationTheFormReads pins that the
// predicate covers the form, so subtracting the citations the form read
// leaves only the spellings it missed.
func TestBroadPredicateSelectsEveryCitationTheFormReads(t *testing.T) {
	t.Parallel()
	// Each spelling is held in a fixture rather than in this source, so
	// the tooling's own file does not carry the form its gates report.
	for _, name := range []string{
		"broad-enumerated-keyword.txt",
		"broad-enumerated-plural.txt",
		"broad-enumerated-list.txt",
		"broad-enumerated-colon.txt",
		"broad-enumerated-path.txt",
		"broad-enumerated-prose.txt",
		"broad-enumerated-comma.txt",
		"broad-enumerated-paren.txt",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			content := citationFixture(t, name)
			read := citation.Find(content)
			if len(read) != 1 {
				t.Fatalf("the citation form read %d occurrence(s) of %s, want one", len(read), name)
			}
			broad := citation.FindBroad(content)
			if len(broad) != 1 {
				t.Fatalf("the broad predicate selected %d occurrence(s) of %s, want one", len(broad), name)
			}
			// The two matchers bound a citation differently, so the
			// relation asserted is overlap rather than equality.
			if broad[0].Offset >= read[0].Offset+len(read[0].Raw) || read[0].Offset >= broad[0].End {
				t.Errorf("the broad occurrence at [%d,%d) does not overlap the citation at [%d,%d)",
					broad[0].Offset, broad[0].End, read[0].Offset, read[0].Offset+len(read[0].Raw))
			}
		})
	}
}

// TestBroadPredicateSelectsASpellingTheFormDoesNotRead pins the residual
// itself: an occurrence the form leaves unread is still selected, and it
// is reported with the text a register is keyed by. What the form leaves
// unread is the keyword written in another case, the keyword standing at
// the tail of a word rather than at its head, and a run carrying one of
// the bytes the form holds out of it, which is where the format verb of
// an assertion message and the escape of a string literal fall. Each
// spelling and each expectation is held in a fixture for the reason
// fixtureCitations states.
func TestBroadPredicateSelectsASpellingTheFormDoesNotRead(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, fixture string }{
		{"a keyword written in another case", "broad-residual-case.txt"},
		{"a keyword standing at the tail of a word", "broad-residual-wordtail.txt"},
		{"a format verb between the reference and the keyword", "broad-residual-format.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			content := citationFixture(t, tc.fixture)
			if read := citation.Find(content); len(read) != 0 {
				t.Fatalf("the citation form read %q, so it is no residual", read[0].Text)
			}
			broad := citation.FindBroad(content)
			if len(broad) != 1 {
				t.Fatalf("the broad predicate selected %d occurrence(s) of %s, want one", len(broad), tc.fixture)
			}
			if want := wantCitation(t, tc.fixture); broad[0].Text != want {
				t.Errorf("the occurrence reads %q, want %q", broad[0].Text, want)
			}
		})
	}
}

// TestBroadPredicateReadsAWrappedOccurrenceAsOneLine pins the adjacency
// rule: the continuation join is tolerated, so a citation wrapped across
// two comment lines is one occurrence whose text reads as one line. A
// line-oriented scan would see a reference with no line-number token on
// the first line and a token with no reference on the second, and report
// neither.
func TestBroadPredicateReadsAWrappedOccurrenceAsOneLine(t *testing.T) {
	t.Parallel()
	const fixture = "broad-residual-wrapped.txt"
	broad := citation.FindBroad(citationFixture(t, fixture))
	if len(broad) != 1 {
		t.Fatalf("the broad predicate selected %d occurrence(s) across the wrap, want one", len(broad))
	}
	if want := wantCitation(t, fixture); broad[0].Text != want {
		t.Errorf("the wrapped occurrence reads %q, want %q", broad[0].Text, want)
	}
	if !strings.Contains(broad[0].Raw, "\n") {
		t.Errorf("the raw span %q does not carry the wrap it was read across", broad[0].Raw)
	}
}

// TestBroadPredicateRejectsAReferenceTakenOutOfALongerWord pins the left
// boundary: a file-name spelling cut out of a longer digit run names no
// section, so the predicate does not report the prose that carries it.
func TestBroadPredicateRejectsAReferenceTakenOutOfALongerWord(t *testing.T) {
	t.Parallel()
	for _, content := range []string{
		"// build 2004_release.md line 12 of the log\n",
		"// a note with no line-number token about §4.6 of the section\n",
		"// §4.6 states the rule, and 12 unrelated sentences follow it in the same paragraph of prose\n",
	} {
		t.Run(content, func(t *testing.T) {
			t.Parallel()
			if broad := citation.FindBroad(content); len(broad) != 0 {
				t.Errorf("the broad predicate selected %q", broad[0].Text)
			}
		})
	}
}

// TestMarkerDeclaredAnswersNotGeneratedForAProducerOutput pins the
// enumeration of the generated-artifact class: the marker disjuncts
// alone answer for a file, so a producer output that declares nothing is
// what the residual scan reports and a file that declares itself
// generated is not.
func TestMarkerDeclaredAnswersNotGeneratedForAProducerOutput(t *testing.T) {
	t.Parallel()
	const target = "charts/lenny/crds/lenny.dev_widgets.yaml"
	for _, tc := range []struct {
		name    string
		content string
		marker  scope.Disjunct
	}{
		{"a manifest with no marker", "apiVersion: apiextensions.k8s.io/v1\n", scope.NotGenerated},
		{"a manifest declaring itself generated", "# Code generated by controller-gen. DO NOT EDIT.\napiVersion: v1\n", scope.HeaderMarker},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			read := func(p string) ([]byte, error) {
				if p != target {
					return nil, fs.ErrNotExist
				}
				return []byte(tc.content), nil
			}
			// The whole rule selects the file either way, because a
			// producer names it.
			disjunct, err := scope.Generated(target, read)
			if err != nil {
				t.Fatalf("apply the generated-artifact rule: %v", err)
			}
			if disjunct != scope.ProducerOutput {
				t.Fatalf("the rule answered %q, want the producer disjunct", disjunct)
			}
			marker, err := scope.MarkerDeclared(target, read)
			if err != nil {
				t.Fatalf("read the generation marker: %v", err)
			}
			if marker != tc.marker {
				t.Errorf("the marker disjuncts answered %q, want %q", marker, tc.marker)
			}
		})
	}
}

// TestEveryResidualRegisterIsAnOrdinaryMemberOfTheSharedReadDomain pins
// that the exclusion which keeps the residual scan from reading a
// register as tree content belongs to that scan rather than to the read
// domain the gates and the passes share.
//
// A residual register carries the text of every member it triages and a
// reason for each, and the naming lint, the identifier-resolution gate,
// the citation resolver, and the ratchet all read it as tree content. A
// register named in the shared read exclusion would be read by none of
// them and, through the write domain that exclusion also governs,
// written by no pass, so a reserved phrase, a retired identifier
// spelling, or a citation written there would have no route out. The
// residual scan still excludes every residual register from every
// class's domain, because a scan that read one would report the copy it
// holds of each member.
func TestEveryResidualRegisterIsAnOrdinaryMemberOfTheSharedReadDomain(t *testing.T) {
	t.Parallel()
	read := func(string) ([]byte, error) { return []byte("kind: residual-register\nversion: 1\n"), nil }
	for _, c := range scope.Classes() {
		register := c.ResidualRegister()
		if !scope.Readable(register) {
			t.Errorf("the shared read domain excludes %s, so no gate reads the members it triages", register)
		}
		writable, err := scope.Writable(scope.Line, register, read)
		if err != nil {
			t.Fatalf("read the write domain for %s: %v", register, err)
		}
		if !writable {
			t.Errorf("no pass may write %s, so a site recorded there has no route out", register)
		}
		own, err := scope.ReadableForClass(c, register)
		if err != nil {
			t.Fatalf("read the %s class domain: %v", c, err)
		}
		if own {
			t.Errorf("the %s class reads its own register %s as tree content, so its seeding does not converge",
				c, register)
		}
		// No sibling class reads it either. Two classes whose predicates
		// overlap, as the two line-citation classes do, each hold a copy of
		// the other's member text, so a sibling that read it would triage
		// every member twice and would keep matching after the pass had
		// rewritten every carrier site, leaving the in-class entry that
		// recorded the member unremovable.
		for _, other := range scope.Classes() {
			if other == c {
				continue
			}
			sibling, err := scope.ReadableForClass(other, register)
			if err != nil {
				t.Fatalf("read the %s class domain: %v", other, err)
			}
			if sibling {
				t.Errorf("the %s class reads the %s residual register %s as tree content, so the two registers pin each other",
					other, c, register)
			}
		}
	}
}
