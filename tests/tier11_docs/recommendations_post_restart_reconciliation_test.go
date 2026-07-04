// SPDX-License-Identifier: MIT

// Tier-11 documentation / spec-consistency checks for proposal 0029
// (F-25.3.16): §25.3's self-contradictory post-restart recommendations
// contract is reconciled to the response-level §25.2 degradation envelope.
//
// Before this reconciliation §25.3 gave two opposite prescriptions for the
// identical post-restart empty-window state: the Sliding-window paragraph
// required a per-item `"confidence": 0.0` / `"dataAvailable": false` entry,
// while the Degradation subsection forbade the entry ("No recommendations
// are generated for categories with insufficient data"). Three further
// passages (§25.4 Prometheus-requirement bullet, §25.4 operational-
// consequences row, §25.15 failure-mode row) promised a surfaced signal
// phrased as `confidence: 0.0` "for hours after any restart". The
// reconciliation keeps `recommendations[]` empty and surfaces the starved
// state through the canonical §25.2 `degradation` envelope at
// `"level": "degraded"`, which returns to `"healthy"` as soon as any rule
// records a sample.
//
// Each assertion derives the reconciled substring from the applied spec
// text and fails against the pre-fix wording, so a future regression that
// reintroduces the `confidence: 0.0` reading, a per-item degraded entry, or
// a multi-hour degraded duration is caught.
//
// These tests are NOT under a build tag: they read the repository state
// directly and need no external infrastructure.

package tier11_docs_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// opsSpecPath is spec/25_agent-operability.md.
func opsSpecPath(root string) string {
	return filepath.Join(root, "spec", "25_agent-operability.md")
}

// slidingWindowParagraph returns the §25.3 "Sliding window aggregation"
// paragraph, a single source line, or "" if not found.
func slidingWindowParagraph(body string) string {
	for _, ln := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "**Sliding window aggregation.**") {
			return ln
		}
	}
	return ""
}

// omittedWhenHealthyClause returns the §25.2 "Omitted when healthy" clause,
// a single source line, or "" if not found.
func omittedWhenHealthyClause(body string) string {
	for _, ln := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "**Omitted when healthy.**") {
			return ln
		}
	}
	return ""
}

// envelopeTriggerSentence returns the §25.2 Canonical Degradation Envelope
// trigger sentence (the line that opens with "Any response whose data
// quality depends"), or "" if not found.
func envelopeTriggerSentence(body string) string {
	for _, ln := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "Any response whose data quality depends") {
			return ln
		}
	}
	return ""
}

// degradationSubsection returns the §25.3 Capacity Recommendations
// endpoint's `#### Degradation` subsection. It first narrows to the
// `### Capacity Recommendations` endpoint block (via the shared section()
// helper) and then extracts the `#### Degradation` subsection within it.
// It cannot call section(body, "Degradation") directly: every §25 endpoint
// carries a `#### Degradation` subsection, and the §25.2 heading "Canonical
// Degradation Envelope" also contains the word, so a whole-file search
// returns the wrong block.
func degradationSubsection(body string) string {
	endpoint := section(body, "Capacity Recommendations")
	if endpoint == "" {
		return ""
	}
	lines := strings.Split(endpoint, "\n")
	start := -1
	var startLevel int
	for i, ln := range lines {
		if start < 0 {
			if strings.HasPrefix(ln, "#") && headingText(ln) == "Degradation" {
				start = i
				startLevel = headingLevel(ln)
			}
			continue
		}
		if strings.HasPrefix(ln, "#") && headingLevel(ln) <= startLevel {
			return strings.Join(lines[start:i], "\n")
		}
	}
	if start < 0 {
		return ""
	}
	return strings.Join(lines[start:], "\n")
}

// headingText returns the text of an ATX heading line with the leading '#'
// run and surrounding whitespace stripped.
func headingText(line string) string {
	return strings.TrimSpace(strings.TrimLeft(line, "#"))
}

