// SPDX-License-Identifier: MIT

package rules

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// openSLOAPIVersion is the OpenSLO v1 document apiVersion the export
// emits. spec: §16.10 line 733 ("OpenSLO v1 YAML documents").
const openSLOAPIVersion = "openslo/v1"

// OpenSLOService is the default OpenSLO service name every exported SLO
// is attached to. The gen-alerting-rules build step and the lenny-ctl
// slo command pass it to RenderOpenSLO so the rendered service name is
// canonical across the chart fragment and the CLI output.
const OpenSLOService = "lenny"

// openSLODoc is one OpenSLO v1 document. Spec is one of sliSpec,
// sloSpec, or alertPolicySpec depending on Kind.
type openSLODoc struct {
	APIVersion string      `yaml:"apiVersion"`
	Kind       string      `yaml:"kind"`
	Metadata   openSLOMeta `yaml:"metadata"`
	Spec       any         `yaml:"spec"`
}

type openSLOMeta struct {
	Name   string            `yaml:"name"`
	Labels map[string]string `yaml:"labels,omitempty"`
}

// sliSpec is the OpenSLO SLI spec: a ratio of good (or bad) events over
// total events, expressed as Prometheus queries that reference the
// canonical §16.5 metric names.
type sliSpec struct {
	Description string             `yaml:"description"`
	RatioMetric openSLORatioMetric `yaml:"ratioMetric"`
}

type openSLORatioMetric struct {
	Counter bool              `yaml:"counter"`
	Good    *openSLOMetricRef `yaml:"good,omitempty"`
	Bad     *openSLOMetricRef `yaml:"bad,omitempty"`
	Total   openSLOMetricRef  `yaml:"total"`
}

type openSLOMetricRef struct {
	MetricSource openSLOMetricSource `yaml:"metricSource"`
}

type openSLOMetricSource struct {
	Type string            `yaml:"type"`
	Spec map[string]string `yaml:"spec"`
}

// sloSpec is the OpenSLO SLO spec: it references an SLI by name, sets a
// rolling 30-day time window (the §16.5 measurement window) and an
// Occurrences budgeting method, carries the SLO objective target, and
// references the burn-rate AlertPolicy.
type sloSpec struct {
	Description     string              `yaml:"description"`
	Service         string              `yaml:"service"`
	IndicatorRef    string              `yaml:"indicatorRef"`
	TimeWindow      []openSLOTimeWindow `yaml:"timeWindow"`
	BudgetingMethod string              `yaml:"budgetingMethod"`
	Objectives      []openSLOObjective  `yaml:"objectives"`
	AlertPolicies   []string            `yaml:"alertPolicies"`
}

type openSLOTimeWindow struct {
	Duration  string `yaml:"duration"`
	IsRolling bool   `yaml:"isRolling"`
}

type openSLOObjective struct {
	DisplayName string  `yaml:"displayName"`
	Target      float64 `yaml:"target"`
}

// alertPolicySpec is the OpenSLO AlertPolicy spec carrying the §16.5
// multi-window burn-rate conditions (fast critical + slow warning).
type alertPolicySpec struct {
	Description        string                  `yaml:"description"`
	AlertWhenBreaching bool                    `yaml:"alertWhenBreaching"`
	Conditions         []openSLOConditionEntry `yaml:"conditions"`
}

type openSLOConditionEntry struct {
	Kind     string               `yaml:"kind"`
	Metadata openSLOMeta          `yaml:"metadata"`
	Spec     openSLOConditionSpec `yaml:"spec"`
}

type openSLOConditionSpec struct {
	Description string              `yaml:"description"`
	Severity    string              `yaml:"severity"`
	Condition   openSLOBurnRateCond `yaml:"condition"`
}

type openSLOBurnRateCond struct {
	Kind           string `yaml:"kind"`
	Op             string `yaml:"op"`
	Threshold      int    `yaml:"threshold"`
	LookbackWindow string `yaml:"lookbackWindow"`
	AlertAfter     string `yaml:"alertAfter"`
}

