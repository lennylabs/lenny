// SPDX-License-Identifier: MIT

// Tier-11 consistency check between the §12.4 Redis Cluster migration
// pre-plan, the §17.8.2 Tier 3 Redis rows, and the operator-facing comments on
// the chart's Redis topology values.
//
// The §12.4 pre-plan moves the whole deployment from Sentinel to Redis Cluster
// in one `redis.provider` cutover, and §17.8.2 runs every concern on one Redis
// Cluster at Tier 3. A values.yaml comment that calls Redis Cluster the
// migration topology of the Quota/Rate Limiting instance alone, or that tells
// an operator a Sentinel topology is sufficient at Tier 3, sends the operator
// to a topology the specification no longer describes.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 12.4 (Redis HA and Failure Modes), 17.8.2

package tier11_docs_test

import (
	"strings"
	"testing"
)

const (
	chartValuesPath = "charts/lenny/values.yaml"

	// clusterAddrsCommentMarker and singleTenantCommentMarker open the two
	// values.yaml comment blocks this test reads.
	clusterAddrsCommentMarker = "cluster.addrs is a comma-separated list"
	singleTenantCommentMarker = "singleTenantRedisTopology documents the operator's"

	// specRedisCutover is the §12.4 migration step that switches the whole
	// deployment to Redis Cluster.
	specRedisCutover = "switch `redis.provider` to `cluster`"

	// specTier3OneCluster is the §17.8.2 Tier 3 concern-separation statement.
	specTier3OneCluster = "One Redis Cluster for every concern"

	// clusterAddrsCutover is the comment's statement of the §12.4 cutover.
	clusterAddrsCutover = "the topology a deployment moves to at the §12.4 whole-deployment `redis.provider` cutover"
)

// staleRedisTopologyClaims are the claims the two comments made before the
// §12.4 pre-plan became a whole-deployment cutover and §17.8.2 put every
// Tier 3 concern on one Redis Cluster.
var staleRedisTopologyClaims = []string{
	"Tier 2→3 migration topology",
	"high-write Quota/Rate Limiting instance",
	"single-threaded-primary ceiling",
	"Sentinel topology is sufficient",
}

// valuesCommentBlock returns the contiguous `#` comment block in body that
// contains marker, with the comment prefixes stripped and the lines joined by
// single spaces so a phrase wrapped across lines matches as one.
func valuesCommentBlock(body, marker string) (string, bool) {
	var block []string
	found := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") {
			if found {
				break
			}
			block = block[:0]
			continue
		}
		text := strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
		block = append(block, text)
		if strings.Contains(text, marker) {
			found = true
		}
	}
	if !found {
		return "", false
	}
	return strings.Join(block, " "), true
}

// requireSpecStatement fails the test when the spec file no longer carries
// statement, so the chart comments are re-derived from the current text.
func requireSpecStatement(t *testing.T, root, file, statement string) {
	t.Helper()
	if !strings.Contains(readRepoFile(t, root, "spec", file), statement) {
		t.Fatalf("spec/%s no longer states %q; re-derive the values.yaml Redis comments from the current text", file, statement)
	}
}

// requireValuesComment returns the values.yaml comment block opened by marker.
func requireValuesComment(t *testing.T, values, marker string) string {
	t.Helper()
	block, ok := valuesCommentBlock(values, marker)
	if !ok {
		t.Fatalf("%s: no comment block containing %q", chartValuesPath, marker)
	}
	return block
}

// TestValuesRedisTopologyCommentsMatchSpec_spec_12_4 pins the
// `redis.cluster.addrs` and `capacityPlanning.singleTenantRedisTopology`
// comments to the §12.4 whole-deployment `redis.provider` cutover and the
// §17.8.2 Tier 3 rows: the cluster comment names the cutover, and neither
// comment carries a per-instance migration or a Tier 3 Sentinel sufficiency
// claim.
//
// spec: 12.4 (Redis HA and Failure Modes), 17.8.2
//
// diagnosis: a charts/lenny/values.yaml Redis topology comment disagrees with the §12.4 migration pre-plan or the §17.8.2 Tier 3 rows; restate the comment from the current specification text.
func TestValuesRedisTopologyCommentsMatchSpec_spec_12_4(t *testing.T) {
	root := repoRoot(t)
	requireSpecStatement(t, root, "12_storage-architecture.md", specRedisCutover)
	requireSpecStatement(t, root, "17_deployment-topology.md", specTier3OneCluster)

	values := readRepoFile(t, root, chartValuesPath)
	clusterAddrs := requireValuesComment(t, values, clusterAddrsCommentMarker)
	singleTenant := requireValuesComment(t, values, singleTenantCommentMarker)

	if !strings.Contains(clusterAddrs, clusterAddrsCutover) {
		t.Errorf("%s: redis.cluster.addrs comment does not state %q\ncomment: %s", chartValuesPath, clusterAddrsCutover, clusterAddrs)
	}
	for _, block := range []struct{ name, text string }{
		{"redis.cluster.addrs", clusterAddrs},
		{"capacityPlanning.singleTenantRedisTopology", singleTenant},
	} {
		for _, stale := range staleRedisTopologyClaims {
			if strings.Contains(block.text, stale) {
				t.Errorf("%s: %s comment still carries %q", chartValuesPath, block.name, stale)
			}
		}
	}
}

// TestValuesCommentBlockJoinsWrappedLines_spec_12_4 pins the comment-block
// reader the topology check depends on: a phrase wrapped across comment lines
// matches, a block that lacks the marker is not returned, and a block ends at
// the first non-comment line.
//
// spec: 12.4 (Redis HA and Failure Modes)
func TestValuesCommentBlockJoinsWrappedLines_spec_12_4(t *testing.T) {
	body := strings.Join([]string{
		"  # first block",
		"key: 1",
		"  # marker opens the",
		"  # wrapped phrase here",
		"next: 2",
		"  # trailing block",
	}, "\n")
	cases := []struct {
		name, marker, want string
		ok                 bool
	}{
		{"wrapped", "marker opens", "marker opens the wrapped phrase here", true},
		{"first", "first block", "first block", true},
		{"absent", "no such marker", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := valuesCommentBlock(body, tc.marker)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("valuesCommentBlock(%q) = %q, %v; want %q, %v", tc.marker, got, ok, tc.want, tc.ok)
			}
		})
	}
}
