// SPDX-License-Identifier: MIT

package identifier

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// The declaration the sense register carries. It is a driving register
// rather than a residual one (tests/registers/README.md): it is keyed by
// file and occurrence for the rewrite it drives, and it is emptied by
// the change that runs the pass over the whole domain rather than
// carrying an owner and an expiry.
const (
	registerKind    = "identifier-senses"
	registerVersion = 1
)

// Entry is one site's sense: which channel the occurrence of a retired
// spelling denotes, or that the occurrence is not a channel at all.
//
// The register carries an entry per occurrence rather than per channel,
// because a retired spelling is two-valued in both directions. One
// spelling denotes two channels, so a channel-keyed register cannot say
// which of them an occurrence means. A spelling the table maps to one
// channel still occurs where the text is not that channel, such as a
// command-line file argument that happens to carry the socket token, so
// a channel-count trigger substitutes there blind.
type Entry struct {
	// File is the repo-relative path of the carrier.
	File string `yaml:"file"`
	// Occurrence is the 1-based position of the site among the
	// retired-spelling sites the file's content carries, in source
	// order. It is unset on an entry that resolves the file's own path.
	Occurrence int `yaml:"occurrence"`
	// Path marks the entry as resolving the retired spelling in the
	// file's name rather than an occurrence in its content. The naming
	// law reaches the file-name stem, so the run that rewrites the
	// identifier inside a carrier is the run that moves the carrier
	// whose own name carries it.
	Path bool `yaml:"path"`
	// Channel is the canonical identifier the occurrence denotes.
	Channel string `yaml:"channel"`
	// NotAChannel records that the occurrence is not a channel
	// reference, so the pass leaves it as it stands. It is the register's
	// second disposition, and it exists so a permanently correct
	// non-channel occurrence is recorded where the pass reads it rather
	// than routed through an exception register whose owner and expiry it
	// could never retire against.
	NotAChannel bool `yaml:"not-a-channel"`
	// Carrier names the naming-table row the substitution is read from,
	// for an occurrence whose channel states a spelling in more than one
	// carrier and whose form does not select one. It is empty otherwise.
	Carrier Carrier `yaml:"carrier"`
}

// key returns the entry's position within its file: the occurrence
// number, or zero for the entry that resolves the file's own path.
func (e Entry) key() int {
	if e.Path {
		return 0
	}
	return e.Occurrence
}

// where renders the entry's position for a message.
func (e Entry) where() string {
	if e.Path {
		return fmt.Sprintf("%s file name", e.File)
	}
	return fmt.Sprintf("%s occurrence %d", e.File, e.Occurrence)
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
// per-position senses the pass is driven by.
//
// A missing, unreadable, or malformed register is an error rather than
// an empty set. A load that degraded to carrying nothing would abort the
// run at the first site in the tree, which reads as a register that has
// not been seeded, and a run over a tree the pass had already rewritten
// would report a completed migration it never performed.
func loadSenses(path string) (map[string]map[int]Entry, error) {
	data, err := os.ReadFile(filepath.FromSlash(path))
	if err != nil {
		return nil, fmt.Errorf("read the identifier sense register %s: %w", path, err)
	}
	var doc senseDocument
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse the identifier sense register %s: %w", path, err)
	}
	if doc.Kind != registerKind {
		return nil, fmt.Errorf("identifier sense register %s: expected kind %q, got %q", path, registerKind, doc.Kind)
	}
	if doc.Version != registerVersion {
		return nil, fmt.Errorf("identifier sense register %s: expected version %d, got %d", path, registerVersion, doc.Version)
	}
	if doc.Entries == nil {
		return nil, fmt.Errorf("identifier sense register %s: carries no entries block", path)
	}
	if len(*doc.Entries) == 0 {
		return nil, fmt.Errorf("identifier sense register %s: carries no entry", path)
	}
	senses := map[string]map[int]Entry{}
	for i, entry := range *doc.Entries {
		if err := validateEntry(path, i, entry); err != nil {
			return nil, err
		}
		if _, ok := senses[entry.File]; !ok {
			senses[entry.File] = map[int]Entry{}
		}
		if _, ok := senses[entry.File][entry.key()]; ok {
			return nil, fmt.Errorf("identifier sense register %s: %s is declared twice", path, entry.where())
		}
		senses[entry.File][entry.key()] = entry
	}
	return senses, nil
}

// validateEntry reports the first schema defect in one entry.
//
// An entry states one position and one disposition. An entry stating
// neither position resolves nothing, an entry stating both is two
// entries, and an entry stating both dispositions says at once that the
// occurrence is a channel and that it is not, which the pass would have
// to break by a rule the reviewer never wrote.
func validateEntry(path string, i int, e Entry) error {
	where := fmt.Sprintf("identifier sense register %s: entry %d", path, i)
	if strings.TrimSpace(e.File) == "" {
		return fmt.Errorf("%s carries no file", where)
	}
	if e.Occurrence < 0 {
		return fmt.Errorf("%s for %s carries occurrence %d, and occurrences are numbered from one",
			where, e.File, e.Occurrence)
	}
	if e.Path == (e.Occurrence > 0) {
		return fmt.Errorf("%s for %s states %s, and an entry resolves either one occurrence of the content or the file name",
			where, e.File, position(e))
	}
	where = fmt.Sprintf("identifier sense register %s: %s", path, e.where())
	if e.NotAChannel == (e.Channel != "") {
		return fmt.Errorf("%s states %s, and an entry records either the channel the site denotes or that the site is not a channel",
			where, disposition(e))
	}
	if e.Channel != "" && !identifierExpr.MatchString(e.Channel) {
		return fmt.Errorf("%s names the channel %q, which is not a canonical identifier", where, e.Channel)
	}
	if e.Carrier != "" && !e.Carrier.Valid() {
		return fmt.Errorf("%s names the carrier %q, which is none of %s", where, e.Carrier, carrierNames())
	}
	if e.Carrier != "" && e.NotAChannel {
		return fmt.Errorf("%s names the carrier %q and records the site as no channel, so the carrier selects nothing", where, e.Carrier)
	}
	return nil
}

// position renders what an entry said about its position, for the
// message that refuses it.
func position(e Entry) string {
	if e.Path {
		return fmt.Sprintf("both the file name and occurrence %d", e.Occurrence)
	}
	return "neither an occurrence nor the file name"
}

// disposition renders what an entry said about its sense, for the
// message that refuses it.
func disposition(e Entry) string {
	if e.NotAChannel {
		return fmt.Sprintf("the channel %s and that the site is not a channel", e.Channel)
	}
	return "no channel and no record that the site is not a channel"
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

// sortedPositions returns one file's entry positions in order.
func sortedPositions(entries map[int]Entry) []int {
	out := make([]int, 0, len(entries))
	for at := range entries {
		out = append(out, at)
	}
	sort.Ints(out)
	return out
}
