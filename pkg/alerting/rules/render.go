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
	group := promRuleGroup{Name: name}
	for _, r := range catalog {
		if err := r.Validate(); err != nil {
			return nil, fmt.Errorf("rules: cannot render an invalid catalogue: %w", err)
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
		group.Rules = append(group.Rules, ar)
	}
	doc := prometheusRule{
		APIVersion: "monitoring.coreos.com/v1",
		Kind:       "PrometheusRule",
		Metadata:   prometheusRuleMeta{Name: name},
		Spec:       prometheusRuleSpec{Groups: []promRuleGroup{group}},
	}
	return yaml.Marshal(doc)
}

// prometheusDuration formats a Go duration as a Prometheus duration
// string. A whole number of minutes is rendered in minutes, otherwise
// in seconds — alert sustain windows are always whole seconds.
func prometheusDuration(d time.Duration) string {
	secs := int64(d.Seconds())
	if secs > 0 && secs%60 == 0 {
		return fmt.Sprintf("%dm", secs/60)
	}
	return fmt.Sprintf("%ds", secs)
}
