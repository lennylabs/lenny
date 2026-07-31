// SPDX-License-Identifier: MIT

package citation

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// Section is one heading's range in a specification file. Start is the heading
// line and End is the last line before the next heading at the same level or
// above, so a section's range covers its own subsections and a section-level
// citation resolves against the whole of the section it names.
type Section struct {
	Number string
	File   string
	Level  int
	Start  int
	End    int
}

// Contains reports whether the 1-based line sits inside the section.
func (s Section) Contains(line int) bool { return line >= s.Start && line <= s.End }

// String renders the section for a failure message.
func (s Section) String() string {
	return fmt.Sprintf("§%s (%s lines %d-%d)", s.Number, s.File, s.Start, s.End)
}

// FailureKind names why a citation did not resolve. The kinds are distinct
// because their remedies are: a member outside its section is a stale pointer
// to correct, a straddling range is a citation whose two endpoints disagree
// about which section it means, an unknown section names a heading the
// specification does not carry, and an unknown file names a path that does not
// resolve under spec/. Collapsing them would report a typo in a file name as a
// stale line number.
type FailureKind string

// The failure kinds the resolver reports.
const (
	// OutsideSection is a member whose lines fall outside the cited section.
	OutsideSection FailureKind = "outside-section"
	// StraddlingRange is a range whose endpoints fall in different sections.
	StraddlingRange FailureKind = "straddling-range"
	// UnknownSection is a section number no heading under spec/ declares.
	UnknownSection FailureKind = "unknown-section"
	// UnknownFile is a path-form reference naming a file that does not
	// resolve under spec/.
	UnknownFile FailureKind = "unknown-file"
	// UnclosedParenthesis is a citation whose head opened a parenthesis of the
	// carrier's own that no parenthesis closes inside the citation. The line
	// pass cannot convert it to an anchor without leaving the matching
	// delimiter behind, so it is reported for hand correction the way a
	// straddling range is.
	UnclosedParenthesis FailureKind = "unclosed-parenthesis"
)

// Failure is one reason a citation did not resolve. Member is the zero value
// for a failure that belongs to the citation rather than to one member.
type Failure struct {
	Kind   FailureKind
	Member Member
	Detail string
}

// String renders the failure for a gate's report.
func (f Failure) String() string {
	if f.Member.Text == "" {
		return fmt.Sprintf("%s: %s", f.Kind, f.Detail)
	}
	return fmt.Sprintf("%s: member %q %s", f.Kind, f.Member.Text, f.Detail)
}

// Resolver answers whether a citation still points inside the section it names.
// It holds the range of every numbered section under spec/, computed from the
// headings headingExpr reads.
type Resolver struct {
	byNumber map[string]Section
	byFile   map[string][]Section
}

// headingExpr matches the headings a section's range is computed from. The
// ranges below it are computed from the `##` through `######` headings, and a
// level-one heading is read as well so a file that states its numbered title at
// that level declares the section it names. A level-one heading carrying no
// leading number is an ordinary document title: numberExpr alone excludes it,
// the same way it excludes an unnumbered sub-heading, and it closes the
// headings above it without declaring a section.
//
// Reading level two and below alone leaves a file whose numbered title sits at
// level one declaring no section under that number, so every section-level
// citation into it is reported as naming a heading the specification does not
// declare. That report is false, it gives no line-containment verdict for a
// section that exists, and it puts the file's first lines inside no section for
// the path form.
var headingExpr = regexp.MustCompile(`^(#{1,6})[ \t]+(.*)$`)

// fenceExpr matches the opening or closing line of a fenced code block. A line
// opening with hashes inside a fence is a comment in an example rather than a
// heading, and reading it as one declares sections the specification does not
// have.
var fenceExpr = regexp.MustCompile("^[ \t]*(?:```|~~~)")

