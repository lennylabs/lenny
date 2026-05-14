// SPDX-License-Identifier: MIT

package environment

import (
	"testing"
)

// FuzzSelectorMatches exercises the §10.6 LabelSelector evaluator
// on arbitrary (key, operator, values) requirements + candidate
// labels. Invariant: Matches never panics.
func FuzzSelectorMatches(f *testing.F) {
	f.Add("env", "In", "prod,staging", "env=prod")
	f.Add("", "", "", "")
	f.Add("kind", "DoesNotExist", "", "kind=session")
	f.Add("region", "NotIn", "us-east,us-west", "region=eu-west")

	f.Fuzz(func(t *testing.T, key, op, csv, candidateCSV string) {
		req := Requirement{
			Key:      key,
			Operator: LabelOperator(op),
			Values:   splitCSV(csv),
		}
		if err := req.Validate(); err != nil {
			return
		}
		s := Selector{MatchExpressions: []Requirement{req}}
		c := Candidate{Labels: parseLabels(candidateCSV)}
		_ = s.Matches(c)
	})
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	out := []string{}
	cur := ""
	for _, c := range s {
		if c == ',' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(c)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func parseLabels(s string) map[string]string {
	out := map[string]string{}
	for _, pair := range splitCSV(s) {
		eq := -1
		for i, c := range pair {
			if c == '=' {
				eq = i
				break
			}
		}
		if eq <= 0 {
			continue
		}
		out[pair[:eq]] = pair[eq+1:]
	}
	return out
}
