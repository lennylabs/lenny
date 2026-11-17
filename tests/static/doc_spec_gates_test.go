// SPDX-License-Identifier: MIT

package static

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/docgate"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// These gates keep the documentation under docs/ in step with the
// specification, through the docgate package of specshift. They hold the
// documentation rule of .claude/rules/doc-content.md ("Documentation and
// the specification") rather than a platform behavior, so no specification
// section applies and they carry no specification annotation. A page is
// checked once its front matter carries the spec key, and the catalog-name
// gate reads it once every page its spec key names has landed. The
// generated reference pages are held by TestSpecGeneratedFilesAreCurrent.

// diagnosis: a documentation page's spec front matter is not a list of
// page codes, or names a code spec/registry.json does not register; or a
// page in scope names a setting, metric, alert, error code, label,
// annotation, event type, or CLI command that the catalogs and the
// numbered sections do not define, or quotes a default, a limit, or a
// unit that differs from the definition block. Run `go run
// ./scripts/specshift docs check` to reproduce it.
func TestDocumentationFrontMatterAndCatalogNamesHold(t *testing.T) {
	t.Parallel()
	res, err := docgate.Check(schematest.RepoRoot(t))
	if err != nil {
		t.Fatalf("run the documentation gates: %v", err)
	}
	if res.Pages == 0 {
		t.Fatal("the documentation holds no page; a gate over no page certifies nothing")
	}
	problems := append(append([]docgate.Problem(nil), res.FrontMatter...), res.Names.Problems...)
	if len(problems) > 0 {
		lines := make([]string, 0, len(problems))
		for _, p := range problems {
			lines = append(lines, p.String())
		}
		t.Errorf("%d problem(s) in the documentation:\n  %s", len(problems), strings.Join(lines, "\n  "))
	}
	for _, u := range res.Names.Undecided {
		t.Logf("undecided: %s", u)
	}
	t.Logf("%d page(s), %d with spec front matter, %d in scope for the catalog names", res.Pages, res.WithSpec, res.InScope)
}

// diagnosis: the branch adds, modifies, or retires a requirement, an
// operation, or a definition block on a landed specification page, and
// edits no documentation page whose front matter names that page, and no
// change folder it touches states under its Documentation section that no
// reader-facing behavior changes. The finding names the page. Run `go run
// ./scripts/specshift docs impact` to reproduce it.
func TestBranchDiffCarriesItsDocumentation(t *testing.T) {
	t.Parallel()
	rep, err := docgate.CheckImpact(context.Background(), schematest.RepoRoot(t))
	if errors.Is(err, docgate.ErrNoBase) {
		t.Skipf("not-yet-applicable: %v, as in a shallow clone, so there is no branch diff to read", err)
	}
	if err != nil {
		t.Fatalf("read the branch diff: %v", err)
	}
	if len(rep.Problems) > 0 {
		lines := make([]string, 0, len(rep.Problems))
		for _, p := range rep.Problems {
			lines = append(lines, p.String())
		}
		t.Errorf("%d specification page(s) changed since %s without their documentation:\n  %s",
			len(rep.Problems), rep.Base, strings.Join(lines, "\n  "))
	}
	if len(rep.NotYet) > 0 {
		t.Logf("not checked yet, because the page has not landed: %s", strings.Join(rep.NotYet, ", "))
	}
}
