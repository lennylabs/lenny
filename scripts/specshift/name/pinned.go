// SPDX-License-Identifier: MIT

package name

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// The pinned-literal register and the carrier population it covers.
//
// The tier-11 documentation reconciliation tests pin specification
// prose, specification heading slugs, and intra-spec markdown links as
// Go string literals, and a run that rewrote such a sentence in the
// specification while leaving the literal that pins it verbatim would
// leave tier 11 red with a reader on the sentence and no pass able to
// write the literal. Those literals are therefore positions of this
// pass, driven by their own register, which names every one of them.
//
// The register names the literals rather than the pass walking every
// string of the file, because a walk would also admit the
// operator-facing help text a tier-11 carrier happens to hold, and the
// reviewer resolves which literal pins the specification before the
// substitution runs.
const (
	pinnedRegisterPath    = "tests/registers/pinned-spec-literals.yaml"
	pinnedRegisterKind    = "pinned-spec-literals"
	pinnedRegisterVersion = 1
	pinnedCarrierPrefix   = "tests/tier11_docs/"
)

// pinnedEntry is one pinned literal: the carrier and the literal's
// 1-based position among the string literals that file carries, in
// source order. The position is the key rather than the text, because
// the pass rewrites the text and a key made of it would stop resolving
// at the moment the substitution landed.
type pinnedEntry struct {
	File    string `yaml:"file"`
	Literal int    `yaml:"literal"`
}

// pinnedDocument is the pinned-literal register as it is written.
type pinnedDocument struct {
	Kind    string `yaml:"kind"`
	Version int    `yaml:"version"`
	// Entries is a pointer so a document that declares no entries block
	// is distinguishable from one that declares an empty list, as the
	// sense register's is and for the same reason.
	Entries *[]pinnedEntry `yaml:"entries"`
}

// pinnedCarrier reports whether a tracked path is a Go carrier of the
// tier-11 documentation reconciliation tests, which is the population
// the pinned-literal register covers.
func pinnedCarrier(target string) bool {
	return strings.HasPrefix(target, pinnedCarrierPrefix) && filepath.Ext(target) == ".go"
}

// loadPinnedLiterals parses and validates the pinned-literal register
// into the per-file, per-literal positions the matcher admits.
//
// A malformed register is an error rather than an empty set, as the
// sense register is: a register that loaded as carrying nothing would
// leave every pinned literal unwritten while the run exited zero.
func loadPinnedLiterals(data []byte) (map[string]map[int]bool, error) {
	var doc pinnedDocument
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse the pinned-literal register %s: %w", pinnedRegisterPath, err)
	}
	if doc.Kind != pinnedRegisterKind {
		return nil, fmt.Errorf("pinned-literal register %s: expected kind %q, got %q", pinnedRegisterPath, pinnedRegisterKind, doc.Kind)
	}
	if doc.Version != pinnedRegisterVersion {
		return nil, fmt.Errorf("pinned-literal register %s: expected version %d, got %d", pinnedRegisterPath, pinnedRegisterVersion, doc.Version)
	}
	if doc.Entries == nil {
		return nil, fmt.Errorf("pinned-literal register %s: carries no entries block", pinnedRegisterPath)
	}
	if len(*doc.Entries) == 0 {
		return nil, fmt.Errorf("pinned-literal register %s: carries no entry", pinnedRegisterPath)
	}
	pinned := map[string]map[int]bool{}
	for i, entry := range *doc.Entries {
		if err := validatePinnedEntry(i, entry); err != nil {
			return nil, err
		}
		if _, ok := pinned[entry.File]; !ok {
			pinned[entry.File] = map[int]bool{}
		}
		if pinned[entry.File][entry.Literal] {
			return nil, fmt.Errorf("pinned-literal register %s: %s literal %d is declared twice",
				pinnedRegisterPath, entry.File, entry.Literal)
		}
		pinned[entry.File][entry.Literal] = true
	}
	return pinned, nil
}

// validatePinnedEntry reports the first schema defect in one entry.
//
// An entry keyed to a carrier outside the tier-11 reconciliation tests
// is refused rather than admitted, because a string literal anywhere
// else holds operator-facing or internal text the naming law does not
// govern, and admitting one would rewrite it under the same register.
func validatePinnedEntry(i int, e pinnedEntry) error {
	where := fmt.Sprintf("pinned-literal register %s: entry %d", pinnedRegisterPath, i)
	if strings.TrimSpace(e.File) == "" {
		return fmt.Errorf("%s carries no file", where)
	}
	if !pinnedCarrier(e.File) {
		return fmt.Errorf("%s names %s, which is not a Go carrier under %s", where, e.File, pinnedCarrierPrefix)
	}
	if e.Literal < 1 {
		return fmt.Errorf("%s for %s carries literal %d, and literals are numbered from one", where, e.File, e.Literal)
	}
	return nil
}

