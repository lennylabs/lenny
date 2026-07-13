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
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/prometheus/promql/parser"
	"gopkg.in/yaml.v3"

	"github.com/lennylabs/lenny/pkg/alerting/evaluator"
	"github.com/lennylabs/lenny/pkg/alerting/inproceval"
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

// alertKey identifies a single catalog/rendered alert. Most alerts are
// keyed by name alone, but the §16.5 / §17.2 multi-pair alerts
// (AdmissionPlaneFeatureFlagDowngrade emits one rule per gated
// flag/webhook pair, F-17.2.6) share an alert Name and are distinguished
// only by their static flag_name / expected_webhook_name labels. Keying
// by name alone collapses those distinct rules and triggers a false
// duplicate/count-drift verdict, so the cross-check keys on the name
// plus the distinguishing labels. For a single-rule alert the labels are
// absent, so the key reduces to the name.
func alertKey(name string, labels map[string]string) string {
	return name + "|" + labels["flag_name"] + "|" + labels["expected_webhook_name"]
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
	// `helm template --show-only` still renders every template (the
	// filter applies afterward), so the chart's render-time `required`
	// and `fail` guards fire even though only prometheusrule.yaml is
	// returned. Two such guards must be satisfied:
	//   - §10.3 (NET-064) F-10.3.4: global.spiffeTrustDomain is a
	//     required value with no default (gateway-deployment guard).
	//   - §13.2 (K8S-033) F-13.2.3: coredns.clusterIP must be non-empty
	//     when agentNamespaces is set, which it is in the shipped
	//     default values.yaml (coredns-service.yaml fail guard). The
	//     address mirrors the chart unit tests' free service-CIDR value.
	//
	// §16.9 R8 F-16.9.4: prometheusrule.yaml renders the PrometheusRule
	// CRD only when the Prometheus Operator API group is registered;
	// otherwise monitoring.format degrades to a ConfigMap so a
	// kubectl apply does not fail on a missing CRD. `helm template` sees
	// no live cluster, so the operator group must be declared explicitly
	// with --api-versions for the catalog cross-check to observe the
	// PrometheusRule output it asserts on.
	args := []string{
		"template", chart,
		"--show-only", "templates/prometheusrule.yaml",
		"--api-versions", "monitoring.coreos.com/v1",
		"--set", "global.spiffeTrustDomain=lenny-test",
		"--set", "coredns.clusterIP=10.96.0.53",
	}
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
	if len(doc.Spec.Groups) == 0 {
		t.Fatal("rendered PrometheusRule has no rule groups")
	}

	// spec: §25.13 line 4822 — the catalog renders as one rule group per
	// §16.5 severity bucket (lenny.critical / lenny.warning /
	// lenny.info), so flatten the rules across every group before the
	// catalog cross-check.
	rendered := map[string]renderedRule{}
	for _, g := range doc.Spec.Groups {
		for _, r := range g.Rules {
			key := alertKey(r.Alert, r.Labels)
			if _, dup := rendered[key]; dup {
				t.Errorf("rendered PrometheusRule has duplicate alert %q", r.Alert)
			}
			rendered[key] = r
		}
	}

	catalog := rules.Catalog()
	catalogByKey := map[string]rules.Rule{}
	for _, r := range catalog {
		catalogByKey[alertKey(r.Name, r.Labels)] = r
	}

	// Direction 1: every catalog alert must appear in the chart.
	for _, r := range catalog {
		if _, ok := rendered[alertKey(r.Name, r.Labels)]; !ok {
			t.Errorf("§16.5 catalog alert %q is missing from the rendered chart PrometheusRule", r.Name)
		}
	}

	// Direction 2: every rendered alert must be a catalog alert.
	for _, r := range flattenRendered(doc) {
		if _, ok := catalogByKey[alertKey(r.Alert, r.Labels)]; !ok {
			t.Errorf("rendered PrometheusRule alert %q is not in the §16.5 catalog — the chart must not invent rules", r.Alert)
		}
	}

	// Count parity is the headline drift signal.
	if len(rendered) != len(catalog) {
		t.Errorf("rendered chart has %d alerts, the §16.5 catalog has %d — the two surfaces have drifted",
			len(rendered), len(catalog))
	}
}

