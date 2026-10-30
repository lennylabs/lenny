// SPDX-License-Identifier: MIT

// Package gate holds the implementations the tier-0 citation gates run.
// A gate is delivered as a Go test under tests/tier0_static/, which is a
// channel the repository hard-gates, and the logic it drives lives here
// so the same walk, the same matcher, and the same section resolver the
// passes use answer for the gates as well.
//
// This is migration tooling rather than a platform behavior, so the code
// carries no spec citation of its own. The rule the gates enforce is
// stated in the specification and cited by the tests that run them.
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

// ResolutionBaselinePath is the repo-relative path of the citation
// resolver's baseline. It is outside the read domain, because a gate
// cannot read its own baseline as tree content: the baseline holds a
// copy of the text of every citation it carries, each copy is
// non-resolving by construction under the register's own path, and
// seeding an entry for such a copy would add a further copy.
const ResolutionBaselinePath = "tests/registers/line-citation-resolution.yaml"

// The declaration the baseline carries. It is a baseline rather than an
// exception register (tests/registers/README.md): a citation that no
// longer points inside the section it names has no owner and no expiry,
// and its route out of the file is the retirement of the citation.
const (
	resolutionBaselineKind    = "line-citation-resolution-baseline"
	resolutionBaselineVersion = 1
)

// ResolutionEntry is one baseline key. The pair is the file the citation
// was written in and the citation as written, with any continuation join
// applied. Keying by both is what stops an entry travelling: the same
// citation text written in another file is a citation the baseline does
// not carry.
type ResolutionEntry struct {
	Path string
	Text string
}

// String renders the entry for a report.
func (e ResolutionEntry) String() string { return e.Path + ": " + e.Text }

// Occurrence is one citation that did not resolve, together with every
// reason it did not.
type Occurrence struct {
	citation.Located
	Failures []citation.Failure
}

// Entry returns the baseline key the occurrence would be carried under.
func (o Occurrence) Entry() ResolutionEntry {
	return ResolutionEntry{Path: o.Path, Text: o.Text}
}

// String renders the occurrence for a gate's failure message, naming the
// file, the line, the citation text, and every reason.
func (o Occurrence) String() string {
	reasons := make([]string, 0, len(o.Failures))
	for _, f := range o.Failures {
		reasons = append(reasons, f.String())
	}
	return fmt.Sprintf("%s line %d: %s (%s)", o.Path, o.Line, o.Text, strings.Join(reasons, "; "))
}

// ResolutionReport is what one run of the resolver measured.
type ResolutionReport struct {
	// Files is the number of tracked files inside the read domain.
	Files int
	// Citations is the number of citations of the retired form read
	// across them.
	Citations int
	// NonResolving is how many of those did not resolve.
	NonResolving int
	// Carried is how many entries the baseline held when the run loaded
	// it. It is the denominator a caller checks Baselined and Retired
	// against, and it reaches zero when the last citation is retired.
	Carried int
	// Baselined is how many of the non-resolving ones the baseline
	// carries. One entry covers every occurrence of its citation text in
	// its file, so this counts occurrences and can exceed Carried.
	Baselined int
	// Matched is how many distinct baseline entries an occurrence used.
	// Together with Retired it accounts for every entry Carried names.
	Matched int
	// Retired is the baseline entries the run removed, because the
	// citation each was written for is no longer in the tree.
	Retired []ResolutionEntry
	// Failures is every non-resolving citation the baseline does not
	// carry. A non-empty list is the gate's failure.
	Failures []Occurrence
}

