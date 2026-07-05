// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency checks for the proposal 0024 direct-mode
// usage reconciliation (F-15.3.7 / F-11.2.20). Three genuine spec defects
// the proposal repaired are pinned here so a later spec or doc edit cannot
// silently reintroduce them:
//
//   - The ReportUsage-direction inconsistency: the proto puts ReportUsage on
//     `service Adapter` (the gateway is the caller), while the pre-fix §11.2
//     and §6.2 prose described an adapter push. S2 reconciled every
//     adapter-direction surface to the gateway-pull model, keeping the
//     terminal FINAL_USAGE_REPORT as the one adapter push in its §8.3
//     settlement role.
//   - The four dangling §7.3 citations: the pod-reported cumulative-usage
//     re-report on reconnection was cited to §7.3 at four sites, but §7.3
//     defines only retry and resume. S2 re-pointed all four to §4.7, where
//     ReportUsage and the adapter's cumulative accumulation are defined.
//   - The §16.1.1-forbidden `session_id` label: the pre-fix §11.2 prose
//     labeled the anomaly metric by `session_id`, which §16.1.1 forbids as a
//     Prometheus label. S6 relabeled it `{tenant_id, reason}`.
//
// It also pins the focused TokenUsageAnomaly alert→runbook resolution (the
// per-alert regression on top of the catalog-wide cross-checks in
// runbooks_test.go) and the docs/reference/metrics.md operator mirror.
//
// These tests read the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: §4.7 (ReportUsage is a gateway pull; the adapter cumulative-total
// re-report on reconnection), §11.2 (direct-mode gateway-pull usage,
// {tenant_id, reason} label), §6.2 (idle reset on a non-zero pulled delta),
// §8.3 (FINAL_USAGE_REPORT terminal settlement push), §16.1.1 (forbidden
// session_id label), §16.5 (TokenUsageAnomaly alert).

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/alerting/rules"
)

// spec: 11.2, 6.2, 8.3, 4.7
// diagnosis: the direct-mode ReportUsage-direction prose drifted back to an
// adapter-push model, contradicting the proto (ReportUsage is on `service
// Adapter`, so the gateway is the caller) and the reconciled §4.7/§6.2/§8.3
// surfaces. Proposal 0024 S2 reconciled every adapter-direction surface to
// the gateway-pull model: the adapter accumulates a per-session cumulative
// total from the llm_request_completed frame, and the gateway pulls the
// incremental delta over ReportUsage. A failure here means one surface
// reverted to describing an adapter that pushes ReportUsage, leaving the
// applied spec self-contradictory about who drives the call — the exact
// direction defect S2 closed. The only surviving adapter push is the
// terminal FINAL_USAGE_REPORT in its §8.3 settlement role.
func TestSpecReportUsageDirectionIsGatewayPull_F15_3_7(t *testing.T) {
	root := repoRoot(t)
	specDir := filepath.Join(root, "spec")

	s112 := specSection(t, filepath.Join(specDir, "11_policy-and-controls.md"), "### 11.2 ")
	s62 := specSection(t, filepath.Join(specDir, "06_warm-pod-model.md"), "### 6.2 ")
	s83 := specSection(t, filepath.Join(specDir, "08_recursive-delegation.md"), "### 8.3 ")

	// §11.2 direct-mode bullet: the gateway pulls the incremental delta over
	// ReportUsage; the adapter accumulates from the llm_request_completed
	// frame. It must not describe the adapter reporting usage to the gateway.
	requireAllContain(t, "§11.2 direct-mode ReportUsage direction", s112, []string{
		"The gateway pulls the incremental delta",
		"over the `ReportUsage` RPC",
		"accumulates a per-session cumulative total from the `llm_request_completed`",
	})
	requireNoneContain(t, "§11.2 direct-mode ReportUsage direction", s112, []string{
		"reports them to the gateway via the `ReportUsage`",
		"adapter reports them to the gateway",
	})

	// §6.2 direct-mode idle bullet: the idle clock resets on a gateway
	// ReportUsage pull that returns a non-zero token delta, not on an
	// adapter-initiated ReportUsage call whose cadence the adapter owns.
	requireAllContain(t, "§6.2 direct-mode idle reset", s62, []string{
		"Each gateway `ReportUsage` pull that returns a non-zero token delta",
		"a zero-delta pull is not evidence of activity and leaves the clock untouched",
	})
	requireNoneContain(t, "§6.2 direct-mode idle reset", s62, []string{
		"Each `ReportUsage` gRPC call received from the adapter",
	})

	// §8.3: the over-run cadence is bounded against the gateway ReportUsage
	// poll interval, and the terminal FINAL_USAGE_REPORT is the one adapter
	// push, sent once in-flight gateway pulls have settled.
	requireAllContain(t, "§8.3 over-run cadence and terminal settlement", s83, []string{
		"the next gateway `ReportUsage` poll interval",
		"after all in-flight gateway `ReportUsage` pulls have settled",
	})
}

