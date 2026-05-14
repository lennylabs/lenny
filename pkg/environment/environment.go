// SPDX-License-Identifier: MIT

// Package environment encodes the §10.6 Environment Resource and
// RBAC Model primitives that are pure: the member-role enum and the
// K8s-style LabelSelector evaluator (matchLabels + matchExpressions)
// with include/exclude overrides.
//
// The admin API surface (POST /v1/admin/environments, GET, PUT, etc.),
// the transparent-filtering middleware, the explicit-environment
// endpoint dispatcher, the OIDC-group resolver, and the cross-
// environment delegation policy resolver all live in later phases
// that import this package.
package environment

import "fmt"

// Role is the §10.6 member-role enum.
type Role string

const (
	// RoleViewer: read-only access scoped to this environment.
	RoleViewer Role = "viewer"
	// RoleCreator: can create sessions in the environment.
	RoleCreator Role = "creator"
	// RoleOperator: viewer + creator + operational tooling (drain,
	// scale-down) on the environment's pool.
	RoleOperator Role = "operator"
	// RoleAdmin: full environment administration including member
	// management and policy edits.
	RoleAdmin Role = "admin"
)

// AllRoles returns the closed §10.6 enum in escalation order
// (lowest to highest privilege).
func AllRoles() []Role {
	return []Role{RoleViewer, RoleCreator, RoleOperator, RoleAdmin}
}

// IsValid reports whether r is one of the four §10.6 roles.
func (r Role) IsValid() bool {
	for _, v := range AllRoles() {
		if r == v {
			return true
		}
	}
	return false
}

// AtLeast reports whether the role is at least as privileged as
// other. Uses the canonical escalation order: viewer < creator <
// operator < admin.
func (r Role) AtLeast(other Role) bool {
	return roleRank(r) >= roleRank(other)
}

func roleRank(r Role) int {
	switch r {
	case RoleViewer:
		return 0
	case RoleCreator:
		return 1
	case RoleOperator:
		return 2
	case RoleAdmin:
		return 3
	}
	return -1
}

// LabelOperator is the §10.6 matchExpressions operator enum. Mirrors
// the K8s metav1.LabelSelectorOperator set.
type LabelOperator string

const (
	OpIn           LabelOperator = "In"
	OpNotIn        LabelOperator = "NotIn"
	OpExists       LabelOperator = "Exists"
	OpDoesNotExist LabelOperator = "DoesNotExist"
)

// AllLabelOperators returns the closed enum.
func AllLabelOperators() []LabelOperator {
	return []LabelOperator{OpIn, OpNotIn, OpExists, OpDoesNotExist}
}

// IsValid reports whether op is one of the four supported operators.
func (op LabelOperator) IsValid() bool {
	for _, v := range AllLabelOperators() {
		if op == v {
			return true
		}
	}
	return false
}

// Requirement is one matchExpressions entry.
type Requirement struct {
	Key      string
	Operator LabelOperator
	Values   []string
}

// Validate reports the §10.6 requirement-shape errors.
func (r Requirement) Validate() error {
	if r.Key == "" {
		return fmt.Errorf("environment: requirement.key is required")
	}
	if !r.Operator.IsValid() {
		return fmt.Errorf("environment: requirement.operator %q is not in %v", r.Operator, AllLabelOperators())
	}
	switch r.Operator {
	case OpIn, OpNotIn:
		if len(r.Values) == 0 {
			return fmt.Errorf("environment: requirement.values is required for operator %q", r.Operator)
		}
	case OpExists, OpDoesNotExist:
		if len(r.Values) != 0 {
			return fmt.Errorf("environment: requirement.values must be empty for operator %q", r.Operator)
		}
	}
	return nil
}

// Selector mirrors a K8s metav1.LabelSelector. The §10.6 spec adds
// `types`, `include`, and `exclude` overrides that wrap the label
// selector with type-list filtering and explicit allowlist/denylist
// tail layers.
type Selector struct {
	MatchLabels      map[string]string
	MatchExpressions []Requirement

	// Types is the optional §10.6 type filter (e.g., {"agent", "mcp"}).
	// Empty means no type filter is applied.
	Types []string

	// Include force-admits these named runtimes/connectors regardless
	// of label-selector outcome (§10.6 override).
	Include []string

	// Exclude force-rejects these named runtimes/connectors even
	// when the label selector would admit them (§10.6 override).
	Exclude []string
}

// Validate reports the §10.6 admission-time selector errors. An
// empty MatchLabels + empty MatchExpressions is permitted (matches
// everything by selector), but combined with empty Include/Exclude
// on an empty Types this means the environment selects everything —
// callers that want to reject this should layer their own check.
func (s Selector) Validate() error {
	for i, req := range s.MatchExpressions {
		if err := req.Validate(); err != nil {
			return fmt.Errorf("environment: matchExpressions[%d]: %w", i, err)
		}
	}
	return nil
}

// Candidate is the input to Matches: the named resource the
// environment is filtering (a runtime, a connector). Type may be
// empty when types filtering is disabled.
type Candidate struct {
	Name   string
	Type   string
	Labels map[string]string
}

// Matches applies the §10.6 selector + override layers to a
// candidate. The evaluation order matches the spec text:
//
//	1. Exclude → if c.Name is on the Exclude list, reject.
//	2. Include → if c.Name is on the Include list, admit.
//	3. Types → if Types is non-empty and c.Type is not in it, reject.
//	4. MatchLabels → every (key, value) MUST be present on c.Labels.
//	5. MatchExpressions → every requirement MUST be satisfied.
//
// Returns true when every layer admits.
func (s Selector) Matches(c Candidate) bool {
	for _, ex := range s.Exclude {
		if ex == c.Name {
			return false
		}
	}
	for _, in := range s.Include {
		if in == c.Name {
			return true
		}
	}
	if len(s.Types) > 0 {
		ok := false
		for _, t := range s.Types {
			if t == c.Type {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	for k, want := range s.MatchLabels {
		if got, ok := c.Labels[k]; !ok || got != want {
			return false
		}
	}
	for _, req := range s.MatchExpressions {
		if !req.matches(c.Labels) {
			return false
		}
	}
	return true
}

// matches evaluates a single matchExpressions requirement against
// the candidate's labels.
func (r Requirement) matches(labels map[string]string) bool {
	value, present := labels[r.Key]
	switch r.Operator {
	case OpIn:
		if !present {
			return false
		}
		for _, v := range r.Values {
			if v == value {
				return true
			}
		}
		return false
	case OpNotIn:
		if !present {
			return true
		}
		for _, v := range r.Values {
			if v == value {
				return false
			}
		}
		return true
	case OpExists:
		return present
	case OpDoesNotExist:
		return !present
	}
	return false
}

// Filter returns the subset of candidates the selector admits. Useful
// for the §10.6 transparent-filtering path that returns the union of
// authorized runtimes across the user's environments.
func (s Selector) Filter(candidates []Candidate) []Candidate {
	out := make([]Candidate, 0, len(candidates))
	for _, c := range candidates {
		if s.Matches(c) {
			out = append(out, c)
		}
	}
	return out
}
