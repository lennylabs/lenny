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
	for _, p := range []scope.Pass{scope.Name, scope.Identifier} {
		domain, err := scope.ClassReadDomain(context.Background(), list, p)
		if err != nil {
			t.Fatalf("ClassReadDomain(%s): %v", p, err)
		}
		in := membership(domain)
		for _, rec := range records {
			if in[rec] {
				t.Errorf("%s class read domain admits %s", p, rec)
			}
		}
		// The generated artifacts stay in the class read domain: the
		// residual scan ranges wider than the pass writes.
		if !in["charts/lenny/crds/lenny.dev_runtimes.yaml"] {
			t.Errorf("%s class read domain omits a generated artifact", p)
		}
		if !in["pkg/carrier/carrier.go"] {
			t.Errorf("%s class read domain omits the ordinary carrier", p)
		}
	}
	for _, p := range []scope.Pass{scope.Anchor, scope.Line} {
		domain, err := scope.ClassReadDomain(context.Background(), list, p)
		if err != nil {
			t.Fatalf("ClassReadDomain(%s): %v", p, err)
		}
		in := membership(domain)
		for _, rec := range records {
			if !in[rec] {
				t.Errorf("%s class read domain omits %s", p, rec)
			}
		}
	}
	if _, err := scope.ClassReadDomain(context.Background(), list, scope.Pass("reduction")); err == nil {
		t.Error("ClassReadDomain with an unknown class returned no error")
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
