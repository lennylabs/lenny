// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §25.13 bundled alerting rules, rendered
// from the shared pkg/alerting/rules catalog, fire under their
// triggering metric conditions when evaluated by the real Prometheus
// rule engine.
//
// A large subset of the §16.5 catalog uses PromQL constructs the
// gateway's in-process fallback (pkg/alerting/inproceval) reports
// unsupported by design — range vectors (rate/increase), the time()
// function, label-set joins (unless on()), and histogram_quantile — so
// those alerts are evaluated only by Prometheus. The in-process tests
// pin the fallback's "leave it to Prometheus" behavior; this test closes
// the other half of the split by asserting the rendered expressions
// actually fire in Prometheus under the documented triggering condition.
//
// It runs `promtool test rules` (the Prometheus rule-unit-test tool,
// which ships in the Prometheus image) against the rendered catalog.
// promtool advances synthetic input series through simulated time, so
// each rule's `for:` sustain window is honored without wall-clock delay,
// and asserts the expected alert fires with its rendered severity label
// and annotations.

package tier4_integration_test

import (
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/lenny/pkg/alerting/rules"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// promtoolDoc is the promtool `test rules` unit-test document.
type promtoolDoc struct {
	RuleFiles []string       `yaml:"rule_files"`
	Tests     []promtoolTest `yaml:"tests"`
}

type promtoolTest struct {
	Interval      string              `yaml:"interval"`
	InputSeries   []promtoolSeries    `yaml:"input_series"`
	AlertRuleTest []promtoolAlertTest `yaml:"alert_rule_test"`
}

type promtoolSeries struct {
	Series string `yaml:"series"`
	Values string `yaml:"values"`
}

type promtoolAlertTest struct {
	EvalTime  string             `yaml:"eval_time"`
	Alertname string             `yaml:"alertname"`
	ExpAlerts []promtoolExpAlert `yaml:"exp_alerts"`
}

type promtoolExpAlert struct {
	ExpLabels      map[string]string `yaml:"exp_labels"`
	ExpAnnotations map[string]string `yaml:"exp_annotations"`
}

// renderedCatalog is the subset of the rendered rule file this test
// reads back: each alert's static labels and annotations, so the
// expected firing alert is derived from the rendered artifact rather
// than duplicated as brittle literals.
type renderedCatalog struct {
	Groups []struct {
		Rules []struct {
			Alert       string            `yaml:"alert"`
			Labels      map[string]string `yaml:"labels"`
			Annotations map[string]string `yaml:"annotations"`
		} `yaml:"rules"`
	} `yaml:"groups"`
}

// firingCase drives one Prometheus-only alert to its firing state. The
// input series encode the alert's documented triggering condition (the
// spec-derived assertion); the static severity label and annotations of
// the firing alert are pulled from the rendered rule, and resultLabels
// names the labels the alert's expression carries through from the input
// series into the firing alert's label set.
type firingCase struct {
	alert       string
	interval    string
	evalTime    string
	inputSeries []promtoolSeries
	// resultLabels are the non-severity labels the firing alert carries,
	// produced by the expression from the input series (e.g. the pool the
	// idle-pod gauge is scoped to). Merged with the rendered static labels
	// to form the full expected label set.
	resultLabels map[string]string
}

// spec: 25.13 (bundled alerting rules fire in Prometheus), 16.5 (alert catalog)
// diagnosis: a Prometheus-only §16.5 alert did not fire in the real
// Prometheus rule engine under its documented triggering condition, or
// fired with the wrong severity/labels/annotations. The rendered PromQL
// expression is wrong (a bad selector, threshold, join, or window), or
// the render dropped a label or annotation. Because these expressions
// are outside the in-process fallback's evaluable subset, Prometheus is
// the only evaluator for them: a broken expression here means the alert
// never reaches Alertmanager or lenny-ops health aggregation, and the
// gateway fallback silently declines to fire it (never "fires wrong").
func TestBundledPrometheusOnlyAlertsFireUnderTriggeringConditions(t *testing.T) {
	// Cases span the PromQL categories the in-process fallback reports
	// unsupported (§25.13): a counter rate, an `unless on()` label-set
	// join, the time() function, an increase() over a range, and a
	// histogram_quantile. The three §16.5 examples the in-process split
	// calls out (WarmPoolExhausted, RedisUnavailable, BackupOverdue) are
	// all present.
	cases := []firingCase{
		{
			// RedisUnavailable: rate(lenny_quota_redis_fallback_total[2m]) > 0
			// A monotonically climbing fail-open counter yields a positive
			// rate; sustained past the 1m `for` window the alert fires.
			alert:    "RedisUnavailable",
			interval: "30s",
			evalTime: "5m",
			inputSeries: []promtoolSeries{
				{Series: "lenny_quota_redis_fallback_total", Values: "0+5x20"},
			},
		},
		{
			// WarmPoolExhausted:
			//   (min by (pool)(lenny_warmpool_idle_pods) == 0)
			//     unless on (pool)(lenny_warmpool_fill_grace_active == 1)
			// Idle pods pinned at zero for a pool with no fill-grace series
			// keeps the left side and fires after the 1m `for` window. The
			// firing alert carries the pool label from `by (pool)`.
			alert:    "WarmPoolExhausted",
			interval: "30s",
			evalTime: "5m",
			inputSeries: []promtoolSeries{
				{Series: `lenny_warmpool_idle_pods{pool="default"}`, Values: "0x20"},
			},
			resultLabels: map[string]string{"pool": "default"},
		},
		{
			// BackupOverdue:
			//   time() - lenny_backup_last_successful_timestamp{type="full"} > 172800
			// The last-success timestamp held at 0 while simulated time
			// advances past 48h drives the age above the 172800s threshold.
			alert:    "BackupOverdue",
			interval: "1h",
			evalTime: "49h",
			inputSeries: []promtoolSeries{
				{Series: `lenny_backup_last_successful_timestamp{type="full"}`, Values: "0x49"},
			},
			resultLabels: map[string]string{"type": "full"},
		},
		{
			// BackupFailed: increase(lenny_backup_total{status="failed"}[1h]) > 0
			// A climbing failed-backup counter yields a positive increase
			// over the 1h window and fires immediately (for: 0).
			alert:    "BackupFailed",
			interval: "5m",
			evalTime: "1h",
			inputSeries: []promtoolSeries{
				{Series: `lenny_backup_total{status="failed"}`, Values: "0+1x20"},
			},
			resultLabels: map[string]string{"status": "failed"},
		},
		{
			// CheckpointDurationHigh:
			//   histogram_quantile(0.95, sum by (le)(rate(bucket[5m]))) > 2.5
			// All observations land in the (2.5, 5] bucket, so the p95 is
			// above 2.5; sustained past the 5m `for` window it fires. The
			// histogram_quantile drops the le label, leaving only severity.
			alert:    "CheckpointDurationHigh",
			interval: "1m",
			evalTime: "15m",
			inputSeries: []promtoolSeries{
				{Series: `lenny_checkpoint_duration_seconds_bucket{le="1"}`, Values: "0x20"},
				{Series: `lenny_checkpoint_duration_seconds_bucket{le="2.5"}`, Values: "0x20"},
				{Series: `lenny_checkpoint_duration_seconds_bucket{le="5"}`, Values: "0+10x20"},
				{Series: `lenny_checkpoint_duration_seconds_bucket{le="+Inf"}`, Values: "0+10x20"},
			},
		},
	}

	// Render the shared catalog exactly as the Helm chart bundles it for
	// Prometheus. The expected firing labels and annotations are read back
	// from this rendered artifact, so the assertion tracks the catalog.
	rulesYAML, err := rules.RenderRuleGroups("lenny", rules.Catalog())
	if err != nil {
		t.Fatalf("render catalog: %v", err)
	}
	byAlert := indexRendered(t, rulesYAML)

	doc := promtoolDoc{RuleFiles: []string{containers.PromtoolRulesPath}}
	for _, c := range cases {
		rendered, ok := byAlert[c.alert]
		if !ok {
			t.Fatalf("alert %q not present in the rendered catalog", c.alert)
		}
		expLabels := map[string]string{}
		for k, v := range rendered.labels {
			expLabels[k] = v
		}
		for k, v := range c.resultLabels {
			expLabels[k] = v
		}
		doc.Tests = append(doc.Tests, promtoolTest{
			Interval:    c.interval,
			InputSeries: c.inputSeries,
			AlertRuleTest: []promtoolAlertTest{{
				EvalTime:  c.evalTime,
				Alertname: c.alert,
				ExpAlerts: []promtoolExpAlert{{
					ExpLabels:      expLabels,
					ExpAnnotations: rendered.annotations,
				}},
			}},
		})
	}

	testYAML, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal promtool unit test: %v", err)
	}

	res := containers.RunPromtoolRuleTest(t, rulesYAML, testYAML)
	if res.ExitCode != 0 {
		t.Fatalf("promtool test rules exit=%d, want 0 (a Prometheus-only alert did not fire as expected)\n"+
			"unit test:\n%s\npromtool output:\n%s", res.ExitCode, testYAML, res.Output)
	}
}

// renderedRule is the static label and annotation set of one rendered
// alert rule.
type renderedRule struct {
	labels      map[string]string
	annotations map[string]string
}

// indexRendered parses the rendered rule file and indexes each alert's
// static labels and annotations by alert name. Alert names that render
// more than one rule (the label-decomposed alerts) are not among the
// cases here and would collide, so a duplicate is a test-setup error.
func indexRendered(t *testing.T, rulesYAML []byte) map[string]renderedRule {
	t.Helper()
	var rc renderedCatalog
	if err := yaml.Unmarshal(rulesYAML, &rc); err != nil {
		t.Fatalf("parse rendered rules: %v", err)
	}
	out := map[string]renderedRule{}
	for _, g := range rc.Groups {
		for _, r := range g.Rules {
			if _, dup := out[r.Alert]; dup {
				out[r.Alert] = renderedRule{} // mark ambiguous
				continue
			}
			out[r.Alert] = renderedRule{labels: r.Labels, annotations: r.Annotations}
		}
	}
	return out
}