// spec: 25.3 (post-restart degradation), 25.2 (canonical envelope).
//
// diagnosis: a failure means the §25.3 Sliding-window paragraph or the
// §25.3 Degradation subsection no longer describes an empty
// `recommendations` array with a `degradation` envelope at
// `"level": "degraded"`, or one of them still prescribes the pre-fix
// per-item `"confidence": 0.0` / `"dataAvailable": false` entry. The two
// passages once gave opposite prescriptions for the identical post-restart
// state; if either drifts back, an agent reading the spec cannot tell
// whether an empty recommendations array is starved or healthy.
func TestPostRestartSlidingWindowAndDegradationDescribeDegradedEnvelope(t *testing.T) {
	root := repoRoot(t)
	body := readDoc(t, opsSpecPath(root))

	sliding := slidingWindowParagraph(body)
	if sliding == "" {
		t.Fatal("§25.3: could not find the Sliding window aggregation paragraph")
	}
	// (a) The Sliding-window paragraph describes an empty recommendations
	// array with a degraded envelope while every ring buffer is empty.
	for _, want := range []string{
		"After a gateway restart the ring buffers start empty",
		"no per-category recommendations are generated",
		"`degradation` envelope reports `\"level\": \"degraded\"`",
	} {
		if !strings.Contains(sliding, want) {
			t.Errorf("§25.3 Sliding-window paragraph missing reconciled clause %q", want)
		}
	}
	// The pre-fix per-item reading must be gone from this paragraph.
	if strings.Contains(sliding, "recommendations include `\"confidence\": 0.0` and `\"dataAvailable\": false`") {
		t.Errorf("§25.3 Sliding-window paragraph still prescribes the per-item confidence:0.0/dataAvailable:false entry")
	}

	// The §25.3 Degradation subsection: extract it and assert the same
	// contract. It sits under a #### heading within §25.3.
	deg := degradationSubsection(body)
	if deg == "" {
		t.Fatal("§25.3: could not find the Degradation subsection")
	}
	for _, want := range []string{
		"no per-category recommendations are generated",
		"`degradation` envelope reports `\"level\": \"degraded\"`",
		"The `recommendations` array always carries triggered, actionable entries only.",
	} {
		if !strings.Contains(deg, want) {
			t.Errorf("§25.3 Degradation subsection missing reconciled clause %q", want)
		}
	}
	// The pre-fix "No recommendations are generated for categories with
	// insufficient data" no-entry-only reading (which contradicted the
	// Sliding-window paragraph's per-item entry) must be gone.
	if strings.Contains(deg, "No recommendations are generated for categories with insufficient data") {
		t.Errorf("§25.3 Degradation subsection still carries the pre-fix insufficient-data no-entry wording")
	}
}

// spec: 25.2 (canonical envelope), 25.3 (post-restart degradation).
//
// diagnosis: a failure means the §25.2 envelope trigger sentence or the
// "Omitted when healthy" clause no longer admits an endpoint whose data
// quality depends on in-process history having accumulated (the capacity-
// recommendation ring buffers, empty after a gateway restart). Without the
// widened trigger, the gateway's `"level": "degraded"` post-restart stamp
// contradicts the envelope's stated scope.
func TestEnvelopeTriggerAndOmittedClauseAdmitInsufficientInProcessHistory(t *testing.T) {
	root := repoRoot(t)
	body := readDoc(t, opsSpecPath(root))

	trigger := envelopeTriggerSentence(body)
	if trigger == "" {
		t.Fatal("§25.2: could not find the Canonical Degradation Envelope trigger sentence")
	}
	// (b) The trigger admits in-process history accumulation.
	if !strings.Contains(trigger, "or on in-process history having accumulated") {
		t.Errorf("§25.2 envelope trigger does not admit in-process history accumulation")
	}
	if !strings.Contains(trigger, "which start empty after a gateway restart") {
		t.Errorf("§25.2 envelope trigger does not name the ring buffers starting empty after a restart")
	}

	omitted := omittedWhenHealthyClause(body)
	if omitted == "" {
		t.Fatal("§25.2: could not find the Omitted when healthy clause")
	}
	// The omitted-when-healthy clause admits the degraded-while-no-history
	// case and returns to healthy once any rule records a sample.
	for _, want := range []string{
		"an endpoint whose data quality also depends on in-process history reports `\"level\": \"degraded\"` while it holds no history at all",
		"returns to `\"level\": \"healthy\"` once any of its rules records a sample",
	} {
		if !strings.Contains(omitted, want) {
			t.Errorf("§25.2 Omitted-when-healthy clause missing reconciled clause %q", want)
		}
	}
}

