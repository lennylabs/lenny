// SPDX-License-Identifier: MIT

package sessionserver

import (
	"flag"
	"io"
	"testing"
)

// TestRegisterFlagsKeepsTheGatewayFlagNamesAndDefaults checks that
// RegisterFlags registers each flag under the name and with the default the
// gateway's flag surface gives it, and that --max-concurrent-sessions-global
// takes its default from LENNY_MAX_CONCURRENT_SESSIONS_GLOBAL. The flag
// registration is wiring that no requirement states, so this test carries no
// specification annotation.
func TestRegisterFlagsKeepsTheGatewayFlagNamesAndDefaults(t *testing.T) {
	for _, env := range []string{
		"LENNY_MAX_CONCURRENT_SESSIONS_GLOBAL",
		"LENNY_MAX_CONCURRENT_SESSIONS_PER_USER",
		"LENNY_MAX_CONCURRENT_SESSIONS_PER_RUNTIME",
		"LENNY_EVAL_RATE_LIMIT_PER_SESSION_PER_MIN",
		"LENNY_EVAL_RATE_LIMIT_PER_TENANT_PER_MIN",
		"LENNY_ENV_VAR_BLOCKLIST",
		"LENNY_DEFAULT_ISOLATION_PROFILE",
		"LENNY_SESSION_ARTIFACT_RETENTION_SECONDS",
		"LENNY_PERSIST_DERIVE_FAILURE_ROWS",
		"LENNY_SUSPENDED_BACKLOG_RECHECK_SECONDS",
	} {
		t.Setenv(env, "")
	}
	fs := flag.NewFlagSet("lenny-gateway", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	f := RegisterFlags(fs)
	for name, def := range map[string]string{
		"default-isolation-profile":           "",
		"env-var-blocklist":                   "",
		"eval-rate-limit-per-session-per-min": "100",
		"eval-rate-limit-per-tenant-per-min":  "10000",
		"max-concurrent-sessions-global":      "0",
		"max-concurrent-sessions-per-runtime": "0",
		"max-concurrent-sessions-per-user":    "0",
		"persist-derive-failure-rows":         "false",
		"rate-limit-per-pool-per-min":         "0",
		"rate-limit-per-runtime-per-min":      "0",
		"session-artifact-retention-seconds":  "604800",
		"suspended-backlog-recheck-seconds":   "300",
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

	t.Setenv("LENNY_MAX_CONCURRENT_SESSIONS_GLOBAL", "7")
	f = RegisterFlags(flag.NewFlagSet("lenny-gateway", flag.ContinueOnError))
	if got := *f.MaxConcurrentSessionsGlobal; got != 7 {
		t.Errorf("MaxConcurrentSessionsGlobal environment default = %v, want 7", got)
	}
}
