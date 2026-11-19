// SPDX-License-Identifier: MIT

package sessioninbox

import (
	"flag"
	"io"
	"testing"
)

// TestRegisterFlagsKeepsTheGatewayFlagNamesAndDefaults checks that
// RegisterFlags registers each flag under the name and with the default the
// gateway's flag surface gives it, and that --messaging-durable-inbox takes its
// default from LENNY_MESSAGING_DURABLE_INBOX. The flag registration is wiring
// that no requirement states, so this test carries no specification annotation.
func TestRegisterFlagsKeepsTheGatewayFlagNamesAndDefaults(t *testing.T) {
	for _, env := range []string{
		"LENNY_MESSAGING_DURABLE_INBOX",
		"LENNY_MESSAGING_MAX_INBOX_SIZE",
		"LENNY_MESSAGING_MAX_DLQ_SIZE",
	} {
		t.Setenv(env, "")
	}
	fs := flag.NewFlagSet("lenny-gateway", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	f := RegisterFlags(fs)
	for name, def := range map[string]string{
		"messaging-durable-inbox":  "false",
		"messaging-max-dlq-size":   "500",
		"messaging-max-inbox-size": "500",
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

	t.Setenv("LENNY_MESSAGING_DURABLE_INBOX", "true")
	f = RegisterFlags(flag.NewFlagSet("lenny-gateway", flag.ContinueOnError))
	if got := *f.DurableInbox; got != true {
		t.Errorf("DurableInbox environment default = %v, want true", got)
	}
}
