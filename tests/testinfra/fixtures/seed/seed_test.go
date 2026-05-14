// SPDX-License-Identifier: MIT

package seed_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/fixtures/seed"
)

// spec: 18.1 (canonical seed statements name the documented tenants/users)
// diagnosis: A reference identifier drifted out of the statement
//
//	list. The fixtures + seeder agree on the cryptography
//	convention (acme/globex/initech, alice/bob/carol).
func TestSeedPostgresStatementsReferenceFixtures(t *testing.T) {
	stmts := seed.SeedPostgresStatements()
	joined := strings.Join(stmts, "\n")
	for _, expect := range []string{
		"'acme'", "'globex'", "'initech'",
		"alice@acme.com", "bob@acme.com", "carol@globex.com",
		"acme-default-runc", "acme-default-gvisor", "globex-default-gvisor",
		"mock-anthropic", "mock-openai", "mock-google",
	} {
		if !strings.Contains(joined, expect) {
			t.Errorf("statements miss reference identifier %q", expect)
		}
	}
}

// spec: 18.1 (idempotent inserts use ON CONFLICT DO NOTHING)
// diagnosis: A seed statement does not carry ON CONFLICT DO NOTHING.
//
//	Repeat invocations must converge to the same row set.
func TestSeedStatementsAreIdempotent(t *testing.T) {
	for _, stmt := range seed.SeedPostgresStatements() {
		if !strings.Contains(stmt, "ON CONFLICT") {
			t.Errorf("non-idempotent statement: %s", stmt)
		}
	}
}

// spec: 18.1 (Redis seed commands use SETNX / SADD shapes)
// diagnosis: A Redis seed command would clobber an existing key. Use
//
//	SET ... NX or SADD so repeat invocations don't reset
//	live state.
func TestSeedRedisCommandsAreIdempotent(t *testing.T) {
	for _, cmd := range seed.SeedRedisCommands() {
		if !strings.Contains(cmd, "NX") && !strings.HasPrefix(cmd, "SADD") {
			t.Errorf("non-idempotent redis command: %s", cmd)
		}
	}
}
