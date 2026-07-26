// SPDX-License-Identifier: MIT

// Tier-11 doc-consistency check for the spec-map exemption claims made in
// TESTING.md against tests/spec-map-exceptions.yaml and tests/spec-map.json.
//
// TESTING.md §12.1 states the coverage rule these files implement: "Every
// spec section referenced under `spec/**.md` headings has at least one
// entry in the spec map. Exceptions are listed in
// `tests/spec-map-exceptions.yaml` with a justification." A §14 coverage
// subsection that announces an exception for a section the exceptions file
// does not list, or that describes a section's feature as unbuilt while the
// spec map records tests for it, sends a reader auditing coverage to the
// wrong conclusion: the section reads as deliberately uncovered when it is
// covered, and the stated exemption cannot be found in the file that is
// supposed to carry it.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 27.2 (web playground — placement and gating)

package tier11_docs_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// specSectionRefRE matches a spec-section reference as TESTING.md writes
// it: `§spec/27`, `§spec/27.2`. TESTING.md writes references to its own
// sections as a bare `§22.2`, so the `spec/` infix is what distinguishes a
// spec section from a TESTING.md section.
var specSectionRefRE = regexp.MustCompile(`§spec/(\d+(?:\.\d+)*)`)

// exceptionReasons are the `reason` values tests/spec-map-exceptions.yaml
// uses. A prose sentence that names one of them is asserting the reason
// recorded for the section it references.
var exceptionReasons = []string{"post-v1", "non-normative", "indirect-coverage", "anti-feature", "deferred", "empty"}

// deferralPhrases are the ways TESTING.md describes a surface whose
// feature has not been built yet. Each is only true of a spec section that
// carries no tests.
var deferralPhrases = []string{
	"when the feature unblocks",
	"when the feature lands",
	"when the feature ships",
	"once the feature lands",
	"once the feature ships",
	"is not yet built",
	"is not yet implemented",
}

// readSpecMapExceptions returns, per spec-section id, the `reason`
// recorded in tests/spec-map-exceptions.yaml.
func readSpecMapExceptions(t testing.TB, root string) map[string]string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, "tests", "spec-map-exceptions.yaml"))
	if err != nil {
		t.Fatalf("read tests/spec-map-exceptions.yaml: %v", err)
	}
	var doc struct {
		Exceptions []struct {
			Section string `yaml:"section"`
			Reason  string `yaml:"reason"`
		} `yaml:"exceptions"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse tests/spec-map-exceptions.yaml: %v", err)
	}
	out := map[string]string{}
	for _, e := range doc.Exceptions {
		out[e.Section] = e.Reason
	}
	if len(out) == 0 {
		t.Fatal("tests/spec-map-exceptions.yaml lists no exceptions; the file layout changed")
	}
	return out
}

// readSpecMapTestCounts returns, per spec-section id, how many tests
// tests/spec-map.json records for that section.
func readSpecMapTestCounts(t testing.TB, root string) map[string]int {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, "tests", "spec-map.json"))
	if err != nil {
		t.Fatalf("read tests/spec-map.json: %v", err)
	}
	var doc struct {
		Sections map[string]struct {
			Tests []string `json:"tests"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse tests/spec-map.json: %v", err)
	}
	out := map[string]int{}
	for id, sec := range doc.Sections {
		out[id] = len(sec.Tests)
	}
	if len(out) == 0 {
		t.Fatal("tests/spec-map.json records no sections; the file layout changed")
	}
	return out
}

// testingMdSentences splits TESTING.md into sentence-sized units. Prose
// paragraphs in TESTING.md occupy a single physical line, so a sentence
// split on `. ` (plus the line break) keeps an exemption claim together
// with the section ids it names while separating it from unrelated
// sentences in the same paragraph.
func testingMdSentences(t testing.TB, root string) []string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, "TESTING.md"))
	if err != nil {
		t.Fatalf("read TESTING.md: %v", err)
	}
	var out []string
	for _, line := range strings.Split(string(body), "\n") {
		for _, sentence := range strings.Split(line, ". ") {
			sentence = strings.TrimSpace(sentence)
			if sentence != "" {
				out = append(out, sentence)
			}
		}
	}
	return out
}

