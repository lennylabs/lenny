// SPDX-License-Identifier: MIT

package gate

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/lenny/scripts/specshift/citation"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// RatchetBaselinePath is the repo-relative path of the line-citation
// ratchet's baseline. It is outside the read domain for the reason the
// resolver's baseline is: a gate that read its own register as tree
// content would find the register carrying no count of its own and fail
// it on its first line citation.
const RatchetBaselinePath = "tests/registers/line-citations.yaml"

// The declaration the baseline carries. It is a baseline rather than an
// exception register (tests/registers/README.md): a per-file count has
// no owner and no expiry, and its route to zero is the retirement of the
// citations the file carries.
const (
	ratchetBaselineKind    = "line-citation-count-baseline"
	ratchetBaselineVersion = 1
)

// RatchetCount is one file's count of citations of the retired form. A
// file the tree carries no citation in holds no entry, so a count of
// zero is the absence of an entry rather than an entry reading zero.
type RatchetCount struct {
	Path  string
	Count int
}

// String renders the count for a report.
func (c RatchetCount) String() string { return fmt.Sprintf("%s: %d", c.Path, c.Count) }

// RatchetFailure is one file whose citation count the register does not
// allow, either because the file carries a citation while the register
// carries no count for it or because its count rose above the one the
// register carries.
type RatchetFailure struct {
	Path string
	// Registered is the count the register carries, and is zero when
	// Absent is set.
	Registered int
	// Counted is the count the run read from the file.
	Counted int
	// Absent reports that the register carries no count for the file, so
	// the failure is the file's first citation rather than a rise.
	Absent bool
}

// String renders the failure for a gate's message, naming the file and
// both counts.
func (f RatchetFailure) String() string {
	if f.Absent {
		return fmt.Sprintf("%s: %d line citation(s) in a file %s carries no count for",
			f.Path, f.Counted, RatchetBaselinePath)
	}
	return fmt.Sprintf("%s: %d line citation(s) where %s carries %d",
		f.Path, f.Counted, RatchetBaselinePath, f.Registered)
}

// RatchetReport is what one run of the ratchet measured.
type RatchetReport struct {
	// Files is the number of tracked files inside the read domain.
	Files int
	// Citations is the number of citations of the retired form read
	// across them.
	Citations int
	// Carried is how many per-file counts the baseline held when the run
	// loaded it. It reaches zero when the last citation is retired.
	Carried int
	// Held is how many of those files carry the count the baseline
	// records.
	Held int
	// Raised is how many carry more than it records, each of them a
	// failure.
	Raised int
	// Lowered is every file whose count fell, with the count the run
	// read. A file that carries no citation, or that left the tree,
	// falls to zero and loses its entry. Together with Held and Raised
	// it accounts for every entry Carried names.
	Lowered []RatchetCount
	// Failures is every file the register does not allow. A non-empty
	// list is the gate's failure.
	Failures []RatchetFailure
}

// Ratchet is the line-citation ratchet gate. It holds the population of
// citations of the retired form at or below the per-file count measured
// when it landed: a file absent from the baseline fails on its first
// citation, a file present fails when its count rises, and a count that
// falls is written back in the same run. When every count has fallen to
// zero the baseline is empty and the gate is a flat prohibition.
//
// It is delivered as a tier-0 Go test rather than as a linter rule,
// because the repository's lint invocation is downgraded to a warning
// and a check whose script is absent passes silently.
//
// Its dependencies are injected so a test drives it over a fixture tree.
type Ratchet struct {
	// List enumerates the tracked tree.
	List scope.Lister
	// Read reads a repo-relative tracked path.
	Read scope.FileReader
	// Write writes a repo-relative tracked path. It is used for the
	// downward rewrite of the baseline alone.
	Write func(path string, content []byte) error
	// Baseline is the repo-relative path of the baseline.
	Baseline string
}

// NewRatchet returns the gate over the tracked tree at root.
func NewRatchet(root string) *Ratchet {
	return &Ratchet{
		List:     scope.GitLister(root),
		Read:     scope.DirReader(root),
		Write:    scope.DirWriter(root),
		Baseline: RatchetBaselinePath,
	}
}

// NewRatchetOver returns the gate with each dependency supplied, so a
// caller drives it over a tree other than a git checkout.
func NewRatchetOver(list scope.Lister, read scope.FileReader, write func(string, []byte) error) *Ratchet {
	return &Ratchet{List: list, Read: read, Write: write, Baseline: RatchetBaselinePath}
}

