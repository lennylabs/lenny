// SPDX-License-Identifier: MIT

package upgradeservice

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// VersionSource reports the observed current version of one platform
// component for the §25.8 full version report alongside the version that
// component is required to be at. lenny-ops wires a source per component
// it can query (the gateway over HTTP, the controller Deployment over
// the K8s API, the Postgres schema version over the connection pool);
// the aggregator flags a component whose current value does not match
// its required value as drift.
//
// Required is per-component because the components do not share one
// version space: the gateway, ops, and controller binaries are expected
// to match the platform build version, but the Postgres schema version
// is a migration counter and the CRD version is an API-version label.
// Comparison is therefore exact string equality, not SemVer precedence.
//
// spec: §25.8 Version Aggregation (line 3364) — "When any component's
// current version does not match the compiled-in required version, the
// response includes versionDrift: true".
type VersionSource interface {
	// Name is the component label in the report (e.g. "gateway", "ops",
	// "controllers", "postgres-schema").
	Name() string
	// Required is the compiled-in version this component is expected to
	// run. An empty Required disables drift detection for the component.
	Required() string
	// Current returns the component's observed version. An error marks the
	// component unavailable in the report (the §25.8 degradation model:
	// "version introspection returns partial data").
	Current(ctx context.Context) (string, error)
}

// FuncVersionSource adapts a name, a required value, and a closure into a
// VersionSource so lenny-ops can wire each component's query (HTTP call,
// SQL query, K8s lookup) without the aggregator package importing pgx,
// the K8s client, or an HTTP stack.
type FuncVersionSource struct {
	name     string
	required string
	fn       func(ctx context.Context) (string, error)
}

// NewFuncVersionSource returns a VersionSource that reports name, expects
// required, and resolves its current version via fn.
func NewFuncVersionSource(name, required string, fn func(ctx context.Context) (string, error)) FuncVersionSource {
	return FuncVersionSource{name: name, required: required, fn: fn}
}

// Name implements VersionSource.
func (s FuncVersionSource) Name() string { return s.name }

// Required implements VersionSource.
func (s FuncVersionSource) Required() string { return s.required }

// Current implements VersionSource.
func (s FuncVersionSource) Current(ctx context.Context) (string, error) {
	if s.fn == nil {
		return "", fmt.Errorf("upgradeservice: version source %q has no resolver", s.name)
	}
	return s.fn(ctx)
}

// ComponentVersion is one component's entry in the §25.8 version report.
type ComponentVersion struct {
	// Name is the component label.
	Name string `json:"name"`
	// Current is the observed version. Empty when the source was
	// unavailable.
	Current string `json:"current,omitempty"`
	// Required is the version the component is expected to run.
	Required string `json:"required,omitempty"`
	// Available reports whether the source answered. False marks a
	// degraded component per the §25.8 partial-data model.
	Available bool `json:"available"`
	// Drift is true when an available component's current version does not
	// match its required version.
	Drift bool `json:"drift"`
	// RequiredAction names the operator action a drifted component needs.
	RequiredAction string `json:"requiredAction,omitempty"`
	// Error explains why an unavailable component could not be queried.
	Error string `json:"error,omitempty"`
}

// VersionReport is the GET /v1/admin/platform/version/full response: the
// per-component versions, the overall drift flag, and the degradation
// warnings for any component whose source was unavailable.
//
// spec: §25.8 line 3364 (Version Aggregation), line 3610 (degradation).
type VersionReport struct {
	// RequiredVersion is the platform build version this lenny-ops runs
	// (the reference the binary components are expected to match).
	RequiredVersion string `json:"requiredVersion"`
	// Components is the per-component version detail, sorted by name.
	Components []ComponentVersion `json:"components"`
	// VersionDrift is true when any available component drifts from its
	// required version. spec: §25.8 ("the response includes versionDrift:
	// true").
	VersionDrift bool `json:"versionDrift"`
	// DriftCount is the number of available components in drift; it backs
	// the lenny_platform_version_drift gauge.
	DriftCount int `json:"driftCount"`
	// DegradationWarnings lists the components whose source was
	// unavailable, so an agent reading a partial report knows what is
	// missing rather than treating absence as agreement.
	DegradationWarnings []string `json:"degradationWarnings,omitempty"`
}

// DriftGauge sets the §25.8 lenny_platform_version_drift gauge to the
// count of drifted components. lenny-ops supplies a Prometheus-backed
// setter; a nil setter is a no-op.
type DriftGauge func(driftCount int)

// VersionAggregator builds the §25.8 full version report from a set of
// per-component sources.
type VersionAggregator struct {
	platformVersion string
	sources         []VersionSource
	gauge           DriftGauge
}

// VersionAggregatorOptions configures a VersionAggregator.
type VersionAggregatorOptions struct {
	// PlatformVersion is the running lenny-ops build version, reported as
	// the report header reference. Per-component required values come from
	// each source's Required.
	PlatformVersion string
	// Sources are the per-component version sources. An empty set yields a
	// report with only PlatformVersion populated.
	Sources []VersionSource
	// Gauge sets lenny_platform_version_drift after each aggregation. A
	// nil setter is a no-op.
	Gauge DriftGauge
}

// NewVersionAggregator returns a VersionAggregator over opts.
func NewVersionAggregator(opts VersionAggregatorOptions) *VersionAggregator {
	return &VersionAggregator{
		platformVersion: opts.PlatformVersion,
		sources:         opts.Sources,
		gauge:           opts.Gauge,
	}
}

// Aggregate queries every source, compares each available component's
// current version against its required version, assembles the report,
// and sets the version-drift gauge. A source error degrades that
// component to unavailable and adds a degradation warning; it never
// fails the whole report (partial data is the §25.8 contract).
//
// spec: §25.8 Version Aggregation, Degradation.
func (a *VersionAggregator) Aggregate(ctx context.Context) VersionReport {
	report := VersionReport{RequiredVersion: a.platformVersion}
	for _, src := range a.sources {
		required := src.Required()
		comp := ComponentVersion{Name: src.Name(), Required: required}
		current, err := src.Current(ctx)
		if err != nil {
			comp.Available = false
			comp.Error = err.Error()
			report.DegradationWarnings = append(report.DegradationWarnings,
				fmt.Sprintf("%s version unavailable: %s", comp.Name, err.Error()))
			report.Components = append(report.Components, comp)
			continue
		}
		comp.Available = true
		comp.Current = current
		if required != "" && strings.TrimSpace(current) != strings.TrimSpace(required) {
			comp.Drift = true
			comp.RequiredAction = fmt.Sprintf("update %s from %s to the required %s", comp.Name, current, required)
			report.DriftCount++
		}
		report.Components = append(report.Components, comp)
	}
	sort.Slice(report.Components, func(i, j int) bool {
		return report.Components[i].Name < report.Components[j].Name
	})
	report.VersionDrift = report.DriftCount > 0
	if a.gauge != nil {
		a.gauge(report.DriftCount)
	}
	return report
}