// spec: §27.2 (web playground — placement and gating): "The playground is
//
//	served by the **gateway** (not `lenny-ops`) at `/playground` on the same
//	Ingress as the MCP and REST endpoints. It is compiled into the gateway
//	binary as an embedded static asset bundle (`embed.FS`) so there is no
//	separate deployment target." TESTING.md §12.1 rule 3: "Every spec
//	section referenced under `spec/**.md` headings has at least one entry in
//	the spec map. Exceptions are listed in `tests/spec-map-exceptions.yaml`
//	with a justification."
//
// diagnosis: TESTING.md announces a spec-map exception for a section id
//
//	that tests/spec-map-exceptions.yaml does not list, or records a
//	different reason than the file does. An operator auditing coverage for
//	that section reads the prose, goes looking for the waiver, and finds
//	nothing — or finds a waiver granted for a different reason than the one
//	the doc gives. Either the exception was removed from the file without
//	updating TESTING.md, or the prose named the wrong section id (a chapter
//	id such as `27` where only a subsection such as `27.1` is excepted).
//	Fix the TESTING.md sentence to name the ids and reason the file
//	actually carries.
func TestTestingMdExemptionClaimsResolveToTheExceptionsFile(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	exceptions := readSpecMapExceptions(t, root)

	for _, sentence := range testingMdSentences(t, root) {
		if !strings.Contains(sentence, "spec-map-exceptions.yaml") {
			continue
		}
		lower := strings.ToLower(sentence)
		if !strings.Contains(lower, "exempt") && !strings.Contains(lower, "except") {
			continue
		}
		claimed := specSectionRefRE.FindAllStringSubmatch(sentence, -1)
		if len(claimed) == 0 {
			continue
		}
		// A reason named in the sentence is the reason the doc asserts for
		// every section id it references.
		claimedReason := ""
		for _, reason := range exceptionReasons {
			if strings.Contains(lower, reason) {
				claimedReason = reason
				break
			}
		}
		for _, m := range claimed {
			id := m[1]
			reason, ok := exceptions[id]
			if !ok {
				t.Errorf("TESTING.md claims tests/spec-map-exceptions.yaml excepts §spec/%s, "+
					"but the file has no entry for that section id.\nSentence: %s", id, sentence)
				continue
			}
			if claimedReason != "" && reason != claimedReason {
				t.Errorf("TESTING.md says §spec/%s is excepted as %q, but "+
					"tests/spec-map-exceptions.yaml records reason %q.\nSentence: %s",
					id, claimedReason, reason, sentence)
			}
		}
	}
}

// spec: §27.2 (web playground — placement and gating): "It is compiled into
//
//	the gateway binary as an embedded static asset bundle (`embed.FS`) so
//	there is no separate deployment target." TESTING.md §22.2 (spec
//	coverage): "Every spec section with behavior is mapped to at least one
//	test."
//
// diagnosis: a TESTING.md sentence describes a spec section's test surface
//
//	as arriving with a feature that has not shipped, while tests/spec-map.json
//	already records tests for that section. The doc understates delivered
//	coverage: a reader planning work sees a section as pending and either
//	duplicates the existing tests or skips auditing a surface that is live.
//	Either the deferral wording outlived the feature (rewrite the sentence
//	in the present tense) or the spec map records tests for a section that
//	is genuinely unbuilt (fix the map).
func TestTestingMdDoesNotDescribeCoveredSpecSectionsAsUnbuilt(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	counts := readSpecMapTestCounts(t, root)

	for _, sentence := range testingMdSentences(t, root) {
		lower := strings.ToLower(sentence)
		phrase := ""
		for _, candidate := range deferralPhrases {
			if strings.Contains(lower, candidate) {
				phrase = candidate
				break
			}
		}
		if phrase == "" {
			continue
		}
		var covered []string
		for _, m := range specSectionRefRE.FindAllStringSubmatch(sentence, -1) {
			if counts[m[1]] > 0 {
				covered = append(covered, m[1])
			}
		}
		if len(covered) == 0 {
			continue
		}
		sort.Strings(covered)
		t.Errorf("TESTING.md describes §spec/%s as awaiting delivery (%q), but "+
			"tests/spec-map.json already records tests for each.\nSentence: %s",
			strings.Join(covered, ", §spec/"), phrase, sentence)
	}
}
