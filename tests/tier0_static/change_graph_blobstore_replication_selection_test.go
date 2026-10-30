// SPDX-License-Identifier: MIT

package tier0_static

import "testing"

// spec: TESTING.md §5 "Every package under `pkg/` appears in the change
// graph (or under an explicit `pkg/**` glob)."
// diagnosis: pkg/blobstore/replication implements the §25.11 continuous
//
//	ArtifactStore replication pipeline (replication.go, lag.go, and the
//	Postgres-backed pgstore subpackage) and owns an in-package unit suite
//	(replication_test.go, lag_test.go, lag_internal_test.go, and
//	pgstore/pgstore_test.go). tests/change-graph.json's globs map has no
//	entry matching "blobstore" at all, so a change under
//	pkg/blobstore/replication resolves to an empty tier set (static only)
//	and `lenny-test --changed`/`--since` never re-selects this unit suite.
//	Add a "pkg/blobstore/replication" glob entry mapping to at least the
//	unit tier.
func TestChangeGraphBlobstoreReplicationPackageSelectsUnitTier(t *testing.T) {
	t.Parallel()

	tiers := resolveChangeGraphTiers(t, "pkg/blobstore/replication/replication.go")

	if len(tiers) == 0 {
		t.Fatal(`a change to pkg/blobstore/replication resolved to an empty tier set (static only); it owns replication_test.go, lag_test.go, lag_internal_test.go, and pgstore/pgstore_test.go, so tests/change-graph.json must map "pkg/blobstore/replication" to at least the unit tier`)
	}
	if !tiers["unit"] {
		t.Errorf("a change to pkg/blobstore/replication resolved to tiers %v; it owns an in-package unit suite, so the resolution must include %q",
			sortedKeys(tiers), "unit")
	}
}

// spec: TESTING.md §5 "Every package under `pkg/` appears in the change
// graph (or under an explicit `pkg/**` glob)."
// diagnosis: pkg/blobstore/replication/pgstore is a subpackage (the
//
//	§25.11 Postgres-backed ops_artifact_replication_state store) with its
//	own unit suite (pgstore_test.go) but no dedicated change-graph glob.
//	A "pkg/blobstore/replication" glob whose unit tier lists only
//	"pkg/blobstore/replication/replication_test.go" or similarly narrow
//	globs would leave a change under the pgstore subpackage unselected.
//	The unit-tier glob must cover the whole package tree (for example
//	"pkg/blobstore/replication/...") so a change to the subpackage is
//	also selected.
func TestChangeGraphBlobstoreReplicationPackageSelectsUnitTierForPgstoreSubpackage(t *testing.T) {
	t.Parallel()

	tiers := resolveChangeGraphTiers(t, "pkg/blobstore/replication/pgstore/pgstore.go")

	if !tiers["unit"] {
		t.Errorf("a change to pkg/blobstore/replication/pgstore resolved to tiers %v; the pkg/blobstore/replication change-graph entry's unit tier must cover the pgstore subpackage (for example via a \"pkg/blobstore/replication/...\" glob), so the resolution must include %q",
			sortedKeys(tiers), "unit")
	}
}