// spec: 11.2, 12, 4.7, 7.3
// diagnosis: a spec citation for the pod-reported cumulative-usage re-report
// on reconnection still points at §7.3, which defines only retry and resume
// and does not define the re-report mechanism. Proposal 0024 S2 re-pointed
// all four dangling citations (§11.2 crash-recovery MAX rule, §11.2
// in-memory-buffer reconstruction, §11.2 GATEWAY_CRASH_RECONSTRUCTION, and
// the §12 storage-durability row) to §4.7, where ReportUsage and the
// adapter's cumulative accumulation are defined. A failure here means a
// citation drifted back to §7.3, dangling again, or §7.3 was made to home
// the mechanism (which the resolution deliberately did not do). The §7.3
// citations for the resume-window parameters are correct and must stay.
func TestSpecPodReportedCumulativeCitesSection47_F11_2_20(t *testing.T) {
	root := repoRoot(t)
	specDir := filepath.Join(root, "spec")

	pol, err := os.ReadFile(filepath.Join(specDir, "11_policy-and-controls.md"))
	if err != nil {
		t.Fatalf("read §11: %v", err)
	}
	storage, err := os.ReadFile(filepath.Join(specDir, "12_storage-architecture.md"))
	if err != nil {
		t.Fatalf("read §12: %v", err)
	}
	sess, err := os.ReadFile(filepath.Join(specDir, "07_session-lifecycle.md"))
	if err != nil {
		t.Fatalf("read §7: %v", err)
	}

	// Each pod-reported cumulative-usage re-report sentence must cite §4.7 in
	// the same line, not §7.3. Scan line-by-line so the assertion is anchored
	// to the mechanism sentence rather than the whole file.
	type site struct {
		body    []byte
		file    string
		marker  string // a stable phrase in the mechanism sentence
		wantRef string
		banRef  string
	}
	sites := []site{
		{pol, "§11.2 crash-recovery MAX rule", "re-reported on reconnection to a new gateway replica", "04_system-components.md", "07_session-lifecycle.md"},
		{pol, "§11.2 in-memory-buffer reconstruction", "reconstructed from pod-reported token usage during session recovery", "04_system-components.md", "07_session-lifecycle.md"},
		{pol, "§11.2 GATEWAY_CRASH_RECONSTRUCTION", "reconciles billing from pod-reported token totals", "04_system-components.md", "07_session-lifecycle.md"},
		{storage, "§12 storage-durability row", "In-memory buffer events reconstructed from pod-reported token usage", "04_system-components.md", "07_session-lifecycle.md"},
	}
	for _, s := range sites {
		line := lineContaining(string(s.body), s.marker)
		if line == "" {
			t.Errorf("%s: the mechanism sentence %q was not found (reworded or removed?)", s.file, s.marker)
			continue
		}
		if !strings.Contains(line, s.wantRef) {
			t.Errorf("%s: the pod-reported cumulative-usage sentence does not cite %s (must resolve to §4.7 where the mechanism is defined): %q", s.file, s.wantRef, line)
		}
		if strings.Contains(line, s.banRef) {
			t.Errorf("%s: the pod-reported cumulative-usage sentence still cites §7.3 (%s), which defines only retry and resume: %q", s.file, s.banRef, line)
		}
	}

	// §7.3 must not have been made to home the re-report mechanism: the
	// resolution re-pointed the citations rather than moving the mechanism
	// into §7.3, so §7.3 carries none of the pod-reported cumulative phrasing.
	s73 := section(string(sess), "### 7.3 ")
	if s73 == "" {
		t.Fatal("§7.3 heading not found")
	}
	for _, banned := range []string{"pod-reported", "cumulative usage total", "re-reported on reconnection", "reconstructed from pod-reported"} {
		if strings.Contains(s73, banned) {
			t.Errorf("§7.3 now homes the pod-reported cumulative-usage mechanism (%q); the resolution re-points citations to §4.7 rather than moving the mechanism into §7.3", banned)
		}
	}

	// The §7.3 resume-window parameter citations are correct and must stay
	// pointed at §7.3 (the anti-over-correction guard).
	if !strings.Contains(string(pol), "maxResumeWindowSeconds") {
		t.Error("§11.2 lost the maxResumeWindowSeconds row; the resume-window §7.3 citations must remain")
	}
}

