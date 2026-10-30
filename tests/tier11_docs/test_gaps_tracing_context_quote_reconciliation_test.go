// SPDX-License-Identifier: MIT

// Tier-11 consistency check between the coverage audit at TEST-GAPS.md and the
// spec and docs it quotes.
//
// Each TEST-GAPS finding opens with a `**Spec/Doc:**` bullet that quotes the
// requirement and names the file it comes from. Those quotes are the input to
// the gap-closing workflow: an author reads the quote and writes the test it
// describes. A quote that no longer appears in the file it cites therefore
// directs the author at behavior the platform does not have.
//
// The `set_tracing_context` contract changed: the gateway merges a submitted
// tracing context into the session's recorded context and validates the merged
// result when the identifiers are registered, and it attaches the registered
// context to the child's delegation lease. The adapter stores nothing and the
// gateway does not defer validation to the delegation call. Two audit entries
// quoted the superseded wording; this check pins every audit quotation of the
// `set_tracing_context` contract to text that still exists in the cited file,
// so a later spec edit that orphans one of them fails this tier instead of
// seeding a test for behavior that was removed.
//
// The check is scoped to the `set_tracing_context` contract. Extending the same
// mechanism to every audit quotation requires reconciling the paraphrased and
// elided quotes across the rest of the audit, which is its own sweep.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 8.5 (set_tracing_context delegation tool), 8.3 (registration-time
// merge and validation), 28.5.3 (set_tracing_context frame)

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// specDocBullet is one `**Spec/Doc:**` bullet of a TEST-GAPS finding, joined
// into a single line with the continuation lines that belong to it.
type specDocBullet struct {
	line int    // 1-indexed line of the bullet in TEST-GAPS.md
	body string // the bullet text, continuation lines folded in
}

var (
	// quotedText matches a double-quoted span of a Spec/Doc bullet. The audit
	// never nests a double quote inside a quotation.
	quotedText = regexp.MustCompile(`"([^"]+)"`)
	// citedFile matches a repository-relative file reference in a Spec/Doc
	// bullet, with or without a trailing `:line` or `:line-line`.
	citedFile = regexp.MustCompile(`(?:spec|docs|tests|pkg|cmd|sdks|charts|schemas|migrations)[\w/.\-]*\.(?:md|go|yaml|json)`)
	// rootDocFile matches the two audited documents that sit at the repo root.
	rootDocFile = regexp.MustCompile(`\b(?:TESTING|README)\.md\b`)
	// quoteElision splits a quotation at the audit's elision marker, so each
	// contiguous run of quoted text is matched on its own.
	quoteElision = regexp.MustCompile(`\s*(?:\.\.\.|…)\s*`)
	// markupNoise is the markdown punctuation that carries no words: table
	// cell separators, code-span backticks, and emphasis markers. Stripping it
	// lets a quotation that spans a table row match the row it was taken from.
	markupNoise = regexp.MustCompile("[|`*_]+")
	// whitespaceRun collapses the line breaks and cell padding that differ
	// between a quotation and its source.
	whitespaceRun = regexp.MustCompile(`\s+`)
)

