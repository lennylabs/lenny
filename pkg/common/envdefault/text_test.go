// SPDX-License-Identifier: MIT

package envdefault

import "testing"

// The helpers are flag plumbing that no requirement states, so these tests
// carry no specification annotation.

func TestStringReadsTheTrimmedVariableAndFallsBackOnUnsetOrBlank(t *testing.T) {
	const name = "LENNY_ENVDEFAULT_TEST_STRING"
	t.Setenv(name, "")
	if got := String(name, "def"); got != "def" {
		t.Errorf("String with a blank variable = %q, want the default", got)
	}
	t.Setenv(name, "  oidc ")
	if got := String(name, "def"); got != "oidc" {
		t.Errorf("String = %q, want the trimmed value", got)
	}
	if got := String("LENNY_ENVDEFAULT_TEST_STRING_UNSET", "def"); got != "def" {
		t.Errorf("String with an unset variable = %q, want the default", got)
	}
}

func TestFlagReadsOnlyTheSwitchOnWords(t *testing.T) {
	const name = "LENNY_ENVDEFAULT_TEST_FLAG"
	for value, want := range map[string]bool{
		"1": true, "true": true, "YES": true, " on ": true,
		"0": false, "false": false, "": false, "enabled": false,
	} {
		t.Setenv(name, value)
		if got := Flag(name); got != want {
			t.Errorf("Flag(%q) = %v, want %v", value, got, want)
		}
	}
	if Flag("LENNY_ENVDEFAULT_TEST_FLAG_UNSET") {
		t.Error("Flag with an unset variable = true, want false")
	}
}

func TestParseBoolParsesTheVariableAndFallsBackOnUnsetBlankOrMalformed(t *testing.T) {
	const name = "LENNY_ENVDEFAULT_TEST_BOOL"
	cases := []struct {
		value string
		def   bool
		want  bool
	}{
		{"", true, true},
		{"  ", false, false},
		{"false", true, false},
		{"TRUE", false, true},
		{"0", true, false},
		{"yes", true, true},
		{"yes", false, false},
	}
	for _, c := range cases {
		t.Setenv(name, c.value)
		if got := ParseBool(name, c.def); got != c.want {
			t.Errorf("ParseBool(%q, %v) = %v, want %v", c.value, c.def, got, c.want)
		}
	}
}
