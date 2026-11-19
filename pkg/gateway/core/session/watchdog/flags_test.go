// SPDX-License-Identifier: MIT

package watchdog

import (
	"flag"
	"io"
	"testing"
)

// TestRegisterFlagsKeepsTheGatewayFlagNamesAndDefaults checks that
// RegisterFlags registers each flag under the name and with the default the
// gateway's flag surface gives it, and that --max-created-state-timeout-seconds
// takes its default from LENNY_MAX_CREATED_STATE_TIMEOUT_SECONDS. The flag
// registration is wiring that no requirement states, so this test carries no
// specification annotation.
func TestRegisterFlagsKeepsTheGatewayFlagNamesAndDefaults(t *testing.T) {
	for _, env := range []string{
		"LENNY_MAX_CREATED_STATE_TIMEOUT_SECONDS",
		"LENNY_MAX_RESUMING_SECONDS",
		"LENNY_MAX_READY_STATE_TIMEOUT_SECONDS",
		"LENNY_MAX_STARTING_STATE_TIMEOUT_SECONDS",
		"LENNY_MAX_SESSION_AGE_SECONDS",
		"LENNY_MAX_AWAITING_CLIENT_ACTION_SECONDS",
		"LENNY_MAX_SUSPENDED_POD_HOLD_SECONDS",
		"LENNY_MAX_SUSPENDED_SESSION_SECONDS",
		"LENNY_MAX_CLIENT_IDLE_SECONDS",
		"LENNY_SESSION_EXPIRY_WARNING_SECONDS",
	} {
		t.Setenv(env, "")
	}
	fs := flag.NewFlagSet("lenny-gateway", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	f := RegisterFlags(fs)
	for name, def := range map[string]string{
		"max-awaiting-client-action-seconds": "900",
		"max-client-idle-seconds":            "900",
		"max-created-state-timeout-seconds":  "300",
		"max-ready-state-timeout-seconds":    "300",
		"max-resuming-seconds":               "300",
		"max-session-age-seconds":            "7200",
		"max-starting-state-timeout-seconds": "120",
		"max-suspended-pod-hold-seconds":     "900",
		"max-suspended-session-seconds":      "604800",
		"session-expiry-warning-seconds":     "300",
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

	t.Setenv("LENNY_MAX_CREATED_STATE_TIMEOUT_SECONDS", "7")
	f = RegisterFlags(flag.NewFlagSet("lenny-gateway", flag.ContinueOnError))
	if got := *f.MaxCreatedStateSeconds; got != 7 {
		t.Errorf("MaxCreatedStateSeconds environment default = %v, want 7", got)
	}
}
