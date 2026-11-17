// SPDX-License-Identifier: MIT

package annotation

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// RatchetBaselinePath is the repo-relative path of the register that
// holds each Go package's count of section-number citations.
const RatchetBaselinePath = "tests/registers/spec-section-citations.yaml"

// The declaration the register carries. Like the line-citation ratchet's
// baseline, it is a baseline rather than an exception register: a count
// has no owner and no expiry, and its route to zero is the migration of
// the package's area to identifier citations.
const (
	ratchetBaselineKind    = "spec-section-citation-count-baseline"
	ratchetBaselineVersion = 1
)

// PackageCount is one package directory's count of section-number
// citations in its `// spec:` annotations.
type PackageCount struct {
	Package string
	Count   int
}

// String renders the count for a report.
func (c PackageCount) String() string { return fmt.Sprintf("%s: %d", c.Package, c.Count) }

// RatchetFailure is one package whose count the register does not allow.
type RatchetFailure struct {
	Package string
	// Registered is the count the register carries, and is zero when
	// Absent is set.
	Registered int
	// Counted is the count the run read from the package.
	Counted int
	// Absent reports that the register carries no count for the package.
	Absent bool
}

// String renders the failure, naming the package and both counts.
func (f RatchetFailure) String() string {
	if f.Absent {
		return fmt.Sprintf("%s: %d section-number citation(s) in a package %s carries no count for",
			f.Package, f.Counted, RatchetBaselinePath)
	}
	return fmt.Sprintf("%s: %d section-number citation(s) where %s carries %d",
		f.Package, f.Counted, RatchetBaselinePath, f.Registered)
}

// RatchetReport is what one run of the ratchet measured.
type RatchetReport struct {
	// Files is the number of Go files read.
	Files int
	// Citations is the number of section-number citations read across
	// them.
	Citations int
	// Carried is how many package counts the register held when the run
	// loaded it.
	Carried int
	// Held is how many of those packages carry the count the register
	// records.
	Held int
	// Lowered is every package whose count fell, with the count the run
	// read. A package that carries no citation any more, or that left the
	// tree, falls to zero and loses its entry.
	Lowered []PackageCount
	// Failures is every package whose count rose or that the register
	// carries no count for.
	Failures []RatchetFailure
}

// Ratchet holds each Go package's count of section-number citations in
// `// spec:` annotations at or below the count its register carries. A
// count that falls is written back to the register in the same run, so
// the author commits the lowered register with the change that retired
// the citations.
//
// Its dependencies are injected so a test drives it over a fixture tree.
type Ratchet struct {
	List     scope.Lister
	Read     scope.FileReader
	Write    func(path string, content []byte) error
	Baseline string
}

// NewRatchet returns the ratchet over the tracked tree at root.
func NewRatchet(root string) *Ratchet {
	return NewRatchetOver(scope.GitLister(root), scope.DirReader(root), scope.DirWriter(root))
}

// NewRatchetOver returns the ratchet with each dependency supplied.
func NewRatchetOver(list scope.Lister, read scope.FileReader, write func(string, []byte) error) *Ratchet {
	return &Ratchet{List: list, Read: read, Write: write, Baseline: RatchetBaselinePath}
}

// Run counts the section-number citations of every Go package in the
// read domain and compares each count with the register.
//
// A run that could not inspect the tree, or could not load the register,
// returns an error rather than an empty report, so a gate never
// certifies content it did not read.
func (g *Ratchet) Run(ctx context.Context) (RatchetReport, error) {
	var rep RatchetReport
	if g.List == nil || g.Read == nil {
		return rep, fmt.Errorf("section-citation ratchet: a lister and a reader are required")
	}
	counted, files, err := CountSectionCitations(ctx, g.List, g.Read)
	if err != nil {
		return rep, err
	}
	carried, err := LoadRatchetBaseline(g.Read, g.Baseline)
	if err != nil {
		return rep, fmt.Errorf("section-citation ratchet: %w", err)
	}
	rep.Files = files
	rep.Carried = len(carried)
	for _, n := range counted {
		rep.Citations += n
	}
	kept := compare(counted, carried, &rep)
	if len(rep.Lowered) > 0 {
		if err := WriteRatchetBaseline(g.Write, g.Baseline, kept); err != nil {
			return rep, fmt.Errorf("section-citation ratchet: %w", err)
		}
	}
	return rep, nil
}

// CountSectionCitations returns each Go package directory's count of
// section-number citations, and the number of Go files read. A package
// that carries none has no entry. A walk that read no Go file is an
// error.
func CountSectionCitations(ctx context.Context, list scope.Lister, read scope.FileReader) (map[string]int, int, error) {
	domain, err := scope.ReadDomain(ctx, list)
	if err != nil {
		return nil, 0, fmt.Errorf("section-citation ratchet: %w", err)
	}
	counted := map[string]int{}
	files := 0
	for _, target := range domain {
		if !strings.HasSuffix(target, ".go") {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, 0, fmt.Errorf("section-citation ratchet: %w", err)
		}
		src, err := read(target)
		if err != nil {
			return nil, 0, fmt.Errorf("section-citation ratchet: read %s: %w", target, err)
		}
		files++
		n := 0
		for _, a := range Annotations(Comments(target, src)) {
			n += SectionCitations(a.Payload)
		}
		if n > 0 {
			counted[path.Dir(target)] += n
		}
	}
	if files == 0 {
		return nil, 0, fmt.Errorf("section-citation ratchet: the read domain holds no Go file, so the run read nothing")
	}
	return counted, files, nil
}

