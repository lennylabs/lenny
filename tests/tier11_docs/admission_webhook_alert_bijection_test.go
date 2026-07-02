// SPDX-License-Identifier: MIT

// Tier-11 spec/doc-consistency checks for the admission-webhook plane:
// the §13.2 admission-webhook NetworkPolicy row and the §17.2
// high-availability paragraph must agree on which admission-webhook
// Deployments carry the additive `lenny.dev/webhook-name` egress label,
// the §13.2 "acknowledged as a labeled admission-webhook Deployment in
// §17.2" cross-reference must resolve to an antecedent sentence in
// §17.2, and every webhook Deployment enumerated in the §17.2 line-56
// `replicas: 2` SLO enumeration must have a 1:1 per-webhook
// `*Unavailable` alert in the §16.5 catalog. These invariants were
// reconciled by proposal 0022 (F-13.2.24 / SEC-WEBHOOK-1) and have no
// existing tier-11 coverage. No external infrastructure required.

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/alerting/rules"
)

// sectionBounds names the two spec sections this test parses and the
// heading markers that delimit them. Bounding by the next same-level
// heading keeps the extraction robust against line-number drift.
type sectionBounds struct {
	file      string
	startMark string
	endMark   string
}

var (
	section132 = sectionBounds{
		file:      "spec/13_security-model.md",
		startMark: "### 13.2 Network Isolation",
		endMark:   "### 13.3 Credential Flow",
	}
	section172 = sectionBounds{
		file:      "spec/17_deployment-topology.md",
		startMark: "### 17.2 Namespace Layout",
		endMark:   "### 17.3 Disaster Recovery",
	}
)

// readSection returns the text of the spec section delimited by its
// start and end heading markers.
func readSection(t testing.TB, root string, s sectionBounds) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, s.file))
	if err != nil {
		t.Fatalf("read %s: %v", s.file, err)
	}
	content := string(b)
	start := strings.Index(content, s.startMark)
	if start < 0 {
		t.Fatalf("%s: start heading %q not found", s.file, s.startMark)
	}
	rest := content[start+len(s.startMark):]
	end := strings.Index(rest, s.endMark)
	if end < 0 {
		t.Fatalf("%s: end heading %q not found after %q", s.file, s.endMark, s.startMark)
	}
	return rest[:end]
}

// webhookNameLabelRe matches an additive per-webhook egress label
// value: `lenny.dev/webhook-name: <value>` (the NET-068 additive key).
// The two spec sections carry it both as a label assignment
// (`lenny.dev/webhook-name: drain-readiness`) in prose; capturing the
// value lets the test compare the two sections' carrier sets.
var webhookNameLabelRe = regexp.MustCompile(`lenny\.dev/webhook-name:\s*` + "`?" + `([a-z0-9-]+)` + "`?")

// additiveLabelCarriers extracts the distinct set of additive
// `lenny.dev/webhook-name` label values named in a section. The value is
// the webhook-name suffix (e.g. "drain-readiness"); the corresponding
// Deployment carries the `lenny-` prefix.
func additiveLabelCarriers(sectionText string) map[string]bool {
	carriers := map[string]bool{}
	for _, m := range webhookNameLabelRe.FindAllStringSubmatch(sectionText, -1) {
		carriers[m[1]] = true
	}
	return carriers
}

// TestAdmissionWebhookAdditiveLabelCarriersAgree asserts §13.2 and
// §17.2 name the same additive-label carrier set: exactly the two
// gateway-probing webhooks `drain-readiness` and
// `sandboxtemplate-deletion-guard`. The remaining webhooks carry no
// `lenny.dev/webhook-name` label and are in-process validators.
//
// diagnosis: A failure means the §13.2 admission-webhook NetworkPolicy
// row and the §17.2 HA paragraph have drifted on the additive-label
// (`lenny.dev/webhook-name`) carrier set — one section grants a
// per-webhook gateway-egress label to a webhook the other does not, so
// the chart's NET-068 additive-label affordance no longer matches both
// spec surfaces. Either a carrier was added to one section without the
// other, or a carrier was removed on one side only.
//
// spec: 13.2 (admission-webhook NetworkPolicy row, additive-label
// carrier set), 17.2 (HA paragraph additive-label sentences), 16.5
// (per-webhook Unavailable alerts)
func TestAdmissionWebhookAdditiveLabelCarriersAgree(t *testing.T) {
	root := repoRoot(t)
	text132 := readSection(t, root, section132)
	text172 := readSection(t, root, section172)

	// The additive-label affordance (NET-068) narrows gateway egress to
	// the two webhooks that probe the gateway internal port: the
	// drain-readiness pre-drain callback (sub-rule (b)) and the
	// sandboxtemplate-deletion-guard runtime-upgrade probe (sub-rule
	// (c), added by proposal 0022). No other webhook may carry the
	// label; §17.2's "MUST NOT add per-webhook labels to any webhook
	// other than lenny-drain-readiness and
	// lenny-sandboxtemplate-deletion-guard" guard forbids it.
	want := map[string]bool{
		"drain-readiness":                true,
		"sandboxtemplate-deletion-guard": true,
	}

	got132 := additiveLabelCarriers(text132)
	got172 := additiveLabelCarriers(text172)

	if diff := carrierSetDiff(want, got132); diff != "" {
		t.Errorf("§13.2 additive-label carrier set != expected: %s", diff)
	}
	if diff := carrierSetDiff(want, got172); diff != "" {
		t.Errorf("§17.2 additive-label carrier set != expected: %s", diff)
	}
	if diff := carrierSetDiff(got132, got172); diff != "" {
		t.Errorf("§13.2 and §17.2 disagree on the additive-label carrier set: %s", diff)
	}
}

