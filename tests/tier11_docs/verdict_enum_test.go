// SPDX-License-Identifier: MIT

// Tier-11 documentation checks that reconcile the verdict document's two
// enumerated fields with the values the harness emits. TESTING.md §7 owns
// the schema of `tests/results/latest.json`, and its field-semantics list
// closes both enums with a sentence that names every accepted value. A CI
// consumer parses `verdict` and `tiers.<name>.status` against those
// sentences, so a value the harness emits and the sentence omits is a
// mis-parse rather than a cosmetic gap.
//
// The authoritative value sets are the exported constants in
// cmd/lenny-test/verdictstatus, which exists as an importable package for
// this test: cmd/lenny-test is a main package and cannot be imported, so
// restating its values here would let the test drift from the producer it
// guards.
//
// These cases carry no `// spec:` annotation. The verdict schema is owned
// by TESTING.md rather than by a numbered section under spec/, and an
// annotation would credit a failure to a spec section that defines no
// verdict document.
//
// These tests are NOT under a build tag: they read TESTING.md directly and
// need no external infrastructure.

package tier11_docs_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/cmd/lenny-test/verdictstatus"
)

// verdictSentenceKey and tierStatusSentenceKey select the two
// field-semantics lines in TESTING.md §7 that close an enum. Each key is
// the field name as the sentence writes it, followed by the phrase that
// opens the enumeration, so the key matches the defining sentence rather
// than any later mention of the same field.
const (
	verdictSentenceKey    = "`verdict` is one of"
	tierStatusSentenceKey = "`tiers.<name>.status` is one of"
)

// missingEnumValues returns the values that sentence does not enumerate.
// A documented value is written in backticks, so each value is matched in
// that form and a bare occurrence elsewhere in the prose does not satisfy
// the check. The function takes the sentence rather than reading the file
// so the same assertion runs against a fixture.
func missingEnumValues(sentence string, values []string) []string {
	var missing []string
	for _, v := range values {
		if !strings.Contains(sentence, "`"+v+"`") {
			missing = append(missing, v)
		}
	}
	return missing
}

// testingDocSentence returns the single TESTING.md line carrying key, and
// fails when no line does.
func testingDocSentence(t *testing.T, key string) string {
	t.Helper()
	body := readRepoFile(t, repoRoot(t), "TESTING.md")
	line := lineContaining(body, key)
	if line == "" {
		t.Fatalf("TESTING.md §7 has no field-semantics sentence containing %q", key)
	}
	return line
}

// TestVerdictSentenceEnumeratesEveryVerdict pins the TESTING.md §7
// `verdict` sentence to the verdict values the harness emits. UNVERIFIED
// was the value the sentence omitted while the harness had begun emitting
// it, and its exit code differs from the INCONCLUSIVE code, so a consumer
// reading only the sentence cannot map the run's exit status.
//
// diagnosis: a failure means the TESTING.md §7 `verdict` field-semantics
// sentence omits a verdict cmd/lenny-test/verdictstatus declares, so a CI
// consumer parsing tests/results/latest.json against the documented enum
// meets a value the documentation does not define.
func TestVerdictSentenceEnumeratesEveryVerdict(t *testing.T) {
	sentence := testingDocSentence(t, verdictSentenceKey)
	if missing := missingEnumValues(sentence, verdictstatus.Verdicts()); len(missing) > 0 {
		t.Errorf("TESTING.md §7 verdict sentence omits emitted verdict(s) %v; the sentence must enumerate every value in cmd/lenny-test/verdictstatus.Verdicts()", missing)
	}
}

// TestTierStatusSentenceEnumeratesEveryStatus pins the TESTING.md §7
// `tiers.<name>.status` sentence to the per-tier statuses the harness
// emits. Beyond the newly added `unverified`, the sentence had never
// carried `inconclusive`, which the harness records for a tier whose
// failure it reclassifies as infrastructure-class.
//
// diagnosis: a failure means the TESTING.md §7 `tiers.<name>.status`
// field-semantics sentence omits a status cmd/lenny-test/verdictstatus
// declares, so the documented per-tier enum understates the states a tier
// entry in tests/results/latest.json can hold.
func TestTierStatusSentenceEnumeratesEveryStatus(t *testing.T) {
	sentence := testingDocSentence(t, tierStatusSentenceKey)
	if missing := missingEnumValues(sentence, verdictstatus.TierStatuses()); len(missing) > 0 {
		t.Errorf("TESTING.md §7 tier-status sentence omits emitted status(es) %v; the sentence must enumerate every value in cmd/lenny-test/verdictstatus.TierStatuses()", missing)
	}
}

// TestEnumReconciliationDetectsAnOmittedValue pins the detection itself.
// A reconciliation test that passes over any sentence would keep passing
// after the documentation drops a value, so the same assertion runs over
// a fixture sentence built from the live enum with one value removed, and
// must report exactly that value.
//
// diagnosis: a failure means missingEnumValues no longer detects an
// omitted enum value, so the two sentence checks above would pass against
// documentation that has dropped a verdict or a tier status.
func TestEnumReconciliationDetectsAnOmittedValue(t *testing.T) {
	cases := []struct {
		name   string
		values []string
	}{
		{"verdict", verdictstatus.Verdicts()},
		{"tier status", verdictstatus.TierStatuses()},
	}
	for _, tc := range cases {
		for _, dropped := range tc.values {
			t.Run(tc.name+"/"+dropped, func(t *testing.T) {
				var kept []string
				for _, v := range tc.values {
					if v != dropped {
						kept = append(kept, "`"+v+"`")
					}
				}
				fixture := "- is one of " + strings.Join(kept, ", ") + "."
				missing := missingEnumValues(fixture, tc.values)
				if len(missing) != 1 || missing[0] != dropped {
					t.Fatalf("fixture omitting %q reported missing %v; want exactly [%s]", dropped, missing, dropped)
				}
			})
		}
	}
}
