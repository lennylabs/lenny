// SPDX-License-Identifier: MIT

package playground

import (
	"os/exec"
	"testing"
)

// TestPlaygroundUIWalksThreeScreensAndExercisesChatControls runs the
// Node unit test that renders the playground SPA's three screens
// (ui/screens.test.js).
//
// spec: §27.4 ("The playground ships as a single-page React app with
// three screens: 1. Runtime picker ... 2. Session configuration ...
// 3. Chat. A single-session chat pane backed by the MCP WebSocket.
// Renders messages, tool-call events, delegation events, and errors.
// Includes an Interrupt button, a Cancel button, a raw-frame inspector
// (expandable panel that shows the exact MCP frames for debugging),
// and a 'Copy as client SDK snippet' button that emits equivalent code
// in Go/Python/TS.")
//
// The SPA is plain client-side JavaScript bundled directly into
// ui/app.js with no build step and no Go analogue (see app.js's header
// comment), so this test shells out to Node the same way
// TestSDKSnippetGeneratorNeverEmbedsALiveCredential already does for
// the SDK-snippet generator, and skips with a precondition reason when
// Node is unavailable rather than failing the build. The JS test
// renders the runtime picker, session configuration, and chat screens
// through the real app.js render functions against a minimal in-memory
// DOM/fetch/WebSocket shim (no browser or jsdom dependency), walks
// picker -> config -> chat by dispatching the same click events a
// browser would, sends a chat message, and asserts the Interrupt and
// Cancel buttons issue lenny/interrupt_session and lenny/cancel_session
// over the MCP WebSocket and that the raw-frame inspector renders the
// frames sent and received.
func TestPlaygroundUIWalksThreeScreensAndExercisesChatControls(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("precondition: node is not installed; the playground UI screen-walk test needs the Node toolchain")
	}

	cmd := exec.Command("node", "--test", "screens.test.js")
	cmd.Dir = "ui"
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node --test ui/screens.test.js failed: %v\n%s", err, out)
	}
}
