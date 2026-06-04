// SPDX-License-Identifier: MIT

package platformassets_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/platformassets"
)

// spec: §25.8 line 3425 — the CRD manifests and migration SQL are compiled
// into the lenny-ops binary so an air-gapped CRDUpdate/SchemaMigration
// needs no release-channel asset fetch. F-25.8.12 item 5.
func TestEmbeddedAssetsPresent_spec_25_8(t *testing.T) {
	crdNames, err := platformassets.CRDNames()
	if err != nil {
		t.Fatalf("CRDNames: %v", err)
	}
	if len(crdNames) == 0 {
		t.Fatal("no CRD manifests embedded; air-gapped CRDUpdate cannot run")
	}
	migNames, err := platformassets.MigrationNames()
	if err != nil {
		t.Fatalf("MigrationNames: %v", err)
	}
	if len(migNames) == 0 {
		t.Fatal("no migrations embedded; air-gapped SchemaMigration cannot run")
	}

	// Each embedded asset is readable.
	for _, name := range crdNames {
		b, err := platformassets.ReadCRD(name)
		if err != nil || len(b) == 0 {
			t.Fatalf("ReadCRD(%s): %v len=%d", name, err, len(b))
		}
		if !strings.HasSuffix(name, ".yaml") {
			t.Errorf("CRD asset has unexpected name: %s", name)
		}
	}

	crdCount, migCount, err := platformassets.Inventory()
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	if crdCount != len(crdNames) || migCount != len(migNames) {
		t.Errorf("inventory counts = (%d,%d), want (%d,%d)", crdCount, migCount, len(crdNames), len(migNames))
	}
}