// spec: 25.13 (the gateway in-process evaluator, the shared catalog, and the rendered chart are one set)
// diagnosis: The set of alert names the gateway's in-process evaluator
//
//	tracks has drifted from the pkg/alerting/rules catalog or
//	from the chart-rendered PrometheusRule. §25.13 makes the
//	shared catalog the single source of truth for all three
//	surfaces; a filtered or hand-maintained evaluator rule set,
//	or a chart that renders a different set than the gateway
//	evaluates, violates the no-drift invariant. The existing
//	crosscheck tests only compare the rendered chart against the
//	catalog; this one pins the third surface, the gateway
//	evaluator, to the same set.
func TestGatewayEvaluatorTracksCatalogAndRenderedChart(t *testing.T) {
	root := repoRoot(t)

	// Construct the gateway's in-process alert evaluator the same way
	// cmd/lenny-gateway does at startup (runserver.go): the shared
	// pkg/alerting/rules catalog, evaluated by the inproceval instant-
	// vector backend over this replica's own metric registry. Only the
	// tracked rule *set* is under test, so an empty registry suffices
	// and no evaluation tick is driven.
	//
	// spec: §25.13 line 4679 — "**The gateway binary** — the in-process
	// alert state tracker (Section 25.3, Health API) evaluates these
	// expressions against the in-process metric registry."
	catalog := rules.Catalog()
	ev := evaluator.New(catalog, inproceval.New(prometheus.NewRegistry()), evaluator.Options{})

	// Distinct catalog names. The §16.5 / §17.2 multi-pair alerts share
	// an alert Name (distinguished only by static labels), and the
	// evaluator keys its per-rule state machine by Name, so it tracks
	// one entry per distinct name. Compare the three surfaces at that
	// name granularity.
	catalogNames := map[string]bool{}
	for _, r := range catalog {
		catalogNames[r.Name] = true
	}

	// Direction A: the gateway evaluator must track every catalog rule.
	// State(name) reports ok for a tracked rule. Because the evaluator
	// was built from the full catalog, its tracked set cannot exceed the
	// catalog, so this single direction establishes evaluator == catalog.
	// A regression that fed the evaluator a hand-maintained subset, or
	// that made evaluator.New silently drop rules, leaves some catalog
	// name untracked here.
	//
	// spec: §25.13 line 4682 — "the rule definitions cannot drift
	// between what the gateway evaluates and what Prometheus loads."
	for name := range catalogNames {
		if _, ok := ev.State(name); !ok {
			t.Errorf("§25.13 gateway evaluator does not track catalog alert %q — the compiled-in evaluator has drifted from the shared catalog", name)
		}
	}

	// The rendered chart's distinct alert names.
	doc := helmTemplatePrometheusRule(t, root)
	renderedNames := map[string]bool{}
	for _, r := range flattenRendered(doc) {
		renderedNames[r.Alert] = true
	}

	// Direction B: tie the third surface in. The evaluator tracks the
	// catalog (Direction A), so asserting catalog == rendered makes all
	// three sets equal. A rule the gateway evaluates but the chart never
	// renders (or the reverse) is exactly the two-source-of-truth drift
	// §25.13 line 4682 forbids.
	for name := range catalogNames {
		if !renderedNames[name] {
			t.Errorf("catalog alert %q is evaluated by the gateway but missing from the rendered chart — Prometheus would never load a rule the gateway fires on", name)
		}
	}
	for name := range renderedNames {
		if !catalogNames[name] {
			t.Errorf("rendered chart alert %q is not in the shared catalog and is therefore never evaluated by the gateway", name)
		}
	}
	if len(renderedNames) != len(catalogNames) {
		t.Errorf("distinct alert names disagree: gateway evaluator/catalog=%d, rendered chart=%d — the §25.13 single source of truth has drifted",
			len(catalogNames), len(renderedNames))
	}
}

