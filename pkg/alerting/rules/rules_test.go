// SPDX-License-Identifier: MIT

package rules

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSeverityIsValid(t *testing.T) {
	cases := map[Severity]bool{
		SeverityCritical:    true,
		SeverityWarning:     true,
		Severity(""):        false,
		Severity("page"):    false,
		Severity("CRITICAL"): false,
	}
	for s, want := range cases {
		if got := s.IsValid(); got != want {
			t.Errorf("Severity(%q).IsValid() = %v, want %v", s, got, want)
		}
	}
}

func TestRuleValidateAcceptsWellFormed(t *testing.T) {
	r := Rule{
		Name:       "Example",
		Expr:       `up == 0`,
		For:        30 * time.Second,
		Severity:   SeverityWarning,
		Summary:    "test alert",
		SpecRef:    "§16.5",
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate should accept well-formed rule, got %v", err)
	}
}

func TestRuleValidateRejectsMissingFields(t *testing.T) {
	cases := []struct {
		name string
		rule Rule
		want string
	}{
		{"missing name", Rule{Expr: "up == 0", Severity: SeverityWarning, Summary: "s"}, "Name is required"},
		{"missing expr", Rule{Name: "X", Severity: SeverityWarning, Summary: "s"}, "Expr is required"},
		{"bad severity", Rule{Name: "X", Expr: "up == 0", Severity: "bogus", Summary: "s"}, "Severity"},
		{"missing summary", Rule{Name: "X", Expr: "up == 0", Severity: SeverityWarning}, "Summary is required"},
		{"negative for", Rule{Name: "X", Expr: "up == 0", For: -1, Severity: SeverityWarning, Summary: "s"}, "For must be non-negative"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.rule.Validate()
			if err == nil {
				t.Fatal("Validate should have rejected the rule")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error should mention %q, got %v", c.want, err)
			}
		})
	}
}

func TestRuleValidateRejectsBadPromQL(t *testing.T) {
	r := Rule{
		Name:     "X",
		Expr:     "up ===",
		Severity: SeverityWarning,
		Summary:  "s",
	}
	err := r.Validate()
	if err == nil {
		t.Fatal("Validate should reject malformed PromQL")
	}
	if !strings.Contains(err.Error(), "PromQL") {
		t.Errorf("error should mention PromQL, got %v", err)
	}
}

func TestRuleValidateCriticalRequiresRunbook(t *testing.T) {
	r := Rule{
		Name:     "X",
		Expr:     "up == 0",
		Severity: SeverityCritical,
		Summary:  "s",
	}
	err := r.Validate()
	if err == nil {
		t.Fatal("critical alert without runbook should be rejected")
	}
	if !strings.Contains(err.Error(), "RunbookURL") {
		t.Errorf("error should mention RunbookURL, got %v", err)
	}
}

func TestValidationErrorIsRetrievable(t *testing.T) {
	r := Rule{Name: "X"}
	err := r.Validate()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if ve.Rule != "X" {
		t.Errorf("ValidationError.Rule: want X, got %q", ve.Rule)
	}
	if len(ve.Violations) == 0 {
		t.Errorf("ValidationError should list violations")
	}
}

func TestCatalogValidates(t *testing.T) {
	c := Catalog()
	if len(c) == 0 {
		t.Fatal("Catalog should not be empty")
	}
	names := map[string]bool{}
	for _, r := range c {
		if err := r.Validate(); err != nil {
			t.Errorf("Catalog rule %q fails Validate: %v", r.Name, err)
		}
		if names[r.Name] {
			t.Errorf("duplicate rule name %q in Catalog", r.Name)
		}
		names[r.Name] = true
	}
}

func TestCatalogCoversCanonicalAlerts(t *testing.T) {
	want := []string{
		"WarmPoolExhausted",
		"PostgresReplicationLagHigh",
		"CredentialPoolLow",
	}
	got := map[string]bool{}
	for _, r := range Catalog() {
		got[r.Name] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("Catalog missing canonical rule %q", w)
		}
	}
}
