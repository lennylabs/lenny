// SPDX-License-Identifier: MIT

// Package metrics wraps prometheus/client_golang with the label hygiene
// rules from spec §16.1.1.
//
// The package enforces three rules at metric registration time:
//
//  1. No label name in the forbidden list of high-cardinality attributes
//     (session_id, operation_id, root_session_id, credential_id, user_id,
//     lease_id, request_id) per §16.1.1 last paragraph.
//  2. Every metric name starts with the `lenny_` prefix used throughout
//     §16.1.
//  3. Every Lenny-specific label name uses snake_case (no dots, no caps).
//
// Violations return a ValidationError at registration time so a CI run of
// the unit tests catches the bad metric before it ships. Production code
// SHOULD register metrics during package init and treat ValidationError as
// a panic-worthy programming error.
package metrics

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

// ForbiddenLabels lists the high-cardinality attribute names that §16.1.1
// forbids as Prometheus labels. Tests use this list to verify the rule
// stays in sync with the spec; callers should not mutate the slice.
var ForbiddenLabels = []string{
	"session_id",
	"operation_id",
	"root_session_id",
	"credential_id",
	"user_id",
	"lease_id",
	"request_id",
}

// ValidationError is returned by every metric constructor in this package
// when the requested name or labels violate the §16.1.1 rules. It wraps a
// list of specific violations so a single registration with multiple
// problems surfaces them all.
type ValidationError struct {
	Metric     string
	Violations []string
}

func (e *ValidationError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if len(e.Violations) == 0 {
		return fmt.Sprintf("metric %q: validation failed", e.Metric)
	}
	return fmt.Sprintf("metric %q: %s", e.Metric, strings.Join(e.Violations, "; "))
}

var (
	// metricNamePattern is intentionally narrower than Prometheus's grammar:
	// every Lenny metric starts with the lenny_ prefix per §16.1.
	metricNamePattern = regexp.MustCompile(`^lenny_[a-z][a-z0-9_]*[a-z0-9]$`)

	// labelNamePattern matches the snake_case form used throughout §16.1.1.
	labelNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// Validate reports the §16.1.1 violations for a metric name and label set.
// Returns nil when the metric satisfies every rule. Callers that build
// their own collectors (custom Summary, native histograms, etc.) should
// call Validate before registering.
func Validate(name string, labels []string) error {
	v := []string{}
	if !metricNamePattern.MatchString(name) {
		v = append(v, fmt.Sprintf("name %q does not match required form `lenny_<snake_case>`", name))
	}
	for _, label := range labels {
		if !labelNamePattern.MatchString(label) {
			v = append(v, fmt.Sprintf("label %q is not snake_case", label))
			continue
		}
		for _, forbidden := range ForbiddenLabels {
			if label == forbidden {
				v = append(v, fmt.Sprintf("label %q is on the §16.1.1 forbidden high-cardinality list", label))
			}
		}
	}
	if len(v) == 0 {
		return nil
	}
	return &ValidationError{Metric: name, Violations: v}
}

// NewCounter constructs a labelled Counter metric. Returns the metric and
// ValidationError when the name or labels violate the spec rules.
func NewCounter(opts prometheus.CounterOpts, labels []string) (*prometheus.CounterVec, error) {
	if err := Validate(opts.Name, labels); err != nil {
		return nil, err
	}
	return prometheus.NewCounterVec(opts, labels), nil
}

// NewGauge constructs a labelled Gauge metric. Returns the metric and
// ValidationError when the name or labels violate the spec rules.
func NewGauge(opts prometheus.GaugeOpts, labels []string) (*prometheus.GaugeVec, error) {
	if err := Validate(opts.Name, labels); err != nil {
		return nil, err
	}
	return prometheus.NewGaugeVec(opts, labels), nil
}

// NewHistogram constructs a labelled Histogram metric. Returns the metric
// and ValidationError when the name or labels violate the spec rules.
func NewHistogram(opts prometheus.HistogramOpts, labels []string) (*prometheus.HistogramVec, error) {
	if err := Validate(opts.Name, labels); err != nil {
		return nil, err
	}
	return prometheus.NewHistogramVec(opts, labels), nil
}

// MustRegister mirrors prometheus.MustRegister but reports a panic with a
// well-formed message that names the metric whose registration failed.
// Use this in package init.
func MustRegister(reg prometheus.Registerer, c prometheus.Collector) {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	if err := reg.Register(c); err != nil {
		// Already-registered is the common case in tests; treat it as a
		// no-op so multiple test files in a package can share metric
		// declarations safely.
		var are prometheus.AlreadyRegisteredError
		if errors.As(err, &are) {
			return
		}
		panic(fmt.Sprintf("metrics: register: %v", err))
	}
}