// Run counts the citations of the retired form in every file inside the
// read domain and compares each count with the baseline.
//
// A run that could not inspect the tree returns an error rather than an
// empty report: a walk that selected no file and a missing or malformed
// baseline are both states in which a report of no failure would certify
// content the gate never opened, or would exempt every file at once.
//
// The baseline is rewritten downward in the same run, so a retirement is
// recorded and cannot be undone. The rewrite never raises a count, so a
// file that grows fails the run and keeps the count it had.
func (g *Ratchet) Run(ctx context.Context) (RatchetReport, error) {
	var rep RatchetReport
	if g.List == nil || g.Read == nil {
		return rep, fmt.Errorf("line-citation ratchet: a lister and a reader are required")
	}
	domain, err := scope.ReadDomain(ctx, g.List)
	if err != nil {
		return rep, fmt.Errorf("line-citation ratchet: %w", err)
	}
	carried, err := g.loadBaseline()
	if err != nil {
		return rep, fmt.Errorf("line-citation ratchet: %w", err)
	}
	counted, read, err := g.scan(ctx, domain)
	if err != nil {
		return rep, err
	}
	rep.Files = len(domain)
	rep.Citations = read
	rep.Carried = len(carried)

	kept := g.compare(counted, carried, &rep)
	if len(rep.Lowered) > 0 {
		if err := WriteRatchetBaseline(g.Write, g.Baseline, kept); err != nil {
			return rep, fmt.Errorf("line-citation ratchet: %w", err)
		}
	}
	return rep, nil
}

// compare measures each counted file against the baseline, fills in the
// report, and returns the counts the rewritten baseline holds.
//
// A file whose count rose keeps the count the baseline carries, so the
// rise is reported rather than absorbed. The rewrite runs whatever the
// report says: a file whose count fell has retired the citations it no
// longer carries, and holding that fall back because another file failed
// would let the first file regrow to its old count with the gate green.
func (g *Ratchet) compare(counted map[string]int, carried map[string]int, rep *RatchetReport) []RatchetCount {
	kept := make([]RatchetCount, 0, len(carried))
	for _, path := range sortedKeys(counted) {
		now := counted[path]
		was, ok := carried[path]
		if !ok {
			rep.Failures = append(rep.Failures, RatchetFailure{Path: path, Counted: now, Absent: true})
			continue
		}
		switch {
		case now > was:
			rep.Raised++
			rep.Failures = append(rep.Failures, RatchetFailure{Path: path, Registered: was, Counted: now})
			kept = append(kept, RatchetCount{Path: path, Count: was})
		case now < was:
			rep.Lowered = append(rep.Lowered, RatchetCount{Path: path, Count: now})
			kept = append(kept, RatchetCount{Path: path, Count: now})
		default:
			rep.Held++
			kept = append(kept, RatchetCount{Path: path, Count: was})
		}
	}
	// An entry whose file carries no citation of the retired form, or
	// that left the tree, has fallen to zero and loses its entry.
	for _, path := range sortedKeys(carried) {
		if _, ok := counted[path]; !ok {
			rep.Lowered = append(rep.Lowered, RatchetCount{Path: path, Count: 0})
		}
	}
	sortCounts(rep.Lowered)
	return kept
}

// scan counts the citations in every file in the domain, returning the
// files that carry at least one and how many citations were read.
func (g *Ratchet) scan(ctx context.Context, domain []string) (map[string]int, int, error) {
	counted := map[string]int{}
	read := 0
	for _, target := range domain {
		if err := ctx.Err(); err != nil {
			return nil, 0, fmt.Errorf("line-citation ratchet: %w", err)
		}
		data, err := g.Read(target)
		if err != nil {
			return nil, 0, fmt.Errorf("line-citation ratchet: read %s: %w", target, err)
		}
		n := len(citation.FindIn(target, string(data)))
		if n == 0 {
			continue
		}
		counted[target] = n
		read += n
	}
	return counted, read, nil
}

// sortedKeys returns the map's keys in a stable order, so a report and a
// rewritten baseline read the same way on every run.
func sortedKeys(counts map[string]int) []string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sortCounts orders counts by file.
func sortCounts(counts []RatchetCount) {
	sort.Slice(counts, func(i, j int) bool { return counts[i].Path < counts[j].Path })
}

