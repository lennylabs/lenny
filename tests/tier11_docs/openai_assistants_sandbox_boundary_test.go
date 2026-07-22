// SPDX-License-Identifier: MIT

// Tier-11 documentation check for the §26.10 openai-assistants
// code_interpreter out-of-sandbox boundary. These tests are NOT under a
// build tag because they exercise the repository state directly — no
// external infrastructure required.

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// diagnosis: the §26.10 warning that OpenAI's hosted code_interpreter
// executes outside Lenny's sandbox has regressed out of the spec. This
// is the source-of-truth sentence the operator-facing runtime catalog
// row (TestRuntimeAuthorGuideCatalogDisclosesCodeInterpreterBoundary)
// and the embedded reference catalog description both derive from; if
// it disappears here, an operator reading only the spec no longer sees
// the boundary at all.
//
// spec: §26.10 ("OpenAI's hosted code interpreter runs outside Lenny's
// sandbox. ... Lenny does not proxy or intercept code interpreter
// invocations; they execute inside OpenAI's infrastructure.")
func TestSpecOpenAIAssistantsCodeInterpreterBoundaryStated(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "spec", "26_reference-runtime-catalog.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	content := string(b)

	section := sectionBody(content, "### 26.10 `openai-assistants`", "### 26.11")
	if section == "" {
		t.Fatal("could not locate the §26.10 openai-assistants section in spec/26_reference-runtime-catalog.md")
	}

	for _, want := range []string{
		"code interpreter runs outside Lenny's sandbox",
		"Lenny does not proxy or intercept code interpreter invocations",
		"execute inside OpenAI's infrastructure",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("§26.10 openai-assistants section missing code_interpreter out-of-sandbox boundary text %q", want)
		}
	}
}

// diagnosis: the operator-facing reference-runtime catalog table in the
// Runtime Author Guide no longer discloses that OpenAI's hosted
// code_interpreter executes outside Lenny's sandbox for the
// openai-assistants row. The runtime advertises sandboxed isolation
// (isolationProfile: sandboxed per §26.10) while this one tool class
// runs entirely inside OpenAI's infrastructure; an operator scanning
// only this table and not the full spec must still see the boundary
// stated against the openai-assistants row itself, not buried
// elsewhere on the page.
//
// spec: §26.10 ("OpenAI's hosted code interpreter runs outside Lenny's
// sandbox.")
func TestRuntimeAuthorGuideCatalogDisclosesCodeInterpreterBoundary(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "docs", "runtime-author-guide", "index.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	content := string(b)

	row := openAIAssistantsCatalogRow.FindString(content)
	if row == "" {
		t.Fatalf("could not locate the `openai-assistants` row in the reference-runtime catalog table in %s", path)
	}

	if !strings.Contains(row, "code_interpreter") {
		t.Errorf("openai-assistants catalog row does not mention code_interpreter: %q", row)
	}
	if !strings.Contains(row, "outside Lenny's sandbox") {
		t.Errorf("openai-assistants catalog row does not disclose that code_interpreter runs outside Lenny's sandbox: %q", row)
	}
}

// openAIAssistantsCatalogRow matches the markdown table row for the
// `openai-assistants` reference runtime in the Runtime Author Guide's
// catalog table (docs/runtime-author-guide/index.md).
var openAIAssistantsCatalogRow = regexp.MustCompile("(?m)^\\|\\s*`openai-assistants`.*\\|$")

// sectionBody returns the content strictly between the line containing
// startMarker and the next line containing endMarker, or "" if either
// is absent.
func sectionBody(content, startMarker, endMarker string) string {
	start := strings.Index(content, startMarker)
	if start < 0 {
		return ""
	}
	rest := content[start+len(startMarker):]
	end := strings.Index(rest, endMarker)
	if end < 0 {
		return rest
	}
	return rest[:end]
}
