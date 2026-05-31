// SPDX-License-Identifier: MIT

package workspace_test

import (
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// spec: §14.1 line 326 — a plan whose schemaVersion exceeds the known
// revision MUST be rejected before materialization. schemaVersion 0
// (unstamped/empty) and the known version pass; anything higher fails
// with ErrSchemaVersionUnsupported. F-14.1.3.
func TestCheckSchemaVersion_spec_14_1_326(t *testing.T) {
	cases := []struct {
		name    string
		version int
		wantErr bool
	}{
		{"unstamped zero passes", 0, false},
		{"known version passes", workspace.MaxKnownSchemaVersion, false},
		{"one above known rejected", workspace.MaxKnownSchemaVersion + 1, true},
		{"far future rejected", 9999, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := workspace.CheckSchemaVersion(tc.version)
			if tc.wantErr {
				if !errors.Is(err, workspace.ErrSchemaVersionUnsupported) {
					t.Fatalf("version %d: got err %v, want ErrSchemaVersionUnsupported", tc.version, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("version %d: unexpected error %v", tc.version, err)
			}
		})
	}
}

// spec: §14.1 line 320 — the adapter's known schemaVersion must track the
// gateway-side wire identifier; a future bump that lands on only one side
// would silently break the version-skew gate. F-14.1.3.
func TestAdapterSchemaVersionTracksGateway_spec_14_1_320(t *testing.T) {
	if workspace.MaxKnownSchemaVersion != workspaceplan.SchemaVersion {
		t.Fatalf("adapter MaxKnownSchemaVersion=%d, gateway SchemaVersion=%d; the two must agree",
			workspace.MaxKnownSchemaVersion, workspaceplan.SchemaVersion)
	}
}