// carrierSetDiff returns a human-readable diff string when two carrier
// sets differ, or "" when they match.
func carrierSetDiff(want, got map[string]bool) string {
	var missing, extra []string
	for k := range want {
		if !got[k] {
			missing = append(missing, k)
		}
	}
	for k := range got {
		if !want[k] {
			extra = append(extra, k)
		}
	}
	if len(missing) == 0 && len(extra) == 0 {
		return ""
	}
	sort.Strings(missing)
	sort.Strings(extra)
	var b strings.Builder
	if len(missing) > 0 {
		b.WriteString("missing " + strings.Join(missing, ", "))
	}
	if len(extra) > 0 {
		if b.Len() > 0 {
			b.WriteString("; ")
		}
		b.WriteString("unexpected " + strings.Join(extra, ", "))
	}
	return b.String()
}

// TestDeletionGuardCrossReferenceResolves asserts the §13.2
// "acknowledged as a labeled admission-webhook Deployment in §17.2"
// cross-reference has a resolving antecedent in §17.2: §13.2 contains
// the phrase referring the reader to §17.2, and §17.2 contains a
// sentence that acknowledges `lenny-sandboxtemplate-deletion-guard` as
// a labeled admission-webhook Deployment (the additive-label / HA
// sentence added on application of proposal 0022).
//
// diagnosis: A failure means the §13.2 cross-reference to §17.2 for the
// sandboxtemplate-deletion-guard is dangling — either §13.2 dropped the
// "acknowledged as a labeled admission-webhook Deployment in §17.2"
// phrase, or §17.2 dropped the antecedent sentence naming
// lenny-sandboxtemplate-deletion-guard with its additive
// `lenny.dev/webhook-name` label. The reader following the §13.2 row to
// §17.2 would find no acknowledgement of the deletion-guard.
//
// spec: 13.2 (cross-reference phrase), 17.2 (antecedent additive-label
// sentence for lenny-sandboxtemplate-deletion-guard), 16.5
func TestDeletionGuardCrossReferenceResolves(t *testing.T) {
	root := repoRoot(t)
	text132 := readSection(t, root, section132)
	text172 := readSection(t, root, section172)

	// (b1) §13.2 carries the cross-reference phrase pointing at §17.2.
	const crossRef = "acknowledged as a labeled admission-webhook Deployment in"
	if !strings.Contains(text132, crossRef) {
		t.Errorf("§13.2 is missing the deletion-guard cross-reference phrase %q", crossRef)
	}
	// The phrase must name the deletion-guard and target §17.2's anchor.
	if !strings.Contains(text132, "lenny-sandboxtemplate-deletion-guard") {
		t.Errorf("§13.2 does not name lenny-sandboxtemplate-deletion-guard in the admission-webhook row")
	}

	// (b2) §17.2 carries the antecedent sentence: it acknowledges
	// lenny-sandboxtemplate-deletion-guard as a labeled admission-webhook
	// Deployment carrying the additive per-webhook label. The sentence
	// added on application states the deletion-guard "carries the additive
	// per-webhook label lenny.dev/webhook-name: sandboxtemplate-deletion-guard".
	if !strings.Contains(text172, "lenny-sandboxtemplate-deletion-guard") {
		t.Fatalf("§17.2 does not name lenny-sandboxtemplate-deletion-guard; the §13.2 cross-reference has no antecedent")
	}
	if !additiveLabelCarriers(text172)["sandboxtemplate-deletion-guard"] {
		t.Errorf("§17.2 does not acknowledge lenny-sandboxtemplate-deletion-guard as a lenny.dev/webhook-name label carrier; the §13.2 cross-reference antecedent (the additive-label sentence) is missing")
	}
}