// spec: 25.4 (Prometheus requirement), 25.15 (failure-mode analysis),
// 25.2 (canonical envelope).
//
// diagnosis: a failure means the §25.4 capacity-recommendations bullet, the
// §25.4 operational-consequences row, or the §25.15 failure-mode row no
// longer describes the `degradation.level: "degraded"` envelope, or still
// carries the pre-fix `confidence: 0.0` phrasing. The three passages were
// aligned in lockstep with the §25.3 reconciliation; if one drifts, the
// spec again describes two different post-restart mechanisms.
func TestCrossReferencesDescribeDegradedLevelNotConfidenceZero(t *testing.T) {
	root := repoRoot(t)
	body := readDoc(t, opsSpecPath(root))

	// (c) §25.4 capacity-recommendations bullet.
	bullet := lineContaining(body, "**Capacity recommendations.** Many rules use multi-day sliding windows")
	if bullet == "" {
		t.Fatal("§25.4: could not find the Capacity recommendations Prometheus-requirement bullet")
	}
	if !strings.Contains(bullet, "reports a `degradation` envelope with `\"level\": \"degraded\"` immediately after a restart while every ring buffer is empty") {
		t.Errorf("§25.4 capacity-recommendations bullet does not describe the degraded envelope")
	}

	// (c) §25.4 operational-consequences summary-table Capacity row.
	opsRow := lineContaining(body, "| Capacity recommendations | Aggregate metrics from PromQL")
	if opsRow == "" {
		t.Fatal("§25.4: could not find the operational-consequences Capacity recommendations table row")
	}
	if !strings.Contains(opsRow, "`degradation.level: \"degraded\"` immediately after a restart until the first rule records a sample") {
		t.Errorf("§25.4 operational-consequences row does not describe degradation.level: degraded")
	}

	// (c) §25.15 failure-mode Prometheus-permanently-absent row.
	fmRow := lineContaining(body, "| **Prometheus permanently absent** |")
	if fmRow == "" {
		t.Fatal("§25.15: could not find the Prometheus permanently absent failure-mode row")
	}
	if !strings.Contains(fmRow, "reports `degradation.level: \"degraded\"` immediately after a restart while every ring buffer is empty") {
		t.Errorf("§25.15 Prometheus-permanently-absent row does not describe degradation.level: degraded")
	}
}

// spec: 25.2 (canonical envelope), 25.3 (post-restart degradation),
// 25.4 (Prometheus requirement), 25.15 (failure-mode analysis).
//
// diagnosis: a failure means a §25 surface reintroduced the removed
// `confidence: 0.0` post-restart reading, or described the degraded state
// as persisting "for hours after any restart" or "until the ring buffers
// refill" — a multi-hour duration the C3 implementation never produces (it
// returns to healthy on the first sample of any source metric). Either
// drift makes an agent reading the spec expect a duration or signal the
// gateway does not emit.
func TestNoSurfaceKeepsConfidenceZeroOrMultiHourDegradedDuration(t *testing.T) {
	root := repoRoot(t)
	body := readDoc(t, opsSpecPath(root))

	// (d) No §25 surface says the recommendations response returns
	// `confidence: 0.0` for the post-restart case. The recommendations-
	// specific phrasings that carried it are the ones removed; guard the
	// three that referenced a restart-scoped confidence:0.0.
	for _, forbidden := range []string{
		"recommendations return `confidence: 0.0` for hours after any restart",
		"`confidence: 0.0` after every restart",
		"capacity recommendations return `confidence: 0.0` after every restart",
		"recommendations include `\"confidence\": 0.0` and `\"dataAvailable\": false`",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("§25 still carries the removed post-restart confidence:0.0 phrasing %q", forbidden)
		}
	}

	// (d) No §25 surface describes the degraded state as persisting for
	// hours or until the ring buffers refill.
	for _, forbidden := range []string{
		"for hours after any restart",
		"until the ring buffers refill",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("§25 still describes the degraded state persisting %q; C3 returns to healthy on the first sample", forbidden)
		}
	}
}