// compare measures each counted package against the register, fills in
// the report, and returns the counts the rewritten register holds. A
// package whose count rose keeps its registered count, so the rise is
// reported rather than absorbed.
func compare(counted, carried map[string]int, rep *RatchetReport) []PackageCount {
	kept := make([]PackageCount, 0, len(carried))
	for _, pkg := range sortedKeys(counted) {
		now := counted[pkg]
		was, ok := carried[pkg]
		switch {
		case !ok:
			rep.Failures = append(rep.Failures, RatchetFailure{Package: pkg, Counted: now, Absent: true})
		case now > was:
			rep.Failures = append(rep.Failures, RatchetFailure{Package: pkg, Registered: was, Counted: now})
			kept = append(kept, PackageCount{Package: pkg, Count: was})
		case now < was:
			rep.Lowered = append(rep.Lowered, PackageCount{Package: pkg, Count: now})
			kept = append(kept, PackageCount{Package: pkg, Count: now})
		default:
			rep.Held++
			kept = append(kept, PackageCount{Package: pkg, Count: was})
		}
	}
	for _, pkg := range sortedKeys(carried) {
		if _, ok := counted[pkg]; !ok {
			rep.Lowered = append(rep.Lowered, PackageCount{Package: pkg, Count: 0})
		}
	}
	sort.Slice(rep.Lowered, func(i, j int) bool { return rep.Lowered[i].Package < rep.Lowered[j].Package })
	return kept
}

// sortedKeys returns the map's keys in a stable order.
func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ratchetDoc is the register's schema. Packages is a pointer so a
// document with no packages block is told apart from an emptied one.
type ratchetDoc struct {
	Kind     string          `yaml:"kind"`
	Version  int             `yaml:"version"`
	Packages *[]ratchetEntry `yaml:"packages"`
}

// ratchetEntry is one package's count as the register carries it.
type ratchetEntry struct {
	Package string `yaml:"package"`
	Count   int    `yaml:"count"`
}

// LoadRatchetBaseline reads and validates the register. A missing,
// malformed, or self-contradictory register is an error rather than an
// empty set, because a load that degraded to carrying nothing would fail
// every package and one that degraded to carrying an unbounded count
// would exempt the growth the ratchet exists to stop.
func LoadRatchetBaseline(read scope.FileReader, file string) (map[string]int, error) {
	if read == nil {
		return nil, fmt.Errorf("read citation-count register %s: no reader is configured", file)
	}
	data, err := read(file)
	if err != nil {
		return nil, fmt.Errorf("read citation-count register %s: %w", file, err)
	}
	var doc ratchetDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse citation-count register %s: %w", file, err)
	}
	if doc.Kind != ratchetBaselineKind {
		return nil, fmt.Errorf("citation-count register %s: expected kind %q, got %q", file, ratchetBaselineKind, doc.Kind)
	}
	if doc.Version != ratchetBaselineVersion {
		return nil, fmt.Errorf("citation-count register %s: expected version %d, got %d", file, ratchetBaselineVersion, doc.Version)
	}
	if doc.Packages == nil {
		return nil, fmt.Errorf("citation-count register %s: carries no packages block", file)
	}
	carried := map[string]int{}
	for _, e := range *doc.Packages {
		if strings.TrimSpace(e.Package) == "" {
			return nil, fmt.Errorf("citation-count register %s: carries an entry with no package", file)
		}
		if _, ok := carried[e.Package]; ok {
			return nil, fmt.Errorf("citation-count register %s: package %s is declared twice", file, e.Package)
		}
		if e.Count < 1 {
			return nil, fmt.Errorf("citation-count register %s: package %s carries a count of %d, and a package at zero holds no entry",
				file, e.Package, e.Count)
		}
		carried[e.Package] = e.Count
	}
	return carried, nil
}

// WriteRatchetBaseline writes the register holding exactly the counts
// given, dropping any count of zero. It is the one writer of the file:
// the downward rewrite and the seeding both call it.
func WriteRatchetBaseline(write func(string, []byte) error, file string, counts []PackageCount) error {
	if write == nil {
		return fmt.Errorf("rewrite citation-count register %s: no writer is configured", file)
	}
	sorted := append([]PackageCount(nil), counts...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Package < sorted[j].Package })
	entries := make([]ratchetEntry, 0, len(sorted))
	for i, c := range sorted {
		if c.Count < 1 {
			continue
		}
		if i > 0 && sorted[i-1].Package == c.Package {
			return fmt.Errorf("rewrite citation-count register %s: package %s is written twice", file, c.Package)
		}
		entries = append(entries, ratchetEntry(c))
	}
	doc := ratchetDoc{Kind: ratchetBaselineKind, Version: ratchetBaselineVersion, Packages: &entries}
	body, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("encode citation-count register %s: %w", file, err)
	}
	if err := write(file, append([]byte(ratchetBaselineHeader), body...)); err != nil {
		return fmt.Errorf("rewrite citation-count register %s: %w", file, err)
	}
	return nil
}

// ratchetBaselineHeader explains the register to a reader who opens it
// without the ratchet beside them.
const ratchetBaselineHeader = `# SPDX-License-Identifier: MIT
#
# Per-package counts of section-number citations in // spec: annotations.
#
# Each count is how many citations of a numbered specification section
# (the §N.M form or the bare N.M form) the Go package directory carries
# in its // spec: annotations. The annotation rule in
# .claude/rules/spec-annotations.md moves code to identifier citations
# area by area, and an area's count reaches zero before its milestone
# exits.
#
# The ratchet in tests/static fails a package whose count rises
# above the count here, and a package with a citation and no entry. Until
# the page a package implements exists, new code there may cite a section
# as before, and the author raises the package's count here in the same
# commit, so the rise is visible in review. A package whose count falls
# is written back here by the run that measures the fall, and that run
# fails so that the author commits the lowered count with the change.
`
