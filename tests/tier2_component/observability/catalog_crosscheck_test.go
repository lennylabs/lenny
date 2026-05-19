// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component test: the §16 catalog cross-check. This is the
// Wave 4 exit-gate test — it asserts the §16.5 alert catalog in
// pkg/alerting/rules and the PrometheusRule the Helm chart renders are
// the same set, with no drift in either direction.
//
// Two surfaces derive from the single pkg/alerting/rules catalog: the
// gateway's in-process alert evaluator, and the chart's bundled
// PrometheusRule (§16.9, §25.13). The chart's rule fragment is a
// generated file (charts/lenny/files/alerting-rules.yaml). This test
// renders the chart with `helm template` and checks:
//
//   - every catalog alert appears in the rendered PrometheusRule;
//   - every rendered alert is a catalog alert (no invented rules);
//   - each rendered rule's expr, severity, and for match the catalog;
//   - the committed generated fragment is current (no stale checkout).
//
// The test skips gracefully when the helm CLI is not on PATH.

package observability_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/lenny/pkg/alerting/rules"
)

// repoRoot walks up from the working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for d := wd; ; {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			t.Fatalf("no go.mod found above %s", wd)
		}
		d = parent
	}
}

// renderedRule is one alert from the chart-rendered PrometheusRule.
type renderedRule struct {
	Alert       string            `yaml:"alert"`
	Expr        string            `yaml:"expr"`
	For         string            `yaml:"for"`
	Labels      map[string]string `yaml:"labels"`
	Annotations map[string]string `yaml:"annotations"`
}

// renderedPrometheusRule is the chart-rendered PrometheusRule CRD.
type renderedPrometheusRule struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Groups []struct {
			Name  string         `yaml:"name"`
			Rules []renderedRule `yaml:"rules"`
		} `yaml:"groups"`
	} `yaml:"spec"`
}

// helmTemplatePrometheusRule renders just the prometheusrule.yaml
// template and unmarshals it. It skips the test when helm is absent.
func helmTemplatePrometheusRule(t *testing.T, root string, setArgs ...string) renderedPrometheusRule {
	t.Helper()
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skipf("helm not on PATH: %v", err)
	}
	chart := filepath.Join(root, "charts", "lenny")
	args := []string{"template", chart, "--show-only", "templates/prometheusrule.yaml"}
	args = append(args, setArgs...)
	out, err := exec.Command(helm, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	var doc renderedPrometheusRule
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal rendered PrometheusRule: %v\n%s", err, out)
	}
	return doc
}

// spec: 16.5 (the §16.5 alert catalog cross-checks against the rendered chart)
// diagnosis: The chart-rendered PrometheusRule and the pkg/alerting/rules
//
//	catalog disagree. Either the bundled fragment
//	charts/lenny/files/alerting-rules.yaml is stale (run
//	`make generate`) or the prometheusrule.yaml template
//	dropped or mangled rules.
func TestPrometheusRuleMatchesAlertCatalog(t *testing.T) {
	root := repoRoot(t)
	doc := helmTemplatePrometheusRule(t, root)

	if doc.Kind != "PrometheusRule" {
		t.Fatalf("rendered kind = %q, want PrometheusRule", doc.Kind)
	}
	if len(doc.Spec.Groups) != 1 {
		t.Fatalf("rendered %d rule groups, want exactly 1", len(doc.Spec.Groups))
	}

	rendered := map[string]renderedRule{}
	for _, r := range doc.Spec.Groups[0].Rules {
		if _, dup := rendered[r.Alert]; dup {
			t.Errorf("rendered PrometheusRule has duplicate alert %q", r.Alert)
		}
		rendered[r.Alert] = r
	}

	catalog := rules.Catalog()
	catalogByName := map[string]rules.Rule{}
	for _, r := range catalog {
		catalogByName[r.Name] = r
	}

	// Direction 1: every catalog alert must appear in the chart.
	for _, r := range catalog {
		if _, ok := rendered[r.Name]; !ok {
			t.Errorf("§16.5 catalog alert %q is missing from the rendered chart PrometheusRule", r.Name)
		}
	}

	// Direction 2: every rendered alert must be a catalog alert.
	for name := range rendered {
		if _, ok := catalogByName[name]; !ok {
			t.Errorf("rendered PrometheusRule alert %q is not in the §16.5 catalog — the chart must not invent rules", name)
		}
	}

	// Count parity is the headline drift signal.
	if len(rendered) != len(catalog) {
		t.Errorf("rendered chart has %d alerts, the §16.5 catalog has %d — the two surfaces have drifted",
			len(rendered), len(catalog))
	}
}

