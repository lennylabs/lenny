// SPDX-License-Identifier: MIT

package tier0_static

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/lenny/scripts/specshift/citation"
	"github.com/lennylabs/lenny/scripts/specshift/identifier"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The identifier-resolution gate asserts that each canonical channel,
// link, and register identifier the §28.3 registers carry resolves to
// exactly one spelling across the tree. A carrier that still writes a
// channel under the spelling the §28.3 naming table retires is a second
// live spelling of that identifier, so the identifier resolves to two
// and the gate reports the site.
//
// The canonical identifier set and the retired spellings are both read
// out of §28.3 rather than enumerated here, so an identifier the
// registers gain, or a spelling the naming table retires later, is
// covered without this file changing.
//
// The domain is the identifier pass's site-rewrite domain, taken from
// scripts/specshift/scope rather than restated, so every file the gate
// reads has a pass that writes it. A gate reading a file no pass writes
// would report a site with no route to green.
//
// The read is per occurrence rather than per spelling. A retired
// spelling occurs where the text is not a channel at all, such as the
// Azure storage management-policy file argument at
// spec/17_deployment-topology.md, so the gate excuses an occurrence that
// tests/registers/identifier-senses.yaml records as not a channel and
// reports every other one. That register is the whole exemption route:
// a permanently correct non-channel occurrence is recorded where the
// pass reads it, rather than in the shared exception register, whose
// owner and expiry it could never be retired against.
//
// A retired spelling standing in the naming-table row that retires it is
// the declaration of that spelling rather than a reference to the
// channel (§28.3), so those spans are read as data and are no sites.

// identifierSenseRegisterPath is the register the gate reads an
// occurrence's sense from. It is the register the identifier pass is
// driven by, so the gate excuses exactly the occurrences the pass leaves
// standing.
const identifierSenseRegisterPath = "tests/registers/identifier-senses.yaml"

// identifierSectionPrefix is the specification file the §28.3 registers
// and the naming table are written in. The gate reads them out of the
// tree it walks, so the identifier set it certifies and the identifier
// set the specification declares cannot disagree.
const identifierSectionPrefix = "spec/28"

// identifierRegisterHeading opens the section carrying the three
// registers. The set is read from inside that section rather than from
// every table in the file, because §28.5 and §28.8 name identifiers as
// well and a file-wide read would take a matrix row for a register row.
const identifierRegisterHeading = "### 28.3"

// identifierNamingColumns are the naming table's column headings,
// lowercased. The table is recognized by them rather than by its
// position, because §28.3 carries four tables.
var identifierNamingColumns = []string{"channel", "carrier", "retired spelling", "canonical spelling"}

// identifierNamingRow is one naming-table row: the channel a retired
// spelling denotes, the carrier it is written on, the spelling being
// retired, and the spelling that replaces it there.
type identifierNamingRow struct {
	Channel   string
	Carrier   string
	Retired   string
	Canonical string
}

// identifierSenseEntry is one entry of the sense register, in the fields
// the gate reads. The register is keyed by file and by the 1-based
// position of the site among the retired-spelling sites the file
// carries, in source order, with the file-name site held at position
// zero.
type identifierSenseEntry struct {
	File        string `yaml:"file"`
	Occurrence  int    `yaml:"occurrence"`
	Path        bool   `yaml:"path"`
	Channel     string `yaml:"channel"`
	NotAChannel bool   `yaml:"not-a-channel"`
}

// key returns the entry's position within its file.
func (e identifierSenseEntry) key() int {
	if e.Path {
		return 0
	}
	return e.Occurrence
}

// identifierSenseDocument is the register as it is written. Entries is a
// pointer so a document declaring no entries block is distinguishable
// from one declaring an empty list, and both are refused: a gate driven
// by an empty register would excuse nothing and report every non-channel
// occurrence, and a gate driven by a register it could not parse would
// certify a tree it never resolved.
type identifierSenseDocument struct {
	Kind    string                  `yaml:"kind"`
	Version int                     `yaml:"version"`
	Entries *[]identifierSenseEntry `yaml:"entries"`
}

// identifierResolutionFinding is one occurrence that gives a canonical
// identifier a second live spelling.
type identifierResolutionFinding struct {
	// Identifiers names the canonical identifier the occurrence
	// resolves to. It carries every identifier the naming table maps
	// the spelling to when the register resolves the occurrence to none
	// of them, because one retired spelling denotes two channels.
	Identifiers []string
	// Retired is the spelling the occurrence is written in.
	Retired string
	// Canonical is the spelling §28.3 states for it.
	Canonical string
	// Path and Line name the carrier the occurrence stands in. Line is
	// zero for an occurrence in the file's own name.
	Path string
	Line int
	// CanonicalPath names a carrier writing the canonical spelling, so
	// the failure names both files the identifier resolves through.
	CanonicalPath string
	// Reason states why the occurrence is not excused.
	Reason string
}

