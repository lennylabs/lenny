// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// mustReadMigration reads a migration file from the embedded FS or fails
// the test. Shared by the §25.4 / §25.8 / §25.9 / §25.11 ops_* migration
// text tests (0121-0125).
func mustReadMigration(t *testing.T, name string) string {
	t.Helper()
	b, err := FS.ReadFile(name)
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	return string(b)
}

// assertPlatformScoped asserts a §25 control-plane migration carries no
// tenant-isolation apparatus: the ops_* / platform_* tables are
// platform-scoped (§25.4 line 1492 PlatformPostgres()), so they must not
// declare a tenant_id column, an RLS policy, or the tenant guard.
func assertPlatformScoped(t *testing.T, num, up string) {
	t.Helper()
	for _, forbidden := range []string{"ROW LEVEL SECURITY", "lenny_tenant_guard", "tenant_id"} {
		if strings.Contains(up, forbidden) {
			t.Errorf("migration %s up should be platform-scoped but contains %q", num, forbidden)
		}
	}
}
