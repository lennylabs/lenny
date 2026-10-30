// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
)

// spec: 10.2 (Authentication)
//
// TestCapabilityTokenTTLFlagBound_spec_10_2 pins the gateway startup gate on
// --capability-token-ttl-seconds: 119 and 3601 are fatal, and 120 and 3600
// start. The error names the flag and auth.capabilityTokenTtlSeconds.
func TestCapabilityTokenTTLFlagBound_spec_10_2(t *testing.T) {
	for _, tc := range []struct {
		seconds int
		ok      bool
	}{
		{119, false},
		{120, true},
		{900, true},
		{3600, true},
		{3601, false},
	} {
		err := validateCapabilityTokenTTLFlag(tc.seconds)
		if (err == nil) != tc.ok {
			t.Errorf("validateCapabilityTokenTTLFlag(%d) = %v, want ok=%v", tc.seconds, err, tc.ok)
			continue
		}
		if err != nil && (!strings.Contains(err.Error(), "--capability-token-ttl-seconds") ||
			!strings.Contains(err.Error(), "auth.capabilityTokenTtlSeconds")) {
			t.Errorf("error %q does not name the flag and auth.capabilityTokenTtlSeconds", err)
		}
	}

	// The gate is wired into the startup checks that log.Fatalf on error.
	b, err := os.ReadFile("startupgates.go")
	if err != nil {
		t.Fatal(err)
	}
	space := regexp.MustCompile(`\s+`)
	if want := "validateCapabilityTokenTTLFlag(*f.capabilityTokenTTL)"; !strings.Contains(space.ReplaceAllString(string(b), " "), want) {
		t.Errorf("startupgates.go: the capability token lifetime gate is not wired (%q missing)", want)
	}
}

// spec: 10.2 (Authentication)
//
// TestCapabilityTokenTTLFlagReplacesPlaygroundBearerTTL_spec_10_2 pins the
// renamed flag: --capability-token-ttl-seconds defaults to 900, reads
// LENNY_CAPABILITY_TOKEN_TTL_SECONDS, and --playground-bearer-ttl-seconds is
// undefined because no alias is kept.
func TestCapabilityTokenTTLFlagReplacesPlaygroundBearerTTL_spec_10_2(t *testing.T) {
	register := func() *gatewayFlags {
		flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
		flag.CommandLine.SetOutput(io.Discard)
		f := &gatewayFlags{}
		f.registerOpsFlags()
		return f
	}
	savedCmdLine := flag.CommandLine
	t.Cleanup(func() { flag.CommandLine = savedCmdLine })

	t.Setenv("LENNY_CAPABILITY_TOKEN_TTL_SECONDS", "")
	f := register()
	if got := *f.capabilityTokenTTL; got != 900 {
		t.Errorf("--capability-token-ttl-seconds default = %d, want 900", got)
	}
	if err := flag.CommandLine.Parse([]string{"--capability-token-ttl-seconds=1800"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := *f.capabilityTokenTTL; got != 1800 {
		t.Errorf("--capability-token-ttl-seconds = %d, want 1800", got)
	}
	err := flag.CommandLine.Parse([]string{"--playground-bearer-ttl-seconds=900"})
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Errorf("Parse(--playground-bearer-ttl-seconds) error = %v, want the undefined-flag error", err)
	}

	t.Setenv("LENNY_CAPABILITY_TOKEN_TTL_SECONDS", "600")
	if got := *register().capabilityTokenTTL; got != 600 {
		t.Errorf("LENNY_CAPABILITY_TOKEN_TTL_SECONDS=600 gave %d, want 600", got)
	}
}
