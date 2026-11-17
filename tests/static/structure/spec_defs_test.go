// SPDX-License-Identifier: MIT

package structure

import (
	"bytes"
	"context"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/defs"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The definition blocks of the specification tree define each enumerable
// fact once, and the catalogs, the folder indexes, and the generated
// sections of component pages are derived from them by
// `specshift defs generate`. These gates run the two defs commands over the
// real tree. They hold the specification's own structure rather than a
// platform behavior, so no specification section applies.

// diagnosis: a definition block under spec/ breaks its kind's schema,
// repeats an anchor, names a page code no page registers, or a page carries
// a malformed generated-section marker. The output names each file and
// line; the schema reference is scripts/specshift/defs/SCHEMAS.md.
func TestSpecDefinitionBlocksAreValid(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	root := schematest.RepoRoot(t)
	if err := defs.Run(context.Background(), []string{"-root", root, "check"}, &out); err != nil {
		t.Fatalf("specshift defs check: %v\n%s", err, out.String())
	}
}

// diagnosis: a catalog under spec/catalogs/, a folder's index.md, or a
// generated section of a page no longer matches what the definition blocks
// and the front matter produce. Run `go run ./scripts/specshift defs
// generate` and commit the files it names.
func TestSpecGeneratedFilesAreCurrent(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	root := schematest.RepoRoot(t)
	if err := defs.Run(context.Background(), []string{"-root", root, "generate", "--check"}, &out); err != nil {
		t.Fatalf("specshift defs generate --check: %v\n%s", err, out.String())
	}
}
