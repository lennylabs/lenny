// SPDX-License-Identifier: MIT

package anchor

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// The sense register and its declaration.
//
// The register sits in the tree rather than on the command line, because
// the map is what drives the run and the senses are a property of the
// tree the run reads, the way the pinned-literal register is for the
// name pass. It records, per occurrence, the heading a bare section
// citation means, for the occurrences the map alone cannot decide: a
// reduction that carves material out leaves citations of that material
// pointing at a heading that stays where it is, while the map carries
// one successor for the retired anchor as a whole.
const (
	senseRegisterPath    = "tests/registers/anchor-senses.yaml"
	senseRegisterKind    = "anchor-senses"
	senseRegisterVersion = 1
)

// Sense is what one bare citation means: the carrier, the 1-based
// position of the citation among the retired-section citations that file
// carries in source order, and the heading it cites.
//
// The key is the position rather than the citation's text, because a
// file carries the same retired section number at several sites whose
// destinations differ, which is the whole reason the register exists.
type Sense struct {
	File       string `yaml:"file"`
	Occurrence int    `yaml:"occurrence"`
	// Destination is the heading the occurrence cites, written in the
	// `<path>#<anchor>` fragment-link spelling. Every recorded
	// occurrence names one: the register answers what a citation means,
	// and an entry that records an occurrence without naming a heading
	// resolves nothing.
	Destination string `yaml:"destination"`
}

// target returns the destination as a heading reference.
func (s Sense) target() Target {
	file, anchor, _ := strings.Cut(s.Destination, "#")
	return Target{File: file, Anchor: anchor}
}

// senseDocument is the sense register as it is written.
type senseDocument struct {
	Kind    string `yaml:"kind"`
	Version int    `yaml:"version"`
	// Entries is a pointer so a document that declares no entries block
	// is distinguishable from one that declares an empty list, as the
	// name pass's registers are and for the same reason.
	Entries *[]Sense `yaml:"entries"`
}

// loadSenses parses and validates the sense register into the per-file,
// per-occurrence destinations the pass resolves a bare citation against.
//
// A malformed register is an error rather than an empty set. A register
// that loaded as carrying nothing would abort the run at the first bare
// citation in the tree, which reads as a register that has not been
// seeded, and no substitution would be made at a site a reviewer had
// already resolved.
func loadSenses(data []byte) (map[string]map[int]Sense, error) {
	var doc senseDocument
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse the anchor sense register %s: %w", senseRegisterPath, err)
	}
	if doc.Kind != senseRegisterKind {
		return nil, fmt.Errorf("anchor sense register %s: expected kind %q, got %q", senseRegisterPath, senseRegisterKind, doc.Kind)
	}
	if doc.Version != senseRegisterVersion {
		return nil, fmt.Errorf("anchor sense register %s: expected version %d, got %d", senseRegisterPath, senseRegisterVersion, doc.Version)
	}
	if doc.Entries == nil {
		return nil, fmt.Errorf("anchor sense register %s: carries no entries block", senseRegisterPath)
	}
	if len(*doc.Entries) == 0 {
		return nil, fmt.Errorf("anchor sense register %s: carries no entry", senseRegisterPath)
	}
	senses := map[string]map[int]Sense{}
	for i, entry := range *doc.Entries {
		if err := validateSense(i, entry); err != nil {
			return nil, err
		}
		if _, ok := senses[entry.File]; !ok {
			senses[entry.File] = map[int]Sense{}
		}
		if _, ok := senses[entry.File][entry.Occurrence]; ok {
			return nil, fmt.Errorf("anchor sense register %s: %s occurrence %d is declared twice",
				senseRegisterPath, entry.File, entry.Occurrence)
		}
		senses[entry.File][entry.Occurrence] = entry
	}
	return senses, nil
}

// validateSense reports the first schema defect in one entry.
func validateSense(i int, s Sense) error {
	where := fmt.Sprintf("anchor sense register %s: entry %d", senseRegisterPath, i)
	if strings.TrimSpace(s.File) == "" {
		return fmt.Errorf("%s carries no file", where)
	}
	if s.Occurrence < 1 {
		return fmt.Errorf("%s for %s carries occurrence %d, and occurrences are numbered from one", where, s.File, s.Occurrence)
	}
	where = fmt.Sprintf("anchor sense register %s: %s occurrence %d", senseRegisterPath, s.File, s.Occurrence)
	file, anchor, found := strings.Cut(s.Destination, "#")
	if !found || strings.TrimSpace(file) == "" || strings.TrimSpace(anchor) == "" {
		return fmt.Errorf("%s carries the destination %q, and a destination names a file and an anchor as <path>#<anchor>", where, s.Destination)
	}
	return nil
}

// sortedKeys returns a map's keys in a stable order, so a run reports
// the same failures in the same order.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// sortedOccurrences returns one file's occurrence numbers in order.
func sortedOccurrences(entries map[int]Sense) []int {
	out := make([]int, 0, len(entries))
	for occurrence := range entries {
		out = append(out, occurrence)
	}
	sort.Ints(out)
	return out
}
