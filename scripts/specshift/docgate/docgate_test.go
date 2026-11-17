// SPDX-License-Identifier: MIT

package docgate

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests cover contributor tooling over the specification structure
// rather than a platform behavior, so they carry no specification
// annotation. The front-matter and catalog-name gates run over a fixture
// specification under testdata/spec with a passing and a failing set of
// documentation pages.

// fixtureRoot copies the fixture specification and one documentation set
// into a fresh root.
func fixtureRoot(t *testing.T, docs string) string {
	t.Helper()
	root := t.TempDir()
	for _, src := range []string{"testdata/spec", filepath.Join("testdata", docs)} {
		copyDir(t, src, root)
	}
	return root
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func joined(ps []Problem) string {
	lines := make([]string, 0, len(ps))
	for _, p := range ps {
		lines = append(lines, p.String())
	}
	return strings.Join(lines, "\n")
}

func mustContain(t *testing.T, what, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("%s lacks %q:\n%s", what, w, got)
		}
	}
}

func TestCheckPassesThePassingDocumentation(t *testing.T) {
	t.Parallel()
	res, err := Check(fixtureRoot(t, "docs-pass"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.FrontMatter) > 0 || len(res.Names.Problems) > 0 {
		t.Fatalf("the passing documentation has problems:\n%s\n%s", joined(res.FrontMatter), joined(res.Names.Problems))
	}
	if res.Pages != 5 || res.WithSpec != 4 || res.InScope != 3 {
		t.Errorf("read %d page(s), %d with spec, %d in scope; want 5, 4, and 3", res.Pages, res.WithSpec, res.InScope)
	}
	mustContain(t, "the undecided findings", joined(res.Names.Undecided),
		"pools.md:26: the default quoted for `minWarm` (2 (small), 5 (large)) is not a single value",
		"the metric `lenny_legacy_requests_total` is not in the catalogs yet",
		"the event type `dev.lenny.legacy_event` is not in the catalogs yet",
		"the CLI command `lenny-ctl legacy sweep` is not in the catalogs yet",
		"the alert `LegacyAlert` is not in the catalogs yet",
		"`UnknownThing` reads as an alert from its context",
	)
	for _, unwanted := range []string{"lenny_not_in_any_catalog", "lenny_commented_out_metric", "lenny_bogus_metric_total", "claimHoldTTL", "recycle.mode", "maxPodsPerPool"} {
		if strings.Contains(joined(res.Names.Undecided), unwanted) {
			t.Errorf("the undecided findings name %s, which the gate reads as decided or does not read:\n%s", unwanted, joined(res.Names.Undecided))
		}
	}
}

func TestCheckRefusesTheFailingDocumentation(t *testing.T) {
	t.Parallel()
	res, err := Check(fixtureRoot(t, "docs-fail"))
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, "the front-matter problems", joined(res.FrontMatter),
		"docs/guide/front-matter.md:3: spec must be a list of specification page codes",
		`docs/guide/unknown-code.md:3: spec names "NOPE", which spec/registry.json does not register`,
	)
	got := joined(res.Names.Problems)
	mustContain(t, "the catalog-name problems", got,
		"names.md:8: the metric `lenny_bogus_metric_total` is not in the catalogs, and no section of the specification defines it",
		"names.md:8: the label or annotation `lenny.dev/bogus` is not in the catalogs",
		"names.md:8: the event type `dev.lenny.bogus_happened` is not in the catalogs",
		"names.md:8: the CLI command `lenny-ctl pool bogus` is not in the catalogs",
		`names.md:10: the default quoted for `+"`minWarm`"+` is "3", and the definition block gives "2"`,
		`names.md:10: the default quoted for `+"`recycle.mode`"+` is "always", and the definition block gives "never"`,
		`names.md:14: the unit quoted for `+"`minWarm`"+` is "bytes", and the definition block gives "pods"`,
		`names.md:15: the default quoted for `+"`pool.claimHoldTTL`"+` is "45s", and the definition block gives "30"`,
		`names.md:19: the value quoted for `+"`maxPodsPerPool`"+` is "500", and the definition block gives "10000"`,
	)
	if strings.Contains(got, "unknown-code.md") {
		t.Errorf("a page whose spec names an unregistered code was read for catalog names:\n%s", got)
	}
}

func TestCompareValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		quoted, want, unit string
		v                  verdict
	}{
		{"2", "2", "", match},
		{"3", "2", "", mismatch},
		{"30s", "30", "seconds", match},
		{"0.5m", "30", "seconds", match},
		{"45s", "30", "seconds", mismatch},
		{"30 seconds", "30", "seconds", match},
		{"1Gi", "1024", "MiB", undecided},
		{"10 pods", "10", "pods", match},
		{"never", "never", "", match},
		{"always", "never", "", mismatch},
		{"\"never\"", "never", "", match},
		{"1,000", "1000", "", match},
		{"[0, 10]", "none", "", undecided},
		{"5", "", "", undecided},
	}
	for _, c := range cases {
		if got := compareValue(c.quoted, c.want, c.unit); got != c.v {
			t.Errorf("compareValue(%q, %q, %q) = %d, want %d", c.quoted, c.want, c.unit, got, c.v)
		}
	}
	for _, c := range []struct {
		quoted, want string
		v            verdict
	}{{"pod", "pods", match}, {"s", "seconds", match}, {"bytes", "pods", mismatch}, {"pods", "", undecided}} {
		if got := compareUnit(c.quoted, c.want); got != c.v {
			t.Errorf("compareUnit(%q, %q) = %d, want %d", c.quoted, c.want, got, c.v)
		}
	}
}

