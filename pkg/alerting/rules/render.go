// SPDX-License-Identifier: MIT

package rules

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// prometheusRule is the Prometheus Operator PrometheusRule CRD
// document RenderPrometheusRule emits.
type prometheusRule struct {
	APIVersion string             `yaml:"apiVersion"`
	Kind       string             `yaml:"kind"`
	Metadata   prometheusRuleMeta `yaml:"metadata"`
	Spec       prometheusRuleSpec `yaml:"spec"`
}

type prometheusRuleMeta struct {
	Name string `yaml:"name"`
}

type prometheusRuleSpec struct {
	Groups []promRuleGroup `yaml:"groups"`
}

type promRuleGroup struct {
	Name  string          `yaml:"name"`
	Rules []promAlertRule `yaml:"rules"`
}

type promAlertRule struct {
	Alert       string            `yaml:"alert"`
	Expr        string            `yaml:"expr"`
	For         string            `yaml:"for,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty"`
}

// buildRuleGroup validates a catalog and builds the single
// promRuleGroup the renderers share. An invalid catalog is an error.
func buildRuleGroup(groupName string, catalog []Rule) (promRuleGroup, error) {
	group := promRuleGroup{Name: groupName}
	for _, r := range catalog {
		if err := r.Validate(); err != nil {
			return promRuleGroup{}, fmt.Errorf("rules: cannot render an invalid catalogue: %w", err)
		}
		ar := promAlertRule{
			Alert:       r.Name,
			Expr:        r.Expr,
			Labels:      map[string]string{"severity": string(r.Severity)},
			Annotations: map[string]string{"summary": r.Summary},
		}
		if r.For > 0 {
			ar.For = prometheusDuration(r.For)
		}
		if r.Description != "" {
			ar.Annotations["description"] = r.Description
		}
		if r.RunbookURL != "" {
			ar.Annotations["runbook_url"] = r.RunbookURL
		}
		if r.SLO != "" {
			ar.Annotations["slo"] = r.SLO
		}
		group.Rules = append(group.Rules, ar)
	}
	return group, nil
}

// RenderPrometheusRule renders a catalogue of Rule values into a
// Prometheus Operator PrometheusRule CRD document, the form the Helm
// chart bundles as the §16.5 alert manifests. Every rule is validated
// first: an invalid catalogue is a render error rather than an invalid
// manifest. The single rule group is named name; each rule carries its
// §16.5 severity label and summary, description, and runbook_url
// annotations.
func RenderPrometheusRule(name string, catalog []Rule) ([]byte, error) {
	if name == "" {
		return nil, fmt.Errorf("rules: PrometheusRule name is required")
	}
	group, err := buildRuleGroup(name, catalog)
	if err != nil {
		return nil, err
	}
	doc := prometheusRule{
		APIVersion: "monitoring.coreos.com/v1",
		Kind:       "PrometheusRule",
		Metadata:   prometheusRuleMeta{Name: name},
		Spec:       prometheusRuleSpec{Groups: []promRuleGroup{group}},
	}
	return yaml.Marshal(doc)
}

// ruleGroupsDoc is the spec.groups fragment RenderRuleGroups emits.
// It is the body the Helm chart's PrometheusRule template splices in,
// so the chart owns apiVersion, kind, and metadata while the rule
// bodies come verbatim from the shared catalog.
type ruleGroupsDoc struct {
	Groups []promRuleGroup `yaml:"groups"`
}

// RenderRuleGroups renders a catalog into the spec.groups fragment of a
// PrometheusRule, without the apiVersion, kind, or metadata envelope.
// The Helm chart bundles this fragment and its prometheusrule.yaml
// template wraps it with chart-managed metadata. groupName names the
// single rule group; every rule is validated first, so an invalid
// catalog is a render error rather than an invalid fragment.
func RenderRuleGroups(groupName string, catalog []Rule) ([]byte, error) {
	if groupName == "" {
		return nil, fmt.Errorf("rules: rule group name is required")
	}
	group, err := buildRuleGroup(groupName, catalog)
	if err != nil {
		return nil, err
	}
	return yaml.Marshal(ruleGroupsDoc{Groups: []promRuleGroup{group}})
}

// RenderRuleGroupsBySeverity renders the catalog into a spec.groups
// fragment carrying one rule group per §16.5 severity (critical,
// warning, info). The split lets Prometheus evaluate the three buckets
// in parallel rather than serialising the full catalog through a
// single group — the §25.13 line 4822 "~40 rules at ~10ms p95" budget
// applies per group, and the catalog has now grown well past forty
// rules.
//
// Groups are emitted in a deterministic order (critical → warning →
// info), keyed off the severities present in the supplied catalog so a
// trimmed catalog does not render an empty bucket. Rules inside each
// group preserve their catalog order. namePrefix names the group set
// (e.g. "lenny" → "lenny.critical", "lenny.warning", "lenny.info");
// every rule is validated first.
//
// spec: §25.13 line 4822 (per-group evaluation cost); F-25.13.9.
func RenderRuleGroupsBySeverity(namePrefix string, catalog []Rule) ([]byte, error) {
	if namePrefix == "" {
		return nil, fmt.Errorf("rules: rule group name prefix is required")
	}
	order := []Severity{SeverityCritical, SeverityWarning, SeverityInfo}
	bySeverity := map[Severity][]Rule{}
	for _, r := range catalog {
		bySeverity[r.Severity] = append(bySeverity[r.Severity], r)
	}
	var groups []promRuleGroup
	for _, sev := range order {
		bucket := bySeverity[sev]
		if len(bucket) == 0 {
			continue
		}
		name := namePrefix + "." + string(sev)
		group, err := buildRuleGroup(name, bucket)
		if err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	if len(groups) == 0 {
		return nil, fmt.Errorf("rules: catalog has no rules to render")
	}
	return yaml.Marshal(ruleGroupsDoc{Groups: groups})
}

// prometheusDuration formats a Go duration as a Prometheus duration
// string. A whole number of hours is rendered in hours, a whole number
// of minutes in minutes, otherwise in seconds — alert sustain windows
// are always whole seconds.
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
