// SPDX-License-Identifier: MIT

package tier0_static

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// playgroundAuthTestFiles are the in-package test files that encode the
// §27.3.1 authentication guarantees which no tests/tierN file covers:
// cookie opacity and the session-to-tenant fan-in index, the
// spec-mandated cross-replica revocation assertion, the pub/sub
// propagation accelerator, the user-invalidation fan-out, the
// mint-time session-record rewrite, the authoritative per-request
// revocation check on the auth hot path, and the mint and revoke audit
// events. `lenny-test validate-maps` only walks tests/tier2_component
// and above for orphaned test files, so an in-package test under pkg/
// or cmd/ can encode a §27.3 guarantee and still be invisible to the
// spec map. This list keeps the §27.3 entry honest about what is
// covered on disk.
var playgroundAuthTestFiles = []string{
	"pkg/gateway/mcpfabric/playground/cookie_opaque_test.go",
	"pkg/gateway/mcpfabric/playground/cross_replica_test.go",
	"pkg/gateway/mcpfabric/playground/revocation_propagation_test.go",
	"pkg/gateway/mcpfabric/playground/token_currentexp_test.go",
	"pkg/gateway/mcpfabric/playground/user_invalidation_test.go",
	"pkg/gateway/middleware/auth/playground_revocation_test.go",
	"cmd/lenny-gateway/playground_audit_test.go",
}

// spec: §27.3.1 (OIDC cookie-to-MCP-bearer exchange) — "**Integration
//
//	test.** `TestPlaygroundSessionRevocationCrossReplica` MUST assert
//	that a logout on replica A invalidates a subsequent request
//	carrying the same cookie or bearer on replica B, both before and
//	after the pub/sub message is delivered"; and "Every authenticated
//	request carrying a playground-origin bearer ... MUST consult
//	`t:{tenant_id}:pg:revoked:{jti}` on the auth hot path before the
//	bearer is honored."
//
// diagnosis: The tests/spec-map.json §27.3 entry no longer references
//
//	the on-disk test files that encode the §27.3.1 cookie-opacity,
//	cross-replica revocation, propagation, user-invalidation,
//	mint-record, per-request-revocation-check, and audit-event
//	guarantees. Either the entry lost a reference or a file was renamed
//	or deleted. A reviewer reading the §27.3 entry will conclude those
//	guarantees are untested and either duplicate the coverage or treat
//	a regression in them as unguarded. Restore the reference in the
//	§27.3 `tests` list, or update this list when a file is renamed.
func TestSpecMap273ReferencesPlaygroundAuthTests(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	referenced := map[string]bool{}
	for _, path := range readSpecMapTests(t)["27.3"] {
		referenced[path] = true
	}

	missing := []string{}
	absent := []string{}
	for _, path := range playgroundAuthTestFiles {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			absent = append(absent, path)
			continue
		}
		if !referenced[path] {
			missing = append(missing, path)
		}
	}

	sort.Strings(absent)
	sort.Strings(missing)
	if len(absent) > 0 {
		t.Errorf("test file(s) named by this guard are absent from disk: %v; a rename must be reflected both here and in the tests/spec-map.json §27.3 entry", absent)
	}
	if len(missing) > 0 {
		t.Errorf("the tests/spec-map.json §27.3 entry references no test in %v; each encodes a §27.3.1 authentication guarantee and must appear in the section's tests list", missing)
	}
}
