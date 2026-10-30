// SPDX-License-Identifier: MIT

package identifier

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// noTableFixture is a tree whose communication-channels section carries
// the three class registers and no naming table. Its column sets are the
// ones the section states, so the case fails if a class register's
// columns ever drift into the set the loader recognizes the naming table
// by.
const noTableFixture = "testdata/noname"

// embeddedFixture is a tree whose naming table states two rows, one
// retiring a spelling the tree writes only inside a longer lowercase
// word and one retiring a spelling the tree writes on token boundaries.
const embeddedFixture = "testdata/embedded"

// embeddedSpelling is the retired spelling the embedded fixture writes
// only inside a longer lowercase word, so the site walk reads no
// occurrence of it.
const embeddedSpelling = "sample-token"

// boundedSpelling is the retired spelling the embedded fixture writes on
// token boundaries, so the site walk reads one occurrence of it.
const boundedSpelling = "stray-token"

// proseSiteFixture is a tree whose communication-channels section names
// a channel by its retired spelling in the prose of §28.2, outside the
// naming-table row that retires it.
const proseSiteFixture = "testdata/prosesite"

// proseSiteLine is the line of that fixture the prose occurrence stands
// on.
const proseSiteLine = 5

// TestLoadTableReadsTheLandedNamingTable_spec_28_3 reads the naming
// table out of the tracked tree and out of a tree that states no such
// table.
//
// The accept case checks the properties a row has to carry for the pass
// to be able to act on it: a carrier the pass knows, a substitution that
// changes the spelling, both spellings written in the carrier's token
// form, and at least one occurrence of one of the two spellings inside
// the pass's write domain outside the table that declares it.
//
// The reachability rule reads either direction. It formerly read the
// retired direction alone, and that rule is retired: it asserted that
// the rename had sites left to make, which is a state a completed rename
// ends. Once every site of a channel is written in the canonical
// spelling, a correctly authored row and a row naming a spelling nothing
// in the tree ever wrote both reach a retired count of zero, so the
// retired count separates them no longer. Two rows of the tracked table
// reached that state, and the seven that had not stood on specimen
// occurrences inside the migration tooling's own test files rather than
// on anything the pass could still rewrite, so the old rule was already
// measuring an accident. Do not restore it. Reading either direction
// keeps what it was for, which is that the row is about this tree: a row
// whose channel this tree writes in neither spelling names a channel
// that is not here.
//
// The mis-authored row the old rule was aimed at, the one "retiring a
// flag with its shell prefix attached", is now caught by the token-form
// rule rowFrom applies, which reads the cell rather than the tree and so
// holds however far the rename has run. That rule is pinned by
// TestARowStatesEachSpellingInItsCarriersTokenForm_spec_28_3.
//
// spec: §28.3
func TestLoadTableReadsTheLandedNamingTable_spec_28_3(t *testing.T) {
	t.Run("the tracked tree states the naming table", func(t *testing.T) {
		ctx := context.Background()
		root, err := scope.RepoRoot(ctx, ".")
		if err != nil {
			t.Fatalf("locate the repository root: %v", err)
		}
		list, read := scope.GitLister(root), scope.DirReader(root)
		table, err := LoadTable(ctx, list, read)
		if err != nil {
			t.Fatalf("read the naming table out of the tracked tree: %v", err)
		}
		if len(table.rows) == 0 {
			t.Fatal("the naming table states no row, so the pass resolves no site")
		}
		for _, row := range table.rows {
			if !row.Carrier.Valid() {
				t.Errorf("the row for %s states the carrier %q, which is none of %s",
					row.Channel, row.Carrier, carrierNames())
			}
			if row.Retired == row.Canonical {
				t.Errorf("the row for %s retires %q to itself", row.Channel, row.Retired)
			}
		}
		retired, err := retiredSpellingsInWriteDomain(ctx, list, read, table)
		if err != nil {
			t.Fatalf("count the retired spellings of the write domain: %v", err)
		}
		canonical, err := canonicalSpellingsInWriteDomain(ctx, list, read, table)
		if err != nil {
			t.Fatalf("count the canonical spellings of the write domain: %v", err)
		}
		for _, row := range table.rows {
			if retired[row.Retired]+canonical[row.Canonical] == 0 {
				t.Errorf("the %s row for %s retires %q to %q, and no writable tracked file outside %s* carries a site of either spelling, so the row names a channel this tree does not write",
					row.Carrier, row.Channel, row.Retired, row.Canonical, channelSectionPrefix)
			}
		}
	})

	t.Run("a section stating no naming table names the columns it looked for", func(t *testing.T) {
		ctx := context.Background()
		table, err := LoadTable(ctx, scope.DirLister(noTableFixture), scope.DirReader(noTableFixture))
		if err == nil {
			t.Fatalf("a section carrying no naming table returned %d row(s) instead of an error", len(table.rows))
		}
		for _, column := range tableColumns {
			if !strings.Contains(err.Error(), column) {
				t.Errorf("the failure %q does not name the column %q the loader looked for", err, column)
			}
		}
	})
}

