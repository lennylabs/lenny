// SPDX-License-Identifier: MIT

package localcli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// spec: §24.19 line 264 — `lenny restart` requires a component argument.
func TestCmdRestartRequiresComponent_spec_24_19_264(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cmdRestart(context.Background(), nil, &stdout, &stderr)
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "component") {
		t.Errorf("stderr = %q, want a required-argument message", stderr.String())
	}
}

// spec: §24.19 line 264 — only the gateway and controller are
// individually restartable; other names are rejected before any signal.
func TestCmdRestartRejectsUnknownComponent_spec_24_19_264(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cmdRestart(context.Background(), []string{"redis"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "cannot be restarted individually") {
		t.Errorf("stderr = %q, want a rejection naming the restartable set", stderr.String())
	}
}

func TestCmdRestartNoStack(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := cmdRestart(context.Background(), []string{"gateway"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "no embedded stack is running") {
		t.Errorf("stderr = %q, want a no-stack message", stderr.String())
	}
}
