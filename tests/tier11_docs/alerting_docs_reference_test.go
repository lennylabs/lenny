// SPDX-License-Identifier: MIT

// Tier-11 documentation checks for the deployer-facing alerting
// reference under docs/alerting/. The gen-alerting-rules currency check
// (tier 2) guarantees the committed docs/alerting/rules.yaml matches the
// generator output, and the catalog cross-check guarantees the chart's
// ConfigMap payload is a loadable Prometheus rule file. Neither pins the
// committed docs/alerting/rules.yaml reference itself as a schema-valid
// Prometheus rule file a deployer could load, nor that
// docs/alerting/routing-recommendations.md enumerates the severities the
// spec's routing mapping defines. These tests close that gap.
//
// These tests are NOT under a build tag: they read the repository state
// directly and need no external infrastructure.

package tier11_docs_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prometheus/prometheus/promql/parser"
	"gopkg.in/yaml.v3"
)

// docsPromRuleFile mirrors the standard Prometheus rule-file document: a
// top-level `groups:` list, each group carrying `rules:` with the
// alerting-rule fields. It is the rule-file structure, not the
// PrometheusRule CRD (which nests the same groups under
// apiVersion/kind/metadata/spec). Decoding with KnownFields(true) rejects
// a leaked CRD manifest, whose top-level apiVersion/kind/metadata/spec
// keys are unknown here.
type docsPromRuleFile struct {
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

// spec: §25.13 (Documented YAML reference — the full rule set rendered
//
//	into docs/alerting/rules.yaml so deployers on non-Prometheus stacks
//	can translate the expressions)
//
// diagnosis: The committed docs/alerting/rules.yaml deployer reference is
//
//	not a schema-valid, loadable Prometheus rule file. §25.13 requires
//	"the full rule set ... rendered into docs/alerting/rules.yaml ... so
//	deployers using non-Prometheus monitoring stacks ... can translate the
//	expressions to their target system." A deployer translating the file
//	relies on a top-level `groups:` document whose rules[] carry alert/expr
//	and whose expr is valid PromQL. A regression that emitted a
//	PrometheusRule CRD (apiVersion/kind/spec.groups) into this path, or a
//	malformed expression, is caught here: the payload is decoded with
//	KnownFields and every expr is parsed by the same promql parser
//	Prometheus loads rules with. The tier-2 currency check only asserts the
//	committed file matches generator output; it does not validate the
//	schema the deployer loads.
func TestDocsAlertingRulesYAMLIsLoadablePrometheusRuleFile(t *testing.T) {
	root := repoRoot(t)
	payload, err := os.ReadFile(filepath.Join(root, "docs", "alerting", "rules.yaml"))
	if err != nil {
		t.Fatalf("read docs/alerting/rules.yaml: %v", err)
	}

	// Decode as a Prometheus rule file. KnownFields makes an unexpected
	// top-level key (the apiVersion/kind/metadata/spec of a leaked CRD
	// manifest, for example) a hard error rather than a silently ignored
	// field, so only a genuine rule-file document passes.
	dec := yaml.NewDecoder(bytes.NewReader(payload))
	dec.KnownFields(true)
	var file docsPromRuleFile
	if err := dec.Decode(&file); err != nil {
		t.Fatalf("docs/alerting/rules.yaml is not a standard Prometheus rule file: %v", err)
	}
	if len(file.Groups) == 0 {
		t.Fatalf("parsed rule file has no groups; want a top-level `groups:` document")
	}

	// Every parsed group must carry alerting rules whose alert and expr are
	// present and whose expr parses as PromQL — the loadability guarantee a
	// deployer translating the reference relies on. parser.ParseExpr is the
	// same expression parser Prometheus uses when it loads a rule file.
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
		}
	}
}

// spec: §25.13 (Alertmanager Routing — the recommended severity-to-routing
//
//	mapping in docs/alerting/routing-recommendations.md covers critical,
//	warning, and info)
//
// diagnosis: docs/alerting/routing-recommendations.md does not enumerate
//
//	the severities the §25.13 routing mapping defines. The spec's
//	Alertmanager Routing section provides a recommended severity-to-routing
//	mapping keyed on `critical`, `warning`, and `info`, matching the
//	Severity field of the §16.5 catalog rules ("critical", "warning",
//	"info"). A deployer copies the recommendations into their Alertmanager
//	route config as a starting point, so an omitted severity leaves a class
//	of alerts unrouted. This asserts each severity name appears in the
//	committed routing reference.
func TestRoutingRecommendationsNameAllSeverities(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "docs", "alerting", "routing-recommendations.md"))
	if err != nil {
		t.Fatalf("read docs/alerting/routing-recommendations.md: %v", err)
	}
	text := string(body)

	// The §16.5 Rule.Severity field is one of "critical", "warning", or
	// "info"; the §25.13 routing mapping must name each so every alert
	// severity has a recommended route.
	for _, severity := range []string{"critical", "warning", "info"} {
		if !strings.Contains(text, severity) {
			t.Errorf("routing-recommendations.md does not name the %q severity", severity)
		}
	}
}
