// SPDX-License-Identifier: MIT

package envdefault

import (
	"testing"
	"time"
)

// The helpers are flag plumbing that no requirement states, so these tests
// carry no specification annotation.

func TestIntReadsTheVariableAndFallsBackOnUnsetBlankOrMalformed(t *testing.T) {
	const name = "LENNY_ENVDEFAULT_TEST_INT"
	cases := []struct {
		value string
		set   bool
		want  int
	}{
		{set: false, want: 7},
		{value: "", set: true, want: 7},
		{value: "   ", set: true, want: 7},
		{value: "12", set: true, want: 12},
		{value: " 12 ", set: true, want: 12},
		{value: "-3", set: true, want: -3},
		{value: "twelve", set: true, want: 7},
		{value: "1.5", set: true, want: 7},
	}
	for _, c := range cases {
		if c.set {
			t.Setenv(name, c.value)
		}
		if got := Int(name, 7); got != c.want {
			t.Errorf("Int with %q (set %v) = %d, want %d", c.value, c.set, got, c.want)
		}
	}
}

func TestDurationReadsTheVariableAndFallsBackOnUnsetBlankOrMalformed(t *testing.T) {
	const name = "LENNY_ENVDEFAULT_TEST_DURATION"
	def := 5 * time.Second
	if got := Duration(name, def); got != def {
		t.Errorf("Duration unset = %v, want %v", got, def)
	}
	cases := []struct {
		value string
		want  time.Duration
	}{
		{"", def},
		{"  ", def},
		{"90s", 90 * time.Second},
		{" 2m ", 2 * time.Minute},
		{"10", def},
		{"soon", def},
	}
	for _, c := range cases {
		t.Setenv(name, c.value)
		if got := Duration(name, def); got != c.want {
			t.Errorf("Duration with %q = %v, want %v", c.value, got, c.want)
		}
	}
}
