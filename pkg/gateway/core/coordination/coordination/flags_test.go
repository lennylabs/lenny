// SPDX-License-Identifier: MIT

package coordination

import (
	"flag"
	"io"
	"testing"
	"time"
)

// TestRegisterFlagsKeepsTheGatewayFlagNamesAndDefaults checks that
// RegisterFlags registers each flag under the name and with the default the
// gateway's flag surface gives it, and that a value given on the command line
// reaches --coordination-interval. The flag registration is wiring that no
// requirement states, so this test carries no specification annotation.
func TestRegisterFlagsKeepsTheGatewayFlagNamesAndDefaults(t *testing.T) {
	fs := flag.NewFlagSet("lenny-gateway", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	f := RegisterFlags(fs)
	for name, def := range map[string]string{
		"coordination-interval": "15s",
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

	if err := fs.Parse([]string{"-coordination-interval=7s"}); err != nil {
		t.Fatal(err)
	}
	if got := *f.Interval; got != 7*time.Second {
		t.Errorf("Interval command line = %v, want 7s", got)
	}
}
