// SPDX-License-Identifier: MIT

package slotreaper

import (
	"flag"
	"io"
	"testing"
	"time"
)

// TestRegisterFlagsKeepsTheGatewayFlagNamesAndDefaults checks that
// RegisterFlags registers each flag under the name and with the default the
// gateway's flag surface gives it, and that --slot-reaper-interval takes its
// default from LENNY_SLOT_REAPER_INTERVAL. The flag registration is wiring that
// no requirement states, so this test carries no specification annotation.
func TestRegisterFlagsKeepsTheGatewayFlagNamesAndDefaults(t *testing.T) {
	for _, env := range []string{
		"LENNY_SLOT_REAPER_INTERVAL",
		"LENNY_SLOT_REAPER_RATE",
		"LENNY_SLOT_REAPER_TERMINAL_AGE",
	} {
		t.Setenv(env, "")
	}
	fs := flag.NewFlagSet("lenny-gateway", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	f := RegisterFlags(fs)
	for name, def := range map[string]string{
		"slot-reaper-interval":     "1m0s",
		"slot-reaper-rate":         "50",
		"slot-reaper-terminal-age": "15m0s",
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

	t.Setenv("LENNY_SLOT_REAPER_INTERVAL", "7s")
	f = RegisterFlags(flag.NewFlagSet("lenny-gateway", flag.ContinueOnError))
	if got := *f.Interval; got != 7*time.Second {
		t.Errorf("Interval environment default = %v, want 7s", got)
	}
}
