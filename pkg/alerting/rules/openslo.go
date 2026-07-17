// SPDX-License-Identifier: MIT

package rules

import (
	"bytes"
	"fmt"
	"strings"
	"time"

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

// openSLONotificationTargetName is the metadata.name of the single shared
// AlertNotificationTarget document every emitted AlertPolicy references
// through notificationTargets[].targetRef. The Helm template replaces this
// default literal with the deployer-configured
// monitoring.openslo.notificationTarget.name at install time (the literal
// appears only as the target's metadata.name and the targetRef entries
// pointing at it, which change together, so a global replace is correct).
//
// spec: §16.10 (shared AlertNotificationTarget referenced by every
// AlertPolicy; deployer-configurable name, default lenny-slo-notifications).
const openSLONotificationTargetName = "lenny-slo-notifications"

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
	Description     string                  `yaml:"description"`
	Service         string                  `yaml:"service"`
	IndicatorRef    string                  `yaml:"indicatorRef"`
	TimeWindow      []openSLOTimeWindow     `yaml:"timeWindow"`
	BudgetingMethod string                  `yaml:"budgetingMethod"`
	Objectives      []openSLOObjective      `yaml:"objectives"`
	AlertPolicies   []openSLOAlertPolicyRef `yaml:"alertPolicies"`
}

// openSLOAlertPolicyRef is one SLO alertPolicies entry. OpenSLO v1 requires
// each entry to be an object that references an AlertPolicy by name (rather
// than a bare string), so the export emits {alertPolicyRef: <name>}.
//
// spec: §16.10 (SLO alertPolicies as reference objects).
type openSLOAlertPolicyRef struct {
	AlertPolicyRef string `yaml:"alertPolicyRef"`
}

// openSLONotificationTargetRef is one AlertPolicy notificationTargets entry.
// OpenSLO v1 requires spec.notificationTargets present and non-empty; each
// entry references a shared AlertNotificationTarget by name.
//
// spec: §16.10 (required non-empty notificationTargets referencing an
// emitted AlertNotificationTarget).
type openSLONotificationTargetRef struct {
	TargetRef string `yaml:"targetRef"`
}

