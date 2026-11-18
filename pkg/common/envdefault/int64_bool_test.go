// SPDX-License-Identifier: MIT

package envdefault

import "testing"

// The helpers are flag plumbing that no requirement states, so these tests
// carry no specification annotation.

func TestInt64ReadsTheVariableAndFallsBackOnUnsetBlankOrMalformed(t *testing.T) {
	const name = "LENNY_ENVDEFAULT_TEST_INT64"
	if got := Int64(name, 9); got != 9 {
		t.Errorf("Int64 unset = %d, want 9", got)
	}
	cases := []struct {
		value string
		want  int64
	}{
		{"", 9},
		{"  ", 9},
		{"68719476736", 68719476736},
		{" -4 ", -4},
		{"lots", 9},
		{"1.5", 9},
	}
	for _, c := range cases {
		t.Setenv(name, c.value)
		if got := Int64(name, 9); got != c.want {
			t.Errorf("Int64 with %q = %d, want %d", c.value, got, c.want)
		}
	}
}

func TestBoolReadsTruthyAndFalsyValuesAndFallsBackOtherwise(t *testing.T) {
	const name = "LENNY_ENVDEFAULT_TEST_BOOL"
	if got := Bool(name, true); !got {
		t.Error("Bool unset with default true = false")
	}
	cases := []struct {
		value string
		def   bool
		want  bool
	}{
		{"1", false, true},
		{" TRUE ", false, true},
		{"yes", false, true},
		{"On", false, true},
		{"0", true, false},
		{"false", true, false},
		{"NO", true, false},
		{"off", true, false},
		{"", true, true},
		{"maybe", false, false},
		{"maybe", true, true},
	}
	for _, c := range cases {
		t.Setenv(name, c.value)
		if got := Bool(name, c.def); got != c.want {
			t.Errorf("Bool with %q and default %v = %v, want %v", c.value, c.def, got, c.want)
		}
	}
}
