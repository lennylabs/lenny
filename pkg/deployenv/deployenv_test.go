// SPDX-License-Identifier: MIT

package deployenv

import (
	"strings"
	"testing"
)

// spec: 17.9.1 (Composition Dimensions)
// Validate accepts the unset value and the exact members of the closed set,
// refuses every other value with an error naming it, and IsProduction holds
// only for the exact value prod. The PROD and " prod" rows pin the exact-match
// rule: a case-folding or trimming implementation fails them.
func TestValidateAndIsProductionExactClosedSet_spec_17_9_1(t *testing.T) {
	cases := []struct {
		value      string
		valid      bool
		production bool
	}{
		{"local", true, false},
		{"dev", true, false},
		{"staging", true, false},
		{"prod", true, true},
		{"", true, false},
		{"production", false, false},
		{"Prod", false, false},
		{"PROD", false, false},
		{" prod", false, false},
		{"prod ", false, false},
		{"stage", false, false},
		{"qa", false, false},
	}
	for _, c := range cases {
		t.Run(c.value, func(t *testing.T) {
			err := Validate(c.value)
			switch {
			case c.valid && err != nil:
				t.Errorf("Validate(%q) = %v, want nil", c.value, err)
			case !c.valid && err == nil:
				t.Errorf("Validate(%q) = nil, want an error", c.value)
			case !c.valid && !strings.Contains(err.Error(), `"`+c.value+`"`):
				t.Errorf("Validate(%q) error %q does not name the value", c.value, err)
			}
			if got := IsProduction(c.value); got != c.production {
				t.Errorf("IsProduction(%q) = %v, want %v", c.value, got, c.production)
			}
		})
	}
}
