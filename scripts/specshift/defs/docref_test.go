// SPDX-License-Identifier: MIT

package defs

import (
	"strings"
	"testing"
)

// These cases cover the generator of the documentation's reference pages,
// which is specification tooling rather than a platform behavior, so they
// carry no specification annotation.

// metricPage returns a page with one metric block and the given status
// line, which is empty for a page written in full.
func metricPage(code, name, status string) string {
	return "---\ncode: " + code + "\ntype: capability\narea: pools\n" + status +
		"summary: A page with one metric.\n---\n# " + code + "\n\n<a id=\"metric-" + Slug(name) + "\"></a>\n\n" +
		"```lenny-def\nkind: metric\nname: " + name + "\ntype: counter\nlabels: []\nemitter: WPC\nsummary: Counts things. See [the page](other.md).\n```\n"
}

const metricsRefPage = "---\ntitle: Metrics\n---\n\n# Metrics\n\nIntro.\n\n<!-- generated:reference-metrics -->\n<!-- /generated:reference-metrics -->\n\n## Alert rules\n\n<!-- generated:reference-alerts -->\n<!-- /generated:reference-alerts -->\n"

func TestReferenceRegionsListOnlyTheBlocksOfLandedPages(t *testing.T) {
	t.Parallel()
	tr := loadTree(t, map[string]string{
		"spec/caps/index.md":          "---\ncode: CAPIDX\ntype: index\narea: pools\nsummary: Lists the pages.\n---\n\n# Runtimes and pools\n",
		"spec/caps/landed.md":         metricPage("LAND", "lenny_landed_total", ""),
		"spec/caps/partial.md":        metricPage("PART", "lenny_partial_total", "status: partial\n"),
		"spec/components/wpc.md":      "---\ncode: WPC\ntype: component\nstatus: placeholder\nsummary: A placeholder.\n---\n\n# Warm pool controller\n",
		"spec/components/index.md":    "---\ncode: COMPS\ntype: index\nsummary: Lists the components.\n---\n",
		"docs/reference/metrics.md":   metricsRefPage,
		"docs/reference/unrelated.md": "---\ntitle: Other\n---\n\n# Other\n",
	})
	out, problems := Generate(tr)
	if len(problems) > 0 {
		t.Fatalf("Generate refused the tree:\n%s", joinProblems(problems))
	}
	got := out["docs/reference/metrics.md"]
	if !strings.Contains(got, "`lenny_landed_total`") {
		t.Errorf("the metric of the landed page is missing:\n%s", got)
	}
	if strings.Contains(got, "lenny_partial_total") {
		t.Errorf("the metric of the partial page is listed, but its rows stay hand-written until the page lands:\n%s", got)
	}
	if !strings.Contains(got, "## Runtimes and pools metrics") {
		t.Errorf("the section is not titled by the area's index page:\n%s", got)
	}
	if strings.Contains(got, "LAND") || strings.Contains(got, "WPC") || strings.Contains(got, "](") {
		t.Errorf("the region shows a page code or a link into the specification, which a reader never sees:\n%s", got)
	}
	if !strings.Contains(got, "| Warm pool controller |") {
		t.Errorf("the emitter is not named by its page's title:\n%s", got)
	}
	if !strings.Contains(got, "Counts things. See the page.") {
		t.Errorf("the summary's link was not reduced to its text:\n%s", got)
	}
	if !strings.Contains(got, "<!-- generated:reference-alerts -->\n\n<!-- /generated:reference-alerts -->") {
		t.Errorf("a region with nothing to list does not hold exactly one blank line:\n%s", got)
	}
	if _, ok := out["docs/reference/unrelated.md"]; ok {
		t.Error("a reference page with no region was generated")
	}
}

func TestCheckRequiresEachReferencePageToCarryItsRegions(t *testing.T) {
	t.Parallel()
	tr := loadTree(t, map[string]string{
		"spec/components/wpc.md":     placeholder("WPC"),
		"docs/reference/metrics.md":  "---\ntitle: Metrics\n---\n\n<!-- generated:reference-metrics -->\n<!-- /generated:reference-metrics -->\n",
		"docs/reference/glossary.md": "---\ntitle: Glossary\n---\n\n<!-- generated:reference-glossary -->\n<!-- generated:reference-nothing -->\n",
	})
	problems := joinProblems(Check(tr))
	for _, want := range []string{
		"docs/reference/metrics.md: the reference page needs the generated region <!-- generated:reference-alerts -->",
		`docs/reference/glossary.md:6: unknown generated section "reference-nothing"`,
		`docs/reference/glossary.md:5: generated section "reference-glossary" has no closing marker`,
	} {
		if !strings.Contains(problems, want) {
			t.Errorf("Check did not report %q; it reported:\n%s", want, problems)
		}
	}
}

func TestATreeWithNoDocumentationHasNoReferencePages(t *testing.T) {
	t.Parallel()
	tr := loadTree(t, map[string]string{"spec/components/wpc.md": placeholder("WPC")})
	if len(tr.RefPages) != 0 {
		t.Errorf("LoadTree read %d reference page(s) from a tree with no docs folder", len(tr.RefPages))
	}
	if problems := Check(tr); len(problems) > 0 {
		t.Errorf("Check refused a tree with no documentation:\n%s", joinProblems(problems))
	}
}

func TestEveryListingReferencePageCarriesARegion(t *testing.T) {
	t.Parallel()
	got := referencePages
	for _, p := range []string{"configuration.md", "metrics.md", "error-catalog.md", "cloudevents-catalog.md", "state-machines.md", "glossary.md"} {
		if len(got[p]) == 0 {
			t.Errorf("%s/%s carries no region", DocsReferenceDir, p)
		}
	}
}
