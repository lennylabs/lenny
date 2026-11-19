// SPDX-License-Identifier: MIT

package leaderjobs

import (
	"flag"
	"io"
	"testing"
)

// TestRegisterFlagsKeepsTheGatewayFlagNamesAndDefaults checks that
// RegisterFlags registers each flag under the name and with the default the
// gateway's flag surface gives it, and that --t4-kms-probe-interval-seconds
// takes its default from LENNY_T4_KMS_PROBE_INTERVAL_SECONDS. The flag
// registration is wiring that no requirement states, so this test carries no
// specification annotation.
func TestRegisterFlagsKeepsTheGatewayFlagNamesAndDefaults(t *testing.T) {
	for _, env := range []string{
		"LENNY_T4_KMS_PROBE_INTERVAL_SECONDS",
		"LENNY_T4_KMS_PROBE_RATE_LIMIT",
	} {
		t.Setenv(env, "")
	}
	fs := flag.NewFlagSet("lenny-gateway", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	f := RegisterFlags(fs)
	for name, def := range map[string]string{
		"t4-kms-probe-interval-seconds": "300",
		"t4-kms-probe-rate-limit":       "10",
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

	t.Setenv("LENNY_T4_KMS_PROBE_INTERVAL_SECONDS", "7")
	f = RegisterFlags(flag.NewFlagSet("lenny-gateway", flag.ContinueOnError))
	if got := *f.T4KMSProbeIntervalSeconds; got != 7 {
		t.Errorf("T4KMSProbeIntervalSeconds environment default = %v, want 7", got)
	}
}
