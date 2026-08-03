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

// TestLoadTableReadsTheLandedNamingTable_spec_28_3 reads the naming
// table out of the tracked tree and out of a tree that states no such
// table.
//
// The accept case checks the properties a row has to carry for the pass
// to be able to act on it: a carrier the pass knows, a substitution that
// changes the spelling, and at least one occurrence of the retired
// spelling inside the pass's write domain outside the table that
// declares it. Without the last one a row states a substitution for a
// spelling the site walk never reaches, which is how a row retiring a
// flag with its shell prefix attached passes every other check and
// resolves nothing.
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
		reachable, err := retiredSpellingsInWriteDomain(ctx, list, read, table)
		if err != nil {
			t.Fatalf("count the retired spellings of the write domain: %v", err)
		}
		for _, row := range table.rows {
			if reachable[row.Retired] == 0 {
				t.Errorf("the %s row for %s retires %q, which occurs in no writable tracked file outside %s*, so no site carries it",
					row.Carrier, row.Channel, row.Retired, channelSectionPrefix)
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

// TestRetiredSpellingReachabilityIsTheSiteWalk_spec_28_3 pins the
// reachability count to the pass's own site walk.
//
// A retired spelling standing only inside a longer lowercase word is no
// site, so the pass reaches none of its occurrences and the row resolves
// nothing. A count taken by substring reads such a spelling as reachable
// and passes the row, which is the class of unresolvable row the
// reachability check exists to name.
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

// retiredSpellingsInWriteDomain counts, per retired spelling of the
// table, the sites the pass reads outside the specification file the
// table is stated in. Counting is by the pass's own site walk rather
// than by substring, so the count is the number of occurrences the pass
// would act on: a spelling standing only inside a longer lowercase word
// is no site, and a check reading it as one would pass a row the
// substitution never reaches.
//
// The specification file the table is stated in is excluded because
// every retired spelling stands in the row that retires it by
// construction, so counting that row would satisfy the reachability
// check for a spelling nothing else in the tree writes.
func retiredSpellingsInWriteDomain(ctx context.Context, list scope.Lister, read scope.FileReader, table *Table) (map[string]int, error) {
	domain, err := scope.WriteDomain(ctx, list, scope.Identifier, read)
	if err != nil {
		return nil, err
	}
	retired := table.Retired()
	counts := map[string]int{}
	for _, target := range domain {
		if strings.HasPrefix(target, channelSectionPrefix) {
			continue
		}
		data, err := read(target)
		if err != nil {
			return nil, err
		}
		for _, s := range findSites(string(data), retired) {
			counts[s.retired]++
		}
	}
	return counts, nil
}