func TestSplitCellsHonorsEscapedPipesAndCodeSpans(t *testing.T) {
	t.Parallel()
	got := splitCells("| `a|b` | c \\| d | e |")
	want := []string{"`a|b`", "c | d", "e"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("splitCells = %q, want %q", got, want)
	}
}

func TestClassifyReadsTheFormThenTheContext(t *testing.T) {
	t.Parallel()
	cases := []struct {
		span         Span
		kind         string
		strong       bool
		name         string
		ignoredShape bool
	}{
		{span: Span{Text: "lenny_a_b{pool=\"x\"}"}, kind: kindMetric, strong: true, name: "lenny_a_b"},
		{span: Span{Text: "lenny_token", After: " role"}, kind: ""},
		{span: Span{Text: "lenny.dev/pool=alpha"}, kind: kindLabel, strong: true, name: "lenny.dev/pool"},
		{span: Span{Text: "dev.lenny.x_y"}, kind: kindEvent, strong: true, name: "dev.lenny.x_y"},
		{span: Span{Text: "lenny-ctl pool create"}, kind: kindCLI, strong: true, name: "lenny-ctl pool create"},
		{span: Span{Text: "QUOTA_EXCEEDED", Before: "returns the error "}, kind: kindError, name: "QUOTA_EXCEEDED"},
		{span: Span{Text: "QUOTA_EXCEEDED"}, kind: ""},
		{span: Span{Text: "WarmPoolLow", After: " alerts fire"}, kind: kindAlert, name: "WarmPoolLow"},
		{span: Span{Text: "gateway.maxSessions", Header: "Field"}, kind: kindSetting, name: "gateway.maxSessions"},
		{span: Span{Text: "not a setting", Header: "Field"}, kind: ""},
	}
	for _, c := range cases {
		kind, name, strong := classify(c.span)
		if kind != c.kind || (kind != "" && (name != c.name || strong != c.strong)) {
			t.Errorf("classify(%+v) = %q, %q, %v; want %q, %q, %v", c.span, kind, name, strong, c.kind, c.name, c.strong)
		}
	}
}

func TestRunReportsAndRefuses(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"-root", fixtureRoot(t, "docs-pass"), "check"}, &out); err != nil {
		t.Fatalf("docs check over the passing documentation: %v\n%s", err, out.String())
	}
	mustContain(t, "the output", out.String(), "undecided: docs/guide/pools.md", "docs check: 5 page(s), 4 with spec front matter, 3 in scope")
	out.Reset()
	err := Run(context.Background(), []string{"-root", fixtureRoot(t, "docs-fail"), "check"}, &out)
	if !errors.Is(err, ErrProblems) {
		t.Fatalf("docs check over the failing documentation returned %v, want ErrProblems\n%s", err, out.String())
	}
	mustContain(t, "the output", out.String(), "docs/guide/names.md:8:")
	for _, args := range [][]string{{}, {"bogus"}, {"check", "extra"}} {
		if err := Run(context.Background(), append([]string{"-root", t.TempDir()}, args...), &out); err == nil {
			t.Errorf("docs %v succeeded, want an error", args)
		}
	}
}

func TestParsePageReadsTheSpecKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		content string
		hasSpec bool
		codes   []string
		errText string
	}{
		{"# No front matter\n", false, nil, ""},
		{"---\ntitle: x\n---\n", false, nil, ""},
		{"---\ntitle: x\nspec: [A, B]\n---\n", true, []string{"A", "B"}, ""},
		{"---\nspec: []\n---\n", true, nil, ""},
		{"---\nspec: A\n---\n", true, nil, "must be a list"},
		{"---\nspec: [[A]]\n---\n", true, nil, "each entry"},
		{"---\nspec: [A\n---\n", true, nil, "not valid YAML"},
		{"---\ntitle: [x\n---\n", false, nil, ""},
	}
	for _, c := range cases {
		p := ParsePage("docs/x.md", c.content)
		if p.HasSpec != c.hasSpec || strings.Join(p.Codes, ",") != strings.Join(c.codes, ",") ||
			(c.errText == "") != (p.SpecError == "") || !strings.Contains(p.SpecError, c.errText) {
			t.Errorf("ParsePage(%q) = spec %v %v %q", c.content, p.HasSpec, p.Codes, p.SpecError)
		}
	}
}