// String renders the finding for the gate's message.
func (f identifierResolutionFinding) String() string {
	where := fmt.Sprintf("%s file name", f.Path)
	if f.Line > 0 {
		where = fmt.Sprintf("%s line %d", f.Path, f.Line)
	}
	against := f.CanonicalPath
	if against == "" {
		against = "no carrier in the domain"
	}
	return fmt.Sprintf("%s resolves to %q at %s and to %q at %s (%s)",
		strings.Join(f.Identifiers, " or "), f.Retired, where, f.Canonical, against, f.Reason)
}

// identifierResolutionReport is one run of the gate.
type identifierResolutionReport struct {
	// Files is the number of files the walk inspected.
	Files int
	// Identifiers is the number of canonical identifiers the §28.3
	// registers carry.
	Identifiers int
	// Retired is the number of spellings the naming table retires.
	Retired int
	// Sites is the number of retired-spelling occurrences read.
	Sites int
	// Excused is the number the sense register records as not a channel.
	Excused int
	// Findings is every occurrence that gives an identifier a second
	// spelling, in walk order.
	Findings []identifierResolutionFinding
}

// runIdentifierResolution runs the gate over one tree.
//
// It fails rather than reporting green when it cannot read the §28.3
// registers, the naming table, or the sense register, and when the walk
// selected no file or the registers carry no identifier. A run that
// inspected nothing reports no finding for the same reason a clean tree
// does, and the two have to be distinguishable.
func runIdentifierResolution(ctx context.Context, list scope.Lister, read scope.FileReader) (identifierResolutionReport, error) {
	var rep identifierResolutionReport
	domain, err := scope.WriteDomain(ctx, list, scope.Identifier, read)
	if err != nil {
		return rep, fmt.Errorf("identifier-resolution domain: %w", err)
	}
	canonical, err := identifierRegisterSet(ctx, list, read)
	if err != nil {
		return rep, err
	}
	rows, declarations, err := identifierNamingRows(ctx, list, read)
	if err != nil {
		return rep, err
	}
	table, err := identifier.LoadTable(ctx, list, read)
	if err != nil {
		return rep, fmt.Errorf("identifier-resolution gate: %w", err)
	}
	retired := table.Retired()
	byRetired := map[string][]identifierNamingRow{}
	for _, row := range rows {
		if !canonical[row.Channel] {
			return rep, fmt.Errorf("identifier-resolution gate: the naming table retires %q for %s, "+
				"which the §28.3 registers do not carry", row.Retired, row.Channel)
		}
		byRetired[row.Retired] = append(byRetired[row.Retired], row)
	}
	for _, spelling := range retired {
		if len(byRetired[spelling]) == 0 {
			return rep, fmt.Errorf("identifier-resolution gate: the naming table retires %q "+
				"and the gate read no row stating the identifier it denotes", spelling)
		}
	}
	senses, err := loadIdentifierSenses(read, identifierSenseRegisterPath)
	if err != nil {
		return rep, err
	}
	for file, byPosition := range senses {
		for _, entry := range byPosition {
			if entry.Channel != "" && !canonical[entry.Channel] {
				return rep, fmt.Errorf("identifier sense register %s: %s names %s, "+
					"which the §28.3 registers do not carry", identifierSenseRegisterPath, file, entry.Channel)
			}
		}
	}
	rep.Identifiers = len(canonical)
	rep.Retired = len(retired)
	if rep.Identifiers == 0 {
		return rep, fmt.Errorf("identifier-resolution gate: the §28.3 registers carry no identifier")
	}
	if rep.Retired == 0 {
		return rep, fmt.Errorf("identifier-resolution gate: the naming table retires no spelling")
	}

	// The canonical carriers are collected over the same walk, so a
	// finding names the file the identifier resolves to its canonical
	// spelling through as well as the file that holds the retired one.
	canonicalCarrier := map[string]string{}
	for _, target := range domain {
		content, err := read(target)
		if err != nil {
			return rep, fmt.Errorf("read %s: %w", target, err)
		}
		rep.Files++
		text := string(content)
		for _, row := range rows {
			if _, seen := canonicalCarrier[row.Canonical]; seen {
				continue
			}
			if identifierSpellingIn(text, row.Canonical, declarations[target]) {
				canonicalCarrier[row.Canonical] = target
			}
		}
	}

	for _, target := range domain {
		content, err := read(target)
		if err != nil {
			return rep, fmt.Errorf("read %s: %w", target, err)
		}
		text := string(content)
		byPosition := senses[target]
		if spelling := identifierInFileName(target, retired); spelling != "" {
			rep.Sites++
			entry, ok := byPosition[0]
			switch {
			case ok && entry.NotAChannel:
				rep.Excused++
			default:
				rep.Findings = append(rep.Findings,
					identifierFinding(byRetired[spelling], entry, ok, target, 0, canonicalCarrier))
			}
		}
		for i, s := range identifierSites(text, retired, declarations[target]) {
			rep.Sites++
			entry, ok := byPosition[i+1]
			if ok && entry.NotAChannel {
				rep.Excused++
				continue
			}
			rep.Findings = append(rep.Findings,
				identifierFinding(byRetired[s.retired], entry, ok, target, s.line, canonicalCarrier))
		}
	}
	if rep.Files == 0 {
		return rep, fmt.Errorf("identifier-resolution gate: the walk selected no file")
	}
	return rep, nil
}

