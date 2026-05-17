// SPDX-License-Identifier: MIT

package rules_test

import (
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/recommendations/rules"
)

func goodRule() rules.Rule {
	return rules.Rule{
		Name:      "WarmPoolUndersized",
		Category:  rules.CategoryWarmPoolSizing,
		Condition: `increase(lenny_warmpool_exhausted_total[24h]) >= 3`,
		Window:    24 * time.Hour,
		Summary:   "Warm pool exhausted repeatedly",
	}
}

func TestValidateAcceptsWellFormedRule(t *testing.T) {
	if err := goodRule().Validate(); err != nil {
		t.Errorf("a well-formed rule was rejected: %v", err)
	}
}

func TestValidateRejectsViolations(t *testing.T) {
	cases := map[string]func(*rules.Rule){
		"missing name":        func(r *rules.Rule) { r.Name = "" },
		"unknown category":    func(r *rules.Rule) { r.Category = "scaling_things" },
		"missing condition":   func(r *rules.Rule) { r.Condition = "" },
		"malformed promql":    func(r *rules.Rule) { r.Condition = "this is (not promql" },
		"non-positive window": func(r *rules.Rule) { r.Window = 0 },
		"missing summary":     func(r *rules.Rule) { r.Summary = "" },
	}
	for name, mutate := range cases {
		r := goodRule()
		mutate(&r)
		if err := r.Validate(); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
}

func TestValidationErrorIsTyped(t *testing.T) {
	r := goodRule()
	r.Condition = ""
	err := r.Validate()
	var ve *rules.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("Validate must return a *ValidationError, got %T", err)
	}
	if ve.Rule != r.Name || len(ve.Violations) == 0 {
		t.Errorf("ValidationError not populated: %+v", ve)
	}
}

func TestCategoryIsValid(t *testing.T) {
	for _, c := range rules.AllCategories() {
		if !c.IsValid() {
			t.Errorf("category %q from the closed enum reports invalid", c)
		}
	}
	if rules.Category("misc").IsValid() {
		t.Error("an unknown category must report invalid")
	}
}

func TestCatalogRulesAllValidate(t *testing.T) {
	cat := rules.Catalog()
	if len(cat) == 0 {
		t.Fatal("Catalog must ship a representative sample of rules")
	}
	seen := map[string]bool{}
	for _, r := range cat {
		if err := r.Validate(); err != nil {
			t.Errorf("cataloged rule %q does not validate: %v", r.Name, err)
		}
		if seen[r.Name] {
			t.Errorf("duplicate rule name in the catalog: %q", r.Name)
		}
		seen[r.Name] = true
	}
}