// numberExpr reads the section number a heading declares, which is the leading
// dotted number of its text. A heading with no leading number is a sub-heading
// inside a numbered section: it closes deeper headings but declares no section
// of its own.
var numberExpr = regexp.MustCompile(`^(\d+(?:\.\d+)*)\.?(?:[ \t]|$)`)

// NewResolver reads every specification file in the tree and computes the range
// of each section it declares.
//
// It fails on a tree with no specification file and on a tree whose
// specification files declare no numbered section, rather than resolving every
// citation against an empty index. An empty index reads to the gates as a tree
// in which no citation resolves, which is the same report a genuinely broken
// tree produces. A single file that declares no numbered section is ordinary:
// the specification carries a directory README alongside its numbered files.
func NewResolver(ctx context.Context, list scope.Lister, read scope.FileReader) (*Resolver, error) {
	if list == nil || read == nil {
		return nil, fmt.Errorf("build citation resolver: a lister and a reader are required")
	}
	paths, err := list(ctx)
	if err != nil {
		return nil, fmt.Errorf("build citation resolver: %w", err)
	}
	r := &Resolver{byNumber: map[string]Section{}, byFile: map[string][]Section{}}
	files := 0
	for _, path := range paths {
		if !isSpecFile(path) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("build citation resolver: %w", err)
		}
		data, err := read(path)
		if err != nil {
			return nil, fmt.Errorf("read specification file %s: %w", path, err)
		}
		sections := parseSections(path, string(data))
		files++
		for _, s := range sections {
			if prior, ok := r.byNumber[s.Number]; ok {
				return nil, fmt.Errorf("section §%s is declared in both %s and %s", s.Number, prior.File, s.File)
			}
			r.byNumber[s.Number] = s
		}
		r.byFile[path] = sections
	}
	if files == 0 {
		return nil, fmt.Errorf("build citation resolver: no file under %s in the tree", specPrefix)
	}
	if len(r.byNumber) == 0 {
		return nil, fmt.Errorf("build citation resolver: %d file(s) under %s declare no numbered section", files, specPrefix)
	}
	return r, nil
}

// isSpecFile reports whether a tracked path is a specification file.
func isSpecFile(path string) bool {
	return strings.HasPrefix(path, specPrefix) && strings.HasSuffix(path, ".md")
}

// parseSections computes the range of every numbered section in one file. A
// heading ends at the line before the next heading at its own level or above,
// so a parent's range covers its children. A heading with no leading number
// closes deeper headings without declaring a section, which is how an
// unnumbered sub-heading inside a numbered section is read.
func parseSections(path, content string) []Section {
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	type heading struct {
		level  int
		number string
		start  int
	}
	var headings []heading
	fenced := false
	for i, line := range lines {
		if fenceExpr.MatchString(line) {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		m := headingExpr.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		h := heading{level: len(m[1]), start: i + 1}
		if n := numberExpr.FindStringSubmatch(strings.TrimSpace(m[2])); n != nil {
			h.number = n[1]
		}
		headings = append(headings, h)
	}
	var out []Section
	for i, h := range headings {
		if h.number == "" {
			continue
		}
		end := len(lines)
		for _, next := range headings[i+1:] {
			if next.level <= h.level {
				end = next.start - 1
				break
			}
		}
		out = append(out, Section{Number: h.number, File: path, Level: h.level, Start: h.start, End: end})
	}
	return out
}

// Sections returns every section the resolver indexed, sorted by file and start
// line, so a caller can report the index it resolved against.
func (r *Resolver) Sections() []Section {
	out := make([]Section, 0, len(r.byNumber))
	for _, s := range r.byNumber {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Start < out[j].Start
	})
	return out
}

// Section returns the section the number names.
func (r *Resolver) Section(number string) (Section, bool) {
	s, ok := r.byNumber[number]
	return s, ok
}