// upJobRe extracts the `job="<name>"` scrape-target label from a
// per-webhook `up{job="..."} == 0` alert expression. The §17.2 line-56
// enumeration lists webhook Deployment names; each maps to its §16.5
// alert through this job label.
var upJobRe = regexp.MustCompile(`up\{job="([a-z0-9-]+)"\}`)

// replicas2EnumRe extracts the parenthesized Deployment enumeration
// that follows "on the Deployment backing each webhook Service" in the
// §17.2 line-56 `replicas: 2` sentence. That parenthesized list is the
// antecedent the SLO / 1:1-alert clauses quantify over.
var replicas2EnumRe = regexp.MustCompile(
	`on the Deployment backing each webhook Service \(([^)]*)\)`,
)

// backtickedNameRe extracts a backticked `lenny-<name>` webhook
// Deployment name from the enumeration.
var backtickedNameRe = regexp.MustCompile("`(lenny-[a-z0-9-]+)`")

// TestReplicas2EnumerationHasMatchingAlert asserts every webhook
// Deployment named in the §17.2 line-56 `replicas: 2` enumeration has a
// matching per-webhook `up{job="<name>"} == 0` alert in the §16.5
// catalog (rules.Catalog) and is named in the §17.2 SLO clause's alert
// enumeration. The three baseline fail-closed webhooks the spec keeps
// out of the SLO/alert bijection are excepted:
// lenny-sandboxtemplate-deletion-guard, lenny-pod-security, and
// lenny-tenant-label-immutability (proposal 0022, Decision §2). A future
// author who adds one of those (or another baseline webhook) to the
// line-56 enumeration without a matching §16.5 alert is caught.
//
// diagnosis: A failure means a webhook was added to the §17.2 line-56
// `replicas: 2` SLO enumeration (the antecedent the "1:1 alert in this
// list" clause quantifies over) without a matching per-webhook
// `*Unavailable` alert in the §16.5 catalog / rules.Catalog(), so §17.2
// now asserts a per-webhook alert that does not exist — or the alert was
// removed from rules.Catalog while the enumeration still names it. The
// three baseline webhooks are excepted; adding one to the enumeration
// without excepting it here (and without a real §16.5 alert) is the
// exact drift proposal 0022 Decision §2 guards against.
//
// spec: 17.2 (line-56 replicas:2 SLO enumeration and 1:1-alert clause),
// 16.5 (per-webhook Unavailable alerts; up{job="<deployment>"} == 0),
// 13.2
func TestReplicas2EnumerationHasMatchingAlert(t *testing.T) {
	root := repoRoot(t)
	text172 := readSection(t, root, section172)

	// baselineExceptions are the fail-closed webhooks the spec
	// deliberately keeps out of the SLO/1:1-alert bijection: they carry
	// no dedicated per-webhook §16.5 alert. If a future edit adds one to
	// the line-56 enumeration, the test fails unless a real alert also
	// lands (dropping it from this set). proposal 0022 Decision §2.
	baselineExceptions := map[string]bool{
		"lenny-sandboxtemplate-deletion-guard": true,
		"lenny-pod-security":                   true,
		"lenny-tenant-label-immutability":      true,
	}

	// Extract the parenthesized Deployment enumeration from the line-56
	// replicas: 2 sentence.
	enumMatch := replicas2EnumRe.FindStringSubmatch(text172)
	if enumMatch == nil {
		t.Fatalf("§17.2 replicas: 2 Deployment enumeration not found; the line-56 antecedent for the SLO/1:1-alert clauses has moved or changed wording")
	}
	names := backtickedNameRe.FindAllStringSubmatch(enumMatch[1], -1)
	if len(names) == 0 {
		t.Fatalf("§17.2 replicas: 2 enumeration parsed no webhook Deployment names from %q", enumMatch[1])
	}

	// Build the set of per-webhook alert scrape targets from the §16.5
	// catalog: the `job="<name>"` label of every `up{...} == 0`
	// expression. The webhook enumeration names must be a subset (minus
	// the baseline exceptions).
	alertJobs := map[string]bool{}
	for _, r := range rules.Catalog() {
		for _, m := range upJobRe.FindAllStringSubmatch(r.Expr, -1) {
			alertJobs[m[1]] = true
		}
	}

	for _, m := range names {
		name := m[1]
		// crd-conversion is a CRD conversion webhook, not a validating
		// webhook, but §17.2 line 56 includes it in the enumeration and
		// §16.5 defines CrdConversionWebhookUnavailable with
		// up{job="lenny-crd-conversion"}, so it participates in the
		// bijection like the validating webhooks.
		if baselineExceptions[name] {
			// A baseline webhook must NOT appear in the SLO/alert
			// enumeration unless it gains a real §16.5 alert. If one is
			// present here AND has no alert, the exception silently
			// swallows a real drift, so require the alert to be absent to
			// justify the exception. If a future edit adds a real alert,
			// drop the row from baselineExceptions.
			if alertJobs[name] {
				t.Errorf("%s is in baselineExceptions but now has a §16.5 up{job=%q} alert; remove it from baselineExceptions (it is a real 1:1 alert now)", name, name)
			} else {
				t.Errorf("%s is listed in the §17.2 line-56 replicas: 2 SLO enumeration but has no per-webhook §16.5 alert (up{job=%q}); a baseline fail-closed webhook must not be in the SLO/1:1-alert enumeration without a matching alert (proposal 0022 Decision §2)", name, name)
			}
			continue
		}
		if !alertJobs[name] {
			t.Errorf("§17.2 line-56 SLO enumeration names webhook Deployment %q with no matching per-webhook §16.5 alert (no up{job=%q} in rules.Catalog); add the alert or remove the webhook from the enumeration", name, name)
		}
	}
}

