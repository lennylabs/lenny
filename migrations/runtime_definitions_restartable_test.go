// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §5.1 (command), §5.2 (recycle.runtimeProcess) — migration 0189 adds
// the restartable column on runtime_definitions so the RuntimeReconciler
// can mirror the derived restartable flag into the gateway runtime
// registry, where pool admission reads it before admitting
// recycle.runtimeProcess: restart. The column defaults to false so a row the
// reconciler has not mirrored refuses restart, and the down migration drops
// it.
func TestRuntimeRestartableMigration_spec_5_1(t *testing.T) {
	b, err := FS.ReadFile("0189_runtime_restartable.up.sql")
	if err != nil {
		t.Fatalf("read migration 0189: %v", err)
	}
	up := string(b)
	for _, want := range []string{
		"ALTER TABLE runtime_definitions",
		"ADD COLUMN restartable BOOLEAN NOT NULL DEFAULT false",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0189 up missing %q", want)
		}
	}
	// A true default would admit restart on a runtime whose command the
	// reconciler never observed, so the default must be false.
	if strings.Contains(up, "DEFAULT true") {
		t.Error("migration 0189 restartable must default to false")
	}

	down, err := FS.ReadFile("0189_runtime_restartable.down.sql")
	if err != nil {
		t.Fatalf("read migration 0189 down: %v", err)
	}
	if !strings.Contains(string(down), "DROP COLUMN IF EXISTS restartable") {
		t.Error("migration 0189 down must drop restartable")
	}
}