// namingColumns is the column index of each naming-table column in a
// row given as cells in the order the section writes them, which is what
// rowFrom reads a raw row through.
var namingColumns = map[string]int{
	"channel": 0, "carrier": 1, "retired spelling": 2, "canonical spelling": 3,
}

// TestARowStatesEachSpellingInItsCarriersTokenForm_spec_28_3 pins the
// rule that carries the mis-authored-row intent once a rename has run to
// completion.
//
// A row whose spelling no carrier could write resolves nothing: the site
// walk reads tokens, so a flag cell carrying the dashes the shell writes
// the flag with matches no site, and a substitution spliced from such a
// cell would write a token the carrier cannot hold. The defect is in the
// cell, so the rule reads the cell. That is what makes it hold after the
// pass has rewritten every site: a count taken from the tree reads a
// spent row and a mis-authored row alike, and this rule reads neither
// from the tree.
//
// The rule sits in rowFrom rather than here, so a mis-authored row fails
// every run of the pass at load rather than only this case.
//
// The cells are specimen spellings rather than the tracked table's own,
// for the reason the fixture trees state: a retired spelling written in
// a Go source file of this package is a site the identifier-resolution
// gate reads, so a case restating the tracked spellings would report
// this package's own input as a second live spelling of a channel.
//
// spec: §28.3
func TestARowStatesEachSpellingInItsCarriersTokenForm_spec_28_3(t *testing.T) {
	const channel = "CH-SAMPLE"
	for _, tc := range []struct {
		name     string
		carrier  string
		retired  string
		writes   string
		accepted bool
	}{
		{
			name:     "a flag row states the flag without the shell's dashes",
			carrier:  "flag",
			retired:  "sample-token",
			writes:   "specimen-token",
			accepted: true,
		},
		{
			name:    "a flag row retiring a flag with its shell prefix attached",
			carrier: "flag",
			retired: "--sample-token",
			writes:  "specimen-token",
		},
		{
			name:    "a flag row substituting a flag with its shell prefix attached",
			carrier: "flag",
			retired: "sample-token",
			writes:  "--specimen-token",
		},
		{
			name:     "a socket row states the abstract-namespace token",
			carrier:  "socket",
			retired:  "@lenny-sample",
			writes:   "@lenny-specimen",
			accepted: true,
		},
		{
			name:    "a socket row stating words no socket token carries",
			carrier: "socket",
			retired: "@lenny-sample",
			writes:  "Lenny Specimen Token",
		},
		{
			name:     "a go-symbol row states an exported stem",
			carrier:  "go-symbol",
			retired:  "SampleToken",
			writes:   "SpecimenToken",
			accepted: true,
		},
		{
			name:    "a go-symbol row stating a hyphenated cell no Go identifier carries",
			carrier: "go-symbol",
			retired: "SampleToken",
			writes:  "specimen-token",
		},
		{
			name:     "a path row states a lowercase file-name stem",
			carrier:  "path",
			retired:  "sample-token",
			writes:   "specimen-token",
			accepted: true,
		},
		{
			name:    "a path row stating a stem with the extension attached",
			carrier: "path",
			retired: "sample-token",
			writes:  "specimen-token.go",
		},
		{
			name:     "a manifest-key row states a key opening lowercase",
			carrier:  "manifest-key",
			retired:  "sampleToken",
			writes:   "specimenToken",
			accepted: true,
		},
		{
			name:    "a manifest-key row stating a key with its parent path attached",
			carrier: "manifest-key",
			retired: "sampleToken",
			writes:  "channels.specimenToken",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []string{channel, tc.carrier, tc.retired, tc.writes}
			row, err := rowFrom(raw, namingColumns)
			if tc.accepted {
				if err != nil {
					t.Fatalf("the row %v was refused: %v", raw, err)
				}
				if row.Retired != tc.retired || row.Canonical != tc.writes {
					t.Errorf("the row read as %q to %q, want %q to %q", row.Retired, row.Canonical, tc.retired, tc.writes)
				}
				return
			}
			if err == nil {
				t.Fatalf("the row %v was read as a row the pass can act on", raw)
			}
			// rowFrom returns the zero row on a refusal, so the cell the
			// refusal has to name is read off the case's own carrier
			// rather than off the row.
			for _, cell := range []string{tc.retired, tc.writes} {
				if !Carrier(tc.carrier).wellFormed(cell) && !strings.Contains(err.Error(), cell) {
					t.Errorf("the refusal %q does not name the cell %q it refused", err, cell)
				}
			}
		})
	}
}

