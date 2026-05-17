// SPDX-License-Identifier: MIT

package experiment

import "testing"

func TestResolveExternalVariantFromVariantField(t *testing.T) {
	got, known := ResolveExternalVariant("treatment", nil, []string{"treatment", "holdback"})
	if got != "treatment" || !known {
		t.Errorf("ResolveExternalVariant = (%q, %v), want (treatment, true)", got, known)
	}
}

func TestResolveExternalVariantVariantTakesPrecedenceOverValue(t *testing.T) {
	got, known := ResolveExternalVariant("treatment", "holdback", []string{"treatment", "holdback"})
	if got != "treatment" || !known {
		t.Errorf("ResolveExternalVariant = (%q, %v), want (treatment, true) — Variant wins over Value", got, known)
	}
}

func TestResolveExternalVariantFromStringValue(t *testing.T) {
	got, known := ResolveExternalVariant("", "holdback", []string{"treatment", "holdback"})
	if got != "holdback" || !known {
		t.Errorf("ResolveExternalVariant = (%q, %v), want (holdback, true)", got, known)
	}
}

func TestResolveExternalVariantFromObjectValue(t *testing.T) {
	value := map[string]any{"variant_id": "treatment", "other": 1}
	got, known := ResolveExternalVariant("", value, []string{"treatment"})
	if got != "treatment" || !known {
		t.Errorf("ResolveExternalVariant = (%q, %v), want (treatment, true)", got, known)
	}
}

func TestResolveExternalVariantControlIsAlwaysKnown(t *testing.T) {
	// The provider may legitimately assign the reserved control variant.
	got, known := ResolveExternalVariant(ControlVariantID, nil, []string{"treatment"})
	if got != ControlVariantID || !known {
		t.Errorf("ResolveExternalVariant(control) = (%q, %v), want (control, true)", got, known)
	}
}

func TestResolveExternalVariantUnregisteredCandidate(t *testing.T) {
	// A candidate the experiment does not register is unresolvable.
	got, known := ResolveExternalVariant("ghost", nil, []string{"treatment"})
	if got != ControlVariantID || known {
		t.Errorf("ResolveExternalVariant(unregistered) = (%q, %v), want (control, false)", got, known)
	}
}

func TestResolveExternalVariantNoCandidate(t *testing.T) {
	cases := []struct {
		name    string
		variant string
		value   any
	}{
		{"empty variant and nil value", "", nil},
		{"object without variant_id", "", map[string]any{"flag": true}},
		{"object with non-string variant_id", "", map[string]any{"variant_id": 7}},
		{"numeric value", "", 42},
		{"bool value", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, known := ResolveExternalVariant(tc.variant, tc.value, []string{"treatment"})
			if got != ControlVariantID || known {
				t.Errorf("ResolveExternalVariant = (%q, %v), want (control, false)", got, known)
			}
		})
	}
}