// ratchetDoc is the baseline's schema. Files is a pointer so a document
// that declares no files block is distinguishable from one that declares
// an empty list; the first is malformed and the second is a tree with no
// citation of the retired form left in it.
type ratchetDoc struct {
	Kind    string          `yaml:"kind"`
	Version int             `yaml:"version"`
	Files   *[]ratchetCount `yaml:"files"`
}

// ratchetCount is one file's count as the baseline carries it.
type ratchetCount struct {
	Path  string `yaml:"path"`
	Count int    `yaml:"count"`
}

// loadBaseline reads and validates the baseline into the per-file counts
// it carries. A missing, unreadable, or malformed baseline is an error
// rather than an empty set: a load that degraded to carrying nothing
// would report every file in the tree as a first citation, and a load
// that degraded to carrying an unbounded count would exempt the growth
// the gate exists to stop.
func (g *Ratchet) loadBaseline() (map[string]int, error) {
	if g.Baseline == "" {
		return nil, fmt.Errorf("no baseline path is configured")
	}
	data, err := g.Read(g.Baseline)
	if err != nil {
		return nil, fmt.Errorf("read citation-count baseline %s: %w", g.Baseline, err)
	}
	var doc ratchetDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse citation-count baseline %s: %w", g.Baseline, err)
	}
	if doc.Kind != ratchetBaselineKind {
		return nil, fmt.Errorf("citation-count baseline %s: expected kind %q, got %q",
			g.Baseline, ratchetBaselineKind, doc.Kind)
	}
	if doc.Version != ratchetBaselineVersion {
		return nil, fmt.Errorf("citation-count baseline %s: expected version %d, got %d",
			g.Baseline, ratchetBaselineVersion, doc.Version)
	}
	if doc.Files == nil {
		return nil, fmt.Errorf("citation-count baseline %s: carries no files block", g.Baseline)
	}
	carried := map[string]int{}
	for _, entry := range *doc.Files {
		if strings.TrimSpace(entry.Path) == "" {
			return nil, fmt.Errorf("citation-count baseline %s: carries an entry with no path", g.Baseline)
		}
		if _, ok := carried[entry.Path]; ok {
			return nil, fmt.Errorf("citation-count baseline %s: file %s is declared twice", g.Baseline, entry.Path)
		}
		if entry.Count < 1 {
			return nil, fmt.Errorf("citation-count baseline %s: file %s carries a count of %d, and a file at zero holds no entry",
				g.Baseline, entry.Path, entry.Count)
		}
		carried[entry.Path] = entry.Count
	}
	return carried, nil
}

// WriteRatchetBaseline writes the baseline holding exactly the counts
// given. It is the one writer of the file: the downward rewrite calls it
// with the counts that survived a run, and the seeding of the baseline
// calls it with the counts the ratchet itself measured, so the file is
// never hand-assembled from a figure stated elsewhere.
//
// A count of zero is dropped rather than written, because a file at zero
// holds no entry and the loader rejects one.
func WriteRatchetBaseline(write func(string, []byte) error, path string, counts []RatchetCount) error {
	if write == nil {
		return fmt.Errorf("rewrite citation-count baseline %s: no writer is configured", path)
	}
	sortCounts(counts)
	files := make([]ratchetCount, 0, len(counts))
	for i, c := range counts {
		if c.Count < 1 {
			continue
		}
		if i > 0 && counts[i-1].Path == c.Path {
			return fmt.Errorf("rewrite citation-count baseline %s: file %s is written twice", path, c.Path)
		}
		files = append(files, ratchetCount(c))
	}
	doc := ratchetDoc{Kind: ratchetBaselineKind, Version: ratchetBaselineVersion, Files: &files}
	body, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("encode citation-count baseline %s: %w", path, err)
	}
	var b strings.Builder
	b.WriteString(ratchetBaselineHeader)
	b.Write(body)
	if err := write(path, []byte(b.String())); err != nil {
		return fmt.Errorf("rewrite citation-count baseline %s: %w", path, err)
	}
	return nil
}

// ratchetBaselineHeader explains the file to a reader who opens it
// without the gate beside them.
const ratchetBaselineHeader = `# SPDX-License-Identifier: MIT
#
# Per-file line-citation counts.
#
# Each count is how many citations of the retired form the file carried
# when the line-citation ratchet landed. A file that carries no such
# citation holds no entry here, and it fails tier 0 on its first one.
#
# The ratchet rewrites this file downward: a count that falls is written
# back in the same run, and no run raises a count, so a file that grows
# fails tier 0 rather than being absorbed here. When every count has
# fallen to zero the file is empty and the gate is a flat prohibition.
`
