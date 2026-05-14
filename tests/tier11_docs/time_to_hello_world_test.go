// SPDX-License-Identifier: MIT

package tier11_docs

import (
	"testing"
)

// The §16.8 time-to-hello-world obligation: a fresh operator can go
// from `brew install` to a working `lenny session start` in under
// five minutes of total wall-clock. TESTING.md §22.7 makes the
// five-minute budget a release gate; §13.x phase 17b ships the
// supporting load-tier benchmark at tests/tier7_load/scenarios/tthw.go.
//
// The four sub-steps below pin the obligation surface. Each step
// stays skip-bearing until its prerequisite binary ships:
//   - brew install requires a published tap and the lenny-ctl binary
//   - lenny up requires cmd/lenny-ctl with the bundled compose pack
//   - lenny session start requires the gateway + chat runtime
//   - the cleanup step requires the session-end protocol
//
// When all four binaries exist, the sub-tests grow real assertions
// (start a wall-clock timer at step 1, fail at step 4 if elapsed
// > 5 minutes).

// spec: 22.7 (documentation gate — five-minute time-to-hello-world)
// diagnosis: A regression in the install/up/session/cleanup chain
//
//	pushes a new operator past the five-minute budget.
func TestTimeToHelloWorld(t *testing.T) {
	t.Parallel()
	t.Run("step_1_brew_install", func(t *testing.T) {
		t.Skip("not implemented: brew tap + lenny-ctl binary are §17.6 deliverables")
	})
	t.Run("step_2_lenny_up", func(t *testing.T) {
		t.Skip("not implemented: cmd/lenny-ctl up is a §24.19 deliverable")
	})
	t.Run("step_3_lenny_session_start", func(t *testing.T) {
		t.Skip("not implemented: gateway + chat runtime are §4.1 / §26.7 deliverables")
	})
	t.Run("step_4_session_cleanup_within_5min", func(t *testing.T) {
		t.Skip("not implemented: end-to-end wall-clock budget asserts once steps 1-3 land")
	})
}

// spec: 22.7 (documentation gate — runtime-author quick-start)
// diagnosis: The runtime-author onramp (lenny runtime init / make
//
//	image / lenny runtime publish) drifts past its five-minute
//	budget.
func TestRuntimeAuthorQuickStart(t *testing.T) {
	t.Parallel()
	t.Run("init_scaffold", func(t *testing.T) {
		t.Skip("not implemented: cmd/lenny-ctl runtime init is a §24.18 deliverable")
	})
	t.Run("make_image", func(t *testing.T) {
		t.Skip("not implemented: scaffold Makefile + base image are §15.7 deliverables")
	})
	t.Run("publish_to_local_registry", func(t *testing.T) {
		t.Skip("not implemented: cmd/lenny-ctl runtime publish is a §24.18 deliverable")
	})
	t.Run("session_against_new_runtime", func(t *testing.T) {
		t.Skip("not implemented: gateway dispatch against just-published runtime is §15.4 / §24.18")
	})
}

// spec: 22.7 (documentation gate — non-interactive operator install)
// diagnosis: lenny-ctl install --non-interactive with the documented
//
//	answers file fails to produce a working install.
func TestOperatorInstallNonInteractive(t *testing.T) {
	t.Parallel()
	t.Run("install_with_answers_file", func(t *testing.T) {
		t.Skip("not implemented: cmd/lenny-ctl install --non-interactive is §24.20 / §17.9")
	})
	t.Run("smoke_session_after_install", func(t *testing.T) {
		t.Skip("not implemented: smoke session asserts that the install produced a reachable gateway")
	})
}