// Resolution is the citation resolver gate. Its dependencies are
// injected so a test drives it over a fixture tree.
type Resolution struct {
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

// NewResolution returns the gate over the tracked tree at root.
func NewResolution(root string) *Resolution {
	return &Resolution{
		List:     scope.GitLister(root),
		Read:     scope.DirReader(root),
		Write:    scope.DirWriter(root),
		Baseline: ResolutionBaselinePath,
	}
}

// NewResolutionOver returns the gate with each dependency supplied, so a
// caller drives it over a tree other than a git checkout.
func NewResolutionOver(list scope.Lister, read scope.FileReader, write func(string, []byte) error) *Resolution {
	return &Resolution{List: list, Read: read, Write: write, Baseline: ResolutionBaselinePath}
}

// Run reads every citation inside the read domain, resolves each against
// the section it names, and reports the non-resolving ones the baseline
// does not carry.
//
// A run that could not inspect the tree returns an error rather than an
// empty report: a walk that selected no file, a tree with no
// specification file, and a missing or malformed baseline are all states
// in which a report of no failure would certify content the gate never
// opened.
//
// The baseline is rewritten downward in the same run: an entry whose
// citation is no longer in the tree is removed, so an exemption cannot
// outlive the citation it was written for. The rewrite never adds an
// entry, so a citation that stops resolving fails the run.
func (g *Resolution) Run(ctx context.Context) (ResolutionReport, error) {
	var rep ResolutionReport
	if g.List == nil || g.Read == nil {
		return rep, fmt.Errorf("citation resolver: a lister and a reader are required")
	}
	domain, err := scope.ReadDomain(ctx, g.List)
	if err != nil {
		return rep, fmt.Errorf("citation resolver: %w", err)
	}
	resolver, err := citation.NewResolver(ctx, g.List, g.Read)
	if err != nil {
		return rep, fmt.Errorf("citation resolver: %w", err)
	}
	carried, err := g.loadBaseline()
	if err != nil {
		return rep, fmt.Errorf("citation resolver: %w", err)
	}
	unresolved, read, err := g.scan(ctx, domain, resolver)
	if err != nil {
		return rep, err
	}
	rep.Files = len(domain)
	rep.Citations = read
	rep.NonResolving = len(unresolved)
	rep.Carried = len(carried)

	used := map[ResolutionEntry]bool{}
	for _, o := range unresolved {
		entry := o.Entry()
		if !carried[entry] {
			rep.Failures = append(rep.Failures, o)
			continue
		}
		used[entry] = true
		rep.Baselined++
	}
	// The rewrite runs whatever the report says. An entry whose citation
	// is no longer in the tree is retired by the run that read the tree
	// without it, so a failure elsewhere cannot keep an exemption alive
	// past the citation it was written for. The rewrite is one-way, so a
	// reported failure is never absorbed into the file.
	rep.Matched = len(used)
	kept, retired := partitionBaseline(carried, used)
	rep.Retired = retired
	if len(retired) > 0 {
		if err := WriteResolutionBaseline(g.Write, g.Baseline, kept); err != nil {
			return rep, fmt.Errorf("citation resolver: %w", err)
		}
	}
	return rep, nil
}

// scan reads every file in the domain and returns the citations that did
// not resolve, together with how many citations were read.
func (g *Resolution) scan(ctx context.Context, domain []string, resolver *citation.Resolver) ([]Occurrence, int, error) {
	var out []Occurrence
	read := 0
	for _, target := range domain {
		if err := ctx.Err(); err != nil {
			return nil, 0, fmt.Errorf("citation resolver: %w", err)
		}
		data, err := g.Read(target)
		if err != nil {
			return nil, 0, fmt.Errorf("citation resolver: read %s: %w", target, err)
		}
		for _, located := range citation.FindIn(target, string(data)) {
			read++
			failures := resolver.Resolve(located.Citation)
			if len(failures) == 0 {
				continue
			}
			out = append(out, Occurrence{Located: located, Failures: failures})
		}
	}
	return out, read, nil
}

// partitionBaseline splits the loaded baseline into the entries a
// non-resolving citation still uses and the entries no citation in the
// tree matches, both sorted.
func partitionBaseline(carried map[ResolutionEntry]bool, used map[ResolutionEntry]bool) (kept, retired []ResolutionEntry) {
	for entry := range carried {
		if used[entry] {
			kept = append(kept, entry)
			continue
		}
		retired = append(retired, entry)
	}
	sortEntries(kept)
	sortEntries(retired)
	return kept, retired
}

// sortEntries orders entries by file and then by citation text, so a
// rewritten baseline and a report read in a stable order.
func sortEntries(entries []ResolutionEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Path != entries[j].Path {
			return entries[i].Path < entries[j].Path
		}
		return entries[i].Text < entries[j].Text
	})
}

// resolutionDoc is the baseline's schema. Files is a pointer so a
// document that declares no files block is distinguishable from one that
// declares an empty list; the first is malformed and the second is a
// tree in which every citation resolves.
type resolutionDoc struct {
	Kind    string           `yaml:"kind"`
	Version int              `yaml:"version"`
	Files   *[]resolutionSet `yaml:"files"`
}

// resolutionSet is one file's baselined citations.
type resolutionSet struct {
	Path      string   `yaml:"path"`
	Citations []string `yaml:"citations"`
}