// RenderOpenSLO renders the canonical §16.5 SLO catalog (SLODefinitions)
// into the §16.10 OpenSLO v1 export: for each SLO, an SLI document, an
// SLO document, and a multi-window burn-rate AlertPolicy document. The
// documents reference the canonical §16.5 metric names and scope every
// query to deployment_tier="<tier>" so external OpenSLO tooling (Sloth,
// Nobl9) produces burn-rate alerts consistent with the bundled §16.5
// multi-window alerts. The export is a view of the same SLODefinitions
// catalog burnRateAlerts derives from, so the two cannot drift.
//
// service is the OpenSLO service name; tier is the deployment tier
// substituted for SLOTierPlaceholder in the query templates (the
// gen-alerting-rules build step passes SLOTierPlaceholder so the chart
// can substitute global.deploymentTier at install time).
//
// spec: §16.10 lines 732-736; §16.5 lines 611-640.
func RenderOpenSLO(service, tier string) ([]byte, error) {
	if service == "" {
		return nil, fmt.Errorf("rules: OpenSLO service name is required")
	}
	if tier == "" {
		return nil, fmt.Errorf("rules: OpenSLO deployment tier is required")
	}
	var docs []openSLODoc
	for _, d := range SLODefinitions() {
		if err := d.validateForOpenSLO(); err != nil {
			return nil, err
		}
		labels := map[string]string{"deployment_tier": tier}
		sliName := d.Name + "-sli"
		policyName := d.Name + "-burnrate"

		ratio := openSLORatioMetric{
			Counter: d.SLI.Counter,
			Total:   promSource(d.SLI.Total, tier),
		}
		if d.SLI.Good != "" {
			g := promSource(d.SLI.Good, tier)
			ratio.Good = &g
		} else {
			b := promSource(d.SLI.Bad, tier)
			ratio.Bad = &b
		}

		docs = append(docs,
			openSLODoc{
				APIVersion: openSLOAPIVersion,
				Kind:       "SLI",
				Metadata:   openSLOMeta{Name: sliName, Labels: labels},
				Spec: sliSpec{
					Description: d.Objective,
					RatioMetric: ratio,
				},
			},
			openSLODoc{
				APIVersion: openSLOAPIVersion,
				Kind:       "SLO",
				Metadata:   openSLOMeta{Name: d.Name, Labels: labels},
				Spec: sloSpec{
					Description:     d.Objective,
					Service:         service,
					IndicatorRef:    sliName,
					TimeWindow:      []openSLOTimeWindow{{Duration: "30d", IsRolling: true}},
					BudgetingMethod: "Occurrences",
					Objectives:      []openSLOObjective{{DisplayName: d.Objective, Target: d.Target}},
					AlertPolicies:   []string{policyName},
				},
			},
			openSLODoc{
				APIVersion: openSLOAPIVersion,
				Kind:       "AlertPolicy",
				Metadata:   openSLOMeta{Name: policyName, Labels: labels},
				Spec: alertPolicySpec{
					Description:        "Multi-window error-budget burn-rate policy for " + d.Objective,
					AlertWhenBreaching: true,
					Conditions: []openSLOConditionEntry{
						{
							Kind:     "AlertCondition",
							Metadata: openSLOMeta{Name: d.Name + "-fast-burn"},
							Spec: openSLOConditionSpec{
								Description: "Fast-window (1h) error-budget burn rate exceeds the page threshold.",
								Severity:    string(SeverityCritical),
								Condition: openSLOBurnRateCond{
									Kind:           "burnrate",
									Op:             "gt",
									Threshold:      burnRateFastMultiplier,
									LookbackWindow: prometheusDuration(burnRateFastWindow),
									AlertAfter:     prometheusDuration(burnRateFastWindow),
								},
							},
						},
						{
							Kind:     "AlertCondition",
							Metadata: openSLOMeta{Name: d.Name + "-slow-burn"},
							Spec: openSLOConditionSpec{
								Description: "Slow-window (6h) error-budget burn rate exceeds the warning threshold.",
								Severity:    string(SeverityWarning),
								Condition: openSLOBurnRateCond{
									Kind:           "burnrate",
									Op:             "gt",
									Threshold:      burnRateSlowMultiplier,
									LookbackWindow: prometheusDuration(burnRateSlowWindow),
									AlertAfter:     prometheusDuration(burnRateSlowWindow),
								},
							},
						},
					},
				},
			},
		)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	for _, doc := range docs {
		if err := enc.Encode(doc); err != nil {
			_ = enc.Close()
			return nil, fmt.Errorf("rules: encoding OpenSLO document: %w", err)
		}
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("rules: closing OpenSLO encoder: %w", err)
	}
	return buf.Bytes(), nil
}

// promSource builds a Prometheus metricSource with the deployment tier
// substituted into the query template.
func promSource(query, tier string) openSLOMetricRef {
	return openSLOMetricRef{
		MetricSource: openSLOMetricSource{
			Type: "prometheus",
			Spec: map[string]string{
				"query": strings.ReplaceAll(query, SLOTierPlaceholder, tier),
			},
		},
	}
}

// validateForOpenSLO rejects an SLO definition that cannot render a
// well-formed OpenSLO SLI: a name, objective, a target in (0,1], a total
// query, and exactly one of a good or bad query are all required.
func (d SLODefinition) validateForOpenSLO() error {
	if d.Name == "" {
		return fmt.Errorf("rules: OpenSLO SLO has no name")
	}
	if d.Objective == "" {
		return fmt.Errorf("rules: OpenSLO SLO %q has no objective", d.Name)
	}
	if d.Target <= 0 || d.Target > 1 {
		return fmt.Errorf("rules: OpenSLO SLO %q target %v is not in (0,1]", d.Name, d.Target)
	}
	if d.SLI.Total == "" {
		return fmt.Errorf("rules: OpenSLO SLO %q SLI has no total query", d.Name)
	}
	if (d.SLI.Good == "") == (d.SLI.Bad == "") {
		return fmt.Errorf("rules: OpenSLO SLO %q SLI must set exactly one of good or bad", d.Name)
	}
	return nil
}