// identifierFinding renders one unexcused occurrence.
func identifierFinding(rows []identifierNamingRow, entry identifierSenseEntry, registered bool,
	target string, line int, carriers map[string]string,
) identifierResolutionFinding {
	f := identifierResolutionFinding{Path: target, Line: line}
	if len(rows) > 0 {
		f.Retired = rows[0].Retired
	}
	for _, row := range rows {
		if entry.Channel != "" && row.Channel != entry.Channel {
			continue
		}
		f.Identifiers = append(f.Identifiers, row.Channel)
		if f.Canonical == "" {
			f.Canonical = row.Canonical
		}
		if f.CanonicalPath == "" {
			f.CanonicalPath = carriers[row.Canonical]
		}
	}
	sort.Strings(f.Identifiers)
	f.Identifiers = identifierUnique(f.Identifiers)
	switch {
	case registered && entry.Channel != "":
		f.Reason = fmt.Sprintf("%s records the occurrence as a reference to %s, which the pass has not rewritten",
			identifierSenseRegisterPath, entry.Channel)
	case registered:
		f.Reason = fmt.Sprintf("%s carries no disposition for the occurrence", identifierSenseRegisterPath)
	default:
		f.Reason = fmt.Sprintf("%s records no sense for the occurrence", identifierSenseRegisterPath)
	}
	return f
}

