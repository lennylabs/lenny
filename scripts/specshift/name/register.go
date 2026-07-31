// SPDX-License-Identifier: MIT

package name

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// The declaration the sense register carries. It is a driving register
// rather than a residual one (tests/registers/README.md): it is keyed by
// file and occurrence for the rewrite it drives, and it is emptied by
// the change that runs the pass over the whole domain rather than
// carrying an owner and an expiry.
const (
	registerKind    = "reserved-phrase-senses"
	registerVersion = 1
)

// Entry is one site's sense: the identifiers the site denotes, and the
// replacement text they sit in.
type Entry struct {
	// File is the repo-relative path of the carrier, and Occurrence is
	// the 1-based position of the site among the reserved-phrase sites
	// that file carries, in source order.
	File       string `yaml:"file"`
	Occurrence int    `yaml:"occurrence"`
	// Identifiers are the canonical identifiers the site denotes, drawn
	// from the whole identifier space the specification declares.
	Identifiers []string `yaml:"identifiers"`
	// Replacement is the text written at the site. It is required of an
	// entry naming more than one identifier, which is how the entry
	// records the position each identifier takes, and is optional for an
	// entry naming one, where the identifier alone is the substitution.
	Replacement string `yaml:"replacement"`
}

// substitution returns the text the pass writes at the site.
func (e Entry) substitution() string {
	if e.Replacement != "" {
		return e.Replacement
	}
	return e.Identifiers[0]
}

// senseDocument is the register as it is written.
type senseDocument struct {
	Kind    string `yaml:"kind"`
	Version int    `yaml:"version"`
	// Entries is a pointer so a document that declares no entries block
	// is distinguishable from one that declares an empty list. The first
	// is malformed; the second is a register with no site left, which
	// the loader still refuses, because a pass driven by it would report
	// the zero work of a completed migration.
	Entries *[]Entry `yaml:"entries"`
}

// loadSenses reads and validates the sense register into the per-file,
// per-occurrence senses the pass is driven by.
//
// A missing, unreadable, or malformed register is an error rather than
// an empty set. A load that degraded to carrying nothing would abort the
// run at the first site in the tree, which reads as a register that has
// not been seeded, and a run over a tree the pass had already rewritten
// would report a completed migration it never performed.
func loadSenses(path string) (map[string]map[int]Entry, error) {
	data, err := os.ReadFile(filepath.FromSlash(path))
	if err != nil {
		return nil, fmt.Errorf("read the reserved-phrase sense register %s: %w", path, err)
	}
	var doc senseDocument
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse the reserved-phrase sense register %s: %w", path, err)
	}
	if doc.Kind != registerKind {
		return nil, fmt.Errorf("reserved-phrase sense register %s: expected kind %q, got %q", path, registerKind, doc.Kind)
	}
	if doc.Version != registerVersion {
		return nil, fmt.Errorf("reserved-phrase sense register %s: expected version %d, got %d", path, registerVersion, doc.Version)
	}
	if doc.Entries == nil {
		return nil, fmt.Errorf("reserved-phrase sense register %s: carries no entries block", path)
	}
	if len(*doc.Entries) == 0 {
		return nil, fmt.Errorf("reserved-phrase sense register %s: carries no entry", path)
	}
	senses := map[string]map[int]Entry{}
	for i, entry := range *doc.Entries {
		if err := validateEntry(path, i, entry); err != nil {
			return nil, err
		}
		if _, ok := senses[entry.File]; !ok {
			senses[entry.File] = map[int]Entry{}
		}
		if _, ok := senses[entry.File][entry.Occurrence]; ok {
			return nil, fmt.Errorf("reserved-phrase sense register %s: %s occurrence %d is declared twice",
				path, entry.File, entry.Occurrence)
		}
		senses[entry.File][entry.Occurrence] = entry
	}
	return senses, nil
}

// validateEntry reports the first schema defect in one entry.
//
// The replacement rules are what make a multi-identifier entry record a
// position per identifier: the text is written at the site verbatim, so
// an identifier the entry names and the replacement omits would not
// reach the tree, and a replacement that carries a reserved phrase would
// leave the site in the population the pass exists to empty.
func validateEntry(path string, i int, e Entry) error {
	where := fmt.Sprintf("reserved-phrase sense register %s: entry %d", path, i)
	if strings.TrimSpace(e.File) == "" {
		return fmt.Errorf("%s carries no file", where)
	}
	where = fmt.Sprintf("reserved-phrase sense register %s: %s occurrence %d", path, e.File, e.Occurrence)
	if e.Occurrence < 1 {
		return fmt.Errorf("reserved-phrase sense register %s: entry %d for %s carries occurrence %d, and occurrences are numbered from one",
			path, i, e.File, e.Occurrence)
	}
	if len(e.Identifiers) == 0 {
		return fmt.Errorf("%s names no identifier", where)
	}
	for _, id := range e.Identifiers {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("%s names an empty identifier", where)
		}
	}
	if e.Replacement == "" {
		if len(e.Identifiers) > 1 {
			return fmt.Errorf("%s names %d identifiers and records no replacement, so the position of each is unstated",
				where, len(e.Identifiers))
		}
		return reservedFree(where, e.Identifiers[0])
	}
	for _, id := range e.Identifiers {
		if !strings.Contains(e.Replacement, id) {
			return fmt.Errorf("%s names %s and its replacement %q does not carry it", where, id, e.Replacement)
		}
	}
	return reservedFree(where, e.Replacement)
}

// reservedFree reports a substitution that itself carries a reserved
// noun phrase, which would leave the site standing in the population
// after the pass had written it.
func reservedFree(where, substitution string) error {
	if reservedExpr.MatchString(substitution) {
		return fmt.Errorf("%s substitutes %q, which is itself a site of the class the pass removes", where, substitution)
	}
	return nil
}