// flattenRendered returns every rendered rule across the per-severity
// groups in document order. Unlike the keyed map it preserves the
// multi-pair alerts that share an alert Name, so direction-2 checks see
// each rule individually.
func flattenRendered(doc renderedPrometheusRule) []renderedRule {
	var out []renderedRule
	for _, g := range doc.Spec.Groups {
		out = append(out, g.Rules...)
	}
	return out
}

// spec: 16.5 (no per-field drift between the catalog and the rendered chart)
// diagnosis: A rule's expr, severity, or for-duration differs between
//
//	pkg/alerting/rules and the chart. Regenerate the bundled
//	fragment with `make generate`.
func TestPrometheusRuleFieldsMatchCatalog(t *testing.T) {
	root := repoRoot(t)
	doc := helmTemplatePrometheusRule(t, root)
	if len(doc.Spec.Groups) == 0 {
		t.Fatal("rendered PrometheusRule has no rule groups")
	}
	// §25.13 line 4822 per-severity group split — flatten before the
	// per-field comparison. The map is keyed by the composite alertKey so
	// the §16.5 / §17.2 multi-pair alerts that share an alert Name compare
	// against their matching rule rather than collapsing onto one.
	rendered := map[string]renderedRule{}
	for _, g := range doc.Spec.Groups {
		for _, r := range g.Rules {
			rendered[alertKey(r.Alert, r.Labels)] = r
		}
	}

	for _, want := range rules.Catalog() {
		got, ok := rendered[alertKey(want.Name, want.Labels)]
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
	if len(doc.Spec.Groups) == 0 {
		t.Fatal("rendered PrometheusRule has no rule groups")
	}
	// §25.13 line 4822 — every per-severity group must be named and
	// carry well-formed rules.
	for _, g := range doc.Spec.Groups {
		if g.Name == "" {
			t.Error("a rendered rule group has no name")
		}
		if len(g.Rules) == 0 {
			t.Errorf("rendered rule group %q has no rules", g.Name)
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
}

// renderedConfigMap is the chart-rendered rules ConfigMap. Only the data
// map matters to the rule-file cross-check.
type renderedConfigMap struct {
	Kind string            `yaml:"kind"`
	Data map[string]string `yaml:"data"`
}

// helmTemplateRulesConfigMap renders the rules manifest with
// monitoring.format=configmap and returns the embedded rules.yaml payload.
// Unlike the PrometheusRule path, the ConfigMap format is selected
// explicitly, so no --api-versions is needed: the operator-CRD degrade
// (§16.9 R8) applies only when the configured format is prometheusrule.
func helmTemplateRulesConfigMap(t *testing.T, root string) string {
	t.Helper()
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skipf("helm not on PATH: %v", err)
	}
	chart := filepath.Join(root, "charts", "lenny")
	// The same render-time `required`/`fail` guards the PrometheusRule
	// render satisfies apply here (see helmTemplatePrometheusRule): the
	// §10.3 spiffeTrustDomain required value and the §13.2 coredns.clusterIP
	// fail guard.
	args := []string{
		"template", chart,
		"--show-only", "templates/prometheusrule.yaml",
		"--set", "global.spiffeTrustDomain=lenny-test",
		"--set", "coredns.clusterIP=10.96.0.53",
		"--set", "monitoring.format=configmap",
	}
	out, err := exec.Command(helm, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	var cm renderedConfigMap
	if err := yaml.Unmarshal(out, &cm); err != nil {
		t.Fatalf("unmarshal rendered ConfigMap: %v\n%s", err, out)
	}
	if cm.Kind != "ConfigMap" {
		t.Fatalf("rendered kind = %q, want ConfigMap", cm.Kind)
	}
	payload, ok := cm.Data["rules.yaml"]
	if !ok {
		t.Fatalf("rendered ConfigMap has no data[\"rules.yaml\"] key")
	}
	return payload
}

// promRuleFile mirrors the standard Prometheus rule-file document: a
// top-level `groups:` list, each group carrying `rules:` with the
// alerting-rule fields. It is deliberately the rule-file structure, not
// the PrometheusRule CRD (which nests the same groups under
// apiVersion/kind/metadata/spec). Decoding the ConfigMap payload into
// this struct with KnownFields(true) rejects a leaked CRD manifest,
// because its top-level apiVersion/kind/metadata/spec keys are unknown
// here.
type promRuleFile struct {
	Groups []struct {
		Name  string `yaml:"name"`
		Rules []struct {
			Alert       string            `yaml:"alert"`
			Expr        string            `yaml:"expr"`
			For         string            `yaml:"for"`
			Labels      map[string]string `yaml:"labels"`
			Annotations map[string]string `yaml:"annotations"`
		} `yaml:"rules"`
	} `yaml:"groups"`
}

// spec: 25.13 (the ConfigMap emits the same rules in standard Prometheus rule-file YAML)
// diagnosis: The ConfigMap the chart renders for vanilla-Prometheus
//
//	deployers (monitoring.format=configmap) does not carry a
//	loadable Prometheus rule file in data["rules.yaml"]. §25.13
//	line 4721 requires the ConfigMap, mountable into the
//	Prometheus pod's rule_files directory, to hold "the same
//	rules in standard Prometheus YAML format" — a top-level
//	`groups:` document whose rules[] carry alert/expr/for. A
//	regression that spliced the PrometheusRule CRD
//	(apiVersion/kind/spec.groups) into the ConfigMap payload, or
//	otherwise emitted a file Prometheus cannot load, is caught
//	here: the payload is decoded with KnownFields and every expr
//	is parsed by the same promql parser Prometheus loads rules
//	with.
func TestRulesConfigMapIsLoadablePrometheusRuleFile(t *testing.T) {
	root := repoRoot(t)
	payload := helmTemplateRulesConfigMap(t, root)

	// Decode the embedded string as a Prometheus rule file. KnownFields
	// makes an unexpected top-level key (the apiVersion/kind/metadata/spec
	// of a leaked CRD manifest, for example) a hard error rather than a
	// silently ignored field, so only a genuine rule-file document passes.
	//
	// spec: §25.13 line 4721 — "The ConfigMap can be mounted into the
	// Prometheus pod's `rule_files` directory. The chart emits the same
	// rules in standard Prometheus YAML format."
	dec := yaml.NewDecoder(bytes.NewReader([]byte(payload)))
	dec.KnownFields(true)
	var file promRuleFile
	if err := dec.Decode(&file); err != nil {
		t.Fatalf("data[\"rules.yaml\"] is not a standard Prometheus rule file: %v\npayload:\n%s", err, payload)
	}
	if len(file.Groups) == 0 {
		t.Fatalf("parsed rule file has no groups; want a top-level `groups:` document\npayload:\n%s", payload)
	}

	// Every parsed group must carry alerting rules whose alert and expr are
	// present and whose expr parses as PromQL — the loadability guarantee a
	// rule_files entry must meet. parser.ParseExpr is the same expression
	// parser Prometheus uses when it loads a rule file.
	renderedNames := map[string]bool{}
	for _, g := range file.Groups {
		if g.Name == "" {
			t.Error("a rule group has no name")
		}
		if len(g.Rules) == 0 {
			t.Errorf("rule group %q has no rules", g.Name)
		}
		for _, r := range g.Rules {
			if r.Alert == "" {
				t.Errorf("rule group %q has a rule with no alert name", g.Name)
			}
			if r.Expr == "" {
				t.Errorf("rule %q has an empty expr", r.Alert)
			}
			if _, err := parser.ParseExpr(r.Expr); err != nil {
				t.Errorf("rule %q expr does not parse as PromQL: %v", r.Alert, err)
			}
			renderedNames[r.Alert] = true
		}
	}

	// The spec sentence requires "the same rules" as the operator path, so
	// every §16.5 catalog alert must appear in the ConfigMap payload. This
	// pins the ConfigMap output to the shared catalog rather than accepting
	// any well-formed but divergent rule file.
	//
	// spec: §25.13 line 4721 — "The chart emits the same rules ..."
	for _, r := range rules.Catalog() {
		if !renderedNames[r.Name] {
			t.Errorf("§16.5 catalog alert %q is missing from the rendered ConfigMap rules.yaml", r.Name)
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