// identifierUnique drops the repeats a spelling carried by two rows of
// one channel produces.
func identifierUnique(in []string) []string {
	out := in[:0]
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// identifierSite is one occurrence of a retired spelling.
type identifierSite struct {
	retired string
	line    int
}

// identifierSites returns every retired-spelling occurrence a text
// carries, in source order, which is the order the sense register's
// occurrence numbers are assigned in.
//
// Matching is by whole token rather than by substring, on the rule the
// identifier pass matches by: the left boundary admits an underscore and
// an uppercase byte, so a camel-case compound and a generated stem are
// each one site, and a lowercase spelling standing inside a longer
// lowercase word is no site. The spellings are read longest first and a
// match consumes its span, so a stem that is a prefix of a compound does
// not take the occurrence number the compound is registered under.
//
// The spans held out are the naming-table rows, whose retired-spelling
// cell declares the spelling rather than referring to the channel.
func identifierSites(content string, retired []string, held [][2]int) []identifierSite {
	var out []identifierSite
	for at := 0; at < len(content); {
		match := ""
		for _, spelling := range retired {
			if strings.HasPrefix(content[at:], spelling) && identifierBounded(content, at, at+len(spelling)) {
				match = spelling
				break
			}
		}
		if match == "" {
			at++
			continue
		}
		if !identifierHeld(held, at) {
			out = append(out, identifierSite{retired: match, line: citation.LineOf(content, at)})
		}
		at += len(match)
	}
	return out
}

// identifierSpellingIn reports whether a text carries the spelling as a
// whole token outside the held spans.
func identifierSpellingIn(content, spelling string, held [][2]int) bool {
	for at := 0; at+len(spelling) <= len(content); at++ {
		if !strings.HasPrefix(content[at:], spelling) || !identifierBounded(content, at, at+len(spelling)) {
			continue
		}
		if !identifierHeld(held, at) {
			return true
		}
	}
	return false
}

// identifierHeld reports whether an offset falls inside a held span.
func identifierHeld(held [][2]int, at int) bool {
	for _, span := range held {
		if at >= span[0] && at < span[1] {
			return true
		}
	}
	return false
}

// identifierInFileName returns the retired spelling the tracked path's
// own file name carries, or the empty string. The naming law reaches the
// file-name stem, so a file still named after a retired spelling is a
// second live spelling of the identifier just as an occurrence in
// content is.
func identifierInFileName(target string, retired []string) string {
	base := path.Base(target)
	for _, spelling := range retired {
		for at := 0; at+len(spelling) <= len(base); at++ {
			if strings.HasPrefix(base[at:], spelling) && identifierBounded(base, at, at+len(spelling)) {
				return spelling
			}
		}
	}
	return ""
}

// identifierBounded reports whether a match stands on token boundaries.
func identifierBounded(content string, lo, hi int) bool {
	if lo > 0 && !identifierUpper(content[lo]) && identifierAlphanumeric(content[lo-1]) {
		return false
	}
	return hi >= len(content) || !identifierLowerWordByte(content[hi])
}

// identifierUpper reports whether a byte opens a camel-case component.
func identifierUpper(b byte) bool { return b >= 'A' && b <= 'Z' }

// identifierAlphanumeric reports whether a byte is a letter or a digit.
// The underscore is outside the class, so a generated stem joining a
// service name to a channel name with one carries the channel name.
func identifierAlphanumeric(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// identifierLowerWordByte reports whether a byte continues the word a
// match ended in.
func identifierLowerWordByte(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z'
}

// identifierRegisterSet reads the canonical identifiers out of the §28.3
// registers, which is the set the gate certifies. Reading the section
// rather than an enumeration here is what covers an identifier the
// registers gain without this file changing.
func identifierRegisterSet(ctx context.Context, list scope.Lister, read scope.FileReader) (map[string]bool, error) {
	set := map[string]bool{}
	files, err := identifierSectionFiles(ctx, list)
	if err != nil {
		return nil, err
	}
	for _, target := range files {
		content, err := read(target)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", target, err)
		}
		inSection := false
		inNamingTable := false
		for _, line := range strings.Split(string(content), "\n") {
			if strings.HasPrefix(line, "### ") {
				inSection = strings.HasPrefix(line, identifierRegisterHeading)
				inNamingTable = false
				continue
			}
			cells := identifierTableCells(line)
			if len(cells) == 0 {
				inNamingTable = false
				continue
			}
			// The naming table sits inside the same section and its
			// first column refers to a channel the registers declare.
			// The declaration is the register row, so the set is read
			// from the registers alone and a naming-table row naming a
			// channel no register carries is a defect the gate reports
			// rather than a further identifier.
			if identifierColumnHeading(cells) {
				inNamingTable = true
				continue
			}
			if !inSection || inNamingTable {
				continue
			}
			if name := strings.Trim(cells[0], "`"); identifierCanonicalName(name) {
				set[name] = true
			}
		}
	}
	if len(set) == 0 {
		return nil, fmt.Errorf("identifier-resolution gate: no %s* file carries a %s register section with identifier rows",
			identifierSectionPrefix, identifierRegisterHeading)
	}
	return set, nil
}

// identifierCanonicalName reports whether a cell is a canonical
// identifier, which the naming law writes uppercase and hyphenated under
// one of the class prefixes.
func identifierCanonicalName(s string) bool {
	prefixed := false
	for _, prefix := range []string{"LNK-", "CH-", "REG-"} {
		if strings.HasPrefix(s, prefix) {
			prefixed = true
		}
	}
	if !prefixed {
		return false
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b == '-' || identifierUpper(b) || b >= '0' && b <= '9' {
			continue
		}
		return false
	}
	return true
}

// identifierNamingRows reads the naming table, together with the byte
// span of every row it is written in, per file. A retired spelling
// standing in the row that retires it declares the spelling rather than
// referring to the channel, so those spans are no sites.
func identifierNamingRows(ctx context.Context, list scope.Lister, read scope.FileReader) ([]identifierNamingRow, map[string][][2]int, error) {
	var rows []identifierNamingRow
	declarations := map[string][][2]int{}
	files, err := identifierSectionFiles(ctx, list)
	if err != nil {
		return nil, nil, err
	}
	for _, target := range files {
		content, err := read(target)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", target, err)
		}
		text := string(content)
		at := 0
		inTable := false
		for _, line := range strings.SplitAfter(text, "\n") {
			start := at
			at += len(line)
			cells := identifierTableCells(line)
			if len(cells) == 0 {
				inTable = false
				continue
			}
			if identifierColumnHeading(cells) {
				inTable = true
				continue
			}
			if !inTable {
				continue
			}
			if identifierDelimiterRow(cells) {
				continue
			}
			if len(cells) < len(identifierNamingColumns) {
				return nil, nil, fmt.Errorf("naming table in %s: a row carries %d cell(s), want %d",
					target, len(cells), len(identifierNamingColumns))
			}
			row := identifierNamingRow{
				Channel:   strings.Trim(cells[0], "`"),
				Carrier:   strings.Trim(cells[1], "`"),
				Retired:   strings.Trim(cells[2], "`"),
				Canonical: strings.Trim(cells[3], "`"),
			}
			if row.Retired == "" || row.Canonical == "" {
				return nil, nil, fmt.Errorf("naming table in %s: a row for %s states no spelling", target, row.Channel)
			}
			rows = append(rows, row)
			declarations[target] = append(declarations[target], [2]int{start, at})
		}
	}
	if len(rows) == 0 {
		return nil, nil, fmt.Errorf("identifier-resolution gate: no %s* file carries a table headed %s",
			identifierSectionPrefix, strings.Join(identifierNamingColumns, " | "))
	}
	return rows, declarations, nil
}