// TestRetiredSpellingReachabilityIsTheSiteWalk_spec_28_3 pins the
// reachability count to the pass's own site walk.
//
// A retired spelling standing only inside a longer lowercase word is no
// site, so the pass reaches none of its occurrences and the row resolves
// nothing. A count taken by substring reads such a spelling as reachable
// and passes the row, which is the class of unresolvable row the
// reachability check exists to name.
//
// The reachability check reads both spellings of a row, and both counts
// come from this walk, so pinning the retired direction to it pins the
// terms the canonical direction is counted on as well.
//
// spec: §28.3
func TestRetiredSpellingReachabilityIsTheSiteWalk_spec_28_3(t *testing.T) {
	ctx := context.Background()
	list, read := scope.DirLister(embeddedFixture), scope.DirReader(embeddedFixture)
	table, err := LoadTable(ctx, list, read)
	if err != nil {
		t.Fatalf("read the naming table out of the specimen tree: %v", err)
	}
	counts, err := retiredSpellingsInWriteDomain(ctx, list, read, table)
	if err != nil {
		t.Fatalf("count the retired spellings of the write domain: %v", err)
	}
	if counts[embeddedSpelling] != 0 {
		t.Errorf("%q stands only inside a longer lowercase word and was counted %d time(s), so a row the pass resolves no site for reads as reachable",
			embeddedSpelling, counts[embeddedSpelling])
	}
	if counts[boundedSpelling] != 1 {
		t.Errorf("%q stands on token boundaries once and was counted %d time(s)", boundedSpelling, counts[boundedSpelling])
	}
}

// sitesOutsideNamingRows returns the 1-based line of every retired
// spelling one specification file carries outside the naming-table rows
// that declare the spellings, in source order.
//
// It asks of the file the table is stated in the question the pass asks
// of every other file: which occurrences are sites. A site there is an
// occurrence with no entry in the sense register the pass reads, and the
// register is keyed by file and by the position of the site within it,
// so one introduced by a later edit aborts the pass before any write.
// The walk and the exemption are the pass's own, read here through the
// package's own predicates, so the case restates neither the spellings
// nor the exemption. It stays with the case rather than on Table: the
// pass resolves sites through findSites directly, so an exported form
// would be a surface of the tooling that no pass and no gate enters.
func sitesOutsideNamingRows(t *Table, target, content string) []int {
	var lines []int
	for _, s := range findSites(content, t.Retired()) {
		if t.mentioned(target, s.start) {
			continue
		}
		lines = append(lines, s.line)
	}
	return lines
}

