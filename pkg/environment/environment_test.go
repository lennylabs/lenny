// SPDX-License-Identifier: MIT

package environment

import "testing"

func TestAllRolesIsExhaustive(t *testing.T) {
	if got := len(AllRoles()); got != 4 {
		t.Errorf("AllRoles() returned %d, want 4 per §10.6", got)
	}
}

func TestRoleAtLeastOrder(t *testing.T) {
	if !RoleAdmin.AtLeast(RoleViewer) {
		t.Errorf("admin >= viewer")
	}
	if !RoleAdmin.AtLeast(RoleAdmin) {
		t.Errorf("admin >= admin")
	}
	if RoleViewer.AtLeast(RoleCreator) {
		t.Errorf("viewer must not be >= creator")
	}
	if !RoleOperator.AtLeast(RoleCreator) {
		t.Errorf("operator >= creator")
	}
}

func TestAllLabelOperatorsIsExhaustive(t *testing.T) {
	if got := len(AllLabelOperators()); got != 4 {
		t.Errorf("AllLabelOperators() returned %d, want 4", got)
	}
}

func TestRequirementValidateRejectsBadShape(t *testing.T) {
	cases := []Requirement{
		{Key: "", Operator: OpIn, Values: []string{"a"}},
		{Key: "team", Operator: "BADOP", Values: []string{"a"}},
		{Key: "team", Operator: OpIn, Values: nil},               // In requires values
		{Key: "team", Operator: OpExists, Values: []string{"a"}}, // Exists rejects values
	}
	for _, r := range cases {
		if err := r.Validate(); err == nil {
			t.Errorf("Requirement(%+v) should fail validation", r)
		}
	}
}

func TestRequirementValidateAcceptsCanonicalForms(t *testing.T) {
	cases := []Requirement{
		{Key: "team", Operator: OpIn, Values: []string{"security"}},
		{Key: "team", Operator: OpNotIn, Values: []string{"dev"}},
		{Key: "approved", Operator: OpExists},
		{Key: "deprecated", Operator: OpDoesNotExist},
	}
	for _, r := range cases {
		if err := r.Validate(); err != nil {
			t.Errorf("Requirement(%+v) should validate, got %v", r, err)
		}
	}
}

func TestSelectorMatchLabelsExactMatch(t *testing.T) {
	s := Selector{MatchLabels: map[string]string{"team": "security"}}
	if !s.Matches(Candidate{Labels: map[string]string{"team": "security"}}) {
		t.Errorf("exact match must admit")
	}
	if s.Matches(Candidate{Labels: map[string]string{"team": "dev"}}) {
		t.Errorf("mismatched value must reject")
	}
	if s.Matches(Candidate{Labels: map[string]string{}}) {
		t.Errorf("missing label must reject")
	}
}

func TestSelectorMatchExpressionsIn(t *testing.T) {
	s := Selector{
		MatchExpressions: []Requirement{
			{Key: "team", Operator: OpIn, Values: []string{"security", "platform"}},
		},
	}
	if !s.Matches(Candidate{Labels: map[string]string{"team": "security"}}) {
		t.Errorf("In must match member value")
	}
	if !s.Matches(Candidate{Labels: map[string]string{"team": "platform"}}) {
		t.Errorf("In must match member value (2)")
	}
	if s.Matches(Candidate{Labels: map[string]string{"team": "dev"}}) {
		t.Errorf("In must reject non-member value")
	}
	if s.Matches(Candidate{Labels: map[string]string{}}) {
		t.Errorf("In must reject missing key")
	}
}

func TestSelectorMatchExpressionsNotIn(t *testing.T) {
	s := Selector{
		MatchExpressions: []Requirement{
			{Key: "tier", Operator: OpNotIn, Values: []string{"experimental"}},
		},
	}
	if !s.Matches(Candidate{Labels: map[string]string{"tier": "stable"}}) {
		t.Errorf("NotIn must admit non-member value")
	}
	if !s.Matches(Candidate{Labels: map[string]string{}}) {
		t.Errorf("NotIn must admit when key is absent")
	}
	if s.Matches(Candidate{Labels: map[string]string{"tier": "experimental"}}) {
		t.Errorf("NotIn must reject member value")
	}
}