// identifierSectionFiles returns the specification files the §28.3
// registers are written in, sorted.
func identifierSectionFiles(ctx context.Context, list scope.Lister) ([]string, error) {
	all, err := list(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tracked tree: %w", err)
	}
	var out []string
	for _, target := range all {
		if strings.HasPrefix(target, identifierSectionPrefix) && strings.HasSuffix(target, ".md") {
			out = append(out, target)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("identifier-resolution gate: the tree carries no %s* specification file",
			identifierSectionPrefix)
	}
	sort.Strings(out)
	return out, nil
}

// identifierTableCells splits a markdown table row into its cells, or
// returns nothing when the line is no table row.
func identifierTableCells(line string) []string {
	trimmed := strings.TrimSpace(strings.TrimRight(line, "\n"))
	if !strings.HasPrefix(trimmed, "|") {
		return nil
	}
	parts := strings.Split(strings.Trim(trimmed, "|"), "|")
	cells := make([]string, 0, len(parts))
	for _, p := range parts {
		cells = append(cells, strings.TrimSpace(p))
	}
	return cells
}

// identifierColumnHeading reports whether the cells are the naming
// table's heading row.
func identifierColumnHeading(cells []string) bool {
	if len(cells) < len(identifierNamingColumns) {
		return false
	}
	for i, want := range identifierNamingColumns {
		if !strings.EqualFold(strings.Trim(cells[i], "`"), want) {
			return false
		}
	}
	return true
}

// identifierDelimiterRow reports whether the cells are the alignment row
// that follows a heading.
func identifierDelimiterRow(cells []string) bool {
	for _, c := range cells {
		if strings.Trim(c, ":-") != "" {
			return false
		}
	}
	return true
}

// loadIdentifierSenses reads and validates the sense register into the
// per-file, per-position senses the gate excuses occurrences by.
//
// A missing, unreadable, or malformed register is an error rather than
// an empty set. A gate that degraded to excusing nothing would report
// every permanently correct non-channel occurrence, and the route back
// to green would be the widening this proposal forbids.
func loadIdentifierSenses(read scope.FileReader, target string) (map[string]map[int]identifierSenseEntry, error) {
	data, err := read(target)
	if err != nil {
		return nil, fmt.Errorf("read the identifier sense register %s: %w", target, err)
	}
	var doc identifierSenseDocument
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse the identifier sense register %s: %w", target, err)
	}
	if doc.Kind != "identifier-senses" {
		return nil, fmt.Errorf("identifier sense register %s: expected kind %q, got %q",
			target, "identifier-senses", doc.Kind)
	}
	if doc.Version != 1 {
		return nil, fmt.Errorf("identifier sense register %s: expected version 1, got %d", target, doc.Version)
	}
	if doc.Entries == nil {
		return nil, fmt.Errorf("identifier sense register %s: carries no entries block", target)
	}
	if len(*doc.Entries) == 0 {
		return nil, fmt.Errorf("identifier sense register %s: carries no entry", target)
	}
	senses := map[string]map[int]identifierSenseEntry{}
	for _, entry := range *doc.Entries {
		if entry.File == "" {
			return nil, fmt.Errorf("identifier sense register %s: an entry names no file", target)
		}
		if entry.Path == (entry.Occurrence > 0) {
			return nil, fmt.Errorf("identifier sense register %s: %s states neither one position nor exactly one",
				target, entry.File)
		}
		if entry.NotAChannel == (entry.Channel != "") {
			return nil, fmt.Errorf("identifier sense register %s: %s states neither one disposition nor exactly one",
				target, entry.File)
		}
		if _, ok := senses[entry.File]; !ok {
			senses[entry.File] = map[int]identifierSenseEntry{}
		}
		if _, ok := senses[entry.File][entry.key()]; ok {
			return nil, fmt.Errorf("identifier sense register %s: %s position %d is declared twice",
				target, entry.File, entry.key())
		}
		senses[entry.File][entry.key()] = entry
	}
	return senses, nil
}

// identifierFindingTexts renders a report's findings for a message.
func identifierFindingTexts(rep identifierResolutionReport) []string {
	out := make([]string, 0, len(rep.Findings))
	for _, f := range rep.Findings {
		out = append(out, f.String())
	}
	return out
}

// identifierResolutionFixtures is the fixture root for the cases below.
// Every fixture sits under testdata/, which is outside the read domain
// of every pass and every gate, so a fixture carrying a retired spelling
// verbatim is input to this gate rather than a site in the tree.
const identifierResolutionFixtures = "testdata/identifier-resolution"

// identifierTree is a fixture tracked tree the gate runs over. It holds
// the §28.3 registers as spec/28_communication-channels.md, whichever
// carriers a case writes, and the sense register at the path the gate
// reads.
type identifierTree struct {
	t    *testing.T
	root string
}

// newIdentifierTree returns a tree carrying the §28.3 fixture.
func newIdentifierTree(t *testing.T) *identifierTree {
	t.Helper()
	tr := &identifierTree{t: t, root: t.TempDir()}
	tr.copy("spec/28_communication-channels.md", "spec/28_registers.md")
	return tr
}

// carrier writes a carrier fixture into the tree at the given path.
func (tr *identifierTree) carrier(target, fixture string) {
	tr.t.Helper()
	tr.copy(target, filepath.Join("carriers", fixture))
}

// register writes a sense-register fixture at the path the gate reads.
func (tr *identifierTree) register(fixture string) {
	tr.t.Helper()
	tr.copy(identifierSenseRegisterPath, filepath.Join("registers", fixture))
}