// TestRetiredSpellingsStandOnlyInTheRowsThatRetireThem_spec_28_3 pins
// the one exemption the pass grants inside the communication-channels
// section: a retired spelling standing in the row that retires it is the
// declaration of that spelling, and an occurrence anywhere else in the
// section is a site.
//
// The accept case reads the tracked tree, where every occurrence stands
// in a row. The reject case reads a section whose prose names a channel
// by its retired spelling, which is the state that leaves the pass with
// a site its sense register carries no entry for and aborts the run
// before any write.
//
// spec: §28.3
func TestRetiredSpellingsStandOnlyInTheRowsThatRetireThem_spec_28_3(t *testing.T) {
	const channelSection = "spec/28_communication-channels.md"

	t.Run("the tracked section carries every spelling in a row", func(t *testing.T) {
		ctx := context.Background()
		root, err := scope.RepoRoot(ctx, ".")
		if err != nil {
			t.Fatalf("locate the repository root: %v", err)
		}
		read := scope.DirReader(root)
		table, err := LoadTable(ctx, scope.GitLister(root), read)
		if err != nil {
			t.Fatalf("read the naming table out of the tracked tree: %v", err)
		}
		content, err := read(channelSection)
		if err != nil {
			t.Fatalf("read %s: %v", channelSection, err)
		}
		for _, line := range sitesOutsideNamingRows(table, channelSection, string(content)) {
			t.Errorf("%s:%d carries a retired spelling outside the row that retires it, which is a site with no sense-register entry",
				channelSection, line)
		}
	})

	t.Run("a spelling in the section's prose is a site", func(t *testing.T) {
		ctx := context.Background()
		read := scope.DirReader(proseSiteFixture)
		table, err := LoadTable(ctx, scope.DirLister(proseSiteFixture), read)
		if err != nil {
			t.Fatalf("read the naming table out of the specimen tree: %v", err)
		}
		content, err := read(channelSection)
		if err != nil {
			t.Fatalf("read the specimen section: %v", err)
		}
		got := sitesOutsideNamingRows(table, channelSection, string(content))
		if len(got) != 1 || got[0] != proseSiteLine {
			t.Errorf("sites outside a naming-table row = %v, want [%d], the prose line naming the channel by its retired spelling",
				got, proseSiteLine)
		}
	})
}

// spellingsInWriteDomain counts, per spelling of the given set, the
// sites the pass's own walk reads outside the specification file the
// table is stated in. Counting is by that walk rather than by substring,
// so the count is the number of occurrences the pass would act on: a
// spelling standing only inside a longer lowercase word is no site, and
// a check reading it as one would pass a row the substitution never
// reaches.
//
// The naming-table rows are excluded because every spelling of a row
// stands in that row by construction, so counting the row would satisfy
// a reachability check for a spelling nothing else in the tree writes.
// The exclusion is the row rather than the whole specification file: an
// occurrence standing in the section's prose is a site the pass acts on
// like any other, and a file-wide skip counts it in neither direction.
//
// The spellings are a parameter because a row is reachable in either
// direction. Before the pass runs, the tree writes the retired spelling;
// after it, the canonical one. Both counts come from the same walk so
// the two directions are measured on the same terms.
func spellingsInWriteDomain(ctx context.Context, list scope.Lister, read scope.FileReader, table *Table, spellings []string) (map[string]int, error) {
	domain, err := scope.WriteDomain(ctx, list, scope.Identifier, read)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, target := range domain {
		data, err := read(target)
		if err != nil {
			return nil, err
		}
		for _, s := range findSites(string(data), spellings) {
			if table.mentioned(target, s.start) {
				continue
			}
			counts[s.retired]++
		}
	}
	return counts, nil
}

// retiredSpellingsInWriteDomain counts the sites the pass would rewrite,
// which is the retired direction of spellingsInWriteDomain.
func retiredSpellingsInWriteDomain(ctx context.Context, list scope.Lister, read scope.FileReader, table *Table) (map[string]int, error) {
	return spellingsInWriteDomain(ctx, list, read, table, table.Retired())
}

// canonicalSpellingsInWriteDomain counts the occurrences of the spelling
// a row substitutes in, which is the direction the tree writes once the
// pass has run over it.
func canonicalSpellingsInWriteDomain(ctx context.Context, list scope.Lister, read scope.FileReader, table *Table) (map[string]int, error) {
	return spellingsInWriteDomain(ctx, list, read, table, table.Canonical())
}
