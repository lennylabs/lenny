// SPDX-License-Identifier: MIT

package slotcounter

import (
	"flag"
	"io"
	"testing"
)

// TestRegisterFlagsKeepsTheGatewayFlagNamesAndDefaults checks that
// RegisterFlags registers each flag under the name and with the default the
// gateway's flag surface gives it, and that
// --slot-counter-postgres-fallback-max-seconds takes its default from
// LENNY_SLOT_COUNTER_POSTGRES_FALLBACK_MAX_SECONDS. The flag registration is
// wiring that no requirement states, so this test carries no specification
// annotation.
func TestRegisterFlagsKeepsTheGatewayFlagNamesAndDefaults(t *testing.T) {
	for _, env := range []string{
		"LENNY_SLOT_COUNTER_POSTGRES_FALLBACK_MAX_SECONDS",
		"LENNY_SLOT_COUNTER_RECOVERY_PROBE_INTERVAL",
	} {
		t.Setenv(env, "")
	}
	fs := flag.NewFlagSet("lenny-gateway", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	f := RegisterFlags(fs)
	for name, def := range map[string]string{
		"slot-counter-postgres-fallback-max-seconds": "60",
		"slot-counter-recovery-probe-interval":       "2s",
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

	t.Setenv("LENNY_SLOT_COUNTER_POSTGRES_FALLBACK_MAX_SECONDS", "7")
	f = RegisterFlags(flag.NewFlagSet("lenny-gateway", flag.ContinueOnError))
	if got := *f.PostgresFallbackMaxSeconds; got != 7 {
		t.Errorf("PostgresFallbackMaxSeconds environment default = %v, want 7", got)
	}
}