// spec: 16.5 (no per-field drift between the catalog and the rendered chart)
// diagnosis: A rule's expr, severity, or for-duration differs between
//
//	pkg/alerting/rules and the chart. Regenerate the bundled
//	fragment with `make generate`.
func TestPrometheusRuleFieldsMatchCatalog(t *testing.T) {
	root := repoRoot(t)
	doc := helmTemplatePrometheusRule(t, root)
	if len(doc.Spec.Groups) != 1 {
		t.Fatalf("rendered %d rule groups, want exactly 1", len(doc.Spec.Groups))
	}
	rendered := map[string]renderedRule{}
	for _, r := range doc.Spec.Groups[0].Rules {
		rendered[r.Alert] = r
	}

	for _, want := range rules.Catalog() {
		got, ok := rendered[want.Name]
		if !ok {
			continue // absence is reported by TestPrometheusRuleMatchesAlertCatalog
		}
		if got.Expr != want.Expr {
			t.Errorf("%s: rendered expr %q != catalog expr %q", want.Name, got.Expr, want.Expr)
		}
		if got.Labels["severity"] != string(want.Severity) {
			t.Errorf("%s: rendered severity %q != catalog severity %q",
				want.Name, got.Labels["severity"], want.Severity)
		}
		wantFor := ""
		if want.For > 0 {
			wantFor = prometheusDuration(want.For)
		}
		if got.For != wantFor {
			t.Errorf("%s: rendered for %q != catalog for %q", want.Name, got.For, wantFor)
		}
		if got.Annotations["summary"] != want.Summary {
			t.Errorf("%s: rendered summary != catalog summary", want.Name)
		}
		if want.Severity == rules.SeverityCritical && got.Annotations["runbook_url"] == "" {
			t.Errorf("%s: critical alert rendered without a runbook_url annotation", want.Name)
		}
	}
}

// spec: 16.5 (the bundled fragment is generated, not hand-edited)
// diagnosis: charts/lenny/files/alerting-rules.yaml is stale relative
//
//	to the §16.5 catalog. Run `make generate` to regenerate
//	it from pkg/alerting/rules.
func TestBundledAlertingRulesFragmentIsCurrent(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command("go", "run", "./cmd/gen-alerting-rules", "-check")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the bundled alerting-rules fragment is stale; run `make generate`\n%s", out)
	}
}

// spec: 16.5 (the rendered PrometheusRule rule group is well-formed)
// diagnosis: The rendered rule group is empty or unnamed. Inspect the
//
//	prometheusrule.yaml template and the bundled fragment.
func TestRenderedRuleGroupIsWellFormed(t *testing.T) {
	root := repoRoot(t)
	doc := helmTemplatePrometheusRule(t, root)
	if len(doc.Spec.Groups) != 1 {
		t.Fatalf("rendered %d rule groups, want exactly 1", len(doc.Spec.Groups))
	}
	g := doc.Spec.Groups[0]
	if g.Name == "" {
		t.Error("the rendered rule group has no name")
	}
	if len(g.Rules) == 0 {
		t.Error("the rendered rule group has no rules")
	}
	for _, r := range g.Rules {
		if r.Expr == "" {
			t.Errorf("rendered rule %q has an empty expr", r.Alert)
		}
		if r.Labels["severity"] == "" {
			t.Errorf("rendered rule %q has no severity label", r.Alert)
		}
	}
}

// prometheusDuration mirrors the rules-package duration formatting so
// the cross-check compares the chart's for-strings against the same
// rendering the catalog produces.
func prometheusDuration(d time.Duration) string {
	secs := int64(d.Seconds())
	switch {
	case secs > 0 && secs%3600 == 0:
		return fmt.Sprintf("%dh", secs/3600)
	case secs > 0 && secs%60 == 0:
		return fmt.Sprintf("%dm", secs/60)
	default:
		return fmt.Sprintf("%ds", secs)
	}
}
