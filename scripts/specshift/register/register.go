// SPDX-License-Identifier: MIT

// Package register holds the residual register: its schema, its loader,
// its validator, its writer, and the downward-only rewrite. The passes,
// the gates, and the residual scan share this one contract, so an entry
// a gate writes is an entry a pass reads.
//
// A residual register records a triage decision over one class. Each
// entry is a member, the class it was triaged into, a disposition of
// in-class or excluded, and a reason. It carries no owner, no opened-at
// date, no expiry, and no blocker, because an exclusion is a permanent
// statement about the tree and an in-class entry is retired by the event
// that takes its member out of the class rather than by a date.
//
// A pass register drives a rewrite and is keyed for that rewrite; a
// residual register records why a member the broad predicate matched is
// not a defect. The two are held separately for that reason.
package register

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Kind is the kind declaration every residual register carries. A
// document without it is not a register and fails to load rather than
// loading as an empty one. It is the residual family's own kind rather
// than the shared exception-register contract's, which is what keeps the
// shared validator's expiry and blocker ratchet rules off these entries.
const Kind = "residual-register"

// Version is the schema version the loader accepts.
const Version = 1

// Disposition is the triage outcome recorded for a member.
type Disposition string

// The dispositions a residual entry may carry.
const (
	// InClass means the member belongs to the class, so the class's pass
	// or remediation reaches it. In a class whose members can leave it
	// the entry is transitional and the downward rewrite removes it in
	// the run in which its member stops matching the class predicate.
	InClass Disposition = "in-class"
	// Excluded means the member never belonged to the class. The entry
	// is permanent.
	Excluded Disposition = "excluded"
)

// Entry is one triage decision.
type Entry struct {
	// Member is the member as the class's predicate reads it: the text of
	// a citation, a tracked path, or a call site's key.
	//
	// It is stored with the continuation join of the occurrence it was
	// read from preserved, so a member read across two comment lines is
	// written across two lines here. A wrapped citation folded onto one
	// line would be a citation the form reads, under the path of the
	// register itself, and a register is an ordinary member of the read
	// domain the citation resolver and the ratchet share. The key an
	// entry is held under is the stored text with those line breaks
	// joined back into spaces, which MemberKey returns.
	Member string `yaml:"member"`
	// Class names the class the entry belongs to. It repeats the class
	// the register is read for, so an entry cannot be moved between
	// registers by a copy that leaves its class behind.
	Class string `yaml:"class"`
	// Disposition is in-class or excluded.
	Disposition Disposition `yaml:"disposition"`
	// Reason is why the member is in the class or outside it.
	Reason string `yaml:"reason"`
}

// Key returns the key the entry is held under.
func (e Entry) Key() string { return MemberKey(e.Member) }

// String renders the entry for a report.
func (e Entry) String() string {
	return fmt.Sprintf("%s (%s, %s)", e.Key(), e.Class, e.Disposition)
}

// MemberKey returns the key a stored member text is held under, which is
// that text with every preserved continuation join folded back into the
// space it stands for. A member is keyed by the text the class's
// predicate reads rather than by the spelling the register stores, so
// the same occurrence read wrapped in one carrier and unwrapped in
// another is one member.
func MemberKey(stored string) string {
	return strings.ReplaceAll(stored, "\n", " ")
}

// Document is a residual register. Entries is a pointer so a document
// that declares no entries block is distinguishable from one that
// declares an empty list; the first is malformed and the second is a
// class with no residual.
type Document struct {
	Kind    string   `yaml:"kind"`
	Version int      `yaml:"version"`
	Class   string   `yaml:"class"`
	Entries *[]Entry `yaml:"entries"`

	// path is where the document was loaded from, so the downward
	// rewrite writes back to the file it read.
	path string
}

// Reader reads a repo-relative path. It is the one read dependency the
// loader takes, so a gate driving the loader over a fixture tree passes
// its own reader rather than a checkout path.
type Reader func(path string) ([]byte, error)

// Writer writes a repo-relative path.
type Writer func(path string, content []byte) error

// Load reads and validates a residual register from the filesystem.
func Load(path string) (*Document, error) {
	return Read(os.ReadFile, path)
}

// Read reads and validates a residual register through the reader given.
//
// A missing, unreadable, or malformed register fails rather than loading
// as an empty document, because a gate that read a register as empty
// would report every triaged member as a residual and a pass that read
// one as empty would report no work and exit clean.
func Read(read Reader, path string) (*Document, error) {
	if read == nil {
		return nil, fmt.Errorf("read residual register %s: no reader is configured", path)
	}
	data, err := read(path)
	if err != nil {
		return nil, fmt.Errorf("read residual register %s: %w", path, err)
	}
	var doc Document
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse residual register %s: %w", path, err)
	}
	doc.path = path
	if err := doc.Validate(); err != nil {
		return nil, err
	}
	return &doc, nil
}