// loadBaseline reads and validates the baseline into the set of entries
// it carries. A missing, unreadable, or malformed baseline is an error
// rather than an empty set: a load that degraded to carrying nothing
// would report every already-stale citation as a new failure, and a load
// that degraded to exempting everything would report no failure for
// exactly the citations a content move breaks.
func (g *Resolution) loadBaseline() (map[ResolutionEntry]bool, error) {
	if g.Baseline == "" {
		return nil, fmt.Errorf("no baseline path is configured")
	}
	data, err := g.Read(g.Baseline)
	if err != nil {
		return nil, fmt.Errorf("read resolution baseline %s: %w", g.Baseline, err)
	}
	var doc resolutionDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse resolution baseline %s: %w", g.Baseline, err)
	}
	if doc.Kind != resolutionBaselineKind {
		return nil, fmt.Errorf("resolution baseline %s: expected kind %q, got %q",
			g.Baseline, resolutionBaselineKind, doc.Kind)
	}
	if doc.Version != resolutionBaselineVersion {
		return nil, fmt.Errorf("resolution baseline %s: expected version %d, got %d",
			g.Baseline, resolutionBaselineVersion, doc.Version)
	}
	if doc.Files == nil {
		return nil, fmt.Errorf("resolution baseline %s: carries no files block", g.Baseline)
	}
	carried := map[ResolutionEntry]bool{}
	seen := map[string]bool{}
	for _, set := range *doc.Files {
		if strings.TrimSpace(set.Path) == "" {
			return nil, fmt.Errorf("resolution baseline %s: carries an entry with no path", g.Baseline)
		}
		if seen[set.Path] {
			return nil, fmt.Errorf("resolution baseline %s: file %s is declared twice", g.Baseline, set.Path)
		}
		seen[set.Path] = true
		if len(set.Citations) == 0 {
			return nil, fmt.Errorf("resolution baseline %s: file %s carries no citation", g.Baseline, set.Path)
		}
		for _, text := range set.Citations {
			if strings.TrimSpace(text) == "" {
				return nil, fmt.Errorf("resolution baseline %s: file %s carries an empty citation", g.Baseline, set.Path)
			}
			entry := ResolutionEntry{Path: set.Path, Text: text}
			if carried[entry] {
				return nil, fmt.Errorf("resolution baseline %s: file %s declares %q twice", g.Baseline, set.Path, text)
			}
			carried[entry] = true
		}
	}
	return carried, nil
}

// WriteResolutionBaseline writes the baseline holding exactly the
// entries given, keyed by file and citation text. It is the one writer
// of the file: the downward rewrite calls it with the entries that
// survived a run, and the seeding of the baseline calls it with the
// non-resolving citations the resolver itself reported, so the file is
// never hand-assembled from a figure stated elsewhere.
//
// The key is one entry per file and citation text, and the writer
// enforces it whatever the caller supplies. A seeding caller passes the
// occurrences the resolver reported, and one file writes the same
// citation text on several lines, so repeats of a key reach the writer
// and collapse here into the single entry the loader accepts.
func WriteResolutionBaseline(write func(string, []byte) error, path string, entries []ResolutionEntry) error {
	if write == nil {
		return fmt.Errorf("rewrite resolution baseline %s: no writer is configured", path)
	}
	sortEntries(entries)
	doc := resolutionDoc{Kind: resolutionBaselineKind, Version: resolutionBaselineVersion}
	sets := make([]resolutionSet, 0, len(entries))
	for i, e := range entries {
		if i > 0 && entries[i-1] == e {
			continue
		}
		if n := len(sets); n > 0 && sets[n-1].Path == e.Path {
			sets[n-1].Citations = append(sets[n-1].Citations, e.Text)
			continue
		}
		sets = append(sets, resolutionSet{Path: e.Path, Citations: []string{e.Text}})
	}
	doc.Files = &sets
	body, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("encode resolution baseline %s: %w", path, err)
	}
	var b strings.Builder
	b.WriteString(resolutionBaselineHeader)
	b.Write(body)
	if err := write(path, []byte(b.String())); err != nil {
		return fmt.Errorf("rewrite resolution baseline %s: %w", path, err)
	}
	return nil
}

// resolutionBaselineHeader explains the file to a reader who opens it
// without the gate beside them.
const resolutionBaselineHeader = `# SPDX-License-Identifier: MIT
#
# Line-citation resolution baseline.
#
# Every citation listed here pointed outside the specification section
# it names when the citation resolver landed. Entries are keyed by the
# file the citation was written in and by the citation as written, so an
# entry covers that citation in that file alone and the same text
# written elsewhere is a failure.
#
# The resolver rewrites this file downward: an entry whose citation is no
# longer in the tree is removed in the same run, and no run adds an
# entry, so a citation that stops resolving fails tier 0 rather than
# being absorbed here. The file empties as the citations are retired.
`
