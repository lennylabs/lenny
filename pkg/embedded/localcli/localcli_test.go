// SPDX-License-Identifier: MIT

package localcli

import (
	"bytes"
	"strings"
	"testing"
)

const testVersion = "test-version"

func TestMainNoArgsPrintsUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Main(nil, &stdout, &stderr, testVersion)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "Embedded Mode") {
		t.Errorf("stderr = %q, want the usage text", stderr.String())
	}
}

func TestMainHelp(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		var stdout, stderr bytes.Buffer
		code := Main([]string{arg}, &stdout, &stderr, testVersion)
		if code != 0 {
			t.Errorf("%s: exit code = %d, want 0", arg, code)
		}
		if !strings.Contains(stdout.String(), "lenny up") {
			t.Errorf("%s: stdout = %q, want the usage text", arg, stdout.String())
		}
	}
}

// spec: §24.0 line 23, §17.6 line 360 — the embedded binary prints its
// local build version offline. Confirms the -X main.version ldflag has a
// symbol to bind to (F-24.0.4).
func TestMainVersion(t *testing.T) {
	for _, arg := range []string{"version", "--version", "-v"} {
		var stdout, stderr bytes.Buffer
		code := Main([]string{arg}, &stdout, &stderr, testVersion)
		if code != 0 {
			t.Errorf("%s: exit code = %d, want 0", arg, code)
		}
		out := stdout.String()
		if !strings.HasPrefix(out, "lenny ") || !strings.Contains(out, testVersion) {
			t.Errorf("%s: stdout = %q, want 'lenny %s'", arg, out, testVersion)
		}
	}
}

func TestMainUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Main([]string{"frobnicate"}, &stdout, &stderr, testVersion)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Errorf("stderr = %q, want an unknown-command error", stderr.String())
	}
}

// spec: §24.19 line 266 — Local enumerates the Embedded Mode commands
// that lenny-ctl delegates to the local stack so it behaves identically.
func TestLocalEnumeratesEmbeddedCommands(t *testing.T) {
	for _, name := range []string{"up", "down", "status", "logs", "restart", "token", "image", "session"} {
		if !Local(name) {
			t.Errorf("Local(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"admin", "bootstrap", "install", "health", "version", "frobnicate"} {
		if Local(name) {
			t.Errorf("Local(%q) = true, want false", name)
		}
	}
}

func TestRunStatusNoStack(t *testing.T) {
	// With LENNY_HOME pointed at an empty directory, status reports no
	// running stack and exits 0.
	t.Setenv("LENNY_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := Main([]string{"status"}, &stdout, &stderr, testVersion)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "no embedded stack is running") {
		t.Errorf("stdout = %q, want a no-stack message", stdout.String())
	}
}

func TestRunStatusJSON(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := Main([]string{"status", "--json"}, &stdout, &stderr, testVersion)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), `"running"`) {
		t.Errorf("stdout = %q, want JSON output", stdout.String())
	}
}

func TestRunDownNoStack(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := Main([]string{"down"}, &stdout, &stderr, testVersion)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

func TestRunLogsNoStack(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := Main([]string{"logs"}, &stdout, &stderr, testVersion)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

// spec: §24.9 line 120 — token print exits 3 EMBEDDED_MODE_REQUIRED when
// invoked outside Embedded Mode (no `lenny up` has written the persisted
// signing key). (F-24.9.2)
func TestRunTokenWithoutStack_spec_24_9_120(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := Main([]string{"token", "print"}, &stdout, &stderr, testVersion)
	if code != exitEmbeddedModeRequired {
		t.Errorf("exit code = %d, want %d (EMBEDDED_MODE_REQUIRED)", code, exitEmbeddedModeRequired)
	}
	if !strings.Contains(stderr.String(), "lenny up") {
		t.Errorf("stderr = %q, want guidance to run lenny up", stderr.String())
	}
	if !strings.Contains(stderr.String(), "EMBEDDED_MODE_REQUIRED") {
		t.Errorf("stderr = %q, want the EMBEDDED_MODE_REQUIRED marker", stderr.String())
	}
}

func TestRunTokenRejectsBadSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Main([]string{"token", "mint"}, &stdout, &stderr, testVersion)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

func TestRunTokenPrintAfterKeyWritten(t *testing.T) {
	// Seed an OIDC key the way lenny up would, then confirm token
	// print emits a three-segment JWT.
	home := t.TempDir()
	t.Setenv("LENNY_HOME", home)
	seedOIDCKey(t, home)
	var stdout, stderr bytes.Buffer
	code := Main([]string{"token", "print"}, &stdout, &stderr, testVersion)
	if code != 0 {
		t.Fatalf("exit code = %d (stderr %q), want 0", code, stderr.String())
	}
	tok := strings.TrimSpace(stdout.String())
	if n := strings.Count(tok, "."); n != 2 {
		t.Errorf("token %q has %d dots, want 2 (a compact JWT)", tok, n)
	}
}

func TestParsePortFlags(t *testing.T) {
	httpPort, httpsPort := parsePortFlags([]string{"--http-port", "9090", "--https-port", "9443"})
	if httpPort != 9090 || httpsPort != 9443 {
		t.Errorf("parsePortFlags = (%d, %d), want (9090, 9443)", httpPort, httpsPort)
	}
	// Absent flags yield zero so the stack applies the §17.4 defaults.
	h, s := parsePortFlags(nil)
	if h != 0 || s != 0 {
		t.Errorf("parsePortFlags(nil) = (%d, %d), want (0, 0)", h, s)
	}
}

func TestParseTTL(t *testing.T) {
	if _, err := parseTTL("30m"); err != nil {
		t.Errorf("parseTTL(30m): %v", err)
	}
	if _, err := parseTTL("0s"); err == nil {
		t.Error("parseTTL(0s) should reject a non-positive duration")
	}
	if _, err := parseTTL("garbage"); err == nil {
		t.Error("parseTTL(garbage) should reject an unparseable duration")
	}
}