// Resolve reports every reason the citation does not resolve. An empty result
// means every member falls inside the section the citation names.
//
// A section-number citation resolves against the section it names. A path-form
// citation resolves against the section of the named file that contains the
// cited line, so it is the containing section that is inferred rather than the
// membership that is checked.
//
// A citation the matcher marked unconvertible is reported under its own kind
// ahead of its members. The occurrence is a stale pointer like any other, so
// the resolver reports it and the ratchet counts it, and its remedy is a hand
// correction because the pass declines to rewrite an unbalanced span.
func (r *Resolver) Resolve(c Citation) []Failure {
	var out []Failure
	if c.Unconvertible {
		out = append(out, Failure{
			Kind:   UnclosedParenthesis,
			Detail: "opens a parenthesis that does not close inside the citation",
		})
	}
	if c.Section != "" {
		return append(out, r.resolveSection(c)...)
	}
	return append(out, r.resolvePath(c)...)
}

// Resolves reports whether the citation resolves with no failure.
func (r *Resolver) Resolves(c Citation) bool { return len(r.Resolve(c)) == 0 }

// resolveSection checks every member against the range of the cited section.
func (r *Resolver) resolveSection(c Citation) []Failure {
	sec, ok := r.byNumber[c.Section]
	if !ok {
		return []Failure{{
			Kind:   UnknownSection,
			Detail: fmt.Sprintf("no heading under %s declares §%s", specPrefix, c.Section),
		}}
	}
	var out []Failure
	for _, m := range c.Members {
		startIn, endIn := sec.Contains(m.Start), sec.Contains(m.End)
		switch {
		case startIn && endIn:
		case startIn != endIn:
			out = append(out, Failure{
				Kind:   StraddlingRange,
				Member: m,
				Detail: fmt.Sprintf("has one endpoint inside and one outside %s", sec),
			})
		default:
			out = append(out, Failure{
				Kind:   OutsideSection,
				Member: m,
				Detail: fmt.Sprintf("falls outside %s", sec),
			})
		}
	}
	return out
}

// resolvePath checks every member against the section of the named file that
// contains it. A file that does not resolve under spec/ is its own failure
// rather than a member that fell outside a section, because the remedy is the
// path rather than the line number.
//
// A path-form member is anchored on the deepest section containing its start
// line, which is the section the citation means, and it straddles when its end
// line falls outside that section. A range that runs from a section's preamble
// into one of its own subsections is anchored on the parent, whose range covers
// its subsections, so it resolves the way the same range written in the
// section-number spelling resolves against that parent.
//
// Asking instead whether any section of the file contains both endpoints leaves
// the straddle unreportable against the specification's layout, where a file
// opens with a whole-file heading whose range runs to the end of the file: that
// heading contains both endpoints of every range, so a range crossing a
// boundary inside it resolves clean, is never reported for hand correction, and
// holds its file above a zero count while the same range written by section
// number is reported.
func (r *Resolver) resolvePath(c Citation) []Failure {
	sections, ok := r.byFile[c.File]
	if !ok {
		return []Failure{{
			Kind:   UnknownFile,
			Detail: fmt.Sprintf("%s does not resolve under %s", c.File, specPrefix),
		}}
	}
	var out []Failure
	for _, m := range c.Members {
		anchor, startOK := deepest(sections, m.Start)
		end, endOK := deepest(sections, m.End)
		switch {
		case !startOK || !endOK:
			out = append(out, Failure{
				Kind:   OutsideSection,
				Member: m,
				Detail: fmt.Sprintf("falls in no section of %s", c.File),
			})
		case !anchor.Contains(m.End):
			out = append(out, Failure{
				Kind:   StraddlingRange,
				Member: m,
				Detail: fmt.Sprintf("starts in §%s and ends in §%s", anchor.Number, end.Number),
			})
		}
	}
	return out
}

// deepest returns the most specific section of a file containing the line,
// which is the one with the highest heading level among those that contain it.
func deepest(sections []Section, line int) (Section, bool) {
	var best Section
	found := false
	for _, s := range sections {
		if !s.Contains(line) {
			continue
		}
		if !found || s.Level > best.Level {
			best, found = s, true
		}
	}
	return best, found
}
