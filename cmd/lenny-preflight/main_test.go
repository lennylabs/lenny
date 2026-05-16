// SPDX-License-Identifier: MIT

package main

import "testing"

func TestParseAcceptDowngradeCollectsFlags(t *testing.T) {
	got := parseAcceptDowngrade("llmProxy, compliance")
	if len(got) != 2 {
		t.Fatalf("parseAcceptDowngrade = %v, want exactly 2 entries", got)
	}
	if !got["llmProxy"] || !got["compliance"] {
		t.Errorf("parseAcceptDowngrade = %v, want llmProxy and compliance", got)
	}
}

func TestParseAcceptDowngradeEmpty(t *testing.T) {
	if got := parseAcceptDowngrade(""); len(got) != 0 {
		t.Errorf("parseAcceptDowngrade(\"\") = %v, want an empty set", got)
	}
}

func TestParseAcceptDowngradeTrimsAndSkipsBlanks(t *testing.T) {
	got := parseAcceptDowngrade(" drainReadiness , , ")
	if len(got) != 1 || !got["drainReadiness"] {
		t.Errorf("parseAcceptDowngrade = %v, want only drainReadiness", got)
	}
}