// loadPinned reads the pinned-literal register out of the tree the pass
// runs over and holds every entry to a literal that tree carries. It
// runs once per run, from the register check.
//
// The register is required whenever the tree carries a Go carrier under
// the tier-11 reconciliation tests, and absent otherwise. A run over
// such a tree with no register would leave every pinned literal
// unwritten, which is the writerless site the shared domain exists to
// prevent, so the absence fails the run rather than narrowing the pass
// in silence.
func (r *Rewriter) loadPinned(tracked map[string]bool) error {
	r.pinned = map[string]map[int]bool{}
	carriers := pinnedCarriers(tracked)
	if len(carriers) == 0 {
		return nil
	}
	if r.read == nil {
		return fmt.Errorf("read the pinned-literal register %s: a reader is required", pinnedRegisterPath)
	}
	data, err := r.read(pinnedRegisterPath)
	if err != nil {
		return fmt.Errorf("read the pinned-literal register %s, which resolves the string literals the %d tier-11 reconciliation carrier(s) of this tree pin specification prose, heading slugs, and intra-spec links in: %w",
			pinnedRegisterPath, len(carriers), err)
	}
	pinned, err := loadPinnedLiterals(data)
	if err != nil {
		return err
	}
	if err := r.checkPinnedClaimed(pinned, tracked); err != nil {
		return err
	}
	r.pinned = pinned
	return nil
}

// checkPinnedClaimed reports every pinned-literal entry no literal in
// the tree claims, which is an entry keyed to a file the tree does not
// carry and an entry whose literal position is above the count of string
// literals its file holds. Either way the entry names no position, so
// the site it was seeded for would be left unwritten by a run that
// exited zero.
//
// An entry whose carrier the run's confinement does not cover is left
// to the complementary run, as an entry of the sense register is. Both
// abort conditions stay live inside the confinement: neither is
// produced by a prior rewrite, so a run that skipped every pinned entry
// would drop the one compensating check this register has.
func (r *Rewriter) checkPinnedClaimed(pinned map[string]map[int]bool, tracked map[string]bool) error {
	var unclaimed []string
	for _, target := range sortedPinnedFiles(pinned) {
		if !r.confine.Covers(target) {
			continue
		}
		if !tracked[target] {
			unclaimed = append(unclaimed, fmt.Sprintf("%s (the tree carries no such file)", target))
			continue
		}
		content, err := r.read(target)
		if err != nil {
			return fmt.Errorf("read %s for the pinned-literal register check: %w", target, err)
		}
		count, err := goStringLiteralCount(target, string(content))
		if err != nil {
			return err
		}
		for _, literal := range sortedPinnedLiterals(pinned[target]) {
			if literal > count {
				unclaimed = append(unclaimed, fmt.Sprintf("%s literal %d (the file carries %d string literal(s))", target, literal, count))
			}
		}
	}
	if len(unclaimed) > 0 {
		return fmt.Errorf("%s carries %d entr(ies) no literal in the tree claims, so the run would leave a pinned literal unwritten: %s",
			pinnedRegisterPath, len(unclaimed), strings.Join(unclaimed, "; "))
	}
	return nil
}

// pinnedCarriers returns the tier-11 reconciliation Go carriers of the
// tree, sorted.
func pinnedCarriers(tracked map[string]bool) []string {
	var out []string
	for target := range tracked {
		if pinnedCarrier(target) {
			out = append(out, target)
		}
	}
	sort.Strings(out)
	return out
}

// sortedPinnedFiles returns the register's file keys in a stable order.
func sortedPinnedFiles(pinned map[string]map[int]bool) []string {
	out := make([]string, 0, len(pinned))
	for target := range pinned {
		out = append(out, target)
	}
	sort.Strings(out)
	return out
}

// sortedPinnedLiterals returns one file's literal positions in order.
func sortedPinnedLiterals(literals map[int]bool) []int {
	out := make([]int, 0, len(literals))
	for literal := range literals {
		out = append(out, literal)
	}
	sort.Ints(out)
	return out
}
