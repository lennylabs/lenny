// SPDX-License-Identifier: MIT

package tier0_static

import (
	"bytes"
	"context"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/ref"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The reference gates hold the new specification tree's link rules on the
// tracked tree: spec/registry.json matches a fresh build, every link into
// spec/ outside the numbered files resolves, every fragment on a link into
// a new-tree page is an identifier or term anchor registered on that page,
// no link targets a heading anchor on another page or carries a fragment
// into a placeholder, no code, anchor, or block is registered twice, and
// every retired identifier names a successor or its removal. The pass that
// enforces them is scripts/specshift/ref. The structure of the
// specification is contributor tooling rather than a specified platform
// behavior, so no specification section applies.

// diagnosis: spec/registry.json no longer matches the pages under spec/,
// because a page, a requirement, a term, an operation, or a definition
// block was added, moved, or removed without rebuilding it. Run
// `go run ./scripts/specshift ref fix` after a move, or
// `go run ./scripts/specshift ref registry` otherwise, and commit the result.
func TestSpecRegistryIsCurrent(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	tree := ref.NewTree(schematest.RepoRoot(t))
	if err := ref.Run(context.Background(), tree, []string{"registry", "--check"}, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
}

// diagnosis: a link into the new specification tree breaks a link rule,
// or the registry holds a duplicate or an unrecorded retirement. The
// output names each problem with its file and line. After a page move,
// `go run ./scripts/specshift ref fix` rewrites the link paths.
func TestSpecReferencesResolve(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	tree := ref.NewTree(schematest.RepoRoot(t))
	if err := ref.Run(context.Background(), tree, []string{"check"}, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
}