// flattenQuotation reduces text to the form both sides of the comparison are
// held to: markdown markup removed, whitespace collapsed, edges trimmed.
func flattenQuotation(s string) string {
	s = strings.ReplaceAll(s, `\`, "")
	s = markupNoise.ReplaceAllString(s, " ")
	return strings.TrimSpace(whitespaceRun.ReplaceAllString(s, " "))
}

// specDocBullets returns every `**Spec/Doc:**` bullet in TEST-GAPS.md. A
// bullet ends at the next bullet, the next finding heading, or a blank line.
func specDocBullets(t testing.TB, root string) []specDocBullet {
	t.Helper()
	body := readDocPage(t, filepath.Join(root, "TEST-GAPS.md"))

	var bullets []specDocBullet
	var current *specDocBullet
	for i, line := range strings.Split(body, "\n") {
		switch {
		case strings.HasPrefix(line, "- **Spec/Doc:**"):
			bullets = append(bullets, specDocBullet{line: i + 1, body: line})
			current = &bullets[len(bullets)-1]
		case current == nil:
			// Outside a Spec/Doc bullet.
		case strings.HasPrefix(line, "- **"), strings.HasPrefix(line, "### "), strings.TrimSpace(line) == "":
			current = nil
		default:
			current.body += " " + line
		}
	}
	if len(bullets) == 0 {
		t.Fatal("TEST-GAPS.md: no `**Spec/Doc:**` bullets found (format changed?)")
	}
	return bullets
}

// citedCorpus concatenates the flattened contents of every file the bullet
// cites. A quotation is required to appear in one of them; which one is left
// open because a bullet routinely quotes two files in one sentence.
func citedCorpus(t testing.TB, root string, bullet specDocBullet) string {
	t.Helper()
	paths := append(citedFile.FindAllString(bullet.body, -1), rootDocFile.FindAllString(bullet.body, -1)...)

	var parts []string
	seen := map[string]bool{}
	for _, p := range paths {
		if seen[p] {
			continue
		}
		seen[p] = true
		b, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			// A bullet also names test files that may be renamed by unrelated
			// work; only the files that exist form the corpus, and a quotation
			// that matches none of them is reported below.
			continue
		}
		parts = append(parts, flattenQuotation(string(b)))
	}
	return strings.Join(parts, " ")
}

// quotationFound reports whether a flattened quotation still appears in the
// corpus. An enumeration the audit assembled from several table rows (a
// comma-separated run of identifiers) is matched item by item, because the
// items are verbatim even though the commas joining them are the audit's.
func quotationFound(fragment, corpus string) bool {
	if strings.Contains(corpus, fragment) {
		return true
	}
	items := strings.Split(fragment, ",")
	if len(items) < 3 {
		return false
	}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || !strings.Contains(corpus, item) {
			return false
		}
	}
	return true
}

// minQuotationLength is the shortest flattened quotation this check holds to
// its source. Below it a quotation is a bare identifier or an error code that
// the surrounding prose already carries.
const minQuotationLength = 25

// spec: 8.5, 8.3, 28.5.3
// diagnosis: a TEST-GAPS finding quotes `set_tracing_context` wording that no
//
//	longer exists in the spec or doc file it cites. The audit's quotations are
//	the input to the gap-closing workflow, so an orphaned quote directs a future
//	author to write a test for behavior the platform does not have. The known
//	case is the superseded claim that the adapter stores the tracing context and
//	attaches it to subsequent delegation requests and that the gateway validates
//	on delegation; the gateway merges the submitted context into the session's
//	recorded context, validates the merged result at registration, and attaches
//	the registered context to the child's delegation lease. A failure here means
//	either a finding still quotes the removed wording or a later spec edit moved
//	the text a finding quotes without updating the finding.
func TestTestGapsTracingContextQuotationsStillExistInCitedFiles(t *testing.T) {
	root := repoRoot(t)

	checked := 0
	for _, bullet := range specDocBullets(t, root) {
		if !strings.Contains(bullet.body, "set_tracing_context") {
			continue
		}
		corpus := citedCorpus(t, root, bullet)
		for _, quote := range quotedText.FindAllStringSubmatch(bullet.body, -1) {
			for _, fragment := range quoteElision.Split(quote[1], -1) {
				fragment = strings.Trim(flattenQuotation(fragment), ".,;: ")
				if len(fragment) < minQuotationLength {
					continue
				}
				checked++
				if !quotationFound(fragment, corpus) {
					t.Errorf("TEST-GAPS.md:%d: quoted text no longer appears in any cited file: %q", bullet.line, fragment)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("TEST-GAPS.md: no `set_tracing_context` quotations found (findings renamed or removed?)")
	}
}