// ReadFor reads a register and holds it to the class it is read for, so
// a register copied to another class's path is refused rather than
// exempting that class's population.
func ReadFor(read Reader, path, class string) (*Document, error) {
	doc, err := Read(read, path)
	if err != nil {
		return nil, err
	}
	if doc.Class != class {
		return nil, fmt.Errorf("residual register %s: declares class %q, and it is read for class %q",
			path, doc.Class, class)
	}
	return doc, nil
}

// Validate reports the first schema defect in the document.
func (d *Document) Validate() error {
	where := d.path
	if where == "" {
		where = "residual register"
	}
	if d.Kind != Kind {
		return fmt.Errorf("residual register %s: expected kind %q, got %q", where, Kind, d.Kind)
	}
	if d.Version != Version {
		return fmt.Errorf("residual register %s: expected version %d, got %d", where, Version, d.Version)
	}
	if strings.TrimSpace(d.Class) == "" {
		return fmt.Errorf("residual register %s: declares no class", where)
	}
	if d.Entries == nil {
		return fmt.Errorf("residual register %s: carries no entries block", where)
	}
	seen := make(map[string]bool, len(*d.Entries))
	for _, e := range *d.Entries {
		if err := ValidEntry(where, d.Class, e); err != nil {
			return err
		}
		if seen[e.Key()] {
			return fmt.Errorf("residual register %s: member %q is declared twice", where, e.Key())
		}
		seen[e.Key()] = true
	}
	return nil
}

// ValidEntry holds one entry to the schema. A reason is required on both
// dispositions: an entry with no reason records that someone looked at
// the member without recording what they concluded, which is the silent
// widening the register exists to prevent.
func ValidEntry(where, class string, e Entry) error {
	if strings.TrimSpace(e.Member) == "" {
		return fmt.Errorf("residual register %s: carries an entry with no member", where)
	}
	if err := validMemberText(where, e.Member); err != nil {
		return err
	}
	if e.Class != class {
		return fmt.Errorf("residual register %s: member %q declares class %q, and the register is read for class %q",
			where, e.Member, e.Class, class)
	}
	if e.Disposition != InClass && e.Disposition != Excluded {
		return fmt.Errorf("residual register %s: member %q carries disposition %q, want %q or %q",
			where, e.Member, e.Disposition, InClass, Excluded)
	}
	if strings.TrimSpace(e.Reason) == "" {
		return fmt.Errorf("residual register %s: member %q carries no reason", where, e.Member)
	}
	if strings.ContainsAny(e.Reason, "\n\r") {
		return fmt.Errorf("residual register %s: member %q carries a reason with a line break, and a reason is one line",
			where, e.Member)
	}
	return nil
}

// validMemberText holds a stored member text to the spelling the
// register writes. A line break stands for a continuation the class's
// predicate joined, so each line carries content and neither opens nor
// closes on whitespace: a blank or indented line would not survive the
// block scalar it is written in, and the member the next run reads back
// would differ from the one recorded.
func validMemberText(where, member string) error {
	if strings.Contains(member, "\r") {
		return fmt.Errorf("residual register %s: member %q carries a carriage return", where, member)
	}
	for _, line := range strings.Split(member, "\n") {
		if strings.TrimSpace(line) == "" {
			return fmt.Errorf("residual register %s: member %q carries an empty line", where, MemberKey(member))
		}
		if line != strings.TrimSpace(line) {
			return fmt.Errorf("residual register %s: member %q carries a line opening or closing on whitespace",
				where, MemberKey(member))
		}
	}
	return nil
}

// Members returns the members the register carries, keyed and sorted.
func (d *Document) Members() []string {
	out := make([]string, 0, len(*d.Entries))
	for _, e := range *d.Entries {
		out = append(out, e.Key())
	}
	sort.Strings(out)
	return out
}

// Keyed returns the entries by member key together with the order they
// were written in, which is what a gate compares a class's live
// population against.
func (d *Document) Keyed() (map[string]Entry, []string) {
	carried := make(map[string]Entry, len(*d.Entries))
	order := make([]string, 0, len(*d.Entries))
	for _, e := range *d.Entries {
		carried[e.Key()] = e
		order = append(order, e.Key())
	}
	return carried, order
}

// Carries reports whether the register triaged the member.
func (d *Document) Carries(member string) bool {
	key := MemberKey(member)
	for _, e := range *d.Entries {
		if e.Key() == key {
			return true
		}
	}
	return false
}

