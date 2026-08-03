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

// retiredSpellingsInWriteDomain counts, per retired spelling of the
// table, the writable tracked files that carry it outside the
// specification file the table is stated in. The declaring row is
// excluded because every retired spelling stands in it by construction,
// so counting it would satisfy the reachability check for a spelling
// nothing else in the tree writes.
func retiredSpellingsInWriteDomain(ctx context.Context, list scope.Lister, read scope.FileReader, table *Table) (map[string]int, error) {
	domain, err := scope.WriteDomain(ctx, list, scope.Identifier, read)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, target := range domain {
		if strings.HasPrefix(target, channelSectionPrefix) {
			continue
		}
		data, err := read(target)
		if err != nil {
			return nil, err
		}
		content := string(data)
		for _, spelling := range table.Retired() {
			if strings.Contains(content, spelling) {
				counts[spelling]++
			}
		}
	}
	return counts, nil
}