// TestBijectionDriftDetection exercises the drift-detection logic against
// synthetic mutated spec text to confirm each of the three assertions
// would fail against a drifted spec (rather than merely line-covering the
// pass path). Feeding the real spec through the same helpers is done by
// the three tests above; here the helpers are fed the failure inputs the
// tests are meant to catch, so the assertions are proven to have teeth.
//
// diagnosis: A failure here means the parsing or diff helpers no longer
// detect the drift they are meant to catch (a carrier added on one side,
// a webhook enumerated without a §16.5 alert), so the three
// spec-consistency tests above would silently pass on a drifted spec.
//
// spec: 13.2, 17.2, 16.5
func TestBijectionDriftDetection(t *testing.T) {
	// (a) carrier drift: a section that grants the additive label to a
	// third webhook must diverge from the expected two-carrier set.
	drifted := "The `lenny-drain-readiness` Deployment carries `lenny.dev/webhook-name: drain-readiness`. " +
		"The `lenny-sandboxtemplate-deletion-guard` Deployment carries `lenny.dev/webhook-name: sandboxtemplate-deletion-guard`. " +
		"A stray edit adds `lenny.dev/webhook-name: pool-config-validator`."
	want := map[string]bool{"drain-readiness": true, "sandboxtemplate-deletion-guard": true}
	if diff := carrierSetDiff(want, additiveLabelCarriers(drifted)); diff == "" {
		t.Errorf("carrier drift went undetected: a third additive-label carrier did not diff against the expected set")
	}
	// A section missing a carrier must also diff.
	missing := "The `lenny-drain-readiness` Deployment carries `lenny.dev/webhook-name: drain-readiness`."
	if diff := carrierSetDiff(want, additiveLabelCarriers(missing)); diff == "" {
		t.Errorf("missing-carrier drift went undetected: dropping sandboxtemplate-deletion-guard did not diff against the expected set")
	}

	// (b) cross-reference antecedent drift: §17.2 text that names the
	// deletion-guard but drops its additive label must fail the
	// antecedent check (the additive-label sentence is the antecedent).
	noAntecedent := "The `lenny-sandboxtemplate-deletion-guard` Deployment is a fail-closed webhook."
	if additiveLabelCarriers(noAntecedent)["sandboxtemplate-deletion-guard"] {
		t.Errorf("cross-reference antecedent drift went undetected: §17.2 text without the additive-label sentence still resolved the deletion-guard carrier")
	}

	// (c) enumeration/alert bijection drift: an enumeration that adds a
	// webhook with no matching alert must be caught. Simulate the
	// enumeration parse plus the alert-job lookup a drifted spec would
	// produce.
	driftedEnum := "on the Deployment backing each webhook Service (`lenny-drain-readiness`, `lenny-nonexistent-webhook`)"
	m := replicas2EnumRe.FindStringSubmatch(driftedEnum)
	if m == nil {
		t.Fatalf("drifted enumeration did not parse")
	}
	alertJobs := map[string]bool{}
	for _, r := range rules.Catalog() {
		for _, mm := range upJobRe.FindAllStringSubmatch(r.Expr, -1) {
			alertJobs[mm[1]] = true
		}
	}
	sawUnmatched := false
	for _, nm := range backtickedNameRe.FindAllStringSubmatch(m[1], -1) {
		if !alertJobs[nm[1]] {
			sawUnmatched = true
		}
	}
	if !sawUnmatched {
		t.Errorf("enumeration/alert bijection drift went undetected: a webhook with no §16.5 alert did not register as unmatched against rules.Catalog")
	}
}