// alertNotificationTargetSpec is the OpenSLO AlertNotificationTarget spec.
// target is the free-form target type (default webhook); the deployer
// defines the concrete channel and credentials in their own OpenSLO tool
// keyed by the target's metadata.name.
//
// spec: §16.10 (shared AlertNotificationTarget document).
type alertNotificationTargetSpec struct {
	Description string `yaml:"description"`
	Target      string `yaml:"target"`
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
	Description         string                         `yaml:"description"`
	AlertWhenBreaching  bool                           `yaml:"alertWhenBreaching"`
	Conditions          []openSLOConditionEntry        `yaml:"conditions"`
	NotificationTargets []openSLONotificationTargetRef `yaml:"notificationTargets"`
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
// into the §16.10 OpenSLO v1 export. For each SLO it emits an SLI
// document, an SLO document, and two single-condition burn-rate
// AlertPolicy documents (<name>-burnrate-fast at 1h/14x critical and
// <name>-burnrate-slow at 6h/3x warning). OpenSLO v1 caps an AlertPolicy
// at exactly one condition, so the §16.5 multi-window burn rate is
// preserved by splitting across two policies rather than packing two
// conditions into one. After the per-SLO loop it emits one shared
// top-level AlertNotificationTarget document that every AlertPolicy
// references through notificationTargets[].targetRef, so the fragment is
// self-contained. The documents reference the canonical §16.5 metric names
// and scope every query to deployment_tier="<tier>" so external OpenSLO
// tooling (Sloth, Nobl9) produces burn-rate alerts consistent with the
// bundled §16.5 multi-window alerts. The export is a view of the same
// SLODefinitions catalog burnRateAlerts derives from, so the two cannot
// drift.
//
// service is the OpenSLO service name; tier is the deployment tier
// substituted for SLOTierPlaceholder in the query templates (the
// gen-alerting-rules build step passes SLOTierPlaceholder so the chart
// can substitute global.deploymentTier at install time). notificationTarget
// mirrors tier: it is the free-form AlertNotificationTarget type rendered
// into the shared target's spec.target (the chart-fragment caller passes
// SLONotificationTargetPlaceholder for the Helm template to substitute
// monitoring.openslo.notificationTarget.type; the docs and CLI callers pass
// the concrete OpenSLODefaultNotificationTarget).
//
// spec: §16.10 lines 742-746; §16.5 lines 611-640.
func RenderOpenSLO(service, tier, notificationTarget string) ([]byte, error) {
	if service == "" {
		return nil, fmt.Errorf("rules: OpenSLO service name is required")
	}
	if tier == "" {
		return nil, fmt.Errorf("rules: OpenSLO deployment tier is required")
	}
	if notificationTarget == "" {
		return nil, fmt.Errorf("rules: OpenSLO notification target is required")
	}
	var docs []openSLODoc
	for _, d := range SLODefinitions() {
		if err := d.validateForOpenSLO(); err != nil {
			return nil, err
		}
		labels := map[string]string{"deployment_tier": tier}
		sliName := d.Name + "-sli"
		fastPolicyName := d.Name + "-burnrate-fast"
		slowPolicyName := d.Name + "-burnrate-slow"

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

		docs = append(
			docs,
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
					AlertPolicies: []openSLOAlertPolicyRef{
						{AlertPolicyRef: fastPolicyName},
						{AlertPolicyRef: slowPolicyName},
					},
				},
			},
			burnRatePolicyDoc(
				fastPolicyName, labels, d.Name+"-fast-burn",
				"Fast-window (1h) error-budget burn rate exceeds the page threshold.",
				string(SeverityCritical), burnRateFastMultiplier, burnRateFastWindow,
			),
			burnRatePolicyDoc(
				slowPolicyName, labels, d.Name+"-slow-burn",
				"Slow-window (6h) error-budget burn rate exceeds the warning threshold.",
				string(SeverityWarning), burnRateSlowMultiplier, burnRateSlowWindow,
			),
		)
	}

	// One shared AlertNotificationTarget every AlertPolicy references by
	// name, so the fragment is self-contained. spec: §16.10.
	docs = append(docs, openSLODoc{
		APIVersion: openSLOAPIVersion,
		Kind:       "AlertNotificationTarget",
		Metadata:   openSLOMeta{Name: openSLONotificationTargetName},
		Spec: alertNotificationTargetSpec{
			Description: "Shared notification target for the Lenny SLO burn-rate alert policies.",
			Target:      notificationTarget,
		},
	})

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

// burnRatePolicyDoc builds one single-condition burn-rate AlertPolicy
// document. OpenSLO v1 caps spec.conditions at one entry, so each of the
// §16.5 multi-window thresholds (1h/14x critical, 6h/3x warning) renders as
// its own policy. Every policy references the shared AlertNotificationTarget
// so the fragment is self-contained.
//
// spec: §16.10 (one condition per AlertPolicy, required non-empty
// notificationTargets), §16.5 line 627 (multi-window burn rate).
func burnRatePolicyDoc(policyName string, labels map[string]string, condName, condDesc, severity string, multiplier int, window time.Duration) openSLODoc {
	return openSLODoc{
		APIVersion: openSLOAPIVersion,
		Kind:       "AlertPolicy",
		Metadata:   openSLOMeta{Name: policyName, Labels: labels},
		Spec: alertPolicySpec{
			Description:        "Error-budget burn-rate policy: " + condDesc,
			AlertWhenBreaching: true,
			Conditions: []openSLOConditionEntry{
				{
					Kind:     "AlertCondition",
					Metadata: openSLOMeta{Name: condName},
					Spec: openSLOConditionSpec{
						Description: condDesc,
						Severity:    severity,
						Condition: openSLOBurnRateCond{
							Kind:           "burnrate",
							Op:             "gt",
							Threshold:      multiplier,
							LookbackWindow: prometheusDuration(window),
							AlertAfter:     prometheusDuration(window),
						},
					},
				},
			},
			NotificationTargets: []openSLONotificationTargetRef{
				{TargetRef: openSLONotificationTargetName},
			},
		},
	}
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
