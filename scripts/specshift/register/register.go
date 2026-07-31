// SPDX-License-Identifier: MIT

// Package register holds the specshift residual register: its loader,
// its validator, and the downward-only rewrite the passes and the gates
// share.
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
// loading as an empty one.
const Kind = "specshift-residual-register"

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
	Member      string      `yaml:"member"`
	Class       string      `yaml:"class"`
	Disposition Disposition `yaml:"disposition"`
	Reason      string      `yaml:"reason"`
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

// Load reads and validates a residual register. A missing, unreadable,
// or malformed register fails rather than loading as an empty document,
// because a gate that read a register as empty would report every
// triaged member as a residual and a pass that read one as empty would
// report no work and exit clean.
func Load(path string) (*Document, error) {
	data, err := os.ReadFile(path)
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
	for i, e := range *d.Entries {
		if strings.TrimSpace(e.Member) == "" {
			return fmt.Errorf("residual register %s: entry %d carries no member", where, i)
		}
		if e.Class != d.Class {
			return fmt.Errorf("residual register %s: entry %q carries class %q, want %q",
				where, e.Member, e.Class, d.Class)
		}
		if e.Disposition != InClass && e.Disposition != Excluded {
			return fmt.Errorf("residual register %s: entry %q carries disposition %q, want %q or %q",
				where, e.Member, e.Disposition, InClass, Excluded)
		}
		if strings.TrimSpace(e.Reason) == "" {
			return fmt.Errorf("residual register %s: entry %q carries no reason", where, e.Member)
		}
		if seen[e.Member] {
			return fmt.Errorf("residual register %s: entry %q is declared twice", where, e.Member)
		}
		seen[e.Member] = true
	}
	return nil
}

// Members returns the members the register carries, sorted.
func (d *Document) Members() []string {
	out := make([]string, 0, len(*d.Entries))
	for _, e := range *d.Entries {
		out = append(out, e.Member)
	}
	sort.Strings(out)
	return out
}

// Carries reports whether the register triaged the member.
func (d *Document) Carries(member string) bool {
	for _, e := range *d.Entries {
		if e.Member == member {
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
		known[m] = true
	}
	var out []string
	for _, m := range matched {
		if known[m] || d.Carries(m) {
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
			removed = append(removed, e.Member)
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

// SaveTo writes the document to path.
func (d *Document) SaveTo(path string) error {
	if err := d.Validate(); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# SPDX-License-Identifier: MIT\n#\n")
	fmt.Fprintf(&b, "# Residual register for the %s class.\n#\n", d.Class)
	b.WriteString("# Each entry records a triage decision over a member the class's\n")
	b.WriteString("# broad predicate matched: in-class, so the class's pass or\n")
	b.WriteString("# remediation reaches it, or excluded, so it never belonged to the\n")
	b.WriteString("# class. The file is rewritten downward: an in-class entry is\n")
	b.WriteString("# removed in the run in which its member stops matching the\n")
	b.WriteString("# predicate, and no run adds an entry.\n")
	out, err := yaml.Marshal(d)
	if err != nil {
		return fmt.Errorf("encode residual register %s: %w", path, err)
	}
	b.Write(out)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create directory for residual register %s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write residual register %s: %w", path, err)
	}
	return nil
}
