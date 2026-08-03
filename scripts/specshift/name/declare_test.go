// SPDX-License-Identifier: MIT

package name

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// proseFixture is a tree whose communication-channels section carries
// the naming law alone. It states no register table, so the section
// declares no identifier and the index reads it as empty.
const proseFixture = "testdata/prose"

// declaredClassPrefixes are the class prefixes the taxonomy states. The
// landed registers declare at least one identifier under each, so an
// index missing one of them read a register the section states.
var declaredClassPrefixes = []string{"LNK-", "CH-", "REG-"}

// TestDeclaredIdentifiersIndexesTheLandedSection_spec_28_3 indexes the
// identifier space out of the tracked tree and out of a tree whose
// communication-channels section states prose alone.
//
// The accept case asserts that the index carries an identifier of each
// class the taxonomy states, which is the property that fails when the
// index reads one register table and misses another: an entry naming a
// link or a register entry resolves against the same index the channel
// entries resolve against, and an index carrying the channel class alone
// reports every other entry as a misspelling.
//
// The reject case is the empty-declaration boundary. A section that
// exists and declares nothing is a tree the pass cannot run against yet,
// and reporting an empty space instead would fail every entry of the
// register.
//
// spec: §28.1, §28.3
func TestDeclaredIdentifiersIndexesTheLandedSection_spec_28_3(t *testing.T) {
	t.Run("the tracked tree declares an identifier of each class", func(t *testing.T) {
		ctx := context.Background()
		root, err := scope.RepoRoot(ctx, ".")
		if err != nil {
			t.Fatalf("locate the repository root: %v", err)
		}
		declared, err := declaredIdentifiers(ctx, scope.GitLister(root), scope.DirReader(root))
		if err != nil {
			t.Fatalf("index the declared identifiers of the tracked tree: %v", err)
		}
		for _, prefix := range declaredClassPrefixes {
			if countWithPrefix(declared, prefix) == 0 {
				t.Errorf("the index carries no %s identifier, so every register entry naming one resolves against nothing", prefix)
			}
		}
	})

	t.Run("a section stating prose alone names the file count and the section prefix", func(t *testing.T) {
		ctx := context.Background()
		declared, err := declaredIdentifiers(ctx, scope.DirLister(proseFixture), scope.DirReader(proseFixture))
		if err == nil {
			t.Fatalf("a section declaring no identifier returned %d identifier(s) instead of an error", len(declared))
		}
		for _, part := range []string{"1 file(s)", channelSectionPrefix} {
			if !strings.Contains(err.Error(), part) {
				t.Errorf("the failure %q does not name %q, so it does not say which files were read", err, part)
			}
		}
	})
}

// countWithPrefix counts the identifiers of the index that stand under
// one class prefix.
func countWithPrefix(declared map[string]bool, prefix string) int {
	count := 0
	for id := range declared {
		if strings.HasPrefix(id, prefix) {
			count++
		}
	}
	return count
}