// spec: 25.2 (canonical envelope), 25.3 (post-restart degradation),
// 25.4 (Prometheus requirement), 25.15 (failure-mode analysis).
//
// diagnosis: a failure means the §25.2/§25.3/§25.4/§25.15 passages disagree
// about how long the degraded envelope lasts. The reconciled contract is
// that the envelope is degraded only while every ring buffer is empty and
// returns to healthy as soon as any rule records a sample. If a passage
// drops the "returns to healthy on the first sample" predicate, a future
// multi-hour-duration regression would go uncaught.
func TestAllSurfacesAgreeEnvelopeRecoversOnFirstSample(t *testing.T) {
	root := repoRoot(t)
	body := readDoc(t, opsSpecPath(root))

	// (e) Each surface names the empty-window scoping and recovery on the
	// first sample.
	checks := []struct {
		name  string
		line  string
		wants []string
	}{
		{
			name: "§25.3 Sliding-window paragraph",
			line: slidingWindowParagraph(body),
			wants: []string{
				"While every ring buffer is empty",
				"returns to `\"level\": \"healthy\"` as soon as any rule records a sample",
			},
		},
		{
			name: "§25.4 capacity-recommendations bullet",
			line: lineContaining(body, "**Capacity recommendations.** Many rules use multi-day sliding windows"),
			wants: []string{
				"while every ring buffer is empty",
				"returns to `\"level\": \"healthy\"` as soon as any rule records a sample",
			},
		},
		{
			name: "§25.4 operational-consequences row",
			line: lineContaining(body, "| Capacity recommendations | Aggregate metrics from PromQL"),
			wants: []string{
				"until the first rule records a sample, then `\"healthy\"`",
			},
		},
		{
			name: "§25.15 Prometheus-permanently-absent row",
			line: lineContaining(body, "| **Prometheus permanently absent** |"),
			wants: []string{
				"while every ring buffer is empty and returns to `\"healthy\"` once any rule records a sample",
			},
		},
	}
	for _, c := range checks {
		if c.line == "" {
			t.Fatalf("%s: could not locate the passage", c.name)
		}
		for _, want := range c.wants {
			if !strings.Contains(c.line, want) {
				t.Errorf("%s does not agree the envelope recovers on the first sample: missing %q", c.name, want)
			}
		}
	}

	// The §25.3 Degradation subsection carries the healthy-once-any-rule-has-
	// samples predicate too.
	deg := degradationSubsection(body)
	if !strings.Contains(deg, "When at least one rule has samples the envelope reports `\"level\": \"healthy\"`") {
		t.Error("§25.3 Degradation subsection does not state the envelope is healthy once at least one rule has samples")
	}
}

// spec: 25.3 (post-restart degradation), 25.2 (canonical envelope).
//
// diagnosis: a failure means the §25.3 Degradation subsection's cross-
// reference to the §25.2 Canonical Degradation Envelope points at an anchor
// that no heading produces, so the link 404s for a reader following it.
func TestPostRestartDegradationCrossReferenceResolves(t *testing.T) {
	root := repoRoot(t)
	body := readDoc(t, opsSpecPath(root))

	// (f) The §25.3 Degradation subsection links §25.2 via the
	// #canonical-degradation-envelope anchor; confirm the link is present
	// and that a §25 heading produces that slug.
	if !strings.Contains(body, "[Section 25.2](#canonical-degradation-envelope)") {
		t.Error("§25.3 Degradation subsection does not link §25.2 via #canonical-degradation-envelope")
	}
	slugs, err := headingSlugs(opsSpecPath(root))
	if err != nil {
		t.Fatalf("read §25 heading slugs: %v", err)
	}
	if !slugs["canonical-degradation-envelope"] {
		t.Error("§25.3 links to #canonical-degradation-envelope, but no §25 heading produces that slug")
	}
}
