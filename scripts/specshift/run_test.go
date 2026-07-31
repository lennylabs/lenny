// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/pass"
	"github.com/lennylabs/lenny/scripts/specshift/register"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

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

// TestGeneratedRuleFailsWithoutAReader pins that the rule reports a
// missing reader rather than answering that a file is an ordinary
// carrier.
func TestGeneratedRuleFailsWithoutAReader(t *testing.T) {
	t.Parallel()
	if _, err := scope.Generated("pkg/carrier/carrier.go", nil); err == nil {
		t.Fatal("Generated with no reader returned no error")
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

// TestRunFailsOnAMissingRegisterRatherThanReportingZeroWork pins the same
// outcome at the command line, where a run that reported no work would
// read as a completed migration.
func TestRunFailsOnAMissingRegisterRatherThanReportingZeroWork(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	err := run(context.Background(), []string{
		"-root", repoRoot(t),
		"-pass", "line",
		"-register", filepath.Join(fixtureRegisters, "absent.yaml"),
	}, &out)
	if err == nil {
		t.Fatal("run with a missing register returned no error")
	}
	if out.Len() != 0 {
		t.Errorf("run with a missing register wrote %q", out.String())
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
		"-register", filepath.Join(fixtureRegisters, "valid.yaml"),
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
// optionally aborts on one named file at one named line.
type suffixRewriter struct {
	p         scope.Pass
	suffix    string
	abortPath string
	abortLine int
}

func (s *suffixRewriter) Pass() scope.Pass { return s.p }

func (s *suffixRewriter) Rewrite(_ context.Context, path string, content []byte) ([]byte, error) {
	if path == s.abortPath {
		return nil, fmt.Errorf("resolve %s: %w", path,
			&pass.Abort{Path: path, Line: s.abortLine, Reason: "no register entry for this site"})
	}
	return append(append([]byte(nil), content...), []byte(s.suffix)...), nil
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