// copy writes a fixture file into the tree.
func (tr *identifierTree) copy(target, fixture string) {
	tr.t.Helper()
	body, err := os.ReadFile(filepath.Join(identifierResolutionFixtures, filepath.FromSlash(fixture)))
	if err != nil {
		tr.t.Fatalf("read fixture %s: %v", fixture, err)
	}
	full := filepath.Join(tr.root, filepath.FromSlash(target))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		tr.t.Fatalf("create the directory for %s: %v", target, err)
	}
	if err := os.WriteFile(full, body, 0o644); err != nil {
		tr.t.Fatalf("write %s: %v", target, err)
	}
}

// remove deletes a file from the tree.
func (tr *identifierTree) remove(target string) {
	tr.t.Helper()
	if err := os.Remove(filepath.Join(tr.root, filepath.FromSlash(target))); err != nil {
		tr.t.Fatalf("remove %s: %v", target, err)
	}
}

// run drives the gate over the fixture tree.
func (tr *identifierTree) run() (identifierResolutionReport, error) {
	tr.t.Helper()
	return runIdentifierResolution(context.Background(),
		scope.DirLister(tr.root), scope.DirReader(tr.root))
}

// runOK drives the gate and fails the test when the run could not
// inspect the tree.
func (tr *identifierTree) runOK() identifierResolutionReport {
	tr.t.Helper()
	rep, err := tr.run()
	if err != nil {
		tr.t.Fatalf("run the identifier-resolution gate over the fixture tree: %v", err)
	}
	return rep
}

// spec: §28.1 (N2 and N7, one canonical identifier per channel, link,
// and register entry, and one spelling per carrier), §28.3 (the
// registers and the naming table)
func TestIdentifierResolutionCertifiesTheTree(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	rep, err := runIdentifierResolution(context.Background(),
		scope.GitLister(root), scope.DirReader(root))
	if err != nil {
		t.Fatalf("run the identifier-resolution gate over the tracked tree: %v", err)
	}
	if len(rep.Findings) > 0 {
		t.Errorf("%d occurrence(s) give a canonical identifier a second live spelling:\n  %s",
			len(rep.Findings), strings.Join(identifierFindingTexts(rep), "\n  "))
	}
	// A run over a fraction of the tree, or over an empty identifier
	// set, reports no finding for the same reason a clean tree does.
	if rep.Files == 0 {
		t.Errorf("the gate inspected no file; a report of no finding over an unread tree certifies nothing")
	}
	if rep.Identifiers == 0 || rep.Retired == 0 {
		t.Errorf("the gate read %d canonical identifier(s) and %d retired spelling(s) from §28.3; "+
			"a run with either set empty resolves nothing", rep.Identifiers, rep.Retired)
	}
	t.Logf("%d file(s) in the identifier pass's write domain, %d canonical identifier(s), %d retired spelling(s), "+
		"%d occurrence(s) read, %d excused by %s",
		rep.Files, rep.Identifiers, rep.Retired, rep.Sites, rep.Excused, identifierSenseRegisterPath)
}

// spec: §28.1 (N7, a channel takes one spelling per carrier), §28.3 (the
// naming table)
func TestIdentifierResolutionAcceptsAnIdentifierAtOneSpelling(t *testing.T) {
	t.Parallel()
	tr := newIdentifierTree(t)
	tr.carrier("pkg/adapter/runtimeops.go", "canonical.go.txt")
	tr.register("other-file.yaml")

	rep := tr.runOK()
	if len(rep.Findings) != 0 {
		t.Errorf("an identifier written at its canonical spelling alone was reported: %s",
			strings.Join(identifierFindingTexts(rep), "; "))
	}
	if rep.Sites != 0 {
		t.Errorf("read %d retired-spelling occurrence(s) over a tree that carries none", rep.Sites)
	}
}

// spec: §28.1 (N7, a channel takes one spelling per carrier), §28.3 (the
// naming table)
func TestIdentifierResolutionReportsAnIdentifierAtTwoSpellings(t *testing.T) {
	t.Parallel()
	tr := newIdentifierTree(t)
	tr.carrier("pkg/adapter/runtimeops.go", "canonical.go.txt")
	tr.carrier("pkg/gateway/client.go", "retired.go.txt")
	tr.register("runtimeops-reference.yaml")

	rep := tr.runOK()
	if len(rep.Findings) != 1 {
		t.Fatalf("reported %d finding(s), want 1: %s", len(rep.Findings),
			strings.Join(identifierFindingTexts(rep), "; "))
	}
	got := rep.Findings[0]
	if got.Path != "pkg/gateway/client.go" || got.Line != 6 {
		t.Errorf("the finding names %s line %d, want pkg/gateway/client.go line 6", got.Path, got.Line)
	}
	if got.CanonicalPath != "pkg/adapter/runtimeops.go" {
		t.Errorf("the finding names %q as the carrier of the canonical spelling, want pkg/adapter/runtimeops.go",
			got.CanonicalPath)
	}
	rendered := got.String()
	for _, want := range []string{"pkg/gateway/client.go", "pkg/adapter/runtimeops.go", "LifecycleChannel", "RuntimeOps"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the rendered finding %q does not name %q", rendered, want)
		}
	}
}