func TestSelectorMatchExpressionsExists(t *testing.T) {
	s := Selector{MatchExpressions: []Requirement{{Key: "approved", Operator: OpExists}}}
	if !s.Matches(Candidate{Labels: map[string]string{"approved": "true"}}) {
		t.Errorf("Exists must admit when key is present")
	}
	if !s.Matches(Candidate{Labels: map[string]string{"approved": ""}}) {
		t.Errorf("Exists must admit even when value is empty (key present)")
	}
	if s.Matches(Candidate{Labels: map[string]string{}}) {
		t.Errorf("Exists must reject when key is missing")
	}
}

func TestSelectorMatchExpressionsDoesNotExist(t *testing.T) {
	s := Selector{MatchExpressions: []Requirement{{Key: "deprecated", Operator: OpDoesNotExist}}}
	if !s.Matches(Candidate{Labels: map[string]string{}}) {
		t.Errorf("DoesNotExist must admit when key is missing")
	}
	if s.Matches(Candidate{Labels: map[string]string{"deprecated": "true"}}) {
		t.Errorf("DoesNotExist must reject when key is present")
	}
}

func TestSelectorTypesFilter(t *testing.T) {
	s := Selector{Types: []string{"agent", "mcp"}}
	if !s.Matches(Candidate{Type: "agent"}) {
		t.Errorf("type in list must admit")
	}
	if s.Matches(Candidate{Type: "tool"}) {
		t.Errorf("type not in list must reject")
	}
}

func TestSelectorExcludeOverridesMatchLabels(t *testing.T) {
	s := Selector{
		MatchLabels: map[string]string{"team": "security"},
		Exclude:     []string{"unstable-scanner"},
	}
	if s.Matches(Candidate{Name: "unstable-scanner", Labels: map[string]string{"team": "security"}}) {
		t.Errorf("exclude must override matching labels")
	}
}

func TestSelectorIncludeOverridesSelector(t *testing.T) {
	s := Selector{
		MatchLabels: map[string]string{"team": "security"},
		Include:     []string{"legacy-code-auditor"},
	}
	if !s.Matches(Candidate{Name: "legacy-code-auditor", Labels: map[string]string{"team": "other"}}) {
		t.Errorf("include must admit even when matchLabels would reject")
	}
}

func TestSelectorExcludeBeatsInclude(t *testing.T) {
	// §10.6 doesn't explicitly resolve include+exclude on the same
	// name, but the safest deterministic behaviour is exclude wins
	// because a deployer adding a name to exclude is making an
	// explicit deny statement.
	s := Selector{
		Include: []string{"x"},
		Exclude: []string{"x"},
	}
	if s.Matches(Candidate{Name: "x"}) {
		t.Errorf("exclude must beat include for the same name")
	}
}

func TestFilterReturnsAdmittedSubset(t *testing.T) {
	s := Selector{
		MatchLabels: map[string]string{"team": "security"},
		Exclude:     []string{"unstable-scanner"},
	}
	cs := []Candidate{
		{Name: "a", Labels: map[string]string{"team": "security"}},
		{Name: "b", Labels: map[string]string{"team": "dev"}},
		{Name: "unstable-scanner", Labels: map[string]string{"team": "security"}},
	}
	got := s.Filter(cs)
	if len(got) != 1 || got[0].Name != "a" {
		t.Errorf("Filter: want [a], got %+v", got)
	}
}

func TestSelectorValidatePropagatesRequirementErrors(t *testing.T) {
	s := Selector{
		MatchExpressions: []Requirement{
			{Key: "", Operator: OpIn, Values: []string{"a"}},
		},
	}
	if err := s.Validate(); err == nil {
		t.Errorf("invalid requirement must propagate")
	}
}

func TestSelectorMatchesEmptyOnEmpty(t *testing.T) {
	// Empty selector matches every candidate by the AND-of-clauses
	// semantics K8s uses.
	s := Selector{}
	if !s.Matches(Candidate{Name: "anything"}) {
		t.Errorf("empty selector should admit every candidate")
	}
}
