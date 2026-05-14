// SPDX-License-Identifier: MIT

package metrics

import (
	"errors"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestValidateAcceptsCanonicalNames(t *testing.T) {
	cases := []string{
		"lenny_gateway_active_sessions",
		"lenny_warmpool_idle_pods",
		"lenny_session_startup_duration_seconds",
	}
	for _, name := range cases {
		if err := Validate(name, []string{"pool", "tenant_id"}); err != nil {
			t.Errorf("name %q should validate, got %v", name, err)
		}
	}
}

func TestValidateRejectsWrongPrefix(t *testing.T) {
	err := Validate("gateway_active_sessions", []string{"pool"})
	if err == nil {
		t.Fatal("name without lenny_ prefix should be rejected")
	}
	if !strings.Contains(err.Error(), "lenny_") {
		t.Errorf("error should mention the lenny_ prefix, got %v", err)
	}
}

func TestValidateRejectsForbiddenLabels(t *testing.T) {
	for _, label := range ForbiddenLabels {
		err := Validate("lenny_session_duration_seconds", []string{label})
		if err == nil {
			t.Errorf("label %q should be rejected by §16.1.1 high-cardinality rule", label)
			continue
		}
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("expected ValidationError, got %T (%v)", err, err)
			continue
		}
		joined := strings.Join(ve.Violations, "|")
		if !strings.Contains(joined, "high-cardinality") {
			t.Errorf("violation should reference §16.1.1 high-cardinality rule, got %q", joined)
		}
	}
}

func TestValidateAccumulatesMultipleViolations(t *testing.T) {
	err := Validate("not_prefixed", []string{"session_id", "BadLabel"})
	if err == nil {
		t.Fatal("expected violations")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %T", err)
	}
	if len(ve.Violations) < 2 {
		t.Errorf("expected at least 2 violations, got %d: %v", len(ve.Violations), ve.Violations)
	}
}

func TestValidateRejectsNonSnakeCaseLabels(t *testing.T) {
	err := Validate("lenny_session_duration_seconds", []string{"PoolName"})
	if err == nil {
		t.Fatal("non-snake-case label should be rejected")
	}
}

func TestNewCounterPropagatesValidationError(t *testing.T) {
	_, err := NewCounter(prometheus.CounterOpts{Name: "lenny_session_total"}, []string{"session_id"})
	if err == nil {
		t.Fatal("NewCounter with forbidden label should error")
	}
}

func TestNewCounterReturnsUsableCollector(t *testing.T) {
	c, err := NewCounter(
		prometheus.CounterOpts{Name: "lenny_session_total", Help: "test"},
		[]string{"pool", "tenant_id"},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c.WithLabelValues("default", "acme").Inc()
}

func TestNewGaugeRejectsForbiddenLabel(t *testing.T) {
	_, err := NewGauge(prometheus.GaugeOpts{Name: "lenny_active"}, []string{"user_id"})
	if err == nil {
		t.Fatal("NewGauge with user_id label should error")
	}
}

func TestNewHistogramReturnsUsableCollector(t *testing.T) {
	h, err := NewHistogram(
		prometheus.HistogramOpts{Name: "lenny_session_startup_duration_seconds", Help: "test"},
		[]string{"pool"},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	h.WithLabelValues("default").Observe(0.5)
}

// MustRegister panics on programming errors but is a no-op for the
// already-registered case so test files can share definitions.
func TestMustRegisterIgnoresAlreadyRegistered(t *testing.T) {
	reg := prometheus.NewRegistry()
	c, err := NewCounter(prometheus.CounterOpts{Name: "lenny_dup_total", Help: "h"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	MustRegister(reg, c)
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("MustRegister should ignore AlreadyRegisteredError, panicked with %v", r)
		}
	}()
	MustRegister(reg, c)
}

// ForbiddenLabels list must stay aligned with §16.1.1.
func TestForbiddenLabelsCatalog(t *testing.T) {
	expected := map[string]bool{
		"session_id":      true,
		"operation_id":    true,
		"root_session_id": true,
		"credential_id":   true,
		"user_id":         true,
		"lease_id":        true,
		"request_id":      true,
	}
	if len(ForbiddenLabels) != len(expected) {
		t.Errorf("ForbiddenLabels count %d does not match expected %d", len(ForbiddenLabels), len(expected))
	}
	for _, l := range ForbiddenLabels {
		if !expected[l] {
			t.Errorf("ForbiddenLabels contains unexpected entry %q", l)
		}
	}
}
