// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestTerminalWriterSeamsAreWiredToTheSessionServer_spec_7_2 pins the
// production wiring of every terminal writer's seam to the session server's
// terminal claim and post-terminal hook: the MCP tools' Terminal, the
// watchdog's terminal hook, the orphan-cleanup sweep's and the orphan-session
// reconciler's Terminal, the deadlock fail closure's claim, ForceTerminate's
// and the budget terminator's claim, and the full_revoke write's revoke hook.
// Every other test injects a recorder at these seams, so a wiring that skips
// the hook in production is caught only here.
//
// spec: §7.2 (Interactive Session Model), §11.4 (User Invalidation)
func TestTerminalWriterSeamsAreWiredToTheSessionServer_spec_7_2(t *testing.T) {
	space := regexp.MustCompile(`\s+`)
	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return space.ReplaceAllString(string(b), " ")
	}
	for _, tc := range []struct {
		file, want, seam string
	}{
		{"mcpsurface.go", "Terminal: sessionSrv,", "the MCP tools' Terminal"},
		{"controlserver.go", "WithTerminalHook(sessionSrv)", "the watchdog's terminal hook"},
		{"adminrouter.go", "WithRevokeHook(sessionSrv)", "the full_revoke revoke hook"},
		{"adminrouter.go", "beginTerminal: sessionSrv.BeginTerminal", "ForceTerminate's claim"},
		{"adminrouter.go", "onTerminal: sessionSrv.OnSessionTerminal", "ForceTerminate's funnel"},
		{"main.go", "w.budgetTerminator.beginTerminal = sessionSrv.BeginTerminal", "the budget terminator's claim"},
		{"reconciler_workers.go", "end := w.sessionSrv.BeginTerminal(sessionID)", "the deadlock closure's claim"},
	} {
		if !strings.Contains(read(tc.file), tc.want) {
			t.Errorf("%s: %s is not wired (%q missing)", tc.file, tc.seam, tc.want)
		}
	}
	if n := strings.Count(read("reconciler_workers.go"), "Terminal: w.sessionSrv,"); n != 2 {
		t.Errorf("reconciler_workers.go wires Terminal to the session server %d times, want 2 (orphan cleanup and orphan session)", n)
	}
}