// spec: §28.1 (N7, the naming table states the spelling per carrier),
// §28.3 (the naming table row declares the retired spelling rather than
// referring to the channel)
func TestIdentifierResolutionReadsTheNamingTableRowAsADeclaration(t *testing.T) {
	t.Parallel()
	// The tree carries the §28.3 fixture and no carrier writing a
	// retired spelling, so every occurrence the walk meets stands in the
	// naming table itself.
	tr := newIdentifierTree(t)
	tr.carrier("pkg/adapter/runtimeops.go", "canonical.go.txt")
	tr.register("other-file.yaml")

	rep := tr.runOK()
	if rep.Sites != 0 || len(rep.Findings) != 0 {
		t.Errorf("the naming table's own rows were read as %d occurrence(s) and reported as %d finding(s): %s",
			rep.Sites, len(rep.Findings), strings.Join(identifierFindingTexts(rep), "; "))
	}
}

// spec: §28.1 (N7, a retired spelling standing where the text is not a
// channel is no reference to one), §28.3 (the naming table)
func TestIdentifierResolutionAcceptsAnOccurrenceRecordedAsNotAChannel(t *testing.T) {
	t.Parallel()
	tr := newIdentifierTree(t)
	// The worked case is the Azure storage management-policy file
	// argument, which carries the socket token and names a policy
	// document rather than the channel.
	tr.carrier("spec/17_deployment-topology.md", "az-file-argument.md")
	tr.register("not-a-channel.yaml")

	rep := tr.runOK()
	if rep.Sites != 1 {
		t.Fatalf("read %d occurrence(s), want the one the file argument carries", rep.Sites)
	}
	if rep.Excused != 1 || len(rep.Findings) != 0 {
		t.Errorf("the file argument was excused %d time(s) and reported as %d finding(s): %s",
			rep.Excused, len(rep.Findings), strings.Join(identifierFindingTexts(rep), "; "))
	}
}

// spec: §28.1 (N7, a channel reference takes the canonical spelling),
// §28.3 (the naming table)
func TestIdentifierResolutionReportsAChannelReferenceLeftAtTheRetiredSpelling(t *testing.T) {
	t.Parallel()
	// The worked case is the socket token in a root-level contract
	// document, which is a genuine reference to the channel and so has
	// to be rewritten rather than excused.
	tr := newIdentifierTree(t)
	tr.carrier("TESTING.md", "socket-token.md")
	tr.register("channel-reference.yaml")

	rep := tr.runOK()
	if len(rep.Findings) != 1 {
		t.Fatalf("reported %d finding(s), want 1: %s", len(rep.Findings),
			strings.Join(identifierFindingTexts(rep), "; "))
	}
	got := rep.Findings[0]
	if got.Path != "TESTING.md" {
		t.Errorf("the finding names %q, want TESTING.md", got.Path)
	}
	if len(got.Identifiers) != 1 || got.Identifiers[0] != "CH-RUNTIMEOPS" {
		t.Errorf("the finding names %v, want CH-RUNTIMEOPS alone", got.Identifiers)
	}
	if !strings.Contains(got.Reason, "CH-RUNTIMEOPS") {
		t.Errorf("the finding's reason %q does not state that the register resolves the occurrence to the channel",
			got.Reason)
	}
}

// spec: §28.1 (N7, a channel reference takes the canonical spelling),
// §28.3 (the naming table)
func TestIdentifierResolutionReportsAnUnregisteredOccurrence(t *testing.T) {
	t.Parallel()
	// A retired spelling the register carries no sense for is reported
	// under every identifier the naming table maps it to, because one
	// retired spelling denotes two channels and the gate resolves no
	// occurrence by itself.
	tr := newIdentifierTree(t)
	tr.carrier("pkg/gateway/client.go", "retired.go.txt")
	tr.register("other-file.yaml")

	rep := tr.runOK()
	if len(rep.Findings) != 1 {
		t.Fatalf("reported %d finding(s), want 1: %s", len(rep.Findings),
			strings.Join(identifierFindingTexts(rep), "; "))
	}
	got := rep.Findings[0]
	if len(got.Identifiers) != 2 {
		t.Errorf("the finding names %v, want both channels the naming table maps LifecycleChannel to",
			got.Identifiers)
	}
	if !strings.Contains(got.Reason, "records no sense") {
		t.Errorf("the finding's reason %q does not state that the register carries no sense for the occurrence",
			got.Reason)
	}
}

