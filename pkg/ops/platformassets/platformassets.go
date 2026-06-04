// SPDX-License-Identifier: MIT

// Package platformassets exposes the §25.8 air-gap requirement that the
// CRD manifests and migration SQL are compiled into the lenny-ops binary
// rather than fetched from the release channel (spec line 3425). The
// upgrade Phase 3 (CRDUpdate) applies CRDs and Phase 4 (SchemaMigration)
// runs migrations using assets the binary already carries, so an
// air-gapped install needs no extra steps for schema/CRD updates.
//
// Importing this package pulls both embed.FS trees into any binary that
// links it. lenny-ops imports it so the assets travel with the operability
// binary that orchestrates the upgrade.
//
// spec: §25.8 Air-Gapped Support item 5 (line 3425).
package platformassets

import (
	"io/fs"

	"github.com/lennylabs/lenny/migrations"
	"github.com/lennylabs/lenny/pkg/embedded/crds"
)

// CRDNames returns the embedded CustomResourceDefinition manifest file
// names compiled into the binary.
func CRDNames() ([]string, error) {
	return globNames(crds.FS, "*.yaml")
}

// MigrationNames returns the embedded forward (up) migration file names
// compiled into the binary.
func MigrationNames() ([]string, error) {
	return globNames(migrations.FS, "*.up.sql")
}

// ReadCRD returns the bytes of an embedded CRD manifest by file name.
func ReadCRD(name string) ([]byte, error) { return crds.FS.ReadFile(name) }

// ReadMigration returns the bytes of an embedded migration file by name.
func ReadMigration(name string) ([]byte, error) { return migrations.FS.ReadFile(name) }

// Inventory is a one-line description of the embedded asset counts for the
// startup log, so an operator can confirm an air-gapped lenny-ops carries
// the assets the upgrade needs.
func Inventory() (crdCount, migrationCount int, err error) {
	crdNames, err := CRDNames()
	if err != nil {
		return 0, 0, err
	}
	migNames, err := MigrationNames()
	if err != nil {
		return 0, 0, err
	}
	return len(crdNames), len(migNames), nil
}

func globNames(fsys fs.FS, pattern string) ([]string, error) {
	names, err := fs.Glob(fsys, pattern)
	if err != nil {
		return nil, err
	}
	return names, nil
}
