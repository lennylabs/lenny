// SPDX-License-Identifier: MIT

package ctlcli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestRunIsTheUnifiedEntryForBothNames asserts that ctlcli.Run — the single
// dispatcher both cmd/lenny and cmd/lenny-ctl invoke — recognizes the full
// operator command surface. Because both binary names call this same Run,
// the short-name `lenny` binary now supports every subcommand, satisfying
// the §24 preamble line 17 contract ("Both names support every subcommand").
// Before F-24.0.1 the short name reached only the Embedded Mode local
// commands and rejected `lenny bootstrap` / `lenny admin ...` as unknown.
func TestRunIsTheUnifiedEntryForBothNames_spec_24_0(t *testing.T) {
	clearCLIEnv(t)
	restoreVersion(t)
	// Operator commands that dispatch to a sub-handler. With an unroutable
	// gateway/ops endpoint and a 1s timeout, any network call fails fast; the
	// assertion is only that the top-level dispatcher recognizes the command
	// (it must not fall through to the "unknown command" default branch).
	for _, cmd := range []string{
		"health", "bootstrap", "admin", "drift", "backup", "restore",
		"runbooks", "locks", "escalations", "diagnose", "operations",
		"me", "audit", "policy", "slo", "values", "upgrade", "doctor",
		"events", "mcp-management",
	} {
		var stdout, stderr bytes.Buffer
		Run([]string{
			"--api-url", "http://127.0.0.1:0",
			"--ops-server", "http://127.0.0.1:0",
			"--timeout", "1", cmd,
		}, &stdout, &stderr, "test-ver")
		combined := stdout.String() + stderr.String()
		if strings.Contains(combined, `unknown command "`+cmd+`"`) {
			t.Errorf("Run rejected operator command %q as unknown; the unified surface must recognize it under both names", cmd)
		}
	}
	// A genuinely bogus command must still hit the default branch, proving the
	// dispatcher is not blindly accepting everything.
	var stdout, stderr bytes.Buffer
	Run([]string{"definitely-not-a-command"}, &stdout, &stderr, "test-ver")
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Errorf("Run accepted a bogus command; stderr=%q", stderr.String())
	}
}

// TestProgNameTracksInvocationName verifies the version banner names whichever
// of the three invocation names (lenny, lenny-ctl, kubectl-lenny) ran, built
// from one binary. spec: §24 preamble line 17, §17.6 line 358.
func TestProgNameTracksInvocationName_spec_24_0(t *testing.T) {
	orig := os.Args[0]
	t.Cleanup(func() { os.Args[0] = orig })
	for argv0, want := range map[string]string{
		"/usr/local/bin/lenny":      "lenny",
		"/usr/local/bin/lenny-ctl":  "lenny-ctl",
		"kubectl-lenny":             "kubectl-lenny",
		"/opt/bin/kubectl-lenny.v1": "kubectl-lenny",
		"ctlcli.test":               "lenny-ctl", // go test binary falls through
	} {
		os.Args[0] = argv0
		if got := progName(); got != want {
			t.Errorf("progName() with argv0=%q = %q, want %q", argv0, got, want)
		}
	}
}

// TestRunPropagatesStampedVersion asserts the build version each thin shim
// stamps via its -X main.version ldflag flows through Run to the version
// command. spec: §24.0 line 23, §17.6 line 360.
func TestRunPropagatesStampedVersion_spec_24_0(t *testing.T) {
	clearCLIEnv(t)
	restoreVersion(t)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"version"}, &stdout, &stderr, "v9.9.9")
	if code != 0 {
		t.Fatalf("version exit %d, stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "v9.9.9") {
		t.Errorf("version output %q missing the stamped version", stdout.String())
	}
}

// restoreVersion saves and restores the package-level version var so a test
// that calls Run (which sets it) does not leak the value into sibling tests
// that read it (e.g. TestVersionCommandIsOfflineLocal).
func restoreVersion(t *testing.T) {
	t.Helper()
	orig := version
	t.Cleanup(func() { version = orig })
}
