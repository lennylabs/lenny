// SPDX-License-Identifier: MIT

package localcli

import (
	"bytes"
	"strings"
	"testing"
)

// spec: §22.7 (time-to-hello-world: lenny session new entry point)
// diagnosis: the quick-start documents `lenny session new --runtime
// <name>` as the entry point for starting a session against the
// embedded gateway. The CLI dispatch must wire `session` so an
// unknown-command error does not greet the operator on step 3 of the
// TTHW flow.
func TestSessionSubcommandWired(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cmdSession(nil, &stdout, &stderr)
	if code != 2 {
		t.Errorf("no-argument session: exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "lenny session new --runtime") {
		t.Errorf("usage text is missing: %q", stderr.String())
	}
}

func TestSessionNewRequiresRuntime(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cmdSession([]string{"new"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("session new with no runtime: exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--runtime") {
		t.Errorf("expected --runtime hint, got: %q", stderr.String())
	}
}

func TestSessionUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cmdSession([]string{"frobnicate"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("unknown session subcommand: exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown subcommand") {
		t.Errorf("expected unknown-subcommand diagnosis, got: %q", stderr.String())
	}
}
