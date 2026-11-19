// SPDX-License-Identifier: MIT

package budgetstate

import (
	"flag"
	"io"
	"strings"
	"testing"
	"time"
)

// TestRegisterFlagsKeepsTheGatewayFlagNamesAndDefaults checks that
// RegisterFlags registers each flag under the name and with the default the
// gateway's flag surface gives it, and that --budget-state-sweep-interval takes
// its default from LENNY_BUDGET_STATE_SWEEP_INTERVAL. The flag registration is
// wiring that no requirement states, so this test carries no specification
// annotation.
func TestRegisterFlagsKeepsTheGatewayFlagNamesAndDefaults(t *testing.T) {
	for _, env := range []string{
		"LENNY_BUDGET_STATE_SWEEP_INTERVAL",
	} {
		t.Setenv(env, "")
	}
	fs := flag.NewFlagSet("lenny-gateway", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	f := RegisterFlags(fs)
	for name, def := range map[string]string{
		"budget-state-sweep-interval": "1m0s",
	} {
		fl := fs.Lookup(name)
		if fl == nil {
			t.Errorf("--%s is not registered", name)
			continue
		}
		if fl.DefValue != def {
			t.Errorf("--%s default = %q, want %q", name, fl.DefValue, def)
		}
	}

	t.Setenv("LENNY_BUDGET_STATE_SWEEP_INTERVAL", "7s")
	f = RegisterFlags(flag.NewFlagSet("lenny-gateway", flag.ContinueOnError))
	if got := *f.SweepInterval; got != 7*time.Second {
		t.Errorf("SweepInterval environment default = %v, want 7s", got)
	}
}

// TestFlagsValidateRefusesANonPositiveSweepInterval checks that a zero or
// negative --budget-state-sweep-interval is refused with the flag's name, and
// that a positive one is accepted.
//
// spec: §11.2
func TestFlagsValidateRefusesANonPositiveSweepInterval(t *testing.T) {
	for _, tc := range []struct {
		d  time.Duration
		ok bool
	}{
		{0, false},
		{-time.Second, false},
		{time.Minute, true},
	} {
		f := RegisterFlags(flag.NewFlagSet("lenny-gateway", flag.ContinueOnError))
		*f.SweepInterval = tc.d
		err := f.Validate()
		if tc.ok != (err == nil) {
			t.Errorf("Validate(%s) = %v, want ok=%v", tc.d, err, tc.ok)
		}
		if err != nil && !strings.Contains(err.Error(), "--budget-state-sweep-interval must be positive") {
			t.Errorf("Validate(%s) message = %q, want the flag's name", tc.d, err)
		}
	}
}
