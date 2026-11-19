// SPDX-License-Identifier: MIT

package gatewayleader

import (
	"flag"
	"io"
	"testing"
)

// The flag registration is wiring that no requirement states, so this test
// carries no specification annotation.
func TestRegisterFlagsDefaultsTheElectionOn(t *testing.T) {
	for value, want := range map[string]bool{"": true, "false": false, "not-a-bool": true} {
		t.Setenv("LENNY_GATEWAY_LEADER_ELECTION", value)
		fs := flag.NewFlagSet("lenny-gateway", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		f := RegisterFlags(fs)
		if err := fs.Parse(nil); err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if *f.Election != want {
			t.Errorf("LENNY_GATEWAY_LEADER_ELECTION=%q: Election = %v, want %v", value, *f.Election, want)
		}
	}
}
