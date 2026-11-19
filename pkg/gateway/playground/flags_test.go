// SPDX-License-Identifier: MIT

package playground

import (
	"flag"
	"io"
	"reflect"
	"testing"
	"time"
)

func newFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("lenny-gateway", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// spec: §27.2 (Placement and gating)
func TestRegisterFlagsDefaultsMatchThePlaygroundHelmValues(t *testing.T) {
	for _, env := range []string{
		"LENNY_PLAYGROUND_ENABLED", "LENNY_PLAYGROUND_AUTH_MODE", "LENNY_PLAYGROUND_DEV_TENANT_ID",
		"LENNY_PLAYGROUND_ALLOWED_RUNTIMES", "LENNY_PLAYGROUND_MAX_SESSION_MINUTES",
		"LENNY_PLAYGROUND_MAX_IDLE_TIME_SECONDS", "LENNY_PLAYGROUND_OIDC_SESSION_TTL_SECONDS",
		"LENNY_PLAYGROUND_GATEWAY_HOST", "LENNY_PLAYGROUND_SESSION_LABELS",
	} {
		t.Setenv(env, "")
	}
	fs := newFlagSet()
	f := RegisterFlags(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if *f.Enabled {
		t.Error("playground-enabled defaults to true, want false")
	}
	cfg := f.Config(15*time.Minute, true)
	want := Config{
		Enabled:            true,
		AuthMode:           AuthModeOIDC,
		DevTenantID:        "default",
		AllowedRuntimes:    []string{"*"},
		MaxSessionMinutes:  30,
		MaxIdleTimeSeconds: 300,
		OIDCSessionTTL:     time.Hour,
		CapabilityTokenTTL: 15 * time.Minute,
		MultiTenant:        true,
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("Config() = %+v, want %+v", cfg, want)
	}
}

// spec: §27.2 (Placement and gating)
func TestRegisterFlagsReadTheEnvironmentAndTheCommandLine(t *testing.T) {
	t.Setenv("LENNY_PLAYGROUND_ENABLED", "yes")
	t.Setenv("LENNY_PLAYGROUND_AUTH_MODE", "apiKey")
	t.Setenv("LENNY_PLAYGROUND_MAX_SESSION_MINUTES", "not-a-number")
	t.Setenv("LENNY_PLAYGROUND_SESSION_LABELS", "origin=playground, env = stage,broken,=x,")
	fs := newFlagSet()
	f := RegisterFlags(fs)
	if err := fs.Parse([]string{
		"--playground-allowed-runtimes=claude-*, ,codex",
		"--playground-oidc-session-ttl-seconds=120",
		"--playground-gateway-host=gw.acme.com",
	}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !*f.Enabled {
		t.Error("LENNY_PLAYGROUND_ENABLED=yes left the playground disabled")
	}
	cfg := f.Config(time.Minute, false)
	if cfg.AuthMode != AuthModeAPIKey {
		t.Errorf("AuthMode = %q, want apiKey from the environment", cfg.AuthMode)
	}
	if cfg.MaxSessionMinutes != 30 {
		t.Errorf("MaxSessionMinutes = %d, want the default 30 for a malformed variable", cfg.MaxSessionMinutes)
	}
	if !reflect.DeepEqual(cfg.AllowedRuntimes, []string{"claude-*", "codex"}) {
		t.Errorf("AllowedRuntimes = %q, want the trimmed non-blank entries", cfg.AllowedRuntimes)
	}
	if cfg.OIDCSessionTTL != 2*time.Minute {
		t.Errorf("OIDCSessionTTL = %v, want 2m", cfg.OIDCSessionTTL)
	}
	if cfg.GatewayHost != "gw.acme.com" {
		t.Errorf("GatewayHost = %q", cfg.GatewayHost)
	}
	if want := map[string]string{"origin": "playground", "env": "stage"}; !reflect.DeepEqual(cfg.SessionLabels, want) {
		t.Errorf("SessionLabels = %v, want %v", cfg.SessionLabels, want)
	}
}

func TestParseKeyValueListYieldsNilForAnEmptyValue(t *testing.T) {
	if got := parseKeyValueList(""); got != nil {
		t.Errorf("parseKeyValueList(\"\") = %v, want nil", got)
	}
	if got := parseKeyValueList(" , noequals ,=v"); got != nil {
		t.Errorf("parseKeyValueList with no usable entry = %v, want nil", got)
	}
}