// spec: §28.1 (N7, the naming law reaches the file-name stem), §28.3
// (the naming table's path rows)
func TestIdentifierResolutionReportsAFileNameAtTheRetiredSpelling(t *testing.T) {
	t.Parallel()
	tr := newIdentifierTree(t)
	tr.carrier("pkg/adapter/lifecyclechannel.go", "no-occurrence.go.txt")
	tr.register("other-file.yaml")

	rep := tr.runOK()
	if len(rep.Findings) != 1 {
		t.Fatalf("reported %d finding(s), want the file name: %s", len(rep.Findings),
			strings.Join(identifierFindingTexts(rep), "; "))
	}
	got := rep.Findings[0]
	if got.Path != "pkg/adapter/lifecyclechannel.go" || got.Line != 0 {
		t.Errorf("the finding names %s line %d, want the file name of pkg/adapter/lifecyclechannel.go",
			got.Path, got.Line)
	}
	if !strings.Contains(got.String(), "file name") {
		t.Errorf("the rendered finding %q does not state that the occurrence stands in the file name", got.String())
	}
}

// spec: §28.1 (N7, one spelling per carrier), §28.3 (the sense register
// is the exemption route the gate reads)
func TestIdentifierResolutionFailsOnAMissingOrMalformedSenseRegister(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		fixture  string
		remove   bool
		contains string
	}{
		{name: "missing", remove: true, contains: "read the identifier sense register"},
		{name: "unparseable", fixture: "malformed.yaml", contains: "parse the identifier sense register"},
		{name: "wrong kind", fixture: "wrong-kind.yaml", contains: "expected kind"},
		{name: "no entries block", fixture: "no-entries-block.yaml", contains: "carries no entries block"},
		{name: "empty entries", fixture: "no-entries.yaml", contains: "carries no entry"},
		{name: "two dispositions", fixture: "two-dispositions.yaml", contains: "disposition"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tr := newIdentifierTree(t)
			tr.carrier("spec/17_deployment-topology.md", "az-file-argument.md")
			tr.register("not-a-channel.yaml")
			if tc.remove {
				tr.remove(identifierSenseRegisterPath)
			} else {
				tr.register(tc.fixture)
			}
			_, err := tr.run()
			if err == nil {
				t.Fatalf("the gate certified a tree whose sense register is %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.contains) {
				t.Errorf("the failure %q does not state %q", err, tc.contains)
			}
		})
	}
}

// spec: §28.1 (N2, one canonical identifier per channel), §28.3 (the
// registers are the source of the identifier set)
func TestIdentifierResolutionFailsOnAnUnreadableIdentifierSet(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		fixture  string
		contains string
	}{
		{name: "no register section", fixture: "spec/28_no-registers.md", contains: "register section"},
		{
			name:     "a naming table row naming an unregistered identifier",
			fixture:  "spec/28_unregistered-channel.md",
			contains: "the §28.3 registers do not carry",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tr := &identifierTree{t: t, root: t.TempDir()}
			tr.copy("spec/28_communication-channels.md", tc.fixture)
			tr.carrier("pkg/adapter/runtimeops.go", "canonical.go.txt")
			tr.register("other-file.yaml")
			_, err := tr.run()
			if err == nil {
				t.Fatalf("the gate certified a tree whose §28.3 registers it could not read")
			}
			if !strings.Contains(err.Error(), tc.contains) {
				t.Errorf("the failure %q does not state %q", err, tc.contains)
			}
		})
	}
}

// spec: §28.1 (N7, the rename retires a spelling per carrier), §28.3
// (the naming table)
func TestIdentifierResolutionObservesTheRename(t *testing.T) {
	t.Parallel()
	// The gate is red against a tree in the state the rename starts from
	// and green against the same tree once the pass has run, so it is
	// known to observe the rename rather than to be inert. The register
	// resolves the occurrence to its channel in the first tree, which is
	// the disposition the pass rewrites and the gate refuses to excuse.
	before := newIdentifierTree(t)
	before.carrier("pkg/adapter/lifecyclechannel.go", "before.go.txt")
	before.register("before.yaml")
	red := before.runOK()
	if len(red.Findings) == 0 {
		t.Errorf("the gate certified a tree carrying the retired spelling in a carrier and in its file name")
	}

	after := newIdentifierTree(t)
	after.carrier("pkg/adapter/runtimeops.go", "after.go.txt")
	after.register("other-file.yaml")
	green := after.runOK()
	if len(green.Findings) != 0 {
		t.Errorf("the gate reported the renamed tree: %s", strings.Join(identifierFindingTexts(green), "; "))
	}
}

// spec: §28.1 (N7, one spelling per carrier), §28.3 (the registers)
func TestIdentifierResolutionFailsOnAnEmptyWalk(t *testing.T) {
	t.Parallel()
	// A tree the walk selects nothing from reports no finding for the
	// same reason a clean tree does, so the run fails instead.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "testdata"), 0o755); err != nil {
		t.Fatalf("create the fixture tree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "testdata", "held.md"), []byte("held out of every domain\n"), 0o644); err != nil {
		t.Fatalf("write the held-out file: %v", err)
	}
	_, err := runIdentifierResolution(context.Background(), scope.DirLister(root), scope.DirReader(root))
	if err == nil {
		t.Fatalf("the gate certified a tree it selected no file from")
	}
}
