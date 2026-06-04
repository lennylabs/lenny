// SPDX-License-Identifier: MIT

//go:build smoke

// Embedded Mode quick-start smoke test (§17.4 line 150, §24.19). The
// documented quick-start path is `lenny up` → `lenny session new` →
// `lenny down`. This test drives that exact sequence through the real
// cmd/lenny binary against a temporary LENNY_HOME, asserting the stack
// comes up, a session against the seeded runtime is created, and the
// stack tears down with --purge removing the state directory. It is the
// end-to-end counterpart to the Source Mode smoke test
// (source_mode_smoke_test.go), which exercises the in-process gateway
// path. F-17.4.18.
//
// The test is gated behind embedded.SkipUnlessAvailable (Linux host +
// LENNY_EMBEDDED_SMOKE opt-in) because the bring-up downloads and runs
// k3s + PostgreSQL. Session start pulls the runtime image; the
// reference catalog ships placeholder-pinned images, so an operator
// running this test sets LENNY_EMBEDDED_SMOKE_RUNTIME to a runtime
// whose image is pullable on the host (or pre-loads the chat image).
package tier4_integration_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/embedded"
)

// spec: §17.4 line 150 ("# Ready to use in < 60s"), §24.19 — exercises
// the embedded quick-start: bring the stack up, create a session,
// assert a session id, and tear the stack down.
func TestEmbeddedModeSmoke_spec_17_4_18(t *testing.T) {
	embedded.SkipUnlessAvailable(t)
	bin := embedded.Build(t)
	// A dedicated subdirectory so `lenny down --purge` can remove the
	// whole state root without racing the t.TempDir cleanup of its parent.
	home := t.TempDir() + "/lenny-home"

	// `lenny up` — first run downloads PostgreSQL + k3s, so allow the
	// §17.4 lifecycle ceiling (lifecycle.go bounds the foreground wait at
	// 6 minutes) rather than the steady-state 60s aspiration. The
	// supervisor is detached and keeps running until `lenny down`.
	start := time.Now()
	if r := embedded.Run(t, bin, home, 6*time.Minute, "up"); r.ExitCode != 0 {
		t.Fatalf("lenny up: exit %d\nstdout:\n%s\nstderr:\n%s", r.ExitCode, r.Stdout, r.Stderr)
	}
	t.Logf("lenny up: stack ready in %s", time.Since(start).Round(time.Second))
	// Tear the stack down even if a later step fails, so a failed run
	// does not leak a detached supervisor + Postgres + k3s.
	t.Cleanup(func() {
		_ = embedded.Run(t, bin, home, 90*time.Second, "down", "--purge")
	})

	// `lenny status` reports the running stack.
	if r := embedded.Run(t, bin, home, 30*time.Second, "status"); r.ExitCode != 0 {
		t.Fatalf("lenny status: exit %d\nstdout:\n%s\nstderr:\n%s", r.ExitCode, r.Stdout, r.Stderr)
	}

	// `lenny session new` against the seeded runtime. Prints the session
	// id to stdout and exits 0 on success (cmd/lenny session new).
	rt := embedded.Runtime()
	r := embedded.Run(t, bin, home, 2*time.Minute,
		"session", "new", "--runtime", rt, "--user", "alice@acme.com")
	if r.ExitCode != 0 {
		t.Fatalf("lenny session new --runtime %s: exit %d\nstdout:\n%s\nstderr:\n%s\n"+
			"(the reference catalog ships placeholder-pinned images; set %s to a runtime whose image is pullable)",
			rt, r.ExitCode, r.Stdout, r.Stderr, embedded.RuntimeEnv)
	}
	sid := strings.TrimSpace(r.Stdout)
	if sid == "" {
		t.Fatalf("lenny session new: empty session id\nstderr:\n%s", r.Stderr)
	}
	t.Logf("lenny session new: created %s", sid)

	// `lenny down --purge` tears the stack down and removes the state dir.
	if r := embedded.Run(t, bin, home, 90*time.Second, "down", "--purge"); r.ExitCode != 0 {
		t.Fatalf("lenny down --purge: exit %d\nstdout:\n%s\nstderr:\n%s", r.ExitCode, r.Stdout, r.Stderr)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("lenny down --purge: state dir %s still present (stat err: %v)", home, err)
	}
}
