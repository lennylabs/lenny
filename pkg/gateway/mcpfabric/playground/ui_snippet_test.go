// SPDX-License-Identifier: MIT

package playground

import (
	"os/exec"
	"testing"
)

// TestSDKSnippetGeneratorNeverEmbedsALiveCredential runs the Node
// unit test for the playground's "Copy as client SDK snippet"
// generator (uitests/app.test.js).
//
// spec: §27.9 ("The 'Copy as client SDK snippet' feature generates
// code that never includes credentials; snippets reference
// environment variables / OIDC flow only.")
//
// The generator is pure client-side JavaScript bundled directly into
// ui/app.js (no build step, no Go analogue), so this test shells out
// to Node the same way the TypeScript SDK contract tests do
// (tests/tier3_contract/sdks/typescript_client_test.go), and skips
// with a precondition reason when Node is unavailable rather than
// failing the build. The JS test itself puts a live-looking bearer
// (and a live apiKey-mode token) into the in-memory client state and
// asserts the copy-to-clipboard output for each of the Go, Python,
// and TypeScript templates never contains either credential and
// instead sources the token from an environment variable / the OIDC
// flow.
func TestSDKSnippetGeneratorNeverEmbedsALiveCredential(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("precondition: node is not installed; the playground SDK-snippet unit test needs the Node toolchain")
	}

	// The JS test lives in uitests/ rather than in ui/ because the
	// gateway embeds the ui/ subtree wholesale and serves every file
	// under it at GET /playground/<path> (spec: §27.2, §27.7).
	cmd := exec.Command("node", "--test", "app.test.js")
	cmd.Dir = "uitests"
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node --test uitests/app.test.js failed: %v\n%s", err, out)
	}
}