// spec: 11.2, 16.1.1
// diagnosis: a spec section labels the direct-mode token-usage anomaly metric
// by `session_id`, which §16.1.1 forbids as a high-cardinality Prometheus
// label (CI-enforced by the metrics-lint denylist). The pre-fix §11.2 prose
// labeled the metric "by `session_id` and `tenant_id`"; proposal 0024 S6
// relabeled it `{tenant_id, reason}` with per-session attribution in
// structured logs. A failure here means a spec section reintroduced the
// forbidden label on this metric, which the metrics validator would reject
// at runtime.
func TestSpecAnomalyMetricNotLabeledBySessionID_F11_2_20(t *testing.T) {
	root := repoRoot(t)
	specDir := filepath.Join(root, "spec")

	const metric = "lenny_gateway_token_usage_anomaly_total"
	for _, f := range []string{"11_policy-and-controls.md", "16_observability.md"} {
		body, err := os.ReadFile(filepath.Join(specDir, f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, line := range strings.Split(string(body), "\n") {
			if !strings.Contains(line, metric) {
				continue
			}
			// The metric line must name the compliant labels and must not
			// label the metric by session_id.
			if strings.Contains(line, "session_id") && !strings.Contains(line, "intentionally excluded") && !strings.Contains(line, "forbidden-label") {
				t.Errorf("%s: the %s line labels the metric by session_id, which §16.1.1 forbids: %q", f, metric, line)
			}
			if !strings.Contains(line, "tenant_id") {
				t.Errorf("%s: the %s line does not name the compliant `tenant_id` label: %q", f, metric, line)
			}
		}
	}
}

// spec: 16.5, 11.2, 17.7
// diagnosis: the TokenUsageAnomaly warning alert (the §11.2 direct-mode
// under-reporting integrity control) does not resolve to its on-disk runbook.
// Pre-0024 the alert did not exist in the single-source code catalog and the
// metric was emitted nowhere (F-11.2.20). This is the focused per-alert
// regression on top of the catalog-wide cross-checks
// (TestAlertCatalogRunbookSlugsResolveToDocs,
// TestRunbookTriggersResolveToAlertCatalog): it pins the specific
// alert→runbook mapping so a drift in either the alert's RunbookURL slug or
// the runbook filename surfaces here with the right diagnosis. A failure
// means an operator following the runbook_url annotation reaches a broken
// link.
func TestTokenUsageAnomalyResolvesToRunbook_spec_16_5(t *testing.T) {
	const (
		alertName = "TokenUsageAnomaly"
		wantSlug  = "token-usage-anomaly"
	)
	var got rules.Rule
	for _, r := range rules.Catalog() {
		if r.Name == alertName {
			got = r
			break
		}
	}
	if got.Name == "" {
		t.Fatalf("%s missing from the §16.5 alert catalog (F-11.2.20 regression)", alertName)
	}
	if slug := got.RunbookSlug(); slug != wantSlug {
		t.Fatalf("%s runbook slug = %q, want %q", alertName, slug, wantSlug)
	}

	root := repoRoot(t)
	path := filepath.Join(root, "docs", "runbooks", wantSlug+".md")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s runbook does not resolve to %s: %v", alertName, path, err)
	}
	// The runbook's front matter must name the alert back so the reverse
	// trigger→alert cross-check holds and the /v1/admin/runbooks index maps
	// the alert to this runbook.
	if !strings.Contains(string(body), "alert: "+alertName) {
		t.Errorf("%s front matter does not declare a trigger for %q", path, alertName)
	}
}

// spec: 16.1, 16.5
// diagnosis: the hand-authored operator reference docs/reference/metrics.md
// claims completeness for every emitted metric and alert but omits the new
// direct-mode pair. Proposal 0024 S11 mirrored the anomaly metric and the
// TokenUsageAnomaly alert into the reference. A failure here means the
// operator reference contradicts the emitted surface: an operator looking up
// the metric or alert in the reference does not find it.
func TestMetricsReferenceCarriesAnomalyPair_F11_2_20(t *testing.T) {
	root := repoRoot(t)
	ref := readDoc(t, filepath.Join(root, "docs", "reference", "metrics.md"))
	requireAllContain(t, "docs/reference/metrics.md", ref, []string{
		"lenny_gateway_token_usage_anomaly_total",
		"TokenUsageAnomaly",
		"zero_delta",
		"implausibly_small",
	})
}
