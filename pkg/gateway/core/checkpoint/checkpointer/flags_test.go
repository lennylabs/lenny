// SPDX-License-Identifier: MIT

package checkpointer

import (
	"flag"
	"io"
	"testing"
)

// TestRegisterFlagsKeepsTheGatewayFlagNamesAndDefaults checks that
// RegisterFlags registers each flag under the name and with the default the
// gateway's flag surface gives it, and that --checkpoint-jitter-fraction takes
// its default from LENNY_CHECKPOINT_JITTER_FRACTION. The flag registration is
// wiring that no requirement states, so this test carries no specification
// annotation.
func TestRegisterFlagsKeepsTheGatewayFlagNamesAndDefaults(t *testing.T) {
	for _, env := range []string{
		"LENNY_CHECKPOINT_JITTER_FRACTION",
		"LENNY_CHECKPOINT_GRANT_WINDOW",
		"LENNY_CHECKPOINT_CAPABILITY_TTL_SECONDS",
	} {
		t.Setenv(env, "")
	}
	fs := flag.NewFlagSet("lenny-gateway", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	f := RegisterFlags(fs)
	for name, def := range map[string]string{
		"checkpoint-capability-ttl-seconds": "30",
		"checkpoint-interval":               "10m0s",
		"checkpoint-grant-window":           "4",
		"checkpoint-jitter-fraction":        "0.2",
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

	t.Setenv("LENNY_CHECKPOINT_JITTER_FRACTION", "0.7")
	f = RegisterFlags(flag.NewFlagSet("lenny-gateway", flag.ContinueOnError))
	if got := *f.JitterFraction; got != 0.7 {
		t.Errorf("JitterFraction environment default = %v, want 0.7", got)
	}
}