// Residual returns the members the broad predicate matched that neither
// the enumeration nor the register carries, sorted. A non-empty result
// is a build failure: it is never skipped and never silently absorbed.
func (d *Document) Residual(matched, enumerated []string) []string {
	known := make(map[string]bool, len(enumerated))
	for _, m := range enumerated {
		known[MemberKey(m)] = true
	}
	var out []string
	for _, m := range matched {
		if known[MemberKey(m)] || d.Carries(m) {
			continue
		}
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// RewriteDownward removes every in-class entry whose member the class
// predicate no longer matches, and returns the removed members sorted.
// The rewrite is downward only: it never adds an entry, so a member the
// predicate matches that the register does not carry stays a residual
// and fails the build rather than being absorbed.
//
// An excluded entry is never removed, because the member never belonged
// to the class and nothing takes it out. An in-class entry in a class
// whose members cannot leave it, which is the generated-artifact class,
// survives for the same reason: its member keeps matching the
// predicate, so the predicate passed here keeps returning true for it.
//
// Removal happens in the same run in which the member stops matching, so
// ordinary work that takes a member out of its class does not leave a
// dead entry behind and does not turn the gate red.
func (d *Document) RewriteDownward(matches func(Entry) bool) []string {
	kept := make([]Entry, 0, len(*d.Entries))
	var removed []string
	for _, e := range *d.Entries {
		if e.Disposition == InClass && !matches(e) {
			removed = append(removed, e.Key())
			continue
		}
		kept = append(kept, e)
	}
	*d.Entries = kept
	sort.Strings(removed)
	return removed
}

// Save writes the document back to the path it was loaded from.
func (d *Document) Save() error {
	if d.path == "" {
		return fmt.Errorf("save residual register: document has no path")
	}
	return d.SaveTo(d.path)
}

// SaveTo writes the document to path, creating the directory it sits in.
func (d *Document) SaveTo(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create directory for residual register %s: %w", path, err)
	}
	return Write(func(target string, content []byte) error {
		return os.WriteFile(target, content, 0o644)
	}, path, d.Class, *d.Entries)
}

// Write writes a register holding exactly the entries given. It is the
// one writer of the file: the downward removal calls it with the entries
// that survived a run, and the seeding of a register calls it with the
// members a scan selected, so the file is never hand-assembled from a
// population stated elsewhere.
//
// Every value is written as a literal block scalar. A member is a copy
// of text the class's predicate reads, and a sibling class scans this
// file as ordinary tree content, so a quoted or wrapped spelling would
// present the sibling with a member that differs from the one recorded
// here by an escape or a line break. The seeding would then record the
// altered copy, whose own written form differs again, and it would not
// converge.
func Write(write Writer, path, class string, entries []Entry) error {
	if write == nil {
		return fmt.Errorf("rewrite residual register %s: no writer is configured", path)
	}
	body, err := Render(path, class, entries)
	if err != nil {
		return err
	}
	if err := write(path, []byte(body)); err != nil {
		return fmt.Errorf("rewrite residual register %s: %w", path, err)
	}
	return nil
}

// Render returns the text of a register holding exactly the entries
// given, sorted by member key.
func Render(path, class string, entries []Entry) (string, error) {
	sorted := append([]Entry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Key() < sorted[j].Key() })
	var b strings.Builder
	b.WriteString(Header(class))
	fmt.Fprintf(&b, "kind: %s\nversion: %d\nclass: %s\n", Kind, Version, class)
	// An empty class declares an empty list rather than an absent one,
	// because a document with no entries block is the malformed register
	// the loader refuses, and a class whose predicate selects nothing is
	// the terminal state the pass and the remediation reach.
	if len(sorted) == 0 {
		b.WriteString("entries: []\n")
	} else {
		b.WriteString("entries:\n")
	}
	for i, entry := range sorted {
		if err := ValidEntry(path, class, entry); err != nil {
			return "", fmt.Errorf("rewrite residual register %s: %w", path, err)
		}
		if i > 0 && sorted[i-1].Key() == entry.Key() {
			return "", fmt.Errorf("rewrite residual register %s: member %q is written twice", path, entry.Key())
		}
		b.WriteString("  - member: |-\n" + blockScalar(entry.Member))
		b.WriteString("    class: " + entry.Class + "\n")
		b.WriteString("    disposition: " + string(entry.Disposition) + "\n")
		b.WriteString("    reason: |-\n" + blockScalar(entry.Reason))
	}
	return b.String(), nil
}

// blockScalar renders a value as the body of a literal block scalar, one
// indented line per line of the value. A member written across two lines
// keeps the continuation join the occurrence carried, so the stored copy
// is not a citation the form reads.
func blockScalar(value string) string {
	var b strings.Builder
	for _, line := range strings.Split(value, "\n") {
		b.WriteString("      " + line + "\n")
	}
	return b.String()
}

// Header explains the file to a reader who opens it without the gate
// beside them. It states no section reference and no line number of its
// own, because a sibling class reads this file as tree content and a
// citation written here would be a member of that class.
func Header(class string) string {
	return fmt.Sprintf(`# SPDX-License-Identifier: MIT
#
# Residual register for the %s class.
#
# Each entry is a member of the class that the enumeration the migration
# works from does not carry. An in-class entry belongs to the class, and
# its route out is the class's own pass or remediation. An excluded entry
# never belonged to the class, and it is permanent.
#
# The residual gate in tests/tier0_static fails tier 0 on a member that
# matches the class's broad predicate and appears in neither the
# enumeration nor this file, so a member no enumeration anticipated is
# triaged here rather than absorbed. The gate removes an in-class entry
# whose member has left the class in the same run, and it never adds an
# entry.
`, class)
}
