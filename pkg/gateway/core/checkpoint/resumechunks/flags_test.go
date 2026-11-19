// SPDX-License-Identifier: MIT

package resumechunks

import (
	"flag"
	"io"
	"testing"
)

// TestRegisterFlagsKeepsTheGatewayFlagNamesAndDefaults checks that
// RegisterFlags registers each flag under the name and with the default the
// gateway's flag surface gives it, and that
// --partial-recovery-threshold-fraction takes its default from
// LENNY_PARTIAL_RECOVERY_THRESHOLD_FRACTION. The flag registration is wiring
// that no requirement states, so this test carries no specification annotation.
func TestRegisterFlagsKeepsTheGatewayFlagNamesAndDefaults(t *testing.T) {
	for _, env := range []string{
		"LENNY_PARTIAL_RECOVERY_THRESHOLD_FRACTION",
	} {
		t.Setenv(env, "")
	}
	fs := flag.NewFlagSet("lenny-gateway", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	f := RegisterFlags(fs)
	for name, def := range map[string]string{
		"partial-recovery-threshold-fraction": "0.5",
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

	t.Setenv("LENNY_PARTIAL_RECOVERY_THRESHOLD_FRACTION", "0.7")
	f = RegisterFlags(flag.NewFlagSet("lenny-gateway", flag.ContinueOnError))
	if got := *f.PartialRecoveryThresholdFraction; got != 0.7 {
		t.Errorf("PartialRecoveryThresholdFraction environment default = %v, want 0.7", got)
	}
}
